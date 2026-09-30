package codex

import (
	"encoding/json"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// Codex から届く誤りの種類ごとに、分類と Code が定まること（表の全行）。
func TestNormalizeTurnErrorTable(t *testing.T) {
	cases := []struct {
		name    string
		info    string
		message string
		class   aiprovider.ErrorClass
		code    string
		status  int
	}{
		{"コンテキスト長超過", `"contextWindowExceeded"`, "too long", aiprovider.ErrClassPermanent, "context_window_exceeded", 0},
		{"プランの残量切れ", `"usageLimitExceeded"`, "limit", aiprovider.ErrClassConfig, "plan_usage_exhausted", 0},
		{"認証の失効", `"unauthorized"`, "unauthorized", aiprovider.ErrClassConfig, "unauthorized", 0},
		{"要求の不正", `"badRequest"`, "bad", aiprovider.ErrClassConfig, "bad_request", 0},
		{"過負荷", `"serverOverloaded"`, "busy", aiprovider.ErrClassTransient, "serverOverloaded", 0},
		{"サーバ内部エラー", `"internalServerError"`, "oops", aiprovider.ErrClassTransient, "internalServerError", 0},
		{"内容の方針による拒否", `"cyberPolicy"`, "blocked", aiprovider.ErrClassPermanent, "content_policy", 0},
		{"接続の切断", `"other"`, "stream disconnected before completion", aiprovider.ErrClassTransient, "other", 0},
		{"判別不能（HTTP 400 等）", `"other"`, `{"error":{"message":"bad"}}`, aiprovider.ErrClassConfig, "other", 0},
		{"サンドボックス", `"sandboxError"`, "sandbox", aiprovider.ErrClassPermanent, codeUnexpected, 0},
		{"巻き戻しの失敗", `"threadRollbackFailed"`, "rollback", aiprovider.ErrClassPermanent, codeUnexpected, 0},
		{"操作できないターン", `{"activeTurnNotSteerable":{"turnKind":"review"}}`, "steer", aiprovider.ErrClassPermanent, codeUnexpected, 0},
		{"未知の種類", `"brandNewFailure"`, "unknown", aiprovider.ErrClassPermanent, codeUnexpected, 0},
		{"接続の失敗（401）", `{"httpConnectionFailed":{"httpStatusCode":401}}`, "auth", aiprovider.ErrClassConfig, "httpConnectionFailed", 401},
		{"接続の失敗（429）", `{"httpConnectionFailed":{"httpStatusCode":429}}`, "rate", aiprovider.ErrClassTransient, "httpConnectionFailed", 429},
		{"応答の切断（500）", `{"responseStreamDisconnected":{"httpStatusCode":500}}`, "cut", aiprovider.ErrClassTransient, "responseStreamDisconnected", 500},
		{"応答の接続失敗（状態コード無し）", `{"responseStreamConnectionFailed":{"httpStatusCode":null}}`, "x", aiprovider.ErrClassTransient, "responseStreamConnectionFailed", 0},
		{"再試行の上限（403）", `{"responseTooManyFailedAttempts":{"httpStatusCode":403}}`, "y", aiprovider.ErrClassConfig, "responseTooManyFailedAttempts", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeTurnError(&turnError{Message: c.message, CodexErrorInfo: json.RawMessage(c.info)})
			if got.Class != c.class {
				t.Errorf("分類 = %v, want %v", got.Class, c.class)
			}
			if got.Code != c.code {
				t.Errorf("Code = %q, want %q", got.Code, c.code)
			}
			if got.HTTPStatus != c.status {
				t.Errorf("HTTP の状態コード = %d, want %d", got.HTTPStatus, c.status)
			}
			if got.Provider != ProviderCodex {
				t.Errorf("プロバイダが違う: %q", got.Provider)
			}
			if got.Message != c.message {
				t.Errorf("Codex の message が伝わっていない: %q", got.Message)
			}
		})
	}
}

// 原因が分からない失敗は一時的として扱う（黙って落とさない）。
func TestNormalizeTurnErrorWithoutInfo(t *testing.T) {
	got := normalizeTurnError(nil)
	if got.Class != aiprovider.ErrClassTransient || got.Code != codeUnexpected {
		t.Errorf("分類・Code が違う: %v / %q", got.Class, got.Code)
	}
}

// JSON-RPC の誤り応答は設定起因（Code は動的に作る）。
func TestNormalizeRPCError(t *testing.T) {
	got := normalizeRPCError(&rpcError{Code: -32600, Message: "codex account authentication required"})
	if got.Class != aiprovider.ErrClassConfig {
		t.Errorf("分類 = %v, want config", got.Class)
	}
	if got.Code != "rpc_-32600" {
		t.Errorf("Code = %q", got.Code)
	}
}

// エラーの文言にキーの形の文字列を残さない（マスキングの最終防衛線を通す）。
func TestErrorMessagesAreMasked(t *testing.T) {
	got := normalizeTurnError(&turnError{
		Message:        "invalid api key: sk-proj-ABCDEFGHIJKLMNOPQRSTUVWX",
		CodexErrorInfo: json.RawMessage(`"unauthorized"`),
	})
	if got.Message == "" || contains(got.Message, "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWX") {
		t.Errorf("キーの形の文字列が残っている: %q", got.Message)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
