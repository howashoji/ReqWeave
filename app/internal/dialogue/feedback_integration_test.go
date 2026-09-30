//go:build integration

// 結合テスト（フィードバック論点化 × 実ファイル）。
// テンプレ生成側（docgen）との形式一致もここで固定する。

package dialogue

import (
	"context"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// feedbackResponse は分析応答（紐づけ候補つき）。根拠は記入ブロックの行を指す。
const feedbackResponse = `{
  "decisions": [],
  "open_issues": [{"topic": "欠品時のバックオーダー起票を自動にするか",
    "evidence_refs": ["IMP-001#L16-L21"], "related_ids": ["FR-INV-001", "FR-XXX-999"]}],
  "requirement_updates": [{"operation": "update", "target_id": "FR-INV-001",
    "chapter": "functional-requirements", "title": "在庫引当",
    "body_after": "出荷指示時に在庫を引き当てること。",
    "evidence_refs": ["IMP-001#L23-L28"], "related_ids": ["FR-INV-001"]}],
  "term_candidates": [], "contradictions": [], "perspective_candidates": []
}`

func newFeedback(t *testing.T, e *Engine, body string) *importer.Meta {
	t.Helper()
	meta, err := e.ImportFeedback(importer.Input{
		SourceName: "feedback-template.md", SourceFormat: importer.FormatMD,
		Content: []byte(body),
	})
	if err != nil {
		t.Fatalf("フィードバックを取り込めない: %v", err)
	}
	return meta
}

// 定型テンプレ由来は from_template が true になり、記入項目単位に分解される。
// 原本は無変更で保持される（資料の取り込みと同じ扱い）。
func TestImportAndAnalyzeFeedbackFromTemplate(t *testing.T) {
	stub := &stubAdapter{scripts: []string{feedbackResponse}}
	e, store := newTestEngine(t, stub)
	if _, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementAgreed,
		Body: "受注確定時に在庫を引き当てること。"}); err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}
	meta := newFeedback(t, e, filledTemplate)

	if meta.Kind != importer.KindDevAIFeedback {
		t.Errorf("種別が開発AIフィードバックでない: %q", meta.Kind)
	}
	if meta.FromTemplate == nil || !*meta.FromTemplate {
		t.Fatalf("from_template が true でない: %+v", meta.FromTemplate)
	}

	got, err := e.AnalyzeFeedback(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("フィードバックの論点化に失敗: %v", err)
	}
	if !got.FromTemplate || len(got.Entries) != 2 {
		t.Fatalf("記入項目単位の分解ができていない: %+v", got.Entries)
	}
	if got.Entries[0].InitialCandidate() != projectstore.RecordKindOpenIssue ||
		got.Entries[1].InitialCandidate() != projectstore.RecordKindRequirement {
		t.Errorf("種別に対応する初期区分が違う: %+v", got.Entries)
	}

	// 分解した記入内容が分析の入力文脈に載る。
	content := stub.reqs[0].Messages[0].Content
	for _, want := range []string{"定型テンプレの記入内容", "フィードバック 2", "種別: 質問", "FR-INV-001"} {
		if !strings.Contains(content, want) {
			t.Errorf("注入文脈に %q が無い", want)
		}
	}

	// 紐づけ候補は実在する ID に限られる。
	issue := got.Extraction.OpenIssues[0]
	if len(issue.RelatedIDs) != 1 || issue.RelatedIDs[0] != "FR-INV-001" {
		t.Errorf("紐づけ候補が実在 ID に絞られていない: %+v", issue.RelatedIDs)
	}
	if !strings.Contains(got.Notice, "紐づけ候補 1 件") {
		t.Errorf("実在しない紐づけを外した案内が無い: %q", got.Notice)
	}
	// 差し戻し対象が提示される（承認前に影響一覧を確認する対象）。
	if len(got.RevertTargets) != 1 || got.RevertTargets[0] != "FR-INV-001" {
		t.Errorf("差し戻し対象が提示されていない: %+v", got.RevertTargets)
	}

	// 原本は無変更で保持される。
	raw, err := importer.New(store).ReadSource(meta.ID)
	if err != nil || string(raw) != filledTemplate {
		t.Errorf("原本が保持されていない: %v", err)
	}
}

// operation: update の承認は差し戻し手順へ接続し、影響一覧の確認を経ないと通らない。
func TestApplyFeedbackApprovalConnectsToRevert(t *testing.T) {
	stub := &stubAdapter{scripts: []string{feedbackResponse}}
	e, store := newTestEngine(t, stub)
	req, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementAgreed,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}
	meta := newFeedback(t, e, filledTemplate)
	got, err := e.AnalyzeFeedback(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("フィードバックの論点化に失敗: %v", err)
	}
	candidate := got.Extraction.RequirementUpdates[0]

	// 影響一覧を確認していない差し戻しは通らない。
	if _, err := e.ApplyFeedbackApproval(meta.ID, FeedbackApproval{
		ApprovalRequest: ApprovalRequest{
			RequirementUpdates: []RequirementApproval{{Candidate: candidate}}},
	}); err == nil {
		t.Fatal("影響一覧の確認なしで差し戻しが通った")
	}
	// 未確認で弾かれた時点では要件項目は合意済みのまま。
	before, err := store.LoadRequirement(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Status != projectstore.RequirementAgreed {
		t.Fatalf("確認前に状態が変わっている: %q", before.Status)
	}

	applied, err := e.ApplyFeedbackApproval(meta.ID, FeedbackApproval{
		ApprovalRequest: ApprovalRequest{
			RequirementUpdates: []RequirementApproval{{Candidate: candidate}}},
		RevertConfirmed: []string{req.ID},
	})
	if err != nil {
		t.Fatalf("差し戻しつきの承認に失敗: %v", err)
	}
	if len(applied.RevertedRequirementIDs) != 1 || applied.RevertedRequirementIDs[0] != req.ID {
		t.Fatalf("差し戻し結果が返っていない: %+v", applied.RevertedRequirementIDs)
	}

	after, err := store.LoadRequirement(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != projectstore.RequirementDraft {
		t.Errorf("差し戻されていない: %q", after.Status)
	}
	if !strings.Contains(after.RevertedReason, "IMP-001#L23-L28") ||
		!strings.Contains(after.RevertedReason, "フィードバック") {
		t.Errorf("差し戻し理由にフィードバック参照が無い: %q", after.RevertedReason)
	}
	if after.Body != "出荷指示時に在庫を引き当てること。" {
		t.Errorf("承認内容が反映されていない: %q", after.Body)
	}
	// 一時状態は削除される（資料の取り込み分析と共通）。
	saved, err := importer.New(store).LoadAnalysis(meta.ID)
	if err != nil || saved != nil {
		t.Errorf("承認後も一時状態が残っている: %+v %v", saved, err)
	}
}

// 形式が壊れたテンプレは自由形式として分析を続ける
// （取り込み自体は失敗しない）。
func TestAnalyzeFeedbackFallsBackToFreeForm(t *testing.T) {
	const freeForm = "# フィードバック\n\n引当のタイミングを確認したいです。\n"
	stub := &stubAdapter{scripts: []string{`{"decisions":[],"open_issues":[{"topic":"引当のタイミング",
		"evidence_refs":["IMP-001#L3-L3"]}],"requirement_updates":[],"term_candidates":[],
		"contradictions":[],"perspective_candidates":[]}`}}
	e, store := newTestEngine(t, stub)
	meta := newFeedback(t, e, freeForm)

	if meta.FromTemplate == nil || *meta.FromTemplate {
		t.Errorf("自由形式なのに from_template が true: %+v", meta.FromTemplate)
	}
	got, err := e.AnalyzeFeedback(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("自由形式の分析に失敗: %v", err)
	}
	if got.FromTemplate || len(got.Entries) != 0 {
		t.Errorf("自由形式が定型扱いになっている: %+v", got.Entries)
	}
	if !strings.Contains(got.Notice, "自由形式として分析") {
		t.Errorf("縮退の案内が無い: %q", got.Notice)
	}
	if len(got.Extraction.OpenIssues) != 1 {
		t.Errorf("自由形式でも候補が得られていない: %+v", got.Extraction)
	}
	// 原本は保持されている（取り込み自体は成功している）。
	if _, err := importer.New(store).ReadSource(meta.ID); err != nil {
		t.Errorf("原本が保持されていない: %v", err)
	}
}

// 資料・議事録をフィードバックとして分析しない（種別の取り違えを構造で防ぐ）。
func TestAnalyzeFeedbackRejectsOtherKinds(t *testing.T) {
	stub := &stubAdapter{scripts: []string{feedbackResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	if _, err := e.AnalyzeFeedback(context.Background(), meta.ID, true); err == nil {
		t.Fatal("資料がフィードバックとして分析された")
	}
	if stub.calls != 0 {
		t.Errorf("種別違いの資料が送信された: %d 回", stub.calls)
	}
}

// 生成側のテンプレ（docgen）が取り込み側のパースと同一形式であること。
//
// テンプレの見出し構造は両者で保守する取り決めのため、実際に生成したファイルで確かめる。
func TestGeneratedTemplateIsParsable(t *testing.T) {
	body := docgen.FeedbackTemplate()

	entries, err := ParseFeedbackTemplate(body)
	if err != nil {
		t.Fatalf("生成側のテンプレを取り込み側でパースできない（形式の版がずれている）: %v\n%s", err, body)
	}
	if len(entries) != 1 {
		t.Fatalf("記入欄の論点ブロック数が違う: %d 件 %+v", len(entries), entries)
	}
	// 記入前のテンプレは値が未記入（種別は 3 値でない = 初期区分なし）。
	if entries[0].InitialCandidate() != "" {
		t.Errorf("未記入のテンプレで初期区分が決まっている: %+v", entries[0])
	}
	// 4 つの記入項目の見出しがすべて含まれている（欠落は生成側と取り込み側の非互換）。
	for _, field := range []string{feedbackFieldKind, feedbackFieldRelated, feedbackFieldBody, feedbackFieldImpact} {
		if !strings.Contains(body, "- "+field+":") {
			t.Errorf("生成側テンプレに記入項目「%s」の見出しが無い", field)
		}
	}
}

// AI 分析は分類を設定しない（分類は担当者の操作でのみ変わる）。
func TestFeedbackAnalysisDoesNotSetClassification(t *testing.T) {
	stub := &stubAdapter{scripts: []string{feedbackResponse}}
	e, store := newTestEngine(t, stub)
	if _, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementAgreed,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}
	meta := newFeedback(t, e, filledTemplate)

	// 担当者が分類を付けた状態で分析・承認しても分類は変わらない。
	im := importer.New(store)
	if _, err := im.SetClassification(meta.ID, importer.ClassRequirementsGap); err != nil {
		t.Fatalf("分類を付与できない: %v", err)
	}
	got, err := e.AnalyzeFeedback(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("フィードバックの論点化に失敗: %v", err)
	}
	if _, err := e.ApplyFeedbackApproval(meta.ID, FeedbackApproval{
		ApprovalRequest: ApprovalRequest{RequirementUpdates: []RequirementApproval{
			{Candidate: got.Extraction.RequirementUpdates[0]}}},
		RevertConfirmed: []string{"FR-INV-001"},
	}); err != nil {
		t.Fatalf("承認の反映に失敗: %v", err)
	}

	after, err := im.Load(meta.ID)
	if err != nil {
		t.Fatalf("メタデータを読めない: %v", err)
	}
	if after.Classification != importer.ClassRequirementsGap {
		t.Errorf("AI 分析・承認で分類が変わった: %q", after.Classification)
	}
	// 出力契約にも分類を入れていない（AI に分類させる経路が無い）。
	if strings.Contains(string(SchemaForMode(ModeFeedbackAnalysis)), "classification") {
		t.Error("フィードバックのスキーマに classification がある（AI が分類する経路になる）")
	}
	if strings.Contains(stub.reqs[0].System, "分類") {
		t.Errorf("出力契約が分類を求めている:\n%s", stub.reqs[0].System)
	}
}
