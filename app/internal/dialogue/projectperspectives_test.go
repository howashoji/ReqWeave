package dialogue

// 単体テスト（プロジェクト観点の論点連結・充足率非算入・重複指摘の検証）。実行: make -C app test-unit

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// testProjectPerspective は登録済みプロジェクト観点 1 件（関連章観点を持たない）。
func testProjectPerspective(id, name string) SelectedPerspective {
	return SelectedPerspective{
		DomainID:    projectstore.PerspectiveTopicPrefix,
		DomainName:  ProjectPerspectiveDomainName,
		Perspective: Perspective{ID: id, Name: name, Topics: name + "の要旨"},
		TopicKey:    projectstore.PerspectiveTopicPrefix + "/" + id,
	}
}

// 登録済み観点が論点キー custom/PRS-nnn として対象論点に加わり、
// 選択中のプリセット観点と併存する。
func TestSelectTopicIncludesProjectPerspectives(t *testing.T) {
	preset, err := SelectedPerspectives([]string{"inventory"})
	if err != nil {
		t.Fatal(err)
	}
	project := []SelectedPerspective{
		testProjectPerspective("PRS-001", "棚卸の差異処理"),
		testProjectPerspective("PRS-002", "入出庫の権限"),
	}
	perspectives := append(append([]SelectedPerspective{}, preset...), project...)

	// 全章観点を充足させ、プリセット観点も既決にして、プロジェクト観点だけが残る状態を作る。
	records, completeness := satisfiedThrough(t, lastRequirementChapter(t))
	for i, v := range preset {
		records.Decisions = append(records.Decisions, projectstore.Decision{
			ID: projectstore.IDDecision.Format(800 + i), TopicKey: v.TopicKey, Body: "決めた"})
	}

	got, err := SelectTopic(PhaseRequirements, records, completeness, nil, perspectives)
	if err != nil {
		t.Fatalf("論点を選べない: %v", err)
	}
	if got == nil {
		t.Fatal("プロジェクト観点が論点として選ばれない")
	}
	if got.TopicKey != "custom/PRS-001" || !IsProjectTopicKey(got.TopicKey) {
		t.Fatalf("論点キーが違う: %+v", got)
	}
	if got.Reason != ReasonProjectPerspective {
		t.Errorf("選定理由が違う: %q", got.Reason)
	}
	if got.ItemName != "棚卸の差異処理" || got.FollowUpIndex != 1 {
		t.Errorf("選定内容が違う: %+v", got)
	}

	// プロジェクト観点の論点キーも既決判定の対象。決めたら次の観点へ移る。
	records.Decisions = append(records.Decisions, projectstore.Decision{
		ID: "DEC-900", TopicKey: got.TopicKey, Body: "決めた"})
	next, err := SelectTopic(PhaseRequirements, records, completeness, nil, perspectives)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.TopicKey != "custom/PRS-002" {
		t.Fatalf("既決の観点が再び選ばれた・次へ移らない: %+v", next)
	}

	// 覆されていない決定で全観点が既決になれば、次の質問は無い。
	records.Decisions = append(records.Decisions, projectstore.Decision{
		ID: "DEC-901", TopicKey: next.TopicKey, Body: "決めた"})
	last, err := SelectTopic(PhaseRequirements, records, completeness, nil, perspectives)
	if err != nil {
		t.Fatal(err)
	}
	if last != nil {
		t.Fatalf("全観点が既決なのに論点が返った: %+v", last)
	}
}

// 未充足の章観点があるうちは章観点の必須項目が先（プロジェクト観点は走査の最後）。
func TestSelectTopicPrefersChapterItemsOverProjectPerspectives(t *testing.T) {
	perspectives := []SelectedPerspective{testProjectPerspective("PRS-001", "棚卸の差異処理")}

	got, err := SelectTopic(PhaseRequirements, Records{}, nil, nil, perspectives)
	if err != nil {
		t.Fatalf("論点を選べない: %v", err)
	}
	if got == nil {
		t.Fatal("論点が選ばれない")
	}
	if IsProjectTopicKey(got.TopicKey) {
		t.Fatalf("未充足の章観点よりプロジェクト観点が先に選ばれた: %+v", got)
	}
}

// プロジェクト観点でも追問の継続（同一論点の 2 回目）が成立する。
func TestSelectTopicContinuesProjectPerspective(t *testing.T) {
	perspectives := []SelectedPerspective{testProjectPerspective("PRS-001", "棚卸の差異処理")}
	last := &PresentedQuestion{TopicKey: "custom/PRS-001", Answered: true, FollowUpIndex: 1}

	got, err := SelectTopic(PhaseRequirements, Records{}, nil, last, perspectives)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TopicKey != "custom/PRS-001" {
		t.Fatalf("着手済みのプロジェクト観点が継続しない: %+v", got)
	}
	if got.Reason != ReasonFollowUp || got.FollowUpIndex != 2 {
		t.Errorf("継続の内容が違う: %+v", got)
	}
}

// プロジェクト観点は充足率・確定判定に影響しない。
func TestCompletenessIgnoresProjectPerspectives(t *testing.T) {
	records := Records{}
	before, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	records.Decisions = []projectstore.Decision{{ID: "DEC-900",
		TopicKey: "custom/PRS-001", Body: "決めた"}}
	after, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("章観点の件数が変わった: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Total != after[i].Total || before[i].Satisfied != after[i].Satisfied ||
			before[i].Percent != after[i].Percent {
			t.Errorf("%s: プロジェクト観点が充足率に影響した %+v → %+v",
				before[i].ChapterID, before[i], after[i])
		}
	}
	// 未登録・未消化のプロジェクト観点は確定をブロックしない。
	records.Requirements = []projectstore.Requirement{{ID: "FR-INV-001", Title: "在庫引当",
		Chapter: "functional-requirements", Status: projectstore.RequirementAgreed}}
	got := Confirmable(records)
	if !got.Confirmable {
		t.Errorf("未消化のプロジェクト観点が確定をブロックした: %+v（理由: %s）", got, got.Reason())
	}
}

// プロンプトへの注入行は論点キーと要旨を含む。
func TestPerspectiveLinesForProjectPerspectives(t *testing.T) {
	lines := PerspectiveLines([]SelectedPerspective{testProjectPerspective("PRS-003", "棚卸の差異処理")})
	if len(lines) != 1 {
		t.Fatalf("注入行の件数が違う: %+v", lines)
	}
	if !strings.Contains(lines[0], "custom/PRS-003") ||
		!strings.Contains(lines[0], ProjectPerspectiveDomainName) ||
		!strings.Contains(lines[0], "棚卸の差異処理の要旨") {
		t.Errorf("注入行の内容が違う: %q", lines[0])
	}
}

func TestIsProjectTopicKey(t *testing.T) {
	if !IsProjectTopicKey("custom/PRS-001") {
		t.Error("プロジェクト観点の論点キーと判定されない")
	}
	for _, key := range []string{"preset/inventory/stocktaking", "business-flow/main-flow", "custom", ""} {
		if IsProjectTopicKey(key) {
			t.Errorf("プロジェクト観点扱いされた: %q", key)
		}
	}
}

// 観点候補の重複指摘に使う既存観点の一覧を注入する。
func TestPerspectiveContextSection(t *testing.T) {
	preset, err := SelectedPerspectives([]string{"inventory"})
	if err != nil {
		t.Fatal(err)
	}
	perspectives := append(append([]SelectedPerspective{}, preset...),
		testProjectPerspective("PRS-001", "棚卸の差異処理"))

	got := perspectiveContextSection(perspectives)
	if !strings.Contains(got, "duplicate_of") {
		t.Errorf("重複指摘に使う旨が書かれていない: %q", got)
	}
	if !strings.Contains(got, "preset/inventory/") || !strings.Contains(got, "custom/PRS-001") {
		t.Errorf("プリセット・プロジェクトの双方が並んでいない: %q", got)
	}
	if !strings.Contains(got, ProjectPerspectiveDomainName) || !strings.Contains(got, "棚卸の差異処理の要旨") {
		t.Errorf("観点の内容が載っていない: %q", got)
	}
	if perspectiveContextSection(nil) != "" {
		t.Error("観点が無いのに節が出た")
	}
}

// 観点候補の duplicate_of は実在する観点に限る（実在しない指摘先は空へ倒し、候補は残す）。
func TestVerifyPerspectiveRefs(t *testing.T) {
	valid := perspectiveKeys([]SelectedPerspective{
		testProjectPerspective("PRS-001", "棚卸の差異処理"),
		{DomainID: "inventory", Perspective: Perspective{ID: "stocktaking"},
			TopicKey: "preset/inventory/stocktaking"},
	})
	if !valid["custom/PRS-001"] || !valid["PRS-001"] || !valid["preset/inventory/stocktaking"] {
		t.Fatalf("受け付けるキーの集合が違う: %+v", valid)
	}

	ex := &Extraction{PerspectiveCandidates: []PerspectiveCandidate{
		{Name: "候補1", Summary: "要旨", DuplicateOf: "custom/PRS-001"},
		{Name: "候補2", Summary: "要旨", DuplicateOf: "PRS-001"},
		{Name: "候補3", Summary: "要旨", DuplicateOf: "preset/inventory/stocktaking"},
		{Name: "候補4", Summary: "要旨", DuplicateOf: "PRS-999"},
		{Name: "候補5", Summary: "要旨"},
	}}
	if got := verifyPerspectiveRefs(ex, valid); got != 1 {
		t.Fatalf("外した件数が違う: %d（期待 1）", got)
	}
	if len(ex.PerspectiveCandidates) != 5 {
		t.Fatalf("候補が落ちた: %+v", ex.PerspectiveCandidates)
	}
	if ex.PerspectiveCandidates[3].DuplicateOf != "" {
		t.Errorf("実在しない指摘先が残った: %+v", ex.PerspectiveCandidates[3])
	}
	for i := 0; i < 3; i++ {
		if ex.PerspectiveCandidates[i].DuplicateOf == "" {
			t.Errorf("実在する指摘先が外された: %+v", ex.PerspectiveCandidates[i])
		}
	}
}

// lastRequirementChapter は要件定義フェーズの最後の章観点 ID を返す。
func lastRequirementChapter(t *testing.T) string {
	t.Helper()
	m, err := LoadMetaModel()
	if err != nil {
		t.Fatal(err)
	}
	phase, err := m.Phase(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(phase.Chapters) == 0 {
		t.Fatal("章観点が定義されていない")
	}
	return phase.Chapters[len(phase.Chapters)-1].ID
}
