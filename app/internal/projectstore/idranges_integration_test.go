//go:build integration

// 結合テスト（ID の番号帯。実ファイル I/O）。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// 単独利用（同期先未設定）のプロジェクトは id-ranges.yaml を作らず、採番は最大値 + 1 のまま。
func TestSoloProjectKeepsMaxPlusOneNumbering(t *testing.T) {
	s := createTestProject(t)
	if s.UsesIDRanges() {
		t.Fatal("作成直後から番号帯モードになっている")
	}
	if _, err := os.Stat(filepath.Join(s.Root(), FileIDRanges)); !os.IsNotExist(err) {
		t.Fatalf("単独利用で %s が作られた: %v", FileIDRanges, err)
	}
	for i := 1; i <= 3; i++ {
		if got := createSession(t, s); got != IDSession.Format(i) {
			t.Fatalf("%d 件目の採番: got %q, want %q", i, got, IDSession.Format(i))
		}
	}
	if got, err := s.NextID(IDSession); err != nil || got != IDSession.Format(4) {
		t.Errorf("次の ID が最大値 + 1 でない: got %q, err %v", got, err)
	}
}

// 番号帯を確保すると以後の採番が自分の区間の中で行われ、区間を跨がない。
func TestAllocateFromReservedRange(t *testing.T) {
	s := createTestProject(t)
	// 他のメンバーが 1〜100 を確保済みの状態（取り込み済み）を作る。
	seedRanges(t, s, RangeSession, IDRange{AuthorID: "y.suzuki@example.co.jp", From: 1, To: 100,
		ReservedAt: fixedTime()})

	reserved, err := s.ReserveIDRanges(0, nil)
	if err != nil {
		t.Fatalf("番号帯を確保できない: %v", err)
	}
	if got := reserved[RangeSession]; got.From != 101 || got.To != 200 || got.AuthorID != testAuthor().AuthorID {
		t.Fatalf("確保した区間が違う: %+v", got)
	}
	if !s.UsesIDRanges() {
		t.Fatal("確保後も番号帯モードにならない")
	}
	// 全 8 種別が確保される。
	if len(reserved) != len(RangeKeys()) {
		t.Fatalf("確保した種別の数が違う: %d", len(reserved))
	}

	// 採番は自分の区間の未使用最小値から始まり、区間を跨がない。
	if got := createSession(t, s); got != IDSession.Format(101) {
		t.Fatalf("番号帯からの採番になっていない: %q", got)
	}
	if got := createSession(t, s); got != IDSession.Format(102) {
		t.Fatalf("2 件目の採番: %q", got)
	}
	if got, err := s.NextID(IDSession); err != nil || got != IDSession.Format(103) {
		t.Errorf("次の ID が違う: got %q, err %v", got, err)
	}
}

// 残り 20% で警告し、使い切ったら新規採番を止めて同期を促す。
func TestIDRangeWarnsAndStopsWhenExhausted(t *testing.T) {
	s := createTestProject(t)
	// 幅 50（許容範囲の下限）で確保し、48 件使う → 残り 2 件（4%）で警告。
	if _, err := s.ReserveIDRanges(50, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 48; i++ {
		createSession(t, s)
	}
	st := statusOf(t, s, RangeSession)
	if st.Reserved != 50 || st.Used != 48 || st.Remaining != 2 {
		t.Fatalf("残量の集計が違う: %+v", st)
	}
	if !st.Warn || st.Exhausted {
		t.Errorf("枯渇予告の判定が違う: %+v", st)
	}
	// 他の種別は使っていないので警告しない（種別ごとに判定する）。
	if other := statusOf(t, s, RangeDecision); other.Warn {
		t.Errorf("未使用の種別が警告になった: %+v", other)
	}

	createSession(t, s)
	createSession(t, s)
	st = statusOf(t, s, RangeSession)
	if !st.Exhausted || st.Remaining != 0 {
		t.Fatalf("使い切りの判定が違う: %+v", st)
	}
	// 使い切ったら採番せず、原因と次に取る行動を示す（重複を作らない）。
	_, err := s.AllocateID(IDSession, func(id string) error {
		t.Errorf("枯渇後に採番された: %s", id)
		return nil
	})
	if err == nil {
		t.Fatal("枯渇後に採番が成功した")
	}
	if !strings.Contains(err.Error(), "使い切った") || !strings.Contains(err.Error(), "同期") {
		t.Errorf("原因と次の行動が示されない: %v", err)
	}
	if strings.Contains(err.Error(), "session") {
		t.Errorf("内部の種別キーが利用者向け文言に出ている: %v", err)
	}

	// 再確保すれば続きから採番できる（番号は飛んでよい）。
	if _, err := s.ReserveIDRanges(50, nil); err != nil {
		t.Fatal(err)
	}
	if got := createSession(t, s); got != IDSession.Format(51) {
		t.Errorf("再確保後の採番が違う: %q", got)
	}
}

// 2 名が同期先へ到達できない状態で並行に記録を作っても ID が重複しない。
// 採番済みの ID は取り込み・マージで変化しない。
func TestOfflineParallelAllocationHasNoDuplicates(t *testing.T) {
	// 同じ作業コピー相当（取り込み済みの id-ranges.yaml を両者が持つ状態）を 2 つ作る。
	a := createTestProject(t)
	seedRanges(t, a, RangeRequirement,
		IDRange{AuthorID: testAuthor().AuthorID, From: 1, To: 100, ReservedAt: fixedTime()},
		IDRange{AuthorID: "y.suzuki@example.co.jp", From: 101, To: 200, ReservedAt: fixedTime()})
	seedRanges(t, a, RangeDecision,
		IDRange{AuthorID: testAuthor().AuthorID, From: 1, To: 100, ReservedAt: fixedTime()},
		IDRange{AuthorID: "y.suzuki@example.co.jp", From: 101, To: 200, ReservedAt: fixedTime()})

	b := createTestProject(t)
	copyFile(t, filepath.Join(a.Root(), FileIDRanges), filepath.Join(b.Root(), FileIDRanges))
	other, err := Open(b.Root(), Author{AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	// 双方がオフラインのまま 5 件ずつ作る（同期先には触れない）。
	var idsA, idsB []string
	for i := 0; i < 5; i++ {
		idsA = append(idsA, createRequirement(t, a, "INV"), createDecision(t, a))
		idsB = append(idsB, createRequirement(t, other, "INV"), createDecision(t, other))
	}

	seen := map[string]bool{}
	for _, id := range append(append([]string{}, idsA...), idsB...) {
		if seen[id] {
			t.Errorf("ID が重複した: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 20 {
		t.Fatalf("採番数が違う: %d", len(seen))
	}
	// 双方の ID が自分の区間の中にある（区間を跨がない）。
	for _, id := range idsA {
		if n := numberOf(t, id); n < 1 || n > 100 {
			t.Errorf("端末 A の ID が区間外: %s", id)
		}
	}
	for _, id := range idsB {
		if n := numberOf(t, id); n < 101 || n > 200 {
			t.Errorf("端末 B の ID が区間外: %s", id)
		}
	}

	// 取り込み（相手の実体を持ち込む）後も、採番済み ID は変化しない。
	for _, id := range idsB {
		if strings.HasPrefix(id, "DEC-") {
			copyFile(t, filepath.Join(other.Root(), dirDecisions, id+".md"),
				filepath.Join(a.Root(), dirDecisions, id+".md"))
			continue
		}
		copyFile(t, filepath.Join(other.Root(), filepath.FromSlash(RequirementFile(id))),
			filepath.Join(a.Root(), filepath.FromSlash(RequirementFile(id))))
	}
	for _, id := range idsA {
		path := filepath.Join(a.Root(), filepath.FromSlash(RequirementFile(id)))
		if strings.HasPrefix(id, "DEC-") {
			path = filepath.Join(a.Root(), dirDecisions, id+".md")
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("取り込み後に自分の ID の実体が失われた: %s: %v", id, err)
		}
	}
	// 取り込み後の次の採番も自分の区間の中（相手の区間を侵さない）。
	next := createRequirement(t, a, "INV")
	if n := numberOf(t, next); n < 1 || n > 100 {
		t.Errorf("取り込み後の採番が区間外: %s", next)
	}
}

// 番号帯が重なった状態（未反映のまま双方が確保した場合）でも、他の作業者の区間の番号は使わない。
func TestAllocationAvoidsOverlappingOthersRange(t *testing.T) {
	s := createTestProject(t)
	seedRanges(t, s, RangeSession,
		IDRange{AuthorID: testAuthor().AuthorID, From: 1, To: 100, ReservedAt: fixedTime()},
		// 相手が同じ区間を確保してしまった状態（本来は起きない。起きても重複 ID を作らない）。
		IDRange{AuthorID: "y.suzuki@example.co.jp", From: 1, To: 50, ReservedAt: fixedTime()})

	if got := createSession(t, s); got != IDSession.Format(51) {
		t.Errorf("他の作業者の区間と重なる番号を使った: %q", got)
	}
}

// 番号帯の確保は既存 ID の最大値も跨ぐ（確保直後に枯渇しない）。
func TestReserveSkipsAlreadyUsedNumbers(t *testing.T) {
	s := createTestProject(t)
	// 単独利用のまま 3 件作る（1〜3 を使用済み）。
	for i := 0; i < 3; i++ {
		createSession(t, s)
	}
	reserved, err := s.ReserveIDRanges(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reserved[RangeSession]; got.From != 4 {
		t.Errorf("既存 ID を跨いでいない: %+v", got)
	}
	if got := createSession(t, s); got != IDSession.Format(4) {
		t.Errorf("確保後の採番が違う: %q", got)
	}
}

// 連続する自分の区間はまとめる（同期のたびにエントリが増え続けない）。
func TestReserveCoalescesOwnContiguousRange(t *testing.T) {
	s := createTestProject(t)
	if _, err := s.ReserveIDRanges(100, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveIDRanges(100, nil); err != nil {
		t.Fatal(err)
	}
	ranges, err := s.LoadIDRanges()
	if err != nil {
		t.Fatal(err)
	}
	mine := ranges.For(RangeSession, testAuthor().AuthorID)
	if len(mine) != 1 || mine[0].From != 1 || mine[0].To != 200 {
		t.Fatalf("連続する自分の区間がまとまっていない: %+v", mine)
	}

	// 間に他のメンバーが入った場合はまとめず、別の区間として追加する。
	seedRanges(t, s, RangeSession, append(mine,
		IDRange{AuthorID: "y.suzuki@example.co.jp", From: 201, To: 300, ReservedAt: fixedTime()})...)
	if _, err := s.ReserveIDRanges(100, nil); err != nil {
		t.Fatal(err)
	}
	ranges, _ = s.LoadIDRanges()
	mine = ranges.For(RangeSession, testAuthor().AuthorID)
	if len(mine) != 2 || mine[1].From != 301 || mine[1].To != 400 {
		t.Fatalf("他メンバーの区間の後ろに確保していない: %+v", mine)
	}
}

// 桁あふれしても既存 ID は変わらず、以後はゼロ埋め桁を増やして採番する。
func TestAllocationAcrossDigitOverflow(t *testing.T) {
	s := createTestProject(t)
	seedRanges(t, s, RangeQuestionnaire, IDRange{AuthorID: testAuthor().AuthorID, From: 998, To: 1002,
		ReservedAt: fixedTime()})
	var got []string
	for i := 0; i < 5; i++ {
		id, err := s.AllocateID(IDQuestionnaire, func(id string) error {
			return s.WriteFile("questionnaires/"+id+"/meta.yaml", []byte("id: "+id+"\n"))
		})
		if err != nil {
			t.Fatalf("%d 件目の採番に失敗: %v", i+1, err)
		}
		got = append(got, id)
	}
	want := []string{"QS-998", "QS-999", "QS-1000", "QS-1001", "QS-1002"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d 件目: got %q, want %q", i+1, got[i], want[i])
		}
	}
	// 桁あふれ後の ID も走査で認識され、既存 ID は変わらない。
	if max, err := IDQuestionnaire.scanMax(s.Root(), IDQuestionnaire); err != nil || max != 1002 {
		t.Errorf("桁あふれした ID を走査できない: got %d, err %v", max, err)
	}
}

// ---- ヘルパ -----------------------------------------------------------------

func fixedTime() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }

func statusOf(t *testing.T, s *Store, key string) IDRangeStatus {
	t.Helper()
	list, err := s.IDRangeStatuses(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range list {
		if st.Key == key {
			return st
		}
	}
	t.Fatalf("対象種別が残量一覧に無い: %s", key)
	return IDRangeStatus{}
}

// seedRanges は取り込み済みの id-ranges.yaml を作る（同期で受け取った状態を模す）。
//
// 書き込みは検証を通さない（重なりのある状態＝取り込みでしか起こらない状態も作れるようにするため）。
func seedRanges(t *testing.T, s *Store, key string, entries ...IDRange) {
	t.Helper()
	ranges, err := s.LoadIDRanges()
	if err != nil {
		t.Fatal(err)
	}
	if ranges.Ranges == nil {
		ranges.Ranges = map[string][]IDRange{}
	}
	ranges.Ranges[key] = entries
	data, err := yaml.Marshal(ranges)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(filepath.Join(s.Root(), FileIDRanges), data); err != nil {
		t.Fatal(err)
	}
}

func createRequirement(t *testing.T, s *Store, group string) string {
	t.Helper()
	r, err := s.CreateRequirement(group, Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: RequirementFunctional, Priority: PriorityMust,
		Status: RequirementDraft, Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("要件項目を作成できない: %v", err)
	}
	return r.ID
}

func createDecision(t *testing.T, s *Store) string {
	t.Helper()
	d, err := s.CreateDecision(Decision{TopicKey: "custom/引当の起点", Body: "受注確定時に引き当てる。",
		Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("決定事項を作成できない: %v", err)
	}
	return d.ID
}

func numberOf(t *testing.T, id string) int {
	t.Helper()
	if _, _, n, ok := ParseRequirementID(id); ok {
		return n
	}
	if n, ok := IDDecision.Parse(id); ok {
		return n
	}
	t.Fatalf("ID を解釈できない: %s", id)
	return 0
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), dataDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, dataFileMode); err != nil {
		t.Fatal(err)
	}
}
