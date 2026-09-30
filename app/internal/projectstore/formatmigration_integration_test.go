//go:build integration

package projectstore

// データ形式 1.4 への移行。
//
// 1.4 の追加は任意フィールドと追加ファイルだけ（project.yaml の `sync` / `reservations.yaml` /
// `id-ranges.yaml` / `versions.md` の `confirmed_by`・`confirmed_at`）であり、
// 既存プロジェクトはデータの作り替えなしに開けること・自版より新しい minor の未知フィールドを
// 書き戻しで落とさないことを、実ファイルで確かめる。

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setFormatVersion は project.yaml の形式バージョンだけを差し替える（他の内容は変えない）。
func setFormatVersion(t *testing.T, root, version string) {
	t.Helper()
	path := filepath.Join(root, FileProject)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(string(before), `format_version: "`+CurrentFormatVersion+`"`, `format_version: "`+version+`"`, 1)
	if after == string(before) {
		t.Fatalf("形式バージョンを差し替えられませんでした:\n%s", before)
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 1.3 のプロジェクトは、データの作り替えなしに
// 移行して開ける。移行前の退避が残り、旧バージョンが migrated_from に記録される。
func TestMigrateFrom13KeepsExistingData(t *testing.T) {
	s := createTestProject(t)
	root, projectID := s.Root(), s.Project().ProjectID
	// 1.3 の時点で存在した内容（要件項目・セッション）を置く
	if err := s.WriteFile("sessions/S-0001.md", []byte("---\nid: S-0001\n---\n本文\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile("requirements/FR-INV-001.md", []byte("---\nid: FR-INV-001\n---\n在庫を数える\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// 1.4 で追加されたファイルは 1.3 のプロジェクトには無い（追加ファイルなしで開けること）
	if err := os.Remove(filepath.Join(root, FileReservations)); err != nil {
		t.Fatal(err)
	}
	setFormatVersion(t, root, "1.3")

	if _, err := Open(root, testAuthor()); err == nil {
		t.Fatal("移行前に開けてしまった")
	}
	paths := AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
	backupPath, err := Migrate(paths, root, time.Date(2026, 9, 3, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("1.3 からの移行に失敗: %v", err)
	}
	peeked, err := PeekBackupProjectID(backupPath)
	if err != nil {
		t.Fatalf("移行前の退避を読めません: %v", err)
	}
	if peeked.FormatVersion != "1.3" {
		t.Errorf("退避が移行前（1.3）のデータでない: %q", peeked.FormatVersion)
	}
	if peeked.ProjectID != projectID {
		t.Errorf("退避のプロジェクトが違う: %q", peeked.ProjectID)
	}

	reopened, err := Open(root, testAuthor())
	if err != nil {
		t.Fatalf("移行後に開けない: %v", err)
	}
	defer reopened.Close()
	p := reopened.Project()
	if p.FormatVersion != "1.4" {
		t.Errorf("形式が 1.4 になっていない: %q", p.FormatVersion)
	}
	if p.MigratedFrom != "1.3" {
		t.Errorf("migrated_from が 1.3 でない: %q", p.MigratedFrom)
	}
	if p.ProjectID != projectID || p.TargetSystemName != "在庫管理システム" {
		t.Errorf("移行でプロジェクトの識別が変わった: %q %q", p.ProjectID, p.TargetSystemName)
	}
	// 既存データが作り替えられていない
	for _, rel := range []string{"sessions/S-0001.md", "requirements/FR-INV-001.md"} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s が失われた: %v", rel, err)
		}
		if !strings.Contains(string(b), "id: ") {
			t.Errorf("%s の内容が壊れている: %q", rel, string(b))
		}
	}
}

// 自版より新しい minor は未知フィールドを保持したまま読み書きする。
// 版番号を自版へ引き下げない（新しい版のアプリが書いた情報を壊さない）。
func TestNewerMinorKeepsUnknownFieldsOnWrite(t *testing.T) {
	s := createTestProject(t)
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	setFormatVersion(t, root, "1.5")
	path := filepath.Join(root, FileProject)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(before, []byte("future_flag: true\nfuture_map:\n  nested: 値\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root, testAuthor())
	if err != nil {
		t.Fatalf("新しい minor のプロジェクトを開けない: %v", err)
	}
	defer reopened.Close()
	if err := reopened.UpdateProject(func(p *Project) error {
		p.Summary = "移行後も未知フィールドを残す"
		return nil
	}); err != nil {
		t.Fatalf("書き戻しに失敗: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(after)
	for _, want := range []string{"future_flag: true", "nested: 値", `format_version: "1.5"`, "移行後も未知フィールドを残す"} {
		if !strings.Contains(got, want) {
			t.Errorf("書き戻しで %q が失われた:\n%s", want, got)
		}
	}
}

// 自動退避は `.git/` を含めず、手動バックアップは含める。
func TestBackupGitDirectoryInclusion(t *testing.T) {
	s := createTestProject(t)
	root, projectID := s.Root(), s.Project().ProjectID
	// 同期の管理情報を模した中身（実際の git 操作は同期モジュール側で検証する）
	if err := os.MkdirAll(filepath.Join(root, ".git", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		".git/HEAD":            "ref: refs/heads/work/k.sato\n",
		".git/refs/keep":       "0123456789abcdef\n",
		"sessions/S-0001.md":   "---\nid: S-0001\n---\n本文\n",
		"requirements/keep.md": "---\nid: FR-INV-001\n---\n在庫\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 手動バックアップ: `.git/` を含む（別端末への移送・障害復旧のため）
	manual := filepath.Join(t.TempDir(), "manual.zip")
	if err := WriteManualBackupZip(root, manual); err != nil {
		t.Fatalf("手動バックアップに失敗: %v", err)
	}
	manualNames := zipEntryNames(t, manual)
	for _, want := range []string{".git/HEAD", ".git/refs/keep", "sessions/S-0001.md"} {
		if !manualNames[want] {
			t.Errorf("手動バックアップに %s が含まれない", want)
		}
	}

	// 自動退避: `.git/` を含まない（10 世代の容量。取り直しは同期先から行う）
	paths := AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
	autoPath, err := AutoBackup(paths, root, projectID, time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("自動退避に失敗: %v", err)
	}
	autoNames := zipEntryNames(t, autoPath)
	for name := range autoNames {
		if name == ".git/" || strings.HasPrefix(name, ".git/") {
			t.Errorf("自動退避に同期の管理情報が含まれる: %s", name)
		}
	}
	for _, want := range []string{"sessions/S-0001.md", "requirements/keep.md", FileProject} {
		if !autoNames[want] {
			t.Errorf("自動退避に %s が含まれない", want)
		}
	}
}

// zipEntryNames は zip に含まれるエントリ名の集合を返す。
func zipEntryNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("zip を開けません（%s）: %v", path, err)
	}
	defer r.Close()
	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	return names
}
