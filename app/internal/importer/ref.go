package importer

// 本ファイルは取り込み元への遡及（要件などの根拠から元の資料をたどること）を担う。
//
// 承認済みレコードの根拠 `IMP-nnn#Lm-Ln` から、
// 原本の所在と抽出テキストの該当箇所を解決する。
// 逆方向（資料 → 承認済みレコード）は資料側に保持せず、レコード側の evidence から
// 派生で解決する（参照は片方向だけに持ち、二重管理しない）。

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// refRe は取り込み資料の根拠参照（IMP-nnn#Lm-Ln）。
var refRe = regexp.MustCompile(`^(IMP-\d{3})#L(\d+)-L(\d+)$`)

// contextLines は該当箇所の前後に付ける行数（遡及表示の文脈）。
const contextLines = 2

// ParseRef は根拠参照を資料 ID と行範囲へ分解する。
//
// 形式が違う参照（発話 S-nnnn#utt-nnnnn・回答 QS-nnn#q-nn）は ok = false を返す
// （呼び出し側がそれぞれの解決経路へ振り分ける）。
func ParseRef(ref string) (importID string, from, to int, ok bool) {
	m := refRe.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", 0, 0, false
	}
	from, err1 := strconv.Atoi(m[2])
	to, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil || from < 1 || to < from {
		return "", 0, 0, false
	}
	return m[1], from, to, true
}

// FormatRef は資料 ID と行範囲から根拠参照を作る（根拠参照の表記を 1 か所に保つ）。
func FormatRef(importID string, from, to int) string {
	return fmt.Sprintf("%s#L%d-L%d", importID, from, to)
}

// RefLocation は根拠参照の解決結果（遡及の表示材料）。
type RefLocation struct {
	Ref        string `json:"ref"`
	ImportID   string `json:"importId"`
	SourceName string `json:"sourceName"`
	Kind       Kind   `json:"kind"`
	Format     Format `json:"format"`
	ImportedAt string `json:"importedAt"`
	// SourcePath は原本のプロジェクトルートからの相対パス（原本を開くために使う）。
	SourcePath string `json:"sourcePath"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	// Excerpt は該当箇所そのもの（抽出テキストの当該行）。
	Excerpt []string `json:"excerpt"`
	// ContextStartLine / Context は前後 2 行を含めた文脈（先頭行の行番号つき）。
	ContextStartLine int      `json:"contextStartLine"`
	Context          []string `json:"context"`
}

// ResolveRef は根拠参照から原本の所在と該当箇所を解決する。
//
// 形式不正・存在しない資料・抽出テキストの範囲外はいずれもエラーとして返す
// （呼び出し側は参照欠落として扱う）。
func (im *Importer) ResolveRef(ref string) (*RefLocation, error) {
	id, from, to, ok := ParseRef(ref)
	if !ok {
		return nil, fmt.Errorf("取り込み資料の根拠参照の形式ではありません: %q", ref)
	}
	meta, err := im.Load(id)
	if err != nil {
		return nil, err
	}
	text, err := im.ReadExtracted(id)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(text, "\n")
	if to > len(lines) {
		return nil, fmt.Errorf("抽出テキストに存在しない行範囲です（%s。全 %d 行）", ref, len(lines))
	}

	ctxFrom := from - contextLines
	if ctxFrom < 1 {
		ctxFrom = 1
	}
	ctxTo := to + contextLines
	if ctxTo > len(lines) {
		ctxTo = len(lines)
	}
	return &RefLocation{
		Ref: ref, ImportID: meta.ID, SourceName: meta.SourceName, Kind: meta.Kind,
		Format: meta.SourceFormat, ImportedAt: meta.ImportedAt.UTC().Format("2006-01-02T15:04:05Z"),
		SourcePath: SourcePath(meta.ID, meta.SourceFormat),
		StartLine:  from, EndLine: to,
		Excerpt:          append([]string(nil), lines[from-1:to]...),
		ContextStartLine: ctxFrom,
		Context:          append([]string(nil), lines[ctxFrom-1:ctxTo]...),
	}, nil
}
