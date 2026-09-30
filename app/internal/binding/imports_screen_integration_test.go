//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// materialAnalysisResponse は取り込み分析の応答（抽出結果の形式 + perspective_candidates）。
const materialAnalysisResponse = `{
  "decisions": [{"topic_key": "background/current-state", "body": "在庫は受注確定時に引き当てる。",
    "rationale": "現行業務の記述", "evidence_refs": ["IMP-001#L1-L3"]}],
  "open_issues": [], "requirement_updates": [], "term_candidates": [], "contradictions": [],
  "perspective_candidates": [{"name": "棚卸の差異処理", "summary": "差異の承認経路",
    "evidence_refs": ["IMP-001#L1-L3"]}]
}`

const materialBody = "## 在庫管理の現行業務\n\n受注が確定した時点で在庫を引き当てる。\n"

// openForImports は取り込み操作用にプロジェクトを開いた API とスタブを返す。
func openForImports(t *testing.T) (*API, *streamingStub) {
	t.Helper()
	stub := &streamingStub{scripts: []string{materialAnalysisResponse}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	return a, stub
}

// ファイル選択とクリップボード貼り付けの両経路から取り込める。
func TestImportBindingBothRoutes(t *testing.T) {
	a, _ := openForImports(t)

	path := filepath.Join(t.TempDir(), "現行業務.md")
	if err := os.WriteFile(path, []byte(materialBody), 0o644); err != nil {
		t.Fatal(err)
	}
	fromFile, err := a.ImportFile(path, string(importer.KindMaterial))
	if err != nil {
		t.Fatalf("ファイルの取り込みに失敗: %v", err)
	}
	if fromFile.ID != "IMP-001" || fromFile.SourceName != "現行業務.md" ||
		fromFile.KindLabel != "資料" || !fromFile.Extracted {
		t.Fatalf("取り込み結果が違う: %+v", fromFile)
	}

	fromClipboard, err := a.ImportClipboardText("打ち合わせの記録。棚卸は月次。",
		string(importer.KindMinutes))
	if err != nil {
		t.Fatalf("貼り付けの取り込みに失敗: %v", err)
	}
	if fromClipboard.ID != "IMP-002" || fromClipboard.KindLabel != "議事録" {
		t.Fatalf("貼り付けの結果が違う: %+v", fromClipboard)
	}

	list, err := a.Imports()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("一覧の件数が違う: %+v", list)
	}
	for _, v := range list {
		if v.ImportedAt == "" || v.StatusLabel == "" || v.KindLabel == "" {
			t.Errorf("一覧に種別・取り込み日時・抽出状態が揃っていない: %+v", v)
		}
	}

	// 対象外の形式は取り込まない（理由を示す）。
	other := filepath.Join(t.TempDir(), "図.png")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportFile(other, string(importer.KindMaterial)); err == nil {
		t.Error("対象外の形式が取り込まれた")
	} else if !strings.Contains(err.Error(), "txt") {
		t.Errorf("取り込める形式の案内が無い: %v", err)
	}
	if _, err := a.ImportClipboardText("  ", string(importer.KindMaterial)); err == nil {
		t.Error("空の貼り付けが受理された")
	}
}

// 抽出できない資料はその旨を示し、原本は参照できる（テキストを取り出せない資料も原本は保持する）。
func TestImportBindingShowsExtractionFailure(t *testing.T) {
	a, _ := openForImports(t)
	// テキストとして読めない内容の pdf（抽出に失敗する）。
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := a.ImportFile(path, string(importer.KindMaterial))
	if err != nil {
		t.Fatalf("抽出できない資料の取り込みで失敗した（原本は保持するはず）: %v", err)
	}
	if view.Extracted {
		t.Fatalf("抽出できたことになっている: %+v", view)
	}
	if !strings.Contains(view.StatusLabel, "抽出できません") {
		t.Errorf("抽出失敗が表示されない: %+v", view)
	}

	content, err := a.ImportContent(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !content.ExtractionFailed || content.Notice == "" {
		t.Errorf("抽出失敗の案内が無い: %+v", content)
	}
	if content.SourcePath == "" {
		t.Errorf("原本の所在が示されない: %+v", content)
	}
	if _, err := os.Stat(filepath.Join(projectRootOf(t, a), content.SourcePath)); err != nil {
		t.Errorf("原本が保持されていない: %v", err)
	}
}

// 送信前プレビューは実際の送信内容から生成され、同意なしでは送信しない。
func TestImportBindingPreviewAndConsent(t *testing.T) {
	a, stub := openForImports(t)

	view, err := a.ImportClipboardText(materialBody, string(importer.KindMaterial))
	if err != nil {
		t.Fatal(err)
	}

	preview, err := a.PreviewImportAnalysis(view.ID)
	if err != nil {
		t.Fatalf("プレビューに失敗: %v", err)
	}
	if preview.ChunkCount != 1 || preview.EstimatedTokens <= 0 {
		t.Fatalf("分割数・概算トークンが出ていない: %+v", preview)
	}
	if !strings.Contains(preview.Chunks[0].Text, "受注が確定した時点で在庫を引き当てる") {
		t.Errorf("送信本文がプレビューに含まれない: %+v", preview.Chunks[0])
	}
	if stub.calls != 0 {
		t.Fatalf("プレビューで送信された: %d 回", stub.calls)
	}

	// 同意なしの分析は送信されない（同意ゲート）。
	denied, err := a.AnalyzeImport(view.ID, false)
	if err == nil && denied != nil && !denied.Fallback {
		t.Fatalf("同意なしで分析が成立した: %+v", denied)
	}
	if stub.calls != 0 {
		t.Fatalf("同意なしで送信された: %d 回", stub.calls)
	}

	// 同意ありで分析する。
	analysis, err := a.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatalf("分析に失敗: %v", err)
	}
	if len(analysis.Extraction.Decisions) != 1 {
		t.Fatalf("候補が取れていない: %+v", analysis.Extraction)
	}
	if stub.calls != 1 {
		t.Errorf("送信回数が違う: %d", stub.calls)
	}
	// プレビューの内容が実際の送信と一致する。
	if stub.requests[0].System != preview.System {
		t.Error("送信したシステムプロンプトがプレビューと違う")
	}
	if !strings.Contains(stub.requests[0].Messages[0].Content, preview.Context) {
		t.Error("送信した注入文脈がプレビューと違う")
	}

	// 未承認候補は保全され、承認するまで記録されない。
	pending, err := a.PendingImportCandidates(view.ID)
	if err != nil || pending == nil || len(pending.Decisions) != 1 {
		t.Fatalf("未承認候補が保全されていない: %v %+v", err, pending)
	}
	before, err := a.Decisions()
	if err != nil {
		t.Fatal(err)
	}

	applied, err := a.ApproveImportCandidates(view.ID, dialogue.FeedbackApproval{
		MaterialApproval: dialogue.MaterialApproval{
			ApprovalRequest: dialogue.ApprovalRequest{
				Decisions: []dialogue.DecisionApproval{{Candidate: analysis.Extraction.Decisions[0]}},
			},
			Perspectives: analysis.Extraction.PerspectiveCandidates,
		},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	if len(applied.Applied.DecisionIDs) != 1 || len(applied.Applied.PerspectiveIDs) != 1 {
		t.Fatalf("反映結果が違う: %+v", applied)
	}
	after, err := a.Decisions()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Errorf("決定事項が増えていない: %d → %d", len(before), len(after))
	}
	// 承認した観点候補がプロジェクト観点として登録される。
	perspectives, err := a.Perspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(perspectives) != 1 || perspectives[0].Name != "棚卸の差異処理" {
		t.Errorf("観点が登録されていない: %+v", perspectives)
	}
}

// 閲覧権限では取り込み・分析・承認が拒否され、理由が示される。
func TestImportBindingDeniesViewer(t *testing.T) {
	a, _ := openForImports(t)
	view, err := a.ImportClipboardText(materialBody, string(importer.KindMaterial))
	if err != nil {
		t.Fatal(err)
	}
	demoteToViewer(t, projectRootOf(t, a), "k.sato@example.co.jp")

	if _, err := a.ImportClipboardText("追加の資料", string(importer.KindMaterial)); err == nil {
		t.Error("閲覧権限で取り込めてしまった")
	} else if !strings.Contains(err.Error(), "編集権限が必要です") {
		t.Errorf("理由が示されていない: %v", err)
	}
	if _, err := a.PreviewImportAnalysis(view.ID); err == nil {
		t.Error("閲覧権限でプレビューできてしまった")
	}
	if _, err := a.AnalyzeImport(view.ID, true); err == nil {
		t.Error("閲覧権限で分析できてしまった")
	}
	if _, err := a.ApproveImportCandidates(view.ID, dialogue.FeedbackApproval{}); err == nil {
		t.Error("閲覧権限で承認できてしまった")
	}

	// 参照は閲覧権限でもできる。
	if _, err := a.Imports(); err != nil {
		t.Errorf("閲覧権限で一覧を取得できない: %v", err)
	}
	if _, err := a.ImportContent(view.ID); err != nil {
		t.Errorf("閲覧権限で内容を参照できない: %v", err)
	}
}

// projectRootOf は開いているプロジェクトのフォルダを返す。
func projectRootOf(t *testing.T, a *API) string {
	t.Helper()
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	return s.store.Root()
}

// 反映時の競合は**エラーではなく結果**として返り、
// 本人がマージを承認するまで書き換わらない。
func TestImportBindingReturnsConflictAsOutcome(t *testing.T) {
	a, stub := openForImports(t)
	store := storeOf(t, a)
	if _, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementDraft,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	stub.scripts = []string{`{
	  "decisions": [], "open_issues": [],
	  "requirement_updates": [{"operation": "update", "target_id": "FR-INV-001",
	    "chapter": "functional-requirements", "title": "在庫引当",
	    "body_after": "自分の反映案（出荷指示時）。", "evidence_refs": ["IMP-001#L1-L1"]}],
	  "term_candidates": [], "contradictions": [], "perspective_candidates": []
	}`}

	view, err := a.ImportClipboardText("引当の起点を見直す。", string(importer.KindMaterial))
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := a.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatal(err)
	}

	// 候補生成の後に他のメンバーが同じ要件項目を変更する。
	base, err := store.CurrentBaseline("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRequirementGuarded(base, "FR-INV-001",
		func(r *projectstore.Requirement) error {
			r.Body = "他メンバーの変更。"
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	req := dialogue.FeedbackApproval{MaterialApproval: dialogue.MaterialApproval{
		ApprovalRequest: dialogue.ApprovalRequest{
			RequirementUpdates: []dialogue.RequirementApproval{
				{Candidate: analysis.Extraction.RequirementUpdates[0]}},
		},
	}}
	outcome, err := a.ApproveImportCandidates(view.ID, req)
	if err != nil {
		t.Fatalf("競合がエラーとして返った（結果で返すこと）: %v", err)
	}
	if len(outcome.Conflicts) != 1 || outcome.Applied != nil {
		t.Fatalf("競合が結果に入っていない: %+v", outcome)
	}
	c := outcome.Conflicts[0]
	if c.ID != "FR-INV-001" || c.Label != "FR-INV-001" || c.CurrentHash == "" {
		t.Fatalf("競合の内容が違う: %+v", c)
	}
	if !strings.Contains(c.BaselineBody, "受注確定時") || !strings.Contains(c.CurrentBody, "他メンバーの変更") {
		t.Errorf("三面の材料が揃っていない: %+v", c)
	}
	if outcome.Notice == "" {
		t.Errorf("競合の案内が無い: %+v", outcome)
	}

	// 承認するまで書き換わらない。
	before, err := store.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before.Body, "自分の反映案") {
		t.Fatal("承認前に反映された")
	}

	// 本人がマージを承認する（確認した基準版を添えて再実行）。
	req.Merges = []dialogue.MergeResolution{{ID: c.ID, BaselineHash: c.CurrentHash, Reference: view.ID}}
	merged, err := a.ApproveImportCandidates(view.ID, req)
	if err != nil {
		t.Fatalf("マージ承認後の反映に失敗: %v", err)
	}
	if merged.Applied == nil || len(merged.Applied.RequirementIDs) != 1 {
		t.Fatalf("マージ承認後の結果が違う: %+v", merged)
	}
	after, err := store.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after.Body, "自分の反映案") {
		t.Errorf("マージが反映されていない: %+v", after)
	}
}
