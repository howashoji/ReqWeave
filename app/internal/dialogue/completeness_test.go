package dialogue

import (
	"reflect"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func decision(topicKey string) projectstore.Decision {
	return projectstore.Decision{ID: "DEC-001", TopicKey: topicKey, Body: "本文",
		Evidence: []string{"S-0001#utt-00001"}}
}

// メタモデル定義は 12 章観点（要件定義フェーズ）を定義順で持つ。
func TestMetaModelMatchesDesign(t *testing.T) {
	m, err := LoadMetaModel()
	if err != nil {
		t.Fatalf("メタモデル定義を読めない: %v", err)
	}
	p, err := m.Phase(PhaseRequirements)
	if err != nil {
		t.Fatal(err)
	}
	// 要件定義フェーズの章観点と、その走査順（章観点の並び）。
	want := []string{
		"background", "scope", "users", "business-flow", "use-cases",
		"functional-requirements", "non-functional-requirements", "domain-model",
		"data-requirements", "external-interfaces", "constraints", "risks-assumptions",
	}
	if len(p.Chapters) != len(want) {
		t.Fatalf("章観点の数が違う: %d（期待 %d）", len(p.Chapters), len(want))
	}
	for i, id := range want {
		if p.Chapters[i].ID != id {
			t.Errorf("章観点 %d が違う: %s（期待 %s）", i, p.Chapters[i].ID, id)
		}
		if len(p.Chapters[i].Items) == 0 {
			t.Errorf("章観点 %s に必須項目がない", id)
		}
	}
	// 「各 FR に受け入れ条件」は要件項目の acceptance_criteria で判定する。
	fr, err := chapterByID(p, "functional-requirements")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range fr.Items {
		if item.Condition.Kind == CondRequirementCriteria {
			found = true
		}
	}
	if !found {
		t.Error("機能要件に受け入れ条件の判定条件がない")
	}
	if _, err := m.Phase("unknown-phase"); err == nil {
		t.Error("未定義のフェーズが受理された")
	}
}

func chapterByID(p *Phase, id string) (*Chapter, error) {
	for i := range p.Chapters {
		if p.Chapters[i].ID == id {
			return &p.Chapters[i], nil
		}
	}
	return nil, errNotFound(id)
}

type errNotFound string

func (e errNotFound) Error() string { return "章観点が見つかりません: " + string(e) }

// 充足率は 100 ×（充足 ÷ 総数）で小数点以下切り捨て。
func TestCompletenessPercentTruncates(t *testing.T) {
	// 業務背景は必須項目 2 件。1 件だけ充足させると 50%。
	got := chapterOf(t, Records{Decisions: []projectstore.Decision{decision("background/current-state")}}, "background")
	if got.Satisfied != 1 || got.Total != 2 || got.Percent != 50 {
		t.Fatalf("充足率が違う: %+v", got)
	}

	// 利用者は「区分 2 件以上」と「権限記述」の 2 項目。区分 1 件では未充足のまま 0%。
	users := chapterOf(t, Records{Decisions: []projectstore.Decision{decision("users/roles")}}, "users")
	if users.Satisfied != 0 || users.Percent != 0 {
		t.Errorf("件数条件（2 件以上）が効いていない: %+v", users)
	}
	// 3 項目のうち 2 項目充足 → 66.66…% は切り捨てて 66（四捨五入なら 67 になる）。
	nfr := chapterOf(t, Records{Decisions: []projectstore.Decision{
		decision("non-functional-requirements/performance"),
		decision("non-functional-requirements/security"),
	}}, "non-functional-requirements")
	if nfr.Total != 3 || nfr.Satisfied != 2 || nfr.Percent != 66 {
		t.Errorf("切り捨てになっていない: %+v", nfr)
	}
	// 1 項目のみなら 33（33.33… の切り捨て）。
	one := chapterOf(t, Records{Decisions: []projectstore.Decision{
		decision("non-functional-requirements/performance"),
	}}, "non-functional-requirements")
	if one.Percent != 33 {
		t.Errorf("充足率が違う: %+v", one)
	}
}

// 覆された決定事項は充足の根拠にしない。
func TestSupersededDecisionDoesNotSatisfy(t *testing.T) {
	old := decision("background/current-state")
	old.SupersededBy = "DEC-002"
	got := chapterOf(t, Records{Decisions: []projectstore.Decision{old}}, "background")
	if got.Satisfied != 0 {
		t.Errorf("覆された決定で充足済みになった: %+v", got)
	}
}

// 表示区分は 未着手 / 記載あり・未決あり / 記載あり・未決なし の 3 状態。
func TestChapterStates(t *testing.T) {
	empty := chapterOf(t, Records{}, "background")
	if empty.State != StateUntouched {
		t.Errorf("未着手にならない: %+v", empty)
	}

	req := projectstore.Requirement{ID: "FR-BG-001", Chapter: "background",
		Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementDraft,
		BlockedBy: []string{"ISS-001"}}
	issue := projectstore.OpenIssue{ID: "ISS-001", Owner: "佐藤", Status: projectstore.OpenIssueOpen}
	records := Records{
		Decisions:    []projectstore.Decision{decision("background/current-state")},
		Requirements: []projectstore.Requirement{req},
		OpenIssues:   []projectstore.OpenIssue{issue},
	}
	withIssue := chapterOf(t, records, "background")
	if withIssue.State != StateOpenIssues || withIssue.OpenIssues != 1 {
		t.Errorf("未決ありにならない: %+v", withIssue)
	}

	// 未決が決着すれば「記載あり・未決なし」へ変わる。
	records.OpenIssues[0].Status = projectstore.OpenIssueResolved
	settled := chapterOf(t, records, "background")
	if settled.State != StateSettled || settled.OpenIssues != 0 {
		t.Errorf("未決なしにならない: %+v", settled)
	}
}

// 受け入れ条件を持たない機能要件があると「各 FR に受け入れ条件」は未充足。
func TestAcceptanceCriteriaCondition(t *testing.T) {
	withCriteria := projectstore.Requirement{ID: "FR-INV-001", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementDraft,
		AcceptanceCriteria: []string{"受注確定から 3 秒以内に引当が完了すること"}}
	without := projectstore.Requirement{ID: "FR-INV-002", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementDraft}

	full := chapterOf(t, Records{Requirements: []projectstore.Requirement{withCriteria}}, "functional-requirements")
	if full.Satisfied != 2 || full.Percent != 100 {
		t.Fatalf("受け入れ条件つきで満点にならない: %+v", full)
	}
	partial := chapterOf(t, Records{Requirements: []projectstore.Requirement{withCriteria, without}}, "functional-requirements")
	if partial.Satisfied != 1 {
		t.Errorf("受け入れ条件なしの要件があるのに充足した: %+v", partial)
	}
}

// 確定可否は完成度と同一のレコードから算出する。
func TestConfirmable(t *testing.T) {
	agreed := projectstore.Requirement{ID: "FR-INV-001", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementAgreed}
	draft := projectstore.Requirement{ID: "FR-INV-002", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementDraft}

	if got := Confirmable(Records{Requirements: []projectstore.Requirement{agreed}}); !got.Confirmable {
		t.Errorf("全件合意済みなのに確定不可: %+v", got)
	}
	got := Confirmable(Records{Requirements: []projectstore.Requirement{agreed, draft}})
	if got.Confirmable || len(got.DraftRequirements) != 1 || got.DraftRequirements[0] != "FR-INV-002" {
		t.Errorf("未合意の要件項目が検出されない: %+v", got)
	}
	if got.Reason() == "" {
		t.Error("確定できない理由が空")
	}

	blocked := agreed
	blocked.BlockedBy = []string{"ISS-001"}
	open := projectstore.OpenIssue{ID: "ISS-001", Owner: "佐藤", Status: projectstore.OpenIssueOpen}
	got = Confirmable(Records{Requirements: []projectstore.Requirement{blocked}, OpenIssues: []projectstore.OpenIssue{open}})
	if got.Confirmable || len(got.BlockingIssues) != 1 {
		t.Errorf("ブロックする未決事項が検出されない: %+v", got)
	}

	// 決着済みの未決事項はブロックしない。
	open.Status = projectstore.OpenIssueResolved
	got = Confirmable(Records{Requirements: []projectstore.Requirement{blocked}, OpenIssues: []projectstore.OpenIssue{open}})
	if !got.Confirmable {
		t.Errorf("決着済みの未決でブロックされた: %+v", got)
	}
	if got.Reason() != "" {
		t.Errorf("確定可なのに理由が返る: %q", got.Reason())
	}
}

// 完成度と確定前チェックが同じレコードで食い違わない。
func TestCompletenessAndConfirmationUseSameRecords(t *testing.T) {
	req := projectstore.Requirement{ID: "FR-INV-001", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementAgreed,
		AcceptanceCriteria: []string{"条件"}, BlockedBy: []string{"ISS-001"}}
	issue := projectstore.OpenIssue{ID: "ISS-001", Owner: "佐藤", Status: projectstore.OpenIssueOpen}
	records := Records{Requirements: []projectstore.Requirement{req}, OpenIssues: []projectstore.OpenIssue{issue}}

	chapter := chapterOf(t, records, "functional-requirements")
	conf := Confirmable(records)
	// 章観点が「未決あり」なら確定不可でなければならない（表示と判定の食い違いを禁じる）。
	if chapter.State == StateOpenIssues && conf.Confirmable {
		t.Errorf("未決ありなのに確定可: chapter=%+v conf=%+v", chapter, conf)
	}
	if chapter.OpenIssues != len(conf.BlockingIssues) {
		t.Errorf("未決の数が食い違う: 章観点 %d / 確定判定 %d", chapter.OpenIssues, len(conf.BlockingIssues))
	}
}

// chapterOf は指定章観点の充足状況を返す。
func chapterOf(t *testing.T, r Records, chapterID string) ChapterCompleteness {
	t.Helper()
	all, err := Completeness(PhaseRequirements, r)
	if err != nil {
		t.Fatalf("充足率を算出できない: %v", err)
	}
	for _, c := range all {
		if c.ChapterID == chapterID {
			return c
		}
	}
	t.Fatalf("章観点が見つからない: %s", chapterID)
	return ChapterCompleteness{}
}

// structFieldNames は構造体のフィールド名を返す（送信範囲の構造的検査に使う）。
func structFieldNames(v any) []string {
	rt := reflect.TypeOf(v)
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		out = append(out, rt.Field(i).Name)
	}
	return out
}
