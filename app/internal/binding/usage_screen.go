package binding

// 本ファイルは AI 利用量ダッシュボードのバインディング。
//
// 集計の正本は auditlog（利用量）と docgen（セッション統計。進捗レポートと
// 同一の計数規則）であり、本層は期間の解釈・読み出し・Markdown 出力だけを担う。
//
// **閲覧権限でも参照できる**（利用量の参照はすべての権限に許している）。
// AI 呼び出しを行わないため、オフライン・API 障害中でも成立する。
// 集計値は保存しない（プロジェクトデータを変化させない）。

import (
	"fmt"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// UsageReportRequest はダッシュボードの表示条件。
//
// From / To は表示タイムゾーンの暦日（YYYY-MM-DD）で受け取り、両端を含む（進捗レポートと同じ規則）。
// Path が空でないとき、そのプロジェクトの単体詳細とセッション統計も返す。
type UsageReportRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
	Path string `json:"path,omitempty"`
}

// UsageBucketView は内訳 1 行（プロバイダ別・対話セッション別・作業者別）。
//
// Key は生の値（プロバイダ名 / S-nnnn / 利用者 ID）。**セッション文脈を持たない送信（取り込み分析）の
// Key は空文字**であり、表示ラベルへの変換は画面が行う（集計層は表示ラベルを持たない）。
type UsageBucketView struct {
	Key     string `json:"key"`
	Tokens  int    `json:"tokens"`
	Sends   int    `json:"sends"`
	Missing int    `json:"missing"`
}

// UsageDetailView はプロジェクト単体の期間別利用量とセッション統計。
type UsageDetailView struct {
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
	ByAuthor   []UsageBucketView `json:"byAuthor"`

	// LimitTokens / ConsumptionRatio は上限設定時のみ（未設定なら nil）。
	LimitTokens      *int     `json:"limitTokens"`
	ConsumptionRatio *float64 `json:"consumptionRatio"`

	Sessions docgen.SessionStats `json:"sessions"`

	// ScopeNotice は集計範囲の併記（単独利用のプロジェクトでは空）。
	ScopeNotice string `json:"scopeNotice,omitempty"`
	// LastIncorporation は集計に反映されている最後の取り込みの日時（未取り込み・単独利用なら空）。
	LastIncorporation string `json:"lastIncorporation,omitempty"`
}

// UsageDashboardView は横断一覧（＋指定時は単体詳細）と、その内容の Markdown。
type UsageDashboardView struct {
	From     string            `json:"from"`
	To       string            `json:"to"`
	Projects []ProjectUsageRow `json:"projects"`
	Detail   *UsageDetailView  `json:"detail,omitempty"`
	// Markdown はファイル保存・クリップボードコピーと同一の本文（出力経路で内容が変わらない）。
	Markdown string `json:"markdown"`
}

// UsageReportFileName は保存時の既定ファイル名。
const UsageReportFileName = "ai-usage.md"

// UsageDashboard は指定期間の AI 利用量ダッシュボードを組み立てる。
//
// プロジェクトを開いていなくても利用できる（横断一覧はプロジェクト一覧と同じ範囲）。
func (a *API) UsageDashboard(req UsageReportRequest) (UsageDashboardView, error) {
	from, to, err := usagePeriod(req)
	if err != nil {
		return UsageDashboardView{}, err
	}
	settings, err := a.settings()
	if err != nil {
		return UsageDashboardView{}, err
	}
	author, ok := settings.Author()
	if !ok {
		return UsageDashboardView{}, fmt.Errorf("メールアドレス（利用者 ID）が未登録です。設定でメールアドレスを登録してください。")
	}

	view := UsageDashboardView{From: req.From, To: req.To}
	view.Projects = crossProjectUsage(settings.RecentProjectPaths(), author, from, to)
	if strings.TrimSpace(req.Path) != "" {
		detail, err := a.projectUsageDetail(req.Path, author, from, to)
		if err != nil {
			return UsageDashboardView{}, err
		}
		view.Detail = detail
	}
	view.Markdown = usageMarkdown(view, from, to)
	return view, nil
}

// usagePeriod は期間をローカル暦日として解釈する（開始日 0:00 〜 終了日 23:59:59.999。進捗レポートと同じ規則）。
func usagePeriod(req UsageReportRequest) (from, to time.Time, err error) {
	from, err = parsePeriodBound(req.From, false)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err = parsePeriodBound(req.To, true)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if from.IsZero() || to.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("対象期間の開始日と終了日を指定してください。")
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("対象期間の終了日が開始日より前です。日付を確認してください。")
	}
	return from, to, nil
}

// projectUsageDetail は 1 プロジェクトの内訳とセッション統計を読み出す（読み出しのみ）。
func (a *API) projectUsageDetail(path string, author projectstore.Author, from, to time.Time) (*UsageDetailView, error) {
	members, err := loadMembers(path)
	if err != nil {
		return nil, fmt.Errorf("メンバー一覧を読み込めません。共有フォルダの接続を確認してください。")
	}
	if _, ok := members.Find(author.AuthorID); !ok {
		return nil, fmt.Errorf("このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。")
	}
	store, err := projectstore.Open(path, author)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	summary, err := auditlog.AggregateUsage(path, from, to)
	if err != nil {
		return nil, err
	}
	project := store.Project()
	out := &UsageDetailView{
		Path: path, TargetSystemName: project.TargetSystemName,
		Tokens: summary.Tokens.Total, TokensIn: summary.Tokens.In,
		TokensOut: summary.Tokens.Out, TokensReason: summary.Tokens.Reasoning,
		Sends: summary.Sends, MissingRecords: summary.Missing,
		ByProvider: bucketViews(summary.ByProvider),
		BySession:  bucketViews(summary.BySession),
		ByAuthor:   bucketViews(summary.ByAuthor),
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

	stats, err := sessionStatsInput(store, from, to)
	if err != nil {
		return nil, err
	}
	out.Sessions, err = docgen.SessionStatistics(stats)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sessionStatsInput はセッション統計の入力をプロジェクトデータから読み出す
// （進捗レポートと同一のデータソース）。
func sessionStatsInput(store *projectstore.Store, from, to time.Time) (docgen.SessionStatsInput, error) {
	sessions, err := store.ListSessions()
	if err != nil {
		return docgen.SessionStatsInput{}, err
	}
	questionnaires, err := store.ListQuestionnaires()
	if err != nil {
		return docgen.SessionStatsInput{}, err
	}
	decisions, err := store.ListDecisions()
	if err != nil {
		return docgen.SessionStatsInput{}, err
	}
	issues, err := store.ListOpenIssues()
	if err != nil {
		return docgen.SessionStatsInput{}, err
	}
	reqs, err := store.ListRequirements()
	if err != nil {
		return docgen.SessionStatsInput{}, err
	}
	// 期間外の状態変化も判定に要るため、変更履歴は全期間を読む（進捗レポートと同じ）。
	changes, err := auditlog.ReadChanges(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		return docgen.SessionStatsInput{}, err
	}
	return docgen.SessionStatsInput{
		From: from, To: to,
		Sessions:       sessions,
		Questionnaires: questionnaires,
		Records:        docgen.Records{Requirements: reqs, Decisions: decisions, OpenIssues: issues},
		Changes:        changes,
	}, nil
}

func bucketViews(buckets []auditlog.UsageBucket) []UsageBucketView {
	out := make([]UsageBucketView, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, UsageBucketView{Key: b.Key, Tokens: b.Tokens.Total,
			Sends: b.Sends, Missing: b.Missing})
	}
	return out
}

// ChooseUsageReportDestination は AI 利用量の保存先を選ぶ（OS の保存ダイアログ）。
func (a *API) ChooseUsageReportDestination() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("保存先の選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "AI 利用量の保存先を選ぶ",
		DefaultFilename: UsageReportFileName,
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Markdown (*.md)", Pattern: "*.md"},
		},
	})
}

// SaveUsageReport は表示内容を Markdown ファイルへ保存する。
func (a *API) SaveUsageReport(req UsageReportRequest, dst string) (string, error) {
	if strings.TrimSpace(dst) == "" {
		return "", fmt.Errorf("保存先のファイル名を指定してください。")
	}
	view, err := a.UsageDashboard(req)
	if err != nil {
		return "", err
	}
	if err := docgen.SaveProgressReport(dst, view.Markdown); err != nil {
		return "", err
	}
	return dst, nil
}

// CopyUsageReport は表示内容をクリップボードへコピーする（保存と同一の本文）。
func (a *API) CopyUsageReport(req UsageReportRequest) (string, error) {
	view, err := a.UsageDashboard(req)
	if err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("クリップボードへコピーできません。アプリを再起動してください。")
	}
	if err := wailsruntime.ClipboardSetText(a.ctx, view.Markdown); err != nil {
		return "", fmt.Errorf("クリップボードへコピーできませんでした。ファイルへ保存してください。")
	}
	return view.Markdown, nil
}
