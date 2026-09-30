//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

/*
 * `.reqweave` への移行（プロジェクト一覧の画面から利用者が実行する）。
 *
 * **起動時に無断で改名しない**ことと、**改名しても中身と一覧が保たれる**ことを確かめる。
 * 利用者のデータの置き場所を断りなく変えると、ショートカット・クラウド同期・
 * 他アプリからの参照が黙って壊れるため、実行は利用者の操作に限る。
 */

// makeLegacyProject は拡張子の付いていない（移行前の）プロジェクトを作る。
func makeLegacyProject(t *testing.T, a *API, parent, name string) string {
	t.Helper()
	created := createTestProject(t, a, parent, name)
	legacy := filepath.Join(parent, name)
	if err := os.Rename(created, legacy); err != nil {
		t.Fatalf("移行前の形にできない: %v", err)
	}
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.ReplaceRecentProject(created, legacy)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func TestMigratableProjectsListsOnlyLegacyOnes(t *testing.T) {
	a := newProjectAPI(t)
	parent := t.TempDir()
	legacy := makeLegacyProject(t, a, parent, "在庫管理")
	createTestProject(t, a, parent, "済んでいる方")

	list, err := a.MigratableProjects()
	if err != nil {
		t.Fatalf("一覧を取れない: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("対象の件数が違う: %+v", list)
	}
	if list[0].Path != legacy {
		t.Errorf("対象が違う: %+v", list[0])
	}
	if !strings.HasSuffix(list[0].NewName, projectstore.ProjectFolderExt) {
		t.Errorf("移行後の名前を示していない: %+v", list[0])
	}

	// 読み出しただけで改名しない（**起動時に無断で改名しない**）。
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("読み出しただけで場所が変わっている: %v", err)
	}
}

func TestMigrateProjectRenamesAndKeepsContents(t *testing.T) {
	a := newProjectAPI(t)
	parent := t.TempDir()
	legacy := makeLegacyProject(t, a, parent, "在庫管理")

	// 中身が保たれることを確かめるための目印。
	marker := filepath.Join(legacy, "decisions", "DEC-001.md")
	if err := os.WriteFile(marker, []byte("# 決定\n"), 0o644); err != nil {
		t.Fatalf("目印を置けない: %v", err)
	}

	moved, err := a.MigrateProject(legacy)
	if err != nil {
		t.Fatalf("移行に失敗: %v", err)
	}
	// 名前の元は**対象システム名**（フォルダの旧名ではない）。
	if filepath.Base(moved.Path) != "在庫管理"+projectstore.ProjectFolderExt {
		t.Fatalf("移行後の名前が違う: %q", moved.Path)
	}
	if filepath.Dir(moved.Path) != parent {
		t.Errorf("場所が動いている: %q", moved.Path)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("移行前の場所が残っている: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved.Path, "decisions", "DEC-001.md")); err != nil {
		t.Errorf("中身が失われている: %v", err)
	}

	// 一覧が新しい場所を指す。
	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != moved.Path {
		t.Fatalf("一覧が新しい場所を指していない: %+v", list)
	}
	// 移行対象からは消える。
	rest, err := a.MigratableProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("移行済みが対象に残っている: %+v", rest)
	}
}

func TestProjectsFindsRenamedFolderWhenSettingsAreStale(t *testing.T) {
	// 移行は「改名 → 設定の更新」の順。**その間で落ちても**一覧から消えないこと。
	a := newProjectAPI(t)
	parent := t.TempDir()
	legacy := makeLegacyProject(t, a, parent, "在庫管理")

	// 設定を更新せずに改名だけを行う（途中で落ちた状態を作る）。
	renamed := legacy + projectstore.ProjectFolderExt
	if err := os.Rename(legacy, renamed); err != nil {
		t.Fatalf("改名できない: %v", err)
	}

	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("件数が違う: %+v", list)
	}
	if list[0].Path != renamed {
		t.Fatalf("改名後の場所を見つけられない: %q", list[0].Path)
	}
	if !list[0].Available {
		t.Fatalf("見つけたのに開けない扱いになっている: %+v", list[0])
	}
}

func TestMigrateProjectRefusesNonProjectFolder(t *testing.T) {
	a := newProjectAPI(t)
	dir := filepath.Join(t.TempDir(), "ただのフォルダ")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.MigrateProject(dir); err == nil {
		t.Fatal("プロジェクトでないフォルダを改名してしまった")
	}
}
