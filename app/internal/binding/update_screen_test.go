package binding

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/updater"
)

// fakeApplier はテスト用の取得・適用の担い手。
type fakeApplier struct {
	// onDownload / onApply は各段で呼ばれる（状態を観測するために使う）。
	onDownload func() error
	onApply    func() error
	downloads  int
	applies    int
	cleanups   int
}

func (f *fakeApplier) Download(ctx context.Context, asset updater.Asset) (string, error) {
	f.downloads++
	if f.onDownload != nil {
		if err := f.onDownload(); err != nil {
			return "", err
		}
	}
	return "/tmp/staged.zip", nil
}

func (f *fakeApplier) Apply(archivePath, version string) (updater.Applied, error) {
	f.applies++
	if f.onApply != nil {
		if err := f.onApply(); err != nil {
			return updater.Applied{}, err
		}
	}
	return updater.Applied{Version: version, RestartRequired: true}, nil
}

func (f *fakeApplier) Cleanup() error { f.cleanups++; return nil }

// useApplier は API へテスト用の担い手を差し込む。
func useApplier(a *API, f *fakeApplier) {
	a.update.newApplierFn = func() (updateApplier, error) { return f, nil }
}

func availableResult(version string) updater.Result {
	return updater.Result{
		Available: true,
		Version:   version,
		Asset: updater.Asset{OS: "darwin", Arch: updater.ArchUniversal,
			URL: "https://example.invalid/x.zip", SHA256: strings.Repeat("a", 64), Size: 1},
	}
}

// 新版検知 → 同意 → 取得 → 検証 → 再起動待ち の状態遷移。
// 自動更新: 新版の存在が通知され、利用者の承認操作を経て更新が完了すること。
func TestUpdateFlowStates(t *testing.T) {
	a := New()
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult("0.2.0"), nil
	}
	// 各段で「そのとき画面へ返る状態」を記録する。
	var seen []string
	f := &fakeApplier{}
	f.onDownload = func() error { seen = append(seen, a.UpdateState().Status); return nil }
	f.onApply = func() error { seen = append(seen, a.UpdateState().Status); return nil }
	useApplier(a, f)

	if got := a.UpdateState(); got.Status != UpdateIdle {
		t.Fatalf("初期状態 = %q, want %q", got.Status, UpdateIdle)
	}
	st := a.CheckForUpdate()
	if st.Status != UpdateAvailable || st.NewVersion != "0.2.0" {
		t.Fatalf("確認後 = %+v, want available/0.2.0", st)
	}
	if st.CurrentVersion != Version {
		t.Fatalf("現行版 = %q, want %q", st.CurrentVersion, Version)
	}

	st = a.ApplyUpdate()
	if st.Status != UpdateReady {
		t.Fatalf("適用後 = %+v, want %q", st, UpdateReady)
	}
	want := []string{UpdateDownloading, UpdateVerifying}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("進行状態 = %v, want %v", seen, want)
	}
	if f.cleanups != 0 {
		t.Fatalf("成功したのに後始末が %d 回走った", f.cleanups)
	}
}

// 取得・適用のどちらで失敗しても、作業場所を片づけて失敗状態になること。
func TestApplyCleansUpOnFailure(t *testing.T) {
	for name, f := range map[string]*fakeApplier{
		"取得で失敗": {onDownload: func() error { return updater.NewErrorForTest(updater.KindNetwork) }},
		"適用で失敗": {onApply: func() error { return updater.NewErrorForTest(updater.KindNotUserArea) }},
	} {
		a := New()
		a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
			return availableResult("0.2.0"), nil
		}
		useApplier(a, f)
		a.CheckForUpdate()
		st := a.ApplyUpdate()
		if st.Status != UpdateFailed {
			t.Errorf("%s: 状態 = %q, want %q", name, st.Status, UpdateFailed)
		}
		if f.cleanups != 1 {
			t.Errorf("%s: 後始末が %d 回, want 1", name, f.cleanups)
		}
		if st.Message == "" {
			t.Errorf("%s: 文言が空", name)
		}
	}
}

// 受け入れ条件: 同意操作の前に取得・適用が始まらないこと。
func TestApplyDoesNotStartBeforeConsent(t *testing.T) {
	a := New()
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult("0.2.0"), nil
	}
	f := &fakeApplier{}
	useApplier(a, f)

	// 確認だけでは取得は始まらない。
	a.CheckForUpdate()
	if f.downloads != 0 {
		t.Fatalf("同意前に取得が %d 回始まった", f.downloads)
	}
	// 同意（ApplyUpdate）で初めて 1 回だけ始まる。
	a.ApplyUpdate()
	if f.downloads != 1 || f.applies != 1 {
		t.Fatalf("同意後の取得 %d 回 / 適用 %d 回, want 1 / 1", f.downloads, f.applies)
	}
	// 二度押ししても再実行しない（状態が available でないため）。
	a.ApplyUpdate()
	if f.downloads != 1 {
		t.Fatalf("二度押しで取得が %d 回になった", f.downloads)
	}
}

// 同意前に ApplyUpdate を呼んでも取得が始まらない（画面の状態ずれの保護）。
func TestApplyWithoutAvailableDoesNothing(t *testing.T) {
	a := New()
	f := &fakeApplier{}
	useApplier(a, f)
	st := a.ApplyUpdate()
	if f.downloads != 0 {
		t.Fatalf("available でないのに取得が %d 回始まった", f.downloads)
	}
	if st.Status != UpdateIdle {
		t.Fatalf("状態 = %q, want %q", st.Status, UpdateIdle)
	}
}

// 受け入れ条件: 「後で」を選んだ版はこのアプリ実行中に再表示しないこと。
func TestDeferSuppressesSameVersionOnly(t *testing.T) {
	a := New()
	version := "0.2.0"
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult(version), nil
	}

	if st := a.CheckForUpdate(); st.Status != UpdateAvailable {
		t.Fatalf("1 回目 = %q", st.Status)
	}
	if st := a.DeferUpdate(); st.Status != UpdateIdle {
		t.Fatalf("後で = %q, want %q", st.Status, UpdateIdle)
	}
	if st := a.CheckForUpdate(); st.Status != UpdateIdle {
		t.Fatalf("同じ版を再通知した: %+v", st)
	}
	// 別の版が出れば通知する。
	version = "0.3.0"
	if st := a.CheckForUpdate(); st.Status != UpdateAvailable || st.NewVersion != "0.3.0" {
		t.Fatalf("新しい版を通知しない: %+v", st)
	}
}

// 受け入れ条件: 失敗の文言が「原因＋次に取る行動」の 1 文で、内部情報を含まないこと。
// applyFailure は「利用者が『更新する』を押した後」に失敗させた状態を返す。
//
// **確認の失敗は画面へ出さない**（TestCheckFailureIsNotShownToUser）ため、文言の検査は
// 利用者が自分で始めた取得・適用の失敗で行う。
func applyFailure(t *testing.T, err error) UpdateState {
	t.Helper()
	a := New()
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult("0.2.0"), nil
	}
	useApplier(a, &fakeApplier{onDownload: func() error { return err }})
	a.CheckForUpdate()
	return a.ApplyUpdate()
}

func TestFailureMessagesAreUserFacingOnly(t *testing.T) {
	secrets := []string{"https://", "sha256", "ed25519", "status 404", "untrusted", "key_id"}

	cases := map[string]error{
		"署名検証の不合格": failure(updater.KindUntrusted),
		"鍵未登録":     failure(updater.KindNoTrustedKeys),
		"ハッシュ不一致":  failure(updater.KindHashMismatch),
		"配布物なし":    failure(updater.KindNoAsset),
		"通信の失敗":    failure(updater.KindNetwork),
		"想定外の誤り":   context.Canceled,
	}
	for name, err := range cases {
		st := applyFailure(t, err)
		if st.Status != UpdateFailed {
			t.Errorf("%s: 状態 = %q, want %q", name, st.Status, UpdateFailed)
			continue
		}
		if st.Message == "" {
			t.Errorf("%s: 文言が空", name)
			continue
		}
		// エラーの文言の様式は「原因＋利用者が次に取る行動」。
		// カタログの既存文言も原因と行動の 2 文で書かれている（例:
		// 「通信が混み合っています。しばらく待って再実行してください。」）。
		if !strings.HasSuffix(st.Message, "。") {
			t.Errorf("%s: 文末が句点でない: %q", name, st.Message)
		}
		if n := strings.Count(st.Message, "。"); n < 1 || n > 2 {
			t.Errorf("%s: 原因＋行動の体裁でない（句点 %d 個）: %q", name, n, st.Message)
		}
		if !strings.Contains(st.Message, "ください") {
			t.Errorf("%s: 次に取る行動が書かれていない: %q", name, st.Message)
		}
		for _, s := range secrets {
			if strings.Contains(strings.ToLower(st.Message), strings.ToLower(s)) {
				t.Errorf("%s: 内部情報 %q が文言に出ている: %q", name, s, st.Message)
			}
		}
		// 現行版のまま止まっていること（NewVersion を勝手に進めない）。
		if st.CurrentVersion != Version {
			t.Errorf("%s: 現行版 = %q, want %q", name, st.CurrentVersion, Version)
		}
	}
}

// 鍵未登録と署名検証の不合格で、利用者に見せる文言が異なること（鍵未登録を改ざんと誤解させない）。
func TestNoTrustedKeysMessageDiffersFromTampering(t *testing.T) {
	msgFor := func(kind updater.Kind) string {
		return applyFailure(t, failure(kind)).Message
	}
	untrusted := msgFor(updater.KindUntrusted)
	noKeys := msgFor(updater.KindNoTrustedKeys)
	if untrusted == noKeys {
		t.Fatalf("鍵未登録と改ざんの疑いで同じ文言になっている: %q", untrusted)
	}
	// 鍵未登録では改ざんを示唆しない。
	for _, w := range []string{"発行元", "改ざん"} {
		if strings.Contains(noKeys, w) {
			t.Errorf("鍵未登録の文言に %q が含まれる: %q", w, noKeys)
		}
	}
}

// 受け入れ条件: 回答モードでは更新通知を出さないこと（質問票に答えるためだけの起動で、更新の判断を求めない）。
func TestRespondentModeGetsNoUpdateNotice(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"ReqWeave", "/tmp/QS-001.rwvq"}

	a := New()
	if a.StartupMode().Mode != ModeRespondent {
		t.Fatalf("回答モードになっていない: %+v", a.StartupMode())
	}
	called := false
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		called = true
		return availableResult("9.9.9"), nil
	}
	st := a.CheckForUpdate()
	if called {
		t.Error("回答モードで更新確認が走った")
	}
	if st.Status != UpdateIdle || st.NewVersion != "" {
		t.Fatalf("回答モードで更新通知が出た: %+v", st)
	}
}

// 適用が終わっていないのに再起動しないこと（無断で終了しない）。
func TestRestartOnlyAfterApply(t *testing.T) {
	a := New()
	calls := 0
	a.update.restartFn = func() error { calls++; return nil }

	a.RestartApp()
	if calls != 0 {
		t.Fatalf("適用前に再起動が %d 回呼ばれた", calls)
	}

	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult("0.2.0"), nil
	}
	useApplier(a, &fakeApplier{})
	a.CheckForUpdate()
	a.ApplyUpdate()
	a.RestartApp()
	if calls != 1 {
		t.Fatalf("適用後の再起動回数 = %d, want 1", calls)
	}
}

// 同時に確認を呼んでも取得が二重に走らないこと。
func TestConcurrentChecksDoNotDuplicateWork(t *testing.T) {
	a := New()
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult("0.2.0"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.CheckForUpdate() }()
	}
	wg.Wait()
	if st := a.UpdateState(); st.Status != UpdateAvailable && st.Status != UpdateIdle {
		t.Fatalf("状態が壊れた: %+v", st)
	}
}

// failure は指定した種別の更新エラーを作る（updater 側の文言をそのまま使う）。
func failure(kind updater.Kind) error { return updater.NewErrorForTest(kind) }

// 起動時の自動確認が失敗しても、画面には何も出さないこと。
//
// 利用者は更新を頼んでいない。加えて失敗の文言は「再実行してください」だが、
// **再実行する導線が画面に無い**ため次に取る行動を示せない（エラーは原因＋次の行動で示す様式）。
// 設計も「**新版があれば**通知」であり失敗の通知は求めていない。
func TestCheckFailureIsNotShownToUser(t *testing.T) {
	cases := map[string]error{
		"通信の失敗":  failure(updater.KindNetwork),
		"配布物なし":  failure(updater.KindNoAsset),
		"想定外の誤り": context.Canceled,
	}
	for name, err := range cases {
		a := New()
		a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
			return updater.Result{}, err
		}
		st := a.CheckForUpdate()
		if st.Status != UpdateIdle {
			t.Errorf("%s: 状態 = %q, want %q（確認の失敗は画面へ出さない）", name, st.Status, UpdateIdle)
		}
		if st.Message != "" {
			t.Errorf("%s: 文言が出ている: %q", name, st.Message)
		}
		// 現行版は返す（画面のフッタ等が使う）。
		if st.CurrentVersion != Version {
			t.Errorf("%s: 現行版 = %q, want %q", name, st.CurrentVersion, Version)
		}
	}
}

// 確認が失敗しても、新版があるときの通知は従来どおり出ること（確認の失敗を出さない変更で壊していない）。
func TestCheckStillNotifiesWhenUpdateAvailable(t *testing.T) {
	a := New()
	a.update.checkFn = func(ctx context.Context) (updater.Result, error) {
		return availableResult("0.2.0"), nil
	}
	st := a.CheckForUpdate()
	if st.Status != UpdateAvailable {
		t.Fatalf("状態 = %q, want %q", st.Status, UpdateAvailable)
	}
	if st.NewVersion != "0.2.0" {
		t.Errorf("新版 = %q, want %q", st.NewVersion, "0.2.0")
	}
}
