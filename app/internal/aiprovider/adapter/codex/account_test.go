package codex

// ChatGPT のアカウントでのサインインとプランの残量の単体テスト。
//
// 受け入れ条件の文言をテスト名とアサーションに落とす。期待値は設計・スキーマから導出し、
// 実行結果からコピーしない。前提が揃わないときに skip せず失敗させる。

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// resetAccountState はパッケージのメモリ（残量・サインインの待ち合わせ）を空にする。
//
// 残量は**アダプタのメモリ**（= パッケージのメモリ）にあるため、テストをまたいで残ると
// 前のテストの値で緑になる（偽緑）。前後の両方で空にする。
func resetAccountState(t *testing.T) {
	t.Helper()
	clear := func() {
		planUsageStore.clear()
		loginTracker.mu.Lock()
		loginTracker.waiters = map[string]*loginWaiter{}
		loginTracker.early = map[string]loginResult{}
		loginTracker.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// recordingEvents は動作ログの記録口（記録してよい範囲の検査に使う）。
type recordingEvents struct {
	mu   sync.Mutex
	rows []string
}

func (r *recordingEvents) recorder() aiprovider.EventRecorder {
	return func(level aiprovider.EventLevel, event, message string, fields map[string]string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		row := event + " " + message
		for k, v := range fields {
			row += " " + k + "=" + v
		}
		r.rows = append(r.rows, row)
	}
}

func (r *recordingEvents) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.rows, "\n")
}

// newSignInAdapter は認証方式がサインインのアダプタを返す（キーへの参照名は持たない）。
func newSignInAdapter(t *testing.T, rec aiprovider.EventRecorder) *Adapter {
	t.Helper()
	return NewWithOptions(stubKeys{key: dummyKey}, "", aiprovider.AdapterOptions{
		AuthMethod: aiprovider.AuthChatGPTSignin,
		OnEvent:    rec,
	})
}

// signInScenario はサインインを試せる指示書（サインイン前は未認証 = 起動後の検査 (d) が許す状態）。
func signInScenario(account fakeAccount) fakeScenario {
	return fakeScenario{AccountType: "none", Account: account}
}

// サインインの開始でアダプタが**認可 URL を返す**。
// アダプタ自身はブラウザを開かない（起動する外部のプログラムは子プロセスの Codex だけで、
// URL を開く経路を持たない = 依存規則の機械検査 tools/depcheck の browser-open）。
func TestStartSignInReturnsAuthorizationURLAndDoesNotOpenBrowser(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "none"}))
	adapter := newSignInAdapter(t, nil)

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if signIn.AuthorizationURL != fakeAuthURL {
		t.Errorf("認可 URL が違う: %q", signIn.AuthorizationURL)
	}
	if signIn.LoginID != fakeLoginID {
		t.Errorf("待ち受けの識別子が違う: %q", signIn.LoginID)
	}
	params := paramsOf(t, env.received(t), "account/login/start")
	if params["type"] != "chatgpt" {
		t.Errorf("サインインの種類が違う: %v（chatgpt であること）", params["type"])
	}
	// シークレットキー方式の起動（apiKey の login/start）は行われない。
	for _, msg := range env.received(t) {
		if msg.Method == "account/login/start" && strings.Contains(string(msg.Params), `"apiKey"`) {
			t.Error("サインイン方式なのにキーを渡している")
		}
	}
}

// `account/login/completed` の成功で疎通確認が成功し、
// アカウントの情報を取得する。**`account/rateLimits/read` はサインイン完了時に 1 回だけ**呼ぶ
// （閲覧の操作では取得しない）。
func TestSignInSuccessReadsAccountAndRateLimitsOnlyOnce(t *testing.T) {
	resetAccountState(t)
	events := &recordingEvents{}
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "success"}))
	adapter := newSignInAdapter(t, events.recorder())

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	account, err := adapter.WaitSignIn(context.Background(), signIn.LoginID)
	if err != nil {
		t.Fatalf("サインインが完了しない: %v", err)
	}
	if !account.SignedIn || account.Type != "chatgpt" || account.PlanType != "free" {
		t.Errorf("アカウントの情報が違う: %+v", account)
	}
	if account.Email != fakeAccountEmail {
		t.Errorf("メールアドレスが取得できていない: %q", account.Email)
	}
	// 疎通確認はサインイン済みの確認で成功する。
	if err := adapter.VerifyKey(context.Background()); err != nil {
		t.Fatalf("サインイン後の疎通確認に失敗した: %v", err)
	}
	// 閲覧の操作（残量・アカウントの表示）を何度行っても取得の通信を増やさない。
	for i := 0; i < 3; i++ {
		if _, ok := adapter.PlanUsage(); !ok {
			t.Fatal("サインイン直後に残量が未取得になっている（1 回だけの取得の結果が保持されていない）")
		}
	}
	if n := countOf(env.received(t), "account/rateLimits/read"); n != 1 {
		t.Errorf("account/rateLimits/read の呼び出し = %d 回, want 1（サインインの完了時の 1 回だけ）", n)
	}

	// 残量の中身は Codex が返した枠をすべて持つ（枠の本数・長さを前提にしない）。
	usage, _ := adapter.PlanUsage()
	if usage.PlanType != "free" {
		t.Errorf("プラン種別が違う: %q", usage.PlanType)
	}
	if len(usage.Windows) != 3 {
		t.Fatalf("枠の件数が違う: %d（codex の primary / codex_other の primary・secondary = 3）", len(usage.Windows))
	}
	first := usage.Windows[0]
	if first.LimitID != "codex" || first.LimitName != fakeRateLimitName || first.Scope != "primary" {
		t.Errorf("1 つ目の枠が違う: %+v", first)
	}
	if first.UsedPercent != fakeUsedPercent || first.WindowDurationMins != 43200 {
		t.Errorf("1 つ目の枠の値が違う: %+v", first)
	}
	if !first.ResetsAt.Equal(time.Unix(fakeRateLimitResets, 0).UTC()) {
		t.Errorf("回復時刻が違う: %v", first.ResetsAt)
	}
	if usage.ReceivedAt.IsZero() {
		t.Error("受け取った時刻が記録されていない（画面で残量と併せて表示する）")
	}
	// 2 つ目の枠は名前が無く長さも違う（特定の長さを前提にしていないこと）。
	if usage.Windows[1].WindowDurationMins != 300 || usage.Windows[2].WindowDurationMins != 10080 {
		t.Errorf("2 つ目・3 つ目の枠の長さが違う: %+v", usage.Windows[1:])
	}
	if !usage.Windows[2].ResetsAt.IsZero() {
		t.Errorf("回復時刻が無い枠でゼロ値になっていない: %v", usage.Windows[2].ResetsAt)
	}

	// 動作ログへ認可の URL・メールアドレス・トークンの形の値を書かない。
	log := events.all()
	for _, leak := range []string{fakeAuthURL, fakeAccountEmail, "auth.openai.com", "eyJ"} {
		if strings.Contains(log, leak) {
			t.Errorf("動作ログに %q が残っている:\n%s", leak, log)
		}
	}
	if !strings.Contains(log, "ai.codex_signin_completed") {
		t.Errorf("サインインの結果が動作ログに残っていない:\n%s", log)
	}
}

// 認可されないまま**時間切れ**になったときは失敗として扱い、
// アカウント・残量を取得しない（設定を変えないのは呼び出し側 = バインディング）。
func TestSignInTimeoutFailsWithoutAccountOrUsage(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "timeout"}))
	adapter := newSignInAdapter(t, nil)

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	_, err = adapter.WaitSignIn(context.Background(), signIn.LoginID)
	var pErr *aiprovider.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("正規化エラーでない: %v", err)
	}
	if pErr.Class != aiprovider.ErrClassConfig || pErr.Code != codeSignInTimedOut {
		t.Errorf("分類・Code が違う: %v / %q（設定起因・signin_timed_out）", pErr.Class, pErr.Code)
	}
	if _, ok := adapter.PlanUsage(); ok {
		t.Error("時間切れなのに残量を保持している")
	}
	if n := countOf(env.received(t), "account/rateLimits/read"); n != 0 {
		t.Errorf("時間切れなのに残量を取りに行った（%d 回）", n)
	}
	// 疎通確認は失敗のまま（サインインしていない）。
	verifyErr := adapter.VerifyKey(context.Background())
	if !errors.As(verifyErr, &pErr) || pErr.Code != codeUnauthorized {
		t.Errorf("未サインインの疎通確認が設定起因（unauthorized）でない: %v", verifyErr)
	}
}

// Codex から完了が届かないまま待ち受けの上限を過ぎたら、取り消しを送って時間切れにする。
func TestSignInOwnDeadlineCancelsWaiting(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "none"}))

	original := signInWait
	signInWait = 300 * time.Millisecond // 待ち時間そのものではなく、時間が来たときの振る舞いを確かめる
	t.Cleanup(func() { signInWait = original })

	adapter := newSignInAdapter(t, nil)
	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	start := time.Now()
	_, err = adapter.WaitSignIn(context.Background(), signIn.LoginID)
	var pErr *aiprovider.ProviderError
	if !errors.As(err, &pErr) || pErr.Code != codeSignInTimedOut {
		t.Fatalf("待ち受けの上限で時間切れにならない: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("上限より早く諦めている: %v", elapsed)
	}
	waitFor(t, func() bool { return countOf(env.received(t), "account/login/cancel") == 1 },
		"待ち受けの取り消しを送っていない")
}

// 利用者の取り消しは**エラーにしない**（取り消しを Codex へ伝える）。
func TestCancelSignInIsNotAnError(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "none"}))
	adapter := newSignInAdapter(t, nil)

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := adapter.WaitSignIn(ctx, signIn.LoginID)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("取り消しが取り消しとして返らない: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取り消しても待ち受けが終わらない")
	}
	if err := adapter.CancelSignIn(context.Background(), signIn.LoginID); err != nil {
		t.Errorf("取り消しの操作が失敗した: %v", err)
	}
	waitFor(t, func() bool { return countOf(env.received(t), "account/login/cancel") >= 1 },
		"取り消しを Codex へ伝えていない")
}

// サインアウトで Codex の項目が消える（`account/logout`）。保持している残量も捨てる。
func TestSignOutSendsLogoutAndForgetsPlanUsage(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "success"}))
	adapter := newSignInAdapter(t, nil)

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if _, err := adapter.WaitSignIn(context.Background(), signIn.LoginID); err != nil {
		t.Fatalf("サインインが完了しない: %v", err)
	}
	if _, ok := adapter.PlanUsage(); !ok {
		t.Fatal("サインイン後に残量を保持していない")
	}
	if err := adapter.SignOut(context.Background()); err != nil {
		t.Fatalf("サインアウトに失敗した: %v", err)
	}
	if n := countOf(env.received(t), "account/logout"); n != 1 {
		t.Errorf("account/logout の呼び出し = %d 回, want 1", n)
	}
	if _, ok := adapter.PlanUsage(); ok {
		t.Error("サインアウト後も残量を保持している（前のアカウントの値を出し続けない）")
	}
	account, err := adapter.Account(context.Background())
	if err != nil {
		t.Fatalf("アカウントの確認に失敗した: %v", err)
	}
	if account.SignedIn {
		t.Error("サインアウト後もサインイン済みと判定される")
	}
}

// シークレットキー方式では残量を表示しない（Codex が返さないため）。
func TestPlanUsageIsNotReportedForSecretKeyMethod(t *testing.T) {
	resetAccountState(t)
	newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "success"}))

	// サインイン方式で取得した値が残っていても、キー方式のアダプタは返さない。
	signIn := newSignInAdapter(t, nil)
	started, err := signIn.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if _, err := signIn.WaitSignIn(context.Background(), started.LoginID); err != nil {
		t.Fatalf("サインインが完了しない: %v", err)
	}
	if _, ok := signIn.PlanUsage(); !ok {
		t.Fatal("サインイン方式で残量を保持していない（前提が崩れている）")
	}
	secret := newTestAdapter(t)
	if _, ok := secret.PlanUsage(); ok {
		t.Error("シークレットキー方式で残量が表示される（この方式では Codex が残量を返さない）")
	}
}

// 以後の更新は `account/rateLimits/updated` だけで行う
// （AI 呼び出しの間に届く。閲覧の操作では取得しない）。
func TestRateLimitsUpdatedNotificationRefreshesUsageWithoutExtraRead(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{
		LoginOutcome: "success", UpdatesDuringTurn: true, UpdatedUsedPercent: 87,
	}))
	adapter := newSignInAdapter(t, nil)

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if _, err := adapter.WaitSignIn(context.Background(), signIn.LoginID); err != nil {
		t.Fatalf("サインインが完了しない: %v", err)
	}
	before, _ := adapter.PlanUsage()
	if before.Windows[0].UsedPercent != fakeUsedPercent {
		t.Fatalf("サインイン直後の使用率が違う: %d", before.Windows[0].UsedPercent)
	}

	ch, err := adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	collect(t, ch)

	after, ok := adapter.PlanUsage()
	if !ok {
		t.Fatal("対話の後に残量が未取得になっている")
	}
	if len(after.Windows) != 1 || after.Windows[0].UsedPercent != 87 {
		t.Errorf("通知で更新されていない: %+v", after.Windows)
	}
	if !after.ReceivedAt.After(before.ReceivedAt) && !after.ReceivedAt.Equal(before.ReceivedAt) {
		t.Errorf("受け取った時刻が更新されていない: %v → %v", before.ReceivedAt, after.ReceivedAt)
	}
	if n := countOf(env.received(t), "account/rateLimits/read"); n != 1 {
		t.Errorf("account/rateLimits/read の呼び出し = %d 回, want 1（更新は通知だけで行う）", n)
	}
}

// 残量は**アダプタのメモリにだけ**置き、本システムのファイルへ書かない。
// 子プロセスが動いている間（一時領域がある間）と、止めた後の両方で全ファイルを検索する。
func TestPlanUsageIsNeverWrittenToAnyFile(t *testing.T) {
	resetAccountState(t)
	env := newFakeEnv(t, signInScenario(fakeAccount{LoginOutcome: "success"}))
	adapter := newSignInAdapter(t, nil)

	signIn, err := adapter.StartSignIn(context.Background())
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if _, err := adapter.WaitSignIn(context.Background(), signIn.LoginID); err != nil {
		t.Fatalf("サインインが完了しない: %v", err)
	}
	usage, ok := adapter.PlanUsage()
	if !ok || len(usage.Windows) == 0 {
		t.Fatal("残量を保持していない（検索の前提が崩れている）")
	}

	markers := []string{
		fakeRateLimitName,                          // 枠の名前
		strconv.FormatInt(fakeRateLimitResets, 10), // 回復時刻（UNIX 秒）
		fakeAccountEmail,                           // アカウントのメールアドレス（保存しない）
		fakeAuthURL,                                // 認可の URL
	}
	assertNoMarkersInFiles(t, env.base, "子プロセスの実行中", markers)
	if err := procManager.stopCurrent("テストの検査"); err != nil {
		t.Fatalf("子プロセスを止められない: %v", err)
	}
	assertNoMarkersInFiles(t, env.base, "子プロセスの停止後", markers)
}

// assertNoMarkersInFiles は dir 配下の全ファイルに目印が現れないことを確かめる。
//
// 検索が 1 ファイルも見ていない（走査が空振り）場合も失敗させる（0 件を「無かった」と読み違えない）。
func assertNoMarkersInFiles(t *testing.T, dir, phase string, markers []string) {
	t.Helper()
	scanned := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 途中で消えるファイル（一時領域）は無視する
		}
		if d.IsDir() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		scanned++
		for _, marker := range markers {
			if strings.Contains(string(body), marker) {
				t.Errorf("%s: %s に %q が書かれている（残量・アカウントの情報はメモリにだけ置く）",
					phase, path, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: 全ファイル検索に失敗した: %v", phase, err)
	}
	if scanned == 0 {
		t.Fatalf("%s: 検索対象のファイルが 1 件も無い（検索が空振りしている）", phase)
	}
}

// 残量切れは**設定起因**（自動で切り替えない）で、
// 利用者の操作で切り替えるための Code（plan_usage_exhausted）を伴う。
func TestPlanUsageExhaustedIsConfigErrorAndKeepsUsingSameProvider(t *testing.T) {
	resetAccountState(t)
	// サインイン済み（account/read が chatgpt を返す）で残量切れのターンを起こす。
	env := newFakeEnv(t, fakeScenario{
		AccountType: "chatgpt",
		Turn:        fakeTurn{Kind: "error", ErrorInfo: "usageLimitExceeded", ErrorMessage: "usage limit reached"},
	})
	adapter := newSignInAdapter(t, nil)

	ch, err := adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	last := lastEvent(t, collect(t, ch))
	if last.Kind != aiprovider.EventError || last.Err == nil {
		t.Fatalf("エラーで終わっていない: %+v", last)
	}
	if last.Err.Class != aiprovider.ErrClassConfig || last.Err.Code != "plan_usage_exhausted" {
		t.Errorf("分類・Code が違う: %v / %q（設定起因・plan_usage_exhausted）",
			last.Err.Class, last.Err.Code)
	}
	// **別の認証方式・別のプロバイダへ勝手に切り替えない**
	// ＝ キー方式での起動（apiKey の login/start）も、キーの読み出しも起きない。
	for _, msg := range env.received(t) {
		if msg.Method == "account/login/start" && strings.Contains(string(msg.Params), `"apiKey"`) {
			t.Error("残量切れの後にシークレットキー方式へ切り替えている（認証方式を自動で切り替えてはならない）")
		}
	}
	// 同じ子プロセス（同じ認証方式）のまま次の呼び出しへ進める。
	if proc := procManager.current(); proc == nil || proc.key.auth != authChatGPTSignin {
		t.Errorf("残量切れの後に認証方式が変わっている: %+v", proc)
	}
}

// **完了の通知が待ち受け（WaitSignIn）の呼び出しより先に届いても取りこぼさない**。
//
// StartSignIn は要求の直後に待ち合わせ口を作る。結果を渡した時点で口を捨てると、
// WaitSignIn が空の口を作り直して結果を失い、15 分の上限まで止まる（通しのゲートで
// 10 分の時間切れとして観測した）。
func TestSignInResultSurvivesNotificationBeforeWait(t *testing.T) {
	r := &loginRegistry{waiters: map[string]*loginWaiter{}, early: map[string]loginResult{}}

	r.register("login-1")                             // StartSignIn 相当（要求の直後）
	r.complete("login-1", loginResult{success: true}) // 認可が速く、通知が先に届く
	wait := r.register("login-1")                     // WaitSignIn 相当

	select {
	case res := <-wait:
		if !res.success {
			t.Errorf("完了が成功として届かない: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("待ち受けの前に届いた完了を取りこぼした（15 分の上限まで止まる）")
	}
}

// 結果を渡した口へ子プロセスの終了（failAll）が二重に送って詰まらない。
// 口はバッファ 1 のため、二重送信は mutex を保持したまま止まる（全体の停止になる）。
func TestFailAllSkipsWaitersThatAlreadyGotAResult(t *testing.T) {
	r := &loginRegistry{waiters: map[string]*loginWaiter{}, early: map[string]loginResult{}}
	r.register("login-1")
	r.complete("login-1", loginResult{success: true})

	done := make(chan struct{})
	go func() {
		r.failAll(errors.New("子プロセスが終了した"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("結果を渡した待ち合わせ口へ二重に送って詰まった")
	}
	if res := <-r.register("login-1"); !res.success {
		t.Errorf("先に渡した結果が上書きされた: %+v", res)
	}
}
