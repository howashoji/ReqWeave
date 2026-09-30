//go:build integration

// 結合テスト（`locks/` = 同一端末の多重プロセス保護。一覧と残留の解除）。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 保持中のロックが対象・取得日時つきで一覧でき、残留と自分のウィンドウが示される。
// ロックファイルは作業者名を持たない（同一端末内の事象）。
func TestHeldLocksListsStaleAndSelfInstance(t *testing.T) {
	s := newMemberStore(t)

	if got, err := s.HeldLocks(); err != nil || len(got) != 0 {
		t.Fatalf("初期状態でロックがある: %+v %v", got, err)
	}

	lock, err := s.AcquireLock(LockDocumentsRequirements)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	got, err := s.HeldLocks()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("一覧の件数が違う: %+v", got)
	}
	if got[0].Target != LockDocumentsRequirements {
		t.Errorf("対象が違う: %+v", got[0])
	}
	if got[0].Holder.AppInstanceID != AppInstanceID() || got[0].Holder.AcquiredAt.IsZero() || !got[0].SelfInstance {
		t.Errorf("起動 ID・取得日時・自分のウィンドウの判定が取れていない: %+v", got[0])
	}
	if got[0].Stale {
		t.Errorf("取得直後のロックが残留と判定された: %+v", got[0])
	}
	raw, err := os.ReadFile(s.lockPath(LockDocumentsRequirements))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"author_id", "display_name", "佐藤", testAuthor().AuthorID} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("ロックファイルに作業者情報 %q が含まれている（ロックファイルは作業者名を持たない）:\n%s", forbidden, raw)
		}
	}

	// heartbeat が古い・別ウィンドウのロックは残留として示される（解除はされない）。
	writeLockFile(t, s, LockTerms, LockInfo{
		AppInstanceID: "11111111-1111-4111-8111-111111111111",
		AcquiredAt:    time.Now().UTC().Add(-time.Hour), HeartbeatAt: time.Now().UTC().Add(-time.Hour),
	})
	got, err = s.HeldLocks()
	if err != nil {
		t.Fatal(err)
	}
	var stale *HeldLock
	for i := range got {
		if got[i].Target == LockTerms {
			stale = &got[i]
		}
	}
	if stale == nil || !stale.Stale || stale.SelfInstance {
		t.Fatalf("残留ロックが残留（別ウィンドウ）と示されない: %+v", got)
	}
	if _, err := os.Stat(s.lockPath(LockTerms)); err != nil {
		t.Errorf("残留判定だけでロックが消えた（自動解除しないこと）: %v", err)
	}
}

// 残留の解除でロックが解け、解除後に同じ対象を取得できる。
func TestForceReleaseLock(t *testing.T) {
	s := newMemberStore(t)
	writeLockFile(t, s, LockDocumentsRequirements, LockInfo{
		AppInstanceID: "11111111-1111-4111-8111-111111111111",
		AcquiredAt:    time.Now().UTC().Add(-time.Hour), HeartbeatAt: time.Now().UTC().Add(-time.Hour),
	})

	// 解除前は取得できず、利用者向けの文言は「別のウィンドウが処理中」（作業者名を出さない）。
	_, err := s.tryAcquireLock(LockDocumentsRequirements, false)
	if err == nil {
		t.Fatal("保持中のロックを取得できてしまった")
	}
	if err.Error() != ErrOtherWindowBusy {
		t.Errorf("文言が違う: %q", err.Error())
	}

	holder, err := s.ForceReleaseLock(LockDocumentsRequirements)
	if err != nil {
		t.Fatalf("解除に失敗: %v", err)
	}
	if holder.AppInstanceID == "" || holder.AcquiredAt.IsZero() {
		t.Errorf("解除時点のロック内容が返らない: %+v", holder)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), lockDir, LockDocumentsRequirements+lockExt)); !os.IsNotExist(err) {
		t.Errorf("ロックファイルが残っている: %v", err)
	}

	// 解除後は通常どおり取得できる。
	lock, err := s.AcquireLock(LockDocumentsRequirements)
	if err != nil {
		t.Fatalf("解除後に取得できない: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}

	// 既に解放済み・対象未指定は理由つきで拒否する。
	if _, err := s.ForceReleaseLock(LockDocumentsRequirements); err == nil {
		t.Error("解放済みのロックを解除できた")
	}
	if _, err := s.ForceReleaseLock(" "); err == nil {
		t.Error("対象未指定の解除が受理された")
	}
}

// プロジェクトを開かずにロックの有無だけを判定できる（削除前の確認）。
func TestAnyLockHeld(t *testing.T) {
	s := newMemberStore(t)
	if AnyLockHeld(s.Root()) {
		t.Fatal("ロックが無いのに保持中と判定された")
	}
	lock, err := s.AcquireLock(LockDocumentsBasicDesign)
	if err != nil {
		t.Fatal(err)
	}
	if !AnyLockHeld(s.Root()) {
		t.Error("保持中のロックが判定されない")
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if AnyLockHeld(s.Root()) {
		t.Error("解放後も保持中と判定された")
	}
}
