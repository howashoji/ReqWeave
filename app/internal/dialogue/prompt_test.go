package dialogue

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// システムプロンプトは 5 節を固定順で含む。
func TestBuildSystemPromptSections(t *testing.T) {
	got, err := BuildSystemPrompt(SystemPromptInput{
		Phase: PhaseRequirements, Effort: aiprovider.EffortStandard, Mode: ModeQuestion})
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	sections := []string{"## 1. 役割", "## 2. メタモデル定義", "## 3. 対話規律", "## 4. エフォート制御値", "## 5. 出力契約"}
	pos := -1
	for _, s := range sections {
		i := strings.Index(got, s)
		if i < 0 {
			t.Fatalf("節が無い: %s\n%s", s, got)
		}
		if i <= pos {
			t.Errorf("節の順序が違う: %s", s)
		}
		pos = i
	}
	// 2. メタモデル定義には章観点と論点キーが入る。
	if !strings.Contains(got, "background/current-state") {
		t.Error("論点キーが注入されていない")
	}
	// 3. 対話規律。
	for _, want := range []string{"1 件ずつ", "背景説明", "曖昧語", "推測で補完しません", "再質問しません"} {
		if !strings.Contains(got, want) {
			t.Errorf("対話規律に %q が無い", want)
		}
	}
	if _, err := BuildSystemPrompt(SystemPromptInput{
		Phase: PhaseRequirements, Effort: aiprovider.EffortStandard, Mode: "unknown"}); err == nil {
		t.Error("用途が不正なプロンプトが組み立てられた")
	}
}

// エフォート段階の実値（追問上限 低=1 / 標準=3 / 高=5）が注入される。
func TestSystemPromptEffortControls(t *testing.T) {
	cases := map[aiprovider.Effort]string{
		aiprovider.EffortLow:      "最大 1 往復",
		aiprovider.EffortStandard: "最大 3 往復",
		aiprovider.EffortHigh:     "最大 5 往復",
	}
	for effort, want := range cases {
		got, err := BuildSystemPrompt(SystemPromptInput{
			Phase: PhaseRequirements, Effort: effort, Mode: ModeQuestion})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s: 追問上限が注入されていない（期待 %q）", effort, want)
		}
	}
	// 低は受け入れ条件の具体化追問を行わない。
	low, _ := BuildSystemPrompt(SystemPromptInput{Phase: PhaseRequirements,
		Effort: aiprovider.EffortLow, Mode: ModeQuestion})
	if !strings.Contains(low, "具体化追問は行わない") {
		t.Errorf("低の制御値が違う:\n%s", low)
	}
	if c := ControlsFor("unknown"); c.FollowUpLimit != 3 {
		t.Errorf("未知の段階が既定（標準）にならない: %+v", c)
	}
}

// 用途で出力契約が切り替わる。
//
// スキーマ本体はプロンプトへ書かず ChatRequest.ResponseSchema で渡す。
// プロンプトに残るのは、スキーマだけでは表せない埋め方の規則（どの ID を入れるか等）のみ。
func TestSystemPromptOutputContract(t *testing.T) {
	q, _ := BuildSystemPrompt(SystemPromptInput{Phase: PhaseRequirements,
		Effort: aiprovider.EffortStandard, Mode: ModeQuestion})
	if !strings.Contains(q, "論点キー:") || strings.Contains(q, "requirement_updates") {
		t.Errorf("質問生成の出力契約が違う:\n%s", q)
	}
	e, _ := BuildSystemPrompt(SystemPromptInput{Phase: PhaseRequirements,
		Effort: aiprovider.EffortStandard, Mode: ModeExtraction})
	if !strings.Contains(e, "evidence_refs") {
		t.Errorf("抽出の埋め方の規則が無い:\n%s", e)
	}
}

// 回帰テスト:
// 構造化出力のスキーマ本体をシステムプロンプトへ埋め込まない（二重管理の禁止）。
//
// 埋め込みとプロバイダ指定の両方にスキーマがあると、片方だけ更新したときに食い違う。
// スキーマの供給経路は SchemaForMode → ChatRequest.ResponseSchema の 1 本だけにする。
func TestSystemPromptDoesNotEmbedSchemaBody(t *testing.T) {
	// 埋め方の規則は個々のキー名に言及してよい（例:「contradictions に出し」）。
	// 禁じるのは**スキーマ本体そのもの**の埋め込みなので、定義文字列で突き合わせる。
	for _, mode := range []string{ModeQuestion, ModeExtraction, ModeImportAnalysis, ModeQuestionnaire} {
		got, err := BuildSystemPrompt(SystemPromptInput{Phase: PhaseRequirements,
			Effort: aiprovider.EffortStandard, Mode: mode})
		if err != nil {
			t.Fatalf("%s: プロンプトを組み立てられない: %v", mode, err)
		}
		if strings.Contains(got, "```json") {
			t.Errorf("%s: プロンプトに JSON スキーマのコードブロックが残っている:\n%s", mode, got)
		}
		if strings.Contains(got, ExtractionSchemaJSON) {
			t.Errorf("%s: プロンプトに抽出スキーマ本体が埋め込まれている", mode)
		}
		if strings.Contains(got, QuestionnaireSchemaJSON) {
			t.Errorf("%s: プロンプトに質問票スキーマ本体が埋め込まれている", mode)
		}
	}
}

// 構造化出力を使うモードだけがスキーマを持ち、質問生成は非構造化のまま。
// スキーマは**妥当な JSON Schema**であること（プロバイダへそのまま渡るため）。
func TestSchemaForMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want string // スキーマに含まれるべきキー。"" = スキーマなし
	}{
		{ModeQuestion, ""},
		{ModeExtraction, "requirement_updates"},
		{ModeImportAnalysis, "requirement_updates"},
		{ModeQuestionnaire, "questions"},
		{"unknown", ""},
	} {
		got := SchemaForMode(tc.mode)
		if tc.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: 非構造化のはずがスキーマを返した: %s", tc.mode, got)
			}
			continue
		}
		if len(got) == 0 {
			t.Fatalf("%s: スキーマが空", tc.mode)
		}
		var m map[string]any
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatalf("%s: スキーマが JSON として不正: %v", tc.mode, err)
		}
		if m["type"] != "object" {
			t.Errorf("%s: 最上位が type: object の JSON Schema でない: %v", tc.mode, m["type"])
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: JSON Schema の properties が無い: %v", tc.mode, m)
		}
		if _, ok := props[tc.want]; !ok {
			t.Errorf("%s: スキーマの properties に %q が無い: %v", tc.mode, tc.want, keysOf(props))
		}
	}
}

// 出力例の形（{"decisions": [{...}]}）ではなく JSON Schema であることを固定する。
// 3 社の構造化出力機能はスキーマとして検証するため、出力例の形は受理されない。
func TestSchemasAreJSONSchema(t *testing.T) {
	for name, raw := range map[string]string{
		"extraction":    ExtractionSchemaJSON,
		"questionnaire": QuestionnaireSchemaJSON,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("%s: JSON として不正: %v", name, err)
		}
		assertSchemaNode(t, name, m)
	}
}

// assertSchemaNode は JSON Schema のノードを再帰的に検証する
// （オブジェクトは properties を、配列は items を持ち、各プロパティが型を宣言していること）。
func assertSchemaNode(t *testing.T, path string, node map[string]any) {
	t.Helper()
	typ, hasType := node["type"]
	if !hasType {
		if _, hasEnum := node["enum"]; !hasEnum {
			t.Errorf("%s: type も enum も無い（スキーマとして型を宣言していない）: %v", path, keysOf(node))
		}
		return
	}
	switch typ {
	case "object":
		props, ok := node["properties"].(map[string]any)
		if !ok || len(props) == 0 {
			t.Errorf("%s: type: object なのに properties が無い", path)
			return
		}
		for name, child := range props {
			m, ok := child.(map[string]any)
			if !ok {
				t.Errorf("%s.%s: プロパティ定義がオブジェクトでない: %v", path, name, child)
				continue
			}
			assertSchemaNode(t, path+"."+name, m)
		}
	case "array":
		items, ok := node["items"].(map[string]any)
		if !ok {
			t.Errorf("%s: type: array なのに items が無い", path)
			return
		}
		assertSchemaNode(t, path+"[]", items)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// 基本設計フェーズでは根拠要件 ID と選択肢の提示を規律に含める。
func TestSystemPromptBasicDesignDiscipline(t *testing.T) {
	got, err := BuildSystemPrompt(SystemPromptInput{Phase: PhaseBasicDesign,
		Effort: aiprovider.EffortStandard, Mode: ModeQuestion})
	if err != nil {
		t.Fatalf("基本設計フェーズのプロンプトを組み立てられない: %v", err)
	}
	if !strings.Contains(got, "要件項目 ID") || !strings.Contains(got, "トレードオフ") {
		t.Errorf("基本設計フェーズの規律が無い:\n%s", got)
	}
	if !strings.Contains(got, "基本設計を対話で進める") {
		t.Errorf("役割定義がフェーズに追随していない:\n%s", got)
	}
}

// 注入する文脈は 7 種のみ。内訳ラベルを監査へ渡す。
func TestBuildContextIncludesOnlyDefinedSources(t *testing.T) {
	records := Records{
		Decisions: []projectstore.Decision{{ID: "DEC-001", TopicKey: "scope/in-scope",
			Body: "受注チャネルは EDI と Web。\n詳細は別途。", Evidence: []string{"S-0001#utt-00001"}}},
		OpenIssues: []projectstore.OpenIssue{{ID: "ISS-001", Owner: "佐藤", Due: "2026-09-30",
			Status: projectstore.OpenIssueOpen, Body: "与信限度額の決裁者"}},
		Requirements: []projectstore.Requirement{{ID: "FR-INV-001", Title: "在庫引当",
			Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
			Priority: projectstore.PriorityMust, Status: projectstore.RequirementDraft,
			Body: "受注確定時に在庫を引き当てること。", AcceptanceCriteria: []string{"3 秒以内"},
			BlockedBy: []string{"ISS-001"}}},
		Terms: []projectstore.Term{{Name: "在庫引当", NameEn: "Stock Allocation", Definition: "受注に在庫を確保すること。"}},
	}
	completeness, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	text, labels := BuildContext(ContextInput{
		Records: records, Completeness: completeness, CurrentChapter: "functional-requirements",
	})

	for _, want := range []string{"DEC-001", "ISS-001", "FR-INV-001", "在庫引当", "決める人: 佐藤", "ブロック対象: FR-INV-001"} {
		if !strings.Contains(text, want) {
			t.Errorf("文脈に %q が無い:\n%s", want, text)
		}
	}
	wantLabels := map[string]bool{LabelCompleteness: true, LabelDecisions: true,
		LabelOpenIssues: true, LabelRequirements: true, LabelTerms: true}
	for _, l := range labels {
		if !wantLabels[l] {
			t.Errorf("想定外の文脈ラベル: %s", l)
		}
		delete(wantLabels, l)
	}
	if len(wantLabels) != 0 {
		t.Errorf("欠けている文脈ラベル: %v", wantLabels)
	}
	// 現在論点でない決定事項は結論 1 行の要約形（全文を載せない）。
	if strings.Contains(text, "詳細は別途") {
		t.Errorf("要約形になっていない:\n%s", text)
	}
	// 当該論点の継続時は全文を載せる。
	full, _ := BuildContext(ContextInput{Records: records, CurrentTopicKey: "scope/in-scope"})
	if !strings.Contains(full, "詳細は別途") {
		t.Errorf("継続中の論点で全文が載っていない:\n%s", full)
	}
}

// 外部へ送信しないもの（キー・設定・端末情報）を載せる経路が無い。
func TestContextInputHasNoForbiddenSources(t *testing.T) {
	forbidden := []string{"apikey", "secret", "token", "credential", "settings", "device", "machine", "filepath"}
	for _, name := range structFieldNames(ContextInput{}) {
		lower := strings.ToLower(name)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("ContextInput に送信禁止データを載せうるフィールドがある: %s", name)
			}
		}
	}
}

// 基本設計フェーズでは合意済みの要件項目を根拠として注入する。
func TestBuildContextInjectsRequirementsInBasicDesign(t *testing.T) {
	records := Records{Requirements: []projectstore.Requirement{
		{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
			Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
			Status: projectstore.RequirementAgreed, Body: "受注確定時に在庫を引き当てること。",
			AcceptanceCriteria: []string{"3 秒以内"}},
		{ID: "FR-INV-002", Title: "未合意の要件", Chapter: "functional-requirements",
			Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
			Status: projectstore.RequirementDraft, Body: "検討中。"},
	}}

	// 要件定義フェーズでは章観点で絞る（基本設計の章観点には載らない）。
	text, _ := BuildContext(ContextInput{Phase: PhaseRequirements, Records: records,
		CurrentChapter: "architecture"})
	if strings.Contains(text, "FR-INV-001") {
		t.Errorf("要件定義フェーズで章観点外の要件が載った:\n%s", text)
	}

	// 基本設計フェーズでは合意済みの要件項目を要約形で載せる。
	text, labels := BuildContext(ContextInput{Phase: PhaseBasicDesign, Records: records,
		CurrentChapter: "architecture"})
	if !strings.Contains(text, "FR-INV-001") || !strings.Contains(text, "3 秒以内") {
		t.Errorf("基本設計フェーズで根拠要件が載らない:\n%s", text)
	}
	if strings.Contains(text, "FR-INV-002") {
		t.Errorf("未合意の要件項目が載った:\n%s", text)
	}
	var hasLabel bool
	for _, l := range labels {
		if l == LabelRequirements {
			hasLabel = true
		}
	}
	if !hasLabel {
		t.Errorf("文脈ラベルに要件項目が含まれない: %+v", labels)
	}
}
