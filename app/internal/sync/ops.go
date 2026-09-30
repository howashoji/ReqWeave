package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 同期の操作（取得 / 取り込み / 反映 / 接続確認）。

// CredentialProvider は同期先の認証情報の取得元（アプリ設定の参照名 → OS セキュアストレージ）。
// 未登録なら keymanager.ErrSyncCredentialNotSet を返す。
type CredentialProvider interface {
	SyncCredential(ctx context.Context, projectID string) (keymanager.SyncCredential, error)
}

// Stage は進捗表示の段階（実行中に進行状況を示す）。
type Stage string

const (
	StageCommit   Stage = "commit"   // 作業ツリーの変更をまとめる
	StageConnect  Stage = "connect"  // 同期先へ接続
	StageFetch    Stage = "fetch"    // 同期先から受信
	StageMerge    Stage = "merge"    // 他メンバーの変更を統合
	StagePush     Stage = "push"     // 同期先へ送信
	StageFinalize Stage = "finalize" // 仕上げ
)

// StageLabel は段階の表示名（git の語を出さない）。
var StageLabel = map[Stage]string{
	StageCommit:   "作業中の変更をまとめています",
	StageConnect:  "同期先へ接続しています",
	StageFetch:    "同期先から受信しています",
	StageMerge:    "他のメンバーの変更を統合しています",
	StagePush:     "同期先へ送信しています",
	StageFinalize: "仕上げています",
}

// Options は同期モジュールの構成。
type Options struct {
	Author projectstore.Author
	// ConfigDir は本モジュールが管理する設定ファイルの置き場所（アプリ設定領域配下 = 端末内）。
	ConfigDir   string
	Credentials CredentialProvider
	Merger      Merger          // nil = ThreeWayMerger（Resolver / MergeAuditor を組み込む）
	Recorder    Recorder        // nil = NopRecorder
	Ranges      RangeReserver   // nil = 番号帯の確保を行わない（単独利用・未配線）
	Versions    VersionResolver // nil = 版番号の衝突解消を行わない（未配線）
	// Resolver は三面マージの承認の受け口（三面マージの画面。nil = 競合があれば取り込みを中止する）。
	Resolver ConflictResolver
	// MergeAuditor は解決した競合の変更履歴への記録（`merge-applied`。nil = 記録しない）。
	MergeAuditor MergeAuditor
	// Progress は段階の通知（nil 可）。
	Progress func(stage Stage)
}

// Client は同期モジュールの入口。1 作業者につき 1 つ。
type Client struct {
	git      *gitRunner
	author   projectstore.Author
	creds    CredentialProvider
	merger   Merger
	recorder Recorder
	ranges   RangeReserver
	versions VersionResolver
	progress func(Stage)
}

// RangeReserver は ID の番号帯を確保する受け口（実装は projectstore 側）。
//
// **反映の直前にだけ**呼ばれ、確保は**反映が成功したときに確定**する（失敗したら本モジュールが巻き戻す）。
// 取り込みの完了後には呼ばない: 同期先へ載っていない区間は他メンバーから見えず、
// 同じ区間を二重に確保させて ID の重複を生むため。
type RangeReserver interface {
	// ReserveIDRanges は作業コピー root の番号帯を確保する。番号帯を使わないプロジェクトでは何もしない。
	//
	// floors は対象種別 → 同期先で見えている確保済みの最大上限。作業コピーがまだ取り込んでいない
	// 他メンバーの確保を跨ぐために渡す。
	ReserveIDRanges(root string, floors map[string]int) error
}

// VersionResolver は取り込み時の確定版の版番号の衝突を解消する受け口（実装は projectstore 側）。
//
// 相手のブランチを統合する直前に、相手側の版履歴（種別 → versions.md の内容）を渡して呼ばれる。
// 実装は自分側の確定版を必要に応じて再採番し（内容は変えない）、再採番した件数を返す。
type VersionResolver interface {
	// ResolveVersionCollisions は再採番した件数と、自分側で統合済みにしたパス（以後の統合で自分側を採る）を返す。
	ResolveVersionCollisions(root string, theirs map[string][]byte) (int, []string, error)
}

// New は同期モジュールを構成する。git が無くてもエラーにしない（Availability で示す）。
func New(opts Options) (*Client, error) {
	if opts.Author.AuthorID == "" || opts.Author.DisplayName == "" {
		return nil, fmt.Errorf("同期には作業者の登録が必要です。設定でメールアドレス（利用者 ID）と表示名を登録してください。")
	}
	runner, err := newGitRunner(opts.ConfigDir, opts.Author)
	if err != nil {
		return nil, err
	}
	c := &Client{git: runner, author: opts.Author, creds: opts.Credentials, merger: opts.Merger,
		recorder: opts.Recorder, ranges: opts.Ranges, versions: opts.Versions, progress: opts.Progress}
	if c.merger == nil {
		c.merger = ThreeWayMerger{Resolver: opts.Resolver, Auditor: opts.MergeAuditor}
	}
	if c.recorder == nil {
		c.recorder = NopRecorder{}
	}
	if c.progress == nil {
		c.progress = func(Stage) {}
	}
	return c, nil
}

// Availability は同期機能の利用可否。
func (c *Client) Availability() Availability { return c.git.availability() }

// Author は構成した作業者。
func (c *Client) Author() projectstore.Author { return c.author }

// Repo は作業コピーの git 操作の入口を返す（Merger の実装・テスト用）。
func (c *Client) Repo(root string) *Repo { return &Repo{c: c, root: root} }

// handoffFor は同期先に応じた認証の受け渡し資材を用意する。cred が非 nil ならそれを使う
// （取得時の入力値）。nil なら CredentialProvider から projectID で引く。
func (c *Client) handoffFor(ctx context.Context, op Operation, remote Remote, projectID string, cred *keymanager.SyncCredential) (*Handoff, error) {
	if !remote.RequiresCredential() {
		return NoCredential(), nil
	}
	var value keymanager.SyncCredential
	if cred != nil {
		value = *cred
	} else {
		if c.creds == nil {
			return nil, newFailure(FailAuth, op, "認証情報の取得元が構成されていません")
		}
		v, err := c.creds.SyncCredential(ctx, projectID)
		if err != nil {
			if errors.Is(err, keymanager.ErrSyncCredentialNotSet) {
				return nil, newFailure(FailAuth, op, "認証情報が未登録です")
			}
			return nil, newFailure(FailAuth, op, err.Error())
		}
		value = v
	}
	if value.Kind != remote.CredentialKind() {
		return nil, newFailure(FailAuth, op, fmt.Sprintf("同期先の形式（%s）と認証方式（%s）が合いません", remote.Display(), value.Kind))
	}
	h, err := PrepareHandoff(value, remote.Location)
	if err != nil {
		return nil, newFailure(FailAuth, op, err.Error())
	}
	return h, nil
}

// checkFolderRemote は共有フォルダの同期先の事前確認（到達不能と不在を区別する）。
// createIfAbsent が真で親フォルダに到達できるときは bare リポジトリを作る（オーナーの初回反映）。
func (c *Client) checkFolderRemote(ctx context.Context, op Operation, remote Remote, createIfAbsent bool) error {
	loc := filepath.Clean(remote.Location)
	info, err := os.Stat(loc)
	if err == nil {
		if !info.IsDir() {
			return newFailure(FailNotFound, op, loc+" はフォルダではありません")
		}
		if _, err := os.Stat(filepath.Join(loc, "HEAD")); err != nil {
			return newFailure(FailNotFound, op, loc+" は同期先のリポジトリではありません")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return newFailure(FailUnreachable, op, err.Error())
	}
	parent := filepath.Dir(loc)
	if pinfo, perr := os.Stat(parent); perr != nil || !pinfo.IsDir() {
		return newFailure(FailUnreachable, op, parent+" に到達できません")
	}
	if !createIfAbsent {
		return newFailure(FailNotFound, op, loc+" がありません")
	}
	if _, err := c.git.run(ctx, parent, nil, nil, "init", "-q", "--bare", loc); err != nil {
		return failureFrom(op, err)
	}
	return nil
}

// CheckConnection は同期先へ到達・認証できるかを確認する（内容は変えない）。
// cred が非 nil ならその値で確認する（登録前の確認）。nil なら登録済みの認証情報を使う。
func (c *Client) CheckConnection(ctx context.Context, remote Remote, projectID string, cred *keymanager.SyncCredential) error {
	const op = OpCheck
	if err := remote.Validate(); err != nil {
		return newFailure(FailNotFound, op, err.Error())
	}
	if !c.git.availability().Available {
		return newFailure(FailUnavailable, op, "")
	}
	if remote.Kind == RemoteFolder {
		return c.checkFolderRemote(ctx, op, remote, false)
	}
	h, err := c.handoffFor(ctx, op, remote, projectID, cred)
	if err != nil {
		return err
	}
	defer h.Cleanup()
	c.progress(StageConnect)
	if _, err := c.git.run(ctx, "", h, nil, "ls-remote", "--heads", remote.gitURL()); err != nil {
		return failureFrom(op, err)
	}
	return nil
}

// ---- 取得（参加） -----------------------------------------------------------

// CloneOptions は取得の入力。
type CloneOptions struct {
	Remote Remote
	// Dest は作業コピーの作成先（存在しないこと。成功したときだけ作られる）。
	Dest string
	// Credential は取得時に入力された認証情報（同期先が要する場合）。成功後、呼び出し側が
	// 取得した project_id で登録する。
	Credential *keymanager.SyncCredential
	// KnownProject は同一 project_id の作業コピーが既にこの端末にあればそのパスを返す（重複取得の判定）。
	KnownProject func(projectID string) (string, bool)
}

// CloneResult は取得の結果。
type CloneResult struct {
	Root             string
	ProjectID        string
	TargetSystemName string
	// Integrated は自分のブランチを作るために統合した他メンバー（author_id）。
	Integrated []string
	Summary    Summary
}

// Clone は同期先から作業コピーを取得する。成否が確定するまで一時領域で行う。
func (c *Client) Clone(ctx context.Context, opts CloneOptions) (result *CloneResult, err error) {
	const op = OpClone
	remote := opts.Remote
	rec := Record{At: time.Now().UTC(), Author: c.author.AuthorID, Op: op, RemoteKind: remote.Kind, RemoteLocation: remote.Display()}
	defer func() {
		if err != nil {
			f := failureFrom(op, err)
			err = f
			rec.Result, rec.Failure = resultOf(f)
			// 失敗時は作業コピーが無いため記録先も無い（記録は成功時のみ）
		}
	}()
	if err := remote.Validate(); err != nil {
		return nil, newFailure(FailNotFound, op, err.Error())
	}
	if !c.git.availability().Available {
		return nil, newFailure(FailUnavailable, op, "")
	}
	dest := filepath.Clean(opts.Dest)
	if dest == "" || dest == "." {
		return nil, fmt.Errorf("作業コピーの作成先を指定してください。")
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("作成先「%s」は既にあります。別の場所を指定してください。", dest)
	}
	if remote.Kind == RemoteFolder {
		if err := c.checkFolderRemote(ctx, op, remote, false); err != nil {
			return nil, err
		}
	}
	h, err := c.handoffFor(ctx, op, remote, "", opts.Credential)
	if err != nil {
		return nil, err
	}
	defer h.Cleanup()

	// 一時領域（作成先と同じ親フォルダ = 同一ボリューム内で rename できる）
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("作成先の親フォルダを用意できません: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".reqweave-clone-")
	if err != nil {
		return nil, fmt.Errorf("取得用の一時領域を作成できません: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmp)
		}
	}()

	mine := BranchName(c.author.AuthorID)
	if _, err := c.git.run(ctx, tmp, nil, nil, "init", "-q"); err != nil {
		return nil, err
	}
	if _, err := c.git.run(ctx, tmp, nil, nil, "symbolic-ref", "HEAD", "refs/heads/"+mine); err != nil {
		return nil, err
	}
	c.progress(StageFetch)
	if err := c.fetchAll(ctx, tmp, h, remote); err != nil {
		return nil, err
	}
	branches, err := c.syncBranches(ctx, tmp)
	if err != nil {
		return nil, err
	}
	if len(branches) == 0 {
		return nil, newFailure(FailNotFound, op, "同期先に反映済みの内容がありません")
	}
	// 自分のブランチがあればそれを、無ければ名前順の先頭から作り、残りを統合する
	start := branches[0]
	for _, b := range branches {
		if b.Branch == mine {
			start = b
			break
		}
	}
	if _, err := c.git.run(ctx, tmp, nil, nil, "update-ref", "refs/heads/"+mine, start.SHA); err != nil {
		return nil, err
	}
	if _, err := c.git.run(ctx, tmp, nil, nil, "reset", "-q", "--hard", start.SHA); err != nil {
		return nil, err
	}
	if err := ensureExclude(tmp); err != nil {
		return nil, err
	}
	c.progress(StageMerge)
	integrated, conflicts, err := c.integrate(ctx, tmp, branches)
	if err != nil {
		return nil, err
	}

	// メンバー照合（メンバーでなければ内容を表示せずに案内する）と重複取得の判定
	projectData, err := os.ReadFile(filepath.Join(tmp, projectstore.FileProject))
	if err != nil {
		return nil, newFailure(FailNotFound, op, "同期先の内容にプロジェクト設定がありません")
	}
	project, err := projectstore.UnmarshalProject(projectData)
	if err != nil {
		return nil, newFailure(FailNotFound, op, err.Error())
	}
	membersData, err := os.ReadFile(filepath.Join(tmp, projectstore.FileMembers))
	if err != nil {
		return nil, newFailure(FailNotFound, op, "同期先の内容にメンバー一覧がありません")
	}
	members, err := projectstore.UnmarshalMembers(membersData)
	if err != nil {
		return nil, newFailure(FailNotFound, op, err.Error())
	}
	if _, ok := members.Find(c.author.AuthorID); !ok {
		return nil, newFailure(FailNotMember, op, "")
	}
	if opts.KnownProject != nil {
		if path, known := opts.KnownProject(project.ProjectID); known {
			return nil, newFailure(FailAlreadyPresent, op, "既存の作業コピー: "+path)
		}
	}
	// 反映済み位置を記録し（取得直後は同期先と一致）、locks/ を用意して作成先へ移す
	head, err := c.headSHA(ctx, tmp)
	if err != nil {
		return nil, err
	}
	if _, err := c.git.run(ctx, tmp, nil, nil, "update-ref", publishedRef, head); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Join(tmp, "locks"), 0o755)
	c.progress(StageFinalize)
	if err := os.Rename(tmp, dest); err != nil {
		return nil, fmt.Errorf("作業コピーを作成先へ移せません: %w", err)
	}
	summary, _ := c.diffSummary(ctx, dest, "", head)
	rec.Summary, rec.Conflicts, rec.Result = summary, conflicts, ResultOK
	_ = c.recorder.RecordSync(dest, rec)
	return &CloneResult{Root: dest, ProjectID: project.ProjectID, TargetSystemName: project.TargetSystemName,
		Integrated: integrated, Summary: summary}, nil
}

// fetchAll は同期先の全ブランチ（work/*）を refs/sync/work/* へ取得する（消えたブランチは除去）。
func (c *Client) fetchAll(ctx context.Context, root string, h *Handoff, remote Remote) error {
	_, err := c.git.run(ctx, root, h, nil, "fetch", "-q", "--prune", "--no-tags", remote.gitURL(),
		"+refs/heads/work/*:"+syncRefPrefix+"*")
	return err
}

// integrate は取得済みの他ブランチを名前順に自分のブランチへ統合する。
// 戻り値は統合した作業者と三面マージで解決した競合の件数。競合が未解決なら ErrMergeConflict を包んで返す。
func (c *Client) integrate(ctx context.Context, root string, branches []branchRef) ([]string, int, error) {
	mine := "refs/heads/" + BranchName(c.author.AuthorID)
	repo := &Repo{c: c, root: root}
	var integrated []string
	conflicts := 0
	for _, b := range branches {
		head, err := c.headSHA(ctx, root)
		if err != nil {
			return nil, 0, err
		}
		if head == "" {
			// まだコミットが無い作業コピー（初回取り込み）: 相手の位置をそのまま採る
			if err := c.moveTo(ctx, root, mine, b.SHA, ""); err != nil {
				return nil, 0, err
			}
			integrated = append(integrated, b.AuthorID())
			continue
		}
		if done, err := c.isAncestor(ctx, root, b.SHA, head); err != nil {
			return nil, 0, err
		} else if done {
			continue // 統合済み
		}
		if ff, err := c.isAncestor(ctx, root, head, b.SHA); err != nil {
			return nil, 0, err
		} else if ff {
			// 自分の分は追随（別端末からの反映）
			if err := c.moveTo(ctx, root, mine, b.SHA, head); err != nil {
				return nil, 0, err
			}
			integrated = append(integrated, b.AuthorID())
			continue
		}
		// 確定版の版番号の衝突を、統合の前に自分側の再採番で解消する。
		// 先に解消しておくことで `documents/<種別>/v<N>/` の食い違いが統合の対象にならない。
		newHead, resolvedPaths, err := c.resolveVersionCollisions(ctx, root, b.SHA)
		if err != nil {
			return nil, 0, err
		}
		if newHead != "" {
			head = newHead
		}
		base, err := c.mergeBase(ctx, root, head, b.SHA)
		if err != nil {
			return nil, 0, err
		}
		res, err := c.merger.Merge(ctx, repo, MergeInput{Base: base, Ours: head, Theirs: b.SHA,
			TheirsBranch: b.Branch, TheirsAuthor: b.AuthorID(), Resolved: resolvedPaths})
		if err != nil {
			return nil, 0, err
		}
		msg := fmt.Sprintf("取り込み: %s ← %s %s", c.author.DisplayName, b.AuthorID(), time.Now().UTC().Format(time.RFC3339))
		out, err := c.git.run(ctx, root, nil, nil, "commit-tree", res.Tree, "-p", head, "-p", b.SHA, "-m", msg)
		if err != nil {
			return nil, 0, err
		}
		if err := c.moveTo(ctx, root, mine, strings.TrimSpace(out), head); err != nil {
			return nil, 0, err
		}
		integrated = append(integrated, b.AuthorID())
		conflicts += res.Conflicts
	}
	return integrated, conflicts, nil
}

// resolveVersionCollisions は相手側の版履歴を読み、自分側の確定版を再採番する。
//
// 変更が生じたときは作業ツリーの変更をコミットして新しい HEAD を返し（何もしなければ空文字）、
// 統合済みにしたパス（版履歴）を返す。
func (c *Client) resolveVersionCollisions(ctx context.Context, root, theirSHA string) (string, []string, error) {
	if c.versions == nil {
		return "", nil, nil
	}
	theirs := map[string][]byte{}
	for _, kind := range []string{projectstore.DocKindRequirements, projectstore.DocKindBasicDesign} {
		out, err := c.git.run(ctx, root, nil, nil, "show", theirSHA+":"+projectstore.VersionsFile(kind))
		if err != nil {
			continue // 相手に当該種別の版履歴が無い（未確定）
		}
		theirs[kind] = []byte(out)
	}
	if len(theirs) == 0 {
		return "", nil, nil
	}
	applied, resolved, err := c.versions.ResolveVersionCollisions(root, theirs)
	if err != nil {
		return "", nil, err
	}
	if applied == 0 && len(resolved) == 0 {
		return "", nil, nil
	}
	head, _, err := c.commitPending(ctx, root)
	return head, resolved, err
}

// moveTo は自分のブランチと作業ツリーを sha へ進める（old は期待する現在値。空なら検査しない）。
func (c *Client) moveTo(ctx context.Context, root, ref, sha, old string) error {
	args := []string{"update-ref", ref, sha}
	if old != "" {
		args = append(args, old)
	}
	if _, err := c.git.run(ctx, root, nil, nil, args...); err != nil {
		return err
	}
	_, err := c.git.run(ctx, root, nil, nil, "reset", "-q", "--hard", sha)
	return err
}

// ---- 取り込み ---------------------------------------------------------------

// IncorporateResult は取り込みの結果。
type IncorporateResult struct {
	// Summary は取り込みで作業コピーに入った変更（復帰点との差分）。
	Summary Summary
	// Integrated は統合した作業者（author_id）。空 = 取り込む変更が無かった。
	Integrated []string
	Conflicts  int
	// Incoming はこの取り込みで作業コピーへ入った内容（変更パスと変更履歴レコード）。
	// 派生インデックスの差分再構築と変更要約に使う。
	Incoming *Incoming
}

// Incorporate は同期先の他メンバーの変更を作業コピーへ取り込む。
// 失敗・中止のときは復帰点へ戻し、部分的に取り込まれた状態を残さない。
func (c *Client) Incorporate(ctx context.Context, root string, remote Remote, projectID string) (result *IncorporateResult, err error) {
	const op = OpIncorporate
	rec := Record{At: time.Now().UTC(), Author: c.author.AuthorID, Op: op, RemoteKind: remote.Kind, RemoteLocation: remote.Display()}
	restorePoint := ""
	defer func() {
		if err != nil {
			f := failureFrom(op, err)
			if errors.Is(err, ErrMergeConflict) {
				f = newFailure(FailCanceled, op, err.Error())
				f.Message = "他のメンバーと同じ対象が変更されているため、取り込みには内容の確認（三面マージ）が必要です。" + unchangedNote(op)
			}
			if restorePoint != "" {
				if rerr := c.restore(ctx, root, restorePoint); rerr != nil {
					f.Detail = strings.TrimSpace(f.Detail + "\n復帰に失敗: " + rerr.Error())
				}
			}
			err = f
			rec.Result, rec.Failure = resultOf(f)
			_ = c.recorder.RecordSync(root, rec)
		}
	}()
	if err := remote.Validate(); err != nil {
		return nil, newFailure(FailNotFound, op, err.Error())
	}
	if !c.git.availability().Available {
		return nil, newFailure(FailUnavailable, op, "")
	}
	if remote.Kind == RemoteFolder {
		if err := c.checkFolderRemote(ctx, op, remote, false); err != nil {
			return nil, err
		}
	}
	h, err := c.handoffFor(ctx, op, remote, projectID, nil)
	if err != nil {
		return nil, err
	}
	defer h.Cleanup()
	// 復元した作業コピー（同期の管理情報が無い）は、先に同期先から取り直す。
	// 取り込みは同期先へ書かないため、ここでは利用者の確認を求めない（確認が要るのは反映 = Publish）。
	if NeedsReattach(root) {
		if _, err := c.reconstruct(ctx, root, h, remote); err != nil {
			return nil, err
		}
	}
	c.progress(StageCommit)
	if err := c.ensureRepository(ctx, root); err != nil {
		return nil, err
	}
	head, _, err := c.commitPending(ctx, root)
	if err != nil {
		return nil, err
	}
	restorePoint = head
	c.progress(StageFetch)
	if err := c.fetchAll(ctx, root, h, remote); err != nil {
		return nil, err
	}
	branches, err := c.syncBranches(ctx, root)
	if err != nil {
		return nil, err
	}
	c.progress(StageMerge)
	integrated, conflicts, err := c.integrate(ctx, root, branches)
	if err != nil {
		return nil, err
	}
	c.progress(StageFinalize)
	// 番号帯の確保はここでは行わない（反映の直前だけで行い、反映の成功で確定する）。
	newHead, err := c.headSHA(ctx, root)
	if err != nil {
		return nil, err
	}
	summary, err := c.diffSummary(ctx, root, head, newHead)
	if err != nil {
		return nil, err
	}
	// この取り込みで入った内容（変更パス・変更履歴レコード）を取り出す（派生インデックスの再構築と変更要約に使う）
	incoming, err := c.Incoming(ctx, root, head)
	if err != nil {
		return nil, err
	}
	// 同期先の自分のブランチと一致したなら反映済み位置も更新する（取り込み直後の事前提示を正確にする）
	for _, b := range branches {
		if b.Branch == BranchName(c.author.AuthorID) && b.SHA == newHead {
			_, _ = c.git.run(ctx, root, nil, nil, "update-ref", publishedRef, newHead)
		}
	}
	rec.Summary, rec.Conflicts, rec.Result = summary, conflicts, ResultOK
	_ = c.recorder.RecordSync(root, rec)
	return &IncorporateResult{Summary: summary, Integrated: integrated, Conflicts: conflicts,
		Incoming: incoming}, nil
}

// ---- 反映 -------------------------------------------------------------------

// reserveRanges は番号帯の確保を呼ぶ（受け口が未配線なら何もしない）。
//
// 戻り値は確保を巻き戻すための情報（確保前の `id-ranges.yaml` の内容と、確保によって進んだかどうか）。
func (c *Client) reserveRanges(ctx context.Context, root string, floors map[string]int) (*rangeReservation, error) {
	if c.ranges == nil {
		return nil, nil
	}
	path := filepath.Join(root, projectstore.FileIDRanges)
	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	existed := err == nil
	if err := c.ranges.ReserveIDRanges(root, floors); err != nil {
		return nil, err
	}
	after, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil // 番号帯を使わないプロジェクト（単独利用）
	}
	if err != nil {
		return nil, err
	}
	if existed && string(before) == string(after) {
		return nil, nil // 確保が何も進めなかった
	}
	return &rangeReservation{path: path, before: before, existed: existed}, nil
}

// rangeReservation は反映の直前に行った番号帯の確保（反映が失敗したら巻き戻す）。
type rangeReservation struct {
	path    string
	before  []byte
	existed bool
	// commit は確保だけを含むコミット、parent はその直前の位置（巻き戻し先）。
	commit string
	parent string
}

// rollback は確保を取り消す。作業ツリーの `id-ranges.yaml` を確保前へ戻し、確保だけのコミットを
// 作っていたらブランチをその親へ戻す。**利用者の他の変更には触れない**（reset --hard を使わない）。
func (c *Client) rollbackReservation(ctx context.Context, root string, r *rangeReservation) error {
	if r == nil {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	if r.commit != "" && r.parent != "" {
		mine := "refs/heads/" + BranchName(c.author.AuthorID)
		if _, err := c.git.run(ctx, root, nil, nil, "update-ref", mine, r.parent, r.commit); err != nil {
			return err
		}
		// インデックスを戻した位置へ合わせる（対象のパスのみ。作業ツリーには触れない）。
		// 合わせないと、次の同期で「変更があるのにコミットするものが無い」状態になる。
		if _, err := c.git.run(ctx, root, nil, nil, "reset", "-q", "--", projectstore.FileIDRanges); err != nil {
			return err
		}
	}
	if !r.existed {
		if err := os.Remove(r.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return projectstore.WriteFileAtomic(r.path, r.before)
}

// commitReservation は確保した番号帯だけを 1 コミットにする（利用者の他の変更を巻き込まない）。
// 戻り値はコミット後の HEAD。変更が無ければ空文字。
func (c *Client) commitReservation(ctx context.Context, root string) (string, error) {
	rel := projectstore.FileIDRanges
	if _, err := c.git.run(ctx, root, nil, nil, "add", "--", rel); err != nil {
		return "", err
	}
	if _, err := c.git.run(ctx, root, nil, nil, "diff", "--cached", "--quiet", "--", rel); err == nil {
		return "", nil // 差分なし
	}
	msg := fmt.Sprintf("同期: %s %s 番号帯の確保", c.author.DisplayName, time.Now().UTC().Format(time.RFC3339))
	if _, err := c.git.run(ctx, root, nil, nil, "commit", "-q", "--no-verify", "-m", msg, "--", rel); err != nil {
		return "", err
	}
	return c.headSHA(ctx, root)
}

// remoteRangeFloors は取得済みの同期先ブランチから、対象種別ごとの確保済み最大上限を集める。
//
// 作業コピーの `id-ranges.yaml` はまだ取り込んでいない他メンバーの確保を含まないため、
// 反映の直前にこれを見て区間を跨ぐ。
func (c *Client) remoteRangeFloors(ctx context.Context, root string) (map[string]int, error) {
	branches, err := c.syncBranches(ctx, root)
	if err != nil {
		return nil, err
	}
	floors := map[string]int{}
	for _, b := range branches {
		out, err := c.git.run(ctx, root, nil, nil, "show", b.SHA+":"+projectstore.FileIDRanges)
		if err != nil {
			continue // 当該ブランチに番号帯が無い
		}
		ranges, err := projectstore.UnmarshalIDRanges([]byte(out))
		if err != nil {
			continue // 読めない内容は無視する（自分の確保を止めない）
		}
		for key, list := range ranges.Ranges {
			for _, e := range list {
				if e.To > floors[key] {
					floors[key] = e.To
				}
			}
		}
	}
	return floors, nil
}

// PublishPreview は反映の事前提示（何が同期先へ載るかを反映の前に示す）。同期先へは接続しない。
type PublishPreview struct {
	RemoteKind     RemoteKind
	RemoteLocation string
	// Summary は同期先へ載る変更（最後に反映した位置からの差分 + 作業ツリーの未コミット変更）。
	Summary Summary
	// FirstPublish は最後に反映した位置の記録が無い（初回の反映）。
	FirstPublish bool
	// NeedsReattach は同期先からの取り直しが必要（自動退避から復元した作業コピー）。
	// このとき Summary は空で、載る内容は取り直しのあとに確定する。
	NeedsReattach bool
	// RestorePending は取り直しの直後で、反映に利用者の確認が要る。
	RestorePending bool
}

// PreviewPublish は何が同期先へ載るかを数える（内容は変えない。コミットも作らない）。
func (c *Client) PreviewPublish(ctx context.Context, root string, remote Remote) (*PublishPreview, error) {
	const op = OpPublish
	if !c.git.availability().Available {
		return nil, newFailure(FailUnavailable, op, "")
	}
	preview := &PublishPreview{RemoteKind: remote.Kind, RemoteLocation: remote.Display()}
	// 復元した作業コピーは、同期先から取り直すまで何が載るかを決められない
	// （同期先へは接続しない = 事前提示の約束。取り直しは利用者の操作で行う）。
	if NeedsReattach(root) {
		preview.NeedsReattach = true
		return preview, nil
	}
	if err := c.ensureRepository(ctx, root); err != nil {
		return nil, failureFrom(op, err)
	}
	restorePending, err := c.RestorePending(ctx, root)
	if err != nil {
		return nil, failureFrom(op, err)
	}
	preview.RestorePending = restorePending
	published, err := c.resolveRef(ctx, root, publishedRef)
	if err != nil {
		return nil, failureFrom(op, err)
	}
	head, err := c.headSHA(ctx, root)
	if err != nil {
		return nil, failureFrom(op, err)
	}
	preview.FirstPublish = published == ""
	committed, err := c.diffSummary(ctx, root, published, head)
	if err != nil {
		return nil, failureFrom(op, err)
	}
	pending, err := c.pendingSummary(ctx, root)
	if err != nil {
		return nil, failureFrom(op, err)
	}
	preview.Summary = mergeSummaries(committed, pending)
	return preview, nil
}

func mergeSummaries(a, b Summary) Summary {
	out := Summary{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		c := out[k]
		c.Added += v.Added
		c.Modified += v.Modified
		c.Removed += v.Removed
		out[k] = c
	}
	return out
}

// PublishOptions は反映の入力。
type PublishOptions struct {
	// CreateIfAbsent は共有フォルダの同期先が無いときに作る（オーナーの初回反映。呼び出し側が権限を判定する）。
	CreateIfAbsent bool
	// AcknowledgeRestore は「復元した内容を同期先へ載せてよい」という利用者の確認
	// （取り直しの直後は、この確認が無い限り同期先へ書かない）。
	AcknowledgeRestore bool
}

// PublishResult は反映の結果。
type PublishResult struct {
	Summary Summary
	// NothingToPublish は同期先が既に最新で送るものが無かった。
	NothingToPublish bool
}

// Publish は自分のブランチを同期先へ反映する（常に fast-forward。反映で他メンバーの内容が失われない）。
func (c *Client) Publish(ctx context.Context, root string, remote Remote, projectID string, opts PublishOptions) (result *PublishResult, err error) {
	const op = OpPublish
	rec := Record{At: time.Now().UTC(), Author: c.author.AuthorID, Op: op, RemoteKind: remote.Kind, RemoteLocation: remote.Display()}
	// 反映の直前に確保した番号帯は、反映が成功したときだけ確定する。
	var reservation *rangeReservation
	defer func() {
		if err != nil {
			f := failureFrom(op, err)
			if rerr := c.rollbackReservation(ctx, root, reservation); rerr != nil {
				f.Detail = strings.TrimSpace(f.Detail + "\n番号帯の確保の取り消しに失敗: " + rerr.Error())
			}
			err = f
			rec.Result, rec.Failure = resultOf(f)
			_ = c.recorder.RecordSync(root, rec)
		}
	}()
	if err := remote.Validate(); err != nil {
		return nil, newFailure(FailNotFound, op, err.Error())
	}
	if !c.git.availability().Available {
		return nil, newFailure(FailUnavailable, op, "")
	}
	if remote.Kind == RemoteFolder {
		if err := c.checkFolderRemote(ctx, op, remote, opts.CreateIfAbsent); err != nil {
			return nil, err
		}
	}
	h, err := c.handoffFor(ctx, op, remote, projectID, nil)
	if err != nil {
		return nil, err
	}
	defer h.Cleanup()
	// 復元した作業コピー（同期の管理情報が無い）は、先に同期先から取り直す。
	if NeedsReattach(root) {
		if _, err := c.reconstruct(ctx, root, h, remote); err != nil {
			return nil, err
		}
	}
	// 取り直しの直後は、利用者が内容を確認するまで同期先へ書かない。
	// 目印は反映が成功したときに消すため、確認せずに再実行しても通らない。
	restorePending, err := c.RestorePending(ctx, root)
	if err != nil {
		return nil, err
	}
	if restorePending && !opts.AcknowledgeRestore {
		return nil, newFailure(FailRestoreUnconfirmed, op, "")
	}
	c.progress(StageCommit)
	if err := c.ensureRepository(ctx, root); err != nil {
		return nil, err
	}
	head, _, err := c.commitPending(ctx, root)
	if err != nil {
		return nil, err
	}
	if head == "" {
		return nil, newFailure(FailInternal, op, "反映する内容がありません（プロジェクトが空です）")
	}
	published, err := c.resolveRef(ctx, root, publishedRef)
	if err != nil {
		return nil, err
	}

	// 同期先の現況を取得してから自分の次の番号帯を確保し、この反映に含める。
	// まだ取り込んでいない他メンバーの確保を跨ぐため、作業コピーの内容だけでは判断しない。
	c.progress(StageFetch)
	if err := c.fetchAll(ctx, root, h, remote); err != nil {
		return nil, err
	}
	floors, err := c.remoteRangeFloors(ctx, root)
	if err != nil {
		return nil, err
	}
	reservation, err = c.reserveRanges(ctx, root, floors)
	if err != nil {
		return nil, err
	}
	if reservation != nil {
		reservation.parent = head
		reserved, err := c.commitReservation(ctx, root)
		if err != nil {
			return nil, err
		}
		if reserved != "" {
			reservation.commit = reserved
			head = reserved
		}
	}
	c.progress(StagePush)
	mine := BranchName(c.author.AuthorID)
	// 非 fast-forward は git 側で拒否される（--force を付けない）。分類は failure.go
	if _, err := c.git.run(ctx, root, h, nil, "push", "-q", "--no-follow-tags", remote.gitURL(),
		"refs/heads/"+mine+":refs/heads/"+mine); err != nil {
		return nil, err
	}
	c.progress(StageFinalize)
	summary, err := c.diffSummary(ctx, root, published, head)
	if err != nil {
		return nil, err
	}
	_, _ = c.git.run(ctx, root, nil, nil, "update-ref", publishedRef, head)
	_, _ = c.git.run(ctx, root, nil, nil, "update-ref", syncRefPrefix+strings.TrimPrefix(mine, "work/"), head)
	// 復元した内容を確認のうえ反映し終えたので、目印を消す。
	c.clearRestorePending(ctx, root)
	rec.Summary, rec.Result = summary, ResultOK
	_ = c.recorder.RecordSync(root, rec)
	return &PublishResult{Summary: summary, NothingToPublish: published == head}, nil
}

// ---- 反映の状況（読み取り専用） ---------------------------------------------

// PublishState は「同期先へまだ載せていない変更があるか」（常時表示する指標）。
//
// **同期先へ接続せず、作業コピーを一切変更しない**（`.git/` を作らない = PreviewPublish との違い）。
// プロジェクト一覧と同期パネルが、開いていないプロジェクトも含めて
// 状態を出すために使う。
type PublishState struct {
	// Initialized は同期の管理情報がこの作業コピーにあるか（まだ一度も同期していなければ false）。
	Initialized bool
	// HasUnpublished は同期先へまだ載せていない変更があるか。
	HasUnpublished bool
	// Summary は未反映の変更の区分ごとの件数（Initialized が false なら空）。
	Summary Summary
}

// PublishStateOf は未反映の変更の有無を調べる。git が無い・まだ同期していない作業コピーでも
// エラーにせず「未初期化」として返す（同期できないことで一覧の表示を止めない）。
//
// **同期の記録は数えない**（IsSyncBookkeeping。数えると指標が常時点灯する）。
// 反映の事前提示（PreviewPublish）は実際に載る内容を示すため、こちらは記録も数える。
func (c *Client) PublishStateOf(ctx context.Context, root string) (PublishState, error) {
	if !c.git.availability().Available || !isRepository(root) {
		return PublishState{}, nil
	}
	published, err := c.resolveRef(ctx, root, publishedRef)
	if err != nil {
		return PublishState{}, err
	}
	head, err := c.headSHA(ctx, root)
	if err != nil {
		return PublishState{}, err
	}
	// 同期そのものが書いた記録は指標から外す（IsSyncBookkeeping の理由を参照）。
	committed, err := c.diffSummaryFiltered(ctx, root, published, head, IsSyncBookkeeping)
	if err != nil {
		return PublishState{}, err
	}
	pending, err := c.pendingSummaryFiltered(ctx, root, IsSyncBookkeeping)
	if err != nil {
		return PublishState{}, err
	}
	summary := mergeSummaries(committed, pending)
	return PublishState{Initialized: true, HasUnpublished: summary.Total() > 0, Summary: summary}, nil
}
