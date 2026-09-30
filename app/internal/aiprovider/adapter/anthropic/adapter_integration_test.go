//go:build integration

// 結合テスト（アダプタ × HTTP。テスト用サーバへ接続して認証ヘッダ・応答解釈・エラー正規化を確認）。
// 実キー・実 API は使わない。

package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

const dummyKey = "dummy-anthropic-key-1234"

type stubKeys struct{ err error }

func (s stubKeys) SecretKey(context.Context, aiprovider.KeyRef) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return dummyKey, nil
}

// newTestAdapter はテスト用サーバへ向けたアダプタを返す（baseURL の差し替えはパッケージ内のみ）。
func newTestAdapter(t *testing.T, h http.HandlerFunc) *Adapter {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	a := New(stubKeys{}, "anthropic/test")
	a.baseURL = srv.URL
	return a
}

// 認証ヘッダは x-api-key。応答を既知一覧で補完する。
func TestListModels(t *testing.T) {
	var gotAuth, gotVersion, gotPath string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
			{"type":"model","id":"claude-opus-5","display_name":"Claude Opus 5","created_at":"2026-01-01T00:00:00Z","max_input_tokens":1000000,"max_tokens":128000,"capabilities":{}},
			{"type":"model","id":"future-model","display_name":"Future","created_at":"2026-01-01T00:00:00Z","max_input_tokens":0,"max_tokens":0,"capabilities":{}}
		],"has_more":false,"first_id":null,"last_id":null}`))
	})

	models, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if gotAuth != dummyKey {
		t.Errorf("x-api-key ヘッダが違う: %q", gotAuth)
	}
	if gotVersion == "" {
		t.Error("anthropic-version ヘッダが無い")
	}
	if !strings.Contains(gotPath, "/v1/models") {
		t.Errorf("エンドポイントが違う: %q", gotPath)
	}
	if len(models) != 2 {
		t.Fatalf("件数が違う: %d %+v", len(models), models)
	}
	if models[0].ID != "claude-opus-5" || models[0].ContextWindow != 1000000 || models[0].MaxOutput != 128000 {
		t.Errorf("応答値が反映されていない: %+v", models[0])
	}
	if models[0].Tier != aiprovider.TierPrimary {
		t.Errorf("既知一覧の区分が補完されていない: %+v", models[0])
	}
	if models[1].Tier != aiprovider.TierOther {
		t.Errorf("既知一覧に無いモデルが TierOther でない: %+v", models[1])
	}
}

// 疎通確認の成否。
func TestVerifyKeySuccess(t *testing.T) {
	var gotPath string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"type":"model","id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5","created_at":"2026-01-01T00:00:00Z","max_input_tokens":200000,"max_tokens":64000,"capabilities":{}}`))
	})
	if err := a.VerifyKey(context.Background()); err != nil {
		t.Fatalf("疎通確認に失敗: %v", err)
	}
	if !strings.Contains(gotPath, "claude-haiku-4-5-20251001") {
		t.Errorf("既定モデルへの問い合わせでない: %q", gotPath)
	}
}

// HTTP ステータスからの区分。
func TestVerifyKeyErrorClasses(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   aiprovider.ErrorClass
	}{
		{401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, aiprovider.ErrClassConfig},
		{403, `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`, aiprovider.ErrClassConfig},
		{429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, aiprovider.ErrClassTransient},
		{500, `{"type":"error","error":{"type":"api_error","message":"oops"}}`, aiprovider.ErrClassTransient},
		{529, `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`, aiprovider.ErrClassTransient},
		{400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad input"}}`, aiprovider.ErrClassPermanent},
		{400, `{"type":"error","error":{"type":"not_found_error","message":"model not found"}}`, aiprovider.ErrClassConfig},
	}
	for _, c := range cases {
		a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		})
		err := a.VerifyKey(context.Background())
		if err == nil {
			t.Errorf("status %d: エラーにならなかった", c.status)
			continue
		}
		var pe *aiprovider.ProviderError
		if !asProviderError(err, &pe) {
			t.Errorf("status %d: ProviderError でない: %T %v", c.status, err, err)
			continue
		}
		if pe.Class != c.want {
			t.Errorf("status %d (%s): got %v, want %v", c.status, pe.Code, pe.Class, c.want)
		}
		if pe.HTTPStatus != c.status {
			t.Errorf("status %d: 記録されたステータスが違う: %d", c.status, pe.HTTPStatus)
		}
		// エラーにキー本体を含めない
		if strings.Contains(pe.Error(), dummyKey) {
			t.Errorf("status %d: エラーにキーが含まれる", c.status)
		}
	}
}

// 接続できない場合は一時的エラー（ネットワーク断）。
func TestNetworkFailureIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	a := New(stubKeys{}, "anthropic/test")
	a.baseURL = srv.URL
	srv.Close() // 接続先を落とす

	err := a.VerifyKey(context.Background())
	if err == nil {
		t.Fatal("接続できないのに成功した")
	}
	var pe *aiprovider.ProviderError
	if !asProviderError(err, &pe) {
		t.Fatalf("ProviderError でない: %T %v", err, err)
	}
	if pe.Class != aiprovider.ErrClassTransient {
		t.Errorf("ネットワーク断が一時的でない: %v", pe.Class)
	}
	if strings.Contains(pe.Error(), dummyKey) {
		t.Error("エラーにキーが含まれる")
	}
}

// キー未設定（キーマネージャのエラー）はそのまま返し、API を呼ばない。
func TestKeyNotSetShortCircuits(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	a := New(stubKeys{err: errKeyNotSet}, "anthropic/test")
	a.baseURL = srv.URL

	if err := a.VerifyKey(context.Background()); err == nil {
		t.Fatal("キー未設定なのに成功した")
	}
	if called {
		t.Error("キー未設定なのに API を呼び出した")
	}
}
