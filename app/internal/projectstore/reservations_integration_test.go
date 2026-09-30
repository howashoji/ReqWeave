//go:build integration

// 結合テスト（予約と作業状況。実ファイル I/O）。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 着手時に選んだ進め方が作業状況として作業コピー（reservations.yaml）に記録される。
func TestSetReservationRecordsModeInWorkingCopy(t *testing.T) {
	s := newMemberStore(t)

	if got, err := s.LoadReservations(); err != nil || len(got.Active()) != 0 {
		t.Fatalf("初期状態で予約がある: %+v %v", got, err)
	}

	set, err := s.SetReservation(ReservationDocumentsRequirements, ReservationExclusive)
	if err != nil {
		t.Fatalf("予約に失敗: %v", err)
	}
	if set.AuthorID != testAuthor().AuthorID || set.DisplayName != testAuthor().DisplayName ||
		set.Mode != ReservationExclusive || set.StartedAt.IsZero() || !set.Active() {
		t.Errorf("記録された予約が違う: %+v", set)
	}
	if set.StartedAt.Location() != time.UTC {
		t.Errorf("着手日時が UTC ではない: %v", set.StartedAt)
	}

	data, err := os.ReadFile(filepath.Join(s.Root(), FileReservations))
	if err != nil {
		t.Fatalf("reservations.yaml が無い: %v", err)
	}
	for _, want := range []string{"target: documents-requirements", "mode: exclusive", "author_id: k.sato@example.co.jp"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%q が書かれていない:\n%s", want, data)
		}
	}

	// 同じ対象への再設定は進め方だけを更新し、着手日時を保つ（重複エントリを作らない）。
	again, err := s.SetReservation(ReservationDocumentsRequirements, ReservationConcurrent)
	if err != nil {
		t.Fatal(err)
	}
	if again.Mode != ReservationConcurrent || !again.StartedAt.Equal(set.StartedAt) {
		t.Errorf("再設定の結果が違う: %+v（初回 %+v）", again, set)
	}
	list, err := s.LoadReservations()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Reservations) != 1 {
		t.Errorf("同じ対象の再設定でエントリが増えた: %+v", list.Reservations)
	}

	// 別の対象（個別レコード）も同時に持てる。
	if _, err := s.SetReservation("requirement:FR-INV-001", ReservationExclusive); err != nil {
		t.Fatalf("個別レコードの予約に失敗: %v", err)
	}
	if got, _ := s.LoadReservations(); len(got.Active()) != 2 {
		t.Errorf("未解除の件数が違う: %+v", got.Active())
	}

	// 予約はプロジェクトを開き直しても残る（作業コピーに永続化 = 同期対象）。
	_ = s.Close()
	reopened, err := Open(s.Root(), testAuthor())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, _ := reopened.LoadReservations(); len(got.Active()) != 2 {
		t.Errorf("開き直すと予約が消えた: %+v", got.Active())
	}
}

// 他メンバーが排他で予約している対象へも着手（並行の宣言）できる（拒否しない）。
// 着手した場合は自分の名義で concurrent のエントリが追加される。
func TestSetReservationDoesNotRejectOthersExclusive(t *testing.T) {
	s := newMemberStore(t)
	// 他メンバーの排他予約が取り込まれた状態を模す。
	other := Reservation{
		Target: ReservationTerms, Mode: ReservationExclusive,
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木",
		StartedAt: time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC),
	}
	if err := s.SaveReservations(&Reservations{Reservations: []Reservation{other}}); err != nil {
		t.Fatal(err)
	}

	list, _ := s.LoadReservations()
	holder, reserved := list.ExclusiveBy(ReservationTerms, s.Author().AuthorID)
	if !reserved || holder.DisplayName != "鈴木" {
		t.Fatalf("他メンバーの排他予約が見えない: %+v", list)
	}

	mine, err := s.SetReservation(ReservationTerms, ReservationConcurrent)
	if err != nil {
		t.Fatalf("予約された対象への着手が拒否された（禁止しないこと）: %v", err)
	}
	if mine.Mode != ReservationConcurrent || mine.AuthorID != s.Author().AuthorID {
		t.Errorf("並行の宣言が違う: %+v", mine)
	}
	list, _ = s.LoadReservations()
	if len(list.Active()) != 2 {
		t.Errorf("他メンバーの予約が消えた・重複した: %+v", list.Active())
	}
	if _, still := list.ExclusiveBy(ReservationTerms, s.Author().AuthorID); !still {
		t.Error("着手で他メンバーの排他予約が上書きされた")
	}
}

// 本人の解除と他メンバーの予約の解除（released_at / released_by を書く）。
func TestReleaseReservation(t *testing.T) {
	s := newMemberStore(t)
	if _, err := s.SetReservation(ReservationRoster, ReservationExclusive); err != nil {
		t.Fatal(err)
	}
	other := Reservation{
		Target: ReservationRoster, Mode: ReservationExclusive,
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木",
		StartedAt: time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC),
	}
	list, _ := s.LoadReservations()
	list.Reservations = append(list.Reservations, other)
	if err := s.SaveReservations(list); err != nil {
		t.Fatal(err)
	}

	// 本人の解除。
	released, err := s.ReleaseReservation(ReservationRoster, s.Author().AuthorID)
	if err != nil {
		t.Fatalf("本人の解除に失敗: %v", err)
	}
	if released.Active() || released.ReleasedBy != s.Author().AuthorID {
		t.Errorf("解除情報が違う: %+v", released)
	}

	// 他メンバーの予約の解除（released_by が予約者と異なる = 変更履歴で追跡できる）。
	released, err = s.ReleaseReservation(ReservationRoster, "Y.Suzuki@Example.co.jp")
	if err != nil {
		t.Fatalf("他メンバーの予約の解除に失敗: %v", err)
	}
	if released.AuthorID != "y.suzuki@example.co.jp" || released.ReleasedBy != s.Author().AuthorID {
		t.Errorf("他メンバーの解除情報が違う: %+v", released)
	}

	list, _ = s.LoadReservations()
	if len(list.Active()) != 0 {
		t.Errorf("解除後も未解除が残る: %+v", list.Active())
	}
	if len(list.Reservations) != 2 {
		t.Errorf("解除でエントリが消えた（履歴として残すこと）: %+v", list.Reservations)
	}

	// 解除済み・存在しない予約の解除は理由つきで拒否する。
	if _, err := s.ReleaseReservation(ReservationRoster, s.Author().AuthorID); err == nil ||
		!strings.Contains(err.Error(), "既に解除") {
		t.Errorf("解除済みの再解除が拒否されない・理由が違う: %v", err)
	}
	if _, err := s.ReleaseReservation(" ", s.Author().AuthorID); err == nil {
		t.Error("対象未指定の解除が受理された")
	}

	// 解除後に同じ対象を再び予約できる（新しいエントリ）。
	if _, err := s.SetReservation(ReservationRoster, ReservationExclusive); err != nil {
		t.Fatalf("解除後の再予約に失敗: %v", err)
	}
	list, _ = s.LoadReservations()
	if len(list.Active()) != 1 || len(list.Reservations) != 3 {
		t.Errorf("再予約後の内容が違う: %+v", list.Reservations)
	}
}

// 予約はハートビート・残留判定を持たず、経過時間で失効しない。
func TestReservationDoesNotExpire(t *testing.T) {
	s := newMemberStore(t)
	old := Reservation{
		Target: ReservationMembers, Mode: ReservationExclusive,
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木",
		StartedAt: time.Now().UTC().Add(-30 * 24 * time.Hour),
	}
	if err := s.SaveReservations(&Reservations{Reservations: []Reservation{old}}); err != nil {
		t.Fatal(err)
	}
	list, err := s.LoadReservations()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := list.ExclusiveBy(ReservationMembers, s.Author().AuthorID); !ok {
		t.Error("30 日前の予約が失効扱いになった（時間失効を持たないこと）")
	}
	// データ形式 1.4 より前のプロジェクト（reservations.yaml なし）は空として読める。
	if err := os.Remove(filepath.Join(s.Root(), FileReservations)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LoadReservations(); err != nil || len(got.Reservations) != 0 {
		t.Errorf("ファイル無しを空として読めない: %+v %v", got, err)
	}
	if got := ActiveReservationsAt(s.Root()); len(got) != 0 {
		t.Errorf("ファイル無しで作業状況が出た: %+v", got)
	}
}

// 予約の書き込みは records ロックで直列化され、同一端末の 2 プロセス（2 Store）の同時設定でも
// エントリが失われない。
func TestSetReservationSerializedAcrossStores(t *testing.T) {
	s := newMemberStore(t)
	other, err := Open(s.Root(), Author{AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, st := range []*Store{s, other} {
		st.lockPolicy.retryMin = 5 * time.Millisecond
		st.lockPolicy.retryMax = 20 * time.Millisecond
	}
	targets := []string{ReservationTerms, ReservationRoster, ReservationPerspectives, "decision:DEC-001"}
	done := make(chan error, len(targets)*2)
	for _, st := range []*Store{s, other} {
		for _, target := range targets {
			go func(st *Store, target string) {
				_, err := st.SetReservation(target, ReservationConcurrent)
				done <- err
			}(st, target)
		}
	}
	for i := 0; i < len(targets)*2; i++ {
		if err := <-done; err != nil {
			t.Errorf("同時設定に失敗: %v", err)
		}
	}
	list, err := s.LoadReservations()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(list.Active()); got != len(targets)*2 {
		t.Errorf("同時設定でエントリが失われた: %d（期待 %d）: %+v", got, len(targets)*2, list.Active())
	}
	if _, ok := s.LockState(LockRecords); ok {
		t.Error("短時間ロックが解放されていない")
	}
}

// 再入による自己待ちの再発検知: 短時間ロックを保持したまま採番する経路（観点・名簿）が、
// 二重取得で待たずに完了すること。待ち時間を詰めた Store で見るため、退行すると即座に落ちる。
//
// 短時間ロックは O_EXCL のファイルロックで再入できない。ロック内から `AllocateID` を呼ぶと
// 自分の解放を待ってタイムアウトする（`ids` ロックを廃止し `records` に集約したため、
// この二重取得が起きやすい）。
func TestAllocateInsideShortLockDoesNotSelfDeadlock(t *testing.T) {
	s := newMemberStore(t)
	s.lockPolicy.shortTimeout = 500 * time.Millisecond
	s.lockPolicy.retryMin = 10 * time.Millisecond
	s.lockPolicy.retryMax = 50 * time.Millisecond

	// records ロック内での採番（観点）。
	added, err := s.AddPerspective("在庫の引当単位", "引当を行う単位を確認する", PerspectiveOriginManual, "")
	if err != nil {
		t.Fatalf("records ロック内の採番が完了しない（二重取得の疑い）: %v", err)
	}
	if added.ID != IDPerspective.Format(1) {
		t.Errorf("採番が違う: %q", added.ID)
	}
	// roster ロック内での採番（名簿）。
	stk, err := s.AddStakeholder("佐藤", "営業部")
	if err != nil {
		t.Fatalf("roster ロック内の採番が完了しない（二重取得の疑い）: %v", err)
	}
	if stk.ID != IDStakeholder.Format(1) {
		t.Errorf("採番が違う: %q", stk.ID)
	}
	// 採番後にロックが残らない（次の操作が待たされない）。
	for _, target := range []string{LockRecords, LockRoster} {
		if _, held := s.LockState(target); held {
			t.Errorf("採番後にロックが残っている: %s", target)
		}
	}
	// 連続採番が続けて通る（1 回目だけ通る実装を弾く）。
	if second, err := s.AddPerspective("在庫の棚卸周期", "棚卸の周期を確認する", PerspectiveOriginManual, ""); err != nil {
		t.Fatalf("2 件目の採番に失敗: %v", err)
	} else if second.ID != IDPerspective.Format(2) {
		t.Errorf("2 件目の採番が違う: %q", second.ID)
	}
}
