//go:build integration

// 結合テスト（レコード一覧バインディング × 実ファイル）。

package binding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// openWithRecords はレコードを 1 組作った状態の API を返す。
func openWithRecords(t *testing.T) (*API, string, string) {
	t.Helper()
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	ref := projectstore.UtteranceRef(sess.ID, "utt-00001")
	d, err := s.store.CreateDecision(projectstore.Decision{TopicKey: "background/current-state",
		Body: "Excel 台帳で管理している。", Evidence: []string{ref}})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := s.store.CreateOpenIssue(projectstore.OpenIssue{Owner: "営業部 佐藤", Due: "2020-01-01",
		Evidence: []string{ref}, Body: "棚卸の頻度"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Body: "受注確定時に在庫を引き当てること。",
		AcceptanceCriteria: []string{"3 秒以内"}, Decisions: []string{d.ID},
		BlockedBy: []string{issue.ID}}); err != nil {
		t.Fatal(err)
	}
	return a, sess.ID, ref
}

// 決定事項一覧は本文・決定日・根拠を返し、本文を書き換える API を持たない。
func TestDecisionsList(t *testing.T) {
	a, _, ref := openWithRecords(t)
	got, err := a.Decisions()
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("件数が違う: %+v", got)
	}
	if got[0].ID != "DEC-001" || got[0].DecidedAt == "" || len(got[0].Evidence) != 1 || got[0].Evidence[0] != ref {
		t.Errorf("内容が違う: %+v", got[0])
	}
	if !strings.Contains(got[0].Body, "Excel 台帳") {
		t.Errorf("本文が違う: %q", got[0].Body)
	}
}

// 未決事項一覧は決める人・期限・状態・ブロック対象・質問票状態・期限超過を返す。
func TestOpenIssuesList(t *testing.T) {
	a, _, _ := openWithRecords(t)
	got, err := a.OpenIssues()
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("件数が違う: %+v", got)
	}
	i := got[0]
	if i.Owner != "営業部 佐藤" || i.Due != "2020-01-01" || i.Status != projectstore.OpenIssueOpen {
		t.Errorf("内容が違う: %+v", i)
	}
	if !i.Overdue {
		t.Errorf("期限超過が識別されない: %+v", i)
	}
	if len(i.Blocking) != 1 || i.Blocking[0] != "FR-INV-001" {
		t.Errorf("ブロック対象が違う: %+v", i.Blocking)
	}
	if i.QuestionnaireStatus != "未発行" {
		t.Errorf("質問票の状態が違う: %q", i.QuestionnaireStatus)
	}
}

// 未決事項一覧の質問票の状態は、その未決事項を発行元に持つ質問票から逆引きする
// （未発行 / 発行済み / 回答済み / 取込済み を識別できる）。
// 複数の質問票から問われている未決事項は、最も手前の状態（回答待ちが残っていれば発行済み）を出す。
func TestOpenIssuesQuestionnaireStatus(t *testing.T) {
	a, _, ref := openWithRecords(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	// ISS-001 は openWithRecords が作る。ISS-002〜004 を足す（ISS-004 はどの質問票からも問わない）。
	for _, body := range []string{"締め日の扱い", "返品の扱い", "帳票の様式"} {
		if _, err := s.store.CreateOpenIssue(projectstore.OpenIssue{Owner: "営業部 佐藤", Due: "2030-01-01",
			Evidence: []string{ref}, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	issue := func(sources ...string) string {
		t.Helper()
		q := projectstore.Questionnaire{AddresseeRef: "STK-001", Addressee: "佐藤（営業部）",
			IssuedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), IssuedBy: "k.sato@example.co.jp"}
		for n, src := range sources {
			q.Questions = append(q.Questions, projectstore.Question{
				ID: fmt.Sprintf("q-%02d", n+1), SourceIssue: src, AnswerFormat: projectstore.AnswerFormatFree,
				Text: "運用を教えてください。", Background: "要件の判断に使います。"})
		}
		created, err := s.store.CreateQuestionnaire(q)
		if err != nil {
			t.Fatalf("質問票を作れない: %v", err)
		}
		return created.ID
	}
	advance := func(id string, states ...string) {
		t.Helper()
		for _, st := range states {
			if err := s.store.SetQuestionnaireStatus(id, st); err != nil {
				t.Fatalf("%s を %s へ進められない: %v", id, st, err)
			}
		}
	}

	issue("ISS-001")                        // 発行済みのまま
	answered := issue("ISS-002", "ISS-003") // 回答済みへ
	advance(answered, projectstore.QuestionnaireAnswered)
	imported := issue("ISS-003") // 取込済みへ。ISS-003 は回答済みの質問票からも問われている
	advance(imported, projectstore.QuestionnaireAnswered, projectstore.QuestionnaireImported)

	got, err := a.OpenIssues()
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	status := map[string]string{}
	for _, i := range got {
		status[i.ID] = i.QuestionnaireStatus
	}
	want := map[string]string{
		"ISS-001": "発行済み",
		"ISS-002": "回答済み",
		"ISS-003": "回答済み", // 取込済みの質問票より、回答済みの質問票の方が手前
		"ISS-004": "未発行",
	}
	for id, w := range want {
		if status[id] != w {
			t.Errorf("%s の質問票の状態 = %q, want %q（全体: %v）", id, status[id], w, status)
		}
	}
}

// 要件項目の一覧・手動編集・差し戻し（理由必須）。
func TestRequirementsListAndEdit(t *testing.T) {
	a, _, _ := openWithRecords(t)
	list, err := a.Requirements()
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if len(list) != 1 || list[0].ID != "FR-INV-001" {
		t.Fatalf("一覧が違う: %+v", list)
	}
	if list[0].MissingEvidence {
		t.Errorf("決定事項に紐づくのに参照欠落と判定された: %+v", list[0])
	}
	if len(list[0].BlockedBy) != 1 {
		t.Errorf("ブロックする未決事項が出ない: %+v", list[0])
	}

	// 手動編集（AI 障害中でも可）。
	edited, err := a.EditRequirement(EditRequirementRequest{ID: "FR-INV-001", Title: "在庫引当（改）",
		Body:               "受注確定時に在庫を引き当て、欠品時はバックオーダーを起票すること。",
		AcceptanceCriteria: []string{"3 秒以内", "欠品時はバックオーダー"}})
	if err != nil {
		t.Fatalf("編集に失敗: %v", err)
	}
	if edited.Title != "在庫引当（改）" || len(edited.AcceptanceCriteria) != 2 {
		t.Errorf("編集が反映されない: %+v", edited)
	}
	if _, err := a.EditRequirement(EditRequirementRequest{ID: "FR-INV-001", Title: "", Body: "x"}); err == nil {
		t.Error("要件名が空でも編集できた")
	}

	// 合意 → 差し戻し（理由必須）。
	if _, err := a.AgreeRequirement("FR-INV-001"); err != nil {
		t.Fatalf("合意済みにできない: %v", err)
	}
	if _, err := a.RevertRequirement("FR-INV-001", "  ", WorkStart{Mode: projectstore.ReservationExclusive}); err == nil {
		t.Error("理由なしで差し戻せた")
	}
	reverted, err := a.RevertRequirement("FR-INV-001", "回答取込で前提が変わったため", WorkStart{Mode: projectstore.ReservationExclusive})
	if err != nil {
		t.Fatalf("差し戻しに失敗: %v", err)
	}
	if reverted.Status != projectstore.RequirementDraft || reverted.RevertedReason == "" {
		t.Errorf("差し戻しが反映されない: %+v", reverted)
	}
}

// 根拠参照から発話の前後文脈へ遡及できる。
func TestEvidenceContext(t *testing.T) {
	a, sessionID, ref := openWithRecords(t)
	got, err := a.Evidence(ref)
	if err != nil {
		t.Fatalf("遡及に失敗: %v", err)
	}
	if !got.Found || len(got.Context) == 0 {
		t.Fatalf("根拠発話が見つからない: %+v", got)
	}
	if got.Context[0].ID != "utt-00001" {
		t.Errorf("前後文脈が違う: %+v", got.Context)
	}

	// 参照先が無いときは参照欠落として返す（エラーにしない）。
	missing, err := a.Evidence(projectstore.UtteranceRef(sessionID, "utt-09999"))
	if err != nil {
		t.Fatalf("欠落参照でエラーになった: %v", err)
	}
	if missing.Found {
		t.Errorf("存在しない発話が見つかった扱いになっている: %+v", missing)
	}
}

// 変更履歴を読み出せる（新しい順）。
func TestChangeHistoryList(t *testing.T) {
	a, _, _ := openWithRecords(t)
	if _, err := a.AgreeRequirement("FR-INV-001"); err != nil {
		t.Fatal(err)
	}
	got, err := a.ChangeHistory()
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("変更履歴が空")
	}
	if got[0].Target != "FR-INV-001" || got[0].Change == "" {
		t.Errorf("最新の履歴が違う: %+v", got[0])
	}
	if got[0].Author == "" {
		t.Errorf("作業者が記録されていない: %+v", got[0])
	}
}

// 開いていない状態では拒否する。
func TestRecordsRequireOpenProject(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, _ := newDialogueAPI(t, stub)
	if _, err := a.Decisions(); err == nil {
		t.Error("開いていないのに決定事項を取得できた")
	}
	if _, err := a.Requirements(); err == nil {
		t.Error("開いていないのに要件項目を取得できた")
	}
	if _, err := a.Evidence("S-0001#utt-00001"); err == nil {
		t.Error("開いていないのに遡及できた")
	}
	_ = context.Background()
}

// 要件項目・決定事項を起点に影響一覧を返す。
func TestImpact(t *testing.T) {
	a, _, _ := openWithRecords(t)

	// 決定事項を起点: それを根拠とする要件項目が出る。
	decisions, err := a.Decisions()
	if err != nil || len(decisions) == 0 {
		t.Fatalf("決定事項が無い: %v", err)
	}
	got, err := a.Impact(decisions[0].ID)
	if err != nil {
		t.Fatalf("影響一覧の取得に失敗: %v", err)
	}
	if len(got.Requirements) != 1 || got.Requirements[0] != "FR-INV-001" {
		t.Errorf("根拠とする要件項目が出ない: %+v", got)
	}
	var covered bool
	for _, c := range got.Chapters {
		if c.FileName == "01-business-context.md" {
			covered = true
			if c.Title == "" {
				t.Errorf("章の表示名が無い: %+v", c)
			}
		}
	}
	if !covered {
		t.Errorf("収載する章が出ない: %+v", got.Chapters)
	}

	// 要件項目を起点: ブロックする未決事項と収載章が出る。
	got, err = a.Impact("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OpenIssues) != 1 || got.OpenIssues[0] != "ISS-001" {
		t.Errorf("ブロックする未決事項が出ない: %+v", got)
	}
	var inChapter bool
	for _, c := range got.Chapters {
		if c.FileName == "06-functional-requirements.md" {
			inChapter = true
		}
	}
	if !inChapter {
		t.Errorf("収載する章が出ない: %+v", got.Chapters)
	}

	// 影響の無い ID では空を返す（エラーにしない）。
	got, err = a.Impact("FR-XXX-999")
	if err != nil {
		t.Fatalf("未知の ID でエラーになった: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("影響が無いのに項目が返った: %+v", got)
	}
	if _, err := a.Impact("  "); err == nil {
		t.Error("対象なしで影響一覧を取得できた")
	}
}

// 承認済みレコードの根拠から取り込み元の原本と該当箇所を取得できる。
func TestEvidenceResolvesImportRef(t *testing.T) {
	a, _, _ := openWithRecords(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	const material = "## 在庫管理の現行業務\n\n受注が確定した時点で在庫を引き当てる。\n"
	meta, err := importer.New(s.store).Import(importer.Input{
		Kind: importer.KindMaterial, SourceName: "現行業務.md", SourceFormat: importer.FormatMD,
		Content:          []byte(material),
		ExtractionStatus: importer.StatusExtracted, ExtractedText: material,
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	ref := importer.FormatRef(meta.ID, 3, 3)
	if _, err := s.store.CreateDecision(projectstore.Decision{TopicKey: "business-flow/main-flow",
		Body: "在庫は受注確定時に引き当てる。", Evidence: []string{ref}}); err != nil {
		t.Fatalf("決定を作れない: %v", err)
	}

	got, err := a.Evidence(ref)
	if err != nil {
		t.Fatalf("根拠の遡及に失敗: %v", err)
	}
	if !got.Found || got.Import == nil {
		t.Fatalf("取り込み資料の根拠が解決されていない: %+v", got)
	}
	if got.Import.SourceName != "現行業務.md" ||
		got.Import.SourcePath != importer.SourcePath(meta.ID, importer.FormatMD) {
		t.Errorf("原本の所在が違う: %+v", got.Import)
	}
	if len(got.Import.Excerpt) != 1 || !strings.Contains(got.Import.Excerpt[0], "受注が確定した時点") {
		t.Errorf("該当箇所が違う: %+v", got.Import.Excerpt)
	}
	// 発話の遡及は従来どおり（取り込み対応で壊していない）。
	if len(got.Context) != 0 {
		t.Errorf("取り込み資料の根拠に発話文脈が付いている: %+v", got.Context)
	}

	// 解決できない参照は参照欠落として返す（エラーにしない）。
	broken, err := a.Evidence(importer.FormatRef(meta.ID, 900, 950))
	if err != nil {
		t.Fatalf("解決できない参照でエラーになった: %v", err)
	}
	if broken.Found || broken.Import != nil {
		t.Errorf("存在しない行範囲が解決された: %+v", broken)
	}
}

// 資料 → 承認済みレコードの逆引きは派生（資料側には保持しない）。
func TestEvidenceCitationsAndMissingEvidenceBindings(t *testing.T) {
	a, _, uttRef := openWithRecords(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	const material = "## 棚卸\n\n棚卸は月次で実施する。\n"
	meta, err := importer.New(s.store).Import(importer.Input{
		Kind: importer.KindMinutes, SourceName: "議事録.md", SourceFormat: importer.FormatMD,
		Content:          []byte(material),
		ExtractionStatus: importer.StatusExtracted, ExtractedText: material,
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	ref := importer.FormatRef(meta.ID, 3, 3)
	dec, err := s.store.CreateDecision(projectstore.Decision{TopicKey: "business-flow/main-flow",
		Body: "棚卸は月次。", Evidence: []string{ref}})
	if err != nil {
		t.Fatalf("決定を作れない: %v", err)
	}

	citations, err := a.EvidenceCitations(meta.ID)
	if err != nil {
		t.Fatalf("逆引きに失敗: %v", err)
	}
	if len(citations) != 1 || citations[0].RecordID != dec.ID || citations[0].Ref != ref {
		t.Fatalf("逆引きの結果が違う: %+v", citations)
	}
	if citations[0].RecordKind != projectstore.RecordKindDecision || citations[0].Title != "棚卸は月次。" {
		t.Errorf("逆引きの表示項目が違う: %+v", citations[0])
	}
	// 発話を根拠に持つ既存レコードは資料の逆引きに混ざらない。
	for _, c := range citations {
		if c.Ref == uttRef {
			t.Errorf("別の根拠のレコードが混ざっている: %+v", c)
		}
	}

	// 参照欠落の一覧（openWithRecords が作る要件項目は決定を根拠に持つため含まれない）。
	missing, err := a.MissingEvidenceRecords()
	if err != nil {
		t.Fatalf("参照欠落の一覧に失敗: %v", err)
	}
	for _, m := range missing {
		if m.RecordID == dec.ID {
			t.Errorf("根拠のある決定が参照欠落として出ている: %+v", m)
		}
	}
}

// 回答取込で承認した決定事項の根拠（QS-nnn#q-nn）から、
// その根拠になった質問と回答へ遡及できる。
//
// 質問票の往復（発行 → 回答 → 検証 → 取込 → 分析 → 承認）を通したうえで、
// 承認された決定事項が実際に持っている evidence を入力にして遡及する
// （テストが参照文字列を組み立てると、実装が書いた値とずれても気づけないため）。
func TestEvidenceResolvesImportedAnswer(t *testing.T) {
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
	if _, err := f.api.ApproveImportDiff(issued.QuestionnaireID, dialogue.ImportApproval{
		ApprovalRequest: dialogue.ApprovalRequest{Decisions: []dialogue.DecisionApproval{{
			Candidate: analysis.Extraction.Decisions[0], ResolvesIssueIDs: []string{f.issueIDs[0]},
		}}},
	}); err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}

	// 承認された決定事項が実際に保存した根拠を取り出す。
	decisions, err := f.api.Decisions()
	if err != nil {
		t.Fatal(err)
	}
	var ref string
	for _, d := range decisions {
		for _, e := range d.Evidence {
			if strings.HasPrefix(e, issued.QuestionnaireID+"#") {
				ref = e
			}
		}
	}
	if ref == "" {
		t.Fatalf("回答を根拠に持つ決定事項が無い: %+v", decisions)
	}

	got, err := f.api.Evidence(ref)
	if err != nil {
		t.Fatalf("遡及に失敗: %v", err)
	}
	if !got.Found || got.Answer == nil {
		t.Fatalf("回答へ遡及できない（%s）: %+v", ref, got)
	}
	ans := got.Answer
	if ans.QuestionnaireID != issued.QuestionnaireID || ans.QuestionID != "q-01" {
		t.Errorf("遡及先の質問が違う: %+v", ans)
	}
	// 問いと答えの対が揃っていること（片方だけでは根拠を確認できない）。
	if !strings.Contains(ans.QuestionText, "在庫の引き当ては") {
		t.Errorf("質問本文が返っていない: %+v", ans)
	}
	if ans.Background == "" || ans.SourceIssue != f.issueIDs[0] {
		t.Errorf("背景説明・発行元未決事項が返っていない: %+v", ans)
	}
	if ans.Kind != projectstore.AnswerKindAnswered {
		t.Errorf("回答種別が違う: %+v", ans)
	}
	if len(ans.Selected) != 1 || ans.Selected[0] != "受注した時点で行う" {
		t.Errorf("回答の中身が返っていない: %+v", ans)
	}
	if ans.Respondent == "" || ans.AnsweredAt == "" {
		t.Errorf("回答者・回答日時が返っていない: %+v", ans)
	}
	// 自由記述の設問（q-02）も同様に遡及できる。
	free, err := f.api.Evidence(projectstore.AnswerRef(issued.QuestionnaireID, "q-02"))
	if err != nil {
		t.Fatal(err)
	}
	if !free.Found || free.Answer == nil || free.Answer.FreeText == "" {
		t.Errorf("自由記述の回答へ遡及できない: %+v", free)
	}
}

// 存在しない質問票 ID・存在しない設問番号は参照欠落として返す（エラーにしない）。
func TestEvidenceMissingAnswerIsReportedAsGap(t *testing.T) {
	f := newQuestionnaireAPI(t)
	issued, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}

	// 発行しただけで回答が返っていない状態は「見つからない」（回答があったように見せない）。
	pending, err := f.api.Evidence(projectstore.AnswerRef(issued.QuestionnaireID, "q-01"))
	if err != nil {
		t.Fatalf("未回答の遡及でエラーになった: %v", err)
	}
	if pending.Found || pending.Answer != nil {
		t.Errorf("回答がまだ無いのに見つかった扱いになっている: %+v", pending)
	}

	returnFile := respondAndReturn(t, issued)
	if _, err := f.api.ValidateReturnFile(returnFile); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.ImportReturnFile(exchange.ImportConfirmation{}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		projectstore.AnswerRef("QS-999", "q-01"),               // 質問票が無い
		projectstore.AnswerRef(issued.QuestionnaireID, "q-99"), // 設問が無い
	} {
		got, err := f.api.Evidence(ref)
		if err != nil {
			t.Fatalf("欠落参照（%s）でエラーになった: %v", ref, err)
		}
		if got.Found || got.Answer != nil {
			t.Errorf("存在しない参照（%s）が見つかった扱いになっている: %+v", ref, got)
		}
	}
}
