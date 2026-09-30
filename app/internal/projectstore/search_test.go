package projectstore

import (
	"strings"
	"testing"
)

// 抜粋は該当箇所を含み、前後を切った側に省略記号が付く。
func TestExcerptAroundKeepsTheHitInTheMiddle(t *testing.T) {
	long := strings.Repeat("あ", 200) + "在庫の締め処理" + strings.Repeat("い", 200)
	got := excerptAround(long, "在庫の締め処理")
	if !strings.Contains(got, "在庫の締め処理") {
		t.Fatalf("該当箇所が抜粋に入っていない: %q", got)
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("前後を切ったのに省略記号が無い: %q", got)
	}
	// 前後 excerptContextRunes 文字 + 該当語 + 省略記号 2 個。
	want := excerptContextRunes*2 + len([]rune("在庫の締め処理")) + 2
	if got := len([]rune(got)); got != want {
		t.Errorf("抜粋の長さが違う: %d 文字（期待 %d 文字）", got, want)
	}
}

// 短い本文は切らない（省略記号を付けない）。
func TestExcerptAroundDoesNotTruncateShortBody(t *testing.T) {
	got := excerptAround("在庫の締め処理について", "締め")
	if got != "在庫の締め処理について" {
		t.Errorf("短い本文が加工された: %q", got)
	}
}

// 改行は空白へ畳む（結果一覧が 1 行で読めること）。
func TestExcerptAroundFlattensNewlines(t *testing.T) {
	got := excerptAround("前段の説明\n\n在庫の締め処理\n後段", "在庫")
	if strings.Contains(got, "\n") {
		t.Errorf("改行が残っている: %q", got)
	}
	if got != "前段の説明 在庫の締め処理 後段" {
		t.Errorf("畳み方が違う: %q", got)
	}
}
