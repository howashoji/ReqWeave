//go:build integration

package binding

// サインイン方式（ChatGPT のアカウントでのサインイン）の結合テスト。
//
// **OS セキュアストレージを実際に使う**（OS のセキュアストレージへ直接つなぐ方式をモックで置き換えない）。ここで確かめるのは「項目を**作らない**こと」である。

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newSignInIntegrationAPI は実際の OS セキュアストレージを使う API を返す（アダプタだけ代役）。
func newSignInIntegrationAPI(t *testing.T, stub *fakeAccountAdapter) (*API, string) {
	t.Helper()
	label := keytest.Marker + uuid.NewString()
	a := &API{
		paths: projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)},
		keys:  keymanager.New(),
		newAdapter: func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
			opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
			stub.mu.Lock()
			stub.authMethod = opts.AuthMethod
			stub.mu.Unlock()
			return stub, nil
		},
	}
	original := openURLFn
	openURLFn = func(_ context.Context, url string) {}
	t.Cleanup(func() { openURLFn = original })
	// 取り違えて項目を作ってしまった場合にも、確実に片づける（テストが中断されても残骸を残さない）。
	keytest.TrackKey(t, keymanager.Ref{ProviderID: "codex", Label: label})
	return a, label
}

// `chatgpt_signin` の設定は `key_ref` を持たず、
// **キーマネージャ（OS セキュアストレージ）の項目も作らない**
// （認証情報は Codex が自身の項目として保管し、本システムは読み書きしない）。
func TestSignInSetupCreatesNoKeyManagerEntry(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn:  aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		account: aiprovider.AccountInfo{SignedIn: true, Type: "chatgpt", PlanType: "free"},
	}
	a, label := newSignInIntegrationAPI(t, stub)

	if _, err := a.StartCodexSignIn(label); err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	waitForSignInState(t, a, SignInStateSignedIn)

	err := a.CompleteSetup(SetupRequest{
		ProviderID: "codex", Label: label, Model: "gpt-5.5", Effort: "standard",
		AuthMethod: "chatgpt_signin", AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤",
	})
	if runtime.GOOS != "darwin" {
		// Codex App Server を選べるのは macOS だけ（Windows 実機での検証が済むまで提供しない）。
		if err == nil {
			t.Fatal("macOS 以外で Codex App Server が選べてしまう")
		}
		return
	}
	if err != nil {
		t.Fatalf("初期設定を確定できない: %v", err)
	}

	ref := keymanager.Ref{ProviderID: "codex", Label: label}
	exists, err := a.keys.Exists(ref)
	if err != nil {
		t.Fatalf("キーマネージャの項目を確認できない: %v", err)
	}
	if exists {
		t.Errorf("サインイン方式なのに OS セキュアストレージへ項目が作られた: %s", ref.String())
	}
	body, err := os.ReadFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "key_ref") {
		t.Errorf("サインイン方式の設定に key_ref が保存された:\n%s", body)
	}

	// 設定画面の表示: 認証方式のラベルが出て、キーの状態は問い合わせない。
	view, err := a.SettingsView()
	if err != nil {
		t.Fatalf("設定画面の内容を取得できない: %v", err)
	}
	if len(view.Providers) != 1 {
		t.Fatalf("プロバイダ設定の件数が違う: %d", len(view.Providers))
	}
	p := view.Providers[0]
	if p.AuthMethod != "chatgpt_signin" || p.AuthMethodLabel != "ChatGPT のアカウントでのサインイン" {
		t.Errorf("認証方式の表示が違う: %+v", p)
	}
	if p.KeyState != "—" || p.KeyMasked != "—" {
		t.Errorf("サインイン方式でキーの状態が表示されている: %+v", p)
	}
	if !view.AIReady {
		t.Errorf("サインイン済みなのに AI 機能がブロックされている: %q", view.AIBlockedReason)
	}
}

// サインインの失敗では**設定が変わらない**
// （疎通確認に失敗したキーを保存しないのと同じ規律）。
func TestSignInFailureDoesNotCompleteSetup(t *testing.T) {
	stub := &fakeAccountAdapter{
		signIn: aiprovider.SignIn{LoginID: "login-1", AuthorizationURL: testAuthURL},
		waitErr: &aiprovider.ProviderError{
			Class: aiprovider.ErrClassConfig, Code: "signin_timed_out", Message: "Login timed out",
		},
	}
	a, label := newSignInIntegrationAPI(t, stub)

	if _, err := a.StartCodexSignIn(label); err != nil {
		t.Fatalf("サインインを開始できない: %v", err)
	}
	waitForSignInState(t, a, SignInStateFailed)

	err := a.CompleteSetup(SetupRequest{
		ProviderID: "codex", Label: label, Model: "gpt-5.5", Effort: "standard",
		AuthMethod: "chatgpt_signin", AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤",
	})
	if err == nil {
		t.Fatal("サインインしていないのに初期設定が確定できた")
	}
	if _, statErr := os.Stat(a.paths.SettingsFile()); statErr == nil {
		t.Error("サインインの失敗でアプリ設定が書かれた（設定は変えない）")
	}
}
