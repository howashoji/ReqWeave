package projectstore

import (
	"strings"
	"testing"
)

// 本文を作り直さない照合（matcher）は、以前の判定
// strings.Contains(strings.ToLower(body), strings.ToLower(needle)) と**同じ結果**を返す。
// 速くするための変更で、該当する発話が増えたり減ったりしてはならない。
func TestMatcherEquivalentToLowerContains(t *testing.T) {
	bodies := []string{
		"",
		"現状は Excel 台帳で在庫を管理している。",
		"FR-INV-001 と fr-inv-002 を確認する。",
		"在庫管理システムの在庫管理画面",
		"ΑΒΓ ギリシャ文字と Кириллица",
		"全角 ＡＢＣ と半角 abc",
		"Kelvin 記号 K と普通の K",
		"改行を\nまたぐ\n本文 MIXED Case",
		"S-0100 の 1000 番目の発話",
	}
	needles := []string{
		"在庫管理", "excel", "EXCEL", "Excel", "fr-inv", "FR-INV-00", "αβγ", "кирилл",
		"ａｂｃ", "ＡＢＣ", "k", "kelvin", "mixed case", "S-0100 の 1000 番目の発話",
		"存在しない語", "を\nまたぐ", "1000", "番目",
	}
	checked := 0
	for _, body := range bodies {
		for _, needle := range needles {
			want := strings.Contains(strings.ToLower(body), strings.ToLower(needle))
			if got := newMatcher(needle).match(body); got != want {
				t.Errorf("本文 %q / 語句 %q: got %v, want %v（以前の判定と食い違う）", body, needle, got, want)
			}
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("照合の組み合わせが少なすぎる（検査が空振り）: %d", checked)
	}
}

// 大文字・小文字の違いを持たない語句（日本語・数字）は、本文を作り直さずにそのまま照合する。
func TestMatcherFoldableOnlyForCasedLetters(t *testing.T) {
	cases := map[string]bool{
		"在庫管理":          false,
		"1000":          false,
		"S-0100":        true, // S は大文字・小文字の違いを持つ
		"excel":         true,
		"ＡＢＣ":           true, // 全角英字も大文字・小文字の違いを持つ
		"番目の発話":         false,
		"S-0100 の 1000": true,
	}
	for needle, want := range cases {
		if got := newMatcher(needle).foldable; got != want {
			t.Errorf("語句 %q の foldable = %v, want %v", needle, got, want)
		}
	}
}
