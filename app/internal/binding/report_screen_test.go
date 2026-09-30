package binding

// 単体テスト（期間指定の解釈）。実行: make -C app test-unit

import (
	"testing"
	"time"
)

// 期間指定（YYYY-MM-DD）は表示タイムゾーン（ローカル）の暦日として解釈し、
// 両端を含む。保存値は UTC のため、境界は UTC へ直した値で比較する。
func TestParsePeriodBoundUsesLocalCalendarDay(t *testing.T) {
	restore := useTimeZone(t, "JST", 9*60*60)
	defer restore()

	from, err := parsePeriodBound("2026-08-01", false)
	if err != nil {
		t.Fatalf("開始日を解釈できない: %v", err)
	}
	if want := time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Errorf("開始境界が違う: %s（期待 %s = JST 8/1 0:00）", from.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	to, err := parsePeriodBound("2026-08-31", true)
	if err != nil {
		t.Fatalf("終了日を解釈できない: %v", err)
	}
	if want := time.Date(2026, 8, 31, 14, 59, 59, 0, time.UTC); !to.Equal(want) {
		t.Errorf("終了境界が違う: %s（期待 %s = JST 8/31 23:59:59）", to.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// 境界値（JST 0:00 / 8:59 / 9:00）が期間内に入る。UTC 暦日で解釈していると 0:00 と 8:59 が外れる。
	jst := time.FixedZone("JST", 9*60*60)
	for _, tt := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"JST 7/31 23:59（期間外）", time.Date(2026, 7, 31, 23, 59, 0, 0, jst), false},
		{"JST 8/1 0:00", time.Date(2026, 8, 1, 0, 0, 0, 0, jst), true},
		{"JST 8/1 8:59", time.Date(2026, 8, 1, 8, 59, 0, 0, jst), true},
		{"JST 8/1 9:00", time.Date(2026, 8, 1, 9, 0, 0, 0, jst), true},
		{"JST 8/31 23:59", time.Date(2026, 8, 31, 23, 59, 0, 0, jst), true},
		{"JST 9/1 0:00（期間外）", time.Date(2026, 9, 1, 0, 0, 0, 0, jst), false},
	} {
		got := !tt.at.Before(from) && !tt.at.After(to)
		if got != tt.want {
			t.Errorf("%s の期間判定が %v（期待 %v）", tt.name, got, tt.want)
		}
	}

	if _, err := parsePeriodBound("2026/08/01", false); err == nil {
		t.Error("形式違いが受理された")
	}
	if got, err := parsePeriodBound("  ", false); err != nil || !got.IsZero() {
		t.Errorf("空指定は無制限（ゼロ値）であること: %v %v", got, err)
	}
}

// useTimeZone はテスト中だけ表示タイムゾーンを固定する（TZ 依存の検証を再現可能にする）。
func useTimeZone(t *testing.T, name string, offsetSeconds int) func() {
	t.Helper()
	orig := time.Local
	time.Local = time.FixedZone(name, offsetSeconds)
	return func() { time.Local = orig }
}
