package docgen

import (
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func testRecords() Records {
	return Records{
		Requirements: []projectstore.Requirement{
			{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
				Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
				Status: projectstore.RequirementDraft, Body: "受注確定時に在庫を引き当てること。",
				AcceptanceCriteria: []string{"3 秒以内"}, Decisions: []string{"DEC-001"},
				BlockedBy: []string{"ISS-001"}},
			{ID: "NFR-PF-001", Title: "応答性能", Chapter: "non-functional-requirements",
				Kind: projectstore.RequirementNonFunctional, Priority: projectstore.PriorityMust,
				Status: projectstore.RequirementDraft, Body: "1 秒以内に応答すること。"},
		},
		Decisions: []projectstore.Decision{
			{ID: "DEC-001", TopicKey: "functional-requirements/list", Body: "受注確定時に引き当てる。",
				DecidedAt: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC), Evidence: []string{"S-0001#utt-00002"}},
			{ID: "DEC-002", TopicKey: "background/current-state", Body: "現状は Excel 台帳。",
				DecidedAt: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC), Evidence: []string{"S-0001#utt-00001"},
				SupersededBy: "DEC-003"},
		},
		OpenIssues: []projectstore.OpenIssue{
			{ID: "ISS-001", Owner: "営業部 佐藤", Due: "2026-09-30", Status: projectstore.OpenIssueOpen,
				Body: "引当の単位をロットにするか", Evidence: []string{"S-0001#utt-00003"}},
			{ID: "ISS-002", Owner: "情シス", Status: projectstore.OpenIssueOpen,
				Body: "ブロック対象の無い論点", Evidence: []string{"S-0001#utt-00004"}},
		},
		Terms: []projectstore.Term{
			{Name: "在庫引当", NameEn: "Stock Allocation", Definition: "受注に対して在庫を確保すること。",
				Forbidden: []string{"引当て"}},
		},
	}
}

func testInput(t *testing.T, kindID string) AssembleInput {
	t.Helper()
	tmpl, err := LoadTemplate()
	if err != nil {
		t.Fatalf("テンプレート定義を読めない: %v", err)
	}
	doc, err := tmpl.Document(kindID)
	if err != nil {
		t.Fatal(err)
	}
	return AssembleInput{Document: doc, Records: testRecords(), TargetName: "在庫管理システム",
		GeneratedAt: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)}
}

// テンプレート定義が決めた構成と一致する（要件定義 13 文書＋共通 3・基本設計 8 文書）。
func TestTemplateMatchesDesign(t *testing.T) {
	tmpl, err := LoadTemplate()
	if err != nil {
		t.Fatalf("テンプレート定義を読めない: %v", err)
	}
	req, err := tmpl.Document(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Chapters) != 13 {
		t.Fatalf("要件定義書の文書数が違う: %d（期待 13 = 目次 + 12 章観点）", len(req.Chapters))
	}
	wantChapters := []string{
		"index", "background", "scope", "users", "business-flow", "use-cases",
		"functional-requirements", "non-functional-requirements", "domain-model",
		"data-requirements", "external-interfaces", "constraints", "risks-assumptions",
	}
	for i, want := range wantChapters {
		if req.Chapters[i].Chapter != want {
			t.Errorf("章 %d が違う: %s（期待 %s）", i, req.Chapters[i].Chapter, want)
		}
	}
	// 目次は機械組立て、章観点は AI 生成。
	if req.Chapters[0].IsGenerated() {
		t.Error("目次が AI 生成の対象になっている")
	}
	if len(req.GeneratedChapters()) != 12 {
		t.Errorf("AI 生成の対象章数が違う: %d", len(req.GeneratedChapters()))
	}

	design, err := tmpl.Document(projectstore.DocKindBasicDesign)
	if err != nil {
		t.Fatal(err)
	}
	if len(design.Chapters) != 8 {
		t.Fatalf("基本設計書の文書数が違う: %d（期待 8）", len(design.Chapters))
	}
	if len(tmpl.Common) != 3 {
		t.Fatalf("共通文書の数が違う: %d（期待 3 = 用語集・決定・未決）", len(tmpl.Common))
	}
	for _, c := range tmpl.Common {
		if c.IsGenerated() {
			t.Errorf("共通文書が AI 生成の対象になっている: %s", c.File)
		}
	}
	if _, err := tmpl.Document("unknown"); err == nil {
		t.Error("未定義の成果物種別が受理された")
	}
}

// 要件項目は chapter タグ、決定事項は論点キー、未決事項はブロック対象の章へ割当てる。
func TestAssign(t *testing.T) {
	in := testInput(t, projectstore.DocKindRequirements)
	got := Assign(in.Document, in.Records)

	byChapter := map[string]ChapterSources{}
	for _, s := range got.Sources {
		byChapter[s.Chapter.Chapter] = s
	}
	// 機械組立ての章は割当の対象外。
	if _, ok := byChapter["index"]; ok {
		t.Error("目次が割当の対象になっている")
	}
	fr := byChapter["functional-requirements"]
	if len(fr.Requirements) != 1 || fr.Requirements[0].ID != "FR-INV-001" {
		t.Errorf("要件項目が chapter タグの章へ割当てられない: %+v", fr.Requirements)
	}
	if len(fr.Decisions) != 1 || fr.Decisions[0].ID != "DEC-001" {
		t.Errorf("決定事項が論点キーの章へ割当てられない: %+v", fr.Decisions)
	}
	// 未決事項はブロック対象の要件項目の章へ。ブロック対象の無い ISS-002 はどの章にも載らない。
	if len(fr.OpenIssues) != 1 || fr.OpenIssues[0].ID != "ISS-001" {
		t.Errorf("未決事項がブロック対象の章へ割当てられない: %+v", fr.OpenIssues)
	}
	for chapter, s := range byChapter {
		for _, i := range s.OpenIssues {
			if i.ID == "ISS-002" {
				t.Errorf("ブロック対象の無い未決事項が章 %s に載った", chapter)
			}
		}
	}
	bg := byChapter["background"]
	if len(bg.Decisions) != 1 || bg.Decisions[0].ID != "DEC-002" {
		t.Errorf("背景の決定事項が割当てられない: %+v", bg.Decisions)
	}

	// 依存マップ（版履歴へ記録する値）。
	dep := got.DependencyMap()
	ids := dep["06-functional-requirements.md"]
	if len(ids) != 3 {
		t.Fatalf("依存マップの ID 数が違う: %+v", ids)
	}
	if ids[0] != "DEC-001" || ids[1] != "FR-INV-001" || ids[2] != "ISS-001" {
		t.Errorf("依存マップが昇順の ID 集合になっていない: %+v", ids)
	}
	all := got.AllSourceIDs()
	if len(all) != 5 {
		t.Errorf("全収載 ID が違う: %+v", all)
	}
	// 収載レコードの無い章は空として扱える。
	if !byChapter["use-cases"].IsEmpty() {
		t.Errorf("レコードの無い章が空と判定されない: %+v", byChapter["use-cases"])
	}
}

// 機械組立て文書（目次・用語集・決定事項リスト・未決事項リスト）が AI 呼び出しなしで組み立てられる。
func TestAssembleDocuments(t *testing.T) {
	in := testInput(t, projectstore.DocKindRequirements)

	index := AssembleIndex(in)
	for _, want := range []string{"01-business-context.md", "12-risks-assumptions.md", "../00-project/glossary.md", "表記規約"} {
		if !strings.Contains(index, want) {
			t.Errorf("目次に %q が無い:\n%s", want, index)
		}
	}

	glossary := AssembleGlossary(in)
	for _, want := range []string{"在庫引当", "Stock Allocation", "引当て"} {
		if !strings.Contains(glossary, want) {
			t.Errorf("用語集に %q が無い:\n%s", want, glossary)
		}
	}

	decisions := AssembleDecisions(in)
	for _, want := range []string{"DEC-001", "2026-08-27", "S-0001#utt-00002", "DEC-003 で置き換え"} {
		if !strings.Contains(decisions, want) {
			t.Errorf("決定事項リストに %q が無い:\n%s", want, decisions)
		}
	}

	issues := AssembleIssues(in)
	// 「誰が・いつまでに・何を」が揃うこと。
	for _, want := range []string{"ISS-001", "営業部 佐藤", "2026-09-30", "引当の単位", "FR-INV-001"} {
		if !strings.Contains(issues, want) {
			t.Errorf("未決事項リストに %q が無い:\n%s", want, issues)
		}
	}
	// 期限未設定は「未定」と明示する（空欄にしない）。
	if !strings.Contains(issues, "未定") {
		t.Errorf("期限未設定が明示されない:\n%s", issues)
	}

	// 冒頭メタ節を箇条書きで持つ（フロントマターを使わない）。
	for name, text := range map[string]string{"目次": index, "用語集": glossary, "決定": decisions, "未決": issues} {
		if !strings.Contains(text, "- 対象システム: 在庫管理システム") {
			t.Errorf("%s に冒頭メタが無い", name)
		}
		if strings.HasPrefix(text, "---") {
			t.Errorf("%s がフロントマター形式になっている", name)
		}
	}
}

// 空のプロジェクトでも機械組立てが成立する（空欄・推測での補完をしない）。
func TestAssembleWithNoRecords(t *testing.T) {
	in := testInput(t, projectstore.DocKindRequirements)
	in.Records = Records{}
	if !strings.Contains(AssembleDecisions(in), "（なし）") {
		t.Error("決定事項が空のときに明示されない")
	}
	if !strings.Contains(AssembleIssues(in), "（なし）") {
		t.Error("未決事項が空のときに明示されない")
	}
	if !strings.Contains(AssembleGlossary(in), "（未登録）") {
		t.Error("用語が空のときに明示されない")
	}
}

// 表の内容に改行・パイプが含まれても表が壊れない。
func TestAssembleEscapesTableCells(t *testing.T) {
	in := testInput(t, projectstore.DocKindRequirements)
	in.Records.Decisions[0].Body = "決定内容 | パイプつき\n2 行目は載せない"
	in.Records.OpenIssues[0].Owner = "営業部 | 佐藤"
	got := AssembleDecisions(in)

	var target string
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "| DEC-001") {
			target = line
		}
	}
	if target == "" {
		t.Fatalf("決定事項の行が見つからない:\n%s", got)
	}
	// 列区切りのパイプは 7 本（6 列）。本文中のパイプはエスケープされて列を増やさない。
	if got := strings.Count(target, "|") - strings.Count(target, "\\|"); got != 7 {
		t.Errorf("表の列数が崩れている（区切り %d 本）: %q", got, target)
	}
	if !strings.Contains(target, "\\|") {
		t.Errorf("本文中のパイプがエスケープされていない: %q", target)
	}
	if strings.Contains(target, "2 行目は載せない") {
		t.Errorf("本文の 2 行目が表へ混入している: %q", target)
	}

	issues := AssembleIssues(in)
	for _, line := range strings.Split(issues, "\n") {
		if strings.HasPrefix(line, "| ISS-001") {
			if n := strings.Count(line, "|") - strings.Count(line, "\\|"); n != 8 {
				t.Errorf("未決事項の表の列数が崩れている（区切り %d 本）: %q", n, line)
			}
		}
	}
}
