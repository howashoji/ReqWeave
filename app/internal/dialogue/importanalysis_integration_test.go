//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package dialogue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// importFixture は取込済みの回答が揃った状態（ApplyReturn 直後 = 回答済み）を作る。
type importFixture struct {
	questionnaire *projectstore.Questionnaire
	issueIDs      []string
	requirementID string
	decisionID    string
}

func newImportFixture(t *testing.T, store *projectstore.Store) importFixture {
	t.Helper()
	ids := createOpenIssues(t, store)

	// 1 件目の未決事項にブロックされた要件項目（影響範囲の逆引き対象）。
	req, err := store.CreateRequirement("INV", projectstore.Requirement{
		Title: "在庫引当のタイミング", Chapter: "functional/scope",
		Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
		Body: "受注確定時に日次で在庫を引き当てる。", BlockedBy: []string{ids[0]},
		Evidence: []string{"S-0001#utt-00001"},
	})
	if err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}
	// 矛盾検知の対象になる既存決定。
	dec, err := store.CreateDecision(projectstore.Decision{
		TopicKey: "functional/scope", Body: "在庫引当は日次バッチで行う。",
		Evidence: []string{"S-0001#utt-00001"},
	})
	if err != nil {
		t.Fatalf("決定事項を作れない: %v", err)
	}

	at := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	q, err := store.CreateQuestionnaire(projectstore.Questionnaire{
		AddresseeRef: "STK-001", Addressee: "佐藤", IssuedAt: at, IssuedBy: "k.sato@example.co.jp",
		Questions: []projectstore.Question{
			{ID: "q-01", SourceIssue: ids[0], AnswerFormat: projectstore.AnswerFormatChoice,
				Choices: []string{"受注した時点で行う", "1日1回まとめて行う"},
				Text:    "在庫の引き当ては、注文を受けた時点で行いますか。", Background: "取り消しの扱いが変わります。"},
			{ID: "q-02", SourceIssue: ids[1], AnswerFormat: projectstore.AnswerFormatFree,
				Text: "月末の締め作業で手作業になっている工程を教えてください。", Background: "対象範囲の判断に使います。"},
		},
	})
	if err != nil {
		t.Fatalf("質問票を作れない: %v", err)
	}
	answers := &projectstore.Answers{
		QuestionnaireID: q.ID, Respondent: "佐藤", AnsweredAt: at,
		Answers: []projectstore.Answer{
			{QuestionID: "q-01", Kind: projectstore.AnswerKindAnswered,
				Selected: []string{"受注した時点で行う"}, At: at},
			{QuestionID: "q-02", Kind: projectstore.AnswerKindUnknown, At: at,
				Body: "理由: 締め作業の全体像を把握していません\n確認先: 経理部 田中"},
		},
	}
	if err := store.SaveAnswers(answers); err != nil {
		t.Fatalf("回答を保存できない: %v", err)
	}
	if err := store.SetQuestionnaireStatus(q.ID, projectstore.QuestionnaireAnswered); err != nil {
		t.Fatalf("回答済みにできない: %v", err)
	}
	return importFixture{questionnaire: q, issueIDs: ids, requirementID: req.ID, decisionID: dec.ID}
}

// analysisScript は抽出スキーマの応答（根拠は回答参照）。
// q-02 は「不明」なので、それだけを根拠にした決着案は候補から外れる。
const analysisScript = `{
 "decisions":[
  {"topic_key":"functional/scope","body":"在庫引当は受注確定時に即時で行う。","rationale":"受注時点で確保する運用に合わせる",
   "evidence_refs":["%QS%#q-01"],"supersedes_decision_id":null},
  {"topic_key":"functional/close","body":"締め作業の範囲は現行どおりとする。","rationale":"回答が得られなかったため",
   "evidence_refs":["%QS%#q-02"],"supersedes_decision_id":null}
 ],
 "open_issues":[],
 "requirement_updates":[
  {"operation":"update","target_id":"%REQ%","chapter":"functional/scope","title":"在庫引当のタイミング",
   "body_after":"受注確定時に即時で在庫を引き当てる。","acceptance_criteria":["受注確定から3秒以内に引当が記録されること"],
   "evidence_refs":["%QS%#q-01"]}
 ],
 "term_candidates":[],
 "contradictions":[
  {"with_decision_id":"%DEC%","description":"既存決定は日次バッチだが、回答は即時引当を求めている","evidence_refs":["%QS%#q-01"]}
 ]
}`

func buildAnalysisScript(f importFixture) string {
	s := strings.ReplaceAll(analysisScript, "%QS%", f.questionnaire.ID)
	s = strings.ReplaceAll(s, "%REQ%", f.requirementID)
	return strings.ReplaceAll(s, "%DEC%", f.decisionID)
}

// 反映差分（変更前後対比）・矛盾指摘・影響範囲が提示され、この時点では何も書き込まれない。
func TestAnalyzeAnswers(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)
	stub.scripts = []string{buildAnalysisScript(f)}

	got, err := e.AnalyzeAnswers(context.Background(), f.questionnaire.ID)
	if err != nil {
		t.Fatalf("回答分析に失敗: %v", err)
	}
	if got.Fallback {
		t.Fatalf("正常応答なのに縮退しました: %+v", got)
	}
	// 影響を受ける要件項目の変更前後が対比できる。
	if len(got.RequirementDiffs) != 1 {
		t.Fatalf("反映差分が %d 件です（期待 1）: %+v", len(got.RequirementDiffs), got.RequirementDiffs)
	}
	diff := got.RequirementDiffs[0]
	if diff.BodyBefore != "受注確定時に日次で在庫を引き当てる。" || diff.BodyAfter != "受注確定時に即時で在庫を引き当てる。" {
		t.Fatalf("変更前後の対比が違います: %+v", diff)
	}
	// 既存決定との矛盾が本文つきで指摘される。
	if len(got.Contradictions) != 1 || got.Contradictions[0].DecisionID != f.decisionID {
		t.Fatalf("矛盾指摘が組み立てられていません: %+v", got.Contradictions)
	}
	if got.Contradictions[0].DecisionBody != "在庫引当は日次バッチで行う。" {
		t.Fatalf("矛盾する既存決定の本文がありません: %+v", got.Contradictions[0])
	}
	// 影響範囲は未決事項 → blocked_by の逆引き。
	if blocked := got.AffectedRequirements[f.issueIDs[0]]; len(blocked) != 1 || blocked[0] != f.requirementID {
		t.Fatalf("影響範囲の逆引きが違います: %+v", got.AffectedRequirements)
	}
	// 「不明」回答からは決着案を作らない。
	if len(got.Extraction.Decisions) != 1 {
		t.Fatalf("決着案が %d 件です（期待 1）: %+v", len(got.Extraction.Decisions), got.Extraction.Decisions)
	}
	if got.Extraction.Decisions[0].EvidenceRefs[0] != projectstore.AnswerRef(f.questionnaire.ID, "q-01") {
		t.Fatalf("根拠が回答参照になっていません: %+v", got.Extraction.Decisions[0])
	}
	if len(got.UnknownAnswers) != 1 || got.UnknownAnswers[0].OwnerCandidate != "経理部 田中" {
		t.Fatalf("「不明」回答と確認先が提示されていません: %+v", got.UnknownAnswers)
	}
	if got.Notice == "" {
		t.Fatal("「不明」だけを根拠にした候補を外した旨の案内がありません")
	}

	// 分析だけではプロジェクトデータを変更しない。
	q, err := store.LoadQuestionnaire(f.questionnaire.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != projectstore.QuestionnaireAnswered {
		t.Fatalf("分析で状態が変わりました: %s", q.Status)
	}
	decisions, err := store.ListDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 {
		t.Fatalf("分析で決定事項が増えました: %d 件", len(decisions))
	}
}

// 承認した差分のみが反映され、決定事項の evidence に回答参照が入り、未決事項が決着する。
func TestApplyImportApprovalRecordsAnswerEvidence(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)
	stub.scripts = []string{buildAnalysisScript(f)}

	analysis, err := e.AnalyzeAnswers(context.Background(), f.questionnaire.ID)
	if err != nil {
		t.Fatalf("回答分析に失敗: %v", err)
	}

	// 決着案は承認し、要件項目の反映案は破棄する（承認していない差分は反映されない）。
	req := ImportApproval{ApprovalRequest: ApprovalRequest{
		Decisions: []DecisionApproval{{
			Candidate: analysis.Extraction.Decisions[0], ResolvesIssueIDs: []string{f.issueIDs[0]},
		}},
	}}
	for _, u := range analysis.UnknownAnswers {
		req.UnknownNotes = append(req.UnknownNotes, UnknownAnswerNote{
			IssueID: u.SourceIssueID, AnswerRef: u.AnswerRef, AnsweredAt: u.AnsweredAt, Reason: u.Reason,
		})
	}

	result, err := e.ApplyImportApproval(f.questionnaire.ID, req)
	if err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}
	if len(result.DecisionIDs) != 1 {
		t.Fatalf("記録された決定事項が %d 件です（期待 1）: %+v", len(result.DecisionIDs), result.DecisionIDs)
	}

	// evidence が QS-nnn#q-nn 形式の回答参照。
	dec, err := store.LoadDecision(result.DecisionIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	wantRef := projectstore.AnswerRef(f.questionnaire.ID, "q-01")
	if len(dec.Evidence) != 1 || dec.Evidence[0] != wantRef {
		t.Fatalf("決定事項の根拠が回答参照ではありません: %+v", dec.Evidence)
	}
	// 未決事項が resolved + resolved_by。
	issue, err := store.LoadOpenIssue(f.issueIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != projectstore.OpenIssueResolved || issue.ResolvedBy != dec.ID {
		t.Fatalf("未決事項が決着していません: status=%s resolved_by=%s", issue.Status, issue.ResolvedBy)
	}
	// 承認していない要件項目の反映案は書かれない。
	got, err := store.LoadRequirement(f.requirementID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "受注確定時に日次で在庫を引き当てる。" {
		t.Fatalf("承認していない差分が反映されています: %q", got.Body)
	}
	// 「不明」回答: 決着させず、回答日時・理由を経過として追記する。
	unknownIssue, err := store.LoadOpenIssue(f.issueIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	if unknownIssue.Status != projectstore.OpenIssueOpen {
		t.Fatalf("「不明」回答で未決事項が決着しました: %s", unknownIssue.Status)
	}
	if !strings.Contains(unknownIssue.Body, "2026-08-28 06:00") ||
		!strings.Contains(unknownIssue.Body, "締め作業の全体像を把握していません") {
		t.Fatalf("回答日時・理由が追記されていません:\n%s", unknownIssue.Body)
	}
	if !strings.Contains(strings.Join(unknownIssue.Evidence, " "), projectstore.AnswerRef(f.questionnaire.ID, "q-02")) {
		t.Fatalf("経過の根拠に回答参照がありません: %+v", unknownIssue.Evidence)
	}
	// 反映完了で取込済みへ。
	if result.QuestionnaireStatus != projectstore.QuestionnaireImported {
		t.Fatalf("質問票の状態が %s です（期待 %s）", result.QuestionnaireStatus, projectstore.QuestionnaireImported)
	}
	q, err := store.LoadQuestionnaire(f.questionnaire.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != projectstore.QuestionnaireImported {
		t.Fatalf("保存された状態が %s です（期待 %s）", q.Status, projectstore.QuestionnaireImported)
	}
}

// 何も承認しなければレコードは 1 件も増えない。
func TestApplyImportApprovalWritesNothingWithoutApproval(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)
	stub.scripts = []string{buildAnalysisScript(f)}

	before, err := store.ListDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplyImportApproval(f.questionnaire.ID, ImportApproval{}); err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}
	after, err := store.ListDecisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("承認なしで決定事項が増えました: %d → %d", len(before), len(after))
	}
	issue, err := store.LoadOpenIssue(f.issueIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != projectstore.OpenIssueOpen {
		t.Fatalf("承認なしで未決事項が決着しました: %s", issue.Status)
	}
}

// AI 呼び出しが失敗しても突き合わせ表示が返り、手動編集の内容で取込を完了できる。
func TestAnalyzeAnswersFallsBackAndCompletesManually(t *testing.T) {
	stub := &stubAdapter{err: &aiprovider.ProviderError{
		Class: aiprovider.ErrClassConfig, Provider: aiprovider.ProviderAnthropic, Message: "invalid api key"}}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := e.AnalyzeAnswers(ctx, f.questionnaire.ID)
	if err != nil {
		t.Fatalf("AI 障害でエラーが返りました（縮退すべき）: %v", err)
	}
	if !got.Fallback {
		t.Fatalf("縮退として扱われていません: %+v", got)
	}
	if !got.Extraction.IsEmpty() {
		t.Fatalf("縮退なのに候補が入っています: %+v", got.Extraction)
	}
	// 突き合わせ表示の材料（発行元未決事項・影響範囲・「不明」回答）は AI なしで揃う。
	if len(got.SourceIssueIDs) != 2 {
		t.Fatalf("発行元未決事項が %d 件です（期待 2）: %+v", len(got.SourceIssueIDs), got.SourceIssueIDs)
	}
	if blocked := got.AffectedRequirements[f.issueIDs[0]]; len(blocked) != 1 {
		t.Fatalf("影響範囲が返っていません: %+v", got.AffectedRequirements)
	}
	if len(got.UnknownAnswers) != 1 {
		t.Fatalf("「不明」回答が返っていません: %+v", got.UnknownAnswers)
	}
	if !strings.Contains(got.Notice, "AI") {
		t.Fatalf("縮退の理由が案内されていません: %q", got.Notice)
	}

	// 手動編集した決着内容で同じ反映経路を通せる。
	manual := ImportApproval{ApprovalRequest: ApprovalRequest{
		Decisions: []DecisionApproval{{
			Candidate: DecisionCandidate{
				TopicKey: "functional/scope", Body: "在庫引当は受注確定時に即時で行う。",
				EvidenceRefs: []string{projectstore.AnswerRef(f.questionnaire.ID, "q-01")},
			},
			ResolvesIssueIDs: []string{f.issueIDs[0]},
		}},
	}}
	result, err := e.ApplyImportApproval(f.questionnaire.ID, manual)
	if err != nil {
		t.Fatalf("手動編集での反映に失敗: %v", err)
	}
	if result.QuestionnaireStatus != projectstore.QuestionnaireImported {
		t.Fatalf("取込済みになっていません: %s", result.QuestionnaireStatus)
	}
	issue, err := store.LoadOpenIssue(f.issueIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != projectstore.OpenIssueResolved {
		t.Fatalf("手動反映で未決事項が決着していません: %s", issue.Status)
	}
}

// 「不明」の確認先は承認された場合だけ owner へ反映する。
func TestApplyImportApprovalUpdatesOwnerOnApproval(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)
	stub.scripts = []string{buildAnalysisScript(f)}

	analysis, err := e.AnalyzeAnswers(context.Background(), f.questionnaire.ID)
	if err != nil {
		t.Fatal(err)
	}
	u := analysis.UnknownAnswers[0]
	if _, err := e.ApplyImportApproval(f.questionnaire.ID, ImportApproval{
		OwnerUpdates: []OwnerUpdate{{IssueID: u.SourceIssueID, Owner: u.OwnerCandidate}},
	}); err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}
	issue, err := store.LoadOpenIssue(u.SourceIssueID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Owner != "経理部 田中" {
		t.Fatalf("決める人が更新されていません: %q", issue.Owner)
	}
}

// 取込済みの質問票へ再度反映しようとしても状態機械を壊さない。
func TestApplyImportApprovalRejectsWrongStatus(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)

	if _, err := e.ApplyImportApproval(f.questionnaire.ID, ImportApproval{}); err != nil {
		t.Fatalf("1 回目の反映に失敗: %v", err)
	}
	if _, err := e.ApplyImportApproval(f.questionnaire.ID, ImportApproval{}); err == nil {
		t.Fatal("取込済みの質問票へ二重に反映できました")
	}
	q, err := store.LoadQuestionnaire(f.questionnaire.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q.Status != projectstore.QuestionnaireImported {
		t.Fatalf("状態が %s です（期待 %s）", q.Status, projectstore.QuestionnaireImported)
	}
}

// 回帰テスト: エラーイベントが届かないまま応答が空でも panic せず縮退する。
func TestAnalyzeAnswersFallsBackOnEmptyResponse(t *testing.T) {
	stub := &stubAdapter{scripts: []string{""}}
	e, store := newTestEngine(t, stub)
	f := newImportFixture(t, store)

	got, err := e.AnalyzeAnswers(context.Background(), f.questionnaire.ID)
	if err != nil {
		t.Fatalf("空応答でエラーが返りました（縮退すべき）: %v", err)
	}
	if !got.Fallback || got.Notice == "" {
		t.Fatalf("縮退として扱われていません: %+v", got)
	}
	if len(got.UnknownAnswers) != 1 {
		t.Fatalf("突き合わせ表示の材料が返っていません: %+v", got)
	}
}
