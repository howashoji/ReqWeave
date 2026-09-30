//go:build integration

// 資料の取り込み・議事録の随時取り込み・進捗レポート・開発 AI フィードバックの取り込みの
// 受け入れ条件と代替・例外フローを、画面が呼ぶ公開バインディングだけを通して実証する統合テスト。
//
// AI プロバイダはスタブへ差し替える（外部 API を呼ばない = 実キーを使わない方針）。
// 資料の取り込みの入力には、実際の要件定義で使う形の Markdown 文書（用語集）を用いる。

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// sampleGlossary は取り込みの入力にする用語集（表・全角文字を含む実際の文書の形）。
const sampleGlossary = `# 用語集

全文書の用語の正本。同義語を新造せず、ここに一本化する。

| 用語 | 英語 | 定義 |
| -- | -- | -- |
| 用語集 | glossary | 全文書の用語の正本 |
| 要件定義書 | requirements specification | システムに求める機能と条件をまとめた文書 |
| 未決事項 | open issue | 誰が・いつまでに・何を決めるかを添えて管理する、決まっていない事項 |
`

// writeSampleDoc は取り込みの入力にする Markdown 文書を一時ディレクトリへ書き出し、そのパスを返す。
func writeSampleDoc(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("入力の文書を書き出せない（%s）: %v", name, err)
	}
	return path
}

// 資料の取り込みの分析応答（資料から決定・未決・要件・用語・観点候補を出す）。
const uc13Response = `{
  "decisions": [{"topic_key": "background/current-state", "body": "用語の正本は用語集とする。",
    "rationale": "資料の記述", "evidence_refs": ["IMP-001#L1-L1"]}],
  "open_issues": [{"topic": "用語の追加手順を決める", "owner": "情報システム部",
    "evidence_refs": ["IMP-001#L1-L1"]}],
  "requirement_updates": [{"operation": "create", "chapter": "functional-requirements",
    "title": "用語集の維持", "body_after": "用語集を成果物と同時に維持すること。",
    "acceptance_criteria": ["新規用語の追加が 1 操作で行えること"],
    "evidence_refs": ["IMP-001#L1-L1"]}],
  "term_candidates": [{"term": "用語集", "english": "glossary",
    "definition": "全文書の用語の正本", "evidence_refs": ["IMP-001#L1-L1"]}],
  "contradictions": [],
  "perspective_candidates": [{"name": "用語の維持体制", "summary": "誰がいつ用語を追加するか",
    "evidence_refs": ["IMP-001#L1-L1"]}]
}`

// 既存資料の取り込みと初期構造化。
//
// 基本フロー 1〜6: 取り込み → 原本保持・抽出 → 送信前プレビューと同意 → 分析 →
// 候補の承認 → 完成度の更新。
func TestUC13ImportProjectDocument(t *testing.T) {
	a, stub := openForImports(t)
	stub.scripts = []string{uc13Response}

	// 1〜2. Markdown の文書を取り込む（原本を保持し、テキストを抽出する）。
	doc := writeSampleDoc(t, "glossary.md", sampleGlossary)
	view, err := a.ImportFile(doc, string(importer.KindMaterial))
	if err != nil {
		t.Fatalf("文書を取り込めない: %v", err)
	}
	if !view.Extracted || view.SourceName != "glossary.md" {
		t.Fatalf("取り込み結果が違う: %+v", view)
	}
	content, err := a.ImportContent(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if content.Extracted != string(original) {
		t.Errorf("抽出テキストが原本と一致しない（md は無加工で保持する）")
	}
	if !strings.HasPrefix(content.Extracted, "# 用語集") {
		t.Errorf("文書の内容が取り込まれていない: %.40s", content.Extracted)
	}

	// 3. 送信前プレビュー（実際の送信内容そのもの）。
	preview, err := a.PreviewImportAnalysis(view.ID)
	if err != nil {
		t.Fatalf("プレビューに失敗: %v", err)
	}
	if preview.ChunkCount < 1 || preview.EstimatedTokens <= 0 {
		t.Fatalf("分割数・概算トークンが出ていない: %+v", preview)
	}
	if stub.calls != 0 {
		t.Fatalf("プレビューで送信された: %d 回", stub.calls)
	}

	// 4. 同意して分析する。
	analysis, err := a.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatalf("分析に失敗: %v", err)
	}
	ex := analysis.Extraction
	if len(ex.Decisions) != 1 || len(ex.OpenIssues) != 1 || len(ex.RequirementUpdates) != 1 ||
		len(ex.TermCandidates) != 1 || len(ex.PerspectiveCandidates) != 1 {
		t.Fatalf("候補が揃っていない: %+v", ex)
	}

	// 5. 承認して反映する（承認していない候補は反映されない）。
	before, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	applied, err := a.ApproveImportCandidates(view.ID, dialogue.FeedbackApproval{
		MaterialApproval: dialogue.MaterialApproval{
			ApprovalRequest: dialogue.ApprovalRequest{
				Decisions:  []dialogue.DecisionApproval{{Candidate: ex.Decisions[0]}},
				OpenIssues: ex.OpenIssues,
				RequirementUpdates: []dialogue.RequirementApproval{
					{Candidate: ex.RequirementUpdates[0], Group: "DOC"}},
				TermCandidates: ex.TermCandidates,
			},
			Perspectives: ex.PerspectiveCandidates,
		},
	})
	if err != nil {
		t.Fatalf("承認の反映に失敗: %v", err)
	}
	if len(applied.Applied.DecisionIDs) != 1 || len(applied.Applied.OpenIssueIDs) != 1 ||
		len(applied.Applied.RequirementIDs) != 1 || len(applied.Applied.PerspectiveIDs) != 1 {
		t.Fatalf("反映結果が違う: %+v", applied)
	}

	// 6. 完成度が更新される。
	after, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	if satisfiedTotal(after) <= satisfiedTotal(before) {
		t.Errorf("承認しても完成度が変わらない: %d → %d", satisfiedTotal(before), satisfiedTotal(after))
	}

	// 根拠から取り込み元へ遡及できる。
	decisions, err := a.Decisions()
	if err != nil {
		t.Fatal(err)
	}
	var ref string
	for _, d := range decisions {
		if len(d.Evidence) > 0 && strings.HasPrefix(d.Evidence[0], "IMP-") {
			ref = d.Evidence[0]
		}
	}
	if ref == "" {
		t.Fatalf("取り込み元の根拠が記録されていない: %+v", decisions)
	}
	ev, err := a.Evidence(ref)
	if err != nil || !ev.Found || ev.Import == nil {
		t.Fatalf("取り込み元へ遡及できない: %v %+v", err, ev)
	}
}

// 取り込みの代替フロー: 抽出できないファイルは原本のみ保持し、その旨を示す（読めない資料も原本を失わないように）。
func TestUC13AlternateExtractionFailure(t *testing.T) {
	a, _ := openForImports(t)
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := a.ImportFile(path, string(importer.KindMaterial))
	if err != nil {
		t.Fatalf("抽出不能ファイルの取り込みで失敗した: %v", err)
	}
	if view.Extracted {
		t.Fatalf("抽出できたことになっている: %+v", view)
	}
	content, err := a.ImportContent(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !content.ExtractionFailed || content.Notice == "" {
		t.Errorf("抽出失敗の案内が無い: %+v", content)
	}
	if _, err := os.Stat(filepath.Join(projectRootOf(t, a), content.SourcePath)); err != nil {
		t.Errorf("原本が保持されていない: %v", err)
	}
	// 分析は開始できない（抽出テキストが無い）。
	if _, err := a.PreviewImportAnalysis(view.ID); err == nil {
		t.Error("抽出できていない資料のプレビューが成立した")
	}
}

// 取り込みの代替フロー: 外部送信に同意しない場合は送信せず、レコードも作らない。
func TestUC13AlternateWithoutConsent(t *testing.T) {
	a, stub := openForImports(t)
	view, err := a.ImportClipboardText("在庫は受注確定時に引き当てる。", string(importer.KindMaterial))
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.Decisions()
	if err != nil {
		t.Fatal(err)
	}

	analysis, err := a.AnalyzeImport(view.ID, false)
	if err == nil && analysis != nil && !analysis.Fallback {
		t.Fatalf("同意なしで分析が成立した: %+v", analysis)
	}
	if stub.calls != 0 {
		t.Fatalf("同意なしで送信された: %d 回", stub.calls)
	}
	after, err := a.Decisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("同意なしでレコードが増えた: %d → %d", len(before), len(after))
	}
}

// 取り込みの代替フロー: AI API 障害中は縮退し、原本・抽出テキストを保ったまま分析だけ再実行できる。
func TestUC13AlternateProviderFailureThenRetry(t *testing.T) {
	a, stub := openForImports(t)
	view, err := a.ImportClipboardText("在庫は受注確定時に引き当てる。", string(importer.KindMaterial))
	if err != nil {
		t.Fatal(err)
	}

	stub.streamErr = &aiprovider.ProviderError{Class: aiprovider.ErrClassConfig, Message: "キーが無効です"}
	failed, err := a.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatalf("障害時にエラーで止まった（縮退して案内するはず）: %v", err)
	}
	if !failed.Fallback || failed.Notice == "" {
		t.Fatalf("障害時の縮退・案内が無い: %+v", failed)
	}
	// 原本・抽出テキストは保持されたまま。
	if _, err := a.ImportContent(view.ID); err != nil {
		t.Errorf("障害後に内容を参照できない: %v", err)
	}

	// 復旧後、分析だけをやり直せる。
	stub.streamErr = nil
	stub.scripts = []string{uc13Response}
	retried, err := a.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatalf("再実行に失敗: %v", err)
	}
	if retried.Fallback || len(retried.Extraction.Decisions) != 1 {
		t.Fatalf("再実行で候補が得られない: %+v", retried)
	}
}

// satisfiedTotal は全章観点の充足済み必須項目の合計。
func satisfiedTotal(v DialogueCompletenessView) int {
	n := 0
	for _, c := range v.Chapters {
		n += c.Satisfied
	}
	return n
}

// 議事録の取り込みの分析応答（議事録から既存未決事項の決着案を出す）。
const uc14Response = `{
  "decisions": [{"topic_key": "background/current-state", "body": "現状は Excel 台帳で管理している。",
    "rationale": "議事録の記述", "evidence_refs": ["IMP-001#L1-L1"]}],
  "open_issues": [], "requirement_updates": [], "term_candidates": [], "contradictions": [],
  "perspective_candidates": []
}`

// 会議議事録の随時取り込み。
//
// 対話の途中で議事録を取り込み、決着案を承認すると、以後その論点は質問されない。
func TestUC14MinutesDuringDialogue(t *testing.T) {
	a, stub := openForImports(t)
	stub.scripts = []string{dialogueQuestion}

	// 1. 対話を開始し、質問を 1 件受け取る（論点 = background/current-state）。
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)
	asked, err := a.DialogueUtterances(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) == 0 || !strings.Contains(asked[0].Body, "background/current-state") {
		t.Fatalf("想定の論点が質問されていない: %+v", asked)
	}

	// 2〜3. 対話の途中で議事録を取り込み、分析する（対話状態を変えない）。
	stateBefore, err := stateOf(t, a, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.ImportClipboardText("現状は Excel 台帳で管理している。", string(importer.KindMinutes))
	if err != nil {
		t.Fatal(err)
	}
	stub.scripts = []string{uc14Response}
	analysis, err := a.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatalf("議事録の分析に失敗: %v", err)
	}
	if len(analysis.Extraction.Decisions) != 1 {
		t.Fatalf("決着案が提示されない: %+v", analysis.Extraction)
	}
	stateAfter, err := stateOf(t, a, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stateAfter != stateBefore {
		t.Errorf("議事録の取り込みが対話状態を変えた: %s → %s", stateBefore, stateAfter)
	}

	// 4. 決着案を承認する。
	if _, err := a.ApproveImportCandidates(view.ID, dialogue.FeedbackApproval{
		MaterialApproval: dialogue.MaterialApproval{
			ApprovalRequest: dialogue.ApprovalRequest{
				Decisions: []dialogue.DecisionApproval{{Candidate: analysis.Extraction.Decisions[0]}},
			},
		},
	}); err != nil {
		t.Fatalf("決着案の承認に失敗: %v", err)
	}

	// 決着した論点は以後の質問生成の対象にならない。
	stub.scripts = []string{dialogueQuestion}
	next, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(next.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, next.ID, dialogue.StateAwaitingAnswer)
	if len(stub.requests) == 0 {
		t.Fatal("質問生成の送信が無い")
	}
	instruction := stub.requests[len(stub.requests)-1].Messages[0].Content
	if strings.Contains(instruction, "論点キー: background/current-state（") {
		t.Errorf("決着済みの論点が再び質問された:\n%s", instruction)
	}
}

// stateOf は対話状態を返す（取り込みが対話へ影響しないことの確認用）。
func stateOf(t *testing.T, a *API, sessionID string) (string, error) {
	t.Helper()
	s, err := a.current()
	if err != nil {
		return "", err
	}
	state, err := s.engine.LoadState(sessionID)
	if err != nil {
		return "", err
	}
	return state.DialogueState, nil
}

// 進捗レポートの生成。
//
// AI プロバイダが一切応答しない状態（オフライン相当）でも、レコードから 7 章を組み立て、
// Markdown ファイルへ出力できる。プロジェクトデータは変化しない（レポートは読み取りだけで作る）。
func TestUC15ProgressReportOffline(t *testing.T) {
	a, stub := openForImports(t)

	// レポートに載る動きを作る（決定の承認 = 章2、フィードバック = 章7）。
	stub.scripts = []string{uc13Response}
	material, err := a.ImportClipboardText("用語の正本は用語集とする。", string(importer.KindMaterial))
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := a.AnalyzeImport(material.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApproveImportCandidates(material.ID, dialogue.FeedbackApproval{
		MaterialApproval: dialogue.MaterialApproval{
			ApprovalRequest: dialogue.ApprovalRequest{
				Decisions:  []dialogue.DecisionApproval{{Candidate: analysis.Extraction.Decisions[0]}},
				OpenIssues: analysis.Extraction.OpenIssues,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	feedback, err := a.ImportClipboardText("この要件の優先度を確認したい。", string(importer.KindDevAIFeedback))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetFeedbackClassification(feedback.ID, string(importer.ClassClarification)); err != nil {
		t.Fatal(err)
	}

	// オフライン相当（送信すれば必ず失敗する状態）にする。
	stub.streamErr = &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient, Message: "接続できません"}
	callsBefore := stub.calls
	before := hashProjectTree(t, projectRootOf(t, a))

	period := ProgressReportRequest{From: today(), To: today()}
	report, err := a.ProgressReport(period)
	if err != nil {
		t.Fatalf("オフラインで生成できない: %v", err)
	}
	for _, want := range []string{
		"# 進捗レポート:", "## 1. 要約", "## 2. 決定事項", "## 3. 未決事項の動き",
		"## 4. 完成度", "## 5. 回答待ち質問票", "## 6. 確定をブロックしている要因",
		"## 7. " + docgenFeedbackChapterTitle,
	} {
		if !strings.Contains(report.Markdown, want) {
			t.Errorf("章が組み立っていない: %q", want)
		}
	}
	if stub.calls != callsBefore {
		t.Errorf("レポート生成で AI プロバイダを呼んだ: %d 回", stub.calls-callsBefore)
	}

	// Markdown ファイルへ出力できる（コピーは同一本文を返す = 出力経路で内容が変わらない）。
	dst := filepath.Join(t.TempDir(), "progress-report.md")
	if _, err := a.SaveProgressReport(period, dst); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	saved, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != report.Markdown {
		t.Error("保存内容がプレビューと一致しない")
	}

	// プロジェクトデータは変化しない。
	if diff := diffTrees(before, hashProjectTree(t, projectRootOf(t, a))); len(diff) != 0 {
		t.Errorf("レポート生成でプロジェクトデータが変化した: %v", diff)
	}
}

// docgenFeedbackChapterTitle は章7 の見出し（docgen 側の定数と同一であることを固定する）。
var docgenFeedbackChapterTitle = docgen.FeedbackChapterTitle

// 送信量がモデルの上限を超える資料の扱いの検証は、対話エンジン側の結合テストが担う
// （TestPreviewImportAnalysisReportsTooLarge = internal/dialogue）。
// 上限超過はモデルのコンテキスト長に依存し、バインディング層からは選択中モデルを差し替えられない
// （差し替え手段を検証のためだけに公開しない）。経路は同一（PreviewImportAnalysis /
// AnalyzeImport → buildImportSend → PlanImportChunks）。

// 開発 AI フィードバックの取り込みの分析応答（フィードバックから未決事項と要件変更の候補を出す）。
const uc16Response = `{
  "decisions": [],
  "open_issues": [{"topic": "欠品時のバックオーダー起票を自動にするか",
    "evidence_refs": ["IMP-001#L1-L1"], "related_ids": ["FR-INV-001", "FR-XXX-999"]}],
  "requirement_updates": [{"operation": "update", "target_id": "FR-INV-001",
    "chapter": "functional-requirements", "title": "在庫引当",
    "body_after": "出荷指示時に在庫を引き当てること。",
    "evidence_refs": ["IMP-001#L1-L1"], "related_ids": ["FR-INV-001"]}],
  "term_candidates": [], "contradictions": [], "perspective_candidates": []
}`

// ucFilledTemplate はフィードバックの定型テンプレへ記入した内容。
// テンプレ本体は生成側（docgen.FeedbackTemplate）と同じ版であることを本テストで確かめる。
func ucFilledTemplate() string {
	return docgen.FeedbackTemplate() + `
### フィードバック 9

- 種別: 修正依頼
- 関連 ID: FR-INV-001
- 内容: 引当の起点を出荷指示に変えてください。
- 実装への影響: 実装を保留しています。
`
}

// 開発AIフィードバックの取り込み（論点化・差し戻し・分類の付与と集計）。
//
// 定型テンプレ由来と自由形式の双方を取り込み、紐づけ候補・差し戻し接続・分類付与と集計を確認する。
func TestUC16FeedbackTemplateAndFreeForm(t *testing.T) {
	a, stub := openForImports(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	// 差し戻しの対象になる合意済み要件を用意する。
	if _, err := s.store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementAgreed,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}

	// 1. 定型テンプレ由来の取り込み（from_template が立つ）。
	path := filepath.Join(t.TempDir(), "feedback-template.md")
	if err := os.WriteFile(path, []byte(ucFilledTemplate()), 0o644); err != nil {
		t.Fatal(err)
	}
	fromTemplate, err := a.ImportFile(path, string(importer.KindDevAIFeedback))
	if err != nil {
		t.Fatalf("定型テンプレの取り込みに失敗: %v", err)
	}
	if !fromTemplate.FromTemplate {
		t.Fatalf("定型テンプレ由来と判定されない: %+v", fromTemplate)
	}

	// 2. 自由形式の取り込み（定型扱いにしない）。
	freeForm, err := a.ImportClipboardText("在庫引当の対象に予約在庫を含めますか。",
		string(importer.KindDevAIFeedback))
	if err != nil {
		t.Fatal(err)
	}
	if freeForm.FromTemplate {
		t.Fatalf("自由形式が定型扱いになっている: %+v", freeForm)
	}

	// 3. 論点化（紐づけ候補は実在 ID に限る）。
	stub.scripts = []string{uc16Response}
	analysis, err := a.AnalyzeImport(fromTemplate.ID, true)
	if err != nil {
		t.Fatalf("フィードバックの論点化に失敗: %v", err)
	}
	if !analysis.FromTemplate || len(analysis.Entries) == 0 {
		t.Fatalf("記入項目単位の分解ができていない: %+v", analysis)
	}
	issue := analysis.Extraction.OpenIssues[0]
	if len(issue.RelatedIDs) != 1 || issue.RelatedIDs[0] != "FR-INV-001" {
		t.Errorf("実在しない紐づけ候補が残っている: %+v", issue.RelatedIDs)
	}
	if len(analysis.RevertTargets) != 1 || analysis.RevertTargets[0] != "FR-INV-001" {
		t.Fatalf("差し戻し対象が示されない: %+v", analysis.RevertTargets)
	}

	// 4. 影響を確認せずに要件変更を承認することはできない。
	update := dialogue.FeedbackApproval{
		MaterialApproval: dialogue.MaterialApproval{
			ApprovalRequest: dialogue.ApprovalRequest{
				RequirementUpdates: []dialogue.RequirementApproval{
					{Candidate: analysis.Extraction.RequirementUpdates[0]}},
			},
		},
	}
	if _, err := a.ApproveImportCandidates(fromTemplate.ID, update); err == nil {
		t.Fatal("影響一覧の確認なしで差し戻しが通った")
	}
	update.RevertConfirmed = []string{"FR-INV-001"}
	applied, err := a.ApproveImportCandidates(fromTemplate.ID, update)
	if err != nil {
		t.Fatalf("差し戻しつきの承認に失敗: %v", err)
	}
	if len(applied.Applied.RevertedRequirementIDs) != 1 {
		t.Fatalf("差し戻しが行われていない: %+v", applied)
	}
	reqs, err := a.Requirements()
	if err != nil {
		t.Fatal(err)
	}
	var reverted *RequirementView
	for i := range reqs {
		if reqs[i].ID == "FR-INV-001" {
			reverted = &reqs[i]
		}
	}
	if reverted == nil || reverted.Status != projectstore.RequirementDraft {
		t.Fatalf("合意済み要件が draft へ戻っていない: %+v", reverted)
	}
	if !strings.Contains(reverted.RevertedReason, fromTemplate.ID) {
		t.Errorf("差し戻し理由に取り込み元の参照が無い: %q", reverted.RevertedReason)
	}

	// 5. 分類の付与と集計（AI は分類しない）。
	if _, err := a.SetFeedbackClassification(fromTemplate.ID, string(importer.ClassRequirementsGap)); err != nil {
		t.Fatalf("分類の付与に失敗: %v", err)
	}
	if _, err := a.SetFeedbackClassification(freeForm.ID, string(importer.ClassClarification)); err != nil {
		t.Fatal(err)
	}
	summary, err := a.FeedbackSummary(today(), today())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 2 || summary.Unclassified != 0 {
		t.Fatalf("集計が違う: %+v", summary)
	}
	counts := map[string]int{}
	for _, c := range summary.Counts {
		counts[string(c.Classification)] = c.Count
	}
	if counts[string(importer.ClassRequirementsGap)] != 1 || counts[string(importer.ClassClarification)] != 1 {
		t.Errorf("分類別の件数が違う: %+v", summary.Counts)
	}

	// 一覧にも分類と定型テンプレ由来が反映される。
	list, err := a.Imports()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.ID == fromTemplate.ID {
			if !v.FromTemplate || v.ClassificationLabel == "" {
				t.Errorf("一覧に定型由来・分類が出ていない: %+v", v)
			}
		}
	}
}

// 選択したプリセット観点と登録済みプロジェクト観点が
// 併存して質問生成の対象論点に加わり、システムプロンプトへ注入される。
func TestUCPerspectivesFromPresetAndProject(t *testing.T) {
	a, stub := openForImports(t)
	root := projectRootOf(t, a)

	// 業務領域（プリセット観点）を選ぶ。
	if err := a.SetDomainPresets(root, []string{"inventory"}); err != nil {
		t.Fatalf("業務領域を選べない: %v", err)
	}
	// プロジェクト観点を手動で登録する（AI 呼び出しを伴わない）。
	callsBefore := stub.calls
	added, err := a.AddPerspective(PerspectiveRequest{
		Name: "棚卸の差異処理", Summary: "差異の承認経路と記録方法を確認する"})
	if err != nil {
		t.Fatalf("観点を登録できない: %v", err)
	}
	if stub.calls != callsBefore {
		t.Errorf("観点の手動登録で AI プロバイダを呼んだ: %d 回", stub.calls-callsBefore)
	}

	// 設定変更後に開き直すと、以後の質問生成へ反映される。
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	stub.scripts = []string{dialogueQuestion}
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	if len(stub.requests) == 0 {
		t.Fatal("質問生成の送信が無い")
	}
	system := stub.requests[len(stub.requests)-1].System
	if !strings.Contains(system, "preset/inventory/") {
		t.Errorf("プリセット観点が注入されていない:\n%s", system)
	}
	if !strings.Contains(system, added.TopicKey) {
		t.Errorf("プロジェクト観点が注入されていない（%s）:\n%s", added.TopicKey, system)
	}
	// 注入順はプリセット → プロジェクト（プロジェクト観点はプリセット観点に続けて注入する）。
	if strings.Index(system, "preset/inventory/") > strings.Index(system, added.TopicKey) {
		t.Errorf("注入順がプリセット → プロジェクトになっていない")
	}

	// 論理削除した観点は以後の注入対象から外れる（ID を再利用しないよう実体は残し、利用側で除く）。
	if err := a.RemovePerspective(added.ID); err != nil {
		t.Fatal(err)
	}
	stub.scripts = []string{dialogueQuestion}
	next, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(next.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, next.ID, dialogue.StateAwaitingAnswer)
	latest := stub.requests[len(stub.requests)-1].System
	if strings.Contains(latest, added.TopicKey) {
		t.Errorf("削除した観点が注入された:\n%s", latest)
	}
}
