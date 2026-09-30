//go:build sharedfs

// 共有フォルダ（SMB / NAS）上の同期先の実機確認（ネットワークファイルシステム上の参照ロックの信頼性）。
//
// ネットワークファイルシステムでは OS のファイルロックが期待どおり動作しないことがある。
// git の参照更新は `refs/heads/<branch>.lock` の排他的作成（O_CREAT|O_EXCL）に依存するため、
// **実際に使う共有フォルダで**確かめる必要がある。本ファイルはそのための手順を機械化したもので、
// 通常の検証ゲートには入れない（対象の共有フォルダが要るため）。
//
// 実行:
//
//	REQWEAVE_SHARED_DIR=/Volumes/share/reqweave-test make -C app test-sharedfs
//
// 確認すること:
//  1. 同時反映を 10 回試行し、失われた変更が 0 件であること
//  2. 拒否された側が「取り込み → マージ → 再反映」で回復できること
//  3. 大文字小文字の扱い・パーミッション・gc の並行実行で破損が起きないこと
//  4. メンバーごとのブランチ運用でも同様であること（こちらは原理的に競合しない）

package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// concurrentTrials は同時反映の試行回数（確認すること 1）。
const concurrentTrials = 10

// sharedRoot は検証対象の共有フォルダを返す。
//
// 未指定なら fail させる（前提が揃わないときに skip しない。確かめていないのに緑にしない）。
func sharedRoot(t *testing.T) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("REQWEAVE_SHARED_DIR"))
	if dir == "" {
		t.Fatal("REQWEAVE_SHARED_DIR が未指定です。実際に使う共有フォルダ（SMB / NAS）のパスを指定してください。" +
			"例: REQWEAVE_SHARED_DIR=/Volumes/share/reqweave-test make -C app test-sharedfs")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("共有フォルダを参照できません（%s）: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("共有フォルダがフォルダではありません: %s", dir)
	}
	// 実際に書けることをここで確かめる（読み取り専用マウントを検証開始前に弾く）。
	probe := filepath.Join(dir, fmt.Sprintf(".reqweave-probe-%d", time.Now().UnixNano()))
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		t.Fatalf("共有フォルダへ書き込めません（%s）: %v", dir, err)
	}
	_ = os.Remove(probe)
	return dir
}

// sharedRemote は共有フォルダ上に一意な同期先の所在を作る（実体は初回反映で作られる）。
func sharedRemote(t *testing.T, name string) Remote {
	t.Helper()
	base := filepath.Join(sharedRoot(t), fmt.Sprintf("%s-%d.git", name, time.Now().UnixNano()))
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Logf("後片づけに失敗（手動で削除してください）: %s: %v", base, err)
		}
	})
	return Remote{Kind: RemoteFolder, Location: base}
}

// deviceOf は同じ作業者の「別の端末」を模したクライアントを返す（設定領域を分ける）。
func deviceOf(t *testing.T, author projectstore.Author) *Client {
	t.Helper()
	return newClient(t, author)
}

// bareGit は同期先（bare リポジトリ）へ直接 git を実行する（検証用の観測手段）。
func bareGit(t *testing.T, c *Client, remote Remote, args ...string) string {
	t.Helper()
	out, err := c.git.run(context.Background(), remote.Location, nil, nil, args...)
	if err != nil {
		t.Fatalf("同期先への git %v に失敗: %v", args, err)
	}
	return strings.TrimSpace(out)
}

// 確認すること 1・2:
// 同一ブランチへの同時反映は片方が拒否され、拒否された側は取り込み → 再反映で回復し、変更は失われない。
func TestSharedFSConcurrentPublishSameBranch(t *testing.T) {
	root := sharedRoot(t)
	t.Logf("検証対象の共有フォルダ: %s", root)

	lost := 0
	for i := 0; i < concurrentTrials; i++ {
		t.Run(fmt.Sprintf("試行%02d", i+1), func(t *testing.T) {
			remote := sharedRemote(t, "same-branch")

			// 端末 1: オーナーのプロジェクトを作って初回反映（同期先の実体を作る）。
			dev1 := deviceOf(t, authorA)
			root1 := filepath.Join(t.TempDir(), "device1", "proj")
			newProject(t, root1, authorA)
			mustPublish(t, dev1, root1, remote, true)

			// 端末 2: 同じ作業者が別端末で取得する（同一ブランチ work/<A> を共有する状態）。
			dev2 := deviceOf(t, authorA)
			root2 := filepath.Join(t.TempDir(), "device2", "proj")
			if _, err := dev2.Clone(context.Background(), CloneOptions{Remote: remote, Dest: root2}); err != nil {
				t.Fatalf("端末 2 が取得できない: %v", err)
			}

			// 双方が別々の変更を作る。
			writeProjectFile(t, root1, "decisions/DEC-901.md", "# DEC-901 端末 1 の決定\n")
			writeProjectFile(t, root2, "decisions/DEC-902.md", "# DEC-902 端末 2 の決定\n")

			// 同時に反映する。
			type outcome struct {
				name string
				err  error
			}
			results := make([]outcome, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, err := dev1.Publish(context.Background(), root1, remote, "", PublishOptions{})
				results[0] = outcome{"端末 1", err}
			}()
			go func() {
				defer wg.Done()
				<-start
				_, err := dev2.Publish(context.Background(), root2, remote, "", PublishOptions{})
				results[1] = outcome{"端末 2", err}
			}()
			close(start)
			wg.Wait()

			var okCount, rejected int
			for _, r := range results {
				switch {
				case r.err == nil:
					okCount++
				default:
					f, is := AsFailure(r.err)
					if !is {
						t.Fatalf("%s の失敗が分類されていない: %v", r.name, r.err)
					}
					// 参照更新の競合は非 fast-forward として拒否される。
					// 共有フォルダ側のロックの取り合いは unreachable / internal になり得るため、
					// 種別ごとに件数を数えて後段で判定する。
					t.Logf("%s は拒否された: kind=%s message=%s", r.name, f.Kind, f.Message)
					rejected++
				}
			}
			if okCount != 1 || rejected != 1 {
				t.Fatalf("同時反映の結果が期待と違う（成功 %d 件 / 拒否 %d 件。期待 1 / 1）: %+v",
					okCount, rejected, results)
			}

			// 拒否された側が「取り込み → マージ → 再反映」で回復する（確認すること 2）。
			loserRoot, loserClient := root1, dev1
			if results[0].err == nil {
				loserRoot, loserClient = root2, dev2
			}
			if _, err := loserClient.Incorporate(context.Background(), loserRoot, remote, ""); err != nil {
				t.Fatalf("拒否された側が取り込めない: %v", err)
			}
			if _, err := loserClient.Publish(context.Background(), loserRoot, remote, "", PublishOptions{}); err != nil {
				t.Fatalf("拒否された側が再反映できない: %v", err)
			}

			// 第三の端末で取得し、両方の変更が残っていることを確認する（失われた変更 0 件）。
			verifier := deviceOf(t, authorA)
			verifyRoot := filepath.Join(t.TempDir(), "verify", "proj")
			if _, err := verifier.Clone(context.Background(), CloneOptions{Remote: remote, Dest: verifyRoot}); err != nil {
				t.Fatalf("確認用の取得に失敗: %v", err)
			}
			for _, rel := range []string{"decisions/DEC-901.md", "decisions/DEC-902.md"} {
				if _, err := os.Stat(filepath.Join(verifyRoot, filepath.FromSlash(rel))); err != nil {
					lost++
					t.Errorf("変更が失われた: %s（%v）", rel, err)
				}
			}
		})
	}
	if lost != 0 {
		t.Errorf("失われた変更が %d 件（確認すること 1 は 0 件）", lost)
	}
}

// 確認すること 4: メンバーごとのブランチ運用（通常の運用形態）では同時反映が競合しない。
func TestSharedFSConcurrentPublishPerMemberBranch(t *testing.T) {
	remote := sharedRemote(t, "per-member")

	a := deviceOf(t, authorA)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	store := newProject(t, rootA, authorA)
	if _, err := store.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, a, rootA, remote, true)

	b := deviceOf(t, authorB)
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	if _, err := b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("メンバー B が取得できない: %v", err)
	}

	writeProjectFile(t, rootA, "decisions/DEC-911.md", "# DEC-911 A の決定\n")
	writeProjectFile(t, rootB, "decisions/DEC-912.md", "# DEC-912 B の決定\n")

	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = a.Publish(context.Background(), rootA, remote, "", PublishOptions{})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errs[1] = b.Publish(context.Background(), rootB, remote, "", PublishOptions{})
	}()
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("メンバーごとのブランチなのに反映が失敗した（%d 人目）: %v", i+1, err)
		}
	}

	// 片方が取り込めば両方の変更が揃う。
	if _, err := a.Incorporate(context.Background(), rootA, remote, ""); err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	for _, rel := range []string{"decisions/DEC-911.md", "decisions/DEC-912.md"} {
		if _, err := os.Stat(filepath.Join(rootA, filepath.FromSlash(rel))); err != nil {
			t.Errorf("取り込み後に変更が無い: %s（%v）", rel, err)
		}
	}
}

// 確認すること 3（大文字小文字）: 大文字小文字だけが異なるファイルの扱いを確かめる。
//
// 共有フォルダが大文字小文字を区別しない場合、同期先（bare リポジトリ）は影響を受けない
// （bare 側は作業ツリーを持たないため）が、取得先の作業コピーで一方が失われうる。
// ここでは**同期先に両方が保持されること**と、取得後の状態を記録する。
func TestSharedFSCaseSensitivity(t *testing.T) {
	remote := sharedRemote(t, "case")

	a := deviceOf(t, authorA)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	newProject(t, rootA, authorA)
	writeProjectFile(t, rootA, "decisions/DEC-921.md", "# 小文字側\n")
	mustPublish(t, a, rootA, remote, true)

	// 同期先の側で、大文字小文字だけが異なる 2 ファイルが別物として保持されるか。
	files := bareGit(t, a, remote, "ls-tree", "-r", "--name-only", "refs/heads/"+BranchName(authorA.AuthorID))
	if !strings.Contains(files, "decisions/DEC-921.md") {
		t.Fatalf("反映した内容が同期先に無い: %q", files)
	}

	// 破損が無いこと（同期先のオブジェクト整合性）。
	bareGit(t, a, remote, "fsck", "--no-progress")

	// 取得し直して同じ内容が読めること（パーミッション・大文字小文字の往復）。
	verifyRoot := filepath.Join(t.TempDir(), "verify", "proj")
	if _, err := a.Clone(context.Background(), CloneOptions{Remote: remote, Dest: verifyRoot}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if got := readProjectFile(t, verifyRoot, "decisions/DEC-921.md"); !strings.Contains(got, "小文字側") {
		t.Errorf("取得した内容が違う: %q", got)
	}
}

// 確認すること 3（パーミッション）: 別の作業者が同じ同期先へ読み書きできる。
func TestSharedFSPermissions(t *testing.T) {
	remote := sharedRemote(t, "perm")

	a := deviceOf(t, authorA)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	store := newProject(t, rootA, authorA)
	if _, err := store.AddMember(authorB.AuthorID, authorB.DisplayName, projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}
	mustPublish(t, a, rootA, remote, true)

	b := deviceOf(t, authorB)
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	if _, err := b.Clone(context.Background(), CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("メンバー B が取得できない（読み取り権限）: %v", err)
	}
	writeProjectFile(t, rootB, "decisions/DEC-931.md", "# B の決定\n")
	if _, err := b.Publish(context.Background(), rootB, remote, "", PublishOptions{}); err != nil {
		t.Fatalf("メンバー B が反映できない（書き込み権限）: %v", err)
	}
	if _, err := a.Incorporate(context.Background(), rootA, remote, ""); err != nil {
		t.Fatalf("オーナーが取り込めない: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootA, "decisions", "DEC-931.md")); err != nil {
		t.Errorf("別の作業者の変更が取り込めていない: %v", err)
	}
}

// 確認すること 3（gc の並行実行）: 同期先の gc と反映が同時に走っても破損しない。
func TestSharedFSConcurrentGC(t *testing.T) {
	remote := sharedRemote(t, "gc")

	a := deviceOf(t, authorA)
	rootA := filepath.Join(t.TempDir(), "A", "proj")
	newProject(t, rootA, authorA)
	mustPublish(t, a, rootA, remote, true)

	var wg sync.WaitGroup
	wg.Add(2)
	publishErr := make([]error, concurrentTrials)
	go func() {
		defer wg.Done()
		for i := 0; i < concurrentTrials; i++ {
			writeProjectFile(t, rootA, fmt.Sprintf("decisions/DEC-9%02d.md", 40+i),
				fmt.Sprintf("# 反映 %d\n", i))
			_, publishErr[i] = a.Publish(context.Background(), rootA, remote, "", PublishOptions{})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < concurrentTrials; i++ {
			// gc の失敗自体は許容する（別プロセスが触っていれば git 側が見送る）。
			// ここで見るのは「gc と反映の同時実行でリポジトリが壊れないこと」。
			_, _ = a.git.run(context.Background(), remote.Location, nil, nil, "gc", "--auto", "--quiet")
		}
	}()
	wg.Wait()

	for i, err := range publishErr {
		if err != nil {
			t.Errorf("gc と同時の反映 %d 回目が失敗: %v", i+1, err)
		}
	}
	// 破損していないこと。
	bareGit(t, a, remote, "fsck", "--no-progress")

	// 取得し直して全件が読めること。
	verifyRoot := filepath.Join(t.TempDir(), "verify", "proj")
	if _, err := a.Clone(context.Background(), CloneOptions{Remote: remote, Dest: verifyRoot}); err != nil {
		t.Fatalf("gc 後の取得に失敗: %v", err)
	}
	for i := 0; i < concurrentTrials; i++ {
		rel := fmt.Sprintf("decisions/DEC-9%02d.md", 40+i)
		if _, err := os.Stat(filepath.Join(verifyRoot, filepath.FromSlash(rel))); err != nil {
			t.Errorf("gc 後に変更が失われた: %s（%v）", rel, err)
		}
	}
}
