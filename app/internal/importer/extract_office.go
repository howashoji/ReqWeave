package importer

// 本ファイルは Word（docx）・Excel（xlsx）・PowerPoint（pptx）からのテキスト抽出
// （pptx は後の版で追加した）。
//
// いずれも ZIP + XML の Office Open XML であり、標準ライブラリ（archive/zip・encoding/xml）
// だけで扱える。外部ライブラリを足さない（依存を増やさない・取り込みで外部通信をしない原則を保つ）。

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// extractDocx は word/document.xml の段落を 1 行ずつ取り出す。
//
// 段落（w:p）を行、テキスト実行（w:t）を連結の単位とする。改ページ・改行（w:br）は改行にする。
func extractDocx(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", ErrNotExtractable
	}
	doc, err := readZipEntry(zr, "word/document.xml")
	if err != nil {
		return "", ErrNotExtractable
	}

	var b strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(doc))
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", ErrNotExtractable
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "br", "cr":
				b.WriteString("\n")
			case "tab":
				b.WriteString("\t")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// extractXlsx はシート名とセル位置を行テキストへ展開する。
//
// 出力例:
//
//	## シート: 受注一覧
//	A1: 受注番号
//	B1: 得意先
//
// セル位置を残すのは、取り込み分析の根拠参照（IMP-nnn#Lm-Ln）から
// 元表のどこを指しているかを人が追えるようにするため。
func extractXlsx(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", ErrNotExtractable
	}
	shared, err := readSharedStrings(zr)
	if err != nil {
		return "", ErrNotExtractable
	}
	sheets, err := readSheetIndex(zr)
	if err != nil {
		return "", ErrNotExtractable
	}

	var b strings.Builder
	for _, sh := range sheets {
		data, err := readZipEntry(zr, sh.target)
		if err != nil {
			continue // 参照が壊れているシートは飛ばす（他のシートは読める）
		}
		rows, err := readSheetCells(data, shared)
		if err != nil {
			return "", ErrNotExtractable
		}
		fmt.Fprintf(&b, "## シート: %s\n", sh.name)
		for _, r := range rows {
			fmt.Fprintf(&b, "%s: %s\n", r.ref, r.value)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// readZipEntry は ZIP 内の 1 エントリを読む。
func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("%s がありません", name)
}

// readSharedStrings は xl/sharedStrings.xml を索引順の配列で返す（無い場合は空）。
func readSharedStrings(zr *zip.Reader) ([]string, error) {
	data, err := readZipEntry(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, nil // 共有文字列を持たないブックもある
	}
	var doc struct {
		SI []struct {
			T string   `xml:"t"`
			R []string `xml:"r>t"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(doc.SI))
	for _, si := range doc.SI {
		if len(si.R) > 0 {
			out = append(out, strings.Join(si.R, ""))
			continue
		}
		out = append(out, si.T)
	}
	return out, nil
}

type sheetRef struct {
	name   string
	target string
}

// readSheetIndex は xl/workbook.xml と rels からシート名 → 実体パスの対応を作る。
func readSheetIndex(zr *zip.Reader) ([]sheetRef, error) {
	wb, err := readZipEntry(zr, "xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wb, &book); err != nil {
		return nil, err
	}

	targets := map[string]string{}
	if rels, err := readZipEntry(zr, "xl/_rels/workbook.xml.rels"); err == nil {
		var doc struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(rels, &doc); err != nil {
			return nil, err
		}
		for _, r := range doc.Rel {
			t := r.Target
			if !strings.HasPrefix(t, "/") {
				t = path.Join("xl", t)
			}
			targets[r.ID] = strings.TrimPrefix(t, "/")
		}
	}

	out := make([]sheetRef, 0, len(book.Sheets))
	for i, s := range book.Sheets {
		target, ok := targets[s.RID]
		if !ok {
			// rels が読めない場合の保険（既定の並び）。
			target = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		}
		out = append(out, sheetRef{name: s.Name, target: target})
	}
	return out, nil
}

type cell struct {
	ref   string
	value string
}

// readSheetCells は 1 シートの値を持つセルを A1 形式の位置つきで返す。
func readSheetCells(data []byte, shared []string) ([]cell, error) {
	var sheet struct {
		Rows []struct {
			Cells []struct {
				Ref  string `xml:"r,attr"`
				Type string `xml:"t,attr"`
				V    string `xml:"v"`
				IS   string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(data, &sheet); err != nil {
		return nil, err
	}
	var out []cell
	for _, row := range sheet.Rows {
		for _, c := range row.Cells {
			v := c.V
			switch c.Type {
			case "s": // 共有文字列の索引
				idx, err := strconv.Atoi(strings.TrimSpace(c.V))
				if err != nil || idx < 0 || idx >= len(shared) {
					continue
				}
				v = shared[idx]
			case "inlineStr":
				v = c.IS
			}
			if strings.TrimSpace(v) == "" {
				continue
			}
			out = append(out, cell{ref: c.Ref, value: v})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return cellLess(out[i].ref, out[j].ref) })
	return out, nil
}

// cellLess は A1 形式の参照を「行 → 列」の順で比較する（表示順を行優先にする）。
func cellLess(a, b string) bool {
	ca, ra := splitCellRef(a)
	cb, rb := splitCellRef(b)
	if ra != rb {
		return ra < rb
	}
	if len(ca) != len(cb) {
		return len(ca) < len(cb)
	}
	return ca < cb
}

// splitCellRef は "AB12" を ("AB", 12) に分ける。
func splitCellRef(ref string) (string, int) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		i++
	}
	n, _ := strconv.Atoi(ref[i:])
	return ref[:i], n
}

// extractPptx はスライド番号を見出しにして本文を並べる。
//
// 出力例:
//
//	## スライド 1
//	在庫管理システム 刷新方針
//	現行の課題と移行案
//
//	### スライド 1 の発表者ノート
//	現行は Excel 台帳で運用している
//
// スライド番号を残すのは xlsx がシート名・セル位置を残すのと同じ理由である。
// 取り込み分析の根拠参照（IMP-nnn#Lm-Ln）から**元資料の何枚目を指しているか**を人が追えるようにする。
//
// 発表者ノート（`ppt/notesSlides/`）も本文に含める。
// 要件定義の資料では、スライド本文が箇条書きの見出しだけで、根拠・前提がノート側に
// 書かれていることが多いため。スライドとの対応は `ppt/slides/_rels/slideN.xml.rels` で解決する
// （ファイル名の番号は一致しないことがあるため、番号での突き合わせをしない）。
func extractPptx(content []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", ErrNotExtractable
	}
	slides := slideEntries(zr)
	if len(slides) == 0 {
		return "", ErrNotExtractable
	}

	var b strings.Builder
	// hasText は**見出し以外の中身**が 1 つでもあったか。スライド番号の見出しだけを並べた
	// extracted.md は分析にも根拠参照にも使えないため、その場合は抽出不能として扱う
	// （見出しがあるぶん looksExtractable は素通りしてしまう）。
	hasText := false
	for i, name := range slides {
		data, err := readZipEntry(zr, name)
		if err != nil {
			continue // 1 枚読めなくても他のスライドは出す
		}
		body, err := drawingText(data)
		if err != nil {
			return "", ErrNotExtractable
		}
		fmt.Fprintf(&b, "## スライド %d\n", i+1)
		if strings.TrimSpace(body) != "" {
			b.WriteString(body)
			hasText = true
		}
		if note := slideNote(zr, name); strings.TrimSpace(note) != "" {
			fmt.Fprintf(&b, "\n### スライド %d の発表者ノート\n", i+1)
			b.WriteString(note)
			hasText = true
		}
		b.WriteString("\n")
	}
	if !hasText {
		return "", ErrNotExtractable
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// slideEntries は ppt/slides/slideN.xml を N の**数値順**で返す。
//
// 文字列順に並べると slide10 が slide2 より前に来るため、番号を数値として比較する
// （10 枚を超える資料で順序が崩れるのを防ぐ）。
func slideEntries(zr *zip.Reader) []string {
	var names []string
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "ppt/slides/slide") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		if strings.Contains(f.Name, "/_rels/") {
			continue
		}
		names = append(names, f.Name)
	}
	sort.SliceStable(names, func(i, j int) bool { return slideNo(names[i]) < slideNo(names[j]) })
	return names
}

// slideNo は "ppt/slides/slide12.xml" から 12 を取り出す（取り出せなければ 0）。
func slideNo(name string) int {
	base := strings.TrimSuffix(path.Base(name), ".xml")
	n, err := strconv.Atoi(strings.TrimPrefix(base, "slide"))
	if err != nil {
		return 0
	}
	return n
}

// slideNote は 1 枚のスライドに紐づく発表者ノートの本文を返す（無ければ空）。
//
// 対応は `ppt/slides/_rels/slideN.xml.rels` の relationship で解決する。
func slideNote(zr *zip.Reader, slide string) string {
	rels, err := readZipEntry(zr, path.Join(path.Dir(slide), "_rels", path.Base(slide)+".rels"))
	if err != nil {
		return ""
	}
	var doc struct {
		Rel []struct {
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(rels, &doc); err != nil {
		return ""
	}
	for _, r := range doc.Rel {
		if !strings.HasSuffix(r.Type, "/notesSlide") {
			continue
		}
		target := r.Target
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(path.Dir(slide), target)
		}
		data, err := readZipEntry(zr, target)
		if err != nil {
			return ""
		}
		text, err := drawingText(data)
		if err != nil {
			return ""
		}
		return text
	}
	return ""
}

// drawingText は DrawingML（a:t / a:p / a:br）から行単位の本文を取り出す。
//
// スライドとノートで同じ構造のため共通にしてある。
func drawingText(data []byte) (string, error) {
	var b strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "br":
				b.WriteString("\n")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	// 空段落が続いた分を 1 行に畳む（スライドはプレースホルダで空段落が多いため）。
	lines := strings.Split(b.String(), "\n")
	var kept []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return "", nil
	}
	return strings.Join(kept, "\n") + "\n", nil
}
