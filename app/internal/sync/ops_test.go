package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 取得・取り込み・反映の一連を、実際の git と共有フォルダ相当の bare リポジトリで検証する。
// 2 端末 = 2 つの作業コピー（別フォルダ・別 Client）。同期先 = t.TempDir() 配下の bare リポジトリ。
//
// git が無い環境では失敗させる（黙って skip しない。前提が揃わないテストを緑にしない）。

var (
	authorA = projectstore.Author{AuthorID: "a.sato@example.co.jp", DisplayName: "佐藤"}
	authorB = projectstore.Author{AuthorID: "b.suzuki@example.co.jp", DisplayName: "鈴木"}
)

func newClient(t *testing.T, author projectstore.Author) *Client {
	t.Helper()
	c, err := New(Options{Author: author, ConfigDir: filepath.Join(t.TempDir(), "sync")})
	if err != nil {
		t.Fatal(err)
	}
	if av := c.Availability(); !av.Available {
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}
	return c
}

// newProject は作業者 author がオーナーのプロジェクトフォルダを作る。
func newProject(t *testing.T, root string, author projectstore.Author) *projectstore.Store {
	t.Helper()
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{TargetSystemName: "在庫管理システム", Author: author})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func folderRemote(t *testing.T) Remote {
	t.Helper()
	share := filepath.Join(t.TempDir(), "share")
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	return Remote{Kind: RemoteFolder, Location: filepath.Join(share, "proj.git")}
}

func writeProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readProjectFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s を読めない: %v", rel, err)
	}
	return string(b)
}

func mustPublish(t *testing.T, c *Client, root string, remote Remote, create bool) *PublishResult {
	t.Helper()
	res, err := c.Publish(context.Background(), root, remote, "", PublishOptions{CreateIfAbsent: create})
	if err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}
	return res
}

func mustIncorporate(t *testing.T, c *Client, root string, remote Remote) *IncorporateResult {
	t.Helper()
	res, err := c.Incorporate(context.Background(), root, remote, "")
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	return res
}

func expectFailure(t *testing.T, err error, kind FailureKind) *Failure {
	t.Helper()
	if err == nil {
		t.Fatalf("失敗（%s）を期待したが成功した", kind)
	}
	f, ok := AsFailure(err)
	if !ok {
		t.Fatalf("Failure でないエラー: %v", err)
	}
	if f.Kind != kind {
		t.Fatalf("失敗種別が違う: got %s, want %s（%s）", f.Kind, kind, f.Message)
	}
	if !strings.Contains(f.Message, "変わっていません") && !strings.Contains(f.Message, "作られていません") {
		t.Errorf("作業コピーが変わらない旨が無い: %q", f.Message)
	}
	// git の概念語を出さない。プログラム名としての「git」は不在時の対処に必要なため対象外
	for _, word := range []string{"commit", "branch", "push", "fetch", "merge"} {
		if strings.Contains(strings.ToLower(f.Message), word) {
			t.Errorf("利用者向け文言に git の語が含まれる（%s）: %q", word, f.Message)
		}
	}
	return f
}

// setupShared はオーナー A のプロジェクトを同期先へ反映し、メンバー B が取得した状態を作る。
func setupShared(t *testing.T) (a, b *Client, rootA, rootB string, remote Remote) {
	t.Helper()
	a, b = newClient(t, authorA), newClient(t, authorB)
	rootA = filepath.Join(t.TempDir(), "A", "proj")
	store := newProject(t, rootA, authorA)
	if _, err := store.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	remote = folderRemote(t)
	mustPublish(t, a, rootA, remote, true)
	rootB = filepath.Join(t.TempDir(), "B", "proj")
	if _, err := b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	return a, b, rootA, rootB, remote
}

// オーナーの初回反映で同期先ができ、メンバーが取得できる。非メンバーは取得できない。
func TestPublishThenCloneByMember(t *testing.T) {
	a, b := newClient(t, authorA), newClient(t, authorB)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	store := newProject(t, rootA, authorA)
	remote := folderRemote(t)

	// 反映の事前提示（同期先へ接続しない）: 初回・全区分が載る
	preview, err := a.PreviewPublish(context.Background(), rootA, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.FirstPublish || preview.Summary.Total() == 0 || preview.RemoteLocation != remote.Location {
		t.Errorf("事前提示が違う: %+v", preview)
	}
	if _, ok := preview.Summary[CatProject]; !ok {
		t.Errorf("事前提示にプロジェクト設定が無い: %v", preview.Summary)
	}

	// 同期先が無い・CreateIfAbsent なし → 不在
	_, err = a.Publish(context.Background(), rootA, remote, "", PublishOptions{})
	expectFailure(t, err, FailNotFound)
	if _, err := os.Stat(remote.Location); err == nil {
		t.Error("失敗した反映で同期先が作られた")
	}

	res := mustPublish(t, a, rootA, remote, true)
	if res.Summary.Total() == 0 || res.NothingToPublish {
		t.Errorf("初回反映の結果が違う: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(remote.Location, "HEAD")); err != nil {
		t.Fatal("同期先の bare リポジトリが作られていない")
	}
	// 2 回目は送るものが無い
	if res := mustPublish(t, a, rootA, remote, false); !res.NothingToPublish {
		t.Errorf("変更なしの反映が「送った」になった: %+v", res)
	}

	// 非メンバー B の取得は内容を渡さず拒否
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	_, err = b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB})
	expectFailure(t, err, FailNotMember)
	if _, err := os.Stat(rootB); err == nil {
		t.Error("拒否された取得で作業コピーが残った")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(rootB), ".reqweave-clone-*")); len(left) != 0 {
		t.Errorf("一時領域が残った: %v", left)
	}

	// B を登録して反映 → B が取得できる
	if _, err := store.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, a, rootA, remote, false)
	cres, err := b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB})
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if cres.Root != rootB || cres.TargetSystemName != "在庫管理システム" || cres.ProjectID != store.Project().ProjectID {
		t.Errorf("取得結果が違う: %+v", cres)
	}
	if !projectstore.IsProjectFolder(rootB) {
		t.Error("取得した作業コピーがプロジェクトフォルダとして成立していない")
	}
	if got := readProjectFile(t, rootB, "members.yaml"); !strings.Contains(got, authorB.AuthorID) {
		t.Error("取得した作業コピーにメンバー一覧が無い")
	}
	if got := readProjectFile(t, rootB, ".git/info/exclude"); !strings.Contains(got, "/locks/") {
		t.Error("取得した作業コピーに同期対象外の宣言（locks/）が無い")
	}
	if info, err := os.Stat(filepath.Join(rootB, "locks")); err != nil || !info.IsDir() {
		t.Error("取得した作業コピーに locks/ が無い")
	}
	head, _ := b.Repo(rootB).Git(context.Background(), "symbolic-ref", "--short", "HEAD")
	if strings.TrimSpace(head) != BranchName(authorB.AuthorID) {
		t.Errorf("取得後の現在ブランチが自分のものでない: %q", head)
	}

	// 同一プロジェクトの重複取得は拒否し、取り込みへ誘導
	rootB2 := filepath.Join(t.TempDir(), "B2", "proj")
	_, err = b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB2,
		KnownProject: func(id string) (string, bool) { return rootB, id == cres.ProjectID }})
	f := expectFailure(t, err, FailAlreadyPresent)
	if !strings.Contains(f.Message, "取り込み") {
		t.Errorf("取り込みへの誘導が無い: %q", f.Message)
	}
	if _, err := os.Stat(rootB2); err == nil {
		t.Error("拒否された取得で作業コピーが残った")
	}
}

// 変更ファイルが重ならなければ承認なしに統合され、双方の変更がすべて残る。
func TestIncorporateDisjointChanges(t *testing.T) {
	a, b, rootA, rootB, remote := setupShared(t)
	ctx := context.Background()

	writeProjectFile(t, rootA, "requirements/FR-INV-001.md", "---\nid: FR-INV-001\n---\n在庫を登録できること\n")
	mustPublish(t, a, rootA, remote, false)

	// B は未反映の作業（未コミット）を持ったまま取り込む → 失われない
	writeProjectFile(t, rootB, "decisions/DEC-001.md", "---\nid: DEC-001\n---\n決定\n")
	res := mustIncorporate(t, b, rootB, remote)
	if len(res.Integrated) != 1 || res.Integrated[0] != authorA.AuthorID {
		t.Errorf("統合した作業者が違う: %v", res.Integrated)
	}
	if res.Summary[CatRequirements].Added != 1 {
		t.Errorf("取り込みの要約が違う: %v", res.Summary)
	}
	if readProjectFile(t, rootB, "requirements/FR-INV-001.md") == "" || readProjectFile(t, rootB, "decisions/DEC-001.md") == "" {
		t.Error("双方の変更が残っていない")
	}
	parents, _ := b.Repo(rootB).Git(ctx, "rev-list", "--parents", "-n", "1", "HEAD")
	if len(strings.Fields(parents)) != 3 {
		t.Errorf("2 親のマージコミットになっていない: %q", parents)
	}
	// 取り込む変更が無いときは何もしない
	if res := mustIncorporate(t, b, rootB, remote); len(res.Integrated) != 0 || res.Summary.Total() != 0 {
		t.Errorf("変更なしの取り込みで何かが起きた: %+v", res)
	}

	// B が反映 → A が取り込むと DEC-001 が届く
	pres := mustPublish(t, b, rootB, remote, false)
	if pres.Summary[CatDecisions].Added != 1 {
		t.Errorf("反映の要約が違う: %v", pres.Summary)
	}
	res = mustIncorporate(t, a, rootA, remote)
	if readProjectFile(t, rootA, "decisions/DEC-001.md") == "" {
		t.Error("相手の変更が届いていない")
	}
	if res.Summary[CatDecisions].Added != 1 {
		t.Errorf("取り込みの要約が違う: %v", res.Summary)
	}
	// 統合後の作業ツリーにコンフリクトマーカーが無い
	for _, root := range []string{rootA, rootB} {
		out, _ := a.Repo(root).Git(ctx, "grep", "-l", "-e", "<<<<<<<", "-e", ">>>>>>>", "--", ".")
		if strings.TrimSpace(out) != "" {
			t.Errorf("コンフリクトマーカーが残っている: %s", out)
		}
	}
}

// 同一対象の相反する変更は承認なしに統合されず、
// 取り込みは中止されて作業コピーが取り込み前の状態に戻る（後勝ち上書き 0 件）。
func TestIncorporateConflictIsCanceledAndRestored(t *testing.T) {
	a, b, rootA, rootB, remote := setupShared(t)
	ctx := context.Background()

	writeProjectFile(t, rootA, "terms.yaml", "terms:\n  - name: 在庫\n    definition: A の定義\n")
	mustPublish(t, a, rootA, remote, false)
	mine := "terms:\n  - name: 在庫\n    definition: B の定義\n"
	writeProjectFile(t, rootB, "terms.yaml", mine)
	writeProjectFile(t, rootB, "open-issues/ISS-001.md", "---\nid: ISS-001\n---\n未決\n")

	_, err := b.Incorporate(ctx, rootB, remote, "")
	f := expectFailure(t, err, FailCanceled)
	if !strings.Contains(f.Message, "三面マージ") || !strings.Contains(f.Detail, "terms.yaml") {
		t.Errorf("競合の提示が違う: %q / %q", f.Message, f.Detail)
	}
	if got := readProjectFile(t, rootB, "terms.yaml"); got != mine {
		t.Errorf("取り込み前の内容に戻っていない（後勝ち上書き）:\n%s", got)
	}
	if readProjectFile(t, rootB, "open-issues/ISS-001.md") == "" {
		t.Error("未反映の作業が失われた")
	}
	head, _ := b.Repo(rootB).Git(ctx, "rev-list", "--parents", "-n", "1", "HEAD")
	if len(strings.Fields(head)) != 2 {
		t.Errorf("中止したのにマージコミットが残っている: %q", head)
	}
	status, _ := b.Repo(rootB).Git(ctx, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Errorf("中止後の作業ツリーが復帰点と一致しない:\n%s", status)
	}
	if strings.Contains(readProjectFile(t, rootB, "terms.yaml"), "<<<<<<<") {
		t.Error("コンフリクトマーカーが書かれた")
	}
}

// 同一利用者が別端末から先に反映していると非 fast-forward として拒否され、
// 取り込み後に再反映できる。先に載った内容は失われない。
func TestPublishNonFastForwardIsRejected(t *testing.T) {
	a, _, rootA1, _, remote := setupShared(t)
	ctx := context.Background()
	a2 := newClient(t, authorA)
	rootA2 := filepath.Join(t.TempDir(), "A2", "proj")
	if _, err := a2.Clone(ctx, CloneOptions{Remote: remote, Dest: rootA2}); err != nil {
		t.Fatalf("別端末での取得に失敗: %v", err)
	}

	writeProjectFile(t, rootA1, "requirements/FR-INV-001.md", "from A1\n")
	mustPublish(t, a, rootA1, remote, false)
	writeProjectFile(t, rootA2, "requirements/FR-INV-002.md", "from A2\n")
	_, err := a2.Publish(ctx, rootA2, remote, "", PublishOptions{})
	f := expectFailure(t, err, FailNonFastForward)
	if !strings.Contains(f.Message, "取り込") {
		t.Errorf("取り込みへの誘導が無い: %q", f.Message)
	}
	// 同期先には A1 の内容が残っている
	out, _ := a.git.run(ctx, "", nil, nil, "--git-dir", remote.Location, "ls-tree", "--name-only", "refs/heads/"+BranchName(authorA.AuthorID), "requirements/")
	if !strings.Contains(out, "FR-INV-001.md") || strings.Contains(out, "FR-INV-002.md") {
		t.Errorf("同期先の内容が違う: %q", out)
	}
	// 取り込み → 再反映
	res := mustIncorporate(t, a2, rootA2, remote)
	if len(res.Integrated) != 1 || res.Integrated[0] != authorA.AuthorID {
		t.Errorf("自分の別端末の変更が統合されていない: %v", res.Integrated)
	}
	mustPublish(t, a2, rootA2, remote, false)
	out, _ = a.git.run(ctx, "", nil, nil, "--git-dir", remote.Location, "ls-tree", "--name-only", "refs/heads/"+BranchName(authorA.AuthorID), "requirements/")
	if !strings.Contains(out, "FR-INV-001.md") || !strings.Contains(out, "FR-INV-002.md") {
		t.Errorf("再反映後の同期先の内容が違う（更新消失）: %q", out)
	}
}

// 到達不能と不在を区別し、いずれも作業コピーを変えない。
func TestUnreachableAndNotFound(t *testing.T) {
	a := newClient(t, authorA)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	newProject(t, rootA, authorA)
	before := readProjectFile(t, rootA, "project.yaml")
	ctx := context.Background()

	unreachable := Remote{Kind: RemoteFolder, Location: filepath.Join(t.TempDir(), "not-mounted", "share", "proj.git")}
	_, err := a.Publish(ctx, rootA, unreachable, "", PublishOptions{CreateIfAbsent: true})
	f := expectFailure(t, err, FailUnreachable)
	if !strings.Contains(f.Message, "続けられます") {
		t.Errorf("作業を続けられる旨が無い: %q", f.Message)
	}
	_, err = a.Incorporate(ctx, rootA, unreachable, "")
	expectFailure(t, err, FailUnreachable)
	_, err = a.Clone(ctx, CloneOptions{Remote: unreachable, Dest: filepath.Join(t.TempDir(), "x")})
	expectFailure(t, err, FailUnreachable)
	if err := a.CheckConnection(ctx, unreachable, "", nil); err == nil {
		t.Error("到達不能の接続確認が成功した")
	}

	missing := folderRemote(t) // 親はあるがリポジトリが無い
	_, err = a.Incorporate(ctx, rootA, missing, "")
	expectFailure(t, err, FailNotFound)
	_, err = a.Clone(ctx, CloneOptions{Remote: missing, Dest: filepath.Join(t.TempDir(), "y")})
	expectFailure(t, err, FailNotFound)
	expectFailure(t, a.CheckConnection(ctx, missing, "", nil), FailNotFound)
	if got := readProjectFile(t, rootA, "project.yaml"); got != before {
		t.Error("失敗した同期で作業コピーが変わった")
	}
	if isRepository(rootA) {
		// 取り込み・反映の先頭で .git は作られる（内容は不変）。反映失敗時も変更は次回の反映対象として残る
		status, _ := a.Repo(rootA).Git(ctx, "status", "--porcelain")
		_ = status
	}

	// 空の bare リポジトリからの取得は「不在」（反映済みの内容が無い）
	empty := folderRemote(t)
	if _, err := a.git.run(ctx, filepath.Dir(empty.Location), nil, nil, "init", "-q", "--bare", empty.Location); err != nil {
		t.Fatal(err)
	}
	_, err = a.Clone(ctx, CloneOptions{Remote: empty, Dest: filepath.Join(t.TempDir(), "z")})
	expectFailure(t, err, FailNotFound)
	if err := a.CheckConnection(ctx, empty, "", nil); err != nil {
		t.Errorf("空でも到達できる同期先の接続確認が失敗した: %v", err)
	}
}

// 到達できない間の変更は次回の反映ですべて載る。
func TestOfflineChangesArePublishedAfterRecovery(t *testing.T) {
	a, b, rootA, rootB, remote := setupShared(t)
	ctx := context.Background()

	hidden := remote.Location + ".hidden"
	if err := os.Rename(remote.Location, hidden); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		writeProjectFile(t, rootB, "requirements/FR-OFF-00"+string(rune('0'+i))+".md", "offline\n")
		_, err := b.Publish(ctx, rootB, remote, "", PublishOptions{})
		expectFailure(t, err, FailNotFound) // 親フォルダはあるがリポジトリが無い
	}
	if err := os.Rename(hidden, remote.Location); err != nil {
		t.Fatal(err)
	}
	res := mustPublish(t, b, rootB, remote, false)
	if res.Summary[CatRequirements].Added != 5 {
		t.Errorf("オフライン中の変更が全部載っていない: %v", res.Summary)
	}
	res2 := mustIncorporate(t, a, rootA, remote)
	if res2.Summary[CatRequirements].Added != 5 {
		t.Errorf("相手にオフライン中の変更が届いていない: %v", res2.Summary)
	}
	for i := 1; i <= 5; i++ {
		readProjectFile(t, rootA, "requirements/FR-OFF-00"+string(rune('0'+i))+".md")
	}
}

// locks/ は同期しない。宣言が壊れても起動時に修復する。コミットメッセージは要約のみ。
func TestExcludeRepairAndCommitMessage(t *testing.T) {
	a := newClient(t, authorA)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	newProject(t, rootA, authorA)
	ctx := context.Background()
	if err := a.ensureRepository(ctx, rootA); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(rootA, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureRepository(ctx, rootA); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exclude); !strings.Contains(string(got), "/locks/") || !strings.Contains(string(got), "# broken") {
		t.Errorf("宣言が修復されていない（既存行も保持する）:\n%s", got)
	}
	writeProjectFile(t, rootA, "locks/records.lock", "app_instance_id: x\n")
	writeProjectFile(t, rootA, "requirements/FR-1.md", "秘密の本文 sk-ant-abcdefghijklmnop\n")
	head, summary, err := a.commitPending(ctx, rootA)
	if err != nil || head == "" {
		t.Fatalf("コミットに失敗: %v", err)
	}
	if summary[CatRequirements].Added != 1 || summary[CatOther].Total() != 0 {
		t.Errorf("要約が違う（locks/ を数えている）: %v", summary)
	}
	tracked, _ := a.Repo(rootA).Git(ctx, "ls-files", "--", "locks")
	if strings.TrimSpace(tracked) != "" {
		t.Errorf("locks/ が同期対象に入った: %q", tracked)
	}
	msg, _ := a.Repo(rootA).Git(ctx, "log", "-1", "--format=%s%n%b")
	if !strings.Contains(msg, "佐藤") || !strings.Contains(msg, "要件項目 1") {
		t.Errorf("コミットメッセージに作業者名・区分と件数が無い: %q", msg)
	}
	if strings.Contains(msg, "秘密の本文") || strings.Contains(msg, "sk-ant") {
		t.Errorf("コミットメッセージに本文が含まれる: %q", msg)
	}
	if !strings.Contains(msg, "同期:") {
		t.Errorf("メッセージ様式が違う: %q", msg)
	}
	// フォルダごとコピーして別の作業者が開いた場合: 自分のブランチを現在位置に作る（内容は変えない）
	b := newClient(t, authorB)
	if err := b.ensureRepository(ctx, rootA); err != nil {
		t.Fatal(err)
	}
	cur, _ := b.Repo(rootA).Git(ctx, "symbolic-ref", "--short", "HEAD")
	if strings.TrimSpace(cur) != BranchName(authorB.AuthorID) {
		t.Errorf("別の作業者のブランチへ切り替わっていない: %q", cur)
	}
	if readProjectFile(t, rootA, "requirements/FR-1.md") == "" {
		t.Error("切り替えで内容が変わった")
	}
}

// git が無い環境では理由と対処を提示し、操作は失敗するが他機能を止めない。
func TestGitUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c, err := New(Options{Author: authorA, ConfigDir: filepath.Join(t.TempDir(), "sync")})
	if err != nil {
		t.Fatalf("git 不在で構成自体が失敗した: %v", err)
	}
	av := c.Availability()
	if av.Available || !strings.Contains(av.Reason, "git") || !strings.Contains(av.Reason, "そのまま使えます") {
		t.Errorf("利用不可の提示が違う: %+v", av)
	}
	root := filepath.Join(t.TempDir(), "proj")
	newProject(t, root, authorA)
	remote := folderRemote(t)
	_, err = c.Publish(context.Background(), root, remote, "", PublishOptions{CreateIfAbsent: true})
	expectFailure(t, err, FailUnavailable)
	_, err = c.Incorporate(context.Background(), root, remote, "")
	expectFailure(t, err, FailUnavailable)
	_, err = c.Clone(context.Background(), CloneOptions{Remote: remote, Dest: filepath.Join(t.TempDir(), "x")})
	expectFailure(t, err, FailUnavailable)
	if isRepository(root) {
		t.Error("git 不在で .git が作られた")
	}
}

// 同期先が認証情報を要するのに未登録なら auth_failed として導線を示す。
func TestMissingCredentialIsAuthFailure(t *testing.T) {
	c, err := New(Options{Author: authorA, ConfigDir: filepath.Join(t.TempDir(), "sync"), Credentials: notSetProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "proj")
	newProject(t, root, authorA)
	remote := Remote{Kind: RemoteGitInternal, Location: "https://git.example.co.jp/team/proj.git"}
	_, err = c.Publish(context.Background(), root, remote, "pid", PublishOptions{})
	f := expectFailure(t, err, FailAuth)
	if !strings.Contains(f.Message, "認証情報") {
		t.Errorf("認証情報の登録への導線が無い: %q", f.Message)
	}
	_, err = c.Incorporate(context.Background(), root, remote, "pid")
	expectFailure(t, err, FailAuth)
}

type notSetProvider struct{}

func (notSetProvider) SyncCredential(context.Context, string) (keymanagerSyncCredential, error) {
	return keymanagerSyncCredential{}, errCredentialNotSetForTest
}

// 認証情報の受け渡しが git の失敗出力に現れないこと（run は stderr をマスクする）。
func TestGitErrorOutputIsMasked(t *testing.T) {
	a := newClient(t, authorA)
	root := filepath.Join(t.TempDir(), "proj")
	newProject(t, root, authorA)
	if err := a.ensureRepository(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	_, err := a.Repo(root).Git(context.Background(), "cat-file", "-p", "ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	if err == nil {
		t.Fatal("失敗するはずの git が成功した")
	}
	if strings.Contains(err.Error(), "ghp_abcdefghijklmnopqrstuvwxyz0123456789") || !strings.Contains(err.Error(), "***MASKED***") {
		t.Errorf("git の出力がマスクされていない: %v", err)
	}
	if errors.Is(err, errGitUnavailable) {
		t.Error("分類が違う")
	}
}

// 原子的置換の一時ファイルの残骸を同期の対象にしない。
//
// SIGKILL では WriteFileAtomic の後片づけ（defer）が走らず `.<名前>.tmp<数字>` が残る。
// 作業ツリーそのものが同期の対象であるため、除外しないと
// **次の反映で同期先へ載り、他メンバーの作業コピーへ配られる**。
func TestAtomicTempLeftoversAreNotSynced(t *testing.T) {
	a := newClient(t, authorA)
	root := filepath.Join(t.TempDir(), "A", "proj")
	newProject(t, root, authorA)
	ctx := context.Background()
	if err := a.ensureRepository(ctx, root); err != nil {
		t.Fatal(err)
	}

	// 落ちた後に残るのと同じ名前で置く。
	writeProjectFile(t, root, ".project.json.tmp1910561282", "半端な内容")
	writeProjectFile(t, root, "sessions/.S-0001.md.tmp451420443", "半端な内容")
	// **紛らわしいが利用者のもの**（先頭が `.` でない / 末尾が数字でない）は同期する。
	writeProjectFile(t, root, "imports/作業中.tmp", "利用者が置いたファイル")
	writeProjectFile(t, root, "requirements/FR-1.md", "在庫を数える\n")

	if _, _, err := a.commitPending(ctx, root); err != nil {
		t.Fatalf("コミットに失敗: %v", err)
	}

	tracked, _ := a.Repo(root).Git(ctx, "ls-files")
	for _, leftover := range []string{".project.json.tmp1910561282", ".S-0001.md.tmp451420443"} {
		if strings.Contains(tracked, leftover) {
			t.Errorf("一時ファイルの残骸が同期対象に入った: %s\n%s", leftover, tracked)
		}
	}
	if !strings.Contains(tracked, "作業中.tmp") {
		t.Errorf("利用者が置いた .tmp ファイルまで除外している:\n%s", tracked)
	}
	if !strings.Contains(tracked, "requirements/FR-1.md") {
		t.Errorf("通常のレコードが同期対象に入っていない:\n%s", tracked)
	}
}
