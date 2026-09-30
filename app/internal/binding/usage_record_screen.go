package binding

// 本ファイルはトークン消費実績の画面のバインディング。
//
// ダッシュボード（usage_screen.go）が「期間別・作業者別・プロジェクト横断」を担うのに対し、
// 本画面は **1 プロジェクトの全期間累計と直近 1 件の実績**を担う（期間別・
// 作業者別・横断はダッシュボードへの導線とする）。
//
// 直近 1 件は **API 応答が返した実績値**をそのまま示し、推定値で代用しない。
// 実績を持たない送信（欠測）は 0 と区別して「実績なし」として示す。
// 集計値は保存せず、audit/ai-log の読み出しから毎回組み立てる（集計値を別に持つと記録とずれるため）。
// AI を呼ばないためオフライン・API 障害中でも成立し、閲覧権限でも参照できる。

import (
	"fmt"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// TokenUsageRequest は消費実績の対象。
type TokenUsageRequest struct {
	// Path は対象プロジェクトのフォルダ。空なら開いているプロジェクト
	// （対話画面の共通メニューから開いた場合）。
	Path string `json:"path,omitempty"`
}

// TokenUsageLatest は直近 1 件の AI 送信の消費実績。
type TokenUsageLatest struct {
	At       string `json:"at"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Session は S-nnnn。空 = 対話セッションの文脈を持たない送信（取り込み分析など）。
	Session string `json:"session,omitempty"`

	// HasTokens が false のとき実績は欠測（「実績なし」。0 と区別する）。
	HasTokens       bool `json:"hasTokens"`
	TokensIn        int  `json:"tokensIn"`
	TokensOut       int  `json:"tokensOut"`
	TokensReasoning int  `json:"tokensReasoning"`
	TokensTotal     int  `json:"tokensTotal"`
}

// TokenUsageView は消費実績画面の表示内容（全期間累計＋直近 1 件）。
type TokenUsageView struct {
	Path             string `json:"path"`
	TargetSystemName string `json:"targetSystemName"`

	Tokens         int    `json:"tokens"`
	TokensIn       int    `json:"tokensIn"`
	TokensOut      int    `json:"tokensOut"`
	TokensReason   int    `json:"tokensReasoning"`
	Sends          int    `json:"sends"`
	MissingRecords int    `json:"missingRecords"`
	LastUsedAt     string `json:"lastUsedAt,omitempty"`

	ByProvider []UsageBucketView `json:"byProvider"`
	BySession  []UsageBucketView `json:"bySession"`

	// LimitTokens / ConsumptionRatio は上限設定時のみ（未設定なら nil）。
	LimitTokens      *int     `json:"limitTokens"`
	ConsumptionRatio *float64 `json:"consumptionRatio"`

	// Latest は直近 1 件（送信が 1 件も無ければ nil）。
	Latest *TokenUsageLatest `json:"latest"`

	// ScopeNotice は集計範囲の併記（単独利用のプロジェクトでは空）。
	ScopeNotice string `json:"scopeNotice,omitempty"`
	// LastIncorporation は集計に反映されている最後の取り込みの日時（未取り込み・単独利用なら空）。
	LastIncorporation string `json:"lastIncorporation,omitempty"`
}

// TokenUsage は 1 プロジェクトの消費実績を組み立てる。
func (a *API) TokenUsage(req TokenUsageRequest) (TokenUsageView, error) {
	store, closeStore, err := a.usageRecordStore(req.Path)
	if err != nil {
		return TokenUsageView{}, err
	}
	defer closeStore()

	// 全期間を 1 度だけ読み、累計と直近 1 件の双方をそこから作る（同じ読み出しを 2 度しない）。
	sends, err := auditlog.ReadAISends(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		return TokenUsageView{}, err
	}
	summary := auditlog.AggregateSends(sends, time.Time{}, time.Time{})

	project := store.Project()
	out := TokenUsageView{
		Path: store.Root(), TargetSystemName: project.TargetSystemName,
		Tokens: summary.Tokens.Total, TokensIn: summary.Tokens.In,
		TokensOut: summary.Tokens.Out, TokensReason: summary.Tokens.Reasoning,
		Sends: summary.Sends, MissingRecords: summary.Missing,
		ByProvider: bucketViews(summary.ByProvider),
		BySession:  bucketViews(summary.BySession),
		Latest:     latestSend(sends),
	}
	if !summary.LastUsedAt.IsZero() {
		out.LastUsedAt = summary.LastUsedAt.UTC().Format(time.RFC3339)
	}
	// 集計範囲の併記（共同プロジェクトは取り込み時点までが集計対象）
	scope := usageScopeOf(store)
	out.ScopeNotice, out.LastIncorporation = scope.Notice, scope.LastIncorporation
	if limit := project.UsageLimit; limit != nil {
		max := limit.TokensMax
		ratio := float64(out.Tokens) / float64(max)
		out.LimitTokens, out.ConsumptionRatio = &max, &ratio
	}
	return out, nil
}

// usageRecordStore は対象プロジェクトのストアを返す（第 2 戻り値は後始末）。
//
// path が空なら開いているプロジェクト（閉じない）。path 指定時はメンバー照合のうえで読み出し用に開く
// （ダッシュボードの単体詳細と同じ方式 = ロックを取らない・locks/ に痕跡を残さない）。
func (a *API) usageRecordStore(path string) (*projectstore.Store, func(), error) {
	if strings.TrimSpace(path) == "" {
		s, err := a.current()
		if err != nil {
			return nil, nil, err
		}
		return s.store, func() {}, nil
	}
	settings, err := a.settings()
	if err != nil {
		return nil, nil, err
	}
	author, ok := settings.Author()
	if !ok {
		return nil, nil, fmt.Errorf("メールアドレス（利用者 ID）が未登録です。設定でメールアドレスを登録してください。")
	}
	members, err := loadMembers(path)
	if err != nil {
		return nil, nil, fmt.Errorf("メンバー一覧を読み込めません。共有フォルダの接続を確認してください。")
	}
	if _, ok := members.Find(author.AuthorID); !ok {
		return nil, nil, fmt.Errorf("このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。")
	}
	store, err := projectstore.Open(path, author)
	if err != nil {
		return nil, nil, err
	}
	return store, func() { store.Close() }, nil
}

// latestSend は最後に行われた送信 1 件を返す（送信が無ければ nil）。
//
// 同時刻の送信が複数あるときは送信 ID の大きい方を採る（採番は単調増加。
// 「同時刻だから順序不定」で表示が揺れないように決めておく）。
func latestSend(sends []auditlog.AISendRecord) *TokenUsageLatest {
	var newest *auditlog.AISendRecord
	for i := range sends {
		rec := &sends[i]
		if newest == nil || rec.At.After(newest.At) ||
			(rec.At.Equal(newest.At) && rec.ID > newest.ID) {
			newest = rec
		}
	}
	if newest == nil {
		return nil
	}
	out := &TokenUsageLatest{
		At: newest.At.UTC().Format(time.RFC3339), Provider: newest.Provider,
		Model: newest.Model, Session: newest.Session, HasTokens: newest.HasTokens(),
	}
	if newest.TokensIn != nil {
		out.TokensIn = *newest.TokensIn
	}
	if newest.TokensOut != nil {
		out.TokensOut = *newest.TokensOut
	}
	if newest.TokensReasoning != nil {
		out.TokensReasoning = *newest.TokensReasoning
	}
	out.TokensTotal = out.TokensIn + out.TokensOut + out.TokensReasoning
	return out
}
