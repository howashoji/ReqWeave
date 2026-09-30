package appversion

import "testing"

// 正本（VERSION ファイル）の値が実行時に参照でき、major.minor.patch 形式であること。
// ビルドフラグを指定しないテスト実行でも正本と一致する（埋め込みのため）。
func TestVersionIsSemverFromSourceOfTruth(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("Version() が空。VERSION ファイルの埋め込みが効いていない")
	}
	sv, err := Current()
	if err != nil {
		t.Fatalf("正本の版番号 %q が major.minor.patch 形式ではない: %v", v, err)
	}
	if sv.String() != v {
		t.Fatalf("正本 %q と解釈結果 %q が一致しない（余分な接頭辞・前置ゼロ等）", v, sv.String())
	}
}

func TestParseSemverAccepts(t *testing.T) {
	cases := map[string]Semver{
		"0.1.0":      {0, 1, 0},
		"1.0.0":      {1, 0, 0},
		"0.10.0":     {0, 10, 0},
		"10.20.30":   {10, 20, 30},
		" 1.2.3 \n":  {1, 2, 3},
		"0.0.0":      {0, 0, 0},
		"123.456.78": {123, 456, 78},
	}
	for in, want := range cases {
		got, err := ParseSemver(in)
		if err != nil {
			t.Errorf("ParseSemver(%q) = err %v, want %v", in, err, want)
			continue
		}
		if got != want {
			t.Errorf("ParseSemver(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseSemverRejects(t *testing.T) {
	// 接頭辞 v・プレリリース・要素数違い・非数字・前置ゼロは受け付けない。
	bad := []string{"", "   ", "1.2", "1.2.3.4", "v1.2.3", "1.2.3-rc1", "1.2.3+build",
		"1.2.x", "a.b.c", "01.2.3", "1.02.3", "1.2.03", "-1.2.3", "1..3", "0.0.0-dev"}
	for _, in := range bad {
		if got, err := ParseSemver(in); err == nil {
			t.Errorf("ParseSemver(%q) = %v, nil。誤りを返すべき", in, got)
		}
	}
}

// 版比較は数値順であること（文字列比較なら 0.10.0 < 0.9.0 になり、更新確認が新しい版を見落とす）。
func TestCompareIsNumeric(t *testing.T) {
	cases := []struct {
		a, b string
		want string // "<" / "=" / ">"
	}{
		{"0.9.0", "0.10.0", "<"},
		{"0.10.0", "0.9.0", ">"},
		{"9.0.0", "10.0.0", "<"},
		{"1.2.3", "1.2.3", "="},
		{"1.2.3", "1.2.4", "<"},
		{"1.3.0", "1.2.99", ">"},
		{"2.0.0", "1.99.99", ">"},
	}
	for _, c := range cases {
		a, err := ParseSemver(c.a)
		if err != nil {
			t.Fatalf("ParseSemver(%q): %v", c.a, err)
		}
		b, err := ParseSemver(c.b)
		if err != nil {
			t.Fatalf("ParseSemver(%q): %v", c.b, err)
		}
		got := a.Compare(b)
		sign := "="
		if got < 0 {
			sign = "<"
		} else if got > 0 {
			sign = ">"
		}
		if sign != c.want {
			t.Errorf("%s Compare %s = %d (%s), want %s", c.a, c.b, got, sign, c.want)
		}
	}
}
