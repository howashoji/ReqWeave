//go:build integration

// 結合テスト（前回の異常終了の検知）。
//
// バインディングの起動フック（Startup）を通して、
//   - 前回の起動に対応する終了の記録が無いとき「前回は正常に終了していません」を記録すること
//   - 正常に終了していたときは記録しないこと
// を確認する。Go の fatal error・cgo 側のシグナルは recover も終了フックも通らないため、
// クラッシュはこの欠落でしか気づけない（実際にそうしたクラッシュが起きている）。

package binding

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/applog"
)

// exitedPID は実際に起動して終了させたプロセスの識別子を返す。
//
// 前回の異常終了は「終了の記録が無く、かつ**そのプロセスが既に居ない**起動」で判定する
// （多重起動・自動更新の再起動を誤検知しないため）。テスト自身の pid は生きているため、前回の記録として使えない。
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("go", "version")
	if err := cmd.Start(); err != nil {
		t.Fatalf("確認用のプロセスを起動できない: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("確認用のプロセスが失敗した: %v", err)
	}
	return pid
}

// writeLifecycleRecord は「別のプロセスが書いた」生存期間の記録を動作ログへ足す。
//
// applog は自プロセスの pid を自動で付けるため、他プロセスの記録は作れない。
// ここでは**保存されている形（JSON Lines）そのもの**を書いて、
// 読み取り側（scanPreviousRun）が実ファイルから正しく判定することを確かめる。
func writeLifecycleRecord(t *testing.T, dir string, pid int, event string) {
	t.Helper()
	line := fmt.Sprintf(
		`{"at":"2026-09-04T04:19:28Z","level":"info","event":%q,"message":"前回の記録","fields":{%q:%d}}`,
		event, applog.FieldPID, pid) + "\n"
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("動作ログへ書けない: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("動作ログへ書けない: %v", err)
	}
}

// restartWithAppLog は前回の記録を残したままロガーを開き直す（アプリの再起動に相当）。
func restartWithAppLog(t *testing.T, a *API) {
	t.Helper()
	if err := a.log.Close(); err != nil {
		t.Fatalf("動作ログを閉じられない: %v", err)
	}
	l, err := applog.New(a.paths)
	if err != nil {
		t.Fatalf("動作ログを開き直せない: %v", err)
	}
	a.log = l
	t.Cleanup(func() { _ = l.Close() })
}

func TestStartupDetectsPreviousCrash(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	dir := attachAppLog(t, a)

	// 前回: 起動したが終了の記録が無く、そのプロセスは既に居ない（＝異常終了）。
	writeLifecycleRecord(t, dir, exitedPID(t), applog.EventAppStart)
	restartWithAppLog(t, a)

	a.Startup(context.Background())

	events := appLogEvents(t, dir)
	if !containsEvent(events, applog.EventAppStart) {
		t.Errorf("起動が記録されていない: %v", events)
	}
	if !containsEvent(events, applog.EventPreviousRunIncomplete) {
		t.Errorf("前回の異常終了が記録されていない: %v", events)
	}
}

func TestStartupSilentAfterNormalShutdown(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	dir := attachAppLog(t, a)

	// 前回: 起動して正常に終了した。
	prev := exitedPID(t)
	writeLifecycleRecord(t, dir, prev, applog.EventAppStart)
	writeLifecycleRecord(t, dir, prev, applog.EventAppStop)
	restartWithAppLog(t, a)

	a.Startup(context.Background())

	events := appLogEvents(t, dir)
	if containsEvent(events, applog.EventPreviousRunIncomplete) {
		t.Errorf("正常終了だったのに異常終了として記録した: %v", events)
	}
}

func containsEvent(events []string, want string) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

// AI ストリーミング用ゴルーチンのパニックの記録先が配線されていること。
// 抽象化層は動作ログのパッケージへ依存できない（import が循環する）ため、記録先は本層が渡す。
func TestStreamPanicRecorderIsWired(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	dir := attachAppLog(t, a)

	if a.adapterOptions().OnPanic == nil {
		t.Error("アダプタ生成オプションへ記録先が渡っていない")
	}

	a.onStreamPanic("応答の解釈に失敗 sk-ant-api03-STREAMPANICKEY0123456789")

	body := appLogBody(t, dir)
	if !strings.Contains(body, "ai.stream_panic") {
		t.Errorf("パニックが記録されていない: %q", body)
	}
	if !strings.Contains(body, `"stack"`) {
		t.Errorf("スタックが記録されていない: %q", body)
	}
	if strings.Contains(body, "sk-ant-api03-STREAMPANICKEY0123456789") {
		t.Error("記録がマスキングを通っていない")
	}
}

// 診断情報の書き出しが画面から使える形で組み立てられること。
func TestDiagnosticsPreviewFromBinding(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	attachAppLog(t, a)
	a.Startup(context.Background())

	d, err := a.DiagnosticsPreview()
	if err != nil {
		t.Fatalf("診断情報を組み立てられない: %v", err)
	}
	if !strings.Contains(d.Environment, Version) {
		t.Errorf("アプリの版番号が入っていない: %q", d.Environment)
	}
	if !strings.Contains(d.LogText, applog.EventAppStart) {
		t.Errorf("動作ログの本文が入っていない: %q", d.LogText)
	}
	// 含まれるのは実行環境と動作ログだけ（業務データ・コアダンプを含めない）。
	for _, item := range d.Items {
		if item.Name != "environment.txt" && !strings.HasPrefix(item.Name, "app.") {
			t.Errorf("想定外のものが含まれている: %s", item.Name)
		}
	}
}

// 保存先を選べない状態（起動直後など）でも、利用者向けの文言（原因＋次の行動）で断ること。
func TestExportDiagnosticsWithoutContext(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	attachAppLog(t, a)
	a.ctx = nil

	saved, err := a.ExportDiagnostics()
	if err == nil {
		t.Fatal("保存先を選べないのに成功した")
	}
	if saved != "" {
		t.Errorf("保存していないのに保存先を返した: %q", saved)
	}
	if !strings.Contains(err.Error(), "アプリを再起動") {
		t.Errorf("次に取る行動が示されていない: %v", err)
	}
}

// 前回の異常終了の有無を画面へ返せること（起動時の案内に使う）。
func TestPreviousRunIncompleteFromBinding(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	dir := attachAppLog(t, a)

	if a.PreviousRunIncomplete() {
		t.Error("記録が無いのに異常終了と判定した")
	}

	writeLifecycleRecord(t, dir, exitedPID(t), applog.EventAppStart)
	restartWithAppLog(t, a)

	if !a.PreviousRunIncomplete() {
		t.Error("終了の記録が無いのに正常終了と判定した")
	}
}

// 多重起動と自動更新の自己再起動を異常終了と誤検知しないこと。
//
// 本システムは単一インスタンス化をしない（単一インスタンスの仕組みが自動更新の自己再起動を妨げるため）。2 つ目のウィンドウを開いたときも、
// 自動更新が**新版を起動してから旧版を終了させる**ときも、
// 「終了の記録が無い起動」が動作ログに正常に残る。
func TestStartupDoesNotWarnWhileAnotherInstanceIsRunning(t *testing.T) {
	a, _ := newTestAPI(t, &stubAdapter{})
	dir := attachAppLog(t, a)

	// まだ動いている別インスタンス（ここではテスト自身のプロセスで代用する）。
	writeLifecycleRecord(t, dir, os.Getpid(), applog.EventAppStart)
	restartWithAppLog(t, a)

	a.Startup(context.Background())

	events := appLogEvents(t, dir)
	if containsEvent(events, applog.EventPreviousRunIncomplete) {
		t.Errorf("まだ動いているインスタンスがあるのに異常終了として記録した: %v", events)
	}
}
