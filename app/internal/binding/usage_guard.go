package binding

// 本ファイルはトークン上限の判定と AI 呼び出しの停止。
//
// 判定は AI 呼び出しを伴うすべての公開バインディングの**共通前段**（beginAICall）で行い、個々の機能へ
// 分散実装しない（権限判定と同じ「バインディング前段」方式。個々の機能で判定を忘れる経路を作らない）。
// 前段を通らない AI 実行口が生まれていないことは usage_guard_test.go が構造で固定する。
//
// 判定式:
//   - usage_limit 未設定 → 判定しない（警告・停止なし）
//   - 累計 ≥ tokens_max × warn_ratio → 呼び出しは許可し、残りトークン数を警告として返す
//   - 累計 ≥ tokens_max → 呼び出しを開始せず、エラーカタログ「利用量上限系」で応答する
//
// 累計は判定のたびに ai-log を再集計して得る（他端末の追記を取り込む。集計値は保存しない）。
// 進行中の呼び出しは中断しない（完走させて実績を記録する）。
// 回答モードのステークホルダー任意 AI 対話は本 enforcement の対象外（設計上の判断）。

import (
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 利用量の状態（利用量の警告バナー・ブロック表示の出し分けに使う）。
// 画面はこの値をラベルへ写像する（生のコード値を表示しない）。
const (
	UsageLevelNone    = "none"    // 上限未設定、または警告閾値未満
	UsageLevelWarn    = "warn"    // 警告閾値以上・上限未満（呼び出しは可能）
	UsageLevelBlocked = "blocked" // 上限到達（AI 呼び出しを開始できない）
)

// UsageStatus は現在の利用量と上限（常設表示「消費 / 上限」）。
type UsageStatus struct {
	// ConsumedTokens はプロジェクト全期間・全メンバーの累計消費。
	ConsumedTokens int `json:"consumedTokens"`
	// MissingRecords はトークン実績を持たない送信の件数（欠測。0 と区別して示す）。
	MissingRecords int `json:"missingRecords"`
	// LimitTokens は上限値。nil = 上限未設定（警告・停止は発生しない）。
	LimitTokens *int `json:"limitTokens"`
	// WarnRatio は警告閾値（上限未設定なら 0）。
	WarnRatio float64 `json:"warnRatio"`
	// RemainingTokens は上限までの残り（上限未設定なら nil。到達済みなら 0）。
	RemainingTokens *int `json:"remainingTokens"`
	// ConsumptionRatio は上限に対する消費率（上限未設定なら nil）。
	ConsumptionRatio *float64 `json:"consumptionRatio"`
	// Level は UsageLevel* のいずれか。
	Level string `json:"level"`
	// ScopeNotice は集計範囲の併記。共同プロジェクトでは累計が
	// 「最後に取り込んだ時点まで」であり、上限判定も同じ範囲で行う（超過が取り込みで初めて判明しうる）。
	ScopeNotice string `json:"scopeNotice,omitempty"`
}

// usageStatusOf はプロジェクトの累計消費と上限設定から現在の状態を組み立てる。
//
// 累計は判定時に再集計する（プロセス内キャッシュだけで判定しない。
// 他端末が共有フォルダへ追記した消費を取りこぼさないため）。
func usageStatusOf(store *projectstore.Store) (UsageStatus, error) {
	total, err := auditlog.TotalUsage(store.Root())
	if err != nil {
		return UsageStatus{}, err
	}
	status := UsageStatus{
		ConsumedTokens: total.Tokens.Total,
		MissingRecords: total.Missing,
		Level:          UsageLevelNone,
	}
	status.ScopeNotice = usageScopeOf(store).Notice
	limit := store.UsageLimitSetting()
	if limit == nil {
		return status, nil
	}
	max := limit.TokensMax
	remaining := max - status.ConsumedTokens
	if remaining < 0 {
		remaining = 0
	}
	ratio := float64(status.ConsumedTokens) / float64(max)
	status.LimitTokens = &max
	status.WarnRatio = limit.WarnRatioOrDefault()
	status.RemainingTokens = &remaining
	status.ConsumptionRatio = &ratio
	switch {
	case status.ConsumedTokens >= max:
		status.Level = UsageLevelBlocked
	case status.ConsumedTokens >= limit.WarnThreshold():
		status.Level = UsageLevelWarn
	}
	return status, nil
}

// beginAICall は AI 呼び出しを伴う操作の共通前段（AI プロバイダの設定確認 + トークン上限の判定）。
//
// operation は利用者向けの操作名（「対話の質問生成」等。内部 ID・コード値を含めない）。
// 上限到達時はエラーカタログ「利用量上限系」の文言で拒否し、AI 呼び出しを開始しない。
func (a *API) beginAICall(s *dialogueSession, operation string) (UsageStatus, error) {
	if err := a.requireAIReady(); err != nil {
		return UsageStatus{}, err
	}
	status, err := usageStatusOf(s.store)
	if err != nil {
		return UsageStatus{}, err
	}
	if status.Level == UsageLevelBlocked {
		// 動作ログ（利用量上限系。記録の形は applog_failures.go が正本）。
		a.recordUsageBlocked(operation, status)
		return status, fmt.Errorf(
			"AI の利用量がこのプロジェクトの上限に達したため、%sを開始できません。"+
				"オーナーが上限を変更すると再開できます（AI 利用量ダッシュボードで確認できます）。"+
				"AI を使わない操作（参照・進捗レポート・エクスポート・利用量の表示）は続けられます。",
			operation)
	}
	return status, nil
}

// UsageStatusNow は開いているプロジェクトの現在の利用量と上限を返す。
//
// 画面は本値だけで警告バナー・ブロック表示・ステータスラインの「消費 / 上限」を決める
// （画面側で上限判定をやり直さない）。
func (a *API) UsageStatusNow() (UsageStatus, error) {
	s, err := a.current()
	if err != nil {
		return UsageStatus{}, err
	}
	return usageStatusOf(s.store)
}

// ---- トークン上限の設定（オーナーのみ）--------------

// UsageLimitRequest は上限設定の入力。
type UsageLimitRequest struct {
	// TokensMax はプロジェクト累計のトークン上限（1 以上）。
	TokensMax int `json:"tokensMax"`
	// WarnRatio は警告閾値（0 超 1 以下）。nil なら既定 0.8。
	WarnRatio *float64 `json:"warnRatio"`
}

// usageLimitLabel は変更履歴へ残す上限設定の表記（未設定は「未設定」）。
func usageLimitLabel(limit *projectstore.UsageLimit) string {
	if limit == nil {
		return "未設定"
	}
	return fmt.Sprintf("上限 %d トークン・警告 %d%%",
		limit.TokensMax, int(limit.WarnRatioOrDefault()*100+0.5))
}

// SetUsageLimit は AI 利用量上限を設定・変更する（オーナーのみ）。
//
// 権限判定は前段の共通実装（requireRole）。値が変わったときだけ
// 変更履歴 usage-limit-changed を記録する（誰がいつ上限を変えたかを追えるように）。
func (a *API) SetUsageLimit(req UsageLimitRequest) (UsageStatus, error) {
	s, err := a.current()
	if err != nil {
		return UsageStatus{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "AI 利用量上限の設定"); err != nil {
		return UsageStatus{}, err
	}
	before, after, err := s.store.SetUsageLimit(req.TokensMax, req.WarnRatio)
	if err != nil {
		return UsageStatus{}, err
	}
	a.recordUsageLimitChange(s, before, after)
	return usageStatusOf(s.store)
}

// ClearUsageLimit は AI 利用量上限を解除する（オーナーのみ）。
func (a *API) ClearUsageLimit() (UsageStatus, error) {
	s, err := a.current()
	if err != nil {
		return UsageStatus{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "AI 利用量上限の解除"); err != nil {
		return UsageStatus{}, err
	}
	before, err := s.store.ClearUsageLimit()
	if err != nil {
		return UsageStatus{}, err
	}
	a.recordUsageLimitChange(s, before, nil)
	return usageStatusOf(s.store)
}

// recordUsageLimitChange は上限設定の変更を変更履歴へ記録する（値が変わったときだけ）。
func (a *API) recordUsageLimitChange(s *dialogueSession, before, after *projectstore.UsageLimit) {
	if before.Equal(after) {
		return
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: "project", Change: auditlog.ChangeUsageLimitChanged,
		Before: usageLimitLabel(before), After: usageLimitLabel(after),
	})
}
