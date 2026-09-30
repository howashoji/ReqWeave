package dialogue

import (
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 根拠は当該資料の実在する行範囲だけを認める。
func TestValidImportRef(t *testing.T) {
	const lines = 120
	cases := map[string]bool{
		"IMP-001#L1-L3":     true,
		"IMP-001#L120-L120": true,
		"IMP-001#L1-L120":   true,
		"IMP-001#L0-L3":     false, // 行番号は 1 始まり
		"IMP-001#L5-L4":     false, // 範囲が逆転
		"IMP-001#L100-L121": false, // 末尾行を超える
		"IMP-002#L1-L3":     false, // 別の資料
		"IMP-1#L1-L3":       false, // ID の桁が違う
		"S-0001#utt-00001":  false, // 発話参照は取り込み分析の根拠にならない
		"QS-001#q-01":       false, // 回答参照も同様
		"IMP-001":           false, // 行範囲がない
	}
	for ref, want := range cases {
		if got := validImportRef(ref, "IMP-001", lines); got != want {
			t.Errorf("validImportRef(%q) = %v, want %v", ref, got, want)
		}
	}
}

// 実在しないレコードを指す重複・矛盾の指摘は提示しない（存在しない ID は除去）。
func TestVerifyRecordRefs(t *testing.T) {
	ex := &Extraction{
		Decisions: []DecisionCandidate{
			{TopicKey: "scope/in-scope", Body: "本文", DuplicateOf: "DEC-001"},
			{TopicKey: "scope/in-scope", Body: "本文", DuplicateOf: "DEC-999", SupersedesDecisionID: "DEC-998"},
		},
		OpenIssues:         []OpenIssueCandidate{{Topic: "論点", DuplicateOf: "ISS-999"}},
		RequirementUpdates: []RequirementCandidate{{Operation: OperationCreate, DuplicateOf: "FR-INV-001"}},
		Contradictions: []Contradiction{
			{WithDecisionID: "DEC-001", Description: "食い違い"},
			{WithDecisionID: "DEC-999", Description: "実在しない決定との矛盾"},
		},
	}
	existing := map[string]bool{"DEC-001": true, "FR-INV-001": true}

	dup, contra := verifyRecordRefs(ex, existing)

	if ex.Decisions[0].DuplicateOf != "DEC-001" {
		t.Errorf("実在する重複指摘が消えた: %q", ex.Decisions[0].DuplicateOf)
	}
	if ex.Decisions[1].DuplicateOf != "" || ex.Decisions[1].SupersedesDecisionID != "" {
		t.Errorf("実在しない指摘先が残っている: %+v", ex.Decisions[1])
	}
	if ex.OpenIssues[0].DuplicateOf != "" {
		t.Errorf("未決事項の実在しない重複指摘が残っている: %q", ex.OpenIssues[0].DuplicateOf)
	}
	if ex.RequirementUpdates[0].DuplicateOf != "FR-INV-001" {
		t.Errorf("要件項目の実在する重複指摘が消えた: %q", ex.RequirementUpdates[0].DuplicateOf)
	}
	if len(ex.Contradictions) != 1 || ex.Contradictions[0].WithDecisionID != "DEC-001" {
		t.Errorf("矛盾指摘の絞り込みが違う: %+v", ex.Contradictions)
	}
	if dup != 3 || contra != 1 {
		t.Errorf("除去件数が違う: duplicate=%d contradiction=%d", dup, contra)
	}
	// 候補自体は残す（担当者が新規として承認できる）。
	if len(ex.Decisions) != 2 || len(ex.OpenIssues) != 1 || len(ex.RequirementUpdates) != 1 {
		t.Error("指摘先の除去で候補そのものが消えた")
	}
}

// 注入する文脈は決定・未決の要約、要件項目の ID とタイトル、用語集。
// 対話履歴は注入しない。
func TestBuildMaterialContext(t *testing.T) {
	records := Records{
		Decisions: []projectstore.Decision{{ID: "DEC-001", TopicKey: "scope/in-scope",
			Body: "受注チャネルは EDI と Web。\n詳細は別途。"}},
		OpenIssues: []projectstore.OpenIssue{{ID: "ISS-001", Owner: "佐藤",
			Status: projectstore.OpenIssueOpen, Body: "与信限度額の決裁者"}},
		Requirements: []projectstore.Requirement{{ID: "FR-INV-001", Title: "在庫引当",
			Chapter: "functional-requirements", Body: "受注確定時に在庫を引き当てること。"}},
		Terms: []projectstore.Term{{Name: "引当", NameEn: "allocation", Definition: "在庫を確保すること"}},
	}
	meta := importer.Meta{ID: "IMP-001", Kind: importer.KindMinutes, SourceName: "議事録.docx",
		ImportedAt: time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)}

	got, labels := buildMaterialContext(records, meta)

	for _, want := range []string{"IMP-001", "議事録", "DEC-001", "受注チャネルは EDI と Web。",
		"ISS-001", "与信限度額の決裁者", "FR-INV-001", "在庫引当", "引当（allocation）"} {
		if !strings.Contains(got, want) {
			t.Errorf("注入文脈に %q が無い:\n%s", want, got)
		}
	}
	// 決定は結論 1 行の要約（2 行目以降は載せない）。
	if strings.Contains(got, "詳細は別途。") {
		t.Error("決定事項が要約形になっていない（全文が載っている）")
	}
	// 要件項目は ID とタイトルのみ（本文は載せない）。
	if strings.Contains(got, "受注確定時に在庫を引き当てること。") {
		t.Error("要件項目の本文が載っている（ID・タイトル一覧のはず）")
	}
	// 対話履歴は注入しない。
	if strings.Contains(got, "これまでの対話") || strings.Contains(got, "[担当者]") {
		t.Errorf("対話履歴が注入されている:\n%s", got)
	}
	for _, want := range []string{"IMP-001", LabelDecisions, LabelOpenIssues, LabelRequirements, LabelTerms} {
		if !contains(labels, want) {
			t.Errorf("送信文脈の内訳ラベルに %q が無い: %v", want, labels)
		}
	}
	if contains(labels, LabelHistory) {
		t.Errorf("対話履歴のラベルが付いている: %v", labels)
	}
}

// 出力は対話の抽出と同一スキーマ + perspective_candidates。
// 対話抽出・回答取込のスキーマには含めない。
func TestMaterialAnalysisSchemaAddsPerspectives(t *testing.T) {
	material := string(SchemaForMode(ModeMaterialAnalysis))
	if !strings.Contains(material, "perspective_candidates") {
		t.Fatalf("取り込み分析のスキーマに観点候補が無い: %s", material)
	}
	for _, key := range []string{"decisions", "open_issues", "requirement_updates",
		"term_candidates", "contradictions"} {
		if !strings.Contains(material, key) {
			t.Errorf("取り込み分析のスキーマに %q が無い（対話の抽出と同一スキーマのはず）", key)
		}
	}
	for _, mode := range []string{ModeExtraction, ModeImportAnalysis} {
		if strings.Contains(string(SchemaForMode(mode)), "perspective_candidates") {
			t.Errorf("%s のスキーマに観点候補が含まれている（取り込み分析のみのはず）", mode)
		}
	}
}

// 観点候補は名称と要旨が必須。
func TestParseExtractionValidatesPerspectiveCandidates(t *testing.T) {
	ok := `{"decisions":[],"open_issues":[],"requirement_updates":[],"term_candidates":[],
		"contradictions":[],"perspective_candidates":[{"name":"棚卸の差異処理","summary":"差異の承認経路"}]}`
	got, err := ParseExtraction(ok)
	if err != nil {
		t.Fatalf("観点候補つきの応答を解釈できない: %v", err)
	}
	if len(got.PerspectiveCandidates) != 1 || got.PerspectiveCandidates[0].Name != "棚卸の差異処理" {
		t.Fatalf("観点候補が取れていない: %+v", got.PerspectiveCandidates)
	}
	bad := `{"decisions":[],"open_issues":[],"requirement_updates":[],"term_candidates":[],
		"contradictions":[],"perspective_candidates":[{"name":"","summary":"要旨"}]}`
	if _, err := ParseExtraction(bad); err == nil {
		t.Error("名称が空の観点候補が受理された")
	}
}

// 対話抽出・回答取込の経路では観点候補を捨てる（出力契約の一方向の担保）。
func TestDropPerspectiveCandidates(t *testing.T) {
	ex := &Extraction{PerspectiveCandidates: []PerspectiveCandidate{{Name: "観点", Summary: "要旨"}}}
	if ex.IsEmpty() {
		t.Error("観点候補だけの抽出結果が空扱いになっている")
	}
	ex.DropPerspectiveCandidates()
	if len(ex.PerspectiveCandidates) != 0 || !ex.IsEmpty() {
		t.Errorf("観点候補が残っている: %+v", ex.PerspectiveCandidates)
	}
}

// 分割送信の応答は 1 つの候補集合へまとまる（重複は統合せず並べる）。
func TestMergeChunkExtractions(t *testing.T) {
	run := &ImportChunkRun{
		Plan: &ImportChunkPlan{Chunks: []ImportChunk{{Index: 1}, {Index: 2}, {Index: 3}}},
		results: map[int]ImportChunkResult{
			1: {Index: 1, Body: `{"decisions":[{"topic_key":"scope/in-scope","body":"A","rationale":"","evidence_refs":["IMP-001#L1-L2"]}],
				"open_issues":[],"requirement_updates":[],"term_candidates":[],"contradictions":[]}`},
			2: {Index: 2, Body: `これは JSON ではない`},
			3: {Index: 3, Body: `{"decisions":[{"topic_key":"scope/in-scope","body":"B","rationale":"","evidence_refs":["IMP-001#L9-L9"]}],
				"open_issues":[{"topic":"論点","evidence_refs":["IMP-001#L9-L9"]}],
				"requirement_updates":[],"term_candidates":[],"contradictions":[],
				"perspective_candidates":[{"name":"観点","summary":"要旨"}]}`},
		},
	}

	merged, causes := mergeChunkExtractions(run)

	if len(merged.Decisions) != 2 {
		t.Errorf("全チャンクの決定候補がまとまっていない: %+v", merged.Decisions)
	}
	if len(merged.OpenIssues) != 1 || len(merged.PerspectiveCandidates) != 1 {
		t.Errorf("候補の併合が漏れている: %+v", merged)
	}
	if len(causes) != 1 || !strings.Contains(causes[0], "2/3 部") {
		t.Errorf("解釈できなかったチャンクが原因として残っていない: %v", causes)
	}
}
