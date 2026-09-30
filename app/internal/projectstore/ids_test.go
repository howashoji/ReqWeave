package projectstore

import "testing"

// 内部識別子の形式（S-nnnn / QS-nnn / IMP-nnn / STK-nnn / PRS-nnn）。
func TestIDKindFormat(t *testing.T) {
	cases := []struct {
		kind IDKind
		n    int
		want string
	}{
		{IDSession, 3, "S-0003"},
		{IDSession, 1234, "S-1234"},
		{IDQuestionnaire, 2, "QS-002"},
		{IDImport, 3, "IMP-003"},
		{IDStakeholder, 2, "STK-002"},
		{IDPerspective, 4, "PRS-004"},
	}
	for _, c := range cases {
		if got := c.kind.Format(c.n); got != c.want {
			t.Errorf("%s.Format(%d): got %q, want %q", c.kind.Prefix, c.n, got, c.want)
		}
	}
}

func TestIDKindParse(t *testing.T) {
	if n, ok := IDSession.Parse("S-0003"); !ok || n != 3 {
		t.Errorf("S-0003 の解釈: %d, %v", n, ok)
	}
	if n, ok := IDQuestionnaire.Parse("QS-012"); !ok || n != 12 {
		t.Errorf("QS-012 の解釈: %d, %v", n, ok)
	}
	invalid := []string{"S-003", "S-00003", "QS-0003", "IMP-3", "S-abcd", "S-0000", "X-0001", "0001"}
	for _, id := range invalid {
		if n, ok := IDSession.Parse(id); ok {
			t.Errorf("%q は S-nnnn として不正だが受理された: %d", id, n)
		}
	}
	// 種別違いは受理しない
	if _, ok := IDSession.Parse("QS-001"); ok {
		t.Error("種別違いの ID が受理された")
	}
}
