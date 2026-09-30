//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const (
	testAuthorID   = "k.sato@example.co.jp"
	testAuthorName = "佐藤"
)

// newProjectAPI は一時フォルダに設定・作業者を用意した API を返す。
func newProjectAPI(t *testing.T) *API {
	t.Helper()
	paths := projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}
	settings := projectstore.NewSettings()
	if err := settings.RegisterAuthor(testAuthorID, testAuthorName); err != nil {
		t.Fatal(err)
	}
	if err := projectstore.SaveSettings(paths, settings); err != nil {
		t.Fatal(err)
	}
	return &API{paths: paths}
}

// 作成すると現在フェーズ「要件定義」で作られ、一覧に出る。
// createTestProject は**保存先**を渡してプロジェクトを作り、実際に作られた場所を返す。
//
// フォルダ名はアプリが `<対象システム名>.reqweave` として決めるため、
// テストは渡したパスではなく**戻り値の場所**を使う。ここを固定値で書くと、
// 名前の規則を変えたときにテストが実装を追い越して壊れる。
func createTestProject(t *testing.T, a *API, parent, targetSystemName string) string {
	t.Helper()
	created, err := a.CreateProject(CreateProjectRequest{
		Path: parent, TargetSystemName: targetSystemName,
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	return created.Path
}

func TestCreateAndListProjects(t *testing.T) {
	a := newProjectAPI(t)
	parent := t.TempDir()

	created, err := a.CreateProject(CreateProjectRequest{
		Path: parent, TargetSystemName: "在庫管理システム", DomainPresets: []string{"inventory"},
	})
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}
	// フォルダ名はアプリが決める（`<対象システム名>.reqweave`）。
	if filepath.Base(created.Path) != "在庫管理システム"+projectstore.ProjectFolderExt {
		t.Errorf("作られたフォルダ名が違う: %q", created.Path)
	}
	if filepath.Dir(created.Path) != parent {
		t.Errorf("保存先の下に作られていない: %q", created.Path)
	}
	if created.Phase != projectstore.PhaseRequirements || created.PhaseLabel != "要件定義" {
		t.Errorf("作成直後のフェーズが違う: %+v", created)
	}
	if created.RoleLabel != "オーナー" {
		t.Errorf("作成者がオーナーとして表示されない: %+v", created)
	}
	if created.UpdatedAt == "" {
		t.Error("最終更新日時が空")
	}
	if _, err := time.Parse(time.RFC3339, created.UpdatedAt); err != nil {
		t.Errorf("最終更新日時が ISO 8601 でない: %q", created.UpdatedAt)
	}

	list, err := a.Projects()
	if err != nil {
		t.Fatalf("一覧に失敗: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("一覧の件数が違う: %d", len(list))
	}
	if list[0].TargetSystemName != "在庫管理システム" || !list[0].Available {
		t.Errorf("一覧の内容が違う: %+v", list[0])
	}
}

// 対象システム名・作成先・業務領域の検証。
func TestCreateProjectValidates(t *testing.T) {
	a := newProjectAPI(t)
	cases := map[string]CreateProjectRequest{
		"作成先が空":     {Path: "", TargetSystemName: "X"},
		"対象システム名が空": {Path: filepath.Join(t.TempDir(), "p"), TargetSystemName: "  "},
		"業務領域が一覧外":  {Path: filepath.Join(t.TempDir(), "p2"), TargetSystemName: "X", DomainPresets: []string{"hr"}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := a.CreateProject(req); err == nil {
				t.Error("不正な入力で作成できてしまった")
			}
		})
	}
}

// 到達できないプロジェクトは一覧から消さず、理由つきで示す。
func TestProjectsReportsUnavailable(t *testing.T) {
	a := newProjectAPI(t)
	dir := createTestProject(t, a, t.TempDir(), "在庫管理システム")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("到達不能なプロジェクトが一覧から消えた: %d 件", len(list))
	}
	if list[0].Available {
		t.Errorf("到達不能なのに利用可能と表示された: %+v", list[0])
	}
	if list[0].Notice == "" {
		t.Error("読み込めない理由が示されない")
	}
}

// メンバー外のプロジェクトは理由を示す（エラーの分類「共同作業系」）。
func TestProjectsReportsNonMember(t *testing.T) {
	a := newProjectAPI(t)
	dir := filepath.Join(t.TempDir(), "proj")
	other := projectstore.Author{AuthorID: "t.suzuki@example.co.jp", DisplayName: "鈴木"}
	store, err := projectstore.CreateProject(dir, projectstore.CreateOptions{
		TargetSystemName: "他人のプロジェクト", Author: other})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	settings, err := projectstore.LoadSettings(a.paths)
	if err != nil {
		t.Fatal(err)
	}
	settings.AddRecentProject(dir, 0)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatal(err)
	}

	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("件数が違う: %d", len(list))
	}
	if list[0].Role != "" {
		t.Errorf("メンバー外なのに権限が付いた: %+v", list[0])
	}
	if !strings.Contains(list[0].Notice, "メンバーに登録されていません") {
		t.Errorf("理由が示されない: %q", list[0].Notice)
	}
	// メンバー外は開けない
	if _, err := a.openProject(dir); err == nil {
		t.Error("メンバー外のプロジェクトを開けてしまった")
	}
}

// 削除前に対象システム名と成果物の有無を提示し、確認なしに削除しない。
func TestDeletePreviewAndDelete(t *testing.T) {
	a := newProjectAPI(t)
	dir := createTestProject(t, a, t.TempDir(), "在庫管理システム")
	if err := os.WriteFile(filepath.Join(dir, "documents", "requirements", "draft", "01.md"),
		[]byte("# 章\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := a.DeletePreview(dir)
	if err != nil {
		t.Fatalf("確認情報の取得に失敗: %v", err)
	}
	if preview.TargetSystemName != "在庫管理システム" {
		t.Errorf("対象システム名が提示されない: %+v", preview)
	}
	if !preview.HasDocuments {
		t.Error("成果物の有無が提示されない")
	}
	if !preview.Deletable {
		t.Errorf("オーナーなのに削除できない: %+v", preview)
	}

	if err := a.DeleteProject(dir); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("フォルダが残っている")
	}
	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("一覧から取り除かれていない: %+v", list)
	}
}

// オーナー以外は削除できない。ロック保持中は削除できない。
func TestDeleteBlockedByRoleAndWindowLock(t *testing.T) {
	a := newProjectAPI(t)

	// 他人がオーナーのプロジェクト（自分は非メンバー）
	otherDir := filepath.Join(t.TempDir(), "other")
	other := projectstore.Author{AuthorID: "t.suzuki@example.co.jp", DisplayName: "鈴木"}
	store, err := projectstore.CreateProject(otherDir, projectstore.CreateOptions{
		TargetSystemName: "他人のプロジェクト", Author: other})
	if err != nil {
		t.Fatal(err)
	}
	// 自分を閲覧権限で登録する
	members, err := store.LoadMembers()
	if err != nil {
		t.Fatal(err)
	}
	members.Members = append(members.Members, projectstore.Member{
		AuthorID: testAuthorID, DisplayName: testAuthorName, Role: projectstore.RoleViewer,
		AddedAt: time.Now().UTC(), AddedBy: other.AuthorID})
	if err := store.SaveMembers(members); err != nil {
		t.Fatal(err)
	}
	store.Close()

	preview, err := a.DeletePreview(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Deletable {
		t.Error("閲覧権限で削除できてしまう")
	}
	if !strings.Contains(preview.Notice, "オーナー") {
		t.Errorf("理由が示されない: %q", preview.Notice)
	}
	if err := a.DeleteProject(otherDir); err == nil {
		t.Error("権限が無いのに削除が実行された")
	}
	if _, err := os.Stat(otherDir); err != nil {
		t.Error("削除されてはいけないフォルダが消えた")
	}

	// 自分のプロジェクトでも、別のウィンドウが処理中（`locks/` のロック）は削除できない
	myDir := createTestProject(t, a, t.TempDir(), "自分のプロジェクト")
	mine, err := a.openProject(myDir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := mine.AcquireLock(projectstore.LockDocumentsRequirements)
	if err != nil {
		t.Fatal(err)
	}
	preview, err = a.DeletePreview(myDir)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Deletable {
		t.Error("別のウィンドウが処理中なのに削除できてしまう")
	}
	if !strings.Contains(preview.Notice, "別のウィンドウが処理中") {
		t.Errorf("理由が示されない: %q", preview.Notice)
	}
	// 同一端末内の事象のため作業者名を出さない。
	if strings.Contains(preview.Notice, "佐藤") || strings.Contains(preview.Notice, "さん") {
		t.Errorf("削除できない理由に作業者名が出ている: %q", preview.Notice)
	}
	lock.Release()
	mine.Close()

	preview, err = a.DeletePreview(myDir)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Deletable {
		t.Errorf("ロック解放後も削除できない: %+v", preview)
	}
}

// 既存プロジェクトの業務領域を設定から変更できる。
func TestSetDomainPresets(t *testing.T) {
	a := newProjectAPI(t)
	created, err := a.CreateProject(CreateProjectRequest{
		Path: t.TempDir(), TargetSystemName: "在庫管理システム", DomainPresets: []string{"inventory"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := created.Path
	if err := a.SetDomainPresets(dir, []string{"sales", "accounting"}); err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	store, err := a.openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got := store.Project().DomainPresets
	if len(got) != 2 || got[0] != "sales" || got[1] != "accounting" {
		t.Errorf("業務領域が変わっていない: %v", got)
	}
	if err := a.SetDomainPresets(dir, []string{"unknown"}); err == nil {
		t.Error("一覧外の業務領域が受理された")
	}
}

// 新規作成時の概要が保存される。
func TestCreateProjectStoresSummary(t *testing.T) {
	a := newProjectAPI(t)
	created, err := a.CreateProject(CreateProjectRequest{
		Path: t.TempDir(), TargetSystemName: "在庫管理システム",
		Summary: " 受発注から出荷までを扱う社内システム ",
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := created.Path
	store, err := a.openProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.Project().Summary; got != "受発注から出荷までを扱う社内システム" {
		t.Errorf("概要が保存されていない: %q", got)
	}
	if got := store.Project().FormatVersion; got != projectstore.CurrentFormatVersion {
		t.Errorf("形式バージョンが違う: %q", got)
	}
}

// 旧形式のプロジェクトは、開くときに「退避 → 移行 → migrated_from の記録」を経て開く。
func TestOpenProjectMigratesOlderFormat(t *testing.T) {
	a := newProjectAPI(t)
	dir := createTestProject(t, a, t.TempDir(), "在庫管理システム")
	path := filepath.Join(dir, projectstore.FileProject)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(string(before),
		`format_version: "`+projectstore.CurrentFormatVersion+`"`, `format_version: "1.0"`, 1)
	if older == string(before) {
		t.Fatalf("形式バージョンを差し替えられませんでした:\n%s", before)
	}
	if err := os.WriteFile(path, []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := a.openProject(dir)
	if err != nil {
		t.Fatalf("旧形式のプロジェクトを開けない: %v", err)
	}
	defer store.Close()
	p := store.Project()
	if p.FormatVersion != projectstore.CurrentFormatVersion {
		t.Errorf("移行されていない: %q", p.FormatVersion)
	}
	if p.MigratedFrom != "1.0" {
		t.Errorf("migrated_from が記録されていない: %q", p.MigratedFrom)
	}
	gens, err := projectstore.ListBackupGenerations(a.paths, p.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != 1 {
		t.Fatalf("移行前の退避が作られていない: %d 件", len(gens))
	}
	peeked, err := projectstore.PeekBackupProjectID(gens[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if peeked.FormatVersion != "1.0" {
		t.Errorf("退避が移行前のデータでない: %q", peeked.FormatVersion)
	}
}

// 一覧に無い既存のプロジェクトフォルダを選んで一覧へ加えられる。
func TestAddExistingProjectAddsToList(t *testing.T) {
	// 端末 A（オーナー）がプロジェクトを作る。
	owner := newProjectAPI(t)
	dir := createTestProject(t, owner, t.TempDir(), "共有プロジェクト")

	// 端末 B（別のアプリ設定 = 別端末に相当）。作った覚えが無いので一覧は空。
	other := newProjectAPI(t)
	before, err := other.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("別端末の一覧が空でない: %+v", before)
	}

	added, err := other.AddExistingProject(dir)
	if err != nil {
		t.Fatalf("既存プロジェクトの追加に失敗: %v", err)
	}
	if !added.Available || added.TargetSystemName != "共有プロジェクト" {
		t.Fatalf("追加結果が想定外: %+v", added)
	}

	after, err := other.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].TargetSystemName != "共有プロジェクト" {
		t.Fatalf("一覧に加わっていない: %+v", after)
	}
	// アプリ設定へ永続化されていること（再起動しても一覧に残る）。
	settings, err := projectstore.LoadSettings(other.paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.RecentProjects) != 1 {
		t.Fatalf("recent_projects に残っていない: %+v", settings.RecentProjects)
	}
}

// 既に一覧にあるフォルダを選んでも重複して増えない。
func TestAddExistingProjectDoesNotDuplicate(t *testing.T) {
	a := newProjectAPI(t)
	dir := createTestProject(t, a, t.TempDir(), "重複しないプロジェクト")
	for i := 0; i < 3; i++ {
		if _, err := a.AddExistingProject(dir); err != nil {
			t.Fatalf("%d 回目の追加に失敗: %v", i+1, err)
		}
	}
	list, err := a.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("一覧が %d 件に増えた（重複している）: %+v", len(list), list)
	}
}

// プロジェクトデータでないフォルダは一覧へ加えず、原因と次の行動を示す。
func TestAddExistingProjectRejectsNonProjectFolder(t *testing.T) {
	a := newProjectAPI(t)

	empty := filepath.Join(t.TempDir(), "ただのフォルダ")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "壊れたプロジェクト")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(broken, projectstore.FileProject), "{ not json"); err != nil {
		t.Fatal(err)
	}
	aFile := filepath.Join(t.TempDir(), "ファイル.txt")
	if err := writeFile(aFile, "x"); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"project.json が無い":    empty,
		"project.json が壊れている": broken,
		"フォルダでない":             aFile,
		"存在しないパス":             filepath.Join(t.TempDir(), "無いフォルダ"),
		"空文字":                 "",
	}
	for name, path := range cases {
		if _, err := a.AddExistingProject(path); err == nil {
			t.Errorf("%s: 一覧へ加えられてしまった", name)
			continue
		} else {
			msg := err.Error()
			if !strings.Contains(msg, "ください") {
				t.Errorf("%s: 次に取る行動が書かれていない: %q", name, msg)
			}
		}
		list, err := a.Projects()
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 0 {
			t.Fatalf("%s: 一覧へ残ってしまった: %+v", name, list)
		}
	}
}

// 別端末のメンバーが共有フォルダのプロジェクトへ参加できる。
// メンバー未登録なら一覧へは加わるが、開けない旨が示される（判定は前段の共通実装）。
func TestAddExistingProjectSurfacesMembershipState(t *testing.T) {
	owner := newProjectAPI(t)
	dir := createTestProject(t, owner, t.TempDir(), "メンバー確認")

	// 別端末・別の利用者 ID（メンバー未登録）。
	paths := projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}
	settings := projectstore.NewSettings()
	if err := settings.RegisterAuthor("t.yamada@example.co.jp", "山田"); err != nil {
		t.Fatal(err)
	}
	if err := projectstore.SaveSettings(paths, settings); err != nil {
		t.Fatal(err)
	}
	guest := &API{paths: paths}

	added, err := guest.AddExistingProject(dir)
	if err != nil {
		t.Fatalf("追加自体は成功すべき: %v", err)
	}
	// 一覧には加わる（経路は開かれる）。
	list, err := guest.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("一覧に加わっていない: %+v", list)
	}
	// メンバー外である旨が示される（エラーカタログ「共同作業系」の様式）。
	if !strings.Contains(added.Notice, "メンバーに登録されていません") {
		t.Fatalf("メンバー外の注意書きが出ていない: %q", added.Notice)
	}
	if !strings.Contains(added.Notice, "ください") {
		t.Fatalf("次に取る行動が書かれていない: %q", added.Notice)
	}
}

// 相対パスで渡しても、一覧と戻り値には絶対パスが入ること
// （一覧は端末ごとの設定に絶対パスで持つ。作業ディレクトリに依存させない）。
func TestAddExistingProjectStoresAbsolutePath(t *testing.T) {
	a := newProjectAPI(t)
	base := t.TempDir()
	dir := createTestProject(t, a, base, "相対パス確認")
	// 一覧から一度消して、相対パスで加え直す。
	settings, err := projectstore.LoadSettings(a.paths)
	if err != nil {
		t.Fatal(err)
	}
	settings.RecentProjects = nil
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(rel) {
		t.Fatalf("相対パスを作れていない: %q", rel)
	}

	added, err := a.AddExistingProject(rel)
	if err != nil {
		t.Fatalf("相対パスで追加できない: %v", err)
	}
	if !filepath.IsAbs(added.Path) {
		t.Fatalf("戻り値のパスが絶対パスでない: %q", added.Path)
	}
	saved, err := projectstore.LoadSettings(a.paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.RecentProjects) != 1 || !filepath.IsAbs(saved.RecentProjects[0].Path) {
		t.Fatalf("一覧へ絶対パスで保存されていない: %+v", saved.RecentProjects)
	}
}
