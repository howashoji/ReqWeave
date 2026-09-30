package projectstore

import (
	"os"
	"path/filepath"
	"testing"
)

/*
 * 移行（`.reqweave` への改名）に伴う一覧のパス解決。
 *
 * 移行は「改名 → 設定の更新」の順で行う。**その間で落ちると設定は旧名を指したまま**になるため、
 * 記録どおりの場所が無ければ `.reqweave` を付けた名前も探す。
 * これが無いと、改名済みのプロジェクトが一覧から消えて「無くなった」ように見える。
 */

func TestResolveProjectPathFindsRenamedFolder(t *testing.T) {
	parent := t.TempDir()
	old := filepath.Join(parent, "在庫管理")
	renamed := old + ProjectFolderExt
	if err := os.Mkdir(renamed, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	// プロジェクトと判定できる状態にする（project.yaml の存在で判定する）。
	if err := os.WriteFile(filepath.Join(renamed, FileProject), []byte("project_id: P-1\n"), 0o644); err != nil {
		t.Fatalf("書けない: %v", err)
	}

	if got := ResolveProjectPath(old, ""); got != renamed {
		t.Fatalf("改名後の場所を見つけられない: got %q want %q", got, renamed)
	}
}

func TestResolveProjectPathKeepsExistingPath(t *testing.T) {
	// 記録どおりの場所があるなら、そのまま使う（勝手に別の場所を指さない）。
	parent := t.TempDir()
	here := filepath.Join(parent, "在庫管理")
	if err := os.Mkdir(here, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	if got := ResolveProjectPath(here, ""); got != here {
		t.Fatalf("記録どおりの場所を返していない: %q", got)
	}
}

func TestResolveProjectPathDoesNotInventFolders(t *testing.T) {
	// 実体がどちらにも無いときは、記録どおりの値を返す（読み込めない旨を上位が出す）。
	missing := filepath.Join(t.TempDir(), "無い")
	if got := ResolveProjectPath(missing, ""); got != missing {
		t.Fatalf("存在しない場所を作り出している: %q", got)
	}
	// `.reqweave` を付けた側がプロジェクトでないただのフォルダなら、そちらへ寄せない。
	parent := t.TempDir()
	old := filepath.Join(parent, "紛らわしい")
	if err := os.Mkdir(old+ProjectFolderExt, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	if got := ResolveProjectPath(old, ""); got != old {
		t.Fatalf("プロジェクトでないフォルダへ寄せている: %q", got)
	}
}

func TestReplaceRecentProjectKeepsOtherFields(t *testing.T) {
	// 名前が変わっただけで、確認済みの位置などの履歴が消えないこと。
	parent := t.TempDir()
	from := filepath.Join(parent, "在庫管理")
	to := from + ProjectFolderExt
	s := NewSettings()
	s.AddRecentProject(from, 0)
	s.RecentProjects[0].AcknowledgedIncorporation = "inc-1"

	s.ReplaceRecentProject(from, to)

	if len(s.RecentProjects) != 1 {
		t.Fatalf("件数が変わっている: %d", len(s.RecentProjects))
	}
	if s.RecentProjects[0].Path != to {
		t.Fatalf("パスが差し替わっていない: %q", s.RecentProjects[0].Path)
	}
	if s.RecentProjects[0].AcknowledgedIncorporation != "inc-1" {
		t.Fatal("付随する記録が消えている")
	}
}

/*
 * フォルダ名を変えられても見失わない。
 *
 * プロジェクトを 1 個のファイルに見せた結果、**Finder で名前を変えるのは
 * 当然の操作**になった。パスだけで追うと一覧から消えて「無くなった」ように見える。
 * 識別の根拠は `project_id` であり、一覧の追跡もそれに合わせる。
 */

// project_id は UUIDv4 でなければ project.yaml として読めない（Project.Validate）。
// 固定値にして、どの場所を指しているかがテストから読めるようにする。
const (
	testProjectID1 = "11111111-1111-4111-8111-111111111111"
	testProjectID2 = "22222222-2222-4222-8222-222222222222"
	testProjectID3 = "33333333-3333-4333-8333-333333333333"
)

// putProject は project_id を持つプロジェクトフォルダを作る。
func putProject(t *testing.T, path, id string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	body := "format_version: \"1.4\"\nproject_id: " + id +
		"\ntarget_system_name: テスト\nphase: requirements\ncreated_at: 2026-09-08T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(path, FileProject), []byte(body), 0o644); err != nil {
		t.Fatalf("書けない: %v", err)
	}
}

func TestResolveProjectPathFindsRenamedByProjectID(t *testing.T) {
	parent := t.TempDir()
	recorded := filepath.Join(parent, "旧い名前"+ProjectFolderExt)
	actual := filepath.Join(parent, "利用者が付けた名前"+ProjectFolderExt)
	putProject(t, actual, testProjectID1)

	if got := ResolveProjectPath(recorded, testProjectID1); got != actual {
		t.Fatalf("改名後の場所を見つけられない: got %q want %q", got, actual)
	}
}

func TestResolveProjectPathDoesNotGuessWithoutID(t *testing.T) {
	// 識別子が無い記録（古い形式）では探さない。当てずっぽうで別のプロジェクトを指さない。
	parent := t.TempDir()
	recorded := filepath.Join(parent, "旧い名前"+ProjectFolderExt)
	putProject(t, filepath.Join(parent, "別のもの"+ProjectFolderExt), testProjectID2)

	if got := ResolveProjectPath(recorded, ""); got != recorded {
		t.Fatalf("識別子が無いのに別の場所を指した: %q", got)
	}
}

func TestResolveProjectPathIgnoresOtherProjects(t *testing.T) {
	// 同じ親に別のプロジェクトがあっても、識別子が違えば掴まない。
	parent := t.TempDir()
	recorded := filepath.Join(parent, "旧い名前"+ProjectFolderExt)
	putProject(t, filepath.Join(parent, "他人"+ProjectFolderExt), testProjectID2)

	if got := ResolveProjectPath(recorded, testProjectID1); got != recorded {
		t.Fatalf("別のプロジェクトを掴んだ: %q", got)
	}
}

func TestResolveRecentProjectsFixesRecordAndFillsID(t *testing.T) {
	parent := t.TempDir()
	recorded := filepath.Join(parent, "旧い名前"+ProjectFolderExt)
	actual := filepath.Join(parent, "新しい名前"+ProjectFolderExt)
	putProject(t, actual, testProjectID1)

	s := NewSettings()
	s.RecentProjects = []RecentProject{{Path: recorded, ProjectID: testProjectID1}}

	if !s.ResolveRecentProjects() {
		t.Fatal("解決したのに変更なしと返している")
	}
	if s.RecentProjects[0].Path != actual {
		t.Fatalf("記録が直っていない: %q", s.RecentProjects[0].Path)
	}
	// 2 度目は探し直さない（記録が正しくなっているため）。
	if s.ResolveRecentProjects() {
		t.Fatal("直したのに毎回変更ありと返している")
	}
}

func TestResolveRecentProjectsFillsMissingID(t *testing.T) {
	// 古い形式の記録（識別子なし）に、開ける場所があるなら識別子を補う。
	parent := t.TempDir()
	here := filepath.Join(parent, "そのまま"+ProjectFolderExt)
	putProject(t, here, testProjectID3)

	s := NewSettings()
	s.RecentProjects = []RecentProject{{Path: here}}

	if !s.ResolveRecentProjects() {
		t.Fatal("識別子を補ったのに変更なしと返している")
	}
	if s.RecentProjects[0].ProjectID != testProjectID3 {
		t.Fatalf("識別子が入っていない: %+v", s.RecentProjects[0])
	}
}

func TestForgetRecentProjectRemovesOnlyTheEntry(t *testing.T) {
	s := NewSettings()
	s.RecentProjects = []RecentProject{
		{Path: absPath("/a/x"), ProjectID: testProjectID1},
		{Path: absPath("/a/y"), ProjectID: testProjectID2},
	}
	if !s.ForgetRecentProject("/a/x") {
		t.Fatal("取り除けていない")
	}
	if len(s.RecentProjects) != 1 || s.RecentProjects[0].ProjectID != testProjectID2 {
		t.Fatalf("残りが違う: %+v", s.RecentProjects)
	}
	if s.ForgetRecentProject("/a/z") {
		t.Fatal("無い項目を取り除いたと言っている")
	}
}
