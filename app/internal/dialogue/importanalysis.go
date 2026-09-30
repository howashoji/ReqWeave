package dialogue

// 本ファイルはステークホルダーの回答を取り込むときの分析と、その反映を担う。
//
// 出力スキーマは対話の抽出と同一（Extraction）。根拠は回答参照（QS-nnn#q-nn）とする。
// 承認・反映は対話の候補と同一処理（applyApproval）を通し、反映の実装を二重に持たない。
// AI API 障害時は突き合わせ表示（AI 呼び出し不要）と手動編集で取込を完了できる。

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ModeImportAnalysis は回答取込時の分析。
const ModeImportAnalysis = "import-analysis"

// RequirementDiff は要件項目の変更前後の対比。
type RequirementDiff struct {
	Operation string `json:"operation"`
	// TargetID は update 時の既存 ID（create 時は空）。
	TargetID string `json:"targetId,omitempty"`
	Title    string `json:"title"`
	// BodyBefore は現行本文（create 時は空 = 新規追加）。
	BodyBefore string `json:"bodyBefore"`
	BodyAfter  string `json:"bodyAfter"`
}

// ContradictionView は既存決定との矛盾の指摘表示。
type ContradictionView struct {
	DecisionID string `json:"decisionId"`
	// DecisionBody は矛盾する既存決定の本文（対比表示に使う）。
	DecisionBody string   `json:"decisionBody"`
	Description  string   `json:"description"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

// UnknownAnswer は「不明」回答。決着案は生成しない（「まだ決められない」は決着の根拠にならない）。
type UnknownAnswer struct {
	QuestionID string `json:"questionId"`
	// AnswerRef は回答参照（QS-nnn#q-nn）。
	AnswerRef string `json:"answerRef"`
	// SourceIssueID は質問の発行元未決事項（ISS-nnn）。
	SourceIssueID string    `json:"sourceIssueId"`
	AnsweredAt    time.Time `json:"answeredAt"`
	// Reason は回答者が書いた理由・確認先（answers.md の回答本文そのまま）。
	Reason string `json:"reason"`
	// OwnerCandidate は記入された確認先（未決事項の owner の更新候補として提示する）。
	// 抽出できなかった場合は空。
	OwnerCandidate string `json:"ownerCandidate,omitempty"`
	// CurrentOwner は現在の owner（更新候補との対比表示に使う）。
	CurrentOwner string `json:"currentOwner,omitempty"`
}

// ImportAnalysis は回答取込時の分析結果（取込画面の提示材料）。
//
// この値を作る時点でプロジェクトデータは変更していない（反映は ApplyImportApproval）。
type ImportAnalysis struct {
	QuestionnaireID string      `json:"questionnaireId"`
	Extraction      *Extraction `json:"extraction"`
	// RequirementDiffs は Extraction.RequirementUpdates の変更前後対比。
	RequirementDiffs []RequirementDiff `json:"requirementDiffs,omitempty"`
	// Contradictions は既存決定との矛盾（本文つき）。
	Contradictions []ContradictionView `json:"contradictions,omitempty"`
	// UnknownAnswers は「不明」回答（決着案なし。未決事項への経過追記の材料）。
	UnknownAnswers []UnknownAnswer `json:"unknownAnswers,omitempty"`
	// AffectedRequirements は未決事項 ID → ブロックされている要件項目 ID（影響範囲）。
	AffectedRequirements map[string][]string `json:"affectedRequirements,omitempty"`
	// SourceIssueIDs は質問の発行元未決事項（ID 順）。
	SourceIssueIDs []string `json:"sourceIssueIds,omitempty"`
	// Fallback は AI 呼び出しに失敗し、突き合わせ表示のみで返したか。
	Fallback bool `json:"fallback"`
	// Notice は画面へ出す案内文（原因＋次の行動）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// AnalyzeAnswers は取り込んだ回答から反映差分を生成する。
//
// プロジェクトデータを変更しない。AI 呼び出しに失敗した場合もエラーを返さず、
// 突き合わせ表示に必要な材料（未決事項・影響要件・「不明」回答）を Fallback として返す
// （手動編集で取込を完了できる）。
func (e *Engine) AnalyzeAnswers(ctx context.Context, questionnaireID string) (*ImportAnalysis, error) {
	q, err := e.cfg.Store.LoadQuestionnaire(questionnaireID)
	if err != nil {
		return nil, err
	}
	answers, err := e.cfg.Store.LoadAnswers(questionnaireID)
	if err != nil {
		return nil, fmt.Errorf("取り込んだ回答を読み込めません（%s）: %w", questionnaireID, err)
	}
	issues, err := e.sourceIssues(q)
	if err != nil {
		return nil, err
	}
	requirements, err := e.cfg.Store.ListRequirements()
	if err != nil {
		return nil, err
	}
	decisions, err := e.cfg.Store.ListDecisions()
	if err != nil {
		return nil, err
	}

	analysis := &ImportAnalysis{
		QuestionnaireID:      q.ID,
		Extraction:           &Extraction{},
		UnknownAnswers:       unknownAnswers(q, answers, issues),
		AffectedRequirements: affectedRequirements(issues, requirements),
		SourceIssueIDs:       issueIDs(issues),
	}

	req, labels, err := e.buildImportAnalysisRequest(q, answers, issues, requirements, decisions)
	if err != nil {
		return nil, err
	}
	body, res := e.streamSilently(ctx, "", req, labels)
	if res.err != nil || strings.TrimSpace(body) == "" {
		return degradedAnalysis(analysis, res.cause()), nil
	}
	extraction, err := ParseExtraction(body)
	if err != nil {
		return degradedAnalysis(analysis, err), nil
	}
	// 根拠は当該質問票の回答参照に限る（実在しない参照は除去する）。
	extraction.VerifyEvidence(answerRefs(q, answers))
	// 観点候補は資料の取り込み分析だけの出力。回答取込では扱わない。
	extraction.DropPerspectiveCandidates()
	// 新規の要件項目の ID グループ・種別をアプリが確定する（利用者に入力させない）。
	extraction.ResolveRequirementIDs(e.requirementGroupsByChapter())
	// 「不明」回答だけを根拠とする決着案は採らない。
	dropped := dropUnknownOnlyDecisions(extraction, unknownRefs(q, answers))

	analysis.Extraction = extraction
	analysis.RequirementDiffs = requirementDiffs(extraction, requirements)
	analysis.Contradictions = contradictionViews(extraction, decisions)
	if dropped > 0 {
		analysis.Notice = fmt.Sprintf("「不明」の回答だけを根拠とする決着案 %d 件は候補から外しました。確認先へ問い合わせてから決着させてください。", dropped)
	}
	return analysis, nil
}

// sourceIssues は質問の発行元未決事項を ID 順に読む（重複は 1 件にまとめる）。
func (e *Engine) sourceIssues(q *projectstore.Questionnaire) ([]projectstore.OpenIssue, error) {
	seen := map[string]bool{}
	var ids []string
	for _, question := range q.Questions {
		if question.SourceIssue == "" || seen[question.SourceIssue] {
			continue
		}
		seen[question.SourceIssue] = true
		ids = append(ids, question.SourceIssue)
	}
	sort.Strings(ids)
	out := make([]projectstore.OpenIssue, 0, len(ids))
	for _, id := range ids {
		issue, err := e.cfg.Store.LoadOpenIssue(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *issue)
	}
	return out, nil
}

func issueIDs(issues []projectstore.OpenIssue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.ID)
	}
	return out
}

// affectedRequirements は未決事項 → ブロック対象要件項目の逆引き（影響範囲）。
func affectedRequirements(issues []projectstore.OpenIssue, reqs []projectstore.Requirement) map[string][]string {
	byIssue := blockedRequirementsByIssue(reqs)
	out := map[string][]string{}
	for _, issue := range issues {
		if blocked := byIssue[issue.ID]; len(blocked) > 0 {
			out[issue.ID] = blocked
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// unknownAnswers は「不明」回答を集める（決着案を生成しない対象）。
func unknownAnswers(q *projectstore.Questionnaire, a *projectstore.Answers, issues []projectstore.OpenIssue) []UnknownAnswer {
	owner := map[string]string{}
	for _, issue := range issues {
		owner[issue.ID] = issue.Owner
	}
	var out []UnknownAnswer
	for _, question := range q.Questions {
		answer, ok := a.Find(question.ID)
		if !ok || answer.Kind != projectstore.AnswerKindUnknown {
			continue
		}
		reason := strings.TrimSpace(answer.Body)
		if reason == "" {
			reason = strings.TrimSpace(answer.FreeText)
		}
		out = append(out, UnknownAnswer{
			QuestionID:     question.ID,
			AnswerRef:      projectstore.AnswerRef(q.ID, question.ID),
			SourceIssueID:  question.SourceIssue,
			AnsweredAt:     answer.At,
			Reason:         reason,
			OwnerCandidate: parseOwnerCandidate(reason),
			CurrentOwner:   owner[question.SourceIssue],
		})
	}
	return out
}

// ownerCandidateLabels は確認先の記入見出し（回答モードの「不明」欄の項目名）。
var ownerCandidateLabels = []string{"確認先:", "確認先：", "確認先 :", "確認先 ："}

// parseOwnerCandidate は「不明」の理由欄から確認先を取り出す（owner の更新候補）。
//
// 見出しが無ければ空を返す（推測で owner を書き換えないため）。
func parseOwnerCandidate(reason string) string {
	for _, line := range strings.Split(reason, "\n") {
		for _, label := range ownerCandidateLabels {
			idx := strings.Index(line, label)
			if idx < 0 {
				continue
			}
			value := strings.TrimSpace(line[idx+len(label):])
			// 同一行に続く別項目（「理由: …」等）は取らない。
			if cut := strings.IndexAny(value, "。\t"); cut > 0 {
				value = strings.TrimSpace(value[:cut])
			}
			if value != "" {
				return value
			}
		}
	}
	return ""
}

// answerRefs は当該質問票の回答参照（QS-nnn#q-nn）の集合を返す（根拠の実在検証用）。
func answerRefs(q *projectstore.Questionnaire, a *projectstore.Answers) map[string]bool {
	out := map[string]bool{}
	for _, question := range q.Questions {
		if _, ok := a.Find(question.ID); ok {
			out[projectstore.AnswerRef(q.ID, question.ID)] = true
		}
	}
	return out
}

// unknownRefs は「不明」回答の参照集合を返す。
func unknownRefs(q *projectstore.Questionnaire, a *projectstore.Answers) map[string]bool {
	out := map[string]bool{}
	for _, question := range q.Questions {
		answer, ok := a.Find(question.ID)
		if ok && answer.Kind == projectstore.AnswerKindUnknown {
			out[projectstore.AnswerRef(q.ID, question.ID)] = true
		}
	}
	return out
}

// dropUnknownOnlyDecisions は「不明」回答だけを根拠とする決着案を落とし、落とした件数を返す。
//
// 「不明」は「まだ決められない」という回答であり、決着の根拠にならない。
// 「不明」と通常回答の両方を根拠に持つ候補は残す（通常回答が根拠として成立するため）。
func dropUnknownOnlyDecisions(e *Extraction, unknown map[string]bool) int {
	kept := make([]DecisionCandidate, 0, len(e.Decisions))
	dropped := 0
	for _, c := range e.Decisions {
		if len(c.EvidenceRefs) > 0 && allIn(c.EvidenceRefs, unknown) {
			dropped++
			continue
		}
		kept = append(kept, c)
	}
	e.Decisions = kept
	return dropped
}

func allIn(refs []string, set map[string]bool) bool {
	for _, ref := range refs {
		if !set[ref] {
			return false
		}
	}
	return true
}

// requirementDiffs は反映案の変更前後対比を組み立てる。
func requirementDiffs(e *Extraction, reqs []projectstore.Requirement) []RequirementDiff {
	body := map[string]string{}
	for _, r := range reqs {
		body[r.ID] = r.Body
	}
	var out []RequirementDiff
	for _, c := range e.RequirementUpdates {
		out = append(out, RequirementDiff{
			Operation:  c.Operation,
			TargetID:   c.TargetID,
			Title:      c.Title,
			BodyBefore: body[c.TargetID],
			BodyAfter:  c.BodyAfter,
		})
	}
	return out
}

// contradictionViews は矛盾指摘に既存決定の本文を添える。
func contradictionViews(e *Extraction, decisions []projectstore.Decision) []ContradictionView {
	body := map[string]string{}
	for _, d := range decisions {
		body[d.ID] = d.Body
	}
	var out []ContradictionView
	for _, c := range e.Contradictions {
		out = append(out, ContradictionView{
			DecisionID:   c.WithDecisionID,
			DecisionBody: body[c.WithDecisionID],
			Description:  c.Description,
			EvidenceRefs: c.EvidenceRefs,
		})
	}
	return out
}

// degradedAnalysis は AI 障害時の縮退結果（突き合わせ表示の材料のみを返す）。
func degradedAnalysis(analysis *ImportAnalysis, cause error) *ImportAnalysis {
	analysis.Fallback = true
	analysis.Notice = describeQuestionnaireFailure(cause) +
		"回答と未決事項の突き合わせは表示できます。決定事項・要件項目の反映内容を入力して取り込んでください。"
	return analysis
}

// buildImportAnalysisRequest は回答分析のリクエストを組み立てる。
func (e *Engine) buildImportAnalysisRequest(q *projectstore.Questionnaire, a *projectstore.Answers,
	issues []projectstore.OpenIssue, reqs []projectstore.Requirement,
	decisions []projectstore.Decision) (aiprovider.ChatRequest, []string, error) {

	system, err := BuildSystemPrompt(SystemPromptInput{
		Phase: e.cfg.Store.Project().Phase, Effort: e.cfg.Effort, Mode: ModeImportAnalysis})
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	effort := e.effortParams()

	labels := []string{q.ID}
	var b strings.Builder
	fmt.Fprintf(&b, "### ステークホルダーの回答（質問票 %s。回答者: %s）\n\n", q.ID, a.Respondent)
	for _, question := range q.Questions {
		fmt.Fprintf(&b, "- %s（発行元未決事項: %s）\n", projectstore.AnswerRef(q.ID, question.ID), question.SourceIssue)
		fmt.Fprintf(&b, "  - 質問: %s\n", oneLine(question.Text))
		answer, ok := a.Find(question.ID)
		switch {
		case !ok:
			b.WriteString("  - 回答: 未回答\n")
		case answer.Kind == projectstore.AnswerKindUnknown:
			fmt.Fprintf(&b, "  - 回答: 不明（理由・確認先: %s）\n", oneLine(answer.Body))
		default:
			fmt.Fprintf(&b, "  - 回答: %s\n", oneLine(answerText(answer)))
		}
	}

	byIssue := blockedRequirementsByIssue(reqs)
	bodies := map[string]projectstore.Requirement{}
	for _, r := range reqs {
		bodies[r.ID] = r
	}
	b.WriteString("\n### 発行元の未決事項とブロック対象の要件項目\n\n")
	for _, issue := range issues {
		fmt.Fprintf(&b, "- %s（決める人: %s）: %s\n", issue.ID, issue.Owner, oneLine(issue.Body))
		labels = append(labels, issue.ID)
		for _, reqID := range byIssue[issue.ID] {
			r := bodies[reqID]
			fmt.Fprintf(&b, "  - %s %s\n%s\n", r.ID, r.Title, indent(r.Body))
			labels = append(labels, r.ID)
		}
	}
	if len(decisions) > 0 {
		b.WriteString("\n### 既存の決定（各 1 行の要約。矛盾の検知に使う）\n\n")
		for _, d := range decisions {
			fmt.Fprintf(&b, "- %s: %s\n", d.ID, oneLine(d.Body))
		}
		labels = append(labels, "decisions")
	}
	b.WriteString("\n上の回答から、未決事項の決着案・要件項目への反映案・既存決定との矛盾を出してください。")

	return aiprovider.ChatRequest{
		Model:    e.cfg.Model.ID,
		System:   system,
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: b.String()}},
		Effort:   effort,
		// 構造化出力。スキーマはプロンプトではなくここで渡す。
		ResponseSchema: SchemaForMode(ModeImportAnalysis),
	}, labels, nil
}

// answerText は回答の表示用テキスト（選択値と自由記述をつなぐ）。
func answerText(a projectstore.Answer) string {
	parts := make([]string, 0, 3)
	if len(a.Selected) > 0 {
		parts = append(parts, strings.Join(a.Selected, " / "))
	}
	if strings.TrimSpace(a.FreeText) != "" {
		parts = append(parts, a.FreeText)
	}
	if strings.TrimSpace(a.Body) != "" {
		parts = append(parts, a.Body)
	}
	return strings.Join(parts, " ")
}

// UnknownAnswerNote は「不明」回答の経過を未決事項へ追記する指示。
type UnknownAnswerNote struct {
	IssueID    string    `json:"issueId"`
	AnswerRef  string    `json:"answerRef"`
	AnsweredAt time.Time `json:"answeredAt"`
	Reason     string    `json:"reason"`
}

// OwnerUpdate は未決事項の決める人の更新（「不明」回答の確認先を担当者が承認したときのみ）。
type OwnerUpdate struct {
	IssueID string `json:"issueId"`
	Owner   string `json:"owner"`
}

// ImportApproval は回答取込の承認内容（承認した差分だけを含む）。
//
// 破棄した候補は渡さない（渡されたものだけを反映する構造で、承認していない差分が記録されないことを担保する）。
type ImportApproval struct {
	ApprovalRequest
	// UnknownNotes は「不明」回答の経過追記（回答日時・理由）。
	UnknownNotes []UnknownAnswerNote `json:"unknownNotes,omitempty"`
	// OwnerUpdates は確認先を反映する未決事項（承認された分のみ）。
	OwnerUpdates []OwnerUpdate `json:"ownerUpdates,omitempty"`
}

// ImportApplyResult は回答取込の反映結果。
type ImportApplyResult struct {
	*ApprovalResult
	QuestionnaireID string `json:"questionnaireId"`
	// QuestionnaireStatus は反映後の質問票の状態（取込済み）。
	QuestionnaireStatus string `json:"questionnaireStatus"`
	// NotedIssueIDs は経過を追記した未決事項。
	NotedIssueIDs []string `json:"notedIssueIds,omitempty"`
	// OwnerUpdatedIssueIDs は決める人を更新した未決事項。
	OwnerUpdatedIssueIDs []string `json:"ownerUpdatedIssueIds,omitempty"`
}

// ApplyImportApproval は承認された反映差分を記録へ反映し、質問票を取込済みにする。
//
// 承認していない差分は渡らないため反映されない。
// AI 障害時に手動編集した内容も同じ経路を通す。
func (e *Engine) ApplyImportApproval(questionnaireID string, req ImportApproval) (*ImportApplyResult, error) {
	q, err := e.cfg.Store.LoadQuestionnaire(questionnaireID)
	if err != nil {
		return nil, err
	}
	if q.Status != projectstore.QuestionnaireAnswered {
		return nil, fmt.Errorf("回答済みの質問票ではありません（%s。現在: %s）", questionnaireID, q.Status)
	}

	result, err := e.applyApproval(req.ApprovalRequest)
	if err != nil {
		return nil, err
	}
	out := &ImportApplyResult{ApprovalResult: result, QuestionnaireID: questionnaireID}

	// 「不明」回答は決着させず、経過（回答日時・理由）を未決事項へ追記する。
	for _, note := range req.UnknownNotes {
		if err := e.appendUnknownNote(note); err != nil {
			return nil, err
		}
		out.NotedIssueIDs = append(out.NotedIssueIDs, note.IssueID)
	}
	// 確認先は承認されたものだけ owner へ反映する。
	for _, update := range req.OwnerUpdates {
		if err := e.updateIssueOwner(update); err != nil {
			return nil, err
		}
		out.OwnerUpdatedIssueIDs = append(out.OwnerUpdatedIssueIDs, update.IssueID)
	}

	// 反映完了で回答済み → 取込済み。
	if err := e.cfg.Store.SetQuestionnaireStatus(questionnaireID, projectstore.QuestionnaireImported); err != nil {
		return nil, err
	}
	e.record(auditlog.ChangeRecord{Target: questionnaireID, Change: auditlog.ChangeStatusChanged,
		Before: projectstore.QuestionnaireAnswered, After: projectstore.QuestionnaireImported})
	out.QuestionnaireStatus = projectstore.QuestionnaireImported
	return out, nil
}

// progressHeading は「不明」回答の経過を書く節の見出し。
const progressHeading = "## 経過"

// hasProgressSection は経過節が既にあるかを返す（見出しを重ねないため）。
func hasProgressSection(body string) bool {
	return strings.HasPrefix(body, progressHeading+"\n") || strings.Contains(body, "\n"+progressHeading+"\n")
}

// appendUnknownNote は「不明」回答の経過を未決事項の論点へ追記する（決着させない）。
func (e *Engine) appendUnknownNote(note UnknownAnswerNote) error {
	line := fmt.Sprintf("- %s 「不明」の回答（%s）: %s",
		note.AnsweredAt.Format("2006-01-02 15:04"), note.AnswerRef, oneLine(note.Reason))
	base, err := e.cfg.Store.CurrentBaseline(note.IssueID)
	if err != nil {
		return err
	}
	issue, err := e.cfg.Store.UpdateOpenIssueGuarded(base, note.IssueID, func(i *projectstore.OpenIssue) error {
		if i.Status != projectstore.OpenIssueOpen {
			return fmt.Errorf("決着済みの未決事項には経過を追記できません（%s）", note.IssueID)
		}
		if strings.Contains(i.Body, line) {
			// 同じ回答での再取込では二重に追記しない。
			return nil
		}
		body := strings.TrimRight(i.Body, "\n")
		if hasProgressSection(body) {
			body += "\n" + line
		} else {
			body += "\n\n" + progressHeading + "\n\n" + line
		}
		i.Body = body + "\n"
		if !contains(i.Evidence, note.AnswerRef) {
			i.Evidence = append(i.Evidence, note.AnswerRef)
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.record(auditlog.ChangeRecord{Target: issue.ID, Change: auditlog.ChangeUpdated,
		After: line, Evidence: note.AnswerRef})
	return nil
}

// updateIssueOwner は未決事項の決める人を更新する（確認先の反映）。
func (e *Engine) updateIssueOwner(update OwnerUpdate) error {
	if strings.TrimSpace(update.Owner) == "" {
		return fmt.Errorf("未決事項の決める人が空です（%s）", update.IssueID)
	}
	var before string
	base, err := e.cfg.Store.CurrentBaseline(update.IssueID)
	if err != nil {
		return err
	}
	issue, err := e.cfg.Store.UpdateOpenIssueGuarded(base, update.IssueID, func(i *projectstore.OpenIssue) error {
		before = i.Owner
		i.Owner = update.Owner
		return nil
	})
	if err != nil {
		return err
	}
	e.record(auditlog.ChangeRecord{Target: issue.ID, Change: auditlog.ChangeUpdated,
		Before: "owner: " + before, After: "owner: " + issue.Owner})
	return nil
}
