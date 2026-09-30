package projectstore

import (
	"strings"
	"testing"
	"time"
)

// 予約対象 ID は対象一覧（固定 6 種 + 個別レコード 3 種）で閉じている。
func TestValidateReservationTarget(t *testing.T) {
	valid := []string{
		ReservationDocumentsRequirements, ReservationDocumentsBasicDesign,
		ReservationMembers, ReservationRoster, ReservationTerms, ReservationPerspectives,
		"requirement:FR-INV-001", "requirement:NFR-PF-002", "decision:DEC-001", "open-issue:ISS-012",
	}
	for _, target := range valid {
		if err := ValidateReservationTarget(target); err != nil {
			t.Errorf("有効な対象 %q が拒否された: %v", target, err)
		}
	}
	invalid := []string{
		"", "ids", "records", "documents", "sessions",
		"requirement:", "requirement:DEC-001", "decision:DEC-1", "open-issue:FR-INV-001", "session:S-0001",
	}
	for _, target := range invalid {
		if err := ValidateReservationTarget(target); err == nil {
			t.Errorf("無効な対象 %q が受理された", target)
		}
	}
}

// 同じ作業者・同じ対象の未解除エントリは 1 件まで。必須項目の欠落を拒否する。
func TestReservationsValidate(t *testing.T) {
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	base := Reservation{
		Target: ReservationTerms, Mode: ReservationExclusive,
		AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤", StartedAt: now,
	}
	ok := &Reservations{Reservations: []Reservation{
		base,
		{Target: ReservationTerms, Mode: ReservationConcurrent, AuthorID: "y.suzuki@example.co.jp",
			DisplayName: "鈴木", StartedAt: now},
		// 解除済みは同じ作業者・対象でも履歴として残る。
		{Target: ReservationTerms, Mode: ReservationExclusive, AuthorID: "k.sato@example.co.jp",
			DisplayName: "佐藤", StartedAt: now.Add(-time.Hour), ReleasedAt: now.Add(-time.Minute),
			ReleasedBy: "k.sato@example.co.jp"},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("正しい予約が拒否された: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(r *Reservation)
		want   string
	}{
		{"進め方が不正", func(r *Reservation) { r.Mode = "shared" }, "進め方"},
		{"利用者 ID が空", func(r *Reservation) { r.AuthorID = "" }, "利用者 ID"},
		{"表示名が空", func(r *Reservation) { r.DisplayName = "" }, "表示名"},
		{"着手日時が空", func(r *Reservation) { r.StartedAt = time.Time{} }, "着手日時"},
		{"解除者なしの解除", func(r *Reservation) { r.ReleasedAt = now }, "解除者"},
		{"対象が不正", func(r *Reservation) { r.Target = "ids" }, "予約できない対象"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base
			c.mutate(&e)
			r := &Reservations{Reservations: []Reservation{e}}
			err := r.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("拒否されない・理由が違う: %v", err)
			}
		})
	}

	dup := &Reservations{Reservations: []Reservation{base, base}}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "重複") {
		t.Errorf("同じ作業者・同じ対象の未解除エントリの重複が拒否されない: %v", err)
	}
}

// 他メンバーの排他予約だけが警告の対象（自分の排他・他人の並行は対象外）。
func TestReservationsExclusiveBy(t *testing.T) {
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	r := &Reservations{Reservations: []Reservation{
		{Target: ReservationTerms, Mode: ReservationExclusive, AuthorID: "y.suzuki@example.co.jp",
			DisplayName: "鈴木", StartedAt: now},
		{Target: ReservationRoster, Mode: ReservationConcurrent, AuthorID: "y.suzuki@example.co.jp",
			DisplayName: "鈴木", StartedAt: now},
		{Target: ReservationMembers, Mode: ReservationExclusive, AuthorID: "k.sato@example.co.jp",
			DisplayName: "佐藤", StartedAt: now},
		{Target: ReservationPerspectives, Mode: ReservationExclusive, AuthorID: "y.suzuki@example.co.jp",
			DisplayName: "鈴木", StartedAt: now, ReleasedAt: now.Add(time.Minute), ReleasedBy: "y.suzuki@example.co.jp"},
	}}
	self := "k.sato@example.co.jp"
	if holder, ok := r.ExclusiveBy(ReservationTerms, self); !ok || holder.DisplayName != "鈴木" {
		t.Errorf("他メンバーの排他予約が検出されない: %+v %v", holder, ok)
	}
	if _, ok := r.ExclusiveBy(ReservationRoster, self); ok {
		t.Error("他メンバーの並行が排他として検出された")
	}
	if _, ok := r.ExclusiveBy(ReservationMembers, self); ok {
		t.Error("自分の排他予約が警告対象になった")
	}
	if _, ok := r.ExclusiveBy(ReservationPerspectives, self); ok {
		t.Error("解除済みの排他予約が警告対象になった")
	}
	if got := r.Active(); len(got) != 3 {
		t.Errorf("未解除の件数が違う: %d", len(got))
	}
}

// reservations.yaml の往復（空・解除済みを含む）で情報が失われない。
func TestReservationsRoundTrip(t *testing.T) {
	empty := &Reservations{}
	out, err := empty.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "reservations: []\n" {
		t.Errorf("空の reservations.yaml の形が違う: %q", out)
	}
	back, err := UnmarshalReservations(out)
	if err != nil || len(back.Reservations) != 0 {
		t.Fatalf("空の往復に失敗: %+v %v", back, err)
	}

	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	r := &Reservations{Reservations: []Reservation{
		{Target: "requirement:FR-INV-001", Mode: ReservationExclusive, AuthorID: "k.sato@example.co.jp",
			DisplayName: "佐藤", StartedAt: now},
		{Target: ReservationTerms, Mode: ReservationConcurrent, AuthorID: "y.suzuki@example.co.jp",
			DisplayName: "鈴木", StartedAt: now, ReleasedAt: now.Add(time.Hour), ReleasedBy: "k.sato@example.co.jp"},
	}}
	out, err = r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"target: requirement:FR-INV-001", "mode: exclusive", "released_by: k.sato@example.co.jp"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q が書き出されていない:\n%s", want, text)
		}
	}
	// 未解除エントリに released_at / released_by を書かない（未解除は空）。
	if strings.Count(text, "released_at") != 1 || strings.Count(text, "released_by") != 1 {
		t.Errorf("解除情報の出方が違う:\n%s", text)
	}
	// ハートビート・残留判定を持たない（以前の文書ロックの heartbeat_at を廃止）。
	if strings.Contains(text, "heartbeat") {
		t.Errorf("予約にハートビートが含まれている:\n%s", text)
	}
	back, err = UnmarshalReservations(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Reservations) != 2 || !back.Reservations[0].Active() || back.Reservations[1].Active() ||
		!back.Reservations[1].ReleasedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("往復後の内容が違う: %+v", back.Reservations)
	}
}
