package auditlog

// 単体テスト（利用量の集計ロジック。実ファイル I/O を伴わない純粋部分 = AggregateSends）。
// 実ファイルからの集計（AggregateUsage / TotalUsage）は audit_integration_test.go で検証する。

import (
	"testing"
	"time"
)

func ptr(n int) *int { return &n }

// jst は表示・期間指定のローカル暦日（保存は UTC・期間はローカルの暦日で指定する規則）を固定するための位置。
var jst = time.FixedZone("JST", 9*60*60)

// fixture は期間 2026-08-01〜2026-08-31（JST）の内外にまたがる送信記録。
// 期待値は本フィクスチャの定義から手で導出する（集計実装の出力をコピーしない）。
func fixture() []AISendRecord {
	at := func(day, hour int) time.Time { return time.Date(2026, 8, day, hour, 0, 0, 0, jst) }
	return []AISendRecord{
		// 期間内: anthropic / S-0001 / 佐藤 — 実績あり（10 + 20 + 5 = 35）
		{ID: "a1", At: at(3, 10), Author: "k.sato@example.co.jp", Provider: "anthropic",
			Session: "S-0001", Prompt: "p", TokensIn: ptr(10), TokensOut: ptr(20), TokensReasoning: ptr(5)},
		// 期間内: anthropic / S-0001 / 鈴木 — 実績あり（1 + 2 + 0 = 3）
		{ID: "a2", At: at(4, 9), Author: "t.suzuki@example.co.jp", Provider: "anthropic",
			Session: "S-0001", Prompt: "p", TokensIn: ptr(1), TokensOut: ptr(2), TokensReasoning: ptr(0)},
		// 期間内: openai / セッション外（取り込み分析）/ 佐藤 — 欠測（実績行なし）
		{ID: "a3", At: at(5, 12), Author: "k.sato@example.co.jp", Provider: "openai", Prompt: "p"},
		// 期間内: openai / S-0002 / 佐藤 — 実績が明示的に 0（欠測ではない）
		{ID: "a4", At: at(6, 8), Author: "k.sato@example.co.jp", Provider: "openai",
			Session: "S-0002", Prompt: "p", TokensIn: ptr(0), TokensOut: ptr(0), TokensReasoning: ptr(0)},
		// 期間外（8/31 23:59:59 より後 = 9/1）: 集計に入らない
		{ID: "a5", At: time.Date(2026, 9, 1, 0, 0, 0, 0, jst), Author: "k.sato@example.co.jp",
			Provider: "google", Session: "S-0003", Prompt: "p", TokensIn: ptr(1000), TokensOut: ptr(1000)},
		// 期間外（7/31）: 集計に入らない
		{ID: "a0", At: time.Date(2026, 7, 31, 23, 59, 59, 0, jst), Author: "k.sato@example.co.jp",
			Provider: "google", Session: "S-0000", Prompt: "p", TokensIn: ptr(500)},
	}
}

func aug2026() (time.Time, time.Time) {
	return time.Date(2026, 8, 1, 0, 0, 0, 0, jst),
		time.Date(2026, 8, 31, 23, 59, 59, 999999999, jst)
}

// 期間内の合計と 3 軸の内訳。期待値はフィクスチャから手で導出したもの。
func TestAggregateSendsTotalsAndBreakdown(t *testing.T) {
	from, to := aug2026()
	got := AggregateSends(fixture(), from, to)

	if want := (TokenTotals{In: 11, Out: 22, Reasoning: 5, Total: 38}); got.Tokens != want {
		t.Errorf("合計が違う: got %+v, want %+v", got.Tokens, want)
	}
	if got.Sends != 4 {
		t.Errorf("期間内の送信件数が違う: got %d, want 4", got.Sends)
	}
	if want := time.Date(2026, 8, 6, 8, 0, 0, 0, jst); !got.LastUsedAt.Equal(want) {
		t.Errorf("直近の利用日時が違う: got %v, want %v", got.LastUsedAt, want)
	}

	wantProvider := []UsageBucket{
		{Key: "anthropic", Tokens: TokenTotals{In: 11, Out: 22, Reasoning: 5, Total: 38}, Sends: 2},
		{Key: "openai", Tokens: TokenTotals{}, Sends: 2, Missing: 1},
	}
	assertBuckets(t, "プロバイダ別", got.ByProvider, wantProvider)

	wantSession := []UsageBucket{
		{Key: "S-0001", Tokens: TokenTotals{In: 11, Out: 22, Reasoning: 5, Total: 38}, Sends: 2},
		{Key: "", Tokens: TokenTotals{}, Sends: 1, Missing: 1},
		{Key: "S-0002", Tokens: TokenTotals{}, Sends: 1},
	}
	assertBuckets(t, "対話セッション別", got.BySession, wantSession)

	wantAuthor := []UsageBucket{
		{Key: "k.sato@example.co.jp", Tokens: TokenTotals{In: 10, Out: 20, Reasoning: 5, Total: 35}, Sends: 3, Missing: 1},
		{Key: "t.suzuki@example.co.jp", Tokens: TokenTotals{In: 1, Out: 2, Total: 3}, Sends: 1},
	}
	assertBuckets(t, "作業者別", got.ByAuthor, wantAuthor)
}

// 欠測は 0 として合算し、件数を別に示す（実績 0 と区別する）。
func TestAggregateSendsMissingIsCountedNotDropped(t *testing.T) {
	from, to := aug2026()
	got := AggregateSends(fixture(), from, to)
	if got.Missing != 1 {
		t.Errorf("欠測件数が違う: got %d, want 1（a3 のみ。実績が明示 0 の a4 は欠測ではない）", got.Missing)
	}
	if got.Sends != 4 {
		t.Errorf("欠測を落として件数を減らしている: got %d, want 4", got.Sends)
	}
	// 欠測を落とさないことの反証: 欠測レコードを除いたときだけ件数が減ること
	withoutMissing := []AISendRecord{}
	for _, r := range fixture() {
		if r.ID != "a3" {
			withoutMissing = append(withoutMissing, r)
		}
	}
	if s := AggregateSends(withoutMissing, from, to); s.Sends != 3 || s.Missing != 0 {
		t.Errorf("欠測を除いた集計が違う: sends=%d missing=%d, want 3 / 0", s.Sends, s.Missing)
	}
}

// 期間の境界は含む。境界外は含まない（ローカル暦日で判定する）。
func TestAggregateSendsPeriodBoundaryIsInclusive(t *testing.T) {
	first := time.Date(2026, 8, 1, 0, 0, 0, 0, jst)
	last := time.Date(2026, 8, 31, 23, 59, 59, 999999999, jst)
	sends := []AISendRecord{
		{ID: "b0", At: first.Add(-time.Nanosecond), Provider: "anthropic", Prompt: "p", TokensIn: ptr(100)},
		{ID: "b1", At: first, Provider: "anthropic", Prompt: "p", TokensIn: ptr(1)},
		{ID: "b2", At: last, Provider: "anthropic", Prompt: "p", TokensIn: ptr(2)},
		{ID: "b3", At: last.Add(time.Nanosecond), Provider: "anthropic", Prompt: "p", TokensIn: ptr(200)},
	}
	got := AggregateSends(sends, first, last)
	if got.Sends != 2 || got.Tokens.Total != 3 {
		t.Errorf("境界の扱いが違う: sends=%d total=%d, want 2 / 3", got.Sends, got.Tokens.Total)
	}
}

// 期間指定なし（ゼロ値）は全期間累計。上限判定はこの値を使う。
func TestAggregateSendsWithoutPeriodIsAllTime(t *testing.T) {
	got := AggregateSends(fixture(), time.Time{}, time.Time{})
	// 38（8月分）+ 2000（9/1）+ 500（7/31）= 2538
	if got.Tokens.Total != 2538 {
		t.Errorf("全期間累計が違う: got %d, want 2538", got.Tokens.Total)
	}
	if got.Sends != 6 {
		t.Errorf("全期間の送信件数が違う: got %d, want 6", got.Sends)
	}
}

// 表示を安定させるため、内訳は消費の多い順・同値はキーの昇順で固定する。
func TestSortedBucketsIsDeterministic(t *testing.T) {
	sends := []AISendRecord{
		{ID: "c1", Provider: "zeta", Prompt: "p", TokensIn: ptr(5)},
		{ID: "c2", Provider: "alpha", Prompt: "p", TokensIn: ptr(5)},
		{ID: "c3", Provider: "beta", Prompt: "p", TokensIn: ptr(9)},
	}
	got := AggregateSends(sends, time.Time{}, time.Time{}).ByProvider
	want := []string{"beta", "alpha", "zeta"}
	for i, w := range want {
		if got[i].Key != w {
			t.Fatalf("並び順が違う: got %v, want %v", keys(got), want)
		}
	}
}

func keys(bs []UsageBucket) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Key)
	}
	return out
}

func assertBuckets(t *testing.T, axis string, got, want []UsageBucket) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%sの行数が違う: got %v, want %v", axis, keys(got), keys(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%sの %d 行目が違う: got %+v, want %+v", axis, i+1, got[i], want[i])
		}
	}
}
