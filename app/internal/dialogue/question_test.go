package dialogue

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 状態機械に無い遷移を作らない。
func TestStateTransitions(t *testing.T) {
	allowed := [][2]string{
		{StateQuestioning, StateAwaitingAnswer},
		{StateQuestioning, StateSuspended},
		{StateQuestioning, StateFailed},
		{StateAwaitingAnswer, StateExtracting},
		{StateExtracting, StateAwaitingApproval},
		{StateAwaitingApproval, StateApplying},
		{StateAwaitingApproval, StateQuestioning},
		{StateApplying, StateQuestioning},
		{StateApplying, StateAwaitingApproval}, // 反映の失敗・競合で候補を保全したまま戻る
		{StateFailed, StateSuspended},
		{StateSuspended, StateQuestioning},
	}
	for _, tc := range allowed {
		if err := ValidateTransition(tc[0], tc[1]); err != nil {
			t.Errorf("許されるはずの遷移が拒否された: %s → %s（%v）", tc[0], tc[1], err)
		}
	}
	denied := [][2]string{
		{StateApplying, StateFailed}, // 反映中は AI 呼び出しを伴わない
		{StateQuestioning, StateApplying},
		{StateAwaitingAnswer, StateAwaitingApproval},
		{StateSuspended, StateExtracting},
	}
	for _, tc := range denied {
		if err := ValidateTransition(tc[0], tc[1]); err == nil {
			t.Errorf("許されない遷移が通った: %s → %s", tc[0], tc[1])
		}
	}
	if err := ValidateTransition(StateQuestioning, StateQuestioning); err != nil {
		t.Errorf("同一状態への遷移が拒否された: %v", err)
	}
	if IsKnownState("unknown") {
		t.Error("未知の状態が既知と判定された")
	}
}

// 出力契約の質問提示形式を解釈する。形式が崩れても対話を止めない。
func TestParseQuestion(t *testing.T) {
	got := ParseQuestion("論点キー: scope/in-scope\n質問: 対象となる業務範囲はどこまでですか。\n背景: スコープ外を明記するために確認します。")
	if got.TopicKey != "scope/in-scope" {
		t.Errorf("論点キーが取れない: %+v", got)
	}
	if !strings.Contains(got.Question, "業務範囲") || !strings.Contains(got.Background, "スコープ外") {
		t.Errorf("質問・背景が取れない: %+v", got)
	}

	// 形式が崩れている場合は本文全体を質問として扱う。
	broken := ParseQuestion("対象範囲を教えてください。")
	if broken.TopicKey != "" || broken.Question != "対象範囲を教えてください。" {
		t.Errorf("崩れた形式の扱いが違う: %+v", broken)
	}
}

// 既決論点への再確認は明示表示を伴って提示する。
func TestQuestionUtteranceBody(t *testing.T) {
	q := ParsedQuestion{TopicKey: "scope/in-scope", Question: "変更しますか。", Background: "決定済みです。"}
	plain := QuestionUtteranceBody(q, false)
	if strings.Contains(plain, ReconfirmNotice) {
		t.Errorf("再確認でないのに明示表示が付いた:\n%s", plain)
	}
	reconfirm := QuestionUtteranceBody(q, true)
	if !strings.HasPrefix(reconfirm, ReconfirmNotice) {
		t.Errorf("再確認の明示表示が先頭に無い:\n%s", reconfirm)
	}
}

// 章観点の並び順で最初に現れる未充足の論点を選ぶ。
func TestSelectTopicChapterOrder(t *testing.T) {
	records := Records{}
	completeness, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SelectTopic(PhaseRequirements, records, completeness, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.TopicKey != "background/current-state" {
		t.Fatalf("最初の論点が違う: %+v", got)
	}
	if got.Reason != ReasonChapterOrder || got.FollowUpIndex != 1 {
		t.Errorf("選定理由・往復数が違う: %+v", got)
	}

	// 既決の論点は飛ばす。
	records.Decisions = []projectstore.Decision{{ID: "DEC-001", TopicKey: "background/current-state",
		Body: "現状は手作業。", Evidence: []string{"S-0001#utt-00001"}}}
	completeness, _ = Completeness(PhaseRequirements, records)
	got, _ = SelectTopic(PhaseRequirements, records, completeness, nil, nil)
	if got == nil || got.TopicKey != "background/purpose" {
		t.Fatalf("既決の論点が再選定された: %+v", got)
	}
}

// 着手済みで追問上限に達していない論点を継続する。
func TestSelectTopicContinuesFollowUp(t *testing.T) {
	records := Records{}
	completeness, _ := Completeness(PhaseRequirements, records)
	last := &PresentedQuestion{TopicKey: "scope/in-scope", FollowUpIndex: 1, Answered: true}

	got, err := SelectTopic(PhaseRequirements, records, completeness, last, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.TopicKey != "scope/in-scope" || got.Reason != ReasonFollowUp || got.FollowUpIndex != 2 {
		t.Fatalf("継続にならない: %+v", got)
	}

	// 未回答の質問は継続対象にしない（同じ質問を二重に出さない）。
	last.Answered = false
	got, _ = SelectTopic(PhaseRequirements, records, completeness, last, nil)
	if got.TopicKey == "scope/in-scope" && got.Reason == ReasonFollowUp {
		t.Errorf("未回答の論点が継続された: %+v", got)
	}

	// 決着した論点は継続しない。
	last.Answered = true
	records.Decisions = []projectstore.Decision{{ID: "DEC-001", TopicKey: "scope/in-scope",
		Body: "EDI と Web。", Evidence: []string{"S-0001#utt-00001"}}}
	completeness, _ = Completeness(PhaseRequirements, records)
	got, _ = SelectTopic(PhaseRequirements, records, completeness, last, nil)
	if got.TopicKey == "scope/in-scope" {
		t.Errorf("既決の論点が継続された: %+v", got)
	}
}

// 追問上限（低=1 / 標準=3 / 高=5）の超過判定。
func TestExceedsFollowUpLimit(t *testing.T) {
	if ExceedsFollowUpLimit(3, 3) {
		t.Error("上限ちょうどが超過と判定された")
	}
	if !ExceedsFollowUpLimit(4, 3) {
		t.Error("上限超過が検出されない")
	}
}

// 全章観点が充足したら次の質問は無い。
func TestSelectTopicReturnsNilWhenComplete(t *testing.T) {
	m, _ := LoadMetaModel()
	p, _ := m.Phase(PhaseRequirements)
	records := Records{}
	for _, c := range p.Chapters {
		for _, item := range c.Items {
			switch item.Condition.Kind {
			case CondDecision:
				for i := 0; i < item.Condition.Min; i++ {
					records.Decisions = append(records.Decisions, projectstore.Decision{
						ID: "DEC-000", TopicKey: c.TopicKey(item.ID), Body: "決定",
						Evidence: []string{"S-0001#utt-00001"}})
				}
			case CondRequirement, CondRequirementCriteria:
				records.Requirements = append(records.Requirements, projectstore.Requirement{
					ID: "FR-X-001", Chapter: c.ID, Kind: item.Condition.RequirementKind,
					Priority: projectstore.PriorityMust, Status: projectstore.RequirementAgreed,
					Title: "要件", Body: "本文", AcceptanceCriteria: []string{"条件"}})
			case CondTerm:
				for i := 0; i < item.Condition.Min; i++ {
					records.Terms = append(records.Terms, projectstore.Term{
						Name: string(rune('あ' + i)), NameEn: string(rune('a' + i)), Definition: "定義"})
				}
			}
		}
	}
	completeness, err := Completeness(PhaseRequirements, records)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range completeness {
		if c.Percent != 100 {
			t.Fatalf("充足していない章観点がある: %+v", c)
		}
	}
	got, err := SelectTopic(PhaseRequirements, records, completeness, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("充足済みなのに論点が選ばれた: %+v", got)
	}
}

// 提示済み質問は併置メタデータへ往復できる（再開時の復元）。
func TestPresentedQuestionRoundTrip(t *testing.T) {
	q := &PresentedQuestion{TopicKey: "scope/in-scope", Text: "質問文", FollowUpIndex: 2,
		Answered: true, UtteranceID: "utt-00003", Reconfirm: true}
	got, err := decodePresentedQuestion(encodePresentedQuestion(q))
	if err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if *got != *q {
		t.Errorf("往復で内容が変わった: %+v（期待 %+v）", got, q)
	}
	if encodePresentedQuestion(nil) != nil {
		t.Error("nil が nil に変換されない")
	}
	if _, err := decodePresentedQuestion("不正な形式"); err == nil {
		t.Error("不正な形式が受理された")
	}
}
