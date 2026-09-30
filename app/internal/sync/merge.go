package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// 取り込み時の統合の受け口と、ツリーの組み立ての共通部分。
//
// 実際の突き合わせ（レコード / エントリ / フィールド / 章単位の提示と承認）は
// threeway.go の ThreeWayMerger が行う。統合は一時インデックス上で組み立て、git の自動マージ機構は
// 使わない（コンフリクトマーカーを作業ツリーへ書かせない）。
// 承認が得られない競合が残る場合は ConflictError を返し、呼び出し側が中止して復帰点へ戻す
// （承認なしに統合しない。後勝ち上書きの経路を持たない）。

// MergeInput は統合 1 回分の入力。Base / Ours / Theirs はコミット ID（Base は共通祖先。無ければ空ツリー扱い）。
type MergeInput struct {
	Base         string
	Ours         string
	Theirs       string
	TheirsBranch string
	TheirsAuthor string
	// Resolved は統合の前に解決済みにしたパス（確定版の版番号の再採番で直した版履歴）。相手側の内容を自分側へ
	// 取り込み済みであるため、これらのパスは競合とせず**自分側を採る**。
	Resolved []string
}

// MergeResult は統合結果。Tree は統合後のツリー ID、Conflicts は承認を経て解決した競合の件数。
type MergeResult struct {
	Tree      string
	Conflicts int
}

// Merger は取り込み時の統合の実装。
type Merger interface {
	Merge(ctx context.Context, repo *Repo, in MergeInput) (*MergeResult, error)
}

// ErrMergeConflict は同一対象を両側が変更しており、承認を伴う三面マージが必要であることを表す。
var ErrMergeConflict = errors.New("三面マージが必要です")

// ConflictError は競合の詳細（対象パス）。
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%v: %d 件（%s）", ErrMergeConflict, len(e.Paths), strings.Join(e.Paths, ", "))
}

// Unwrap は ErrMergeConflict を返す（errors.Is で判定できる）。
func (e *ConflictError) Unwrap() error { return ErrMergeConflict }

// treeChange は 1 パスの変更（diff-tree の 1 行）。
type treeChange struct {
	Mode   string // 変更後のモード（削除時は "000000"）
	SHA    string // 変更後の blob（削除時はゼロ）
	Status byte   // A / M / D / T
	Path   string
}

// diffTree は base → target の変更をパス→変更で返す。
func diffTree(ctx context.Context, repo *Repo, base, target string) (map[string]treeChange, error) {
	if base == "" {
		base = emptyTree
	}
	out, err := repo.Git(ctx, "diff-tree", "-r", "-z", "--no-renames", base, target)
	if err != nil {
		return nil, err
	}
	changes := map[string]treeChange{}
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		meta, path := fields[i], fields[i+1]
		// ":<oldmode> <newmode> <oldsha> <newsha> <status>"
		parts := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(parts) < 5 || path == "" {
			continue
		}
		changes[path] = treeChange{Mode: parts[1], SHA: parts[3], Status: parts[4][0], Path: path}
	}
	return changes, nil
}

// applyChanges は base コミットのツリーに changes を適用したツリー ID を返す（一時インデックスを使い、
// 作業ツリーと通常のインデックスに触れない）。
func applyChanges(ctx context.Context, repo *Repo, base string, changes map[string]treeChange) (string, error) {
	indexFile := filepath.Join(repo.Root(), gitDirName, "reqweave-merge-"+uuid.NewString()+".index")
	defer os.Remove(indexFile)
	env := []string{"GIT_INDEX_FILE=" + indexFile}
	if _, err := repo.GitEnv(ctx, env, "read-tree", base); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(changes))
	for p := range changes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ch := changes[p]
		if ch.Status == 'D' {
			if _, err := repo.GitEnv(ctx, env, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		if _, err := repo.GitEnv(ctx, env, "update-index", "--add", "--replace", "--cacheinfo", ch.Mode+","+ch.SHA+","+p); err != nil {
			return "", err
		}
	}
	out, err := repo.GitEnv(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
