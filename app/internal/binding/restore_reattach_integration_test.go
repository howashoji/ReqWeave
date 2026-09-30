//go:build integration

package binding

// 自動退避からの復元と同期の取り直し。
//
// 自動退避の zip に同期の管理情報（`.git/`）を含めないため、そこから復元した作業コピーは
// 同期の操作の前に同期先から取り直す必要がある。復元の結果にその案内が出ることと、
// 手動バックアップには管理情報が含まれることを、バインディング層の経路で確かめる。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// makeSyncedProject は同期先が設定され、同期の記録と管理情報を持つ作業コピーを作る。
//
// 保存先（parent）を渡し、**実際に作られた場所**を返す（フォルダ名はアプリが決める）。
func makeSyncedProject(t *testing.T, a *API, parent string) string {
	t.Helper()
	dir := createTestProject(t, a, parent, "在庫管理システム")
	store, err := a.openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpdateProject(func(p *projectstore.Project) error {
		p.Sync = &projectstore.SyncSetting{
			Kind:     projectstore.SyncKindFolder,
			Location: filepath.Join(t.TempDir(), "share", "proj.git"),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	logger, err := auditlog.New(store, store.Author().AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordSync(auditlog.SyncRecord{
		At: time.Now().UTC(), Author: store.Author().AuthorID, Op: "publish",
		RemoteKind: "folder", Result: "ok",
	}); err != nil {
		t.Fatal(err)
	}
	// 同期の管理情報（実体は同期モジュールが作る。ここでは中身のあるフォルダで代用する）
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/work/k.sato\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 自動退避から復元した作業コピーには同期の管理情報が無く、
// 復元の結果に「同期先から取り直す」案内が出る。
func TestRestoreFromAutoBackupAsksForReattach(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})
	projectDir := makeSyncedProject(t, a, t.TempDir())

	store, err := a.openProject(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CloseAndBackup(a.paths); err != nil {
		t.Fatalf("自動退避に失敗: %v", err)
	}
	gens, err := a.BackupGenerations(projectDir)
	if err != nil || len(gens) != 1 {
		t.Fatalf("自動退避の世代が作られていない: %d 件 %v", len(gens), err)
	}

	restoredDir := filepath.Join(t.TempDir(), "restored")
	restored, err := a.RestoreBackup(gens[0].Path, restoredDir, true)
	if err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoredDir, ".git")); !os.IsNotExist(err) {
		t.Error("自動退避からの復元に同期の管理情報が含まれている（自動退避は管理情報を除外する）")
	}
	if !syncmod.NeedsReattach(restoredDir) {
		t.Error("復元した作業コピーが取り直しの対象と判定されない")
	}
	if !strings.Contains(restored.Notice, "取り直") {
		t.Errorf("復元の結果に取り直しの案内が無い: %q", restored.Notice)
	}
}

// 手動バックアップには同期の管理情報を含める（別端末への移送・障害復旧）。
func TestManualBackupCarriesSyncManagementInfo(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})
	projectDir := makeSyncedProject(t, a, t.TempDir())

	dst := filepath.Join(t.TempDir(), "manual.zip")
	if err := projectstore.WriteManualBackupZip(projectDir, dst); err != nil {
		t.Fatalf("手動バックアップに失敗: %v", err)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	if _, err := a.RestoreBackup(dst, restoredDir, true); err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restoredDir, ".git", "HEAD")); err != nil {
		t.Errorf("手動バックアップからの復元に同期の管理情報が無い: %v", err)
	}
	if syncmod.NeedsReattach(restoredDir) {
		t.Error("管理情報が復元されているのに取り直しを求めている")
	}
}
