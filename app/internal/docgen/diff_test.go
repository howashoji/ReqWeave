package docgen

import (
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func diffChapter(name, body string) projectstore.DocumentChapter {
	return projectstore.DocumentChapter{
		DocKind: projectstore.DocKindRequirements, Chapter: name, FileName: name + ".md",
		Status: projectstore.DocStatusGenerated, GeneratedAt: time.Now(), Body: body,
	}
}

// 行単位の追加・削除・変更を算出し、変更の無い章も一覧に含める。
func TestDiffDocuments(t *testing.T) {
	before := []projectstore.DocumentChapter{
		diffChapter("01", "# 業務背景\n\n現状は Excel 台帳。\n改善の余地がある。"),
		diffChapter("02", "# スコープ\n\n受注から出荷まで。"),
		diffChapter("03", "# 利用者\n\n営業部と情シス。"),
	}
	after := []projectstore.DocumentChapter{
		diffChapter("01", "# 業務背景\n\n現状は Excel 台帳と紙の受払簿。\n改善の余地がある。"),
		diffChapter("02", "# スコープ\n\n受注から出荷まで。"),
		diffChapter("04", "# 業務フロー\n\n受注 → 引当 → 出荷。"),
	}
	got := DiffDocuments(before, after)

	byName := map[string]ChapterDiff{}
	for _, d := range got {
		byName[d.FileName] = d
	}
	if len(got) != 4 {
		t.Fatalf("章数が違う: %d %+v", len(got), got)
	}

	// 変更された章。
	changed := byName["01.md"]
	if changed.Status != DiffStatusChanged || changed.Added != 1 || changed.Removed != 1 {
		t.Errorf("変更章の差分が違う: %+v", changed)
	}
	if !changed.Changed() {
		t.Error("変更章が Changed でない")
	}
	// 変更のない章も一覧に含める（文書一覧で章ごとの変更有無を示すため）。
	unchanged := byName["02.md"]
	if unchanged.Status != DiffStatusUnchanged || unchanged.Added != 0 || unchanged.Removed != 0 {
		t.Errorf("無変更章の差分が違う: %+v", unchanged)
	}
	// 削除・追加された章。
	if byName["03.md"].Status != DiffStatusRemoved {
		t.Errorf("削除章の状態が違う: %+v", byName["03.md"])
	}
	if byName["04.md"].Status != DiffStatusAdded {
		t.Errorf("追加章の状態が違う: %+v", byName["04.md"])
	}

	// 行の内容（変更前後が並ぶ）。
	var removed, added string
	for _, line := range changed.Lines {
		switch line.Kind {
		case DiffRemove:
			removed = line.Text
		case DiffAdd:
			added = line.Text
		}
	}
	if removed != "現状は Excel 台帳。" || added != "現状は Excel 台帳と紙の受払簿。" {
		t.Errorf("変更前後の行が違う: %q → %q", removed, added)
	}
}

// LCS の基本性質: 共通行は equal として残り、順序が保たれる。
func TestDiffLines(t *testing.T) {
	got := diffLines("a\nb\nc\nd", "a\nx\nc\nd\ne")
	var kinds []string
	var texts []string
	for _, l := range got {
		kinds = append(kinds, l.Kind)
		texts = append(texts, l.Text)
	}
	// a=equal, b=remove, x=add, c=equal, d=equal, e=add（削除が先に来る実装）
	want := []string{DiffEqual, DiffRemove, DiffAdd, DiffEqual, DiffEqual, DiffAdd}
	if len(kinds) != len(want) {
		t.Fatalf("差分行数が違う: %v / %v", kinds, texts)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("差分の種別が違う: %v / %v", kinds, texts)
		}
	}
	// 同一内容なら全行 equal。
	for _, l := range diffLines("a\nb", "a\nb") {
		if l.Kind != DiffEqual {
			t.Errorf("同一内容で差分が出た: %+v", l)
		}
	}
	// 片側が空なら全行 add / remove。
	if len(diffLines("", "a\nb")) != 2 {
		t.Error("追加のみの差分が算出できない")
	}
	if len(diffLines("a\nb", "")) != 2 {
		t.Error("削除のみの差分が算出できない")
	}
}
