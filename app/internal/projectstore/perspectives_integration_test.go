//go:build integration

// 結合テスト（実ファイル I/O・ロック）。実行: make -C app test-integration

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newPerspectiveStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 観点が perspectives.yaml へ保存され、採番は PRS-nnn の連番。
func TestAddPerspectiveWritesFile(t *testing.T) {
	s := newPerspectiveStore(t)

	first, err := s.AddPerspective("棚卸の差異処理", "差異の確定者と締め時刻を確認する", PerspectiveOriginManual, "")
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if first.ID != "PRS-001" || first.Origin != PerspectiveOriginManual {
		t.Fatalf("登録結果が違う: %+v", first)
	}
	if first.Author != testAuthor().AuthorID || first.CreatedAt.IsZero() {
		t.Fatalf("登録者・登録日時が入っていない: %+v", first)
	}
	second, err := s.AddPerspective("入出庫の権限", "誰が入出庫を確定できるかを確認する",
		PerspectiveOriginImport, "IMP-002#L18-L40")
	if err != nil {
		t.Fatalf("2 件目の登録に失敗: %v", err)
	}
	if second.ID != "PRS-002" || second.Evidence != "IMP-002#L18-L40" {
		t.Fatalf("2 件目の登録結果が違う: %+v", second)
	}

	data, err := os.ReadFile(filepath.Join(s.Root(), FilePerspectives))
	if err != nil {
		t.Fatalf("perspectives.yaml を読めない: %v", err)
	}
	body := string(data)
	for _, want := range []string{"id: PRS-001", "id: PRS-002", "origin: manual", "origin: import",
		"evidence: IMP-002#L18-L40", "name: 棚卸の差異処理"} {
		if !strings.Contains(body, want) {
			t.Fatalf("perspectives.yaml に %q がない:\n%s", want, body)
		}
	}

	active, err := s.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("一覧の件数が違う: %+v", active)
	}
}

// origin: import の evidence 欠落は書き込み前に拒否する。
func TestAddPerspectiveRejectsImportWithoutEvidence(t *testing.T) {
	s := newPerspectiveStore(t)

	if _, err := s.AddPerspective("入出庫の権限", "誰が確定できるか", PerspectiveOriginImport, ""); err == nil {
		t.Fatalf("根拠なしの取り込み由来が受理された")
	} else if !strings.Contains(err.Error(), "該当箇所") {
		t.Fatalf("理由が示されていない: %v", err)
	}
	if _, err := s.AddPerspective("入出庫の権限", "誰が確定できるか", PerspectiveOriginImport, "IMP-2#L1"); err == nil {
		t.Fatalf("根拠の形式違反が受理された")
	}
	if _, err := s.AddPerspective("入出庫の権限", "誰が確定できるか", "auto", ""); err == nil {
		t.Fatalf("値集合外の由来が受理された")
	}
	if _, err := s.AddPerspective("", "誰が確定できるか", PerspectiveOriginManual, ""); err == nil {
		t.Fatalf("観点名なしが受理された")
	}
	if _, err := s.AddPerspective("入出庫の権限", " ", PerspectiveOriginManual, ""); err == nil {
		t.Fatalf("要旨なしが受理された")
	}

	// 拒否された登録はファイルへ残らない（ID も消費されない）。
	active, err := s.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("拒否した観点が保存されている: %+v", active)
	}
	next, err := s.NextID(IDPerspective)
	if err != nil {
		t.Fatal(err)
	}
	if next != "PRS-001" {
		t.Fatalf("拒否で ID が消費された: %q", next)
	}
}

// 観点名・要旨の編集。由来・根拠・登録者・登録日時は変えない。
func TestUpdatePerspective(t *testing.T) {
	s := newPerspectiveStore(t)
	added, err := s.AddPerspective("棚卸の差異", "差異の確定者を確認する", PerspectiveOriginImport, "IMP-002#L18-L40")
	if err != nil {
		t.Fatal(err)
	}

	before, after, err := s.UpdatePerspective(added.ID, "棚卸の差異処理", "差異の確定者と締め時刻を確認する")
	if err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	if before.Name != "棚卸の差異" || after.Name != "棚卸の差異処理" {
		t.Fatalf("変更前後が違う: before=%+v after=%+v", before, after)
	}
	if after.Origin != added.Origin || after.Evidence != added.Evidence ||
		!after.CreatedAt.Equal(added.CreatedAt) || after.Author != added.Author {
		t.Fatalf("登録時点の事実が書き換わっている: %+v", after)
	}
	if after.TopicKey() != added.TopicKey() {
		t.Fatalf("論点キーが変わった: %q → %q", added.TopicKey(), after.TopicKey())
	}

	if _, _, err := s.UpdatePerspective("PRS-999", "名", "要旨"); err == nil {
		t.Fatalf("未登録の観点が変更できた")
	}
	if _, _, err := s.UpdatePerspective(added.ID, "", "要旨"); err == nil {
		t.Fatalf("観点名なしの変更が受理された")
	}
}

// 削除は論理削除。実体は残り、PRS-nnn は再採番されない。
func TestRemovePerspectiveIsSoftDeleteAndKeepsID(t *testing.T) {
	s := newPerspectiveStore(t)
	for _, name := range []string{"観点1", "観点2", "観点3"} {
		if _, err := s.AddPerspective(name, "要旨", PerspectiveOriginManual, ""); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.RemovePerspective("PRS-002")
	if err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if removed.DeletedAt.IsZero() || removed.DeletedBy != testAuthor().AuthorID {
		t.Fatalf("論理削除の印が立っていない: %+v", removed)
	}

	// 実体は残る（物理削除の経路を持たない）。
	all, err := s.LoadPerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Perspectives) != 3 {
		t.Fatalf("エントリが消えている: %+v", all.Perspectives)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), FilePerspectives))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: PRS-002") || !strings.Contains(string(data), "deleted_by:") {
		t.Fatalf("論理削除がファイルへ反映されていない:\n%s", data)
	}

	// 一覧からは外れる。
	active, err := s.ActivePerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != "PRS-001" || active[1].ID != "PRS-003" {
		t.Fatalf("削除済みが一覧に残っている: %+v", active)
	}

	// 削除 API 経由でも削除済み ID は再採番されない（再採番で論点キーが別の観点を指す不具合の回帰）。
	next, err := s.AddPerspective("観点4", "要旨", PerspectiveOriginManual, "")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != "PRS-004" {
		t.Fatalf("削除済み ID が再採番された: %q（期待 PRS-004）", next.ID)
	}

	// 削除済みは編集・再削除できない。
	if _, _, err := s.UpdatePerspective("PRS-002", "名", "要旨"); err == nil {
		t.Fatalf("削除済みの観点が編集できた")
	}
	if _, err := s.RemovePerspective("PRS-002"); err == nil {
		t.Fatalf("削除済みの観点が再削除できた")
	}
	if _, err := s.RemovePerspective("PRS-999"); err == nil {
		t.Fatalf("未登録の観点が削除できた")
	}
}

// 観点の書き込みは records ロック内で行う
// （保持者がいる間は追加・編集・削除のいずれも書き込まない）。
func TestPerspectiveWritesWaitForRecordsLock(t *testing.T) {
	s := newPerspectiveStore(t)
	existing, err := s.AddPerspective("棚卸の差異処理", "要旨", PerspectiveOriginManual, "")
	if err != nil {
		t.Fatal(err)
	}

	s.lockPolicy.shortTimeout = 200 * time.Millisecond
	lock, err := s.AcquireLock(LockRecords)
	if err != nil {
		t.Fatalf("ロックを取得できません: %v", err)
	}

	if _, err := s.AddPerspective("入出庫の権限", "要旨", PerspectiveOriginManual, ""); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に登録が成立しました")
	}
	if _, _, err := s.UpdatePerspective(existing.ID, "別名", "要旨"); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に変更が成立しました")
	}
	if _, err := s.RemovePerspective(existing.ID); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に削除が成立しました")
	}

	// 部分書き込みが起きていない（ロック待ちの間にファイルは変わらない）。
	all, err := s.LoadPerspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Perspectives) != 1 || all.Perspectives[0].Name != "棚卸の差異処理" ||
		all.Perspectives[0].Deleted() {
		t.Fatalf("部分書き込みが発生しています: %+v", all.Perspectives)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("ロックを解放できません: %v", err)
	}
	if _, err := s.AddPerspective("入出庫の権限", "要旨", PerspectiveOriginManual, ""); err != nil {
		t.Fatalf("解放後の登録に失敗: %v", err)
	}
}
