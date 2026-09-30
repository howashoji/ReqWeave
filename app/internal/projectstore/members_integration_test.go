//go:build integration

// 結合テスト（メンバー管理 × 実ファイル・ロック）。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newMemberStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 追加・権限変更・削除が members.yaml へ原子的に書き込まれる。
func TestMemberLifecycle(t *testing.T) {
	s := newMemberStore(t)

	added, err := s.AddMember("Y.Suzuki@Example.co.jp", "鈴木", RoleEditor)
	if err != nil {
		t.Fatalf("追加に失敗: %v", err)
	}
	// 利用者 ID は正規化して保持する。
	if added.AuthorID != "y.suzuki@example.co.jp" {
		t.Fatalf("利用者 ID が正規化されていない: %q", added.AuthorID)
	}
	if added.Role != RoleEditor || added.AddedBy != testAuthor().AuthorID || added.AddedAt.IsZero() {
		t.Fatalf("登録内容が違う: %+v", added)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), FileMembers))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "y.suzuki@example.co.jp") {
		t.Fatalf("members.yaml へ書き込まれていない:\n%s", data)
	}

	// 同じ利用者 ID の重複追加を拒否する（大文字小文字の違いを含む）。
	if _, err := s.AddMember("y.suzuki@example.co.jp", "鈴木", RoleViewer); err == nil {
		t.Error("重複する利用者 ID が追加できた")
	} else if !strings.Contains(err.Error(), "既にメンバー") {
		// 追加の前に弾く（members.yaml の検証まで落ちると内部ファイル名を含む文言になる）。
		t.Errorf("重複の拒否理由が利用者向けでない: %v", err)
	}
	if _, err := s.AddMember("Y.SUZUKI@EXAMPLE.CO.JP", "鈴木", RoleViewer); err == nil {
		t.Error("大文字違いの重複が追加できた")
	} else if !strings.Contains(err.Error(), "既にメンバー") {
		t.Errorf("大文字違いの重複が正規化前に弾かれていない: %v", err)
	}
	if _, err := s.AddMember("y.tanaka@example.co.jp", " ", RoleEditor); err == nil {
		t.Error("表示名なしが追加できた")
	}
	if _, err := s.AddMember("y.tanaka@example.co.jp", "田中", "admin"); err == nil {
		t.Error("値集合外の権限が追加できた")
	}

	before, after, err := s.ChangeMemberRole(added.AuthorID, RoleViewer)
	if err != nil {
		t.Fatalf("権限変更に失敗: %v", err)
	}
	if before.Role != RoleEditor || after.Role != RoleViewer {
		t.Fatalf("変更前後が違う: %+v → %+v", before, after)
	}

	removed, err := s.RemoveMember(added.AuthorID)
	if err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if removed.AuthorID != added.AuthorID {
		t.Fatalf("削除結果が違う: %+v", removed)
	}
	members, err := s.LoadMembers()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := members.Find(added.AuthorID); ok {
		t.Error("削除したメンバーが残っている")
	}
	if _, err := s.RemoveMember("z.sato@example.co.jp"); err == nil {
		t.Error("未登録のメンバーが削除できた")
	}
}

// 最後のオーナーは削除も降格もできない（オーナー 0 名を作らない）。
func TestMemberLastOwnerProtected(t *testing.T) {
	s := newMemberStore(t)
	owner := testAuthor().AuthorID

	if _, err := s.RemoveMember(owner); err == nil {
		t.Fatal("最後のオーナーが削除できた")
	} else if !strings.Contains(err.Error(), "移譲") {
		t.Errorf("次の行動が案内されていない: %v", err)
	}
	if _, _, err := s.ChangeMemberRole(owner, RoleEditor); err == nil {
		t.Fatal("最後のオーナーが降格できた")
	} else if !strings.Contains(err.Error(), "移譲") {
		// 保存前の検証で弾き、次の行動（移譲）を案内する。
		// members.yaml の検証（最後の砦）まで落ちると内部ファイル名を含む文言になる。
		t.Errorf("降格の拒否理由が次の行動を案内していない: %v", err)
	}

	// 別のオーナーを立てれば移譲・降格できる。
	if _, err := s.AddMember("y.suzuki@example.co.jp", "鈴木", RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ChangeMemberRole(owner, RoleEditor); err != nil {
		t.Fatalf("移譲後の降格に失敗: %v", err)
	}
	members, err := s.LoadMembers()
	if err != nil {
		t.Fatal(err)
	}
	if members.countOwners() != 1 {
		t.Fatalf("オーナー数が違う: %d", members.countOwners())
	}
}

// オーナー不在時に編集権限のメンバーが引き継げる。
func TestMemberTakeOverOwner(t *testing.T) {
	s := newMemberStore(t)
	// オーナーが去った状態を作る（オーナー在籍のまま編集者を追加し、ファイル側で権限を落とす）。
	if _, err := s.AddMember("y.suzuki@example.co.jp", "鈴木", RoleEditor); err != nil {
		t.Fatal(err)
	}
	editor, err := Open(s.Root(), Author{AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = editor.Close() })

	// 引き継ぎ前は編集権限。
	before, err := editor.LoadMembers()
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := before.Find("y.suzuki@example.co.jp"); m.Role != RoleEditor {
		t.Fatalf("前提が崩れている: %+v", m)
	}

	taken, err := editor.TakeOverOwner()
	if err != nil {
		t.Fatalf("引き継ぎに失敗: %v", err)
	}
	if taken.Role != RoleOwner || taken.AuthorID != "y.suzuki@example.co.jp" {
		t.Fatalf("引き継ぎ結果が違う: %+v", taken)
	}
	// 既存のオーナーは降格しない（オーナー不在の状況を作らない）。
	after, err := editor.LoadMembers()
	if err != nil {
		t.Fatal(err)
	}
	if after.countOwners() != 2 {
		t.Errorf("既存オーナーが降格された: %+v", after.Members)
	}
	if _, err := editor.TakeOverOwner(); err == nil {
		t.Error("オーナーが重ねて引き継げた")
	}
}

// 利用者 ID の訂正で権限・登録日時・登録者が変わらない。
func TestMemberCorrectAuthorID(t *testing.T) {
	s := newMemberStore(t)
	added, err := s.AddMember("y.suzuki@example.co.jp", "鈴木", RoleEditor)
	if err != nil {
		t.Fatal(err)
	}

	before, after, err := s.CorrectAuthorID(added.AuthorID, "Y.Tanaka@Example.co.jp")
	if err != nil {
		t.Fatalf("訂正に失敗: %v", err)
	}
	if before.AuthorID != "y.suzuki@example.co.jp" || after.AuthorID != "y.tanaka@example.co.jp" {
		t.Fatalf("訂正前後が違う: %+v → %+v", before, after)
	}
	if after.Role != before.Role || !after.AddedAt.Equal(before.AddedAt) || after.AddedBy != before.AddedBy {
		t.Errorf("権限・登録日時・登録者が変わった: %+v → %+v", before, after)
	}
	if _, _, err := s.CorrectAuthorID("y.tanaka@example.co.jp", testAuthor().AuthorID); err == nil {
		t.Error("既存メンバーと重複する訂正ができた")
	}
	if _, _, err := s.CorrectAuthorID("y.tanaka@example.co.jp", "y.tanaka@example.co.jp"); err == nil {
		t.Error("同一 ID への訂正ができた")
	}
	if _, _, err := s.CorrectAuthorID("z.sato@example.co.jp", "new@example.co.jp"); err == nil {
		t.Error("未登録のメンバーを訂正できた")
	}
}

// メンバー管理の書き込みは members ロック内で行う。
func TestMemberWritesWaitForMembersLock(t *testing.T) {
	s := newMemberStore(t)
	existing, err := s.AddMember("y.suzuki@example.co.jp", "鈴木", RoleEditor)
	if err != nil {
		t.Fatal(err)
	}

	s.lockPolicy.shortTimeout = 200 * time.Millisecond
	lock, err := s.AcquireLock(LockMembers)
	if err != nil {
		t.Fatalf("ロックを取得できません: %v", err)
	}

	if _, err := s.AddMember("y.tanaka@example.co.jp", "田中", RoleViewer); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に追加が成立しました")
	}
	if _, _, err := s.ChangeMemberRole(existing.AuthorID, RoleViewer); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に権限変更が成立しました")
	}
	if _, err := s.RemoveMember(existing.AuthorID); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に削除が成立しました")
	}
	if _, _, err := s.CorrectAuthorID(existing.AuthorID, "new@example.co.jp"); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に訂正が成立しました")
	}

	// 部分書き込みが起きていない。
	members, err := s.LoadMembers()
	if err != nil {
		t.Fatal(err)
	}
	if len(members.Members) != 2 {
		t.Fatalf("部分書き込みが発生しています: %+v", members.Members)
	}
	if m, _ := members.Find(existing.AuthorID); m.Role != RoleEditor {
		t.Fatalf("権限が書き換わっています: %+v", m)
	}

	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMember("y.tanaka@example.co.jp", "田中", RoleViewer); err != nil {
		t.Fatalf("解放後の追加に失敗: %v", err)
	}
}
