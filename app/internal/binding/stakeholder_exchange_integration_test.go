//go:build integration

// 結合テスト（ステークホルダー連携の一巡 = 質問票の発行・回答・取込）。
//
// 担当者側（発行・取込・反映）と回答モード側（パスコード・回答・返送）を同一プロセス内で
// 往復させ、実ファイル（.rwvq / .rwva）を介して受け渡す。AI はテスト用スタブで決定的に行う。

package binding

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// exchangeAnalysisScript は取込時の分析結果（AI の応答の形式。根拠は回答参照）。
const exchangeAnalysisScript = `{
 "decisions":[{"topic_key":"functional/scope","body":"在庫引当は受注確定時に即時で行う。",
  "rationale":"ステークホルダーの回答のとおり","evidence_refs":["%QS%#q-01"],"supersedes_decision_id":null}],
 "open_issues":[],
 "requirement_updates":[],
 "term_candidates":[],
 "contradictions":[]}`

// 発行 → 回答 → 返送 → 取込 → 承認反映を 1 本で通す。
func TestStakeholderExchangeRoundTrip(t *testing.T) {
	owner := newQuestionnaireAPI(t)

	// --- 発行 ---------------------------------------------------
	preview, err := owner.api.PreviewQuestionnaireIssue(owner.draftRequest())
	if err != nil {
		t.Fatalf("プレビューに失敗: %v", err)
	}
	if preview.QuestionCount != 2 {
		t.Fatalf("プレビューの質問数が %d です（期待 2）", preview.QuestionCount)
	}
	issued, err := owner.api.IssueQuestionnaire(owner.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	assertQuestionnaireStatus(t, owner.api, issued.QuestionnaireID, projectstore.QuestionnaireIssued)

	// --- 回答モードで回答して返送ファイルを作る --------------------
	respondent := newRespondAPI(t)
	opened, err := respondent.OpenQuestionnaireFile(issued.Path, issued.Passcode)
	if err != nil {
		t.Fatalf("回答モードで開けない: %v", err)
	}
	if len(opened.Questions) != 2 {
		t.Fatalf("質問数が %d です（期待 2）: %+v", len(opened.Questions), opened.Questions)
	}
	// 1 問目は選択、2 問目は「不明」（理由・確認先つき）。
	if _, err := respondent.SaveAnswer(AnswerInput{
		QuestionID: opened.Questions[0].ID, Kind: projectstore.AnswerKindAnswered,
		Selected: []string{opened.Questions[0].Choices[0]},
	}); err != nil {
		t.Fatalf("回答を保存できない: %v", err)
	}
	if _, err := respondent.SaveAnswer(AnswerInput{
		QuestionID: opened.Questions[1].ID, Kind: projectstore.AnswerKindUnknown,
		Body: "理由: 全体像を把握していません\n確認先: 経理部 田中",
	}); err != nil {
		t.Fatalf("回答を保存できない: %v", err)
	}
	returnFile := filepath.Join(t.TempDir(), "return"+exchange.ExtReturn)
	if _, err := respondent.FinalizeAnswers(returnFile); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}

	// --- 取込 -----------------------------------------------------
	review, err := owner.api.ValidateReturnFile(returnFile)
	if err != nil {
		t.Fatalf("返送ファイルの検証に失敗: %v", err)
	}
	if len(review.Warnings) != 0 {
		t.Fatalf("正規の返送ファイルで警告が出た: %+v", review.Warnings)
	}
	imported, err := owner.api.ImportReturnFile(exchange.ImportConfirmation{})
	if err != nil {
		t.Fatalf("取込に失敗: %v", err)
	}
	if imported.Status != projectstore.QuestionnaireAnswered {
		t.Fatalf("取込後の状態が %s です（期待 %s）", imported.Status, projectstore.QuestionnaireAnswered)
	}
	assertQuestionnaireStatus(t, owner.api, issued.QuestionnaireID, projectstore.QuestionnaireAnswered)

	// --- 反映差分の生成と承認 --------------------------------------
	owner.stub.scripts = []string{strings.ReplaceAll(exchangeAnalysisScript, "%QS%", issued.QuestionnaireID)}
	analysis, err := owner.api.AnalyzeImportedAnswers(issued.QuestionnaireID)
	if err != nil {
		t.Fatalf("分析に失敗: %v", err)
	}
	if analysis.Fallback {
		t.Fatalf("正常応答なのに縮退した: %+v", analysis)
	}
	if len(analysis.Extraction.Decisions) != 1 {
		t.Fatalf("決着案が %d 件です（期待 1）: %+v", len(analysis.Extraction.Decisions), analysis.Extraction.Decisions)
	}
	if len(analysis.UnknownAnswers) != 1 || analysis.UnknownAnswers[0].OwnerCandidate != "経理部 田中" {
		t.Fatalf("「不明」回答と確認先が出ていない: %+v", analysis.UnknownAnswers)
	}

	applied, err := owner.api.ApproveImportDiff(issued.QuestionnaireID, dialogue.ImportApproval{
		ApprovalRequest: dialogue.ApprovalRequest{Decisions: []dialogue.DecisionApproval{{
			Candidate: analysis.Extraction.Decisions[0], ResolvesIssueIDs: []string{owner.issueIDs[0]},
		}}},
		UnknownNotes: []dialogue.UnknownAnswerNote{{
			IssueID:    analysis.UnknownAnswers[0].SourceIssueID,
			AnswerRef:  analysis.UnknownAnswers[0].AnswerRef,
			AnsweredAt: analysis.UnknownAnswers[0].AnsweredAt,
			Reason:     analysis.UnknownAnswers[0].Reason,
		}},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}

	// 状態遷移 issued → answered → imported。
	if applied.Applied.QuestionnaireStatus != projectstore.QuestionnaireImported {
		t.Fatalf("反映後の状態が %s です（期待 %s）", applied.Applied.QuestionnaireStatus, projectstore.QuestionnaireImported)
	}
	assertQuestionnaireStatus(t, owner.api, issued.QuestionnaireID, projectstore.QuestionnaireImported)

	// 未決事項が決着し、決定事項の根拠が回答参照になる（どの回答から決まったかを辿れる）。
	s, err := owner.api.current()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.store.LoadOpenIssue(owner.issueIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != projectstore.OpenIssueResolved || resolved.ResolvedBy == "" {
		t.Fatalf("未決事項が決着していない: status=%s resolved_by=%s", resolved.Status, resolved.ResolvedBy)
	}
	decision, err := s.store.LoadDecision(resolved.ResolvedBy)
	if err != nil {
		t.Fatal(err)
	}
	wantRef := projectstore.AnswerRef(issued.QuestionnaireID, "q-01")
	if len(decision.Evidence) != 1 || decision.Evidence[0] != wantRef {
		t.Fatalf("決定事項の根拠が %v です（期待 [%s]）", decision.Evidence, wantRef)
	}

	// 「不明」の未決事項は決着させず、経過を追記する。
	pending, err := s.store.LoadOpenIssue(owner.issueIDs[1])
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != projectstore.OpenIssueOpen {
		t.Fatalf("「不明」回答で未決事項が決着した: %s", pending.Status)
	}
	if !strings.Contains(pending.Body, "全体像を把握していません") {
		t.Fatalf("「不明」の経過が追記されていない:\n%s", pending.Body)
	}
}

// 発行・返送の両ファイルとも、鍵・パスコードなしでは業務情報を平文で取り出せない。
func TestExchangeFilesHideBusinessContent(t *testing.T) {
	owner := newQuestionnaireAPI(t)
	issued, err := owner.api.IssueQuestionnaire(owner.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	respondent := newRespondAPI(t)
	opened, err := respondent.OpenQuestionnaireFile(issued.Path, issued.Passcode)
	if err != nil {
		t.Fatalf("回答モードで開けない: %v", err)
	}
	const secretAnswer = "請求書の突合を手作業でやっています"
	for i, q := range opened.Questions {
		in := AnswerInput{QuestionID: q.ID, Kind: projectstore.AnswerKindAnswered}
		if i == 0 {
			in.Selected = []string{q.Choices[0]}
		} else {
			in.FreeText = secretAnswer
		}
		if _, err := respondent.SaveAnswer(in); err != nil {
			t.Fatal(err)
		}
	}
	returnFile := filepath.Join(t.TempDir(), "return"+exchange.ExtReturn)
	if _, err := respondent.FinalizeAnswers(returnFile); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}

	// 業務情報（質問文・背景説明・宛先・回答本文）がバイト列として現れない。
	needles := []string{
		opened.Questions[0].Text, opened.Questions[0].Background,
		owner.addressee.Name, owner.addressee.Org, secretAnswer,
	}
	for _, path := range []string{issued.Path, returnFile} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range needles {
			if needle == "" {
				t.Fatalf("検索語が空です（検証にならない）: %q", needles)
			}
			if bytes.Contains(body, []byte(needle)) {
				t.Fatalf("%s に業務情報が平文で現れました: %q", filepath.Base(path), needle)
			}
		}
	}
}

// 回答・取込の異常系（誤ったパスコード・別プロジェクト宛など）: 取込が停止し、プロジェクトデータが変わらない。
func TestExchangeAnomaliesStopWithoutChangingProject(t *testing.T) {
	t.Run("誤ったパスコードでは開けない", func(t *testing.T) {
		owner := newQuestionnaireAPI(t)
		issued, err := owner.api.IssueQuestionnaire(owner.draftRequest(), filepath.Join(t.TempDir(), "QS"))
		if err != nil {
			t.Fatalf("発行に失敗: %v", err)
		}
		respondent := newRespondAPI(t)
		if _, err := respondent.OpenQuestionnaireFile(issued.Path, "WrongPasscode99"); err == nil {
			t.Fatal("誤ったパスコードで開けた")
		} else if !strings.Contains(err.Error(), "システム担当者へ連絡してください") {
			t.Fatalf("担当者への連絡案内がない: %v", err)
		}
		// 質問票の状態は発行済みのまま。
		assertQuestionnaireStatus(t, owner.api, issued.QuestionnaireID, projectstore.QuestionnaireIssued)
	})

	t.Run("別プロジェクト宛のファイルは取り込まない", func(t *testing.T) {
		owner := newQuestionnaireAPI(t)
		if _, err := owner.api.IssueQuestionnaire(owner.draftRequest(), filepath.Join(t.TempDir(), "QS")); err != nil {
			t.Fatalf("発行に失敗: %v", err)
		}
		before := snapshotProject(t, owner)

		// 別のプロジェクトで発行・回答した返送ファイルを用意する。
		other := newQuestionnaireAPI(t)
		otherIssued, err := other.api.IssueQuestionnaire(other.draftRequest(), filepath.Join(t.TempDir(), "QS"))
		if err != nil {
			t.Fatalf("別プロジェクトの発行に失敗: %v", err)
		}
		otherReturn := respondAndReturn(t, otherIssued)

		if _, err := owner.api.ValidateReturnFile(otherReturn); err == nil {
			t.Fatal("別プロジェクト宛のファイルを取り込めた")
		} else if !strings.Contains(err.Error(), "別のプロジェクト") {
			t.Fatalf("宛先違いとして扱われていない: %v", err)
		}
		assertUnchanged(t, owner, before)
	})

	t.Run("質問部が改変された返送ファイルは確認なしに取り込まない", func(t *testing.T) {
		owner := newQuestionnaireAPI(t)
		issued, err := owner.api.IssueQuestionnaire(owner.draftRequest(), filepath.Join(t.TempDir(), "QS"))
		if err != nil {
			t.Fatalf("発行に失敗: %v", err)
		}
		before := snapshotProject(t, owner)
		tampered := writeTamperedReturn(t, issued)

		review, err := owner.api.ValidateReturnFile(tampered)
		if err != nil {
			t.Fatalf("検証で止まってしまった（警告として続行するはず）: %v", err)
		}
		if !hasWarning(review, exchange.WarnContentModified) {
			t.Fatalf("改変が検出されていない: %+v", review.Warnings)
		}
		// 確認しないまま取り込もうとしても書き込まない。
		if _, err := owner.api.ImportReturnFile(exchange.ImportConfirmation{}); err == nil {
			t.Fatal("確認なしに改変ファイルを取り込めた")
		}
		assertUnchanged(t, owner, before)
	})
}

// hasWarning は指定種別の警告があるかを返す。
func hasWarning(review ImportReviewView, kind string) bool {
	for _, w := range review.Warnings {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// projectSnapshot は取込前後で不変であるべきプロジェクトの状態。
type projectSnapshot struct {
	questionnaires []QuestionnaireView
	decisions      int
	issueStatuses  map[string]string
}

func snapshotProject(t *testing.T, f *questionnaireFixture) projectSnapshot {
	t.Helper()
	list, err := f.api.Questionnaires()
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.api.current()
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := s.store.ListDecisions()
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, id := range f.issueIDs {
		issue, err := s.store.LoadOpenIssue(id)
		if err != nil {
			t.Fatal(err)
		}
		statuses[id] = issue.Status
	}
	return projectSnapshot{questionnaires: list, decisions: len(decisions), issueStatuses: statuses}
}

func assertUnchanged(t *testing.T, f *questionnaireFixture, before projectSnapshot) {
	t.Helper()
	after := snapshotProject(t, f)
	if len(after.questionnaires) != len(before.questionnaires) {
		t.Fatalf("質問票の件数が変わった: %d → %d", len(before.questionnaires), len(after.questionnaires))
	}
	for i := range after.questionnaires {
		if after.questionnaires[i].Status != before.questionnaires[i].Status {
			t.Fatalf("質問票 %s の状態が変わった: %s → %s", after.questionnaires[i].ID,
				before.questionnaires[i].Status, after.questionnaires[i].Status)
		}
	}
	if after.decisions != before.decisions {
		t.Fatalf("決定事項の件数が変わった: %d → %d", before.decisions, after.decisions)
	}
	for id, status := range before.issueStatuses {
		if after.issueStatuses[id] != status {
			t.Fatalf("未決事項 %s の状態が変わった: %s → %s", id, status, after.issueStatuses[id])
		}
	}
}

func assertQuestionnaireStatus(t *testing.T, a *API, id, want string) {
	t.Helper()
	list, err := a.Questionnaires()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range list {
		if q.ID == id {
			if q.Status != want {
				t.Fatalf("質問票 %s の状態が %s です（期待 %s）", id, q.Status, want)
			}
			return
		}
	}
	t.Fatalf("質問票 %s が一覧にありません: %+v", id, list)
}

// writeTamperedReturn は質問部を書き換えた返送ファイルを作る（第三者による改変を模す）。
func writeTamperedReturn(t *testing.T, issued IssueResultView) string {
	t.Helper()
	container, err := exchange.ReadContainer(issued.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := container.OpenIssuePayload(issued.Passcode)
	if err != nil {
		t.Fatal(err)
	}
	questionnaire := payload[exchange.EntryQuestionnaire]
	if !bytes.Contains(questionnaire, []byte("在庫の引き当ては")) {
		t.Fatalf("改変対象の質問文が見つからない:\n%s", questionnaire)
	}
	tampered := bytes.Replace(questionnaire, []byte("在庫の引き当ては"), []byte("在庫の払い出しは"), 1)

	var content exchange.ContentMeta
	if err := yaml.Unmarshal(payload[exchange.EntryContent], &content); err != nil {
		t.Fatal(err)
	}
	returnKey, err := base64.StdEncoding.DecodeString(content.ReturnKey)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := projectstore.UnmarshalExchangeQuestionnaire(tampered)
	if err != nil {
		t.Fatal(err)
	}
	answers := &projectstore.Answers{
		QuestionnaireID: parsed.ID, Respondent: parsed.Addressee,
		AnsweredAt: container.Manifest.IssuedAt,
	}
	for _, q := range parsed.Questions {
		answers.Answers = append(answers.Answers, projectstore.Answer{
			QuestionID: q.ID, Kind: projectstore.AnswerKindUnknown,
			At: container.Manifest.IssuedAt, Body: "改変されたファイルからの回答",
		})
	}
	answersMD, err := answers.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "tampered"+exchange.ExtReturn)
	if err := exchange.WriteReturnFile(dst, exchange.Manifest{
		ExchangeFormatVersion: exchange.CurrentFormatVersion,
		Kind:                  exchange.KindReturn,
		ProjectID:             container.Manifest.ProjectID,
		QuestionnaireID:       container.Manifest.QuestionnaireID,
		IssuedAt:              container.Manifest.IssuedAt,
	}, exchange.Payload{
		exchange.EntryQuestionnaire: tampered,
		exchange.EntryContent:       payload[exchange.EntryContent],
		exchange.EntryAnswers:       answersMD,
	}, returnKey); err != nil {
		t.Fatal(err)
	}
	return dst
}
