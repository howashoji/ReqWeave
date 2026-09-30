package docgen

// 本ファイルは版どうしの差分表示の算出を担う。
//
// ファイル（章）単位の行ベース比較。行単位 LCS で追加・削除・変更行を求める。
// 差分再生成後の変更箇所一覧も同じ比較器を使う。

import (
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 差分行の種別。
const (
	DiffEqual  = "equal"
	DiffAdd    = "add"
	DiffRemove = "remove"
)

// DiffLine は差分の 1 行。
type DiffLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// ChapterDiff は 1 章ぶんの差分。
type ChapterDiff struct {
	FileName string `json:"fileName"`
	Chapter  string `json:"chapter"`
	// Status は before / after のどちらにあるか（added / removed / changed / unchanged）。
	Status  string     `json:"status"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Lines   []DiffLine `json:"lines,omitempty"`
}

// 章の差分状態。
const (
	DiffStatusUnchanged = "unchanged"
	DiffStatusChanged   = "changed"
	DiffStatusAdded     = "added"
	DiffStatusRemoved   = "removed"
)

// Changed は内容に差があるかを返す。
func (d ChapterDiff) Changed() bool { return d.Status != DiffStatusUnchanged }

// DiffDocuments は 2 つの版（確定版どうし、またはドラフトと確定版）の章単位の差分を返す。
//
// 章の並びはファイル名の昇順。変更の無い章も status: unchanged で含める
// （文書一覧で変更有無を示すため）。
func DiffDocuments(before, after []projectstore.DocumentChapter) []ChapterDiff {
	beforeByName := map[string]projectstore.DocumentChapter{}
	for _, c := range before {
		beforeByName[c.FileName] = c
	}
	afterByName := map[string]projectstore.DocumentChapter{}
	for _, c := range after {
		afterByName[c.FileName] = c
	}

	names := map[string]bool{}
	for name := range beforeByName {
		names[name] = true
	}
	for name := range afterByName {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	out := make([]ChapterDiff, 0, len(sorted))
	for _, name := range sorted {
		b, hasBefore := beforeByName[name]
		a, hasAfter := afterByName[name]
		d := ChapterDiff{FileName: name}
		switch {
		case hasBefore && hasAfter:
			d.Chapter = a.Chapter
			d.Lines = diffLines(b.Body, a.Body)
		case hasAfter:
			d.Chapter = a.Chapter
			d.Status = DiffStatusAdded
			d.Lines = diffLines("", a.Body)
		default:
			d.Chapter = b.Chapter
			d.Status = DiffStatusRemoved
			d.Lines = diffLines(b.Body, "")
		}
		for _, line := range d.Lines {
			switch line.Kind {
			case DiffAdd:
				d.Added++
			case DiffRemove:
				d.Removed++
			}
		}
		if d.Status == "" {
			d.Status = DiffStatusUnchanged
			if d.Added > 0 || d.Removed > 0 {
				d.Status = DiffStatusChanged
			}
		}
		out = append(out, d)
	}
	return out
}

// diffLines は行単位 LCS で差分行の列を返す。
func diffLines(before, after string) []DiffLine {
	b := splitLines(before)
	a := splitLines(after)

	// LCS の長さ表（行数は章あたり数百のため O(n*m) で足りる）。
	lcs := make([][]int, len(b)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(a)+1)
	}
	for i := len(b) - 1; i >= 0; i-- {
		for j := len(a) - 1; j >= 0; j-- {
			if b[i] == a[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []DiffLine
	i, j := 0, 0
	for i < len(b) && j < len(a) {
		switch {
		case b[i] == a[j]:
			out = append(out, DiffLine{Kind: DiffEqual, Text: b[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Kind: DiffRemove, Text: b[i]})
			i++
		default:
			out = append(out, DiffLine{Kind: DiffAdd, Text: a[j]})
			j++
		}
	}
	for ; i < len(b); i++ {
		out = append(out, DiffLine{Kind: DiffRemove, Text: b[i]})
	}
	for ; j < len(a); j++ {
		out = append(out, DiffLine{Kind: DiffAdd, Text: a[j]})
	}
	return out
}

// splitLines は本文を行へ分ける（末尾の空行は落とす）。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
