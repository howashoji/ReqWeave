package dialogue

// 単体テスト（ファイル I/O なし）。実行: make -C app test-unit

import (
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 「不明」の理由欄から確認先を取り出す（owner の更新候補）。
func TestParseOwnerCandidate(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   string
	}{
		{"半角コロン", "理由: 判断できません\n確認先: 経理部 田中", "経理部 田中"},
		{"全角コロン", "確認先：購買部", "購買部"},
		{"同一行に理由と確認先", "理由: 権限がない。確認先: 情報システム部", "情報システム部"},
		{"確認先の記入なし", "理由: 社内で調整中です", ""},
		{"空文字", "", ""},
		{"見出しだけで値が空", "確認先: ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseOwnerCandidate(tc.reason); got != tc.want {
				t.Fatalf("確認先が %q です（期待 %q）: 入力 %q", got, tc.want, tc.reason)
			}
		})
	}
}

// 「不明」回答だけを根拠とする決着案は候補にしない。
func TestDropUnknownOnlyDecisions(t *testing.T) {
	unknown := map[string]bool{"QS-001#q-02": true}
	e := &Extraction{Decisions: []DecisionCandidate{
		{Body: "即時引当とする", EvidenceRefs: []string{"QS-001#q-01"}},
		{Body: "締め処理は据え置く", EvidenceRefs: []string{"QS-001#q-02"}},
		{Body: "両方を根拠にする", EvidenceRefs: []string{"QS-001#q-01", "QS-001#q-02"}},
		{Body: "根拠を失った候補", EvidenceRefs: nil},
	}}

	dropped := dropUnknownOnlyDecisions(e, unknown)
	if dropped != 1 {
		t.Fatalf("落とした件数が %d です（期待 1）", dropped)
	}
	if len(e.Decisions) != 3 {
		t.Fatalf("残った候補が %d 件です（期待 3）: %+v", len(e.Decisions), e.Decisions)
	}
	for _, c := range e.Decisions {
		if c.Body == "締め処理は据え置く" {
			t.Fatalf("「不明」だけを根拠とする決着案が残っています: %+v", c)
		}
	}
}

// 反映案に現行本文を添えて変更前後を対比できる。
func TestRequirementDiffs(t *testing.T) {
	reqs := []projectstore.Requirement{
		{ID: "FR-INV-001", Title: "在庫引当", Body: "受注確定時に日次で引き当てる。"},
	}
	e := &Extraction{RequirementUpdates: []RequirementCandidate{
		{Operation: OperationUpdate, TargetID: "FR-INV-001", Title: "在庫引当", BodyAfter: "受注確定時に即時で引き当てる。"},
		{Operation: OperationCreate, Title: "引当取消", BodyAfter: "受注取消時に引当を戻す。"},
	}}

	got := requirementDiffs(e, reqs)
	if len(got) != 2 {
		t.Fatalf("対比が %d 件です（期待 2）: %+v", len(got), got)
	}
	if got[0].BodyBefore != "受注確定時に日次で引き当てる。" {
		t.Fatalf("変更前の本文が違います: %q", got[0].BodyBefore)
	}
	if got[0].BodyAfter != "受注確定時に即時で引き当てる。" {
		t.Fatalf("変更後の本文が違います: %q", got[0].BodyAfter)
	}
	if got[1].BodyBefore != "" {
		t.Fatalf("新規作成の変更前が空ではありません: %q", got[1].BodyBefore)
	}
}

// 矛盾指摘に既存決定の本文が添う。
func TestContradictionViews(t *testing.T) {
	decisions := []projectstore.Decision{{ID: "DEC-001", Body: "在庫引当は日次で行う。"}}
	e := &Extraction{Contradictions: []Contradiction{
		{WithDecisionID: "DEC-001", Description: "回答は即時引当を求めている", EvidenceRefs: []string{"QS-001#q-01"}},
	}}

	got := contradictionViews(e, decisions)
	if len(got) != 1 {
		t.Fatalf("矛盾指摘が %d 件です（期待 1）", len(got))
	}
	if got[0].DecisionBody != "在庫引当は日次で行う。" {
		t.Fatalf("既存決定の本文が添えられていません: %+v", got[0])
	}
}

// 経過節の見出しを重ねない（同一未決事項へ複数回追記した場合）。
func TestHasProgressSection(t *testing.T) {
	if hasProgressSection("論点だけの本文") {
		t.Fatal("経過節が無い本文を有りと判定しました")
	}
	if !hasProgressSection("論点\n\n## 経過\n\n- 2026-08-28 …") {
		t.Fatal("経過節を検出できません")
	}
	if !hasProgressSection("## 経過\n\n- 2026-08-28 …") {
		t.Fatal("先頭の経過節を検出できません")
	}
}

// 「不明」回答の集約（決着案を作らない対象の識別）。
func TestUnknownAnswers(t *testing.T) {
	q := &projectstore.Questionnaire{ID: "QS-001", Questions: []projectstore.Question{
		{ID: "q-01", SourceIssue: "ISS-001"},
		{ID: "q-02", SourceIssue: "ISS-002"},
	}}
	a := &projectstore.Answers{QuestionnaireID: "QS-001", Answers: []projectstore.Answer{
		{QuestionID: "q-01", Kind: projectstore.AnswerKindAnswered, Selected: []string{"即時"}},
		{QuestionID: "q-02", Kind: projectstore.AnswerKindUnknown, Body: "理由: 判断できません\n確認先: 経理部"},
	}}
	issues := []projectstore.OpenIssue{{ID: "ISS-002", Owner: "情報システム部"}}

	got := unknownAnswers(q, a, issues)
	if len(got) != 1 {
		t.Fatalf("「不明」回答が %d 件です（期待 1）: %+v", len(got), got)
	}
	if got[0].AnswerRef != "QS-001#q-02" || got[0].SourceIssueID != "ISS-002" {
		t.Fatalf("参照・発行元が違います: %+v", got[0])
	}
	if got[0].OwnerCandidate != "経理部" {
		t.Fatalf("確認先が取り出せていません: %+v", got[0])
	}
	if got[0].CurrentOwner != "情報システム部" {
		t.Fatalf("現在の決める人が入っていません: %+v", got[0])
	}
}

// 回帰テスト: 型付き nil の *ProviderError を error として渡しても panic しない。
//
// streamResult.err は *aiprovider.ProviderError のため、nil のまま error 引数へ渡すと
// インタフェースが非 nil になり、受け側の nil 判定をすり抜けて nil 参照に至る。
func TestStreamResultCauseIsNilWhenNoProviderError(t *testing.T) {
	if cause := (streamResult{}).cause(); cause != nil {
		t.Fatalf("エラー無しの結果から原因が返りました: %#v", cause)
	}
	// 縮退の案内文は原因が無くても組み立てられる（panic しない）。
	got := fallbackResult(nil, (streamResult{}).cause())
	if !got.Fallback || got.Notice == "" {
		t.Fatalf("縮退結果が組み立てられていません: %+v", got)
	}
}

// 回帰テスト: 型付き nil を直接渡しても panic せず、既定の案内文になる。
func TestDescribeQuestionnaireFailureWithTypedNil(t *testing.T) {
	var perr *aiprovider.ProviderError
	if got := describeQuestionnaireFailure(perr); got == "" {
		t.Fatal("案内文が空です")
	}
}
