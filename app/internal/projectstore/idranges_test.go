package projectstore

import (
	"strings"
	"testing"
	"time"
)

// 対象種別は 8 種で閉じ、未知の種別・不正な区間・確保日時の欠落を拒否する。
func TestIDRangesValidate(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	ok := &IDRanges{Ranges: map[string][]IDRange{
		RangeRequirement: {
			{AuthorID: "k.sato@example.co.jp", From: 1, To: 100, ReservedAt: now},
			{AuthorID: "y.suzuki@example.co.jp", From: 101, To: 200, ReservedAt: now},
		},
		RangeSession: {{AuthorID: "k.sato@example.co.jp", From: 1, To: 100, ReservedAt: now}},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("正しい番号帯が拒否された: %v", err)
	}
	if got := RangeKeys(); len(got) != 8 {
		t.Fatalf("対象種別の数が違う: %v", got)
	}

	cases := []struct {
		name string
		in   *IDRanges
		want string
	}{
		{"未知の対象種別", &IDRanges{Ranges: map[string][]IDRange{
			"utterance": {{AuthorID: "k.sato@example.co.jp", From: 1, To: 100, ReservedAt: now}}}}, "未知の対象種別"},
		{"下限が 0", &IDRanges{Ranges: map[string][]IDRange{
			RangeSession: {{AuthorID: "k.sato@example.co.jp", From: 0, To: 100, ReservedAt: now}}}}, "区間が不正"},
		{"上限が下限より小さい", &IDRanges{Ranges: map[string][]IDRange{
			RangeSession: {{AuthorID: "k.sato@example.co.jp", From: 100, To: 99, ReservedAt: now}}}}, "区間が不正"},
		{"確保日時なし", &IDRanges{Ranges: map[string][]IDRange{
			RangeSession: {{AuthorID: "k.sato@example.co.jp", From: 1, To: 100}}}}, "確保日時"},
		{"区間の重なり", &IDRanges{Ranges: map[string][]IDRange{
			RangeSession: {
				{AuthorID: "k.sato@example.co.jp", From: 1, To: 100, ReservedAt: now},
				{AuthorID: "y.suzuki@example.co.jp", From: 100, To: 200, ReservedAt: now}}}}, "重なって"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.in.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("拒否されない・理由が違う: %v", err)
			}
		})
	}
}

// 区間が重なった番号帯は**読み込みでは失敗させない**（プロジェクトが開けなくなるため）。
// 重複 ID の防止は採番側で行う（他の作業者の区間を避ける）。
func TestUnmarshalIDRangesToleratesOverlap(t *testing.T) {
	data := []byte(`ranges:
  session:
    - author_id: k.sato@example.co.jp
      from: 101
      to: 200
      reserved_at: 2026-09-02T00:00:00Z
    - author_id: y.suzuki@example.co.jp
      from: 101
      to: 200
      reserved_at: 2026-09-02T00:00:00Z
`)
	r, err := UnmarshalIDRanges(data)
	if err != nil {
		t.Fatalf("重なりのある番号帯が読み込めない: %v", err)
	}
	if len(r.Ranges[RangeSession]) != 2 {
		t.Fatalf("読み込み結果が違う: %+v", r.Ranges)
	}
	if !r.othersCover(RangeSession, "k.sato@example.co.jp", 150) {
		t.Error("他の作業者の区間に含まれる番号を検出できない")
	}
	if r.othersCover(RangeSession, "k.sato@example.co.jp", 300) {
		t.Error("区間外の番号を他の作業者のものと判定した")
	}
	// 書き出しでは重なりを拒否する（自分が壊れた内容を書かない）。
	if _, err := r.Marshal(); err == nil {
		t.Error("重なりのある番号帯を書き出せてしまう")
	}
}

// 桁あふれ（連番が形式の桁数を超える）を受け入れ、ゼロ埋め桁を増やす。既存 ID は変えない。
func TestIDKindFormatAndParseHandlesDigitOverflow(t *testing.T) {
	cases := []struct {
		kind IDKind
		n    int
		want string
	}{
		{IDQuestionnaire, 1, "QS-001"},
		{IDQuestionnaire, 999, "QS-999"},
		{IDQuestionnaire, 1000, "QS-1000"},
		{IDQuestionnaire, 12345, "QS-12345"},
		{IDSession, 10000, "S-10000"},
	}
	for _, c := range cases {
		got := c.kind.Format(c.n)
		if got != c.want {
			t.Errorf("Format(%d): got %q, want %q", c.n, got, c.want)
		}
		n, ok := c.kind.Parse(got)
		if !ok || n != c.n {
			t.Errorf("Parse(%q): got (%d, %v), want (%d, true)", got, n, ok, c.n)
		}
	}
	// 桁数不足・先頭ゼロ付きの過剰桁は受け付けない（表記ゆれを作らない）。
	for _, bad := range []string{"QS-01", "QS-0001", "QS-abc", "S-001"} {
		if _, ok := IDQuestionnaire.Parse(bad); ok && bad != "S-001" {
			t.Errorf("不正な ID が受理された: %q", bad)
		}
	}
	if _, ok := IDSession.Parse("S-0001"); !ok {
		t.Error("規定桁数の ID が拒否された: S-0001")
	}
	// 要件項目 ID も桁あふれを受け入れる。
	for _, id := range []string{"FR-INV-001", "FR-INV-1000", "NFR-PF-12345"} {
		if _, _, _, ok := ParseRequirementID(id); !ok {
			t.Errorf("要件項目 ID が拒否された: %q", id)
		}
	}
	for _, id := range []string{"FR-INV-01", "FR-INV-0001"} {
		if _, _, _, ok := ParseRequirementID(id); ok {
			t.Errorf("不正な要件項目 ID が受理された: %q", id)
		}
	}
}

// 残量から枯渇予告・枯渇を判定する。
func TestIDRangeStatusThresholds(t *testing.T) {
	cases := []struct {
		name          string
		reserved      int
		used          int
		wantWarn      bool
		wantExhausted bool
	}{
		{"十分な残り", 100, 50, false, false},
		{"閾値の直前", 100, 79, false, false},
		{"閾値ちょうど（残り 20%）", 100, 80, true, false},
		{"閾値を下回る", 100, 95, true, false},
		{"使い切り", 100, 100, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := IDRangeStatus{Reserved: c.reserved, Used: c.used, Remaining: c.reserved - c.used}
			st.Exhausted = st.Remaining <= 0
			st.Warn = st.Exhausted || float64(st.Remaining) <= float64(st.Reserved)*DefaultIDRangeWarnRatio
			if st.Warn != c.wantWarn || st.Exhausted != c.wantExhausted {
				t.Errorf("判定が違う: %+v", st)
			}
		})
	}
	// 既定値が許容範囲（幅 50〜1000・予告 10〜30%）に収まる。
	if DefaultIDRangeWidth < 50 || DefaultIDRangeWidth > 1000 {
		t.Errorf("区間幅が許容範囲外: %d", DefaultIDRangeWidth)
	}
	if DefaultIDRangeWarnRatio < 0.1 || DefaultIDRangeWarnRatio > 0.3 {
		t.Errorf("枯渇予告の閾値が許容範囲外: %v", DefaultIDRangeWarnRatio)
	}
}
