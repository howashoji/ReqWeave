package aiprovider

// 本ファイルは抽象化層への追加（任意のインタフェース）。
//
// サインイン・サインアウト・アカウントの情報・プランの残量は Codex App Server だけの機能のため、
// ProviderAdapter には足さず、**アダプタが実装してよい任意のインタフェース**として
// ここに定義する。上位（公開バインディング層）は型の判定で実装の有無を確かめる。
// 既存 3 社のアダプタは変更しない。

import (
	"context"
	"time"
)

// AuthMethod は認証方式（アプリ設定の `providers[].auth_method`）。
//
// 値の正本は本型で、保存層（projectstore）は同じ 2 値だけを受理する。
type AuthMethod string

const (
	// AuthSecretKey はシークレットキー方式（未設定はこれとみなす）。
	AuthSecretKey AuthMethod = "secret_key"
	// AuthChatGPTSignin は ChatGPT のアカウントでのサインイン。
	AuthChatGPTSignin AuthMethod = "chatgpt_signin"
)

// NormalizeAuthMethod は未設定をシークレットキー方式へ倒す。
func NormalizeAuthMethod(m AuthMethod) AuthMethod {
	if m == "" {
		return AuthSecretKey
	}
	return m
}

// IsValidAuthMethod は保存・利用してよい認証方式かを返す（未設定も可 = secret_key 扱い）。
func IsValidAuthMethod(m AuthMethod) bool {
	switch m {
	case "", AuthSecretKey, AuthChatGPTSignin:
		return true
	default:
		return false
	}
}

// SignIn はサインインの開始の結果。
//
// **アダプタは認可の URL を返すだけで、既定のブラウザで開くのはバインディング層**
// （外部のプログラムを起動する層を分ける。依存規則の機械検査 = tools/depcheck）。
type SignIn struct {
	// LoginID は待ち受け・取り消しで使う識別子（Codex の `loginId`）。
	LoginID string
	// AuthorizationURL は利用者がブラウザで開く認可の URL（Codex の `authUrl`）。
	AuthorizationURL string
}

// AccountInfo はアカウントの情報。値は**生のコード**であり、
// 画面へ出す前に必ずラベルへ写す（未知は「不明」）。
type AccountInfo struct {
	// SignedIn はサインイン済みか（Codex が認証情報を持っているか）。
	SignedIn bool
	// Type は認証の種類（`chatgpt` / `apiKey` など）。
	Type string
	// PlanType はプラン種別（`free` / `plus` / … / `unknown`）。
	PlanType string
	// Email はアカウントのメールアドレス。**表示のみ**に使い、
	// アプリ設定・プロジェクトデータ・動作ログへ保存しない（個人情報を手元に残さないため）。
	Email string
}

// AccountAdapter はサインインを持つアダプタが実装する任意のインタフェース。
type AccountAdapter interface {
	// StartSignIn はサインインを開始し、認可の URL を返す（ブラウザは開かない）。
	StartSignIn(ctx context.Context) (SignIn, error)
	// WaitSignIn は完了の通知を待つ。時間切れ・失敗は ProviderError（設定起因）を返す。
	// 成功時はアカウントの情報と残量の取得（1 回だけ）まで済ませる。
	WaitSignIn(ctx context.Context, loginID string) (AccountInfo, error)
	// CancelSignIn は待ち受けを取り消す（利用者の操作。エラーとして表示しない）。
	CancelSignIn(ctx context.Context, loginID string) error
	// SignOut はサインアウトする（OS セキュアストレージの Codex の項目が消える）。
	SignOut(ctx context.Context) error
	// Account は現在のアカウントの情報を返す（未サインインは SignedIn = false）。
	Account(ctx context.Context) (AccountInfo, error)
}

// PlanUsageWindow は 1 つの枠の残量。
//
// **枠の本数・長さはプランで異なる**ため、特定の長さ（5 時間等）を前提にしない。
type PlanUsageWindow struct {
	// LimitID は複数の枠があるときの枠の識別子（`rateLimitsByLimitId` のキー。単一のときは空）。
	LimitID string
	// LimitName は Codex が付けた枠の名前（空のことがある）。
	LimitName string
	// Scope は同じ枠の中の区分（`primary` / `secondary`）。
	Scope string
	// UsedPercent は使用率（0〜100）。
	UsedPercent int
	// WindowDurationMins は枠の長さ（分）。0 = 不明。
	WindowDurationMins int
	// ResetsAt は次の回復時刻（UTC）。ゼロ値 = 不明。
	ResetsAt time.Time
}

// PlanUsage はプランの残量の最新値。
//
// **アダプタのメモリにだけ置き、本システムのファイルへ書かない。**
// 本システムを終了すると消え、次の AI 呼び出しまで「未取得」になる。
type PlanUsage struct {
	// PlanType はプラン種別（生のコード。ラベルは上位）。
	PlanType string
	// Windows は枠ごとの残量（Codex が返した枠をすべて持つ）。
	Windows []PlanUsageWindow
	// ReachedType は残量切れの種類（`rate_limit_reached` など。空 = 未到達・不明）。
	ReachedType string
	// ReceivedAt は値を受け取った時刻（UTC。画面で残量と併せて表示する）。
	ReceivedAt time.Time
}

// PlanUsageReporter は残量を持つアダプタが実装する任意のインタフェース。
//
// **取得のための通信をここで起こしてはならない**（閲覧の操作で通信を発生させない）。
// 保持済みの値を返すだけとする。
type PlanUsageReporter interface {
	// PlanUsage は保持している最新値を返す。未取得のときは ok = false。
	PlanUsage() (PlanUsage, bool)
}
