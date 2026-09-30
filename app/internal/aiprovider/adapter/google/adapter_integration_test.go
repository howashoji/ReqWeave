//go:build integration

// 結合テスト（アダプタ × HTTP）。実キー・実 API は使わない。

package google

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

const dummyKey = "dummy-google-key-1234"

var errKeyNotSet = errors.New("キーが未設定です")

type stubKeys struct{ err error }

func (s stubKeys) SecretKey(context.Context, aiprovider.KeyRef) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return dummyKey, nil
}

func newTestAdapter(t *testing.T, h http.HandlerFunc) *Adapter {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	a := New(stubKeys{}, "google/test")
	a.baseURL = srv.URL
	return a
}

// 認証は x-goog-api-key ヘッダ（クエリパラメータ方式は使わない = キーを URL に載せるとログに残りうるため）。
// 応答が inputTokenLimit / outputTokenLimit を含むため API 応答値を優先する。
func TestListModels(t *testing.T) {
	var gotHeaderKey, gotPath, gotQuery string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeaderKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[
			{"name":"models/gemini-3.7-flash","displayName":"Gemini 3.7 Flash","inputTokenLimit":1048576,"outputTokenLimit":65536},
			{"name":"models/future-model","displayName":"Future","inputTokenLimit":123,"outputTokenLimit":45}
		]}`))
	})

	models, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if gotHeaderKey != dummyKey {
		t.Errorf("x-goog-api-key ヘッダが違う: %q", gotHeaderKey)
	}
	if strings.Contains(gotQuery, dummyKey) {
		t.Errorf("キーがクエリパラメータに含まれる: %q", gotQuery)
	}
	if !strings.Contains(gotPath, "models") {
		t.Errorf("エンドポイントが違う: %q", gotPath)
	}
	if len(models) != 2 {
		t.Fatalf("件数が違う: %d %+v", len(models), models)
	}
	if models[0].ID != "gemini-3.7-flash" {
		t.Errorf("モデル ID の接頭辞が外れていない: %+v", models[0])
	}
	if models[0].ContextWindow != 1048576 || models[0].MaxOutput != 65536 {
		t.Errorf("API 応答値が使われていない: %+v", models[0])
	}
	if models[0].Tier != aiprovider.TierPrimary {
		t.Errorf("既知一覧の区分が補完されていない: %+v", models[0])
	}
	if models[1].Tier != aiprovider.TierOther {
		t.Errorf("既知一覧に無いモデルが TierOther でない: %+v", models[1])
	}
}

func TestVerifyKeySuccess(t *testing.T) {
	var gotPath string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"models/gemini-3.5-flash-lite","displayName":"Gemini 3.5 Flash-Lite","inputTokenLimit":1048576,"outputTokenLimit":65536}`))
	})
	if err := a.VerifyKey(context.Background()); err != nil {
		t.Fatalf("疎通確認に失敗: %v", err)
	}
	if !strings.Contains(gotPath, "gemini-3.5-flash-lite") {
		t.Errorf("既定モデルへの問い合わせでない: %q", gotPath)
	}
}

// Google API 標準の status で区分を分ける。
func TestVerifyKeyErrorClasses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   aiprovider.ErrorClass
	}{
		{"認証エラー", 401, `{"error":{"code":401,"message":"API key not valid","status":"UNAUTHENTICATED"}}`, aiprovider.ErrClassConfig},
		{"権限エラー", 403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, aiprovider.ErrClassConfig},
		{"レート制限", 429, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`, aiprovider.ErrClassTransient},
		{"サーバエラー", 503, `{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`, aiprovider.ErrClassTransient},
		{"入力不正", 400, `{"error":{"code":400,"message":"bad input","status":"INVALID_ARGUMENT"}}`, aiprovider.ErrClassPermanent},
		{"モデル不明", 404, `{"error":{"code":404,"message":"model not found","status":"NOT_FOUND"}}`, aiprovider.ErrClassConfig},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				w.Write([]byte(c.body))
			})
			err := a.VerifyKey(context.Background())
			if err == nil {
				t.Fatal("エラーにならなかった")
			}
			var pe *aiprovider.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("ProviderError でない: %T %v", err, err)
			}
			if pe.Class != c.want {
				t.Errorf("区分が違う: got %v, want %v（status=%q）", pe.Class, c.want, pe.Code)
			}
			if strings.Contains(pe.Error(), dummyKey) {
				t.Error("エラーにキーが含まれる")
			}
		})
	}
}

func TestNetworkFailureIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	a := New(stubKeys{}, "google/test")
	a.baseURL = srv.URL
	srv.Close()

	err := a.VerifyKey(context.Background())
	var pe *aiprovider.ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("ProviderError でない: %T %v", err, err)
	}
	if pe.Class != aiprovider.ErrClassTransient {
		t.Errorf("ネットワーク断が一時的でない: %v", pe.Class)
	}
}

func TestKeyNotSetShortCircuits(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	a := New(stubKeys{err: errKeyNotSet}, "google/test")
	a.baseURL = srv.URL
	if err := a.VerifyKey(context.Background()); err == nil {
		t.Fatal("キー未設定なのに成功した")
	}
	if called {
		t.Error("キー未設定なのに API を呼び出した")
	}
}
