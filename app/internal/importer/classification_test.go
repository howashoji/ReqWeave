package importer

import (
	"testing"
	"time"
)

// 分類の値集合は 5 値。ラベルはコード値を画面へ出さないための対応表。
func TestClassificationValueSet(t *testing.T) {
	if len(Classifications) != 5 {
		t.Fatalf("分類の値集合の件数が違う: %d 件 %v", len(Classifications), Classifications)
	}
	want := []Classification{ClassRequirementsGap, ClassDesignGap, ClassNewRequest,
		ClassClarification, ClassOther}
	for i, c := range want {
		if Classifications[i] != c {
			t.Errorf("値集合の定義順が違う[%d]: %q, want %q", i, Classifications[i], c)
		}
		if !validClassifications[c] {
			t.Errorf("%q が検証の値集合に無い", c)
		}
		if label := ClassificationLabel(c); label == "" || label == string(c) {
			t.Errorf("%q の表示ラベルが無い（コード値を画面へ出している）: %q", c, label)
		}
	}
	if ClassificationLabel("") != "未分類" {
		t.Errorf("未分類のラベルが違う: %q", ClassificationLabel(""))
	}
}

// 期間の両端を含み、ゼロ値は無制限（集計の期間絞り込み）。
func TestInPeriod(t *testing.T) {
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)

	cases := []struct {
		name     string
		at       time.Time
		from, to time.Time
		want     bool
	}{
		{"期間内", base, from, to, true},
		{"開始と同時刻", from, from, to, true},
		{"終了と同時刻", to, from, to, true},
		{"開始より前", from.Add(-time.Second), from, to, false},
		{"終了より後", to.Add(time.Second), from, to, false},
		{"期間指定なし", base, time.Time{}, time.Time{}, true},
		{"開始のみ", base, from, time.Time{}, true},
		{"終了のみ（範囲外）", base, time.Time{}, from, false},
	}
	for _, c := range cases {
		if got := inPeriod(c.at, c.from, c.to); got != c.want {
			t.Errorf("%s: inPeriod = %v, want %v", c.name, got, c.want)
		}
	}
}
