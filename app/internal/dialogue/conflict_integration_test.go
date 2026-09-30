//go:build integration

// 結合テスト（反映時の競合検知とマージ）。
// 候補生成 → 他メンバーの変更 → 承認 → 競合の提示 → マージ承認 → 反映、までを対話の経路で通す。

package dialogue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 既存の要件項目を書き換える候補（取り込み分析の応答）。
const conflictResponse = `{
  "decisions": [], "open_issues": [],
  "requirement_updates": [{"operation": "update", "target_id": "FR-INV-001",
    "chapter": "functional-requirements", "title": "在庫引当",
    "body_after": "自分の反映案（出荷指示時に引き当てる）。",
    "evidence_refs": ["IMP-001#L1-L3"]}],
  "term_candidates": [], "contradictions": [], "perspective_candidates": []
}`

// newConflictEngine は合意済みの要件項目を持つプロジェクトと対話エンジンを返す。
func newConflictEngine(t *testing.T) (*Engine, *projectstore.Store, *stubAdapter) {
	t.Helper()
	stub := &stubAdapter{scripts: []string{conflictResponse}}
	e, store := newTestEngine(t, stub)
	if _, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementDraft,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	return e, store, stub
}

// 候補生成後に他メンバーが同じレコードを変えると、承認時に競合として中断する。
func TestApprovalDetectsConflictWithOtherMember(t *testing.T) {
	e, store, _ := newConflictEngine(t)
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("分析に失敗: %v", err)
	}
	if len(analysis.Extraction.RequirementUpdates) != 1 {
		t.Fatalf("候補が取れていない: %+v", analysis.Extraction)
	}

	// 候補生成の後に、他のメンバーが同じ要件項目を変更する。
	otherBase, err := store.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRequirementGuarded(otherBase, "FR-INV-001",
		func(r *projectstore.Requirement) error {
			r.Body = "他のメンバーの変更（受注確定時のまま、条件を追記）。"
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	req := MaterialApproval{ApprovalRequest: ApprovalRequest{
		RequirementUpdates: []RequirementApproval{{Candidate: analysis.Extraction.RequirementUpdates[0]}},
	}}
	_, err = e.ApplyMaterialApproval(meta.ID, req)
	var conflict *projectstore.ErrRecordConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("競合として中断されない: %v", err)
	}
	if len(conflict.Conflicts) != 1 || conflict.Conflicts[0].ID != "FR-INV-001" {
		t.Fatalf("競合の内容が違う: %+v", conflict.Conflicts)
	}
	c := conflict.Conflicts[0]
	if !strings.Contains(c.BaselineBody, "受注確定時に在庫を引き当てること") ||
		!strings.Contains(c.CurrentBody, "他のメンバーの変更") {
		t.Errorf("三面の材料（基準版・現在の内容）が揃っていない: %+v", c)
	}

	// 承認しない限り書き換わらない（後勝ち上書きをしない）。
	after, err := store.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.Body, "自分の反映案") {
		t.Fatalf("競合したのに反映された: %+v", after)
	}
	// 候補は未反映のまま保全される（後から未決事項化・再承認できる）。
	pending, err := e.PendingMaterialAnalysis(meta.ID)
	if err != nil || pending == nil || len(pending.RequirementUpdates) != 1 {
		t.Fatalf("未反映の候補が保全されていない: %v %+v", err, pending)
	}

	// マージを承認する（三面を確認したうえで自分の案を通す）。
	req.Merges = []MergeResolution{{ID: c.ID, BaselineHash: c.CurrentHash, Reference: "IMP-001"}}
	applied, err := e.ApplyMaterialApproval(meta.ID, req)
	if err != nil {
		t.Fatalf("マージ承認後の反映に失敗: %v", err)
	}
	if len(applied.RequirementIDs) != 1 {
		t.Fatalf("反映結果が違う: %+v", applied.ApprovalResult)
	}
	merged, err := store.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged.Body, "自分の反映案") {
		t.Errorf("マージが反映されていない: %+v", merged)
	}
}

// マージの実行が変更履歴へ merge-applied として記録される。
func TestMergeAppliedIsRecorded(t *testing.T) {
	e, store, _ := newConflictEngine(t)
	logger, err := auditlog.New(store, store.Author().AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Logger = logger
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{
		ApprovalRequest: ApprovalRequest{
			RequirementUpdates: []RequirementApproval{{Candidate: analysis.Extraction.RequirementUpdates[0]}},
			Merges: []MergeResolution{{ID: "FR-INV-001", BaselineHash: base.Hash,
				Reference: "他メンバーの変更（IMP-001 の反映）"}},
		},
	}); err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}

	changes, err := auditlog.ReadChanges(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var found *auditlog.ChangeRecord
	for i := range changes {
		if changes[i].Change == auditlog.ChangeMergeApplied {
			found = &changes[i]
		}
	}
	if found == nil {
		t.Fatalf("merge-applied が記録されていない: %+v", changes)
	}
	if found.Target != "FR-INV-001" || !strings.Contains(found.Evidence, "IMP-001") {
		t.Errorf("マージの記録内容が違う: %+v", *found)
	}
}

// 競合しない反映（新規作成・一致）は追加の操作なしで通る。
func TestApprovalWithoutConflictNeedsNoExtraStep(t *testing.T) {
	e, store, _ := newConflictEngine(t)
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{
		ApprovalRequest: ApprovalRequest{
			RequirementUpdates: []RequirementApproval{{Candidate: analysis.Extraction.RequirementUpdates[0]}},
		},
	})
	if err != nil {
		t.Fatalf("競合していないのに反映できない: %v", err)
	}
	if len(applied.RequirementIDs) != 1 {
		t.Fatalf("反映結果が違う: %+v", applied.ApprovalResult)
	}
	updated, err := store.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.Body, "自分の反映案") {
		t.Errorf("反映されていない: %+v", updated)
	}
}
