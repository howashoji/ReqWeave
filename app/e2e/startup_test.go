//go:build e2e

// e2e（起動系）: ビルド済みの配布物を実際に起動し、画面（DOM）到達までを確認する。
// 起動して画面まで到達することの最低限の確認として、常に維持する。
// 実行: make -C app test-e2e（build に依存）
//
// 判定は binding.ReadyMarker の標準出力（OnDomReady フックで出力）による。
// GUI セッションのない環境（ヘッドレス CI 等）では起動できないため、本段は
// 実機のデスクトップセッション上で実行することを前提とする。

package e2e

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/binding"
)

// startupTimeout は起動〜DOM 到達の待ち時間。画面表示の応答時間の基準（3 秒）に対し十分な余裕を取る
// （本テストの目的は性能測定ではなく「起動して画面まで到達する」ことの確認）。
const startupTimeout = 60 * time.Second

// binaryPath はビルド成果物（make build の出力）の実行ファイルを返す。
func binaryPath(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return filepath.FromSlash("../build/bin/ReqWeave.app/Contents/MacOS/ReqWeave")
	case "windows":
		return filepath.FromSlash("../build/bin/ReqWeave.exe")
	default:
		return filepath.FromSlash("../build/bin/ReqWeave")
	}
}

func TestAppStartsAndReachesUI(t *testing.T) {
	bin := binaryPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin)
	// 終了時にプロセスを確実に落とす（ウィンドウが残らないように）
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("標準出力の取得に失敗: %v", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("配布物を起動できません（%s）: %v — 先に make build を実行すること", bin, err)
	}

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
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("起動マーカー %q を出力せずにプロセスが終了しました。出力:\n%s", binding.ReadyMarker, captured.String())
		}
		if !strings.Contains(line, "version=") {
			t.Errorf("起動マーカーに版番号がありません: %q", line)
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("%v 以内に起動マーカー %q が出力されませんでした。出力:\n%s", startupTimeout, binding.ReadyMarker, captured.String())
	}

	// 起動を確認できたら終了させる（正常に終了できることもあわせて確認する）
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("プロセスを終了できません: %v", err)
	}
	_ = cmd.Wait()
}
