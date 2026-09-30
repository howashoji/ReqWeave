package docgen

// 本ファイルはセッション統計の機械組立て。
//
// **進捗レポートと同一のデータソース・同一の計数規則**を使う。決定事項・未決事項・
// 経過日数の計数は report.go の関数（decisionsInPeriod / openIssueMovement / elapsedDays）を
// そのまま呼び、統計側で判定条件を再実装しない（進捗レポートの件数と必ず一致させるため）。
//
// レコードからの読み出しのみで、AI 呼び出しも書き込みも行わない（本パッケージが aiprovider へ
// 依存しないことは depcheck の no-ai-in-report で機械検知する）。

import (
	"fmt"
	"sort"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// PhaseSessionCount はフェーズ別の対話セッション数。
type PhaseSessionCount struct {
	// Phase はセッションのフェーズの値（requirements / basic-design）。表示ラベルは呼び出し側で付ける。
	Phase string `json:"phase"`
	Count int    `json:"count"`
}

// QuestionnaireFlow は発行から取込まで到達した質問票 1 件（経過日数の計数に使う）。
type QuestionnaireFlow struct {
	ID         string    `json:"id"`
	Addressee  string    `json:"addressee"`
	IssuedAt   time.Time `json:"issuedAt"`
	ImportedAt time.Time `json:"importedAt"`
	// ElapsedDays は発行から取込までの経過日数（24 時間 = 1 日で切り捨て。進捗レポート章5 と同一の規則）。
	ElapsedDays int `json:"elapsedDays"`
}

// SessionStats は期間内の作業統計（次のプロジェクトの見積材料）。
type SessionStats struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	// SessionsTotal は期間内に開始した対話セッション数。SessionsByPhase はその内訳。
	SessionsTotal   int                 `json:"sessionsTotal"`
	SessionsByPhase []PhaseSessionCount `json:"sessionsByPhase"`

	// QuestionnairesIssued は期間内に発行した質問票の件数（questionnaires の issued_at）。
	QuestionnairesIssued int `json:"questionnairesIssued"`
	// QuestionnairesImported は期間内に取込済みへ移行した質問票の件数（変更履歴の status-changed）。
	QuestionnairesImported int `json:"questionnairesImported"`
	// ImportedFlows は期間内に取り込んだ質問票の発行→取込の経過（取込日時の昇順）。
	ImportedFlows []QuestionnaireFlow `json:"importedFlows"`
	// ElapsedDaysAverage は ImportedFlows の経過日数の平均（取込 0 件なら 0）。
	ElapsedDaysAverage float64 `json:"elapsedDaysAverage"`

	// DecisionsApproved は期間内に承認した決定事項数（進捗レポート章2 と同一の計数）。
	DecisionsApproved int `json:"decisionsApproved"`
	// OpenIssuesResolved は期間内に決着した未決事項数（進捗レポート章3 と同一の計数）。
	OpenIssuesResolved int `json:"openIssuesResolved"`
	// RequirementsConfirmed は期間内に確定（draft → agreed）した要件項目数。
	// 同一の要件が期間内に複数回確定しても 1 件として数える。
	RequirementsConfirmed int `json:"requirementsConfirmed"`
}

// SessionStatsInput はセッション統計の入力。
//
// レコード・変更履歴・質問票・セッション一覧は呼び出し側が読み出して渡す
// （進捗レポート = ProgressReportInput と同じ受け渡し方。本パッケージは読み書きしない）。
type SessionStatsInput struct {
	// From / To は対象期間（境界を含む）。保存値と同じ UTC の時刻で渡す。
	From, To time.Time
	// Sessions は対話セッションのフロントマター一覧。
	Sessions []projectstore.Session
	// Questionnaires は質問票のフロントマター一覧（進捗レポート章5 と同一データ）。
	Questionnaires []projectstore.Questionnaire
	// Records は期末のレコード（決定事項・未決事項・要件項目）。
	Records Records
	// Changes は変更履歴（全期間・全作業者をマージしたもの）。
	Changes []auditlog.ChangeRecord
}

// SessionStatistics は期間内の作業統計を組み立てる。
func SessionStatistics(in SessionStatsInput) (SessionStats, error) {
	if in.From.IsZero() || in.To.IsZero() {
		return SessionStats{}, fmt.Errorf("対象期間を指定してください")
	}
	if in.To.Before(in.From) {
		return SessionStats{}, fmt.Errorf("対象期間の終わりが開始より前です")
	}

	out := SessionStats{From: in.From, To: in.To}
	out.SessionsTotal, out.SessionsByPhase = sessionsInPeriod(in.Sessions, in.From, in.To)

	for _, q := range in.Questionnaires {
		if inPeriod(q.IssuedAt, in.From, in.To) {
			out.QuestionnairesIssued++
		}
	}
	out.ImportedFlows = importedQuestionnaireFlows(in.Questionnaires, in.Changes, in.From, in.To)
	out.QuestionnairesImported = len(out.ImportedFlows)
	if n := len(out.ImportedFlows); n > 0 {
		total := 0
		for _, f := range out.ImportedFlows {
			total += f.ElapsedDays
		}
		out.ElapsedDaysAverage = float64(total) / float64(n)
	}

	// 決定事項・未決事項は進捗レポートと同一の関数で数える（二重実装しない）。
	out.DecisionsApproved = len(decisionsInPeriod(in.Records.Decisions, in.From, in.To))
	_, resolved := openIssueMovement(in.Records.OpenIssues, in.Changes, in.From, in.To)
	out.OpenIssuesResolved = len(resolved)

	out.RequirementsConfirmed = requirementsConfirmedInPeriod(in.Changes, in.From, in.To)
	return out, nil
}

// sessionsInPeriod は期間内に開始した対話セッションの件数とフェーズ別内訳を返す。
// 内訳は件数の多い順（同数はフェーズ名の昇順）で並べ、表示を安定させる。
func sessionsInPeriod(all []projectstore.Session, from, to time.Time) (int, []PhaseSessionCount) {
	total := 0
	byPhase := map[string]int{}
	for _, s := range all {
		if !inPeriod(s.StartedAt, from, to) {
			continue
		}
		total++
		byPhase[s.Phase]++
	}
	out := make([]PhaseSessionCount, 0, len(byPhase))
	for phase, n := range byPhase {
		out = append(out, PhaseSessionCount{Phase: phase, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Phase < out[j].Phase
	})
	return total, out
}

// importedQuestionnaireFlows は期間内に取込済みへ移行した質問票の発行→取込の経過を返す。
//
// 取込日時は質問票レコードに持たないため、変更履歴の status-changed
// （after = imported）で判定する（未決事項の決着判定 = openIssueMovement と同じ方式）。
func importedQuestionnaireFlows(all []projectstore.Questionnaire, changes []auditlog.ChangeRecord,
	from, to time.Time) []QuestionnaireFlow {

	byID := map[string]projectstore.Questionnaire{}
	for _, q := range all {
		byID[q.ID] = q
	}
	var out []QuestionnaireFlow
	seen := map[string]bool{}
	for _, c := range changes {
		if c.Change != auditlog.ChangeStatusChanged || c.After != projectstore.QuestionnaireImported {
			continue
		}
		if !inPeriod(c.At, from, to) || seen[c.Target] {
			continue
		}
		q, ok := byID[c.Target]
		if !ok {
			continue
		}
		seen[c.Target] = true
		out = append(out, QuestionnaireFlow{
			ID: q.ID, Addressee: q.Addressee, IssuedAt: q.IssuedAt, ImportedAt: c.At,
			// 経過日数の規則は進捗レポート章5 と同一（elapsedDays）。
			ElapsedDays: elapsedDays(q.IssuedAt, c.At),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ImportedAt.Before(out[j].ImportedAt) })
	return out
}

// requirementsConfirmedInPeriod は期間内に確定（draft → agreed）した要件項目の件数を返す。
// 同一 ID が期間内に複数回確定しても 1 件として数える。
func requirementsConfirmedInPeriod(changes []auditlog.ChangeRecord, from, to time.Time) int {
	seen := map[string]bool{}
	for _, c := range changes {
		if c.Change != auditlog.ChangeStatusChanged || c.After != projectstore.RequirementAgreed {
			continue
		}
		if !inPeriod(c.At, from, to) {
			continue
		}
		seen[c.Target] = true
	}
	return len(seen)
}
