package codex

// 本ファイルは偽の Codex App Server（fake_codex_test.go）の、サインインと残量の振る舞い。
//
// JSON の形は同梱版（bundle.json の版）の生成スキーマ `codex app-server generate-json-schema` の
// 出力で確かめた実際の形に合わせる（LoginAccountResponse / AccountLoginCompletedNotification /
// GetAccountResponse / GetAccountRateLimitsResponse / RateLimitSnapshot / CancelLoginAccountResponse）。
//
// **目印の値は指示書（scenario.json）に書かず、ここに定数で持つ**。
// 「残量がファイルへ書かれない」ことを全ファイル検索で確かめるとき、指示書そのものに
// 目印が含まれていると検索が常に当たってしまう（テストの足場が偽赤を作る）ため。

import (
	"encoding/json"
	"time"
)

// 目印の値（全ファイル検索・動作ログの検査で使う。いずれも実在しない値）。
const (
	fakeLoginID         = "login-marker-4271"
	fakeAuthURL         = "https://auth.openai.com/oauth/authorize?client_id=app_TEST&originator=reqweave"
	fakeAccountEmail    = "user-marker-8834@example.invalid"
	fakeRateLimitName   = "rw-usage-marker-9173"
	fakeRateLimitResets = int64(1789123456) // 2026-09-11T11:10:56Z
	fakeUsedPercent     = 42
	// fakeOtherResets は 2 つ目の枠の回復時刻（1 時間後）。
	fakeOtherResets = fakeRateLimitResets + 3600
)

// fakeAccount はサインインと残量の振る舞い（scenario.json の account）。
type fakeAccount struct {
	// LoginOutcome は `account/login/start`（chatgpt）の後に届ける完了:
	// "success"（既定）/ "timeout"（Codex の 15 分の時間切れと同じ形）/ "none"（届けない）。
	LoginOutcome string `json:"login_outcome"`
	// LoginDelayMs は完了を届けるまでの時間。
	LoginDelayMs int `json:"login_delay_ms"`
	// PlanType は `account/read` と残量が返すプラン種別（空なら free）。
	PlanType string `json:"plan_type"`
	// UpdatesDuringTurn は対話の途中で `account/rateLimits/updated` を届けるか。
	UpdatesDuringTurn bool `json:"updates_during_turn"`
	// UpdatedUsedPercent は途中で届ける使用率。
	UpdatedUsedPercent int `json:"updated_used_percent"`
}

// fakeSignedIn は fake のプロセス内のサインイン状態（fake は別プロセスで動くため、パッケージ変数で足りる）。
var fakeSignedIn bool

func (s *fakeState) planType() string {
	if s.scenario.Account.PlanType != "" {
		return s.scenario.Account.PlanType
	}
	return "free"
}

// handleAccount はサインイン・残量の要求を扱う。扱ったら true。
func (s *fakeState) handleAccount(id int64, msg rpcMessage) bool {
	switch msg.Method {
	case "account/login/start":
		var params struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		if params.Type != "chatgpt" {
			return false // apiKey は従来の振る舞い（fake_codex_test.go）
		}
		s.reply(id, map[string]any{"type": "chatgpt", "loginId": fakeLoginID, "authUrl": fakeAuthURL})
		go s.completeLogin()
		return true
	case "account/login/cancel":
		s.reply(id, map[string]any{"status": "canceled"})
		return true
	case "account/logout":
		fakeSignedIn = false
		s.reply(id, map[string]any{})
		return true
	case "account/read":
		if !fakeSignedIn {
			return false // 未サインインは従来の振る舞い（scenario.AccountType）
		}
		s.reply(id, map[string]any{
			"account": map[string]any{
				"type": "chatgpt", "planType": s.planType(), "email": fakeAccountEmail,
			},
			"requiresOpenaiAuth": true,
		})
		return true
	case "account/rateLimits/read":
		if !fakeSignedIn {
			// 実バイナリと同じ誤り応答（実測）。
			s.write(rpcMessage{ID: &id, Error: &rpcError{Code: -32600,
				Message: "codex account authentication required to read rate limits"}})
			return true
		}
		s.reply(id, s.rateLimitsPayload(fakeUsedPercent))
		return true
	}
	return false
}

// completeLogin は指示どおりに `account/login/completed` を届ける。
func (s *fakeState) completeLogin() {
	account := s.scenario.Account
	if account.LoginDelayMs > 0 {
		time.Sleep(time.Duration(account.LoginDelayMs) * time.Millisecond)
	}
	switch account.LoginOutcome {
	case "none":
		return
	case "timeout":
		s.notify("account/login/completed", map[string]any{
			"loginId": fakeLoginID, "success": false, "error": "Login timed out",
		})
	default:
		fakeSignedIn = true
		s.notify("account/login/completed", map[string]any{
			"loginId": fakeLoginID, "success": true, "error": nil,
		})
	}
}

// codexSnapshot は 1 つ目の枠（limitId = codex。実機の Free プランと同じ 30 日枠の形。実測）。
func (s *fakeState) codexSnapshot(used int) map[string]any {
	return map[string]any{
		"limitId": "codex", "limitName": fakeRateLimitName, "planType": s.planType(),
		"primary": map[string]any{
			"usedPercent": used, "windowDurationMins": 43200, "resetsAt": fakeRateLimitResets,
		},
		"secondary":            nil,
		"credits":              map[string]any{"hasCredits": false, "unlimited": false},
		"rateLimitReachedType": nil,
	}
}

// rateLimitsPayload は `account/rateLimits/read` の応答（複数の枠を持つ形）。
func (s *fakeState) rateLimitsPayload(used int) map[string]any {
	codex := s.codexSnapshot(used)
	other := map[string]any{
		"limitId": "codex_other", "limitName": nil, "planType": s.planType(),
		"primary": map[string]any{
			"usedPercent": 5, "windowDurationMins": 300, "resetsAt": fakeOtherResets,
		},
		"secondary": map[string]any{
			"usedPercent": 1, "windowDurationMins": 10080, "resetsAt": nil,
		},
	}
	return map[string]any{
		"rateLimits":          codex,
		"rateLimitsByLimitId": map[string]any{"codex": codex, "codex_other": other},
	}
}

// emitRateLimitsDuringTurn は対話の途中で残量の通知を届ける（指示があるときだけ）。
func (s *fakeState) emitRateLimitsDuringTurn() {
	account := s.scenario.Account
	if !account.UpdatesDuringTurn {
		return
	}
	s.notify("account/rateLimits/updated", map[string]any{
		"rateLimits": s.codexSnapshot(account.UpdatedUsedPercent),
	})
}
