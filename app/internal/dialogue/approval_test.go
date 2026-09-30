package dialogue

import (
	"strings"
	"testing"
)

// 書き込み前の検証は、記録層が拒む候補をすべて利用者の言葉で先に拒む。
func TestValidateApproval(t *testing.T) {
	decision := DecisionCandidate{TopicKey: "scope/out-of-scope", Body: "開発AIの実行はスコープ外とする。",
		EvidenceRefs: []string{"S-0001#utt-00002"}}
	issue := OpenIssueCandidate{Topic: "棚卸の頻度を決める", Owner: "鈴木", Due: "2026-09-30",
		EvidenceRefs: []string{"S-0001#utt-00002"}}
	create := RequirementCandidate{Operation: OperationCreate, Chapter: "functional-requirements",
		Title: "在庫引当", BodyAfter: "受注確定時に在庫を引き当てること。", EvidenceRefs: []string{"S-0001#utt-00002"}}

	// ID グループ・種別は利用者の入力ではなくアプリが決めるため、ここでは要求しない
	// （解決は ResolveRequirementIDs / applyRequirements）。
	if err := ValidateApproval(ApprovalRequest{
		Decisions:          []DecisionApproval{{Candidate: decision}},
		OpenIssues:         []OpenIssueCandidate{issue},
		RequirementUpdates: []RequirementApproval{{Candidate: create}},
	}); err != nil {
		t.Fatalf("記録できる候補が拒まれた: %v", err)
	}

	noEvidence := decision
	noEvidence.EvidenceRefs = nil
	noOwner := issue
	noOwner.Owner = " "
	badDue := issue
	badDue.Due = "9/30"
	cases := []struct {
		name string
		req  ApprovalRequest
		want string
	}{
		{"根拠のない決定事項", ApprovalRequest{Decisions: []DecisionApproval{{Candidate: noEvidence}}},
			"決定事項候補「開発AIの実行はスコープ外とする。」は、根拠にした発言を特定できないため記録できません"},
		{"決める人が空の未決事項", ApprovalRequest{OpenIssues: []OpenIssueCandidate{noOwner}},
			"未決事項候補「棚卸の頻度を決める」の「決める人」が空です"},
		{"期限の形式違い", ApprovalRequest{OpenIssues: []OpenIssueCandidate{badDue}},
			"期限は 2026-09-30 のように年-月-日で入れてください"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateApproval(tc.req)
			if err == nil {
				t.Fatal("記録できない候補が通った")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("文言が違う:\n got: %s\nwant: %s", err, tc.want)
			}
			// 内部の表記（要件 ID）を出さない。
			if strings.Contains(err.Error(), "FR-TRC") || strings.Contains(err.Error(), "BD-") {
				t.Errorf("内部の表記が出ている: %s", err)
			}
		})
	}
}

// 候補を文中で指す表記は 1 行目・24 文字までに縮める（長い本文でエラー文が読めなくならない）。
func TestCandidateLabel(t *testing.T) {
	long := "ReqWeave のスコープ外として次を明記する。(1) 開発AIの実行"
	got := candidateLabel("\n" + long + "\n2 行目")
	if got != string([]rune(long)[:24])+"…" {
		t.Errorf("縮め方が違う: %q", got)
	}
	if got := candidateLabel("短い本文"); got != "短い本文" {
		t.Errorf("短い本文が変わった: %q", got)
	}
}
