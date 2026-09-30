package binding

// ChatGPT のアカウントでのサインインとプランの残量のバインディングの単体テスト。
//
// OS セキュアストレージへは触れない（サインイン方式はキーを持たないため）。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// fakeAccountAdapter はサインインを持つアダプタの代役（aiprovider の任意のインタフェースを実装）。
type fakeAccountAdapter struct {
	mu sync.Mutex

	authMethod aiprovider.AuthMethod

	signIn    aiprovider.SignIn
	startErr  error
	account   aiprovider.AccountInfo
	waitErr   error
	waitDelay time.Duration
	signOut   error
	usage     aiprovider.PlanUsage
	hasUsage  bool
	models    []aiprovider.ModelInfo

	// 呼び出し回数（閲覧の操作で通信を起こしていないことの検査に使う）。
	startCalls  int
	waitCalls   int
	cancelCalls int
	signOutCall int
	accountCall int
}

func (f *fakeAccountAdapter) ID() aiprovider.ProviderID { return aiprovider.ProviderID("codex") }

func (f *fakeAccountAdapter) ListModels(context.Context) ([]aiprovider.ModelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.models) == 0 {
		return nil, errors.New("このテストではモデル一覧を使わない")
	}
	return f.models, nil
}

func (f *fakeAccountAdapter) StreamMessage(context.Context, aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	return nil, errors.New("このテストではストリーミングを使わない")
}

func (f *fakeAccountAdapter) VerifyKey(context.Context) error { return nil }

func (f *fakeAccountAdapter) StartSignIn(context.Context) (aiprovider.SignIn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	if f.startErr != nil {
		return aiprovider.SignIn{}, f.startErr
	}
	return f.signIn, nil
}

func (f *fakeAccountAdapter) WaitSignIn(ctx context.Context, loginID string) (aiprovider.AccountInfo, error) {
	f.mu.Lock()
	f.waitCalls++
	delay, waitErr, account := f.waitDelay, f.waitErr, f.account
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return aiprovider.AccountInfo{}, ctx.Err()
		}
	}
	if waitErr != nil {
		return aiprovider.AccountInfo{}, waitErr
	}
	return account, nil
}

func (f *fakeAccountAdapter) CancelSignIn(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelCalls++
	return nil
}

func (f *fakeAccountAdapter) SignOut(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signOutCall++
	return f.signOut
}

func (f *fakeAccountAdapter) Account(context.Context) (aiprovider.AccountInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountCall++
	return f.account, nil
}

func (f *fakeAccountAdapter) PlanUsage() (aiprovider.PlanUsage, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usage, f.hasUsage
}

func (f *fakeAccountAdapter) counts() (start, wait, cancel, signOut, account int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startCalls, f.waitCalls, f.cancelCalls, f.signOutCall, f.accountCall
}

// newSignInAPI はサインインを試せる API を返す（保存先は一時フォルダ・アダプタは代役）。
func newSignInAPI(t *testing.T, stub *fakeAccountAdapter) *API {
	t.Helper()
	a := &API{
		paths: projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)},
		newAdapter: func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
			opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
			stub.mu.Lock()
			stub.authMethod = opts.AuthMethod
			stub.mu.Unlock()
			if ref != "" {
				t.Errorf("サインイン方式なのにキーへの参照名が渡された: %q", ref)
			}
			return stub, nil
		},
	}
	// 認可の URL を開く経路を差し替える（実際のブラウザは起動しない）。
	original := openURLFn
	t.Cleanup(func() { openURLFn = original })
	return a
}

// captureOpenedURLs は openURLFn が開いた URL を記録する。
func captureOpenedURLs() *[]string {
	opened := &[]string{}
	openURLFn = func(_ context.Context, url string) { *opened = append(*opened, url) }
	return opened
}

// writeSettings はアプリ設定を書く（サインイン方式の設定は key_ref を持たない）。
func writeSettings(t *testing.T, a *API, providers []projectstore.ProviderSetting, defaultLabel string) []byte {
	t.Helper()
	s := projectstore.NewSettings()
	if err := s.RegisterAuthor("k.sato@example.co.jp", "佐藤"); err != nil {
		t.Fatal(err)
	}
	s.Providers = providers
	s.DefaultProvider = defaultLabel
	if err := projectstore.SaveSettings(a.paths, s); err != nil {
		t.Fatalf("アプリ設定を保存できない: %v", err)
	}
	body, err := os.ReadFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatalf("アプリ設定を読めない: %v", err)
	}
	return body
}

func signInProviderSetting() projectstore.ProviderSetting {
	return projectstore.ProviderSetting{
		Label: "ChatGPT", Provider: "codex", Model: "gpt-5.5", Effort: "standard",
		AuthMethod: projectstore.AuthMethodChatGPTSignin,
	}
}

const testAuthURL = "https://auth.openai.com/oauth/authorize?client_id=app_TEST"

// サインインの操作で**認可のページが既定のブラウザで開き**、
// 認可の後に画面へ成否が出る。URL を開くのはバインディング層（アダプタは返すだけ。ブラウザを開く箇所を 1 か所に限る）。
func TestStartCodexSignInOpensAuthorizationURLFromAdapter(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn: aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		account: aiprovider.AccountInfo{
			SignedIn: true, Type: "chatgpt", PlanType: "plus", Email: "user@example.invalid",
		},
	}
	a := newSignInAPI(t, stub)
	opened := captureOpenedURLs()

	view, err := a.StartCodexSignIn("ChatGPT")
	if err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if view.State != SignInStateWaiting {
		t.Errorf("待機状態にならない: %+v", view)
	}
	if view.WaitMinutes != 15 {
		t.Errorf("待ち受けの上限の表示が違う: %d 分, want 15", view.WaitMinutes)
	}
	if len(*opened) != 1 || (*opened)[0] != testAuthURL {
		t.Fatalf("アダプタが返した認可 URL を開いていない: %v", *opened)
	}
	if stub.authMethod != aiprovider.AuthChatGPTSignin {
		t.Errorf("アダプタへ渡した認証方式が違う: %q", stub.authMethod)
	}

	waitForSignInState(t, a, SignInStateSignedIn)
	final := a.CodexSignInState()
	if final.Account == nil {
		t.Fatal("成功時にアカウントの情報が無い")
	}
	if final.Account.PlanLabel != "Plus" || final.Account.AccountLabel != "ChatGPT のアカウント" {
		t.Errorf("ラベルが違う: %+v", final.Account)
	}
	if final.Account.TrainingNotice == "" {
		t.Error("個人向けプランで学習利用の注意喚起が出ていない（入力が学習に使われうることを利用者へ示す）")
	}
}

// 子プロセスが返した値をそのまま OS へ渡さない（https 以外の URL を開かない）。
func TestStartCodexSignInRefusesNonHTTPSURL(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn: aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: "file:///etc/passwd"},
	}
	a := newSignInAPI(t, stub)
	opened := captureOpenedURLs()

	view, err := a.StartCodexSignIn("ChatGPT")
	if err != nil {
		t.Fatalf("呼び出しが失敗した: %v", err)
	}
	if len(*opened) != 0 {
		t.Errorf("https 以外の URL を開いた: %v", *opened)
	}
	if view.State != SignInStateFailed {
		t.Errorf("失敗状態にならない: %+v", view)
	}
	if view.Message != msgSignInUnavailable {
		t.Errorf("文言が違う: %q", view.Message)
	}
}

// **時間切れは失敗として表示され、設定が変わらない**。
func TestSignInTimeoutShowsFailureAndKeepsSettings(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn: aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		waitErr: &aiprovider.ProviderError{
			Class: aiprovider.ErrClassConfig, Code: "signin_timed_out", Message: "Login timed out",
		},
	}
	a := newSignInAPI(t, stub)
	captureOpenedURLs()
	before := writeSettings(t, a, []projectstore.ProviderSetting{{
		Label: "社内 Claude", Provider: "anthropic", Model: "claude-x", Effort: "standard",
		KeyRef: "anthropic/社内 Claude",
	}}, "社内 Claude")

	if _, err := a.StartCodexSignIn("ChatGPT"); err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	waitForSignInState(t, a, SignInStateFailed)

	view := a.CodexSignInState()
	if view.Message != "サインインが完了しませんでした。もう一度サインインしてください。" {
		t.Errorf("時間切れの文言が違う: %q", view.Message)
	}
	if strings.Contains(view.Message, "signin_timed_out") || strings.Contains(view.Message, "Login timed out") {
		t.Errorf("生のコード値・生メッセージが画面へ出ている: %q", view.Message)
	}
	after, err := os.ReadFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("サインインの失敗で設定が変わった:\n前:\n%s\n後:\n%s", before, after)
	}
}

// 利用者による取り消しはエラーとして表示しない（本人の操作なので失敗ではない）。
func TestCancelCodexSignInIsNotShownAsError(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn:    aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		waitDelay: 5 * time.Second, // 取り消しが先に効くだけの間、待たせる
	}
	a := newSignInAPI(t, stub)
	captureOpenedURLs()

	if _, err := a.StartCodexSignIn("ChatGPT"); err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	if err := a.CancelCodexSignIn(); err != nil {
		t.Fatalf("取り消しに失敗した: %v", err)
	}
	waitForSignInState(t, a, SignInStateSignedOut)
	if msg := a.CodexSignInState().Message; msg != "" {
		t.Errorf("取り消しがエラーとして表示された: %q", msg)
	}
	if _, _, cancels, _, _ := stub.counts(); cancels != 1 {
		t.Errorf("取り消しを AI プロバイダへ伝えていない（%d 回）", cancels)
	}
}

// サインアウト後は「未サインイン」となり、
// AI 呼び出しを伴う操作が「キー未登録」と同じ扱いでブロックされる。
func TestSignOutBlocksAIOperations(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn:  aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		account: aiprovider.AccountInfo{SignedIn: true, Type: "chatgpt", PlanType: "free"},
	}
	a := newSignInAPI(t, stub)
	captureOpenedURLs()
	writeSettings(t, a, []projectstore.ProviderSetting{signInProviderSetting()}, "ChatGPT")

	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	// サインイン済みはキー登録済みと同等に扱う。
	if _, err := a.StartCodexSignIn("ChatGPT"); err != nil {
		t.Fatal(err)
	}
	waitForSignInState(t, a, SignInStateSignedIn)
	if ready, reason := a.aiReadiness(settings); !ready {
		t.Errorf("サインイン済みなのに AI 機能がブロックされている: %q", reason)
	}

	if err := a.SignOutCodex("ChatGPT"); err != nil {
		t.Fatalf("サインアウトに失敗した: %v", err)
	}
	if _, _, _, signOuts, _ := stub.counts(); signOuts != 1 {
		t.Errorf("サインアウトを AI プロバイダへ伝えていない（%d 回）", signOuts)
	}
	ready, reason := a.aiReadiness(settings)
	if ready {
		t.Error("サインアウト後も AI 機能が使える")
	}
	if !strings.Contains(reason, "サインイン") {
		t.Errorf("ブロックの理由が誘導になっていない: %q", reason)
	}
	if err := a.requireAIReady(); err == nil {
		t.Error("サインアウト後に AI 呼び出しの前段が通ってしまう（キー未登録と同じ扱いにならない）")
	}
}

// サインイン方式を提供しないプロバイダの設定に `chatgpt_signin` が
// 入っていても（設定ファイルの手編集で起こりうる。保存形式は組み合わせの正しさを持たない）、
// キーが無いまま「使える」と答えない。
func TestSignInAuthMethodOnProviderWithoutSignInIsNotReady(t *testing.T) {
	a := newSignInAPI(t, &fakeAccountAdapter{})
	writeSettings(t, a, []projectstore.ProviderSetting{{
		Label: "Claude", Provider: "anthropic", Model: "claude-sonnet-5", Effort: "standard",
		AuthMethod: projectstore.AuthMethodChatGPTSignin,
	}}, "Claude")

	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	ready, reason := a.aiReadiness(settings)
	if ready {
		t.Error("サインインを提供しないプロバイダなのに AI 機能が使えると答えた（キーは未登録）")
	}
	if !strings.Contains(reason, "設定") {
		t.Errorf("ブロックの理由が設定への誘導になっていない: %q", reason)
	}
	if err := a.requireAIReady(); err == nil {
		t.Error("AI 呼び出しの前段が通ってしまう（キー未登録と同じ扱いにならない）")
	}
}

// 残量の表示のために AI 呼び出し・取得の通信を起こさない。
func TestCodexPlanUsageDoesNotFetch(t *testing.T) {
	resets := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	stub := &fakeAccountAdapter{
		hasUsage: true,
		usage: aiprovider.PlanUsage{
			PlanType:   "free",
			ReceivedAt: time.Date(2026, 9, 15, 3, 4, 5, 0, time.UTC),
			Windows: []aiprovider.PlanUsageWindow{
				{LimitID: "codex", LimitName: "", Scope: "primary", UsedPercent: 42,
					WindowDurationMins: 43200, ResetsAt: resets},
				{LimitID: "codex", Scope: "secondary", UsedPercent: 3, WindowDurationMins: 300},
			},
		},
	}
	a := newSignInAPI(t, stub)
	writeSettings(t, a, []projectstore.ProviderSetting{signInProviderSetting()}, "ChatGPT")

	view, err := a.CodexPlanUsage("ChatGPT")
	if err != nil {
		t.Fatalf("残量を取得できない: %v", err)
	}
	if !view.Available || !view.Fetched {
		t.Fatalf("残量が表示されない: %+v", view)
	}
	if view.PlanLabel != "Free" {
		t.Errorf("プランのラベルが違う: %q", view.PlanLabel)
	}
	if view.ReceivedAt != "2026-09-15T03:04:05Z" {
		t.Errorf("受け取った時刻が違う: %q", view.ReceivedAt)
	}
	if len(view.Windows) != 2 {
		t.Fatalf("枠の件数が違う: %d", len(view.Windows))
	}
	if view.Windows[0].Label != "利用枠" || view.Windows[0].WindowLabel != "30 日" {
		t.Errorf("1 つ目の枠の表示が違う: %+v", view.Windows[0])
	}
	if view.Windows[0].ResetsAt != "2026-09-30T12:00:00Z" {
		t.Errorf("回復時刻が違う: %q", view.Windows[0].ResetsAt)
	}
	if view.Windows[1].Label != "追加の利用枠" || view.Windows[1].WindowLabel != "5 時間" {
		t.Errorf("2 つ目の枠の表示が違う: %+v", view.Windows[1])
	}
	if view.Windows[1].ResetsAt != "" {
		t.Errorf("回復時刻が不明な枠で値が入っている: %q", view.Windows[1].ResetsAt)
	}
	// 閲覧の操作では、サインイン・アカウントの取得を 1 回も呼ばない。
	if start, wait, _, _, account := stub.counts(); start != 0 || wait != 0 || account != 0 {
		t.Errorf("残量の表示で通信を起こしている: start=%d wait=%d account=%d", start, wait, account)
	}
}

// 値を受け取っていない場合は**未取得である旨**を表示する。
func TestCodexPlanUsageShowsUnfetchedNotice(t *testing.T) {
	stub := &fakeAccountAdapter{hasUsage: false}
	a := newSignInAPI(t, stub)
	writeSettings(t, a, []projectstore.ProviderSetting{signInProviderSetting()}, "ChatGPT")

	view, err := a.CodexPlanUsage("ChatGPT")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Available || view.Fetched {
		t.Fatalf("未取得の扱いが違う: %+v", view)
	}
	if view.Notice == "" {
		t.Error("未取得である旨が表示されない（黙って空欄にしない）")
	}
}

// シークレットキー方式では表示しない（プランの残量はサインイン方式にしか無い）。
// 回答モード（アプリ設定にプロバイダ設定を持たない）でも表示しない。
func TestCodexPlanUsageHiddenForSecretKeyAndWithoutSetting(t *testing.T) {
	stub := &fakeAccountAdapter{hasUsage: true, usage: aiprovider.PlanUsage{PlanType: "free"}}
	a := newSignInAPI(t, stub)
	writeSettings(t, a, []projectstore.ProviderSetting{{
		Label: "Codex キー", Provider: "codex", Model: "gpt-5.5", Effort: "standard",
		AuthMethod: projectstore.AuthMethodSecretKey, KeyRef: "codex/Codex キー",
	}}, "Codex キー")

	view, err := a.CodexPlanUsage("Codex キー")
	if err != nil {
		t.Fatal(err)
	}
	if view.Available {
		t.Errorf("シークレットキー方式で残量が表示される: %+v", view)
	}

	// 設定を持たない場合（回答モードの一時利用）も表示しない。
	empty := newSignInAPI(t, stub)
	view, err = empty.CodexPlanUsage("ChatGPT")
	if err != nil {
		t.Fatal(err)
	}
	if view.Available {
		t.Errorf("設定が無いのに残量が表示される: %+v", view)
	}
}

// **Codex App Server だけ**が認証方式を 2 つ持つ。
// 回答モードでも同じ定義を使う（回答モードの画面は初期設定のプロバイダ選択を共用する）。
func TestOnlyCodexOffersBothAuthMethods(t *testing.T) {
	var codex ProviderOption
	for _, p := range providerOptions {
		if p.ID == "codex" {
			codex = p
			continue
		}
		if len(p.AuthMethods) != 0 {
			t.Errorf("%s に認証方式の選択がある（2 つ持つのは codex だけ）", p.ID)
		}
		if supportsAuthMethod(p.ID, string(aiprovider.AuthChatGPTSignin)) {
			t.Errorf("%s でサインイン方式が選べてしまう", p.ID)
		}
	}
	if len(codex.AuthMethods) != 2 {
		t.Fatalf("Codex App Server の認証方式が 2 つでない: %+v", codex.AuthMethods)
	}
	want := map[string]bool{"secret_key": true, "chatgpt_signin": true}
	defaults := 0
	for _, m := range codex.AuthMethods {
		if !want[m.ID] {
			t.Errorf("想定外の認証方式: %q", m.ID)
		}
		delete(want, m.ID)
		if m.Label == "" || m.Description == "" {
			t.Errorf("%s: ラベル・説明が無い（方式ごとのポリシーと注意喚起を示す）", m.ID)
		}
		if m.Default {
			defaults++
		}
	}
	if len(want) != 0 {
		t.Errorf("欠けている認証方式: %v", want)
	}
	if defaults != 1 {
		t.Errorf("既定の認証方式が %d 件（1 件であること）", defaults)
	}
	// サインイン方式の説明は、動作ログに残る応答のクッキーを開示する。
	for _, m := range codex.AuthMethods {
		if m.ID != "chatgpt_signin" {
			continue
		}
		for _, phrase := range []string{"クッキー", "一時領域", "終了時"} {
			if !strings.Contains(m.Description, phrase) {
				t.Errorf("サインイン方式の説明に %q が無い: %q", phrase, m.Description)
			}
		}
	}
	// 回答モードでも同じ選択肢を使う（OS で選べるものの一覧に含まれる）。
	found := false
	for _, p := range availableProviderOptions("darwin") {
		if p.ID == "codex" && len(p.AuthMethods) == 2 {
			found = true
		}
	}
	if !found {
		t.Error("macOS の選択肢に、認証方式を 2 つ持つ Codex App Server が無い")
	}
	if runtime.GOOS == "darwin" && !supportsAuthMethod("codex", "chatgpt_signin") {
		t.Error("codex でサインイン方式を選べない")
	}
}

// サインイン方式で初期設定を確定すると、
// `auth_method` が保存され **`key_ref` を持たない**（キーマネージャの項目も作らない）。
func TestCompleteSetupWithSignInStoresNoKeyRef(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn:  aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		account: aiprovider.AccountInfo{SignedIn: true, Type: "chatgpt", PlanType: "free"},
	}
	a := newSignInAPI(t, stub)
	captureOpenedURLs()
	if _, err := a.StartCodexSignIn("ChatGPT"); err != nil {
		t.Fatal(err)
	}
	waitForSignInState(t, a, SignInStateSignedIn)

	err := a.CompleteSetup(SetupRequest{
		ProviderID: "codex", Label: "ChatGPT", Model: "gpt-5.5", Effort: "standard",
		AuthMethod: "chatgpt_signin", AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤",
	})
	if runtime.GOOS != "darwin" {
		// Codex App Server を選べるのは macOS だけ。
		if err == nil {
			t.Fatal("macOS 以外で Codex App Server が選べてしまう")
		}
		return
	}
	if err != nil {
		t.Fatalf("初期設定を確定できない: %v", err)
	}
	body, readErr := os.ReadFile(a.paths.SettingsFile())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(body), `"auth_method": "chatgpt_signin"`) {
		t.Errorf("認証方式が保存されていない:\n%s", body)
	}
	if strings.Contains(string(body), "key_ref") {
		t.Errorf("サインイン方式なのに key_ref が保存された:\n%s", body)
	}
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := settings.DefaultProviderSetting()
	if !ok || p.AuthMethodOrDefault() != "chatgpt_signin" || p.KeyRef != "" {
		t.Errorf("保存された設定が違う: %+v", p)
	}
}

// プラン種別・アカウントの種類のラベルは網羅し、未知は「不明」に倒す
// （生のコード値を画面へ出さない）。
func TestPlanAndAccountLabelsAreExhaustiveAndFallBackToUnknown(t *testing.T) {
	if len(planLabels) != len(allPlanTypes) {
		t.Errorf("ラベル表とプラン種別の一覧が食い違う: %d / %d", len(planLabels), len(allPlanTypes))
	}
	for _, p := range allPlanTypes {
		label, ok := planLabels[p]
		if !ok || label == "" {
			t.Errorf("プラン種別 %q のラベルが無い", p)
		}
		if label == string(p) {
			t.Errorf("プラン種別 %q のラベルが生のコード値のまま", p)
		}
	}
	if len(accountLabels) != len(allAccountTypes) {
		t.Errorf("ラベル表とアカウントの種類の一覧が食い違う: %d / %d", len(accountLabels), len(allAccountTypes))
	}
	for _, a := range allAccountTypes {
		if accountLabels[a] == "" {
			t.Errorf("アカウントの種類 %q のラベルが無い", a)
		}
	}
	// 未知・未取得は「不明」。
	for _, code := range []string{"", "starter", "chatgpt_super"} {
		if got := planLabel(code); got != "不明" {
			t.Errorf("未知のプラン種別 %q のラベルが %q（「不明」であること）", code, got)
		}
		if got := accountLabel(code); got != "不明" {
			t.Errorf("未知のアカウントの種類 %q のラベルが %q（「不明」であること）", code, got)
		}
	}
	if planLabel("unknown") != "不明" {
		t.Errorf("unknown のラベルが「不明」でない: %q", planLabel("unknown"))
	}
}

// 学習利用の注意喚起は個人向けプランと、
// **取得できない場合・未知の値**でも出す（安全側）。法人向けでは出さない。
func TestTrainingNoticeCoversPersonalAndUnknownPlans(t *testing.T) {
	for _, code := range []string{"free", "go", "plus", "pro", "prolite", "unknown", "", "starter"} {
		if !needsTrainingNotice(code) {
			t.Errorf("プラン種別 %q で学習利用の注意喚起が出ない", code)
		}
	}
	for _, code := range []string{"team", "business", "enterprise", "edu"} {
		if needsTrainingNotice(code) {
			t.Errorf("法人向けプラン %q で個人向けの注意喚起が出ている", code)
		}
	}
	view := accountView(aiprovider.AccountInfo{SignedIn: true, Type: "chatgpt", PlanType: "free"})
	for _, phrase := range []string{"データコントロール", "Improve the model for everyone", "Do not train on my content"} {
		if !strings.Contains(view.TrainingNotice, phrase) {
			t.Errorf("停止方法に %q が無い: %q", phrase, view.TrainingNotice)
		}
	}
}

// 分類 × Code から利用者向けの 1 文を作り、**未知の Code は分類ごとの既定へ倒す**。
func TestAIErrorMessageCatalog(t *testing.T) {
	cases := []struct {
		name     string
		class    string
		code     string
		signedIn bool
		want     string
	}{
		{"起動後の検査の不合格", "permanent", "codex_guard_failed", false,
			"Codex App Server を安全な設定で使えることを確認できなかったため、呼び出しを止めました。別の AIプロバイダを選ぶか、アプリを最新版へ更新してください。"},
		{"道具の検知", "permanent", "codex_tool_attempt", false,
			"Codex App Server を安全な設定で使えることを確認できなかったため、呼び出しを止めました。別の AIプロバイダを選ぶか、アプリを最新版へ更新してください。"},
		{"子プロセスの異常終了", "transient", "codex_process_exited", false,
			"Codex App Server が応答しませんでした。もう一度実行してください。"},
		{"一時領域の排他", "transient", "codex_workspace_busy", false,
			"別のウィンドウが Codex App Server を使っています。そのウィンドウを閉じてから再実行してください。"},
		{"サインイン中の失効", "config", "unauthorized", true,
			"ChatGPT のアカウントのサインインが切れました。設定画面でサインインし直してください。"},
		{"キー方式の認証失敗", "config", "unauthorized", false,
			"シークレットキーまたはモデルの設定に問題があります。設定画面で確認してください。"},
		{"サインインの時間切れ", "config", "signin_timed_out", true,
			"サインインが完了しませんでした。もう一度サインインしてください。"},
		{"待ち受けを始められない", "config", "signin_unavailable", true,
			"サインインを始められませんでした。しばらく待ってから、もう一度サインインしてください。"},
		{"未知の Code（JSON-RPC の誤り応答）", "config", "rpc_-32600", false,
			"シークレットキーまたはモデルの設定に問題があります。設定画面で確認してください。"},
		{"未知の Code（一時的）", "transient", "responseStreamDisconnected", false,
			"通信が混み合っています。しばらく待って再実行してください。"},
		{"未知の Code（恒久的）", "permanent", "codex_unexpected_future", false,
			"送る内容が長すぎます。対話を分けるか、対象の文書を減らして再実行してください。"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := aiErrorMessage(c.class, c.code, c.signedIn, time.Time{})
			if got != c.want {
				t.Errorf("文言が違う:\n got: %q\nwant: %q", got, c.want)
			}
			if strings.Contains(got, c.code) {
				t.Errorf("生のコード値が画面の文言へ出ている: %q", got)
			}
		})
	}
}

// 残量切れの文言は回復時刻を添え、
// 分からないときは時刻の部分を省く。**自動では切り替えない**ことを設定の不変で確かめる。
func TestPlanUsageExhaustedMessageAndNoAutomaticSwitch(t *testing.T) {
	resets := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	stub := &fakeAccountAdapter{
		hasUsage: true,
		usage: aiprovider.PlanUsage{
			PlanType: "free", ReceivedAt: resets.Add(-time.Hour),
			Windows: []aiprovider.PlanUsageWindow{
				{LimitID: "codex", Scope: "primary", UsedPercent: 100,
					WindowDurationMins: 43200, ResetsAt: resets},
			},
		},
	}
	a := newSignInAPI(t, stub)
	before := writeSettings(t, a, []projectstore.ProviderSetting{
		signInProviderSetting(),
		{Label: "社内 Claude", Provider: "anthropic", Model: "claude-x", Effort: "standard",
			KeyRef: "anthropic/社内 Claude"},
	}, "ChatGPT")

	got := a.aiUserMessage("config", "plan_usage_exhausted")
	wantTime := resets.Local().Format("1月2日 15時04分")
	if !strings.Contains(got, wantTime) {
		t.Errorf("回復時刻が文言に無い（%q）: %q", wantTime, got)
	}
	for _, phrase := range []string{"利用枠を使い切りました", "認証方式または AIプロバイダを切り替え"} {
		if !strings.Contains(got, phrase) {
			t.Errorf("文言に %q が無い: %q", phrase, got)
		}
	}
	// 回復時刻が分からないときは時刻の部分を省く。
	noTime := aiErrorMessage("config", "plan_usage_exhausted", true, time.Time{})
	if strings.Contains(noTime, "の回復を待つ") {
		t.Errorf("回復時刻が不明なのに時刻の部分が残っている: %q", noTime)
	}
	// **設定は一切変わらない**（別のプロバイダ・別の認証方式へ自動で切り替えない）。
	after, err := os.ReadFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("残量切れで設定が変わった:\n前:\n%s\n後:\n%s", before, after)
	}
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.DefaultProvider != "ChatGPT" {
		t.Errorf("既定のプロバイダが切り替わった: %q", settings.DefaultProvider)
	}
	p, _ := settings.DefaultProviderSetting()
	if p.AuthMethodOrDefault() != "chatgpt_signin" {
		t.Errorf("認証方式が切り替わった: %q", p.AuthMethodOrDefault())
	}
}

// 認証方式の値は保存層と抽象化層で同じであること。
func TestAuthMethodValuesMatchAcrossLayers(t *testing.T) {
	if projectstore.AuthMethodSecretKey != string(aiprovider.AuthSecretKey) {
		t.Errorf("secret_key の値が食い違う: %q / %q",
			projectstore.AuthMethodSecretKey, aiprovider.AuthSecretKey)
	}
	if projectstore.AuthMethodChatGPTSignin != string(aiprovider.AuthChatGPTSignin) {
		t.Errorf("chatgpt_signin の値が食い違う: %q / %q",
			projectstore.AuthMethodChatGPTSignin, aiprovider.AuthChatGPTSignin)
	}
	if normalizedAuthMethod("") != "secret_key" {
		t.Errorf("未設定が secret_key へ倒れない: %q", normalizedAuthMethod(""))
	}
}

// waitForState はサインインの状態が期待どおりになるまで待つ（背景の待ち受けの完了を待つ）。
func waitForSignInState(t *testing.T, a *API, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.CodexSignInState().State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("状態が %q にならない（現在 %q）", want, a.CodexSignInState().State)
}

// **初期設定の段階**（アプリ設定にまだ要素が無い）でも、
// 画面で選んでいる認証方式でアダプタを作ってモデル一覧を取りに行く。
//
// 保存済みの値しか見ないと、サインイン方式は必ずシークレットキー方式として扱われ、
// キーが無いまま失敗して既知一覧へ縮退する（利用者には「取得できなかった」としか見えない）。
func TestModelsUsesSelectedAuthMethodBeforeSettingsExist(t *testing.T) {
	stub := &fakeAccountAdapter{models: []aiprovider.ModelInfo{{ID: "gpt-5.5", DisplayName: "GPT-5.5"}}}
	a := newSignInAPI(t, stub)

	list, err := a.Models("codex", "", string(aiprovider.AuthChatGPTSignin))
	if err != nil {
		t.Fatalf("モデル一覧を取得できない: %v", err)
	}
	if list.FromKnownList {
		t.Errorf("既知一覧へ縮退した（注意: %q）", list.Notice)
	}
	if len(list.Models) != 1 || list.Models[0].ID != "gpt-5.5" {
		t.Errorf("アダプタが返した一覧になっていない: %+v", list.Models)
	}
	if stub.authMethod != aiprovider.AuthChatGPTSignin {
		t.Errorf("アダプタへ渡した認証方式が違う: %q", stub.authMethod)
	}
}
