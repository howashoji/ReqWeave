//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/binding"
)

// launchOptions は配布物を 1 回起動するときの条件。
type launchOptions struct {
	// bin は起動する実行ファイル。
	bin string
	// home はアプリのデータ・設定の置き場所（$HOME）。**必須**。
	// 空のまま起動すると利用者の実データ（~/Library/Application Support/…）を読み書きするため、
	// 指定が無ければ失敗させる（呼び出し元の環境をそのまま使う経路を残さない）。
	home string
	// timeout は起動マーカー到達までの待ち時間。
	timeout time.Duration
}

// launchAndWaitReady は配布物を起動し、画面（DOM）到達までの所要時間を返す。
// 判定は binding.ReadyMarker の標準出力（OnDomReady フック）。到達しなければ失敗させる。
func launchAndWaitReady(t *testing.T, opt launchOptions) (time.Duration, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), opt.timeout)
	defer cancel()

	if opt.home == "" {
		t.Fatal("launchOptions.home が空です。隔離した $HOME を必ず渡すこと（利用者の実データへ触れないため）")
	}
	cmd := exec.CommandContext(ctx, opt.bin)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "HOME="+opt.home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("標準出力の取得に失敗: %v", err)
	}
	cmd.Stderr = cmd.Stdout

	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("配布物を起動できません（%s）: %v", opt.bin, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	found := make(chan string, 1)
	var captured strings.Builder
	go func() {
		sc := bufio.NewScanner(io.TeeReader(stdout, &captured))
		for sc.Scan() {
			if strings.Contains(sc.Text(), binding.ReadyMarker) {
				found <- sc.Text()
				return
			}
		}
		close(found)
	}()

	select {
	case line, ok := <-found:
		if !ok {
			t.Fatalf("起動マーカー %q を出力せずにプロセスが終了しました。出力:\n%s", binding.ReadyMarker, captured.String())
		}
		return time.Since(start), line
	case <-ctx.Done():
		t.Fatalf("%v 以内に起動マーカー %q が出力されませんでした。出力:\n%s", opt.timeout, binding.ReadyMarker, captured.String())
	}
	return 0, ""
}

// distArchive は make dist が出力した配布物のパスを返す（macOS は dmg）。
func distArchive(t *testing.T) string {
	t.Helper()
	// **ゲート用（未署名）だけを対象にする**。リリース物と同名にしないため、
	// ファイル名に -unsigned が入る。ここで広く拾うと、署名済みの配布物を e2e が触りうる。
	matches, err := filepath.Glob(filepath.FromSlash("../build/dist/ReqWeave-*-macos-unsigned.dmg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("配布物が 1 個に定まりません（%d 件）。先に make -C app dist を実行すること", len(matches))
	}
	return matches[0]
}

// extractDist は配布物を dest へ取り出す（利用者が dmg を開いて中身を取り出す操作に相当）。
//
// 取り出しに `ditto` を使うのは、`.app` の実行権限・拡張属性・**コード署名**を保つため
// （素朴なコピーでは壊れ、Gatekeeper に拒否される）。
func extractDist(t *testing.T, archive, dest string) {
	t.Helper()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("取り出し先を用意できません: %v", err)
	}
	mountPoint := t.TempDir()
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noverify",
		"-mountpoint", mountPoint, archive).CombinedOutput(); err != nil {
		t.Fatalf("配布物をマウントできません: %v\n%s", err, out)
	}
	// **取り出しが終わったらその場で切り離す**（t.Cleanup にしない）。
	// テストの終わりまでマウントを保持すると、同じ dmg を次のテストが開こうとしたときに
	// `hdiutil: attach failed - リソースが使用中です` で落ちる（実測）。
	//
	// **`-quiet` を付けない**。付けると hdiutil は失敗の理由を出さず、
	// 「切り離せません」だけが残って原因を追えない。
	defer func() {
		if out, err := exec.Command("hdiutil", "detach", mountPoint).CombinedOutput(); err != nil {
			t.Errorf("配布物を切り離せません（マウントが残る）: %v\n%s", err, out)
		}
		// 失敗しても残骸を次の実行へ持ち越さない（理由は上で報告済み）。
		sweepMountLeak(t, archive)
	}()
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		t.Fatalf("配布物の中身を読めません: %v", err)
	}
	copied := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // ボリュームの隠しメタデータは配布物の中身ではない
		}
		if out, err := exec.Command("ditto", filepath.Join(mountPoint, name),
			filepath.Join(dest, name)).CombinedOutput(); err != nil {
			t.Fatalf("配布物を取り出せません（%s）: %v\n%s", name, err, out)
		}
		copied++
	}
	if copied == 0 {
		t.Fatal("配布物から 1 件も取り出せませんでした（空の dmg）")
	}
}

// userAreaDir は「利用者が自分で置ける場所」（ホームフォルダ配下）に作業用の場所を作る。
// 管理者権限を要する場所（/Applications 直下）は使わない。
func userAreaDir(t *testing.T, name string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("ホームフォルダを特定できません: %v", err)
	}
	dir, err := os.MkdirTemp(home, name+"-")
	if err != nil {
		t.Fatalf("利用者領域に作業場所を作れません: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
