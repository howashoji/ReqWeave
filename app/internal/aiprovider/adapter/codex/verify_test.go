package codex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// 疎通確認は 2 段（起動後の検査 → モデル一覧でキーが通るか）。
func TestVerifyKeyPassesBothStages(t *testing.T) {
	newFakeEnv(t, fakeScenario{})

	var authorization string
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/models" || r.Method != http.MethodGet {
			t.Errorf("疎通確認の呼び出しが違う: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	adapter := newTestAdapter(t)
	adapter.verifyBaseURL = server.URL

	if err := adapter.VerifyKey(context.Background()); err != nil {
		t.Fatalf("疎通確認に失敗した: %v", err)
	}
	if calls != 1 {
		t.Errorf("モデル一覧の呼び出し = %d 回, want 1（トークンを消費しない最小の呼び出し）", calls)
	}
	if authorization != "Bearer "+dummyKey {
		t.Errorf("認証ヘッダが違う: %q", authorization)
	}
	// 確認に使った子プロセスは残さない（保存前のキーで起動している場合があるため）。
	waitFor(t, func() bool { return procManager.current() == nil }, "疎通確認の子プロセスが残っている")
}

// 2 段目で弾かれたら設定起因（キーを確認して登録し直す導線を出す）。
func TestVerifyKeyRejectsInvalidKey(t *testing.T) {
	newFakeEnv(t, fakeScenario{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := newTestAdapter(t)
	adapter.verifyBaseURL = server.URL

	err := adapter.VerifyKey(context.Background())
	var pErr *aiprovider.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("正規化エラーでない: %v", err)
	}
	if pErr.Class != aiprovider.ErrClassConfig || pErr.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("分類・状態コードが違う: %v / %d", pErr.Class, pErr.HTTPStatus)
	}
}

// 1 段目（起動後の検査）で落ちたら、2 段目を呼ばずに恒久的エラーで返す
// （設定画面では成功したのに最初の対話で止まる状態を作らない）。
func TestVerifyKeyFailsWhenGuardFails(t *testing.T) {
	newFakeEnv(t, fakeScenario{Version: "0.999.0"})

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := newTestAdapter(t)
	adapter.verifyBaseURL = server.URL

	err := adapter.VerifyKey(context.Background())
	var pErr *aiprovider.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("正規化エラーでない: %v", err)
	}
	if pErr.Class != aiprovider.ErrClassPermanent || pErr.Code != codeGuardFailed {
		t.Errorf("分類・Code が違う: %v / %q", pErr.Class, pErr.Code)
	}
	if calls != 0 {
		t.Errorf("検査に落ちたのにキーを送った（%d 回）", calls)
	}
	if strings.Contains(pErr.Message, dummyKey) {
		t.Error("エラーの文言にキーが含まれている")
	}
}

// キーを取得できないとき（未登録）は、その理由をそのまま返す（バインディングが扱う）。
func TestVerifyKeyWithoutKey(t *testing.T) {
	newFakeEnv(t, fakeScenario{})
	wantErr := errors.New("キーが未設定です")
	adapter := NewWithOptions(stubKeys{err: wantErr}, "codex/テスト", aiprovider.AdapterOptions{})

	if err := adapter.VerifyKey(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("キー未設定の理由が伝わらない: %v", err)
	}
}
