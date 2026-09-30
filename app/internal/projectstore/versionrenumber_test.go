package projectstore

import (
	"testing"
	"time"
)

func v(n int, at time.Time, by string) DocumentVersion {
	return DocumentVersion{Version: n, ConfirmedAt: at, ConfirmedBy: by}
}

// 衝突した版番号は confirmed_at の早い方が保持し、同時刻は confirmed_by の辞書順。
// 保持しない側は未使用の最小版番号へ付け替える。
func TestPlanVersionRenumber(t *testing.T) {
	early := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	late := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	sato, suzuki := "k.sato@example.co.jp", "y.suzuki@example.co.jp"

	cases := []struct {
		name   string
		mine   []DocumentVersion
		theirs []DocumentVersion
		want   []VersionRenumber
	}{
		{
			name:   "衝突なし",
			mine:   []DocumentVersion{v(1, early, sato)},
			theirs: []DocumentVersion{v(1, early, sato), v(2, late, suzuki)},
			want:   nil,
		},
		{
			name:   "自分が早い（保持）",
			mine:   []DocumentVersion{v(1, early, sato), v(2, early, sato)},
			theirs: []DocumentVersion{v(1, early, sato), v(2, late, suzuki)},
			want:   nil,
		},
		{
			name:   "自分が遅い（付け替え。未使用の最小へ）",
			mine:   []DocumentVersion{v(1, early, sato), v(2, late, sato)},
			theirs: []DocumentVersion{v(1, early, sato), v(2, early, suzuki)},
			want:   []VersionRenumber{{Kind: "requirements", From: 2, To: 3}},
		},
		{
			name:   "同時刻は利用者 ID の辞書順（自分が先 = 保持）",
			mine:   []DocumentVersion{v(2, late, sato)},
			theirs: []DocumentVersion{v(2, late, suzuki)},
			want:   nil,
		},
		{
			name:   "同時刻は利用者 ID の辞書順（自分が後 = 付け替え）",
			mine:   []DocumentVersion{v(2, late, suzuki)},
			theirs: []DocumentVersion{v(2, late, sato)},
			want:   []VersionRenumber{{Kind: "requirements", From: 2, To: 1}},
		},
		{
			name:   "複数衝突は版番号の昇順で処理し、付け替え先が重ならない",
			mine:   []DocumentVersion{v(1, late, suzuki), v(2, late, suzuki)},
			theirs: []DocumentVersion{v(1, early, sato), v(2, early, sato)},
			want: []VersionRenumber{
				{Kind: "requirements", From: 1, To: 3},
				{Kind: "requirements", From: 2, To: 4},
			},
		},
		{
			name:   "確定者が記録されていない側（データ形式 1.4 より前）は後ろに置く",
			mine:   []DocumentVersion{v(1, late, "")},
			theirs: []DocumentVersion{v(1, late, sato)},
			want:   []VersionRenumber{{Kind: "requirements", From: 1, To: 2}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlanVersionRenumber(DocKindRequirements, c.mine, c.theirs)
			if len(got) != len(c.want) {
				t.Fatalf("件数が違う: got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("%d 件目: got %+v, want %+v", i+1, got[i], c.want[i])
				}
			}
		})
	}
}

// 同じ入力に対して結果が一意に定まる（決定的であること）。
func TestPlanVersionRenumberIsDeterministic(t *testing.T) {
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	mine := []DocumentVersion{v(3, at, "z@example.co.jp"), v(1, at, "z@example.co.jp"), v(2, at, "z@example.co.jp")}
	theirs := []DocumentVersion{v(1, at, "a@example.co.jp"), v(2, at, "a@example.co.jp"), v(3, at, "a@example.co.jp")}
	first := PlanVersionRenumber(DocKindBasicDesign, mine, theirs)
	for i := 0; i < 5; i++ {
		got := PlanVersionRenumber(DocKindBasicDesign, mine, theirs)
		if len(got) != len(first) {
			t.Fatalf("実行のたびに結果が変わる: %+v / %+v", got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("実行のたびに結果が変わる: %+v / %+v", got, first)
			}
		}
	}
	// 双方が同じ規則で判断するため、相手側の計画と合わせて番号が重複しない。
	theirPlan := PlanVersionRenumber(DocKindBasicDesign, theirs, mine)
	if len(theirPlan) != 0 {
		t.Errorf("辞書順で先の側が付け替えられた: %+v", theirPlan)
	}
	if len(first) != 3 || first[0].To != 4 || first[1].To != 5 || first[2].To != 6 {
		t.Errorf("付け替え先が未使用の最小になっていない: %+v", first)
	}
}
