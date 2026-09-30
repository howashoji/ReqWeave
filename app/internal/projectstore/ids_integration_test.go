//go:build integration

package projectstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// createSession は sessions/S-nnnn.md を作る採番付き作成（採番＋実体作成）。
func createSession(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.AllocateID(IDSession, func(id string) error {
		return s.WriteFile("sessions/"+id+".md", []byte("---\nid: "+id+"\n---\n"))
	})
	if err != nil {
		t.Fatalf("採番に失敗: %v", err)
	}
	return id
}

// 採番は既存 ID の最大値 + 1。カウンタファイルを持たない。
func TestAllocateIDIsSequential(t *testing.T) {
	s := createTestProject(t)
	for i := 1; i <= 3; i++ {
		want := IDSession.Format(i)
		if got := createSession(t, s); got != want {
			t.Fatalf("%d 件目の採番: got %q, want %q", i, got, want)
		}
	}
	// カウンタファイルを作らない（実体との二重管理の禁止）
	for _, e := range mustReadDir(t, s.Root()) {
		if e == "ids.yaml" || e == "counters.yaml" || e == ".ids" {
			t.Errorf("採番カウンタらしきファイルがある: %s", e)
		}
	}
}

// 欠番は再利用しない。
func TestAllocateIDDoesNotReuseGaps(t *testing.T) {
	s := createTestProject(t)
	first := createSession(t, s)
	second := createSession(t, s)
	if err := os.Remove(filepath.Join(s.Root(), "sessions", first+".md")); err != nil {
		t.Fatal(err)
	}
	third := createSession(t, s)
	if third == first {
		t.Errorf("欠番 %q が再利用された", first)
	}
	if want := IDSession.Format(3); third != want {
		t.Errorf("採番が最大値 + 1 でない: got %q, want %q（既存 %q）", third, want, second)
	}
}

// 実体の作成に失敗したとき ID を消費しない（次回同じ番号が採番される）。
func TestAllocateIDNotConsumedWhenCreateFails(t *testing.T) {
	s := createTestProject(t)
	if _, err := s.AllocateID(IDSession, func(id string) error {
		return fmt.Errorf("作成に失敗（テスト）")
	}); err == nil {
		t.Fatal("create が失敗したのに採番が成功した")
	}
	if got := createSession(t, s); got != IDSession.Format(1) {
		t.Errorf("失敗した採番が消費された: got %q", got)
	}
}

// 同一端末の複数プロセス（別 Store）の同時採番でも ID が衝突しない
// （records ロックで直列化。端末間の衝突回避は番号帯）。
func TestConcurrentAllocateIDAcrossStores(t *testing.T) {
	s := createTestProject(t)
	other, err := Open(s.Root(), Author{AuthorID: "t.suzuki@example.co.jp", DisplayName: "鈴木"})
	if err != nil {
		t.Fatalf("2 つ目の Store を開けません: %v", err)
	}
	defer other.Close()

	// 排他の正しさを見るテストのため、待ち間隔だけ短くする（許容範囲外の値だが待ち時間の短縮のみが目的）
	for _, st := range []*Store{s, other} {
		st.lockPolicy.retryMin = 5 * time.Millisecond
		st.lockPolicy.retryMax = 20 * time.Millisecond
	}

	const n = 6
	ids := make(chan string, n*2)
	var wg sync.WaitGroup
	for _, st := range []*Store{s, other} {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(st *Store) {
				defer wg.Done()
				id, err := st.AllocateID(IDSession, func(id string) error {
					return st.WriteFile("sessions/"+id+".md", []byte("---\nid: "+id+"\n---\n"))
				})
				if err != nil {
					t.Errorf("採番に失敗: %v", err)
					return
				}
				ids <- id
			}(st)
		}
	}
	wg.Wait()
	close(ids)

	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("ID が重複した: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != n*2 {
		t.Errorf("採番数が違う: %d（期待 %d）", len(seen), n*2)
	}
	for i := 1; i <= n*2; i++ {
		if !seen[IDSession.Format(i)] {
			t.Errorf("連番に欠けがある: %s", IDSession.Format(i))
		}
	}
}

// 名簿・観点の ID は YAML の一覧から最大値を求める。
func TestNextIDFromYAMLCollections(t *testing.T) {
	s := createTestProject(t)
	if got, err := s.NextID(IDStakeholder); err != nil || got != "STK-001" {
		t.Errorf("空の名簿からの採番: got %q, err %v", got, err)
	}
	roster := "stakeholders:\n  - id: STK-001\n    name: 佐藤\n    org: 営業部\n  - id: STK-004\n    name: 鈴木\n    org: 製造部\n"
	if err := s.WriteFile(FileRoster, []byte(roster)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.NextID(IDStakeholder); err != nil || got != "STK-005" {
		t.Errorf("名簿からの採番: got %q, err %v", got, err)
	}

	perspectives := "perspectives:\n  - id: PRS-002\n    name: 観点\n"
	if err := s.WriteFile(FilePerspectives, []byte(perspectives)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.NextID(IDPerspective); err != nil || got != "PRS-003" {
		t.Errorf("観点からの採番: got %q, err %v", got, err)
	}
}

// 取得は O_EXCL。別のプロセス（Store）が保持している間は取得できず、
// 利用者向けの文言は「別のウィンドウが処理中」で作業者名を出さない。
func TestAcquireLockReportsOtherWindow(t *testing.T) {
	s := createTestProject(t)
	lock, err := s.AcquireLock(LockDocumentsRequirements)
	if err != nil {
		t.Fatalf("ロックを取得できません: %v", err)
	}
	defer lock.Release()

	other, err := Open(s.Root(), Author{AuthorID: "t.suzuki@example.co.jp", DisplayName: "鈴木"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	_, err = other.AcquireLock(LockDocumentsRequirements)
	if err == nil {
		t.Fatal("保持中のロックを二重に取得できてしまった")
	}
	var held *ErrLockHeld
	if !asErrLockHeld(err, &held) {
		t.Fatalf("ErrLockHeld ではない: %T %v", err, err)
	}
	if held.Holder.AppInstanceID != AppInstanceID() {
		t.Errorf("保持側の起動 ID が違う: %q", held.Holder.AppInstanceID)
	}
	if held.Holder.AcquiredAt.IsZero() {
		t.Error("取得日時が記録されていない")
	}
	if held.Stale {
		t.Error("取得直後のロックが残留と判定された")
	}
	if strings.Contains(err.Error(), "佐藤") || strings.Contains(err.Error(), "k.sato") {
		t.Errorf("文言に作業者情報が含まれている: %q", err.Error())
	}
	if err.Error() != ErrOtherWindowBusy {
		t.Errorf("文言が違う: %q", err.Error())
	}

	// 解放後は取得できる
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	lock2, err := other.AcquireLock(LockDocumentsRequirements)
	if err != nil {
		t.Fatalf("解放後に取得できない: %v", err)
	}
	if err := lock2.Release(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LockState(LockDocumentsRequirements); ok {
		t.Error("解放後もロックファイルが残っている")
	}
}

// heartbeat_at が閾値より古いロックは残留として報告する（自動解除はしない）。
func TestAcquireLockDetectsStaleHolder(t *testing.T) {
	s := createTestProject(t)
	stale := LockInfo{
		AppInstanceID: "11111111-1111-4111-8111-111111111111",
		AcquiredAt:    time.Now().UTC().Add(-2 * time.Hour),
		HeartbeatAt:   time.Now().UTC().Add(-2 * time.Hour),
	}
	writeLockFile(t, s, LockMembers, stale)

	_, err := s.AcquireLock(LockMembers)
	var held *ErrLockHeld
	if !asErrLockHeld(err, &held) {
		t.Fatalf("ErrLockHeld ではない: %T %v", err, err)
	}
	if !held.Stale {
		t.Error("残留ロックが残留と判定されない")
	}
	// 確認操作なしの自動解除をしない（ファイルが残っていること）
	if _, ok := s.LockState(LockMembers); !ok {
		t.Error("残留ロックが自動解除された（解除には確認操作を必須とする）")
	}
}

// 短時間ロックは待ってからタイムアウトし、再実行できる形で失敗する。
func TestWithShortLockWaitsThenTimesOut(t *testing.T) {
	s := createTestProject(t)
	s.lockPolicy.shortTimeout = 300 * time.Millisecond
	s.lockPolicy.retryMin = 20 * time.Millisecond
	s.lockPolicy.retryMax = 50 * time.Millisecond

	holder := LockInfo{
		AppInstanceID: "11111111-1111-4111-8111-111111111111",
		AcquiredAt:    time.Now().UTC(), HeartbeatAt: time.Now().UTC(),
	}
	writeLockFile(t, s, LockRecords, holder)

	start := time.Now()
	err := s.WithShortLock(LockRecords, func() error {
		t.Error("保持中なのに実行された")
		return nil
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("保持中のロックでタイムアウトしなかった")
	}
	if !strings.Contains(err.Error(), "別のウィンドウが処理中") {
		t.Errorf("タイムアウトの文言が違う（再実行を促す 1 文）: %v", err)
	}
	if elapsed < s.lockPolicy.shortTimeout {
		t.Errorf("待たずに失敗した: %v", elapsed)
	}
	if elapsed > 3*s.lockPolicy.shortTimeout {
		t.Errorf("待ちすぎ: %v", elapsed)
	}

	// 解放されれば実行できる
	if err := os.Remove(s.lockPath(LockRecords)); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := s.WithShortLock(LockRecords, func() error { ran = true; return nil }); err != nil {
		t.Fatalf("解放後に実行できない: %v", err)
	}
	if !ran {
		t.Error("解放後に fn が実行されていない")
	}
	if _, ok := s.LockState(LockRecords); ok {
		t.Error("短時間ロックが解放されていない")
	}
}

func writeLockFile(t *testing.T, s *Store, target string, info LockInfo) {
	t.Helper()
	data, err := marshalLockInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.lockPath(target)), dataDirMode); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(s.lockPath(target), data); err != nil {
		t.Fatal(err)
	}
}
