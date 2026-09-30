package sync

// 本ファイルは「取り込みで作業コピーへ入った内容」を取り出す（取り込みの最後の手順）。
//
// 用途は 2 つ:
//   - **派生インデックスの差分再構築**: 変更されたパスだけを読み直す。
//     通常操作のたびに更新時刻を確認する経路は廃止し、取り込みの完了時にここへ集約する。
//   - **変更要約**: この取り込みで入った**変更履歴レコード**を渡す。
//     要約の単位は「取り込み」であり、日時の区切りではない（相手が昨日書いた記録が今日の取り込みで入る）。
//
// 起点（Since）は取り込みの位置＝コミット ID で表す。端末内でのみ意味を持つ識別子であり、
// 利用者へは出さない（git の語を画面に出さない）。**確認済みの位置は端末ごとのアプリ設定**
// （`recent_projects` の要素）に持ち、プロジェクトデータへは書かない。

import (
	"context"
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
)

// Incoming は起点から現在までに作業コピーへ入った内容。
type Incoming struct {
	// ID は現在の位置（この取り込みの識別。確認済みの位置として端末側へ保存する値）。
	ID string
	// Since は起点の位置（空 = 起点が無い＝まだ一度も確認していない・取り込んでいない）。
	Since string
	// Paths は変更されたパス（作業コピーからの相対・スラッシュ区切り）。派生インデックスの再構築に使う。
	Paths []string
	// Changes は入ってきた変更履歴レコード（自分の分を含む。絞り込みは呼び出し側が行う）。
	Changes []auditlog.ChangeRecord
	// NoStartingPoint は起点が無かった（Since が空、または起点のコミットがこの作業コピーに無い）。
	//
	// 取り込みが一度も行われていない・別の作業コピーの位置が渡された場合に真になる。
	// **全期間を「前回以降の変更」として見せない**ため、この場合は Changes を空で返す（推定で埋めない）。
	NoStartingPoint bool
}

// IsEmpty は提示する変更が 1 件も無いかを返す。
func (in *Incoming) IsEmpty() bool { return in == nil || len(in.Changes) == 0 }

// Incoming は since（取り込みの位置）から現在までに作業コピーへ入った内容を返す。
//
// since が空、またはこの作業コピーに無いコミットのときは NoStartingPoint を立てて空を返す。
func (c *Client) Incoming(ctx context.Context, root, since string) (*Incoming, error) {
	if !c.git.availability().Available {
		return nil, newFailure(FailUnavailable, OpIncorporate, "")
	}
	if !isRepository(root) {
		// 同期先を設定していない作業コピー（単独利用）。取り込みは存在しない。
		return &Incoming{NoStartingPoint: true}, nil
	}
	head, err := c.headSHA(ctx, root)
	if err != nil {
		return nil, err
	}
	out := &Incoming{ID: head}
	if since == "" {
		out.NoStartingPoint = true
		return out, nil
	}
	resolved, err := c.resolveRef(ctx, root, since)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		// 別の作業コピーの位置・失われたコミット。起点が無いものとして扱う。
		out.NoStartingPoint = true
		return out, nil
	}
	out.Since = resolved
	if resolved == head {
		return out, nil
	}
	return c.incomingBetween(ctx, root, out)
}

// incomingBetween は Since → ID の差分を集める。
func (c *Client) incomingBetween(ctx context.Context, root string, out *Incoming) (*Incoming, error) {
	repo := &Repo{c: c, root: root}
	changes, err := diffTree(ctx, repo, out.Since, out.ID)
	if err != nil {
		return nil, err
	}
	for path := range changes {
		out.Paths = append(out.Paths, path)
	}
	sort.Strings(out.Paths)

	for _, path := range out.Paths {
		if !isChangeHistoryFile(path) {
			continue
		}
		records, err := incomingHistoryLines(ctx, repo, out.Since, out.ID, path)
		if err != nil {
			return nil, err
		}
		out.Changes = append(out.Changes, records...)
	}
	sort.SliceStable(out.Changes, func(i, j int) bool { return out.Changes[i].At.Before(out.Changes[j].At) })
	return out, nil
}

// isChangeHistoryFile は変更履歴の月別 × 作業者別ファイルかを返す。
func isChangeHistoryFile(path string) bool {
	return strings.HasPrefix(path, auditlog.DirHistory+"/") && strings.HasSuffix(path, ".ndjson")
}

// incomingHistoryLines は 1 ファイルについて、起点に無く現在にある変更履歴レコードを返す。
//
// 変更履歴は**追記専用**のため、起点の内容は現在の内容の先頭部分に一致する。
// 一致しない場合（利用者が直接編集した等）は、**行単位で起点に無いものだけ**を採る。
func incomingHistoryLines(ctx context.Context, repo *Repo, since, head, path string) ([]auditlog.ChangeRecord, error) {
	before, err := blobAt(ctx, repo, since, path)
	if err != nil {
		return nil, err
	}
	after, err := blobAt(ctx, repo, head, path)
	if err != nil {
		return nil, err
	}
	if after == "" {
		return nil, nil
	}
	if before != "" && strings.HasPrefix(after, before) {
		return auditlog.ParseChangeLines([]byte(after[len(before):]))
	}
	if before == "" {
		return auditlog.ParseChangeLines([]byte(after))
	}
	known := map[string]bool{}
	for _, line := range strings.Split(before, "\n") {
		if strings.TrimSpace(line) != "" {
			known[line] = true
		}
	}
	var added []string
	for _, line := range strings.Split(after, "\n") {
		if strings.TrimSpace(line) != "" && !known[line] {
			added = append(added, line)
		}
	}
	return auditlog.ParseChangeLines([]byte(strings.Join(added, "\n")))
}
