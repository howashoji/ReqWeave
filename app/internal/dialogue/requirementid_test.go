package dialogue

import (
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ID グループは利用者に入力させず、AI の提案を検証して採る。
func TestNormalizeRequirementGroup(t *testing.T) {
	cases := map[string]string{
		"INV":       "INV",
		" inv ":     "INV",
		"Inv2":      "INV2",
		"ABCDEFGH":  "ABCDEFGH",
		"":          "",
		"I":         "", // 1 文字は短すぎる
		"ABCDEFGHI": "", // 9 文字は長すぎる
		"💩":         "",
		"在庫":        "",
		"FR-INV":    "", // 記号を含む
		"INV 2":     "",
	}
	for in, want := range cases {
		if got := NormalizeRequirementGroup(in); got != want {
			t.Errorf("NormalizeRequirementGroup(%q) = %q, want %q", in, got, want)
		}
	}
}

// 章観点の既定は metamodel.yaml の id_group。未知の章観点は GEN へ倒す。
func TestChapterIDGroupAndKind(t *testing.T) {
	cases := map[string]struct {
		group string
		kind  string
	}{
		"scope":                       {"SCOPE", projectstore.RequirementFunctional},
		"functional-requirements":     {"FUNC", projectstore.RequirementFunctional},
		"non-functional-requirements": {"QUAL", projectstore.RequirementNonFunctional},
		"architecture":                {"ARCH", projectstore.RequirementFunctional},
		"unknown-chapter":             {fallbackIDGroup, projectstore.RequirementFunctional},
		"":                            {fallbackIDGroup, projectstore.RequirementFunctional},
	}
	for chapter, want := range cases {
		if got := ChapterIDGroup(chapter); got != want.group {
			t.Errorf("ChapterIDGroup(%q) = %q, want %q", chapter, got, want.group)
		}
		if got := ChapterRequirementKind(chapter); got != want.kind {
			t.Errorf("ChapterRequirementKind(%q) = %q, want %q", chapter, got, want.kind)
		}
	}
}

// すべての章観点が既定の ID グループを持つ（メタモデルへ章を足したら一緒に決める）。
func TestEveryChapterHasIDGroup(t *testing.T) {
	meta, err := LoadMetaModel()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, phase := range meta.Phases {
		for _, c := range phase.Chapters {
			n++
			if NormalizeRequirementGroup(c.IDGroup) == "" {
				t.Errorf("章観点 %s/%s に id_group が無い（または形式が不正）: %q", phase.ID, c.ID, c.IDGroup)
			}
		}
	}
	if n < 12 {
		t.Fatalf("章観点を読めていない（検査が空振り）: %d 件", n)
	}
}

// 新規候補の ID グループ・種別は、AI の提案 → 同じ章観点の既存グループ → 章観点の既定、の順に決まる。
func TestResolveRequirementIDs(t *testing.T) {
	ex := &Extraction{RequirementUpdates: []RequirementCandidate{
		{Operation: OperationCreate, Chapter: "functional-requirements", Title: "在庫引当", IDGroup: "inv"},
		{Operation: OperationCreate, Chapter: "functional-requirements", Title: "出荷", IDGroup: "💩"},
		{Operation: OperationCreate, Chapter: "scope", Title: "スコープ外", IDGroup: ""},
		{Operation: OperationCreate, Chapter: "non-functional-requirements", Title: "応答時間"},
		{Operation: OperationUpdate, TargetID: "FR-INV-001", Chapter: "functional-requirements", Title: "更新"},
	}}
	// 既に「機能要件」の章で ORD を使っている。
	ex.ResolveRequirementIDs(map[string]string{"functional-requirements": "ORD"})

	want := []struct {
		group string
		kind  string
	}{
		{"INV", projectstore.RequirementFunctional},   // AI の提案（大文字へそろえる）
		{"ORD", projectstore.RequirementFunctional},   // 提案が不正 → 同じ章観点の既存グループ
		{"SCOPE", projectstore.RequirementFunctional}, // 既存が無い章 → 章観点の既定
		{"QUAL", projectstore.RequirementNonFunctional},
	}
	for i, w := range want {
		got := ex.RequirementUpdates[i]
		if got.IDGroup != w.group || got.Kind != w.kind {
			t.Errorf("候補 %d（%s）: group=%q kind=%q, want group=%q kind=%q",
				i+1, got.Title, got.IDGroup, got.Kind, w.group, w.kind)
		}
	}
	// 既存項目の更新は触らない（ID は変えない）。
	if u := ex.RequirementUpdates[4]; u.IDGroup != "" || u.Kind != "" {
		t.Errorf("更新の候補に ID グループ・種別が付いた: %+v", u)
	}
}

// 章観点ごとの既存グループは、件数が最も多いものを採る。
func TestRequirementGroupsByChapter(t *testing.T) {
	got := RequirementGroupsByChapter([]projectstore.Requirement{
		{ID: "FR-INV-001", Chapter: "functional-requirements"},
		{ID: "FR-INV-002", Chapter: "functional-requirements"},
		{ID: "FR-ORD-001", Chapter: "functional-requirements"},
		{ID: "NFR-PF-001", Chapter: "non-functional-requirements"},
		{ID: "壊れたID", Chapter: "scope"},
		{ID: "FR-SCP-001", Chapter: ""},
	})
	if got["functional-requirements"] != "INV" {
		t.Errorf("件数の多いグループを採っていない: %q", got["functional-requirements"])
	}
	if got["non-functional-requirements"] != "PF" {
		t.Errorf("非機能の章のグループが違う: %q", got["non-functional-requirements"])
	}
	if _, ok := got["scope"]; ok {
		t.Errorf("解釈できない ID を数えている: %+v", got)
	}
}
