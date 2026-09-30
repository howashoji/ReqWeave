//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// questionnaireFixture は質問票の発行に必要な前提（名簿・未決事項）を整えた API。
type questionnaireFixture struct {
	api       *API
	root      string
	stub      *streamingStub
	addressee StakeholderView
	issueIDs  []string
}

func newQuestionnaireAPI(t *testing.T) *questionnaireFixture {
	t.Helper()
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	addressee, err := a.AddStakeholder(StakeholderRequest{Name: "佐藤", Org: "営業部"})
	if err != nil {
		t.Fatalf("名簿の登録に失敗: %v", err)
	}

	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, spec := range []struct{ owner, body string }{
		{"営業部長", "在庫引当のタイミングを即時とするか日次とするか"},
		{"情報システム部", "月末締めで手作業になっている範囲"},
	} {
		issue, err := s.store.CreateOpenIssue(projectstore.OpenIssue{
			Owner: spec.owner, Status: projectstore.OpenIssueOpen,
			Evidence: []string{"S-0001#utt-00001"}, NeedsStakeholder: true, Body: spec.body,
		})
		if err != nil {
			t.Fatalf("未決事項を作れない: %v", err)
		}
		ids = append(ids, issue.ID)
	}
	return &questionnaireFixture{api: a, root: root, stub: stub, addressee: addressee, issueIDs: ids}
}

// draftRequest は 2 問の発行内容（1 問目は選択式、2 問目は自由記述）。
func (f *questionnaireFixture) draftRequest() IssueDraftRequest {
	return IssueDraftRequest{
		AddresseeRef: f.addressee.ID,
		Questions: []QuestionInput{
			{SourceIssue: f.issueIDs[0], Text: "在庫の引き当ては、注文を受けた時点で行いますか。",
				Background: "取り消しの扱いが変わります。", AnswerFormat: projectstore.AnswerFormatChoice,
				Choices: []string{"受注した時点で行う", "1日1回まとめて行う"}},
			{SourceIssue: f.issueIDs[1], Text: "月末の締め作業で手作業になっている工程を教えてください。",
				Background: "対象範囲の判断に使います。", AnswerFormat: projectstore.AnswerFormatFree},
		},
	}
}

// プレビューは実際の出力内容そのものから作られ、プレビューだけでは何も書かない。
func TestPreviewAndIssueQuestionnaire(t *testing.T) {
	f := newQuestionnaireAPI(t)

	preview, err := f.api.PreviewQuestionnaireIssue(f.draftRequest())
	if err != nil {
		t.Fatalf("プレビューに失敗: %v", err)
	}
	if preview.QuestionCount != 2 || preview.Addressee != "佐藤（営業部）" {
		t.Fatalf("プレビューの内容が違う: %+v", preview)
	}
	if !strings.Contains(preview.QuestionnaireMarkdown, "在庫の引き当ては") {
		t.Fatalf("質問票の全文が入っていない:\n%s", preview.QuestionnaireMarkdown)
	}
	if len(preview.SourceIssues) != 2 {
		t.Fatalf("発行元未決事項が %d 件: %+v", len(preview.SourceIssues), preview.SourceIssues)
	}
	if len(preview.Excluded) == 0 {
		t.Fatal("「含まれないもの」の表示がない")
	}
	// プレビューではレコードもファイルも作らない。
	list, err := f.api.Questionnaires()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("プレビューで質問票が作られた: %+v", list)
	}

	dst := filepath.Join(t.TempDir(), "QS")
	result, err := f.api.IssueQuestionnaire(f.draftRequest(), dst)
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	if result.QuestionnaireID != preview.QuestionnaireID {
		t.Fatalf("プレビューと発行の ID が違う: %s / %s", preview.QuestionnaireID, result.QuestionnaireID)
	}
	if result.Passcode == "" || result.PasscodeNotice == "" {
		t.Fatalf("パスコードと案内が返っていない: %+v", result)
	}
	if !strings.Contains(result.PasscodeNotice, "別の経路") || !strings.Contains(result.PasscodeNotice, "再表示できません") {
		t.Fatalf("別経路伝達・再表示不可の案内がない: %q", result.PasscodeNotice)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("発行用ファイルが出力されていない: %v", err)
	}

	// 出力したファイルの中身がプレビューと一致する（表示と実際の内容を食い違わせない）。
	sess, err := exchange.NewWorkspace(projectstore.AppPaths{Base: filepath.Join(t.TempDir(), "resp")}).
		Open(result.Path, result.Passcode)
	if err != nil {
		t.Fatalf("発行ファイルを回答モードで開けない: %v", err)
	}
	if len(sess.Questionnaire.Questions) != 2 ||
		sess.Questionnaire.Questions[0].Text != "在庫の引き当ては、注文を受けた時点で行いますか。" {
		t.Fatalf("出力内容がプレビューと違う: %+v", sess.Questionnaire.Questions)
	}

	// 変更履歴に質問票の作成が残る。
	changes, err := auditlog.ReadChanges(f.root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range changes {
		if c.Target == result.QuestionnaireID && c.Change == auditlog.ChangeCreated {
			found = true
		}
	}
	if !found {
		t.Fatalf("質問票の発行が変更履歴に残っていない: %+v", changes)
	}
}

// 一覧が宛先・発行日時・状態・経過日数を返す。
func TestQuestionnairesListShowsElapsedDays(t *testing.T) {
	f := newQuestionnaireAPI(t)
	result, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	list, err := f.api.Questionnaires()
	if err != nil {
		t.Fatalf("一覧を取得できない: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("一覧が %d 件: %+v", len(list), list)
	}
	row := list[0]
	if row.ID != result.QuestionnaireID || row.Addressee != "佐藤（営業部）" {
		t.Fatalf("一覧の内容が違う: %+v", row)
	}
	if row.IssuedAt.IsZero() {
		t.Fatal("発行日時が空")
	}
	if row.Status != projectstore.QuestionnaireIssued || row.StatusLabel != "発行済み" {
		t.Fatalf("状態の表示が違う: %+v", row)
	}
	if row.ElapsedDays != 0 {
		t.Fatalf("発行当日の経過日数が %d 日: %+v", row.ElapsedDays, row)
	}
	if len(row.SourceIssues) != 2 {
		t.Fatalf("発行元未決事項が %d 件: %+v", len(row.SourceIssues), row.SourceIssues)
	}
}

// 経過日数は暦日（時刻差ではなく日付差）で数える。
func TestElapsedDaysCountsCalendarDays(t *testing.T) {
	jst := time.FixedZone("JST", 9*60*60)
	// 発行 = JST 2026-08-27 23:00、参照 = JST 2026-08-28 01:00（時刻差 2 時間・暦日差 1 日）。
	issued := time.Date(2026, 8, 27, 23, 0, 0, 0, jst).UTC()
	now := time.Date(2026, 8, 28, 1, 0, 0, 0, jst)
	if got := elapsedDays(issued, now); got != 1 {
		t.Fatalf("暦日差が %d です（期待 1）", got)
	}
	// 同日内は 0 日。
	sameDay := time.Date(2026, 8, 27, 23, 59, 0, 0, jst)
	if got := elapsedDays(issued, sameDay); got != 0 {
		t.Fatalf("同日の経過日数が %d です（期待 0）", got)
	}
	// 3 日後。
	later := time.Date(2026, 8, 30, 0, 30, 0, 0, jst)
	if got := elapsedDays(issued, later); got != 3 {
		t.Fatalf("3 日後の経過日数が %d です（期待 3）", got)
	}
}

// 再発行は新しいパスコードで再出力し、引き継がれない旨を案内する。
func TestReissueQuestionnaire(t *testing.T) {
	f := newQuestionnaireAPI(t)
	first, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	again, err := f.api.ReissueQuestionnaire(first.QuestionnaireID, filepath.Join(t.TempDir(), "QS-again"))
	if err != nil {
		t.Fatalf("再発行に失敗: %v", err)
	}
	if again.Passcode == first.Passcode {
		t.Fatal("再発行で同じパスコードが返った")
	}
	if !again.Reissued || !strings.Contains(again.PasscodeNotice, "引き継がれません") {
		t.Fatalf("再発行の案内がない: %+v", again)
	}
	// 旧ファイルのパスコードでは新ファイルを開けない（鍵が変わっている）。
	ws := exchange.NewWorkspace(projectstore.AppPaths{Base: filepath.Join(t.TempDir(), "resp")})
	if _, err := ws.Open(again.Path, first.Passcode); err == nil {
		t.Fatal("旧パスコードで再発行ファイルを開けた")
	}
	if _, err := ws.Open(again.Path, again.Passcode); err != nil {
		t.Fatalf("新パスコードで開けない: %v", err)
	}
}

// 検証 → 取込の 2 段。検証だけではプロジェクトデータを変えない。
func TestValidateAndImportReturnFile(t *testing.T) {
	f := newQuestionnaireAPI(t)
	issued, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	returnFile := respondAndReturn(t, issued)

	review, err := f.api.ValidateReturnFile(returnFile)
	if err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if review.QuestionnaireID != issued.QuestionnaireID {
		t.Fatalf("検証結果の質問票が違う: %+v", review)
	}
	if len(review.Warnings) != 0 {
		t.Fatalf("正規の返送ファイルで警告が出た: %+v", review.Warnings)
	}
	if len(review.Matches) != 2 || !review.Matches[0].Answered {
		t.Fatalf("突合表示が組み立てられていない: %+v", review.Matches)
	}
	if review.Matches[0].SourceIssue != f.issueIDs[0] {
		t.Fatalf("発行元未決事項が紐づいていない: %+v", review.Matches[0])
	}
	// 検証だけでは状態が変わらない。
	list, err := f.api.Questionnaires()
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Status != projectstore.QuestionnaireIssued {
		t.Fatalf("検証で状態が変わった: %s", list[0].Status)
	}

	result, err := f.api.ImportReturnFile(exchange.ImportConfirmation{})
	if err != nil {
		t.Fatalf("取込に失敗: %v", err)
	}
	if result.Status != projectstore.QuestionnaireAnswered || result.StatusLabel != "回答済み" {
		t.Fatalf("取込後の状態が違う: %+v", result)
	}
	// 取り込んだ返送ファイルは 1 度きり（続けて呼んでも二重取込しない）。
	if _, err := f.api.ImportReturnFile(exchange.ImportConfirmation{}); err == nil {
		t.Fatal("同じ返送ファイルを続けて取り込めた")
	}
}

// 取込後の分析 → 承認で決着が反映され、質問票が取込済みになる。
func TestAnalyzeAndApproveImportDiff(t *testing.T) {
	f := newQuestionnaireAPI(t)
	issued, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	returnFile := respondAndReturn(t, issued)
	if _, err := f.api.ValidateReturnFile(returnFile); err != nil {
		t.Fatalf("検証に失敗: %v", err)
	}
	if _, err := f.api.ImportReturnFile(exchange.ImportConfirmation{}); err != nil {
		t.Fatalf("取込に失敗: %v", err)
	}

	f.stub.scripts = []string{`{
 "decisions":[{"topic_key":"functional/scope","body":"在庫引当は受注確定時に即時で行う。",
  "rationale":"回答のとおり","evidence_refs":["` + issued.QuestionnaireID + `#q-01"],"supersedes_decision_id":null}],
 "open_issues":[],"requirement_updates":[],"term_candidates":[],"contradictions":[]}`}

	analysis, err := f.api.AnalyzeImportedAnswers(issued.QuestionnaireID)
	if err != nil {
		t.Fatalf("分析に失敗: %v", err)
	}
	if analysis.Fallback {
		t.Fatalf("正常応答なのに縮退した: %+v", analysis)
	}
	if len(analysis.Extraction.Decisions) != 1 {
		t.Fatalf("決着案が %d 件: %+v", len(analysis.Extraction.Decisions), analysis.Extraction.Decisions)
	}

	applied, err := f.api.ApproveImportDiff(issued.QuestionnaireID, dialogue.ImportApproval{
		ApprovalRequest: dialogue.ApprovalRequest{Decisions: []dialogue.DecisionApproval{{
			Candidate: analysis.Extraction.Decisions[0], ResolvesIssueIDs: []string{f.issueIDs[0]},
		}}},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	if applied.Applied.QuestionnaireStatus != projectstore.QuestionnaireImported {
		t.Fatalf("取込済みになっていない: %+v", applied)
	}
	list, err := f.api.Questionnaires()
	if err != nil {
		t.Fatal(err)
	}
	if list[0].StatusLabel != "取込済み" {
		t.Fatalf("一覧の状態表示が違う: %+v", list[0])
	}
}

// 閲覧権限では発行・取込・承認ができず、理由が日本語 1 文で返る。
func TestQuestionnaireBindingDeniesViewer(t *testing.T) {
	f := newQuestionnaireAPI(t)
	issued, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("前提の発行に失敗: %v", err)
	}
	returnFile := respondAndReturn(t, issued)

	demoteToViewer(t, f.root, "k.sato@example.co.jp")

	denied := []struct {
		name string
		call func() error
	}{
		{"質問文の生成", func() error { _, err := f.api.GenerateQuestionDrafts(f.issueIDs); return err }},
		{"発行プレビュー", func() error { _, err := f.api.PreviewQuestionnaireIssue(f.draftRequest()); return err }},
		{"発行", func() error {
			_, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS2"))
			return err
		}},
		{"再発行", func() error {
			_, err := f.api.ReissueQuestionnaire(issued.QuestionnaireID, filepath.Join(t.TempDir(), "QS3"))
			return err
		}},
		{"返送ファイルの検証", func() error { _, err := f.api.ValidateReturnFile(returnFile); return err }},
		{"取込", func() error { _, err := f.api.ImportReturnFile(exchange.ImportConfirmation{}); return err }},
		{"分析", func() error { _, err := f.api.AnalyzeImportedAnswers(issued.QuestionnaireID); return err }},
		{"差分承認", func() error {
			_, err := f.api.ApproveImportDiff(issued.QuestionnaireID, dialogue.ImportApproval{})
			return err
		}},
	}
	for _, tc := range denied {
		err := tc.call()
		if err == nil {
			t.Fatalf("閲覧権限で %s ができてしまった", tc.name)
		}
		if !strings.Contains(err.Error(), "編集権限が必要です") {
			t.Fatalf("%s の拒否理由が示されていない: %v", tc.name, err)
		}
		assertUserFacingMessage(t, tc.name, err.Error())
	}

	// 参照は閲覧権限でもできる。
	list, err := f.api.Questionnaires()
	if err != nil {
		t.Fatalf("閲覧権限で一覧を取得できない: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("一覧の内容が違う: %+v", list)
	}
}

// 画面へ返すメッセージに内部 ID・生のコード値・パスコードを出さない。
func TestQuestionnaireBindingMessagesAreUserFacing(t *testing.T) {
	f := newQuestionnaireAPI(t)

	cases := []struct {
		name string
		call func() error
	}{
		{"宛先が名簿にない", func() error {
			req := f.draftRequest()
			req.AddresseeRef = "STK-999"
			_, err := f.api.PreviewQuestionnaireIssue(req)
			return err
		}},
		{"質問が空", func() error {
			_, err := f.api.PreviewQuestionnaireIssue(IssueDraftRequest{AddresseeRef: f.addressee.ID})
			return err
		}},
		{"検証前の取込", func() error {
			_, err := f.api.ImportReturnFile(exchange.ImportConfirmation{})
			return err
		}},
		{"返送ファイルでないものを読み込む", func() error {
			path := filepath.Join(t.TempDir(), "not-a-return.rwva")
			if err := writeFile(path, "これは受け渡しファイルではありません"); err != nil {
				t.Fatal(err)
			}
			_, err := f.api.ValidateReturnFile(path)
			return err
		}},
	}
	for _, tc := range cases {
		err := tc.call()
		if err == nil {
			t.Fatalf("%s でエラーにならなかった", tc.name)
		}
		assertUserFacingMessage(t, tc.name, err.Error())
	}
}

// assertUserFacingMessage は利用者向けメッセージの禁則を検査する（内部識別子・英字コード値の露出）。
func assertUserFacingMessage(t *testing.T, name, msg string) {
	t.Helper()
	for _, banned := range []string{"issued", "answered", "imported", "questionnaire_id", "payload.enc",
		"manifest.yaml", "argon2id", "*projectstore", "0x"} {
		if strings.Contains(msg, banned) {
			t.Fatalf("%s のメッセージに内部の値が出ています（%q）: %s", name, banned, msg)
		}
	}
}

// respondAndReturn は回答モードで全質問に回答し、返送ファイルを出力して返す。
func respondAndReturn(t *testing.T, issued IssueResultView) string {
	t.Helper()
	ws := exchange.NewWorkspace(projectstore.AppPaths{Base: filepath.Join(t.TempDir(), "respondent")})
	sess, err := ws.Open(issued.Path, issued.Passcode)
	if err != nil {
		t.Fatalf("回答モードで開けない: %v", err)
	}
	at := time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)
	for _, q := range sess.Questionnaire.Questions {
		a := projectstore.Answer{QuestionID: q.ID, Kind: projectstore.AnswerKindAnswered, At: at}
		if len(q.Choices) > 0 {
			a.Selected = []string{q.Choices[0]}
		} else {
			a.FreeText = "請求書の突合が手作業です。"
		}
		if err := sess.SetAnswer(a); err != nil {
			t.Fatalf("回答の保存に失敗: %v", err)
		}
	}
	dst := filepath.Join(t.TempDir(), sess.SuggestedReturnName())
	if err := sess.Finalize(dst); err != nil {
		t.Fatalf("回答の確定に失敗: %v", err)
	}
	return dst
}
