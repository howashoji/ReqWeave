//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

/*
 * 対象システム名の変更とフォルダ名の追従。
 *
 * 利用者の気づき「ファイル名を変更してもプロジェクト名には反映されないのですね」への対応。
 * **アプリ内の明示操作で名前を変え、フォルダ名を追従させる**（逆向きは行わない）。
 */

func TestRenameProjectUpdatesNameAndFolder(t *testing.T) {
	a := newProjectAPI(t)
	parent := t.TempDir()
	path := createTestProject(t, a, parent, "在庫管理")

	// 中身が保たれることを確かめるための目印。
	marker := filepath.Join(path, "decisions", "DEC-001.md")
	if err := os.WriteFile(marker, []byte("# 決定\n"), 0o644); err != nil {
		t.Fatalf("目印を置けない: %v", err)
	}

	moved, err := a.RenameProject(path, "在庫管理システム")
	if err != nil {
		t.Fatalf("改名に失敗: %v", err)
	}
	if moved.TargetSystemName != "在庫管理システム" {
		t.Fatalf("対象システム名が変わっていない: %+v", moved)
	}
	// フォルダ名も追従する（名前が 2 つに分かれたままにしない）。
	if filepath.Base(moved.Path) != "在庫管理システム"+projectstore.ProjectFolderExt {
		t.Fatalf("フォルダ名が追従していない: %q", moved.Path)
	}
	if filepath.Dir(moved.Path) != parent {
		t.Errorf("場所が動いている: %q", moved.Path)
	}
	if _, err := os.Stat(filepath.Join(moved.Path, "decisions", "DEC-001.md")); err != nil {
		t.Errorf("中身が失われている: %v", err)
	}

	// 一覧が新しい場所と名前を指す。
	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != moved.Path || list[0].TargetSystemName != "在庫管理システム" {
		t.Fatalf("一覧が更新されていない: %+v", list)
	}
}

func TestRenameProjectRecordsChangeHistory(t *testing.T) {
	// 対象システム名は成果物のタイトルへ載る業務データ。変更を変更履歴に残す。
	a := newProjectAPI(t)
	path := createTestProject(t, a, t.TempDir(), "旧名")

	moved, err := a.RenameProject(path, "新名")
	if err != nil {
		t.Fatalf("改名に失敗: %v", err)
	}

	store, err := a.openProject(moved.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a.session = &dialogueSession{store: store, projectPath: moved.Path}
	t.Cleanup(func() { a.session = nil })

	history, err := a.ChangeHistory()
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var found *ChangeHistoryView
	for i := range history {
		if history[i].Change == auditlog.ChangeProjectRenamed {
			found = &history[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("改名が変更履歴に無い: %+v", history)
	}
	// 旧名・新名の両方が残る（何から何へ変えたかが後から分かる）。
	if found.Target != "project" || found.Before != "旧名" || found.After != "新名" {
		t.Fatalf("記録の内容が違う: %+v", *found)
	}
}

func TestRenameProjectKeepsNameWhenFolderCannotMove(t *testing.T) {
	// フォルダ名は表示のための写し。追従できなくても**名前の変更は残す**。
	a := newProjectAPI(t)
	parent := t.TempDir()
	path := createTestProject(t, a, parent, "在庫管理")

	// 追従先を先に塞ぐ… のではなく、連番で回避されるため、親を読み取り専用にして改名を失敗させる。
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("権限を変えられない: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	moved, err := a.RenameProject(path, "在庫管理システム")
	if err != nil {
		t.Fatalf("名前の変更まで失敗している: %v", err)
	}
	if moved.TargetSystemName != "在庫管理システム" {
		t.Fatalf("名前の変更が残っていない: %+v", moved)
	}
	// フォルダは動いていない（それでよい）。
	if moved.Path != path {
		t.Errorf("動かせないはずのフォルダが動いている: %q", moved.Path)
	}
}

func TestRenameProjectRejectsEmptyName(t *testing.T) {
	a := newProjectAPI(t)
	path := createTestProject(t, a, t.TempDir(), "在庫管理")
	if _, err := a.RenameProject(path, "   "); err == nil {
		t.Fatal("空の名前を受け入れてしまった")
	}
}

/*
 * 開いたプロジェクトが一覧へ載ること。
 *
 * プロジェクトを 1 個のファイルに見せたことで**一覧を経由しない入口**（パッケージのダブルクリック）ができたため、
 * 開く経路そのものが一覧へ載せる必要がある。
 */
func TestOpenDialogueProjectRemembersProject(t *testing.T) {
	a, _ := newConfiguredAPI(t, &stubAdapter{})
	path := createTestProject(t, a, t.TempDir(), "在庫管理システム")

	// 一覧を空にしてから開く（ダブルクリックで開いた状況を作る）。
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.RecentProjects = nil
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatal(err)
	}
	if list, err := a.Projects(); err != nil || len(list) != 0 {
		t.Fatalf("前提が作れていない: %+v %v", list, err)
	}

	if _, err := a.OpenDialogueProject(path); err != nil {
		t.Fatalf("開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != path {
		t.Fatalf("開いたのに一覧へ載っていない: %+v", list)
	}
	// 識別子つきで記録される（一覧はパスではなく識別子で追うので、以後フォルダ名を変えても追える）。
	after, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	if after.RecentProjects[0].ProjectID == "" {
		t.Fatalf("識別子が入っていない: %+v", after.RecentProjects[0])
	}
}
