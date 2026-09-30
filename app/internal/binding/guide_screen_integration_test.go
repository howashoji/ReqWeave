//go:build integration

package binding

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

/*
 * 工程ガイド（次にやることの案内）の結合。
 *
 * 導出そのものは internal/guide の単体テストが持つ。ここでは
 * **実際のプロジェクトの状態を読んで導出へ渡せているか**を確かめる
 * （固定文言を返していないこと＝状態を変えると結果が変わること）。
 */

func TestWorkflowGuideReflectsProjectState(t *testing.T) {
	a, root, _ := newPerspectiveAPI(t)
	_ = root

	// 何も無いプロジェクトでは、まず対話で要件を出す。
	first, err := a.WorkflowGuide()
	if err != nil {
		t.Fatalf("工程ガイドを取得できない: %v", err)
	}
	if first.StageID != "dialogue" {
		t.Fatalf("最初の工程が対話でない: %+v", first)
	}
	if first.StageTotal != 6 || first.StageIndex < 1 {
		t.Fatalf("工程の位置が入っていない: %+v", first)
	}
	if len(first.Stages) != first.StageTotal {
		t.Fatalf("工程の並びが返っていない: %d 件", len(first.Stages))
	}
	if first.Next == "" || first.Button == "" {
		t.Fatalf("次にやること・操作名が空: %+v", first)
	}

	// 資料を取り込むと、まず「分析する」へ変わる（取り込みは原本の登録までのため）。
	s, err := a.current()
	if err != nil {
		t.Fatalf("セッションを取れない: %v", err)
	}
	im := importer.New(s.store)
	if _, err := im.ImportWithExtraction(importer.Input{
		Kind: importer.KindMaterial, SourceName: "現行業務.md",
		SourceFormat: importer.FormatMD,
		Content:      []byte("# 在庫管理\n\n受注確定時に在庫を引き当てる。\n"),
	}); err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}

	afterImport, err := a.WorkflowGuide()
	if err != nil {
		t.Fatalf("工程ガイドを取得できない: %v", err)
	}
	if afterImport.StageID != "imports" {
		t.Fatalf("資料の取り込み工程へ移っていない: %+v", afterImport)
	}
	if !strings.Contains(afterImport.Next, "1 件") {
		t.Fatalf("未分析の件数が文に入っていない: %q", afterImport.Next)
	}
	if afterImport.Next == first.Next {
		t.Fatal("状態を変えても次にやることが変わっていない（固定文言になっている）")
	}
}

func TestWorkflowGuideCountsAnalyzedMaterialAsDone(t *testing.T) {
	// 分析して承認したあとは、その資料をもう「分析してください」と言わない。
	// 分析済みかどうかは根拠の逆引きで見ているため、
	// 資料を根拠に持つレコードが 1 件でもあれば済んだものとして扱う。
	a, _, _ := newPerspectiveAPI(t)
	s, err := a.current()
	if err != nil {
		t.Fatalf("セッションを取れない: %v", err)
	}
	im := importer.New(s.store)
	meta, err := im.ImportWithExtraction(importer.Input{
		Kind: importer.KindMaterial, SourceName: "現行業務.md",
		SourceFormat: importer.FormatMD,
		Content:      []byte("# 在庫管理\n\n受注確定時に在庫を引き当てる。\n"),
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}

	before, err := a.WorkflowGuide()
	if err != nil {
		t.Fatalf("工程ガイドを取得できない: %v", err)
	}
	if before.StageID != "imports" {
		t.Fatalf("取り込み直後は分析を促すはず: %+v", before)
	}

	if _, err := s.store.CreateDecision(projectstore.Decision{
		TopicKey: "business-flow/main-flow",
		Body:     "受注確定時に在庫を引き当てる。",
		Evidence: []string{meta.ID + "#L3-L3"},
	}); err != nil {
		t.Fatalf("決定事項を作れない: %v", err)
	}

	after, err := a.WorkflowGuide()
	if err != nil {
		t.Fatalf("工程ガイドを取得できない: %v", err)
	}
	if after.StageID == "imports" {
		t.Fatalf("承認済みの資料をまだ未分析として扱っている: %+v", after)
	}
}
