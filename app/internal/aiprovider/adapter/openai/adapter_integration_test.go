//go:build integration

// 結合テスト（アダプタ × HTTP）。実キー・実 API は使わない。

package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

const dummyKey = "dummy-openai-key-1234"

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
	a := New(stubKeys{}, "openai/test")
	a.baseURL = srv.URL + "/v1"
	return a
}

// 認証ヘッダは Authorization: Bearer。応答にコンテキスト長が無いため既知一覧で補完する。
func TestListModels(t *testing.T) {
	var gotAuth, gotPath string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[
			{"id":"gpt-5.6-sol","object":"model","created":1,"owned_by":"openai"},
			{"id":"future-model","object":"model","created":1,"owned_by":"openai"}
		]}`))
	})

	models, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if gotAuth != "Bearer "+dummyKey {
		t.Errorf("Authorization ヘッダが違う: %q", gotAuth)
	}
	if !strings.Contains(gotPath, "/v1/models") {
		t.Errorf("エンドポイントが違う: %q", gotPath)
	}
	if len(models) != 2 {
		t.Fatalf("件数が違う: %d %+v", len(models), models)
	}
	known, _ := aiprovider.KnownModel(aiprovider.ProviderOpenAI, "gpt-5.6-sol")
	if models[0].ContextWindow != known.ContextWindow || models[0].MaxOutput != known.MaxOutput {
		t.Errorf("既知一覧で補完されていない: %+v", models[0])
	}
	if models[0].Tier != aiprovider.TierPrimary {
		t.Errorf("区分が補完されていない: %+v", models[0])
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
		w.Write([]byte(`{"id":"gpt-5.6-luna","object":"model","created":1,"owned_by":"openai"}`))
	})
	if err := a.VerifyKey(context.Background()); err != nil {
		t.Fatalf("疎通確認に失敗: %v", err)
	}
	if !strings.Contains(gotPath, "gpt-5.6-luna") {
		t.Errorf("既定モデルへの問い合わせでない: %q", gotPath)
	}
}

// 429 はレート制限と残高不足の両義。code で区分を分ける。
func TestVerifyKeyErrorClasses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   aiprovider.ErrorClass
	}{
		{"認証エラー", 401, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`, aiprovider.ErrClassConfig},
		{"レート制限", 429, `{"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"rate_limit_exceeded"}}`, aiprovider.ErrClassTransient},
		{"残高不足", 429, `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`, aiprovider.ErrClassConfig},
		{"サーバエラー", 500, `{"error":{"message":"server error","type":"server_error","code":"server_error"}}`, aiprovider.ErrClassTransient},
		{"入力不正", 400, `{"error":{"message":"bad input","type":"invalid_request_error","code":"invalid_value","param":"messages"}}`, aiprovider.ErrClassPermanent},
		{"モデル指定不正", 400, `{"error":{"message":"model not found","type":"invalid_request_error","code":"model_not_found","param":"model"}}`, aiprovider.ErrClassConfig},
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
				t.Errorf("区分が違う: got %v, want %v（code=%q）", pe.Class, c.want, pe.Code)
			}
			if strings.Contains(pe.Error(), dummyKey) {
				t.Error("エラーにキーが含まれる")
			}
		})
	}
}

func TestNetworkFailureIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	a := New(stubKeys{}, "openai/test")
	a.baseURL = srv.URL + "/v1"
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
	a := New(stubKeys{err: errKeyNotSet}, "openai/test")
	a.baseURL = srv.URL + "/v1"
	if err := a.VerifyKey(context.Background()); err == nil {
		t.Fatal("キー未設定なのに成功した")
	}
	if called {
		t.Error("キー未設定なのに API を呼び出した")
	}
}
