//go:build integration

// 結合テスト（**実プロセスの強制終了**による復元・原子性の検証）。
//
// クラッシュ後の復元と保存処理の原子性の測定可能な基準は
// 「プロセス強制終了を繰り返す試験」である。同一プロセスの中で Store を捨てて開き直す
// 既存のテスト（TestUtterancesSurviveAbruptTerminationWithoutSaveOperation）は
// **OS のページキャッシュが応えてしまう**ため、fsync を外しても振る舞いが変わらない
// （fsync を外す故障注入で実測）。本テストは別プロセスを `SIGKILL` で落とし、
// **振る舞いとして**確かめる。
//
// 落とす側の相手は `tools/crashwriter`（検証専用。配布物には入らない）。
//
// **Windows 分は本テストの対象外**（`syscall.SIGKILL` を使うため）。Windows では実機で確認する。

package projectstore

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// crashTrials は 1 条件あたりの試行回数。
//
// クラッシュ後の復元は「macOS / Windows 各 5 回」、保存処理の原子性は「10 回以上」を基準にする。
const (
	crashTrialsRestore = 5
	crashTrialsAtomic  = 10
)

// buildCrashWriter は相手のプログラムを 1 度だけビルドして実行ファイルの場所を返す。
func buildCrashWriter(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "crashwriter")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/howashoji/ReqWeave/app/tools/crashwriter")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crashwriter をビルドできない: %v\n%s", err, out)
	}
	return bin
}

// killProgress は強制終了した時点までに相手が報告した進捗。
type killProgress struct {
	lastCommitted int    // 最後に「返った」保存の番号（0 = 1 件も完了していない）
	lastCommitID  string // その保存が付けた ID（append のときの utt-nnnnn）
	lastWriting   int    // 最後に「始めた」保存の番号
}

// diedWhileWriting は**保存処理の最中に落ちた**かを返す。
//
// 始めた保存の番号が、返った保存の番号より先に進んでいれば、その保存の途中で死んでいる。
// これが偽なら「保存と保存の隙間で死んだ」ことになり、原子性の検証としては空振りである。
func (p killProgress) diedWhileWriting() bool { return p.lastWriting > p.lastCommitted }

// runUntilKilled は相手を起動し、committed を afterCommits 件見てから追加で
// grace だけ待って SIGKILL する。読み取れた進捗を返す。
func runUntilKilled(t *testing.T, bin string, args []string, afterCommits int, grace time.Duration) killProgress {
	t.Helper()
	cmd := exec.Command(bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("標準出力を取れない: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("crashwriter を起動できない: %v", err)
	}

	done := make(chan killProgress, 1)
	go func() {
		var p killProgress
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		killed := false
		for sc.Scan() {
			var n int
			var id string
			switch {
			case strings.HasPrefix(sc.Text(), "committed "):
				if _, err := fmt.Sscanf(sc.Text(), "committed %d %s", &n, &id); err == nil {
					p.lastCommitted, p.lastCommitID = n, id
				}
			case strings.HasPrefix(sc.Text(), "writing "):
				if _, err := fmt.Sscanf(sc.Text(), "writing %d", &n); err == nil {
					p.lastWriting = n
				}
			}
			if !killed && p.lastCommitted >= afterCommits {
				killed = true
				// 次の保存が始まってから落とす（保存の最中を狙う）。
				time.Sleep(grace)
				_ = cmd.Process.Kill()
			}
		}
		done <- p
	}()

	p := <-done
	err = cmd.Wait()
	if err == nil {
		t.Fatalf("相手が自分から終了している（強制終了の検証になっていない）。stderr=%s", stderr.String())
	}
	if p.lastCommitted == 0 {
		t.Fatalf("1 件も保存が完了しないまま終わった（前提が揃っていない）。stderr=%s", stderr.String())
	}
	return p
}

// クラッシュ後の復元の測定可能な基準（macOS 5 回）: 対話中に強制終了し、再起動後に
// (1) 直前に送受信が完了した発話までが表示され、(2) プロジェクトデータが破損なく開けること。
func TestKilledProcessKeepsCommittedUtterances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Fatal("本テストは SIGKILL を使うため Windows では実行しない（Windows 分は実機で確認する）")
	}
	bin := buildCrashWriter(t)

	for trial := 1; trial <= crashTrialsRestore; trial++ {
		t.Run(fmt.Sprintf("試行%d", trial), func(t *testing.T) {
			s := createTestProject(t)
			sess, err := s.CreateSession(SessionOwner, "requirements")
			if err != nil {
				t.Fatalf("セッションを作成できない: %v", err)
			}
			root := s.Root()
			if err := s.Close(); err != nil {
				t.Fatalf("いったん閉じられない: %v", err)
			}

			p := runUntilKilled(t, bin,
				[]string{"-mode", "append", "-root", root, "-session", sess.ID},
				2+trial, time.Duration(trial)*time.Millisecond)

			// 再起動（別の Store で開き直す）。
			reopened, err := Open(root, testAuthor())
			if err != nil {
				t.Fatalf("強制終了の後にプロジェクトを開けない（破損の疑い）: %v", err)
			}
			defer reopened.Close()

			_, got, err := reopened.LoadSession(sess.ID)
			if err != nil {
				t.Fatalf("対話セッションを読み出せない（破損の疑い）: %v", err)
			}
			// (1) 返ってきた保存の分は必ず残っている。
			if len(got) < p.lastCommitted {
				t.Fatalf("完了した発話が失われている: 保存できた %d 件 / 復元 %d 件", p.lastCommitted, len(got))
			}
			var found bool
			for _, u := range got {
				if u.ID == p.lastCommitID {
					found = true
				}
				if strings.TrimSpace(u.Body) == "" {
					t.Errorf("本文が空の発話が復元された（半端な追記の疑い）: %s", u.ID)
				}
			}
			if !found {
				t.Fatalf("最後に保存が返った発話 %s が復元されていない", p.lastCommitID)
			}
			// (2) 一覧も含めて開ける。
			if _, err := reopened.ListSessions(); err != nil {
				t.Fatalf("対話セッション一覧を読めない（破損の疑い）: %v", err)
			}
			t.Logf("保存できた %d 件（最後 %s）/ 復元 %d 件 / 保存中に落ちた=%v",
				p.lastCommitted, p.lastCommitID, len(got), p.diedWhileWriting())
		})
	}
}

// 保存処理の原子性の測定可能な基準（10 回以上）: 保存処理中のプロセス強制終了を繰り返し、
// 再起動後に「保存前の版」または「保存後の版」のいずれかとして必ず開け、読込不能が 0 件であること。
func TestKilledProcessNeverLeavesPartialFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Fatal("本テストは SIGKILL を使うため Windows では実行しない（Windows 分は実機で確認する）")
	}
	bin := buildCrashWriter(t)

	const target = "project.json.crashtest"
	diedWhileWriting := 0
	for trial := 1; trial <= crashTrialsAtomic; trial++ {
		t.Run(fmt.Sprintf("試行%d", trial), func(t *testing.T) {
			s := createTestProject(t)
			root := s.Root()
			if err := s.Close(); err != nil {
				t.Fatalf("いったん閉じられない: %v", err)
			}
			path := filepath.Join(root, target)

			p := runUntilKilled(t, bin,
				[]string{"-mode", "atomic", "-root", root, "-target", target, "-size", "262144"},
				1+trial%3, time.Duration(trial%7)*time.Millisecond)
			if p.diedWhileWriting() {
				diedWhileWriting++
			}

			// 読み込めること（読込不能 0 件）。
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("強制終了の後に保存先を読めない: %v", err)
			}
			// 「どれかの版として完結している」こと。半端な内容ならどの版にも一致しない。
			var version int
			if _, err := fmt.Sscanf(string(raw), "version=%d\n", &version); err != nil {
				t.Fatalf("保存先の内容がどの版としても読めない（半端な置換の疑い。先頭 64 バイト: %q）",
					raw[:min(64, len(raw))])
			}
			want := []byte(fmt.Sprintf("version=%d\n", version) +
				strings.Repeat(fmt.Sprintf("%d", version%10), 262144))
			if string(raw) != string(want) {
				t.Fatalf("版 %d の内容が完結していない（長さ 実際 %d / 期待 %d）", version, len(raw), len(want))
			}
			// **始めてもいない版が見えることはない**。
			//
			// 「返った版まで」ではなく「始めた版まで」で見るのは、rename が完了してから
			// 親が `committed` の行を読むまでに時間差があるため（次の版が見えていても正しい）。
			if version > p.lastWriting {
				t.Fatalf("始めてもいない版 %d が見えている（始めたのは %d まで）", version, p.lastWriting)
			}

			// **一時ファイルの残骸は掃除される**。
			//
			// SIGKILL では WriteFileAtomic の後片づけ（defer）が走らないため、残骸そのものは残る。
			// 放置すると共同プロジェクトでは次の反映で同期先へ載るため、
			// **次にプロジェクトを開いた時点で消える**ことを確かめる。
			leftovers := tempLeftovers(t, root)
			if len(leftovers) == 0 {
				t.Log("この試行では残骸が出なかった（rename 済みの時点で落ちた）")
			}
			// 残骸を「前回のセッションが残したもの」の古さにする（掃除の対象になる条件）。
			for _, name := range leftovers {
				old := time.Now().Add(-time.Hour)
				if err := os.Chtimes(filepath.Join(root, name), old, old); err != nil {
					t.Fatalf("残骸の時刻を変えられない: %v", err)
				}
			}
			reopened, err := Open(root, testAuthor())
			if err != nil {
				t.Fatalf("強制終了の後にプロジェクトを開けない: %v", err)
			}
			defer reopened.Close()
			if rest := tempLeftovers(t, root); len(rest) > 0 {
				t.Errorf("開き直しても一時ファイルの残骸が消えていない: %v", rest)
			}
			// 掃除が本体を巻き添えにしていないこと。
			if _, err := os.ReadFile(path); err != nil {
				t.Errorf("掃除の後に保存先が読めない: %v", err)
			}
			t.Logf("見えた版 %d / 始めた版 %d / 保存中に落ちた=%v / 残骸 %d 件を掃除",
				version, p.lastWriting, p.diedWhileWriting(), len(leftovers))
		})
	}

	// **空振りの検出**: どの試行も「保存と保存の隙間」で死んでいたなら、
	// 原子性を試していないのと同じ。1 回も保存中に落ちていなければ試験として成立しない。
	if diedWhileWriting == 0 {
		t.Fatalf("%d 回すべてが保存処理の外で終わっている（原子性を試せていない）", crashTrialsAtomic)
	}
	t.Logf("保存処理の最中に落ちた試行: %d / %d", diedWhileWriting, crashTrialsAtomic)
}

// tempLeftovers はプロジェクトフォルダ直下に残っている原子的置換の一時ファイル名を返す。
func tempLeftovers(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("プロジェクトフォルダを読めない: %v", err)
	}
	var out []string
	for _, e := range entries {
		if isAtomicTempName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
