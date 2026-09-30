package auditlog

import "time"

// 変更履歴の変更種別（change の値）。
const (
	ChangeCreated       = "created"
	ChangeUpdated       = "updated"
	ChangeRemoved       = "removed"
	ChangeStatusChanged = "status-changed"
	ChangeMemberAdded   = "member-added"
	ChangeMemberRemoved = "member-removed"
	ChangeRoleChanged   = "role-changed"
	ChangeOwnerTakeover = "owner-takeover"
	// 予約の設定・解除。以前の lock-force-released は予約への置き換えに伴い
	// reservation-released が担う（他メンバーによる解除は Author が予約者と異なることで分かる）。
	ChangeReservationSet      = "reservation-set"
	ChangeReservationReleased = "reservation-released"
	// ChangeVersionRenumbered は取り込み時の確定版の版番号の再採番（旧番号・新番号・対象種別を含む。
	// 共同作業で同じ版番号が別々に確定されたときに起きる）。
	ChangeVersionRenumbered = "version-renumbered"
	ChangeMergeApplied      = "merge-applied"
	ChangeProjectOpened     = "project-opened"
	ChangeProjectClosed     = "project-closed"
	ChangeUsageLimitChanged = "usage-limit-changed"
	ChangeAuthorBinding     = "author-binding"
	ChangeMemberIDCorrected = "member-id-corrected"
	// ChangeProjectRenamed は対象システム名の変更（Target は "project"、
	// Before / After に旧名・新名）。成果物のタイトルへ載る業務データのため記録する。
	ChangeProjectRenamed = "project-renamed"
)

// ChangeRecord は変更履歴の 1 レコード（audit/history）。
//
// シークレットキー・キー参照名を持つフィールドを定義しない（構造的非出力）。
type ChangeRecord struct {
	At       time.Time `json:"at"`
	Author   string    `json:"author"` // 変更した作業者の author_id（利用者 ID）
	Target   string    `json:"target"` // DEC-nnn / ISS-nnn / FR-* / 用語名 / QS-nnn / member:<author_id> / PRS-nnn / STK-nnn
	Change   string    `json:"change"`
	Before   string    `json:"before,omitempty"`
	After    string    `json:"after,omitempty"`
	Evidence string    `json:"evidence,omitempty"`

	// OSUser は author-binding イベントでのみ用いる正規化済み OS ユーザー名
	// （OS ユーザー名を記録しない原則の、監査のための例外。作業者のなりすまし・誤登録を追跡するため）。
	OSUser string `json:"os_user,omitempty"`
}

// ImportRef は取り込み分析の送信時に記録する資料の識別（import_refs。どの資料を AI へ送ったかを残す）。
type ImportRef struct {
	ID         string    `json:"id"` // IMP-nnn
	SourceName string    `json:"source_name"`
	ImportedAt time.Time `json:"imported_at"`
}

// AISendRecord は AI 送信記録の 1 レコード（audit/ai-log）。
//
// シークレットキー・キー参照名を持つフィールドを定義しない。
// 記録するのはプロバイダ名・モデル名まで。
//
// ai-log は追記専用のため、1 送信は **送信行 + 実績行の 2 行**で構成する。
// 送信直前に送信行（ID と送信内容）を、実績受信時に実績行（ID とトークン実績のみ）を追記し、
// 読み出し（ReadAISends）が ID をキーにマージする。実績行の無い送信は欠測として扱う。
type AISendRecord struct {
	// ID は送信 ID（プロジェクト内で一意）。送信行と実績行の紐づけに使う。
	ID         string      `json:"id"`
	At         time.Time   `json:"at"`
	Author     string      `json:"author"`
	Provider   string      `json:"provider"`
	Model      string      `json:"model"`
	Session    string      `json:"session,omitempty"` // S-nnnn。セッション文脈を持つ送信のみ
	Prompt     string      `json:"prompt"`            // 送信全文（System + Messages）
	Included   []string    `json:"included"`          // 同梱したプロジェクトデータの範囲（ファイル・ID）
	ImportRefs []ImportRef `json:"import_refs,omitempty"`

	// トークン実績（AI プロバイダ抽象化層の TokenUsage と対応）。API が返さなかった場合は欠測として
	// キーごと省略する（0 と欠測を区別する。集計では欠測の件数を別に示す）。
	TokensIn        *int `json:"tokens_in,omitempty"`
	TokensOut       *int `json:"tokens_out,omitempty"`
	TokensReasoning *int `json:"tokens_reasoning,omitempty"`
}

// SetTokens はトークン実績を設定する（API 応答が実績値を返した場合のみ呼ぶ）。
func (r *AISendRecord) SetTokens(in, out, reasoning int) {
	r.TokensIn, r.TokensOut, r.TokensReasoning = &in, &out, &reasoning
}

// HasTokens はトークン実績を持つかを返す（false = 欠測）。
func (r *AISendRecord) HasTokens() bool {
	return r.TokensIn != nil || r.TokensOut != nil || r.TokensReasoning != nil
}

// isUsageLine は実績行（ID とトークン実績のみの行）かを返す。
// 送信行は必ず送信本文を持つため、本文の有無で判別できる。
func (r *AISendRecord) isUsageLine() bool {
	return r.Prompt == "" && r.HasTokens()
}

// SyncRecord は同期の記録 1 レコード（audit/sync-log）。
//
// シークレットキー・認証情報を持つフィールドを定義しない（構造的非出力）。
// 同期先の所在は**資格情報部を除去した文字列**のみを持つ（RecordSync が記録の直前にも除去する）。
//
// 値集合は同期モジュール（internal/sync）が定める。本パッケージは保存形式だけを担い、
// 意味づけ（操作・失敗種別の一覧）を二重に持たない。
type SyncRecord struct {
	At     time.Time `json:"at"`
	Author string    `json:"author"` // 実行した作業者の author_id（利用者 ID）
	Op     string    `json:"op"`     // clone / incorporate / publish
	// RemoteKind は同期先の種別（folder / git_internal / git_external）。
	RemoteKind string `json:"remote_kind,omitempty"`
	// RemoteLocation は同期先の所在（**資格情報部を除去済み**）。
	RemoteLocation string `json:"remote_location,omitempty"`
	// Summary は区分ごとの件数（区分の値集合は同期モジュール側の区分と同一）。
	Summary map[string]int `json:"summary,omitempty"`
	// Conflicts は三面マージで解決した競合の件数。
	Conflicts int `json:"conflicts,omitempty"`
	// Result は ok / failed / canceled。
	Result string `json:"result"`
	// Failure は失敗種別（同期モジュールの分類）。成功時は空。
	Failure string `json:"failure,omitempty"`
}

// 同期の記録の値のうち、集計側（集計の範囲表示）が判定に使うもの。
// 値集合の正本は同期モジュール（internal/sync）であり、ここでは読み出しに要る分だけを持つ。
const (
	SyncOpIncorporate = "incorporate"
	SyncResultOK      = "ok"
)
