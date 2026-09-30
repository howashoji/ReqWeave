//go:build integration

// 結合テスト（取り込み元への遡及 × 実ファイル）。

package importer

import (
	"strings"
	"testing"
)

const refDocument = `## 在庫管理の現行業務

受注が確定した時点で在庫を引き当てる。
欠品時はバックオーダーを起票する。

## 棚卸

棚卸は月次で実施し、差異は倉庫長が承認する。`

func newRefMaterial(t *testing.T) (*Importer, *Meta) {
	t.Helper()
	im, _ := newTestImporter(t)
	meta, err := im.Import(Input{
		Kind: KindMaterial, SourceName: "現行業務.md", SourceFormat: FormatMD,
		Content:          []byte(refDocument),
		ExtractionStatus: StatusExtracted, ExtractedText: refDocument,
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	return im, meta
}

// 根拠参照から原本の所在と抽出テキストの該当箇所を取得できる。
func TestResolveRefReturnsSourceAndExcerpt(t *testing.T) {
	im, meta := newRefMaterial(t)

	got, err := im.ResolveRef(FormatRef(meta.ID, 3, 4))
	if err != nil {
		t.Fatalf("根拠を解決できない: %v", err)
	}
	if got.ImportID != meta.ID || got.SourceName != "現行業務.md" || got.Kind != KindMaterial {
		t.Errorf("資料の識別が違う: %+v", got)
	}
	if got.SourcePath != SourcePath(meta.ID, FormatMD) {
		t.Errorf("原本の所在が違う: %q", got.SourcePath)
	}
	if len(got.Excerpt) != 2 ||
		!strings.Contains(got.Excerpt[0], "受注が確定した時点") ||
		!strings.Contains(got.Excerpt[1], "バックオーダー") {
		t.Errorf("該当箇所が違う: %q", got.Excerpt)
	}
	// 前後 2 行の文脈がつく（先頭行の行番号つき）。
	if got.ContextStartLine != 1 || len(got.Context) != 6 {
		t.Errorf("文脈の範囲が違う: 開始 L%d・%d 行", got.ContextStartLine, len(got.Context))
	}
	if !strings.Contains(got.Context[0], "在庫管理の現行業務") {
		t.Errorf("文脈の先頭が違う: %q", got.Context[0])
	}
	// 原本は解決を通しても変わらない（読み出しのみ）。
	raw, err := im.ReadSource(meta.ID)
	if err != nil || string(raw) != refDocument {
		t.Errorf("原本が変わっている: %v", err)
	}
}

// 資料の先頭・末尾でも文脈の切り出しが範囲外にならない。
func TestResolveRefAtDocumentEdges(t *testing.T) {
	im, meta := newRefMaterial(t)
	lines := strings.Count(refDocument, "\n") + 1

	head, err := im.ResolveRef(FormatRef(meta.ID, 1, 1))
	if err != nil {
		t.Fatalf("先頭行を解決できない: %v", err)
	}
	if head.ContextStartLine != 1 || len(head.Context) != 3 {
		t.Errorf("先頭の文脈が違う: 開始 L%d・%d 行", head.ContextStartLine, len(head.Context))
	}

	tail, err := im.ResolveRef(FormatRef(meta.ID, lines, lines))
	if err != nil {
		t.Fatalf("末尾行を解決できない: %v", err)
	}
	if tail.EndLine != lines || len(tail.Excerpt) != 1 {
		t.Errorf("末尾の該当箇所が違う: %+v", tail)
	}
	if last := tail.Context[len(tail.Context)-1]; !strings.Contains(last, "倉庫長が承認") {
		t.Errorf("末尾の文脈が違う: %q", last)
	}
}

// 存在しない資料・行範囲・形式違いはエラー（呼び出し側が参照欠落として扱う）。
func TestResolveRefRejectsUnresolvable(t *testing.T) {
	im, meta := newRefMaterial(t)
	lines := strings.Count(refDocument, "\n") + 1

	for name, ref := range map[string]string{
		"存在しない資料":  FormatRef("IMP-099", 1, 2),
		"範囲外の行":    FormatRef(meta.ID, lines, lines+5),
		"発話参照":     "S-0001#utt-00001",
		"回答参照":     "QS-001#q-01",
		"行範囲のない参照": meta.ID,
	} {
		if _, err := im.ResolveRef(ref); err == nil {
			t.Errorf("%s（%s）が解決できてしまった", name, ref)
		}
	}
}

// 抽出できていない資料は該当箇所を持たない（原本は保持されたまま）。
func TestResolveRefOnUnextractedSource(t *testing.T) {
	im, _ := newTestImporter(t)
	meta, err := im.Import(Input{
		Kind: KindMaterial, SourceName: "画像PDF.pdf", SourceFormat: FormatPDF,
		Content: []byte("%PDF-1.7\n"), ExtractionStatus: StatusFailed,
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	if _, err := im.ResolveRef(FormatRef(meta.ID, 1, 1)); err == nil {
		t.Fatal("抽出できていない資料の該当箇所が解決できてしまった")
	}
	if _, err := im.ReadSource(meta.ID); err != nil {
		t.Errorf("原本が保持されていない: %v", err)
	}
}
