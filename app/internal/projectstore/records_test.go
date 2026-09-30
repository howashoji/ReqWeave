package projectstore

import (
	"testing"
	"time"
)

// 要件項目 ID は FR-<グループ>-nnn / NFR-<グループ>-nnn。
func TestParseRequirementID(t *testing.T) {
	cases := map[string]struct {
		prefix string
		group  string
		number int
		ok     bool
	}{
		"FR-INV-001":  {"FR", "INV", 1, true},
		"NFR-PF-012":  {"NFR", "PF", 12, true},
		"FR-INV2-003": {"FR", "INV2", 3, true},
		"FR-INV-1":    {"", "", 0, false}, // 桁数不足
		"FR-inv-001":  {"", "", 0, false}, // 小文字グループ
		"DEC-001":     {"", "", 0, false}, // 別の ID 体系
		"FR--001":     {"", "", 0, false}, // グループ空
	}
	for id, want := range cases {
		prefix, group, number, ok := ParseRequirementID(id)
		if ok != want.ok || prefix != want.prefix || group != want.group || number != want.number {
			t.Errorf("%s: got (%q,%q,%d,%v), want (%q,%q,%d,%v)",
				id, prefix, group, number, ok, want.prefix, want.group, want.number, want.ok)
		}
	}
}

// 種別と ID プレフィックスの不一致を弾く。
func TestRequirementValidate(t *testing.T) {
	r := Requirement{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
		Kind: RequirementFunctional, Priority: PriorityMust, Status: RequirementDraft, Body: "本文"}
	if err := r.Validate(); err != nil {
		t.Fatalf("正しい要件項目が拒否された: %v", err)
	}
	bad := r
	bad.Kind = RequirementNonFunctional
	if err := bad.Validate(); err == nil {
		t.Error("ID と種別が食い違う要件項目が受理された")
	}
	bad = r
	bad.Priority = "high"
	if err := bad.Validate(); err == nil {
		t.Error("優先度が値集合外の要件項目が受理された")
	}
	bad = r
	bad.Status = "confirmed"
	if err := bad.Validate(); err == nil {
		t.Error("状態が値集合外の要件項目が受理された")
	}
	bad = r
	bad.Chapter = ""
	if err := bad.Validate(); err == nil {
		t.Error("章観点なしの要件項目が受理された")
	}
}

// 決着した未決事項は決着させた決定事項の ID を持つ。
func TestOpenIssueValidate(t *testing.T) {
	i := OpenIssue{ID: "ISS-001", Owner: "佐藤", Status: OpenIssueOpen,
		Evidence: []string{"S-0001#utt-00005"}, Body: "論点"}
	if err := i.Validate(); err != nil {
		t.Fatalf("正しい未決事項が拒否された: %v", err)
	}
	i.Status = OpenIssueResolved
	if err := i.Validate(); err == nil {
		t.Error("決着させた決定事項の無い resolved が受理された")
	}
	i.ResolvedBy = "DEC-001"
	if err := i.Validate(); err != nil {
		t.Errorf("決着済みの未決事項が拒否された: %v", err)
	}
}

// 期限超過は「期限日の終わりを過ぎたか」で判定する。
func TestOpenIssueIsOverdue(t *testing.T) {
	i := OpenIssue{ID: "ISS-001", Owner: "佐藤", Status: OpenIssueOpen, Due: "2026-09-30",
		Evidence: []string{"S-0001#utt-00005"}, Body: "論点"}
	cases := map[string]struct {
		now  time.Time
		want bool
	}{
		"期限前":  {time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local), false},
		"期限当日": {time.Date(2026, 9, 30, 23, 59, 0, 0, time.Local), false},
		"期限翌日": {time.Date(2026, 10, 1, 0, 0, 1, 0, time.Local), true},
	}
	for name, tc := range cases {
		if got := i.IsOverdue(tc.now); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
	noDue := i
	noDue.Due = ""
	if noDue.IsOverdue(time.Date(2030, 1, 1, 0, 0, 0, 0, time.Local)) {
		t.Error("期限未設定が超過と判定された")
	}
}

// 決定事項の必須項目。
func TestDecisionValidate(t *testing.T) {
	d := Decision{ID: "DEC-001", TopicKey: "scope/in-scope", DecidedAt: time.Now(),
		Evidence: []string{"S-0001#utt-00003"}, Body: "本文"}
	if err := d.Validate(); err != nil {
		t.Fatalf("正しい決定事項が拒否された: %v", err)
	}
	bad := d
	bad.Evidence = nil
	if err := bad.Validate(); err == nil {
		t.Error("根拠なしの決定事項が受理された")
	}
	bad = d
	bad.ID = "DEC-1"
	if err := bad.Validate(); err == nil {
		t.Error("ID 形式が不正な決定事項が受理された")
	}
}
