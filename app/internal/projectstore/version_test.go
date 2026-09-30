package projectstore

import "testing"

func TestParseFormatVersion(t *testing.T) {
	v, err := ParseFormatVersion("1.0")
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if v.Major != 1 || v.Minor != 0 {
		t.Errorf("解釈結果が違う: %+v", v)
	}
	if v.String() != "1.0" {
		t.Errorf("文字列化が違う: %q", v.String())
	}
	for _, s := range []string{"", "1", "1.x", "x.0", "-1.0", "1.0.0"} {
		if _, err := ParseFormatVersion(s); err == nil {
			t.Errorf("%q は不正だが受理された", s)
		}
	}
}

// 自版より古い = 移行 / 新しい minor = 未知フィールド保持で読む /
// 新しい major = 書き込まない。
func TestClassify(t *testing.T) {
	cases := []struct {
		version string
		want    Compatibility
	}{
		{CurrentFormatVersion, CompatSame},
		{"1.0", CompatNeedsMigration}, // 初版（summary 追加前）
		{"1.1", CompatNeedsMigration}, // acceptance_criteria 追加前
		{"1.2", CompatNeedsMigration}, // ambiguous_terms 追加前
		{"1.3", CompatNeedsMigration}, // sync / reservations.yaml / id-ranges.yaml 追加前
		{"0.9", CompatNeedsMigration},
		{"1.5", CompatNewerMinor},
		{"2.0", CompatTooNew},
		{"2.5", CompatTooNew},
	}
	for _, c := range cases {
		v, err := ParseFormatVersion(c.version)
		if err != nil {
			t.Fatalf("%s: %v", c.version, err)
		}
		if got := Classify(v); got != c.want {
			t.Errorf("%s: got %v, want %v（自版 %s）", c.version, got, c.want, CurrentFormatVersion)
		}
	}
}
