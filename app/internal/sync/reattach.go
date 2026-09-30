package sync

// 復元した作業コピーの取り直し。
//
// 自動退避の zip は `.git/`（同期の管理情報）を含めない（10 世代を保持するため
// 容量が効き、退避の目的は作業コピーの**内容**を直前の正常な状態へ戻すことにあるため）。
// そこから復元した作業コピーには管理情報が無いため、同期の操作を実行する前に同期先から
// 取り直して再構成し、復元した内容を「自分のブランチへの未反映の変更」として載せる。
//
// 取り直しの直後の反映は**利用者の確認を経る**（無確認で同期先へ書かない。
// 古い内容で他メンバーの成果を上書きしたように見える事態を避ける）。確認の有無は
// PublishOptions.AcknowledgeRestore で渡し、確認が無い間は反映を中止する。

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
)

// restorePendingRef は「取り直しの直後で、反映に利用者の確認が要る」目印（端末内の情報）。
// 反映が成功したときに消す。
const restorePendingRef = "refs/sync/restore-pending"

// ErrNeedsReattach は復元した作業コピーに同期の管理情報が無い状態（取り直しが必要）。
//
// この状態のまま `.git/` を作り直すと、同期先と無関係な履歴になって反映が拒否される。
// 同期の操作は取り直し（Reattach）を経てから行う。
var ErrNeedsReattach = errors.New("同期の管理情報がありません。同期先から取り直してから、もう一度実行してください。作業コピーの内容は変わっていません。")

// NeedsReattach は取り直しが必要な作業コピーかを返す（同期先へは接続しない）。
//
// 判定は「同期した記録（sync-log）があるのに `.git/` が無い」。自動退避からの復元と
// フォルダの手作業コピーがこれに当たる。まだ一度も同期していない作業コピー
// （オーナーが同期先を設定した直後など）は対象外で、通常の初回反映で `.git/` を作る。
func NeedsReattach(root string) bool {
	if isRepository(root) {
		return false
	}
	at, err := auditlog.LastSyncAt(root)
	if err != nil {
		return false
	}
	return !at.IsZero()
}

// ReattachResult は取り直しの結果。
type ReattachResult struct {
	// Reconstructed は同期先の内容から管理情報を再構成したか
	// （false = 同期先にまだ反映された内容が無く、初期化だけを行った）。
	Reconstructed bool
	// RestorePending は反映の前に利用者の確認が要るか（Reconstructed と同値）。
	RestorePending bool
	// Summary は復元した内容と同期先との差（= このあと反映すると載る内容）。
	Summary Summary
}

// Reattach は同期先から管理情報を取り直す。
//
// 同期先からの受信のみで、**同期先へは書かない**。復元した内容も書き換えない
// （同期先にあって復元先に無いファイルだけを戻す = reconstruct）。
// 記録は取得（同期の記録の op の `clone`）として残す（同期先から受け取る操作であり、
// 同期の記録の op を増やさない）。
func (c *Client) Reattach(ctx context.Context, root string, remote Remote, projectID string) (result *ReattachResult, err error) {
	const op = OpClone
	rec := Record{At: time.Now().UTC(), Author: c.author.AuthorID, Op: op, RemoteKind: remote.Kind, RemoteLocation: remote.Display()}
	defer func() {
		if err != nil {
			f := failureFrom(op, err)
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
	if isRepository(root) {
		return nil, newFailure(FailInternal, op, "この作業コピーには同期の管理情報があります（取り直しは不要です）")
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

	reconstructed, err := c.reconstruct(ctx, root, h, remote)
	if err != nil {
		return nil, err
	}
	// 反映すると載る内容（復元した内容と同期先との差）。
	published, err := c.resolveRef(ctx, root, publishedRef)
	if err != nil {
		return nil, err
	}
	head, err := c.headSHA(ctx, root)
	if err != nil {
		return nil, err
	}
	committed, err := c.diffSummary(ctx, root, published, head)
	if err != nil {
		return nil, err
	}
	pending, err := c.pendingSummary(ctx, root)
	if err != nil {
		return nil, err
	}
	summary := mergeSummaries(committed, pending)
	rec.Summary, rec.Result = summary, ResultOK
	_ = c.recorder.RecordSync(root, rec)
	return &ReattachResult{Reconstructed: reconstructed, RestorePending: reconstructed, Summary: summary}, nil
}

// reconstruct は `.git/` を作り、同期先の位置へ索引を合わせる。
//
// 復元した内容は書き換えない。ただし**同期先にあって復元先に無いファイルは戻す**
// （復元した作業コピーが他メンバーの成果の削除として反映されるのを防ぐ。
// 退避の時点より後に同期先へ載った内容が、古い退避によって消えたように見えることを避ける）。
//
// 戻り値は「同期先の内容から再構成したか」。同期先にまだ反映された内容が無い場合は
// 初期化だけを行い false を返す（以後は通常の初回反映と同じ扱い）。
func (c *Client) reconstruct(ctx context.Context, root string, h *Handoff, remote Remote) (bool, error) {
	mine := BranchName(c.author.AuthorID)
	if !isRepository(root) {
		if _, err := c.git.run(ctx, root, nil, nil, "init", "-q"); err != nil {
			return false, err
		}
		if _, err := c.git.run(ctx, root, nil, nil, "symbolic-ref", "HEAD", "refs/heads/"+mine); err != nil {
			return false, err
		}
	}
	if err := ensureExclude(root); err != nil {
		return false, err
	}
	c.progress(StageFetch)
	if err := c.fetchAll(ctx, root, h, remote); err != nil {
		return false, err
	}
	branches, err := c.syncBranches(ctx, root)
	if err != nil {
		return false, err
	}
	if len(branches) == 0 {
		// 同期先にまだ反映された内容が無い（オーナーの初回反映の前）。
		return false, nil
	}
	// 自分のブランチがあればそこから、無ければ名前順の先頭から始める（取得 = Clone と同じ）。
	start := branches[0]
	for _, b := range branches {
		if b.Branch == mine {
			start = b
			break
		}
	}
	if _, err := c.git.run(ctx, root, nil, nil, "update-ref", "refs/heads/"+mine, start.SHA); err != nil {
		return false, err
	}
	// 作業ツリー（復元した内容）はそのままに、索引だけを同期先の位置へ合わせる。
	// 復元した内容と同期先の差が、そのまま「未反映の変更」になる。
	if _, err := c.git.run(ctx, root, nil, nil, "reset", "-q", "--mixed", start.SHA); err != nil {
		return false, err
	}
	if err := c.restoreMissingFiles(ctx, root); err != nil {
		return false, err
	}
	if start.Branch == mine {
		// 自分のブランチから始めたときだけ「最後に反映した位置」が分かる。
		if _, err := c.git.run(ctx, root, nil, nil, "update-ref", publishedRef, start.SHA); err != nil {
			return false, err
		}
	}
	if _, err := c.git.run(ctx, root, nil, nil, "update-ref", restorePendingRef, start.SHA); err != nil {
		return false, err
	}
	return true, nil
}

// restoreMissingFiles は同期先の位置にあって作業ツリーに無いファイルを書き戻す。
//
// 復元した内容（既にあるファイル）は上書きしない。退避に含まれていなかった他メンバーの成果を
// 「削除」として反映しないための措置。
func (c *Client) restoreMissingFiles(ctx context.Context, root string) error {
	out, err := c.git.run(ctx, root, nil, nil, "ls-files", "--deleted", "-z")
	if err != nil {
		return err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	stdin := []byte(strings.Join(paths, "\x00") + "\x00")
	// -f は「無いファイルを書き出す」ため（対象はいずれも作業ツリーに存在しない）。
	_, err = c.git.runInput(ctx, root, nil, nil, stdin, "checkout-index", "-f", "-z", "--stdin")
	return err
}

// RestorePending は「取り直しの直後で、反映に利用者の確認が要る」状態かを返す
// （同期先へは接続せず、作業コピーも変えない）。
func (c *Client) RestorePending(ctx context.Context, root string) (bool, error) {
	if !c.git.availability().Available || !isRepository(root) {
		return false, nil
	}
	sha, err := c.resolveRef(ctx, root, restorePendingRef)
	if err != nil {
		return false, err
	}
	return sha != "", nil
}

// clearRestorePending は確認を経た反映が成功したあとに目印を消す。
func (c *Client) clearRestorePending(ctx context.Context, root string) {
	_, _ = c.git.run(ctx, root, nil, nil, "update-ref", "-d", restorePendingRef)
}
