//go:build integration

// 結合テスト（共同プロジェクトの同期下の整合性。基準 1〜5 は各テストの見出しに書く）。
// 実行: make -C app test-integration
//
// 実際の git・共有フォルダ相当の bare リポジトリ・2 つの作業コピーで、各基準を
// そのまま試行回数つきで確かめる。基準 4（強制終了）は本テストのバイナリを子プロセスとして
// 起動し、同期の途中で子プロセス自身を強制終了させて確かめる（後始末が走らないことを担保するため）。
//
// 補助（作業コピーの生成・反映・取り込み）は ops_test.go の関数を使う（同一パッケージ）。

package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 強制終了の子プロセスへ渡す入力（環境変数）。
const (
	envKillOp     = "REQWEAVE_TEST_SYNC_KILL_OP"     // "incorporate" / "publish"
	envKillStage  = "REQWEAVE_TEST_SYNC_KILL_STAGE"  // 強制終了する段階（Stage の値）
	envKillRoot   = "REQWEAVE_TEST_SYNC_KILL_ROOT"   // 作業コピー
	envKillRemote = "REQWEAVE_TEST_SYNC_KILL_REMOTE" // 同期先の所在（共有フォルダ）
	envKillConfig = "REQWEAVE_TEST_SYNC_KILL_CONFIG" // 同期モジュールの設定領域
)

// 基準 1（別メンバー）: 2 台から同一の同期先へ同時に反映する試行を 10 回行い、
// 先に載った内容が失われないこと（更新消失 0 件）。
//
// メンバーごとにブランチが分かれるため、別メンバーの同時反映は双方が成立する。
// ここで確かめるのは「同時に書いても一方の内容が消えない」ことそのもの。
// 一方が拒否される経路（非 fast-forward）は同一メンバーの 2 端末の場合であり、次のテストで確かめる。
func TestSimultaneousPublishByDifferentMembersLosesNothing(t *testing.T) {
	const trials = 10
	for i := 0; i < trials; i++ {
		t.Run(fmt.Sprintf("試行%02d", i+1), func(t *testing.T) {
			a, b, rootA, rootB, remote := setupShared(t)
			fileA := fmt.Sprintf("decisions/DEC-A%02d.md", i)
			fileB := fmt.Sprintf("decisions/DEC-B%02d.md", i)
			writeProjectFile(t, rootA, fileA, "A の決定\n")
			writeProjectFile(t, rootB, fileB, "B の決定\n")

			errs := publishConcurrently(t,
				publishJob{client: a, root: rootA, remote: remote},
				publishJob{client: b, root: rootB, remote: remote})
			for idx, err := range errs {
				if err != nil {
					t.Fatalf("別メンバーの同時反映が失敗した（%d 番目）: %v", idx, err)
				}
			}
			assertBothWorkCopiesHave(t, a, b, rootA, rootB, remote, fileA, fileB)
		})
	}
}

// 基準 1（同一メンバーの 2 端末）: 同じブランチへ同時に反映する試行を 10 回行い、
// 一方だけが成立して先に載った内容が失われず、拒否された側が「先に取り込みが必要」へ誘導され、
// 取り込み → 再反映で双方の内容が載ること。
func TestSimultaneousPublishFromTwoTerminalsRejectsOne(t *testing.T) {
	const trials = 10
	rejected := 0
	for i := 0; i < trials; i++ {
		t.Run(fmt.Sprintf("試行%02d", i+1), func(t *testing.T) {
			// 同一の作業者 A が 2 端末（別フォルダ・別設定領域）から反映する
			owner, _, rootA, _, remote := setupShared(t)
			second := newClient(t, authorA)
			root2 := filepath.Join(t.TempDir(), "A2", "proj")
			if _, err := second.Clone(context.Background(), CloneOptions{Remote: remote, Dest: root2}); err != nil {
				t.Fatalf("2 台目の取得に失敗: %v", err)
			}
			file1 := fmt.Sprintf("decisions/DEC-T1%02d.md", i)
			file2 := fmt.Sprintf("decisions/DEC-T2%02d.md", i)
			writeProjectFile(t, rootA, file1, "1 台目の決定\n")
			writeProjectFile(t, root2, file2, "2 台目の決定\n")

			errs := publishConcurrently(t,
				publishJob{client: owner, root: rootA, remote: remote},
				publishJob{client: second, root: root2, remote: remote})

			clients := []*Client{owner, second}
			roots := []string{rootA, root2}
			files := []string{file1, file2}
			winner, loser := -1, -1
			for idx, err := range errs {
				if err == nil {
					winner = idx
				} else {
					loser = idx
				}
			}
			if winner < 0 || loser < 0 {
				t.Fatalf("同一ブランチへの同時反映で成功と拒否が 1 つずつにならなかった: %v / %v", errs[0], errs[1])
			}
			rejected++

			// 拒否された側は「先に取り込みが必要」（non_fast_forward）
			f := expectFailure(t, errs[loser], FailNonFastForward)
			if !strings.Contains(f.Message, "取り込") {
				t.Errorf("取り込みへの誘導が無い: %q", f.Message)
			}
			// 先に載った内容が同期先に残っている（更新消失 0 件）
			assertRemoteHasFile(t, clients[winner], remote, files[winner])

			// 取り込み → 再反映で双方の内容が載る
			mustIncorporate(t, clients[loser], roots[loser], remote)
			mustPublish(t, clients[loser], roots[loser], remote, false)
			mustIncorporate(t, clients[winner], roots[winner], remote)
			for idx, root := range roots {
				for _, rel := range files {
					if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
						t.Errorf("%d 台目の作業コピーに %s が無い（更新消失）: %v", idx+1, rel, err)
					}
				}
			}
		})
	}
	if rejected != trials {
		t.Fatalf("拒否された試行が %d 回（期待 %d 回。同時反映の一方が拒否される経路を通っていない）", rejected, trials)
	}
}

// publishJob は同時反映の 1 件。
type publishJob struct {
	client *Client
	root   string
	remote Remote
}

// publishConcurrently は 2 件の反映を同時に開始し、それぞれの結果を返す。
func publishConcurrently(t *testing.T, jobs ...publishJob) []error {
	t.Helper()
	errs := make([]error, len(jobs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job publishJob) {
			defer wg.Done()
			<-start // 双方の準備が整ってから同時に開始する
			_, errs[i] = job.client.Publish(context.Background(), job.root, job.remote, "", PublishOptions{})
		}(i, job)
	}
	close(start)
	wg.Wait()
	return errs
}

// 基準 2: 異なる対象を並行に変更し、双方を反映・取り込みする試行を 10 回行い、
// 双方の変更がすべて残ること。
func TestParallelDisjointChangesAllSurvive(t *testing.T) {
	const trials = 10
	a, b, rootA, rootB, remote := setupShared(t)
	var written []string
	for i := 0; i < trials; i++ {
		fileA := fmt.Sprintf("decisions/DEC-A%02d.md", i)
		fileB := fmt.Sprintf("requirements/FR-B%02d.md", i)
		writeProjectFile(t, rootA, fileA, fmt.Sprintf("A の決定 %d\n", i))
		writeProjectFile(t, rootB, fileB, fmt.Sprintf("B の要件 %d\n", i))
		written = append(written, fileA, fileB)

		mustPublish(t, a, rootA, remote, false)
		mustIncorporate(t, b, rootB, remote)
		mustPublish(t, b, rootB, remote, false)
		mustIncorporate(t, a, rootA, remote)

		// 各回のあとで、その回までのすべての変更が双方に残っていること
		for _, rel := range written {
			for label, root := range map[string]string{"A": rootA, "B": rootB} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
					t.Fatalf("試行%02d: %s の作業コピーから %s が失われた: %v", i+1, label, rel, err)
				}
			}
		}
	}
	if len(written) != trials*2 {
		t.Fatalf("検証対象のファイル数が %d（期待 %d）", len(written), trials*2)
	}
}

// 基準 3: 同一レコードへの相反する変更が取り込み時に競合として検知され、
// 承認を経ずに統合されないこと（後勝ち上書き 0 件）。
func TestConflictingChangeIsNeverAutoMerged(t *testing.T) {
	const target = "decisions/DEC-500.md"
	a, b, rootA, rootB, remote := setupShared(t)
	writeProjectFile(t, rootA, target, "共通の下地\n")
	mustPublish(t, a, rootA, remote, false)
	mustIncorporate(t, b, rootB, remote)

	// 同一レコードへ相反する変更
	const mine, theirs = "A が書いた本文\n", "B が書いた本文\n"
	writeProjectFile(t, rootA, target, mine)
	writeProjectFile(t, rootB, target, theirs)
	mustPublish(t, b, rootB, remote, false)

	// 承認の受け口を持たない Client では、競合は中止（後勝ち上書きをしない）
	_, err := a.Incorporate(context.Background(), rootA, remote, "")
	f := expectFailure(t, err, FailCanceled)
	if !strings.Contains(f.Message, "確認") {
		t.Errorf("本人の確認が要る旨が無い: %q", f.Message)
	}

	// 作業コピーは取り込み前のまま（相手の内容で上書きされていない）
	if got := readProjectFile(t, rootA, target); got != mine {
		t.Errorf("承認なしに統合された（後勝ち上書き）: %q", got)
	}
	assertNoConflictMarkers(t, rootA)
	if _, err := projectstore.Open(rootA, authorA); err != nil {
		t.Errorf("競合の中止後に作業コピーを開けない: %v", err)
	}
}

// 基準 5: 同期先へ到達できない状態での作業を 5 回行い、
// 接続の回復後の反映ですべての変更が同期先へ載ること（消失 0 件）。
func TestOfflineChangesAllPublishedAfterRecovery(t *testing.T) {
	const rounds = 5
	a, b, rootA, rootB, remote := setupShared(t)

	// 到達できない同期先（共有フォルダの実体が無い所在）
	offline := Remote{Kind: RemoteFolder, Location: filepath.Join(t.TempDir(), "unreachable", "proj.git")}
	var offlineFiles []string
	for i := 0; i < rounds; i++ {
		rel := fmt.Sprintf("decisions/DEC-OFF%02d.md", i)
		writeProjectFile(t, rootA, rel, fmt.Sprintf("オフライン中の決定 %d\n", i))
		offlineFiles = append(offlineFiles, rel)
		// 到達できないので反映は失敗する（作業は続けられる）
		_, err := a.Publish(context.Background(), rootA, offline, "", PublishOptions{})
		f := expectFailure(t, err, FailUnreachable)
		if !strings.Contains(f.Message, "作業はこのまま続けられます") {
			t.Errorf("作業を続けられる旨が無い: %q", f.Message)
		}
		// 失敗しても作業コピーの内容は残る
		if _, err := os.Stat(filepath.Join(rootA, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("到達不能の反映で %s が失われた: %v", rel, err)
		}
	}

	// 接続の回復後の反映で、オフライン中のすべての変更が同期先へ載る
	mustPublish(t, a, rootA, remote, false)
	mustIncorporate(t, b, rootB, remote)
	for _, rel := range offlineFiles {
		if _, err := os.Stat(filepath.Join(rootB, filepath.FromSlash(rel))); err != nil {
			t.Errorf("オフライン中の変更 %s が同期先へ載っていない: %v", rel, err)
		}
	}
	if len(offlineFiles) != rounds {
		t.Fatalf("検証対象が %d 件（期待 %d 件）", len(offlineFiles), rounds)
	}
}

// 基準 4: 取り込み・反映の途中でプロセスを強制終了する試行を各 5 回行い、
// いずれも作業コピーが破損なく開けること・中途半端に統合された状態やコンフリクトマーカーを
// 含むファイルが残らないこと。
//
// 「後始末（defer の復帰処理）が走らないこと」を担保するため、子プロセスを起動して
// 同期の途中で**子プロセス自身を強制終了**させる（同一プロセス内の panic では defer が走ってしまう）。
func TestForcedTerminationLeavesNoPartialState(t *testing.T) {
	// 取り込み・反映それぞれ 5 回。段階は同期の各局面を順に当たるように選ぶ。
	cases := []struct {
		op    Operation
		stage Stage
	}{
		{OpIncorporate, StageCommit}, {OpIncorporate, StageFetch}, {OpIncorporate, StageMerge},
		{OpIncorporate, StageFinalize}, {OpIncorporate, StageMerge},
		{OpPublish, StageCommit}, {OpPublish, StageFetch}, {OpPublish, StagePush},
		{OpPublish, StageFinalize}, {OpPublish, StagePush},
	}
	incorporates, publishes := 0, 0
	for i, c := range cases {
		if c.op == OpIncorporate {
			incorporates++
		} else {
			publishes++
		}
		t.Run(fmt.Sprintf("%s_%s_%02d", c.op, c.stage, i+1), func(t *testing.T) {
			a, b, rootA, rootB, remote := setupShared(t)
			const fromB = "decisions/DEC-KILL-B.md"
			const fromA = "decisions/DEC-KILL-A.md"
			// 取り込む材料（B の変更）と、反映する材料（A の未反映の変更）を用意する
			writeProjectFile(t, rootB, fromB, "B の決定\n")
			mustPublish(t, b, rootB, remote, false)
			writeProjectFile(t, rootA, fromA, "A の決定\n")
			_ = a // 親プロセスの Client はここまで（同期の実行は子プロセスが行う）

			before := treeSnapshot(t, rootA)
			runKillChild(t, c.op, c.stage, rootA, remote)

			// 強制終了の後: 作業コピーがプロジェクトとして開ける
			store, err := projectstore.Open(rootA, authorA)
			if err != nil {
				t.Fatalf("強制終了の後に作業コピーを開けない: %v", err)
			}
			store.Close()
			// コンフリクトマーカーを含むファイルが残らない
			assertNoConflictMarkers(t, rootA)

			after := treeSnapshot(t, rootA)
			switch c.op {
			case OpPublish:
				// 反映は作業ツリーの内容を変えない（コミットするだけ）
				assertSameTree(t, "反映の強制終了で作業ツリーが変わった", before, after)
			case OpIncorporate:
				// 取り込みは「取り込み前」か「取り込み完了」のいずれかで、混ざった状態にならない
				merged := make(map[string]string, len(before)+1)
				for k, v := range before {
					merged[k] = v
				}
				merged[fromB] = hashOf([]byte("B の決定\n"))
				if !sameTree(before, after) && !sameTree(merged, after) {
					t.Errorf("取り込みが中途半端な状態で残った（取り込み前とも完了後とも一致しない）:\n%s",
						treeDiff(before, merged, after))
				}
			}
		})
	}
	if incorporates != 5 || publishes != 5 {
		t.Fatalf("試行回数が要件と違う: 取り込み %d 回 / 反映 %d 回（各 5 回）", incorporates, publishes)
	}
}

// TestSyncKillHelperProcess は TestForcedTerminationLeavesNoPartialState が起動する子プロセスの入口。
//
// 親から環境変数で指示されたときだけ働く（通常のテスト実行では何もしない = go test の
// ヘルパープロセスの定石）。指示された段階に達した時点で**自分自身を強制終了**するため、
// 同期モジュールの defer（復帰処理）は走らない。
func TestSyncKillHelperProcess(t *testing.T) {
	op := os.Getenv(envKillOp)
	if op == "" {
		return // 親から起動されていない（このテストは子プロセス専用の入口）
	}
	target := Stage(os.Getenv(envKillStage))
	root := os.Getenv(envKillRoot)
	remote := Remote{Kind: RemoteFolder, Location: os.Getenv(envKillRemote)}
	client, err := New(Options{Author: authorA, ConfigDir: os.Getenv(envKillConfig),
		Progress: func(s Stage) {
			if s != target {
				return
			}
			// 自分自身を強制終了する（defer・t.Cleanup のいずれも走らない）
			p, err := os.FindProcess(os.Getpid())
			if err != nil {
				panic(err)
			}
			_ = p.Kill()
			select {} // Kill が届くまで戻らない
		}})
	if err != nil {
		t.Fatalf("子プロセス: 同期モジュールを構成できない: %v", err)
	}
	switch Operation(op) {
	case OpIncorporate:
		_, err = client.Incorporate(context.Background(), root, remote, "")
	case OpPublish:
		_, err = client.Publish(context.Background(), root, remote, "", PublishOptions{})
	default:
		t.Fatalf("子プロセス: 未知の操作 %q", op)
	}
	// ここへ到達したら強制終了が起きていない（親が異常終了を期待している）
	t.Fatalf("子プロセス: 段階 %q に達する前に %s が終了した: %v", target, op, err)
}

// runKillChild は子プロセスを起動し、指定の段階で強制終了されたことを確かめる。
func runKillChild(t *testing.T, op Operation, stage Stage, root string, remote Remote) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSyncKillHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		envKillOp+"="+string(op),
		envKillStage+"="+string(stage),
		envKillRoot+"="+root,
		envKillRemote+"="+remote.Location,
		envKillConfig+"="+filepath.Join(t.TempDir(), "syncKill"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("子プロセスが正常終了した（強制終了が起きていない）:\n%s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("子プロセスを起動できない: %v\n%s", err, out)
	}
	// 強制終了はシグナルによる異常終了（終了コードは -1）。テストの失敗（exit 1）と区別する。
	if code := exitErr.ExitCode(); code != -1 {
		t.Fatalf("子プロセスが強制終了以外で終わった（終了コード %d）:\n%s", code, out)
	}
}

// ---- 補助 -------------------------------------------------------------------

// assertRemoteHasFile は同期先を取得し直して rel が載っていることを確かめる。
func assertRemoteHasFile(t *testing.T, c *Client, remote Remote, rel string) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "verify")
	verifier, err := New(Options{Author: c.Author(), ConfigDir: filepath.Join(t.TempDir(), "verifySync")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Clone(context.Background(), CloneOptions{Remote: remote, Dest: dest}); err != nil {
		t.Fatalf("同期先の確認用の取得に失敗: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
		t.Errorf("同期先に %s が載っていない（更新消失）: %v", rel, err)
	}
}

// assertBothWorkCopiesHave は双方が取り込みを済ませたあと、両者の作業コピーに双方の変更が残ることを確かめる。
func assertBothWorkCopiesHave(t *testing.T, a, b *Client, rootA, rootB string, remote Remote, fileA, fileB string) {
	t.Helper()
	mustIncorporate(t, a, rootA, remote)
	mustIncorporate(t, b, rootB, remote)
	for label, root := range map[string]string{"A": rootA, "B": rootB} {
		for _, rel := range []string{fileA, fileB} {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s の作業コピーに %s が無い（更新消失）: %v", label, rel, err)
			}
		}
	}
}

// conflictMarkers は git のコンフリクトマーカー（利用者へ出してはならない）。
var conflictMarkers = []string{"<<<<<<< ", "=======\n", ">>>>>>> "}

// assertNoConflictMarkers は作業コピー（`.git/` を除く）にコンフリクトマーカーが無いことを確かめる。
func assertNoConflictMarkers(t *testing.T, root string) {
	t.Helper()
	checked := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		body := string(b)
		for _, marker := range conflictMarkers {
			if strings.Contains(body, marker) {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("作業コピーにコンフリクトマーカー %q が残っている: %s", strings.TrimSpace(marker), rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("作業コピーの走査に失敗: %v", err)
	}
	if checked == 0 {
		t.Fatalf("走査対象のファイルが 1 件もない（検査が成立していない）: %s", root)
	}
}

// treeSnapshot は作業コピー（`.git/` を除く）のパス → 内容ハッシュを返す。
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(rel)] = hashOf(b)
		return nil
	})
	if err != nil {
		t.Fatalf("作業コピーの走査に失敗: %v", err)
	}
	if len(snapshot) == 0 {
		t.Fatalf("作業コピーが空（検査が成立していない）: %s", root)
	}
	return snapshot
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sameTree(want, got map[string]string) bool {
	if len(want) != len(got) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func assertSameTree(t *testing.T, msg string, want, got map[string]string) {
	t.Helper()
	if !sameTree(want, got) {
		t.Errorf("%s:\n%s", msg, treeDiff(want, nil, got))
	}
}

// treeDiff は期待（取り込み前・取り込み後）と実際の食い違いを読める形にする。
func treeDiff(before, merged, got map[string]string) string {
	var b strings.Builder
	keys := map[string]bool{}
	for _, m := range []map[string]string{before, merged, got} {
		for k := range m {
			keys[k] = true
		}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if before[k] == got[k] && (merged == nil || merged[k] == got[k]) {
			continue
		}
		fmt.Fprintf(&b, "  %s: 取り込み前=%s 取り込み後=%s 実際=%s\n",
			k, short(before[k]), short(merged[k]), short(got[k]))
	}
	return b.String()
}

func short(h string) string {
	if h == "" {
		return "（無し）"
	}
	return h[:8]
}
