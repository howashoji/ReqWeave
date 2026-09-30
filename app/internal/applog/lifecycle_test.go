package applog

// 前回の異常終了の検知と、既定のロガーによるパニック記録の単体テスト。
//
// 受け入れ条件の対応:
//   - 前回の異常終了（app.start に対する app.stop の欠落）を起動時に判定できる
//   - 引数でロガーを受け取れないゴルーチンでもパニックを記録できる（記録後に再送出する）

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reopen は同じディレクトリでロガーを開き直す（アプリの再起動に相当）。
// pid は開いたプロセスのもの（= 生きている）になる。
func reopen(t *testing.T, dir string) *Logger {
	t.Helper()
	l, err := open(dir)
	if err != nil {
		t.Fatalf("ロガーを開けない: %v", err)
	}
	l.now = func() time.Time { return fixedTime }
	t.Cleanup(func() { l.Close() })
	return l
}

// reopenAs は「別のプロセスとして」開き直す（多重起動・再起動の再現）。
// 実際の判定は pid の対応で行うため、テストは pid を明示して組み立てる。
func reopenAs(t *testing.T, dir string, pid int) *Logger {
	t.Helper()
	l := reopen(t, dir)
	l.pid = pid
	return l
}

// withAliveProcesses は「今動いている pid」の集合を差し替える。
func withAliveProcesses(t *testing.T, alive ...int) {
	t.Helper()
	set := map[int]bool{}
	for _, p := range alive {
		set[p] = true
	}
	prev := aliveCheck
	aliveCheck = func(pid int) bool { return set[pid] }
	t.Cleanup(func() { aliveCheck = prev })
}

// 前回の記録に現れる pid（既に終了しているものとして扱う）。
const deadPID = 424242

func TestPreviousRunIncomplete(t *testing.T) {
	// lifecycleEvent は「どのプロセスが」「何を」記録したかの 1 件。
	type lifecycleEvent struct {
		pid   int
		event string
	}
	tests := []struct {
		name string
		// events は前回までに記録された生存期間イベントを古い順に並べたもの。
		events []lifecycleEvent
		// aliveAfter は判定時にまだ動いている pid。
		aliveAfter []int
		want       bool
	}{
		{name: "記録が無い（初回起動）", events: nil, want: false},
		{name: "起動と終了が揃っている", events: []lifecycleEvent{
			{deadPID, EventAppStart}, {deadPID, EventAppStop}}, want: false},
		{name: "起動のみ・そのプロセスは既に居ない（異常終了）", events: []lifecycleEvent{
			{deadPID, EventAppStart}}, want: true},
		{name: "正常終了のあとに異常終了", events: []lifecycleEvent{
			{deadPID, EventAppStart}, {deadPID, EventAppStop}, {deadPID + 1, EventAppStart}}, want: true},
		{name: "異常終了のあとに正常終了", events: []lifecycleEvent{
			{deadPID, EventAppStart}, {deadPID, EventAppStop},
			{deadPID + 1, EventAppStart}, {deadPID + 1, EventAppStop}}, want: false},

		// 単一インスタンス化をしないため、次の 2 つは正常な状態である。
		{name: "多重起動: 先に開いた別インスタンスがまだ動いている", events: []lifecycleEvent{
			{deadPID, EventAppStart}}, aliveAfter: []int{deadPID}, want: false},
		{name: "自動更新: 旧版がまだ終了していない（新版が先に起動する）", events: []lifecycleEvent{
			{deadPID, EventAppStart}, {deadPID + 1, EventAppStart}, {deadPID + 1, EventAppStop}},
			aliveAfter: []int{deadPID}, want: false},
		{name: "多重起動中に片方が落ちた（生きている方があっても検知する）", events: []lifecycleEvent{
			{deadPID, EventAppStart}, {deadPID + 1, EventAppStart}},
			aliveAfter: []int{deadPID}, want: true},

		// 本修正より前の版が書いた記録には pid が無い。対応づけられないため数えない。
		{name: "pid を持たない古い記録だけがある", events: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, ev := range tt.events {
				w := reopenAs(t, dir, ev.pid)
				w.Info(ev.event, "前回の記録")
				if err := w.Close(); err != nil {
					t.Fatalf("閉じられない: %v", err)
				}
			}
			if tt.name == "pid を持たない古い記録だけがある" {
				writeLegacyLifecycleLines(t, dir)
			}

			withAliveProcesses(t, tt.aliveAfter...)

			// 開き直した時点で判定が確定していること。
			second := reopen(t, dir)
			if got := second.PreviousRunIncomplete(); got != tt.want {
				t.Errorf("PreviousRunIncomplete() = %v（期待 %v）", got, tt.want)
			}
			// 自分が書いた app.start で判定が変わらないこと。
			second.Info(EventAppStart, "起動しました")
			if got := second.PreviousRunIncomplete(); got != tt.want {
				t.Errorf("自分の起動の記録後に %v へ変わった（期待 %v のまま）", got, tt.want)
			}
		})
	}
}

// writeLegacyLifecycleLines は pid を持たない旧版の記録を直接書き足す。
// 旧版の利用者のログには「終了の記録が無い起動」が大量に残っているため、
// **更新後の初回起動でそれを異常終了と報告しないこと**を確かめる。
func writeLegacyLifecycleLines(t *testing.T, dir string) {
	t.Helper()
	legacy := `{"at":"2026-09-04T04:19:28Z","level":"info","event":"app.start","message":"起動しました","fields":{"version":"0.1.0"}}` + "\n"
	p := filepath.Join(dir, baseName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatalf("旧版の記録を書けない: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(legacy); err != nil {
		t.Fatalf("旧版の記録を書けない: %v", err)
	}
}

// 生存期間のイベントには pid が必ず付くこと（呼び出し側の指定に依らない）。
func TestLifecycleEventsCarryPID(t *testing.T) {
	dir := t.TempDir()
	l := reopenAs(t, dir, 4242)
	l.Info(EventAppStart, "起動しました", F("version", "0.1.0"))
	l.Info(EventAppStop, "終了します")
	l.Info("sync.failed", "生存期間とは無関係の記録")
	l.Close()

	b, err := os.ReadFile(filepath.Join(dir, baseName))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		pid, ok := pidOf(line)
		isLifecycle := strings.Contains(line, EventAppStart) || strings.Contains(line, EventAppStop)
		switch {
		case isLifecycle && !ok:
			t.Errorf("生存期間の記録に pid が無い: %s", line)
		case isLifecycle && pid != 4242:
			t.Errorf("pid が %d（期待 4242）: %s", pid, line)
		case !isLifecycle && ok:
			t.Errorf("生存期間以外の記録に pid が付いた: %s", line)
		}
	}
}

// processAlive が「実際に終了したプロセス」を居ないと判定すること（差し替えなしの実測）。
func TestProcessAliveOnExitedProcess(t *testing.T) {
	if processAlive(os.Getpid()) != true {
		t.Error("自分自身を居ないと判定した")
	}
	cmd := exec.Command("go", "version")
	if err := cmd.Start(); err != nil {
		t.Fatalf("確認用のプロセスを起動できない: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("確認用のプロセスが失敗した: %v", err)
	}
	if processAlive(pid) {
		t.Errorf("終了したプロセス（pid %d）を生存と判定した", pid)
	}
}

// 記録が現行ファイルに残っていない場合（世代送り直後など）は「分からない」を
// 「異常終了した」と言わない。
func TestPreviousRunIncompleteWithoutLifecycleRecords(t *testing.T) {
	dir := t.TempDir()
	first := reopen(t, dir)
	first.Info("sync.failed", "生存期間とは無関係の記録")
	first.Close()

	if reopen(t, dir).PreviousRunIncomplete() {
		t.Error("生存期間の記録が無いのに異常終了と判定した")
	}
}

// nil ロガーでも判定できること（動作ログを開けない端末で機能を止めない）。
func TestPreviousRunIncompleteNilLogger(t *testing.T) {
	var l *Logger
	if l.PreviousRunIncomplete() {
		t.Error("nil ロガーが異常終了と判定した")
	}
}

// 既定のロガーでパニックを記録し、握り潰さずに再送出すること。
func TestDefaultRecoverPanic(t *testing.T) {
	l, dir := newTestLogger(t)
	SetDefault(l)
	t.Cleanup(func() { SetDefault(nil) })

	if Default() != l {
		t.Fatal("既定のロガーが据わっていない")
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("パニックが再送出されなかった")
			}
		}()
		defer RecoverPanic(EventAppPanic)
		panic("ゴルーチンの想定外の状態です sk-ant-api03-GOROUTINEKEY0123456789")
	}()

	data, err := os.ReadFile(filepath.Join(dir, baseName))
	if err != nil {
		t.Fatalf("動作ログを読めない: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, EventAppPanic) {
		t.Errorf("パニックが記録されていない: %q", body)
	}
	if !strings.Contains(body, `"stack"`) {
		t.Errorf("スタックが記録されていない: %q", body)
	}
	if strings.Contains(body, "sk-ant-api03-GOROUTINEKEY0123456789") {
		t.Error("パニックのメッセージがマスキングを通っていない")
	}
}

// 既定のロガーが未設定でも、記録せずに再送出すること（起動直後・テスト時に落ちない）。
func TestDefaultRecoverPanicWithoutDefault(t *testing.T) {
	SetDefault(nil)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("パニックが再送出されなかった")
			}
		}()
		defer RecoverPanic(EventAppPanic)
		panic("既定のロガーが無い状態でのパニック")
	}()
}

// パニックしていないときは何もしないこと（通常経路で誤って記録しない）。
func TestDefaultRecoverPanicWithoutPanic(t *testing.T) {
	l, dir := newTestLogger(t)
	SetDefault(l)
	t.Cleanup(func() { SetDefault(nil) })

	func() { defer RecoverPanic(EventAppPanic) }()

	if _, err := os.Stat(filepath.Join(dir, baseName)); err != nil {
		t.Fatalf("動作ログが無い: %v", err)
	}
	if lines := readLines(t, filepath.Join(dir, baseName)); len(lines) != 0 {
		t.Errorf("パニックしていないのに %d 行記録した", len(lines))
	}
}
