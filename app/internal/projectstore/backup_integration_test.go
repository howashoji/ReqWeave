//go:build integration

package projectstore

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 1 回の操作で単一ファイルへ出力し、そこから全データを復元できること。
// バックアップ・エクスポート: 出力 → 別環境で取り込み → 内容が一致すること。
func TestBackupAndRestoreRoundTrip(t *testing.T) {
	s := createTestProject(t)
	if err := s.WriteFile("sessions/S-0001.md", []byte("---\nid: S-0001\n---\n本文\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile("decisions/DEC-001.md", []byte("---\nid: DEC-001\n---\n決定\n")); err != nil {
		t.Fatal(err)
	}
	// ロックは持ち運ばない
	lock, err := s.AcquireLock(LockDocumentsRequirements)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	dst := filepath.Join(t.TempDir(), "backup.zip")
	if err := WriteManualBackupZip(s.Root(), dst); err != nil {
		t.Fatalf("バックアップに失敗: %v", err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() == 0 {
		t.Fatalf("単一ファイルが出力されていない: %v", err)
	}

	// 標準 zip として読めること（OS 標準機能で展開できる形式）
	r, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatalf("標準 zip として開けない: %v", err)
	}
	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
		if strings.HasPrefix(f.Name, lockDir+"/") || f.Name == lockDir+"/" {
			t.Errorf("バックアップに locks/ が含まれる: %s", f.Name)
		}
	}
	r.Close()
	for _, want := range []string{FileProject, FileMembers, "sessions/S-0001.md", "decisions/DEC-001.md"} {
		if !names[want] {
			t.Errorf("バックアップに %s が含まれない", want)
		}
	}

	// 復元: 新しいフォルダへ展開して開ける
	restored := filepath.Join(t.TempDir(), "restored")
	project, err := RestoreZip(dst, restored)
	if err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if project.ProjectID != s.Project().ProjectID {
		t.Errorf("project_id が一致しない: %q", project.ProjectID)
	}
	for _, rel := range []string{"sessions/S-0001.md", "decisions/DEC-001.md"} {
		got, err := os.ReadFile(filepath.Join(restored, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s が復元されていない: %v", rel, err)
			continue
		}
		want, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s の内容が出力時点と一致しない", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(restored, lockDir)); err == nil {
		if entries := mustReadDir(t, filepath.Join(restored, lockDir)); len(entries) > 0 {
			t.Errorf("復元先に保持中のロックが持ち込まれた: %v", entries)
		}
	}
	reopened, err := Open(restored, testAuthor())
	if err != nil {
		t.Fatalf("復元したプロジェクトを開けない: %v", err)
	}
	defer reopened.Close()
}

// 復元は既存フォルダを上書きしない（既存データの破壊防止）。
func TestRestoreRefusesNonEmptyDir(t *testing.T) {
	s := createTestProject(t)
	dst := filepath.Join(t.TempDir(), "backup.zip")
	if err := WriteManualBackupZip(s.Root(), dst); err != nil {
		t.Fatal(err)
	}
	existing := t.TempDir()
	if err := os.WriteFile(filepath.Join(existing, "keep.txt"), []byte("大事なファイル"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreZip(dst, existing); err == nil {
		t.Fatal("空でないフォルダへ復元できてしまった")
	}
	if _, err := os.Stat(filepath.Join(existing, "keep.txt")); err != nil {
		t.Errorf("既存ファイルが失われた: %v", err)
	}
}

// 閉じるたびに退避し、直近 10 世代を保持して古い順に削除する。
func TestAutoBackupKeepsTenGenerations(t *testing.T) {
	s := createTestProject(t)
	paths := AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
	projectID := s.Project().ProjectID

	base := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	for i := 0; i < BackupGenerationsMax+3; i++ {
		if _, err := AutoBackup(paths, s.Root(), projectID, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("%d 回目の退避に失敗: %v", i, err)
		}
	}
	gens, err := ListBackupGenerations(paths, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != BackupGenerationsMax {
		t.Fatalf("保持世代数が違う: %d（期待 %d）", len(gens), BackupGenerationsMax)
	}
	// 新しい順に並び、古い 3 世代が削除されていること
	for i := 1; i < len(gens); i++ {
		if !gens[i].CreatedAt.Before(gens[i-1].CreatedAt) {
			t.Errorf("新しい順に並んでいない: %v %v", gens[i-1].CreatedAt, gens[i].CreatedAt)
		}
	}
	oldest := gens[len(gens)-1].CreatedAt
	if !oldest.Equal(base.Add(3 * time.Minute)) {
		t.Errorf("古い世代が削除されていない: 最古 %v", oldest)
	}
}

// 閉じる操作のたびに退避する。
func TestCloseAndBackup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatal(err)
	}
	paths := AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
	projectID := s.Project().ProjectID

	path, err := s.CloseAndBackup(paths)
	if err != nil {
		t.Fatalf("閉じるときの退避に失敗: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("退避ファイルがない: %v", err)
	}
	gens, err := ListBackupGenerations(paths, projectID)
	if err != nil || len(gens) != 1 {
		t.Fatalf("世代一覧が違う: %d 件 %v", len(gens), err)
	}
	// 閉じたあとは書き込めない
	if err := s.UpdateProject(func(p *Project) error { return nil }); err == nil {
		t.Error("閉じた後に書き込めた")
	}
}

// 復元前に project_id を確認できる（重複判定・確認表示）。
func TestPeekBackupProjectID(t *testing.T) {
	s := createTestProject(t)
	dst := filepath.Join(t.TempDir(), "backup.zip")
	if err := WriteManualBackupZip(s.Root(), dst); err != nil {
		t.Fatal(err)
	}
	peeked, err := PeekBackupProjectID(dst)
	if err != nil {
		t.Fatalf("確認に失敗: %v", err)
	}
	if peeked.ProjectID != s.Project().ProjectID || peeked.TargetSystemName != "在庫管理システム" {
		t.Errorf("確認内容が違う: %+v", peeked)
	}
}

// 「複製として保持」する場合は別の project_id を与える（同一性は project_id で判定）。
func TestReassignProjectID(t *testing.T) {
	s := createTestProject(t)
	dst := filepath.Join(t.TempDir(), "backup.zip")
	if err := WriteManualBackupZip(s.Root(), dst); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreZip(dst, restored); err != nil {
		t.Fatal(err)
	}
	newID, err := ReassignProjectID(restored, testAuthor())
	if err != nil {
		t.Fatalf("再採番に失敗: %v", err)
	}
	if newID == s.Project().ProjectID {
		t.Error("project_id が変わっていない")
	}
	reopened, err := Open(restored, testAuthor())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Project().ProjectID != newID {
		t.Errorf("再採番が保存されていない: %q", reopened.Project().ProjectID)
	}
}

// zip 内の相対パスによる復元先の外への書き出しを拒否する。
func TestRestoreRejectsPathTraversal(t *testing.T) {
	src := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dst := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreZip(src, dst); err == nil {
		t.Fatal("復元先の外へ書き出す zip が受理された")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escaped.txt")); err == nil {
		t.Error("復元先の外にファイルが作られた")
	}
}

// 移行は「退避 → 移行 → migrated_from の記録」の順で行う。
func TestMigrateOlderFormat(t *testing.T) {
	s := createTestProject(t)
	root := s.Root()
	projectID := s.Project().ProjectID
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, FileProject)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(string(before), `format_version: "`+CurrentFormatVersion+`"`, `format_version: "0.9"`, 1)
	if err := os.WriteFile(path, []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, testAuthor()); err == nil {
		t.Fatal("移行前に開けてしまった")
	}

	paths := AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
	backupPath, err := Migrate(paths, root, time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("移行に失敗: %v", err)
	}

	// (1) 移行前のデータが退避されている
	peeked, err := PeekBackupProjectID(backupPath)
	if err != nil {
		t.Fatalf("退避を読めません: %v", err)
	}
	if peeked.FormatVersion != "0.9" {
		t.Errorf("退避が移行前のデータでない: %q", peeked.FormatVersion)
	}
	gens, err := ListBackupGenerations(paths, projectID)
	if err != nil || len(gens) != 1 {
		t.Fatalf("退避世代が作られていない: %d 件 %v", len(gens), err)
	}

	// (2)(3) 自版へ移行し、旧バージョンを記録している
	reopened, err := Open(root, testAuthor())
	if err != nil {
		t.Fatalf("移行後に開けない: %v", err)
	}
	defer reopened.Close()
	p := reopened.Project()
	if p.FormatVersion != CurrentFormatVersion {
		t.Errorf("形式が移行されていない: %q", p.FormatVersion)
	}
	if p.MigratedFrom != "0.9" {
		t.Errorf("migrated_from が記録されていない: %q", p.MigratedFrom)
	}

	// 移行の必要がない形式では移行しない
	if _, err := Migrate(paths, root, time.Now()); err == nil {
		t.Error("移行不要な形式で移行が実行された")
	}
}
