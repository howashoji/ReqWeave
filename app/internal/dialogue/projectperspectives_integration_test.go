//go:build integration

// 結合テスト（プロジェクト観点 × 実ファイル）。

package dialogue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 登録済み観点が対象論点に加わり、プリセット観点に続けてシステムプロンプトへ注入される。
func TestGenerateQuestionUsesProjectPerspectives(t *testing.T) {
	stub := &stubAdapter{scripts: []string{
		"論点キー: custom/PRS-001\n質問: 棚卸差異は誰が承認しますか。\n背景: 承認経路を決めるために確認します。"}}
	e, store := newPresetEngine(t, stub, []string{"inventory"})

	added, err := store.AddPerspective("棚卸の差異処理", "差異の承認経路と記録方法を確認する",
		projectstore.PerspectiveOriginManual, "")
	if err != nil {
		t.Fatalf("観点を登録できない: %v", err)
	}
	removed, err := store.AddPerspective("削除する観点", "削除後は注入されない",
		projectstore.PerspectiveOriginManual, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemovePerspective(removed.ID); err != nil {
		t.Fatalf("観点を削除できない: %v", err)
	}

	// 章観点とプリセット観点を既決にして、プロジェクト観点が選ばれる状態を作る。
	seedDecisions(t, store, lastRequirementChapter(t))
	preset, err := SelectedPerspectives([]string{"inventory"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range preset {
		if _, err := store.CreateDecision(projectstore.Decision{
			TopicKey: v.TopicKey, Body: "決めた。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
			t.Fatal(err)
		}
	}

	sess, err := e.StartSession(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("質問生成に失敗: %v", err)
	}
	if ch == nil {
		t.Fatal("次の論点が無い（プロジェクト観点が対象論点に連結されていない）")
	}
	events := collectEvents(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventDone {
		t.Fatalf("完了しない: %+v", events)
	}
	if last.TopicKey != added.TopicKey() {
		t.Fatalf("プロジェクト観点が対象論点になっていない: %q", last.TopicKey)
	}

	if len(stub.reqs) == 0 {
		t.Fatal("送信されていない")
	}
	system := stub.reqs[len(stub.reqs)-1].System
	if !strings.Contains(system, "custom/PRS-001") || !strings.Contains(system, "棚卸の差異処理") {
		t.Errorf("プロジェクト観点が注入されていない:\n%s", system)
	}
	if strings.Contains(system, "削除する観点") || strings.Contains(system, removed.TopicKey()) {
		t.Errorf("論理削除した観点が注入された:\n%s", system)
	}
	// 注入順はプリセット観点 → プロジェクト観点。
	presetAt := strings.Index(system, "preset/inventory/")
	projectAt := strings.Index(system, "custom/PRS-001")
	if presetAt < 0 || projectAt < 0 || presetAt > projectAt {
		t.Errorf("注入順がプリセット → プロジェクトになっていない（preset=%d custom=%d）", presetAt, projectAt)
	}
	content := stub.reqs[len(stub.reqs)-1].Messages[0].Content
	if !strings.Contains(content, "論点キー: custom/PRS-001") {
		t.Errorf("プロジェクト観点が対象論点として指示されていない:\n%s", content)
	}
}

// 既存の質問観点（登録済み・選択中プリセット）を分析へ注入する。
// 論理削除された観点は注入しない。
func TestAnalyzeMaterialInjectsExistingPerspectives(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newPresetEngine(t, stub, []string{"inventory"})
	if _, err := store.AddPerspective("棚卸の差異処理", "差異の承認経路と記録方法を確認する",
		projectstore.PerspectiveOriginManual, ""); err != nil {
		t.Fatal(err)
	}
	removed, err := store.AddPerspective("削除する観点", "削除後は注入されない",
		projectstore.PerspectiveOriginManual, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemovePerspective(removed.ID); err != nil {
		t.Fatal(err)
	}
	meta := newMaterial(t, store, importer.KindMaterial)

	if _, err := e.AnalyzeMaterial(context.Background(), meta.ID, true); err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	if len(stub.reqs) != 1 {
		t.Fatalf("送信回数が違う: %d", len(stub.reqs))
	}
	content := stub.reqs[0].Messages[0].Content
	if !strings.Contains(content, "既存の質問観点") {
		t.Errorf("既存観点の一覧が注入されていない:\n%s", content)
	}
	if !strings.Contains(content, "custom/PRS-001") || !strings.Contains(content, "preset/inventory/") {
		t.Errorf("登録済み観点・プリセット観点が並んでいない:\n%s", content)
	}
	if strings.Contains(content, "削除する観点") {
		t.Errorf("論理削除した観点が注入された:\n%s", content)
	}
}

// 観点候補は承認操作を経てのみ登録される。
func TestApplyMaterialApprovalRegistersApprovedPerspectives(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	logger, err := auditlog.New(store, store.Author().AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Logger = logger
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	if len(analysis.Extraction.PerspectiveCandidates) != 1 {
		t.Fatalf("観点候補が取れていない: %+v", analysis.Extraction.PerspectiveCandidates)
	}

	// 承認前は 1 件も登録されていない（承認なしの自動追加をしない）。
	before, err := store.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("承認前に観点が登録された: %+v", before)
	}

	applied, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{
		Perspectives: analysis.Extraction.PerspectiveCandidates,
	})
	if err != nil {
		t.Fatalf("承認の反映に失敗: %v", err)
	}
	if len(applied.PerspectiveIDs) != 1 || applied.PerspectiveIDs[0] != "PRS-001" {
		t.Fatalf("登録結果が違う: %+v", applied.PerspectiveIDs)
	}

	after, err := store.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("登録件数が違う: %+v", after)
	}
	got := after[0]
	if got.Name != "棚卸の差異処理" || got.Summary != "差異の承認経路と記録方法" {
		t.Errorf("登録内容が違う: %+v", got)
	}
	if got.Origin != projectstore.PerspectiveOriginImport || got.Evidence != "IMP-001#L7-L7" {
		t.Errorf("由来・根拠が保存されていない: %+v", got)
	}
	if got.Author != store.Author().AuthorID || got.CreatedAt.IsZero() {
		t.Errorf("登録者・登録日時が入っていない: %+v", got)
	}

	// 変更履歴へ target: PRS-nnn / change: created を記録する。
	changes, err := auditlog.ReadChanges(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var found *auditlog.ChangeRecord
	for i := range changes {
		if changes[i].Target == got.ID && changes[i].Change == auditlog.ChangeCreated {
			found = &changes[i]
		}
	}
	if found == nil {
		t.Fatalf("観点の登録が変更履歴にない: %+v", changes)
	}
	if found.After != got.Name || found.Evidence != got.Evidence {
		t.Errorf("記録内容が違う: %+v", *found)
	}
}

// 破棄した観点候補は登録されない（渡された候補だけを反映する構造で担保する）。
func TestApplyMaterialApprovalWithDiscardedPerspectives(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)
	if _, err := e.AnalyzeMaterial(context.Background(), meta.ID, true); err != nil {
		t.Fatal(err)
	}

	applied, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{})
	if err != nil {
		t.Fatalf("全破棄の反映に失敗: %v", err)
	}
	if len(applied.PerspectiveIDs) != 0 {
		t.Errorf("破棄した候補が登録された: %+v", applied.PerspectiveIDs)
	}
	registered, err := store.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 0 {
		t.Fatalf("破棄した候補が保存された: %+v", registered)
	}
}

// origin: import の登録は根拠（IMP-nnn#Lm-Ln）が無ければ拒否する。
func TestApplyMaterialApprovalRejectsPerspectiveWithoutEvidence(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)
	if _, err := e.AnalyzeMaterial(context.Background(), meta.ID, true); err != nil {
		t.Fatal(err)
	}

	_, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{
		Perspectives: []PerspectiveCandidate{{Name: "根拠なしの観点", Summary: "要旨"}},
	})
	if err == nil {
		t.Fatal("根拠なしの観点候補が登録された")
	}
	if !strings.Contains(err.Error(), "根拠なしの観点") || !strings.Contains(err.Error(), "該当箇所") {
		t.Errorf("原因と次の行動が示されていない: %v", err)
	}
	registered, err := store.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 0 {
		t.Fatalf("拒否した候補が保存された: %+v", registered)
	}
}

// 手動の追加・編集・削除は AI プロバイダを呼ばない
// （AI API 障害中でも成功する）。
func TestManualPerspectiveOperationsWithoutAI(t *testing.T) {
	stub := &stubAdapter{scripts: []string{"呼ばれてはいけない"}}
	e, store := newTestEngine(t, stub)

	added, err := store.AddPerspective("棚卸の差異処理", "差異の承認経路を確認する",
		projectstore.PerspectiveOriginManual, "")
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if _, _, err := store.UpdatePerspective(added.ID, "棚卸の差異処理", "差異の承認経路と記録方法を確認する"); err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	if _, err := store.RemovePerspective(added.ID); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("手動操作で AI プロバイダが呼ばれた: %d 回", stub.calls)
	}

	// 削除後は質問生成の論点連結・注入の対象から外れる（engine 経由の読み出し）。
	perspectives, err := e.perspectives()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range perspectives {
		if v.TopicKey == added.TopicKey() {
			t.Errorf("論理削除した観点が論点として残った: %+v", v)
		}
	}
}

// 観点候補の重複指摘は、登録済みプロジェクト観点・選択中プリセット観点を指すものだけ残す。
func TestAnalyzeMaterialKeepsPerspectiveDuplicateOf(t *testing.T) {
	const response = `{
	  "decisions": [], "open_issues": [], "requirement_updates": [], "term_candidates": [],
	  "contradictions": [],
	  "perspective_candidates": [
	    {"name": "棚卸の差異処理", "summary": "差異の承認経路", "evidence_refs": ["IMP-001#L7-L7"],
	      "duplicate_of": "custom/PRS-001"},
	    {"name": "棚卸と差異処理", "summary": "差異の記録方法", "evidence_refs": ["IMP-001#L7-L7"],
	      "duplicate_of": "preset/inventory/stocktaking"},
	    {"name": "実在しない観点との重複", "summary": "要旨", "evidence_refs": ["IMP-001#L7-L7"],
	      "duplicate_of": "PRS-999"}
	  ]
	}`
	stub := &stubAdapter{scripts: []string{response}}
	e, store := newPresetEngine(t, stub, []string{"inventory"})
	if _, err := store.AddPerspective("棚卸の差異処理", "差異の承認経路と記録方法を確認する",
		projectstore.PerspectiveOriginManual, ""); err != nil {
		t.Fatal(err)
	}
	meta := newMaterial(t, store, importer.KindMaterial)

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	cands := got.Extraction.PerspectiveCandidates
	if len(cands) != 3 {
		t.Fatalf("候補が落ちた: %+v", cands)
	}
	if cands[0].DuplicateOf != "custom/PRS-001" {
		t.Errorf("登録済み観点との重複指摘が外された: %+v", cands[0])
	}
	if cands[1].DuplicateOf != "preset/inventory/stocktaking" {
		t.Errorf("プリセット観点との重複指摘が外された: %+v", cands[1])
	}
	if cands[2].DuplicateOf != "" {
		t.Errorf("実在しない観点を指す重複指摘が残った: %+v", cands[2])
	}
	if !strings.Contains(got.Notice, "重複指摘 1 件は外しました") {
		t.Errorf("外した件数が案内に出ていない: %q", got.Notice)
	}
}
