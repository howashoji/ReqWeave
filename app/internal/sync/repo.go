package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
)

// 作業コピー側のリポジトリ操作（メンバーごとのブランチ・ローカルコミット・同期の対象範囲）。

const (
	gitDirName = ".git"
	// branchPrefix はメンバーごとのブランチ（統合ブランチを置かず、反映を常に fast-forward にする）。
	branchPrefix = "refs/heads/work/"
	// syncRefPrefix は同期先から取得したブランチの置き場所（リモート追跡ではなく本モジュール専用の参照）。
	syncRefPrefix = "refs/sync/work/"
	// publishedRef は最後に反映した位置（反映の事前提示に使う。端末内の情報）。
	publishedRef = "refs/sync/published"
	// emptyTree は git の空ツリー（共通祖先が無いときの基準版）。
	emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	// excludeLine は同期対象外の宣言（`locks/` は同一端末の多重起動の保護で、端末の外へ出さない）。
	excludeLine = "/locks/"
	// excludeTempLine は原子的置換の一時ファイルの残骸を同期の対象から外す。
	//
	// `WriteFileAtomic` は後片づけを defer で行うため **SIGKILL では走らず**、
	// `.<対象ファイル名>.tmp<数字>` がプロジェクトフォルダに残る。作業ツリーそのものが
	// 同期の対象であるため、放置すると次の反映で同期先へ載り他メンバーへ配られる。
	// **一時ファイルはプロジェクトデータではない**ので同期しない。
	// 残骸そのものの掃除は projectstore 側（プロジェクトを開いた時点）が行う。
	excludeTempLine = ".*.tmp[0-9]*"
)

// BranchName は作業者のブランチ名（`work/<author_id の安全化表記>`。
// 安全化表記は変更履歴のファイル名と同一規則 = auditlog.SafeAuthorFileName）。
func BranchName(authorID string) string {
	return "work/" + auditlog.SafeAuthorFileName(authorID)
}

// AuthorIDFromBranch はブランチ名から author_id を復元する（安全化表記の逆変換）。
func AuthorIDFromBranch(branch string) (string, bool) {
	safe, ok := strings.CutPrefix(branch, "work/")
	if !ok {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(safe); i++ {
		c := safe[i]
		if c == '%' && i+2 < len(safe) {
			var v byte
			if _, err := fmt.Sscanf(safe[i+1:i+3], "%02x", &v); err == nil {
				b.WriteByte(v)
				i += 2
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// Repo は 1 つの作業コピーに対する git 操作の入口（三面マージの実装 = Merger にも渡す）。
type Repo struct {
	c    *Client
	root string
}

// Root は作業コピーのパス。
func (r *Repo) Root() string { return r.root }

// Git は作業コピーで git を実行する（認証なし・ローカル操作用）。
func (r *Repo) Git(ctx context.Context, args ...string) (string, error) {
	return r.c.git.run(ctx, r.root, nil, nil, args...)
}

// GitEnv は追加環境変数つきで git を実行する（一時インデックス等）。
func (r *Repo) GitEnv(ctx context.Context, env []string, args ...string) (string, error) {
	return r.c.git.run(ctx, r.root, nil, env, args...)
}

// GitInput は標準入力を与えて git を実行する（オブジェクトの書き込み = hash-object）。
func (r *Repo) GitInput(ctx context.Context, stdin []byte, args ...string) (string, error) {
	return r.c.git.runInput(ctx, r.root, nil, nil, stdin, args...)
}

// isRepository は root が git 作業ツリーかを返す。
func isRepository(root string) bool {
	info, err := os.Stat(filepath.Join(root, gitDirName))
	return err == nil && info.IsDir()
}

// ensureRepository は作業コピーを同期可能な状態に整える（起動時・同期操作の先頭で呼ぶ）。
//
//   - `.git/` が無ければ作り、HEAD を自分のブランチへ向ける（作業ツリーの内容は変えない）。
//     ただし**復元した作業コピー**（同期の記録があるのに `.git/` が無い）はここで作らず、
//     ErrNeedsReattach を返して取り直しを求める。
//   - 同期対象外の宣言（`locks/`）を検証・修復する（利用者が壊しても黙って範囲が変わらない）。
//   - HEAD が別の作業者のブランチなら、自分のブランチを現在位置に作って切り替える
//     （フォルダごとコピーして受け取った作業コピー。作業ツリーの内容は変えない）。
func (c *Client) ensureRepository(ctx context.Context, root string) error {
	mine := BranchName(c.author.AuthorID)
	if !isRepository(root) {
		// 同期した記録があるのに管理情報が無い = 復元した作業コピー。ここで作り直すと
		// 同期先と無関係な履歴になるため、取り直し（Reattach）を経ることを求める。
		if NeedsReattach(root) {
			return ErrNeedsReattach
		}
		if _, err := c.git.run(ctx, root, nil, nil, "init", "-q"); err != nil {
			return err
		}
		if _, err := c.git.run(ctx, root, nil, nil, "symbolic-ref", "HEAD", "refs/heads/"+mine); err != nil {
			return err
		}
	}
	if err := ensureExclude(root); err != nil {
		return err
	}
	head, err := c.git.run(ctx, root, nil, nil, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		// detached HEAD 等。自分のブランチへ向け直す（内容は変えない）
		head = ""
	}
	head = strings.TrimSpace(head)
	if head == "refs/heads/"+mine {
		return nil
	}
	exists, err := c.refExists(ctx, root, "refs/heads/"+mine)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("この作業コピーは別の作業者の状態で開かれています。同期先から取得し直してください。")
	}
	// 自分のブランチを現在位置に作る。HEAD がまだ何も指していない（コミット 0）場合は symbolic-ref だけでよい
	if sha, err := c.headSHA(ctx, root); err == nil && sha != "" {
		if _, err := c.git.run(ctx, root, nil, nil, "update-ref", "refs/heads/"+mine, sha); err != nil {
			return err
		}
	}
	_, err = c.git.run(ctx, root, nil, nil, "symbolic-ref", "HEAD", "refs/heads/"+mine)
	return err
}

// ensureExclude は `.git/info/exclude` に同期対象外の宣言があることを保証する。
func ensureExclude(root string) error {
	path := filepath.Join(root, gitDirName, "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("同期対象の宣言を読めません: %w", err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, want := range []string{excludeLine, excludeTempLine} {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("同期対象の宣言を書けません: %w", err)
	}
	content := string(b)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "# ReqWeave: 同期の対象外。この行は起動時に自動修復される。\n"
	content += strings.Join(missing, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func (c *Client) refExists(ctx context.Context, root, ref string) (bool, error) {
	_, err := c.git.run(ctx, root, nil, nil, "show-ref", "--verify", "-q", ref)
	if err == nil {
		return true, nil
	}
	var ge *gitError
	if asGitError(err, &ge) && ge.exitCode == 1 {
		return false, nil
	}
	return false, err
}

func asGitError(err error, target **gitError) bool {
	ge, ok := err.(*gitError)
	if ok {
		*target = ge
	}
	return ok
}

// headSHA は HEAD のコミット ID を返す。コミットが 1 つも無ければ空文字。
func (c *Client) headSHA(ctx context.Context, root string) (string, error) {
	out, err := c.git.run(ctx, root, nil, nil, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	if err != nil {
		var ge *gitError
		if asGitError(err, &ge) && ge.exitCode == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// resolveRef は参照のコミット ID を返す（無ければ空文字）。
func (c *Client) resolveRef(ctx context.Context, root, ref string) (string, error) {
	out, err := c.git.run(ctx, root, nil, nil, "rev-parse", "-q", "--verify", ref+"^{commit}")
	if err != nil {
		var ge *gitError
		if asGitError(err, &ge) && ge.exitCode == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// commitPending は作業ツリーの未コミット変更を 1 つのローカルコミットにまとめる（保存のたびには作らない）。
// 変更が無ければコミットを作らない。戻り値はコミット後の HEAD（= 復帰点）と変更の要約。
func (c *Client) commitPending(ctx context.Context, root string) (string, Summary, error) {
	summary, err := c.pendingSummary(ctx, root)
	if err != nil {
		return "", nil, err
	}
	if summary.Total() == 0 {
		sha, err := c.headSHA(ctx, root)
		return sha, summary, err
	}
	if _, err := c.git.run(ctx, root, nil, nil, "add", "-A", "--", "."); err != nil {
		return "", nil, err
	}
	msg := commitMessage(c.author.DisplayName, time.Now().UTC(), summary)
	if _, err := c.git.run(ctx, root, nil, nil, "commit", "-q", "--no-verify", "--allow-empty-message", "-m", msg); err != nil {
		return "", nil, err
	}
	sha, err := c.headSHA(ctx, root)
	return sha, summary, err
}

// commitMessage はローカルコミットのメッセージ（作業者名・日時・変更区分と件数。本文を含めない）。
func commitMessage(displayName string, at time.Time, s Summary) string {
	return fmt.Sprintf("同期: %s %s %s", displayName, at.Format(time.RFC3339), s.Describe())
}

// pendingSummary は作業ツリーの未コミット変更（未追跡を含む）を区分ごとに数える。
func (c *Client) pendingSummary(ctx context.Context, root string) (Summary, error) {
	return c.pendingSummaryFiltered(ctx, root, nil)
}

// pendingSummaryFiltered は skip が真を返すパスを除いて数える（skip が nil ならすべて数える）。
func (c *Client) pendingSummaryFiltered(ctx context.Context, root string, skip func(string) bool) (Summary, error) {
	out, err := c.git.run(ctx, root, nil, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	s := Summary{}
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) < 4 {
			continue
		}
		x, y, path := entry[0], entry[1], entry[3:]
		if skip != nil && skip(path) {
			continue
		}
		switch {
		case x == '?' || x == 'A' || y == 'A':
			s.add(path, changeAdded)
		case x == 'D' || y == 'D':
			s.add(path, changeRemoved)
		default:
			s.add(path, changeModified)
		}
	}
	return s, nil
}

// diffSummary は 2 つのコミット（またはツリー）の差分を区分ごとに数える。
func (c *Client) diffSummary(ctx context.Context, root, from, to string) (Summary, error) {
	return c.diffSummaryFiltered(ctx, root, from, to, nil)
}

// diffSummaryFiltered は skip が真を返すパスを除いて数える（skip が nil ならすべて数える）。
func (c *Client) diffSummaryFiltered(ctx context.Context, root, from, to string, skip func(string) bool) (Summary, error) {
	if from == "" {
		from = emptyTree
	}
	if to == "" {
		to = emptyTree
	}
	out, err := c.git.run(ctx, root, nil, nil, "diff", "--name-status", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	s := Summary{}
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, path := fields[i], fields[i+1]
		if status == "" {
			continue
		}
		if skip != nil && skip(path) {
			continue
		}
		switch status[0] {
		case 'A':
			s.add(path, changeAdded)
		case 'D':
			s.add(path, changeRemoved)
		default:
			s.add(path, changeModified)
		}
	}
	return s, nil
}

// restore は作業コピーを復帰点へ戻す（取り込みの中止。`locks/` は対象外のため触らない）。
func (c *Client) restore(ctx context.Context, root, sha string) error {
	mine := "refs/heads/" + BranchName(c.author.AuthorID)
	if sha == "" {
		// コミット 0 の状態へは戻せない（取り込み前にコミットしているため通常到達しない）
		return fmt.Errorf("復帰点がありません")
	}
	if _, err := c.git.run(context.WithoutCancel(ctx), root, nil, nil, "update-ref", mine, sha); err != nil {
		return err
	}
	_, err := c.git.run(context.WithoutCancel(ctx), root, nil, nil, "reset", "-q", "--hard", sha)
	return err
}

// syncBranches は同期先から取得済みのブランチ（refs/sync/work/*）を名前順に返す。
func (c *Client) syncBranches(ctx context.Context, root string) ([]branchRef, error) {
	out, err := c.git.run(ctx, root, nil, nil, "for-each-ref", "--format=%(refname) %(objectname)", syncRefPrefix)
	if err != nil {
		return nil, err
	}
	var refs []branchRef
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		refs = append(refs, branchRef{Branch: strings.TrimPrefix(name, "refs/sync/"), Ref: name, SHA: sha})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Branch < refs[j].Branch })
	return refs, nil
}

// branchRef は同期先のブランチ 1 本。
type branchRef struct {
	Branch string // work/<safe author>
	Ref    string // refs/sync/work/<safe author>
	SHA    string
}

// AuthorID はブランチの作業者。
func (b branchRef) AuthorID() string {
	id, _ := AuthorIDFromBranch(b.Branch)
	return id
}

// isAncestor は a が b の祖先（または同一）かを返す。
func (c *Client) isAncestor(ctx context.Context, root, a, b string) (bool, error) {
	_, err := c.git.run(ctx, root, nil, nil, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ge *gitError
	if asGitError(err, &ge) && ge.exitCode == 1 {
		return false, nil
	}
	return false, err
}

// mergeBase は共通祖先を返す（無ければ空文字）。
func (c *Client) mergeBase(ctx context.Context, root, a, b string) (string, error) {
	out, err := c.git.run(ctx, root, nil, nil, "merge-base", a, b)
	if err != nil {
		var ge *gitError
		if asGitError(err, &ge) && ge.exitCode == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}
