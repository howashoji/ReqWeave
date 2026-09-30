package dialogue

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 初期収録の 4 領域と各観点が欠落なく同梱されていること。
//
// 期待値は定義データとは別にテストへ書き起こしたものであり、
// 定義データ側を減らすとこのテストが落ちる。
func TestDomainPresetsMatchDesignTable(t *testing.T) {
	want := map[string][]string{
		"sales": {"受注チャネルと受注形態", "価格・値引きとその承認", "与信管理",
			"請求・入金消込", "出荷・納品と売上計上基準", "返品・キャンセル処理"},
		"inventory": {"在庫管理単位", "入出庫と引当のルール", "棚卸と差異処理",
			"倉庫・ロケーション管理", "発注点・安全在庫"},
		"accounting": {"勘定科目と仕訳の発生点", "月次・年次の締め処理", "消費税・端数処理の規則",
			"債権・債務管理", "既存会計システムとの連携範囲"},
		"workflow": {"承認経路", "代理承認・不在時の扱い", "差し戻し・取り下げ",
			"権限と職務分掌", "申請書式と添付", "監査証跡の要否"},
	}

	presets, err := LoadDomainPresets()
	if err != nil {
		t.Fatalf("プリセット定義を読み込めない: %v", err)
	}
	if len(presets.Domains) != len(want) {
		t.Fatalf("業務領域の件数が違う: %d 件", len(presets.Domains))
	}
	for _, d := range presets.Domains {
		names, ok := want[d.ID]
		if !ok {
			t.Errorf("想定外の業務領域: %q", d.ID)
			continue
		}
		delete(want, d.ID)
		if len(d.Perspectives) != len(names) {
			t.Errorf("%s の観点数が違う: %d 件（期待 %d 件）", d.ID, len(d.Perspectives), len(names))
			continue
		}
		for i, v := range d.Perspectives {
			if v.Name != names[i] {
				t.Errorf("%s の観点[%d] = %q, want %q", d.ID, i, v.Name, names[i])
			}
			if strings.TrimSpace(v.Topics) == "" {
				t.Errorf("%s/%s に典型論点の要旨が無い", d.ID, v.ID)
			}
		}
	}
	if len(want) != 0 {
		t.Errorf("欠けている業務領域: %v", want)
	}
}

// 観点の関連章観点は実在するメタモデルの章観点であること（走査で連結できない定義を作らない）。
func TestPresetChaptersExistInMetaModel(t *testing.T) {
	presets, err := LoadDomainPresets()
	if err != nil {
		t.Fatalf("プリセット定義を読み込めない: %v", err)
	}
	m, err := LoadMetaModel()
	if err != nil {
		t.Fatalf("メタモデル定義を読み込めない: %v", err)
	}
	phase, err := m.Phase(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, c := range phase.Chapters {
		known[c.ID] = true
	}
	for _, d := range presets.Domains {
		for _, v := range d.Perspectives {
			if !known[v.Chapter] {
				t.Errorf("%s/%s の関連章観点が存在しない: %q", d.ID, v.ID, v.Chapter)
			}
		}
	}
}

// 論点キーは preset/<領域ID>/<観点ID>。
func TestPresetTopicKey(t *testing.T) {
	presets, err := LoadDomainPresets()
	if err != nil {
		t.Fatal(err)
	}
	d, ok := presets.Domain("inventory")
	if !ok {
		t.Fatal("在庫領域が引けない")
	}
	key := d.TopicKey("stocktaking")
	if key != "preset/inventory/stocktaking" {
		t.Fatalf("論点キーの形式が違う: %q", key)
	}
	if !IsPresetTopicKey(key) {
		t.Error("プリセットの論点キーと判定されない")
	}
	if IsPresetTopicKey("business-flow/main-flow") {
		t.Error("章観点の論点キーがプリセット扱いされた")
	}
	if _, ok := presets.Domain("hr"); ok {
		t.Error("未定義の領域が引けてしまった")
	}
}

// 選択した領域の観点だけが対象になる（未選択・未知の領域は無視する）。
func TestSelectedPerspectives(t *testing.T) {
	got, err := SelectedPerspectives([]string{"inventory", "hr"})
	if err != nil {
		t.Fatalf("観点を取得できない: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("在庫領域の観点数が違う（未知の領域が混ざっていないか）: %d", len(got))
	}
	for _, v := range got {
		if v.DomainID != "inventory" || !strings.HasPrefix(v.TopicKey, "preset/inventory/") {
			t.Errorf("観点の帰属が違う: %+v", v)
		}
	}
	none, err := SelectedPerspectives(nil)
	if err != nil || len(none) != 0 {
		t.Errorf("未選択で観点が返った: %+v %v", none, err)
	}

	// プロンプトへの注入行は論点キーと要旨を含む。
	lines := PerspectiveLines(got)
	if len(lines) != len(got) {
		t.Fatalf("注入行の件数が違う: %d", len(lines))
	}
	if !strings.Contains(lines[0], "preset/inventory/") || !strings.Contains(lines[0], "在庫") {
		t.Errorf("注入行の内容が違う: %q", lines[0])
	}
}

// 選択した領域の観点が質問生成の対象論点に加わる。
func TestSelectTopicIncludesPresetPerspectives(t *testing.T) {
	perspectives, err := SelectedPerspectives([]string{"inventory"})
	if err != nil {
		t.Fatal(err)
	}
	// 業務フロー章までを充足させ、プリセット観点だけが残る状態を作る。
	records, completeness := satisfiedThrough(t, "business-flow")

	got, err := SelectTopic(PhaseRequirements, records, completeness, nil, perspectives)
	if err != nil {
		t.Fatalf("論点を選べない: %v", err)
	}
	if got == nil {
		t.Fatal("プリセット観点が論点として選ばれない")
	}
	if !IsPresetTopicKey(got.TopicKey) {
		t.Fatalf("プリセット観点より先に章観点が選ばれた: %+v", got)
	}
	if got.Reason != ReasonPreset {
		t.Errorf("選定理由が違う: %q", got.Reason)
	}
	if got.ChapterID != "business-flow" {
		t.Errorf("関連章観点が引き継がれていない: %q", got.ChapterID)
	}

	// プリセット観点の論点キーも既決判定の対象。
	records.Decisions = append(records.Decisions, projectstore.Decision{
		ID: "DEC-900", TopicKey: got.TopicKey, Body: "決めた"})
	next, err := SelectTopic(PhaseRequirements, records, completeness, nil, perspectives)
	if err != nil {
		t.Fatal(err)
	}
	if next != nil && next.TopicKey == got.TopicKey {
		t.Errorf("既決のプリセット観点が再び選ばれた: %+v", next)
	}
}

// 業務領域を選択しないプロジェクトでも対話は成立する。
func TestSelectTopicWithoutPresets(t *testing.T) {
	records, completeness := satisfiedThrough(t, "business-flow")

	got, err := SelectTopic(PhaseRequirements, records, completeness, nil, nil)
	if err != nil {
		t.Fatalf("未選択で論点を選べない: %v", err)
	}
	if got == nil {
		t.Fatal("未選択で次の論点が無くなった（章観点だけで成立するはず）")
	}
	if IsPresetTopicKey(got.TopicKey) {
		t.Errorf("未選択なのにプリセット観点が選ばれた: %+v", got)
	}
}

// プリセット観点は充足率の分母に加えない。
func TestCompletenessIgnoresPresetPerspectives(t *testing.T) {
	records := Records{}
	before, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	// プリセット観点を決めても章観点の必須項目数・充足率は変わらない。
	records.Decisions = []projectstore.Decision{{ID: "DEC-900",
		TopicKey: "preset/inventory/stocktaking", Body: "決めた"}}
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
			t.Errorf("%s: プリセット観点が充足率に影響した %+v → %+v",
				before[i].ChapterID, before[i], after[i])
		}
	}
	// 未消化のプリセット観点は確定をブロックしない。
	// 合意済みの要件項目が 1 件あり未決事項が無ければ、プリセット観点が未消化でも確定できる。
	records.Requirements = []projectstore.Requirement{{ID: "FR-INV-001", Title: "在庫引当",
		Chapter: "functional-requirements", Status: projectstore.RequirementAgreed}}
	got := Confirmable(records)
	if !got.Confirmable {
		t.Errorf("未消化のプリセット観点が確定をブロックした: %+v（理由: %s）", got, got.Reason())
	}
}

// satisfiedThrough は指定の章観点までを充足させたレコードと充足率を返す（テスト用の下ごしらえ）。
func satisfiedThrough(t *testing.T, lastChapter string) (Records, []ChapterCompleteness) {
	t.Helper()
	m, err := LoadMetaModel()
	if err != nil {
		t.Fatal(err)
	}
	phase, err := m.Phase(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	var records Records
	n := 0
	for _, c := range phase.Chapters {
		for _, item := range c.Items {
			n++
			records.Decisions = append(records.Decisions, projectstore.Decision{
				ID: "DEC-" + string(rune('0'+n%10)), TopicKey: c.TopicKey(item.ID), Body: "決めた"})
		}
		if c.ID == lastChapter {
			break
		}
	}
	completeness, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	return records, completeness
}

// 定義データはアプリ同梱（Go embed）で、
// 読み込みに外部通信もプロジェクト外のファイルも要らない。
//
// 作業ディレクトリを空の一時ディレクトリへ移しても読み込めることで、
// ファイル依存が無いことを確認する（外部通信の禁止は depcheck の
// outbound-network が dialogue パッケージの import で機械検知する）。
func TestDomainPresetsAreEmbedded(t *testing.T) {
	if len(presetsYAML) == 0 {
		t.Fatal("プリセット定義が同梱されていない")
	}
	t.Chdir(t.TempDir())

	presets, err := LoadDomainPresets()
	if err != nil {
		t.Fatalf("同梱データだけで読み込めない: %v", err)
	}
	if len(presets.Domains) != 4 {
		t.Errorf("同梱データの領域数が違う: %d", len(presets.Domains))
	}
}

// 定義データの整合検査（同梱データの取り違えを読み込み時に落とす）。
func TestDomainPresetsValidate(t *testing.T) {
	cases := map[string]DomainPresets{
		"領域IDが空": {Domains: []DomainPreset{{Name: "販売",
			Perspectives: []Perspective{{ID: "a", Name: "観点", Chapter: "scope"}}}}},
		"領域IDの重複": {Domains: []DomainPreset{
			{ID: "sales", Name: "販売", Perspectives: []Perspective{{ID: "a", Name: "観点", Chapter: "scope"}}},
			{ID: "sales", Name: "販売2", Perspectives: []Perspective{{ID: "b", Name: "観点", Chapter: "scope"}}}}},
		"観点が無い": {Domains: []DomainPreset{{ID: "sales", Name: "販売"}}},
		"関連章観点が空": {Domains: []DomainPreset{{ID: "sales", Name: "販売",
			Perspectives: []Perspective{{ID: "a", Name: "観点"}}}}},
		"観点IDの重複": {Domains: []DomainPreset{{ID: "sales", Name: "販売",
			Perspectives: []Perspective{
				{ID: "a", Name: "観点1", Chapter: "scope"},
				{ID: "a", Name: "観点2", Chapter: "scope"}}}}},
	}
	for name, p := range cases {
		if err := p.validate(); err == nil {
			t.Errorf("%s: 不正な定義が受理された", name)
		}
	}
	ok := DomainPresets{Domains: []DomainPreset{{ID: "sales", Name: "販売",
		Perspectives: []Perspective{{ID: "a", Name: "観点", Chapter: "scope"}}}}}
	if err := ok.validate(); err != nil {
		t.Errorf("正しい定義が拒否された: %v", err)
	}
}
