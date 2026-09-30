package importer

import "testing"

// 根拠参照の形式（IMP-nnn#Lm-Ln）だけを取り込み資料の参照として扱う。
func TestParseRef(t *testing.T) {
	cases := []struct {
		ref              string
		wantID           string
		wantFrom, wantTo int
		wantOK           bool
	}{
		{"IMP-001#L3-L7", "IMP-001", 3, 7, true},
		{"IMP-012#L1-L1", "IMP-012", 1, 1, true},
		{" IMP-001#L3-L7 ", "IMP-001", 3, 7, true}, // 前後の空白は許す
		{"IMP-001#L0-L3", "", 0, 0, false},         // 行番号は 1 始まり
		{"IMP-001#L7-L3", "", 0, 0, false},         // 範囲の逆転
		{"IMP-1#L1-L2", "", 0, 0, false},           // ID の桁
		{"S-0001#utt-00001", "", 0, 0, false},      // 発話参照
		{"QS-001#q-01", "", 0, 0, false},           // 回答参照
		{"IMP-001", "", 0, 0, false},               // 行範囲なし
	}
	for _, c := range cases {
		id, from, to, ok := ParseRef(c.ref)
		if ok != c.wantOK || id != c.wantID || from != c.wantFrom || to != c.wantTo {
			t.Errorf("ParseRef(%q) = (%q, %d, %d, %v), want (%q, %d, %d, %v)",
				c.ref, id, from, to, ok, c.wantID, c.wantFrom, c.wantTo, c.wantOK)
		}
	}
}

// FormatRef と ParseRef は往復する（表記の正本を 1 か所に保つ）。
func TestFormatRefRoundTrip(t *testing.T) {
	ref := FormatRef("IMP-007", 12, 34)
	if ref != "IMP-007#L12-L34" {
		t.Fatalf("表記が違う: %q", ref)
	}
	id, from, to, ok := ParseRef(ref)
	if !ok || id != "IMP-007" || from != 12 || to != 34 {
		t.Errorf("往復しない: (%q, %d, %d, %v)", id, from, to, ok)
	}
}
