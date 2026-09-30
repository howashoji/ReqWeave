package binding

// 本ファイルは AI 通信系のエラーカタログ（分類は一時的・設定・恒久的の 3 つのまま増やさない）。
//
// 抽象化層は分類（ErrorClass）と**プロバイダ固有コード**（ProviderError.Code）までを返し、
// 利用者向けの文言はここで作る（「原因＋次の行動」の日本語 1 文）。
//
//   - **未知の Code は分類ごとの既定の文言へ倒す**（`rpc_<code>` のように
//     Code は動的に増えうる）。生のコード値・内部の識別子を画面へ出さない。
//   - 文言の正本は本ファイルだけとし、画面側に同じ表を持たせない（二重管理の禁止）。
//     画面は `userMessage` をそのまま表示する。

import (
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// 分類ごとの既定の文言（担当者モード）。
const (
	msgTransientDefault = "通信が混み合っています。しばらく待って再実行してください。"
	msgConfigDefault    = "シークレットキーまたはモデルの設定に問題があります。設定画面で確認してください。"
	msgPermanentDefault = "送る内容が長すぎます。対話を分けるか、対象の文書を減らして再実行してください。"
)

// Codex App Server に固有の原因の文言（分類は増やさない）。
const (
	msgCodexGuardFailed = "Codex App Server を安全な設定で使えることを確認できなかったため、呼び出しを止めました。" +
		"別の AIプロバイダを選ぶか、アプリを最新版へ更新してください。"
	msgCodexProcessExited = "Codex App Server が応答しませんでした。もう一度実行してください。"
	msgCodexWorkspaceBusy = "別のウィンドウが Codex App Server を使っています。" +
		"そのウィンドウを閉じてから再実行してください。"
	msgSignInExpired     = "ChatGPT のアカウントのサインインが切れました。設定画面でサインインし直してください。"
	msgSignInTimedOut    = "サインインが完了しませんでした。もう一度サインインしてください。"
	msgSignInUnavailable = "サインインを始められませんでした。しばらく待ってから、もう一度サインインしてください。"
	// msgPlanUsageExhausted は回復時刻が分かるときに時刻を差し込む（分からないときは時刻の部分を省く）。
	msgPlanUsageExhaustedWithTime = "ChatGPT のプランの利用枠を使い切りました。%s の回復を待つか、" +
		"設定画面で認証方式または AIプロバイダを切り替えてください。"
	msgPlanUsageExhausted = "ChatGPT のプランの利用枠を使い切りました。" +
		"回復を待つか、設定画面で認証方式または AIプロバイダを切り替えてください。"
)

// AI 通信系の Code（正本はアダプタ。ここは文言を引くためのキーとして持つ）。
const (
	codeUnauthorized      = "unauthorized"
	codePlanUsageExausted = "plan_usage_exhausted"
	codeSignInTimedOut    = "signin_timed_out"
	codeSignInUnavailable = "signin_unavailable"
	codeCodexGuardFailed  = "codex_guard_failed"
	codeCodexToolAttempt  = "codex_tool_attempt"
	codeCodexUnexpected   = "codex_unexpected"
	codeCodexProcessExit  = "codex_process_exited"
	codeCodexWorkspaceBsy = "codex_workspace_busy"
)

// aiErrorMessage は分類 × Code から利用者向けの 1 文を作る。
//
// signedIn は「ChatGPT のアカウントでのサインインで使っているか」。`unauthorized` の意味が
// 認証方式で変わる（キーが無効 / サインインが切れた）ため、ここだけ分岐する。
// resetAt は残量切れの回復時刻（ゼロ値 = 不明。時刻の部分を省く）。
func aiErrorMessage(class, code string, signedIn bool, resetAt time.Time) string {
	switch code {
	case codeCodexGuardFailed, codeCodexToolAttempt, codeCodexUnexpected:
		return msgCodexGuardFailed
	case codeCodexProcessExit:
		return msgCodexProcessExited
	case codeCodexWorkspaceBsy:
		return msgCodexWorkspaceBusy
	case codePlanUsageExausted:
		if resetAt.IsZero() {
			return msgPlanUsageExhausted
		}
		return strings.Replace(msgPlanUsageExhaustedWithTime, "%s", formatRecoveryTime(resetAt), 1)
	case codeSignInTimedOut:
		return msgSignInTimedOut
	case codeSignInUnavailable:
		return msgSignInUnavailable
	case codeUnauthorized:
		if signedIn {
			return msgSignInExpired
		}
	}
	// 未知の Code・分岐に当たらなかったものは分類ごとの既定へ倒す。
	switch class {
	case aiprovider.ErrClassTransient.String():
		return msgTransientDefault
	case aiprovider.ErrClassPermanent.String():
		return msgPermanentDefault
	default:
		return msgConfigDefault
	}
}

// formatRecoveryTime は回復時刻を利用者の地域の時刻で「◯月◯日 ◯時◯分」に整える
// （保存・受け渡しは UTC、表示はローカル）。
func formatRecoveryTime(t time.Time) string {
	return t.Local().Format("1月2日 15時04分")
}

// aiUserMessage は現在の設定（認証方式・保持している残量）を添えて文言を作る。
func (a *API) aiUserMessage(class, code string) string {
	if class == "" && code == "" {
		return ""
	}
	signedIn := usesSignIn(a.currentAuthMethod())
	var resetAt time.Time
	if code == codePlanUsageExausted {
		resetAt = a.earliestPlanRecovery()
	}
	return aiErrorMessage(class, code, signedIn, resetAt)
}

// currentAuthMethod は既定のプロバイダ設定の認証方式を返す（読めないときは空 = シークレットキー方式）。
func (a *API) currentAuthMethod() string {
	settings, err := a.settings()
	if err != nil {
		return ""
	}
	p, ok := settings.DefaultProviderSetting()
	if !ok {
		return ""
	}
	return p.AuthMethodOrDefault()
}

// earliestPlanRecovery は保持している残量のうち最も早い回復時刻を返す（無ければゼロ値）。
//
// **取得のための通信は起こさない**（保持済みの値だけを見る）。
func (a *API) earliestPlanRecovery() time.Time {
	usage, ok := a.planUsage(a.currentProviderLabel())
	if !ok {
		return time.Time{}
	}
	var earliest time.Time
	for _, w := range usage.Windows {
		if w.ResetsAt.IsZero() {
			continue
		}
		if earliest.IsZero() || w.ResetsAt.Before(earliest) {
			earliest = w.ResetsAt
		}
	}
	return earliest
}

// currentProviderLabel は既定のプロバイダ設定の表示名を返す。
func (a *API) currentProviderLabel() string {
	settings, err := a.settings()
	if err != nil {
		return ""
	}
	p, ok := settings.DefaultProviderSetting()
	if !ok {
		return ""
	}
	return p.Label
}
