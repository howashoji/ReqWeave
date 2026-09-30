package importer

// 単体テスト（抽出。ファイル I/O を伴わない）。実行: make -C app test-unit
//
// 固定資料はバイナリを同梱せず、テスト内で組み立てる（中身が読める・差分が追える）。

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// --- 固定資料の組み立て ---------------------------------------------------

// buildDocx は word/document.xml だけを持つ最小の docx を組み立てる。
func buildDocx(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	var body strings.Builder
	for _, p := range paragraphs {
		fmt.Fprintf(&body, `<w:p><w:r><w:t>%s</w:t></w:r></w:p>`, p)
	}
	doc := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` + body.String() + `</w:body></w:document>`
	return zipOf(t, map[string]string{"word/document.xml": doc})
}

// buildPptx はスライドと発表者ノートを持つ最小の pptx を組み立てる。
//
// slides は 1 枚ぶんの段落の並び。notes[i] が空でなければ i+1 枚目にノートを付ける。
func buildPptx(t *testing.T, slides [][]string, notes map[int]string) []byte {
	t.Helper()
	files := map[string]string{}
	for i, paragraphs := range slides {
		n := i + 1
		var body strings.Builder
		for _, p := range paragraphs {
			fmt.Fprintf(&body, `<a:p><a:r><a:t>%s</a:t></a:r></a:p>`, p)
		}
		files[fmt.Sprintf("ppt/slides/slide%d.xml", n)] =
			`<?xml version="1.0"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
				`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree>` +
				`<p:sp><p:txBody>` + body.String() + `</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
		note, ok := notes[n]
		if !ok || note == "" {
			continue
		}
		// ノート側のファイル名は**スライド番号と一致させない**（rels で解決していることを確かめるため）。
		target := fmt.Sprintf("notesSlide%d.xml", 100-n)
		files[fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n)] =
			`<?xml version="1.0"?><Relationships>` +
				fmt.Sprintf(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/%s"/>`, target) +
				`</Relationships>`
		files["ppt/notesSlides/"+target] =
			`<?xml version="1.0"?><p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
				`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody>` +
				fmt.Sprintf(`<a:p><a:r><a:t>%s</a:t></a:r></a:p>`, note) +
				`</p:txBody></p:sp></p:spTree></p:cSld></p:notes>`
	}
	return zipOf(t, files)
}

// buildXlsx はシート 1 枚・共有文字列ありの最小の xlsx を組み立てる。
func buildXlsx(t *testing.T, sheetName string, cells map[string]string) []byte {
	t.Helper()
	// 共有文字列表を作る（値の出現順に索引を振る）。
	var shared []string
	index := map[string]int{}
	refs := sortedRefs(cells)
	for _, ref := range refs {
		v := cells[ref]
		if _, ok := index[v]; !ok {
			index[v] = len(shared)
			shared = append(shared, v)
		}
	}

	var si strings.Builder
	for _, s := range shared {
		fmt.Fprintf(&si, `<si><t>%s</t></si>`, s)
	}
	var rows strings.Builder
	for _, ref := range refs {
		_, r := splitCellRef(ref)
		fmt.Fprintf(&rows, `<row r="%d"><c r="%s" t="s"><v>%d</v></c></row>`, r, ref, index[cells[ref]])
	}

	return zipOf(t, map[string]string{
		"xl/workbook.xml": `<?xml version="1.0"?><workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			fmt.Sprintf(`<sheets><sheet name="%s" sheetId="1" r:id="rId1"/></sheets></workbook>`, sheetName),
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?><Relationships>` +
			`<Relationship Id="rId1" Type="worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<?xml version="1.0"?><sst>` + si.String() + `</sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0"?><worksheet><sheetData>` +
			rows.String() + `</sheetData></worksheet>`,
	})
}

func sortedRefs(cells map[string]string) []string {
	out := make([]string, 0, len(cells))
	for k := range cells {
		out = append(out, k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if cellLess(out[j], out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip エントリを作れない（%s）: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip へ書けない（%s）: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip を閉じられない: %v", err)
	}
	return buf.Bytes()
}

// buildPDF は Helvetica（標準エンコーディング）の 1 ページ PDF を組み立てる。
//
// 日本語は埋め込みフォントと CMap が要るため、ここでは抽出できる側の代表として英数字を使う。
// 日本語 PDF が抽出できない場合の扱いは TestExtractRejectsGarbledText が担う。
func buildPDF(t *testing.T, lines ...string) []byte {
	t.Helper()
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 72 720 Td 14 TL\n")
	for _, l := range lines {
		fmt.Fprintf(&content, "(%s) Tj T*\n", l)
	}
	content.WriteString("ET\n")
	stream := content.String()

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return buf.Bytes()
}

// --- 受け入れ条件のテスト -------------------------------------------------

// 受け入れ条件1: 7 経路それぞれで既知の内容からテキストが抽出される（pptx を含む）。
func TestExtractAllSevenFormats(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  Format
		content []byte
		want    []string // 抽出結果に含まれるべき文字列
	}{
		{"txt", FormatTxt, []byte("在庫は Excel 台帳で管理している。\n"), []string{"在庫は Excel 台帳で管理している。"}},
		{"md", FormatMD, []byte("# 現状\n\n受注は EDI と Web の2経路。\n"), []string{"# 現状", "受注は EDI と Web の2経路。"}},
		{"clipboard", FormatClipboard, []byte("打合せメモ: 在庫の締めは月末。"), []string{"打合せメモ: 在庫の締めは月末。"}},
		{"docx", FormatDocx, buildDocx(t, "在庫管理システムの現状", "受注は EDI と Web の2経路です。"),
			[]string{"在庫管理システムの現状", "受注は EDI と Web の2経路です。"}},
		{"xlsx", FormatXlsx, buildXlsx(t, "受注一覧", map[string]string{"A1": "受注番号", "B1": "得意先"}),
			[]string{"受注一覧", "受注番号", "得意先"}},
		{"pptx", FormatPptx, buildPptx(t, [][]string{{"在庫管理システム 刷新方針", "現行の課題と移行案"}}, nil),
			[]string{"在庫管理システム 刷新方針", "現行の課題と移行案"}},
		{"pdf", FormatPDF, buildPDF(t, "Inventory system", "EDI and Web ordering."),
			[]string{"Inventory system", "EDI and Web ordering."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract(tc.format, tc.content)
			if err != nil {
				t.Fatalf("抽出に失敗: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("抽出結果に %q が無い:\n%s", w, got)
				}
			}
		})
	}
}

// 受け入れ条件3: xlsx はシート名とセル位置が行テキストに含まれる。
func TestExtractXlsxIncludesSheetNameAndCellRefs(t *testing.T) {
	x := buildXlsx(t, "受注一覧", map[string]string{"A1": "受注番号", "B1": "得意先", "A2": "1001"})

	got, err := Extract(FormatXlsx, x)
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}
	for _, want := range []string{"## シート: 受注一覧", "A1: 受注番号", "B1: 得意先", "A2: 1001"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
	// 行優先で並ぶ（A1 → B1 → A2）。
	if i, j := strings.Index(got, "B1: "), strings.Index(got, "A2: "); i > j {
		t.Errorf("セルの並びが行優先になっていない:\n%s", got)
	}
}

// 受け入れ条件4: 抽出できない入力は ErrNotExtractable になる。
func TestExtractReportsNotExtractable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  Format
		content []byte
	}{
		{"docx ではない ZIP", FormatDocx, zipOf(t, map[string]string{"foo.txt": "bar"})},
		{"ZIP ですらない docx", FormatDocx, []byte("これは docx ではない")},
		{"xlsx ですらない", FormatXlsx, []byte("これは xlsx ではない")},
		{"pptx ではない ZIP（スライドが無い）", FormatPptx, zipOf(t, map[string]string{"foo.txt": "bar"})},
		{"ZIP ですらない pptx", FormatPptx, []byte("これは pptx ではない")},
		{"スライドはあるが本文が空", FormatPptx, buildPptx(t, [][]string{{"", "  "}}, nil)},
		{"PDF ですらない", FormatPDF, []byte("%PDF-1.4 だけで中身が無い")},
		{"UTF-8 として不正なテキスト", FormatTxt, []byte{0xFF, 0xFE, 0x00, 0x41}},
		{"空のテキスト", FormatTxt, []byte("   \n\t\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Extract(tc.format, tc.content)
			if !errors.Is(err, ErrNotExtractable) {
				t.Fatalf("ErrNotExtractable ではない: err=%v text=%q", err, got)
			}
			if got != "" {
				t.Errorf("失敗なのに本文を返している: %q", got)
			}
		})
	}
}

// 品質ゲート: エラーにならないまま文字化けした結果を extracted.md へ残さない。
//
// 実測: ToUnicode CMap を持たない Identity-H の日本語 PDF では、
// ライブラリがエラーを返さずに文字化けした文字列を返す。これを保存すると
// 取り込み分析の入力と根拠参照（IMP-nnn#Lm-Ln）を汚す。
func TestExtractRejectsGarbledText(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want bool // looksExtractable の期待
	}{
		{"正常な日本語", "在庫管理システムの現状\n受注は EDI と Web の2経路です。", true},
		{"正常な英数字", "Inventory system\nEDI and Web ordering.", true},
		{"表形式（タブ・改行を含む）", "受注番号\t得意先\n1001\t山田商事\n", true},
		{"置換文字だらけ（文字化けした PDF）", strings.Repeat("�", 20) + "EDI", false},
		{"制御文字だらけ", "\x01\x02\x03\x04\x05\x06\x07\x08在庫", false},
		{"空白のみ", "  \n\t ", false},
		{"空", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksExtractable(tc.text); got != tc.want {
				t.Errorf("looksExtractable(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// 受け入れ条件5: 抽出失敗時の文言が取り込みのエラーの様式であること。
func TestNotExtractableMessageFollowsErrorCatalog(t *testing.T) {
	msg := ErrNotExtractable.Error()

	// 原因 → 次に取る行動 → 原本が保持される旨。
	for _, want := range []string{
		"テキストを取り出せませんでした",    // 原因
		"確認してください",           // 次に取る行動
		"原本は取り込み済みのまま保持されます", // 原本が保持される旨
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("文言に %q が無い: %q", want, msg)
		}
	}
	// 内部用語・生のコード値を含めない。
	for _, ng := range []string{
		"error", "Error", "err", "nil", "panic", "ErrNotExtractable",
		"extraction_status", "failed", "ToUnicode", "Identity-H", "CMap",
		"pdf.Open", "zip", "xml", "UTF-8", "IMP-",
	} {
		if strings.Contains(msg, ng) {
			t.Errorf("利用者向け文言に内部用語 %q が含まれる: %q", ng, msg)
		}
	}
}

// 受け入れ条件2 の前提: 同じ入力からは同じ結果が得られる（extracted.md の安定性）。
func TestExtractIsDeterministic(t *testing.T) {
	x := buildXlsx(t, "受注一覧", map[string]string{
		"A1": "受注番号", "B1": "得意先", "C1": "金額", "A2": "1001", "B2": "山田商事", "C2": "120000",
	})
	first, err := Extract(FormatXlsx, x)
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}
	for i := 0; i < 5; i++ {
		got, err := Extract(FormatXlsx, x)
		if err != nil {
			t.Fatalf("%d 回目の抽出に失敗: %v", i, err)
		}
		if got != first {
			t.Fatalf("抽出結果が実行ごとに変わる（行参照が安定しない）:\n1回目:\n%s\n%d回目:\n%s", first, i+1, got)
		}
	}
}

// 対応外の形式は明示的に拒否する（列挙にない値が黙って通らない）。
func TestExtractRejectsUnknownFormat(t *testing.T) {
	if _, err := Extract("csv", []byte("a,b")); err == nil {
		t.Error("対応外の形式が受理された")
	}
}

// 受け入れ条件2: pptx はスライド番号の見出しを持ち、発表者ノートも本文に含まれる。
//
// スライド番号を残すのは、取り込み分析の根拠参照（IMP-nnn#Lm-Ln）から
// **元資料の何枚目を指しているか**を人が追えるようにするため（xlsx のシート名・セル位置と同じ理由）。
func TestExtractPptxIncludesSlideNumbersAndNotes(t *testing.T) {
	p := buildPptx(t,
		[][]string{
			{"在庫管理システム 刷新方針", "現行の課題"},
			{"移行案", "段階移行とする"},
		},
		map[int]string{2: "現行は Excel 台帳で運用している"},
	)

	got, err := Extract(FormatPptx, p)
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}
	for _, want := range []string{
		"## スライド 1", "在庫管理システム 刷新方針", "現行の課題",
		"## スライド 2", "移行案", "段階移行とする",
		"### スライド 2 の発表者ノート", "現行は Excel 台帳で運用している",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
	// ノートは**そのスライドの中**に置く（次のスライドの見出しより前）。
	if i, j := strings.Index(got, "### スライド 2 の発表者ノート"), strings.Index(got, "移行案"); i < j {
		t.Errorf("ノートがスライド本文より前に出ている:\n%s", got)
	}
	// ノートを持たないスライド 1 に見出しを作らない。
	if strings.Contains(got, "### スライド 1 の発表者ノート") {
		t.Errorf("ノートの無いスライドに見出しを作っている:\n%s", got)
	}
}

// pptx のスライドは**数値順**に並ぶ（文字列順だと slide10 が slide2 より前に来る）。
func TestExtractPptxOrdersSlidesNumerically(t *testing.T) {
	slides := make([][]string, 12)
	for i := range slides {
		slides[i] = []string{fmt.Sprintf("これは %d 枚目の本文", i+1)}
	}

	got, err := Extract(FormatPptx, buildPptx(t, slides, nil))
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}
	prev := -1
	for i := 1; i <= 12; i++ {
		at := strings.Index(got, fmt.Sprintf("## スライド %d\n", i))
		if at < 0 {
			t.Fatalf("スライド %d の見出しが無い:\n%s", i, got)
		}
		if at <= prev {
			t.Fatalf("スライド %d が前のスライドより前に出ている（数値順で並んでいない）:\n%s", i, got)
		}
		prev = at
	}
	// 見出しの番号と中身が対応していること（並べ替えで中身がずれていないか）。
	for i := 1; i <= 12; i++ {
		head := fmt.Sprintf("## スライド %d\n", i)
		body := fmt.Sprintf("これは %d 枚目の本文", i)
		at := strings.Index(got, head)
		if !strings.HasPrefix(got[at+len(head):], body) {
			t.Errorf("スライド %d の見出しの直後が %q ではない:\n%s", i, body, got[at:])
		}
	}
}

// 発表者ノートの対応は**ファイル名の番号ではなく rels** で解決する。
//
// buildPptx はノートのファイル名をわざとスライド番号とずらして作る。
// 番号で突き合わせる実装に戻したら、この検査が落ちる。
func TestExtractPptxResolvesNotesThroughRelationships(t *testing.T) {
	p := buildPptx(t,
		[][]string{{"1 枚目"}, {"2 枚目"}, {"3 枚目"}},
		map[int]string{1: "1 枚目のノート", 3: "3 枚目のノート"},
	)

	got, err := Extract(FormatPptx, p)
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}
	if !strings.Contains(got, "### スライド 1 の発表者ノート\n1 枚目のノート") {
		t.Errorf("スライド 1 のノートが正しく紐づいていない:\n%s", got)
	}
	if !strings.Contains(got, "### スライド 3 の発表者ノート\n3 枚目のノート") {
		t.Errorf("スライド 3 のノートが正しく紐づいていない:\n%s", got)
	}
	if strings.Contains(got, "### スライド 2 の発表者ノート") {
		t.Errorf("ノートを持たないスライド 2 に見出しが出ている:\n%s", got)
	}
}
