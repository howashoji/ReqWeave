package applog

// 動作ログの単体テスト。
//
// 受け入れ条件の対応:
//   - 保存先・形式（JSON Lines）
//   - 10MB 相当の上限でローテーションし、保持数（MaxFiles）を超えるファイルを作らない
//   - 出力層で masking.Mask を通す（シークレットキー・同期先の認証情報が書き出されない）

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/masking"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// fixedTime はテストの時刻を固定する（TZ 依存にしない）。
var fixedTime = time.Date(2026, 9, 3, 0, 11, 22, 0, time.UTC)

func newTestLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := open(dir)
	if err != nil {
		t.Fatalf("ロガーを開けない: %v", err)
	}
	l.now = func() time.Time { return fixedTime }
	t.Cleanup(func() { l.Close() })
	return l, dir
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("動作ログを読めない: %v", err)
	}
	var lines []string
	for _, s := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(s) != "" {
			lines = append(lines, s)
		}
	}
	return lines
}

// アプリ設定領域配下の logs/ へ JSON Lines で記録する。
func TestNewWritesUnderLogsDir(t *testing.T) {
	paths := projectstore.AppPaths{Base: t.TempDir()}
	l, err := New(paths)
	if err != nil {
		t.Fatalf("ロガーを開けない: %v", err)
	}
	defer l.Close()

	if l.Dir() != paths.LogsDir() {
		t.Errorf("保存先が logs/ ではない: %q（期待 %q）", l.Dir(), paths.LogsDir())
	}
	l.Info("app.start", "起動しました")
	lines := readLines(t, filepath.Join(paths.LogsDir(), baseName))
	if len(lines) != 1 {
		t.Fatalf("行数が %d（期待 1）", len(lines))
	}
	var rec record
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("JSON Lines として読めない: %v（行 %q）", err, lines[0])
	}
	if rec.Level != string(LevelInfo) || rec.Event != "app.start" || rec.Message != "起動しました" {
		t.Errorf("記録の内容が違う: %+v", rec)
	}
	if rec.At == "" {
		t.Error("記録日時が空")
	}
}

// アプリ設定の保存先が定まらない端末では動作ログを開けない（機能は止めない = nil で使える）。
func TestNewWithoutBaseFails(t *testing.T) {
	if _, err := New(projectstore.AppPaths{}); err == nil {
		t.Fatal("保存先が空でもロガーが開けてしまった")
	}
	var nilLogger *Logger
	nilLogger.Error("x", "y")   // パニックしないこと
	nilLogger.RecoverPanic("x") // 同上（パニック中でないので何もしない）
	if err := nilLogger.Close(); err != nil {
		t.Errorf("nil ロガーの Close がエラー: %v", err)
	}
}

// 水準（エラー・警告・主要動作イベント）と付随項目が記録される。
func TestLevelsAndFields(t *testing.T) {
	l, dir := newTestLogger(t)
	l.Info("sync.publish", "反映しました", F("op", "publish"), F("count", 3))
	l.Warn("ai.degrade", "縮退しました", F("model", "gpt-test"))
	l.Error("audit.record", "記録に失敗しました", F("error", os.ErrPermission))

	lines := readLines(t, filepath.Join(dir, baseName))
	if len(lines) != 3 {
		t.Fatalf("行数が %d（期待 3）", len(lines))
	}
	wantLevel := []string{"info", "warn", "error"}
	for i, line := range lines {
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%d 行目が JSON ではない: %v", i+1, err)
		}
		if rec.Level != wantLevel[i] {
			t.Errorf("%d 行目の水準が %q（期待 %q）", i+1, rec.Level, wantLevel[i])
		}
	}
	var first record
	json.Unmarshal([]byte(lines[0]), &first)
	if first.Fields["op"] != "publish" {
		t.Errorf("付随項目 op が %v", first.Fields["op"])
	}
	if first.Fields["count"] != float64(3) {
		t.Errorf("付随項目 count が %v（数値のまま出ていない）", first.Fields["count"])
	}
	var third record
	json.Unmarshal([]byte(lines[2]), &third)
	if third.Fields["error"] != os.ErrPermission.Error() {
		t.Errorf("error 値が文字列化されていない: %v", third.Fields["error"])
	}
}

// 出力層で必ずマスキングを通す（メッセージ・付随項目のどちらから入っても）。
func TestMaskingAtOutput(t *testing.T) {
	l, dir := newTestLogger(t)
	const apiKey = "sk-ant-api03-TESTKEYVALUE0123456789abcdef"
	const remote = "https://ghp_TESTTOKENVALUE0123456789@example.invalid/team/proj.git"

	l.Error("ai.send", "認証に失敗しました: "+apiKey)
	l.Error("sync.publish", "反映に失敗しました", F("remote", remote))
	l.Error("app.panic", "落ちました", F("stack", "password=TESTPASSWORD0123 in frame"))

	data, err := os.ReadFile(filepath.Join(dir, baseName))
	if err != nil {
		t.Fatalf("動作ログを読めない: %v", err)
	}
	body := string(data)
	for _, secret := range []string{apiKey, "ghp_TESTTOKENVALUE0123456789", "TESTPASSWORD0123"} {
		if strings.Contains(body, secret) {
			t.Errorf("秘密情報が動作ログに残った: %q", secret)
		}
	}
	if !strings.Contains(body, "***MASKED***") {
		t.Error("マスキング後の表記が無い（フィルタを通っていない疑い）")
	}
	// マスキング後も JSON Lines として読めること（行へ掛けても壊れない）。
	for i, line := range readLines(t, filepath.Join(dir, baseName)) {
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("%d 行目がマスキング後に JSON として壊れた: %v（%q）", i+1, err, line)
		}
	}
}

// 再発防止: key=value 形式の秘密情報が値の末尾にあっても行が JSON として壊れない
// （整形後の行へマスキングを掛けて、JSON の区切りまで伏せてしまった不具合があった）。
//
// 整形後の行へマスキングを掛けると `\S+` が JSON の閉じ引用符・閉じ波括弧まで飲み込む。
// 整形前の値へ掛けること（= 出力層の中で順序が正しいこと）をここで固定する。
func TestMaskingKeepsJSONWellFormed(t *testing.T) {
	l, dir := newTestLogger(t)
	l.Error("sync.failed", "認証に失敗しました password=DUMMYPASSWORD0123")
	l.Error("sync.failed", "詳細", F("detail", "password=DUMMYPASSWORD0123"))
	l.Error("sync.failed", "詳細", F("detail", "token: DUMMYTOKENVALUE0123"))

	lines := readLines(t, filepath.Join(dir, baseName))
	if len(lines) != 3 {
		t.Fatalf("行数が %d（期待 3）", len(lines))
	}
	for i, line := range lines {
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%d 行目が JSON として壊れた: %v（%q）", i+1, err, line)
		}
	}
	body := strings.Join(lines, "\n")
	for _, secret := range []string{"DUMMYPASSWORD0123", "DUMMYTOKENVALUE0123"} {
		if strings.Contains(body, secret) {
			t.Errorf("秘密情報が残った: %q", secret)
		}
	}
	var second record
	json.Unmarshal([]byte(lines[1]), &second)
	if got, _ := second.Fields["detail"].(string); !strings.HasPrefix(got, "password="+masking.Masked) {
		t.Errorf("付随項目のマスキング結果が %q", got)
	}
}

// 上限を超えたら世代を送り、保持数を超えるファイルを作らない。
func TestRotationKeepsAtMostMaxFiles(t *testing.T) {
	l, dir := newTestLogger(t)
	l.maxBytes = 512 // 10MB の代わりに小さな上限で同じ経路を通す

	for i := 0; i < 400; i++ {
		l.Info("bulk", strings.Repeat("あ", 30))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("保存先を読めない: %v", err)
	}
	if len(entries) != MaxFiles {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("ファイル数が %d（期待 %d）: %v", len(entries), MaxFiles, names)
	}
	// 現行 + 世代 1..MaxFiles-1 が揃い、MaxFiles 番目の世代は作られない。
	if _, err := os.Stat(filepath.Join(dir, baseName)); err != nil {
		t.Errorf("現行ファイルが無い: %v", err)
	}
	for i := 1; i <= MaxFiles-1; i++ {
		if _, err := os.Stat(l.generation(i)); err != nil {
			t.Errorf("世代 %d が無い: %v", i, err)
		}
	}
	if _, err := os.Stat(l.generation(MaxFiles)); err == nil {
		t.Errorf("保持数を超える世代 %d が作られた", MaxFiles)
	}
	// どのファイルも上限を超えない。
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("ファイル情報を取得できない: %v", err)
		}
		if info.Size() > l.maxBytes {
			t.Errorf("%s が上限 %d を超えている: %d", e.Name(), l.maxBytes, info.Size())
		}
	}
}

// 既存ファイルへ追記して再開する（起動のたびに消さない）。
func TestReopenAppends(t *testing.T) {
	dir := t.TempDir()
	first, err := open(dir)
	if err != nil {
		t.Fatalf("ロガーを開けない: %v", err)
	}
	first.Info("app.start", "1 回目")
	first.Close()

	second, err := open(dir)
	if err != nil {
		t.Fatalf("ロガーを開き直せない: %v", err)
	}
	defer second.Close()
	second.Info("app.start", "2 回目")

	if lines := readLines(t, filepath.Join(dir, baseName)); len(lines) != 2 {
		t.Fatalf("行数が %d（期待 2。追記になっていない）", len(lines))
	}
}

// パニックも同じフィルタを通して記録し、握り潰さず再送出する。
func TestRecoverPanicRecordsAndRepanics(t *testing.T) {
	l, dir := newTestLogger(t)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("パニックが再送出されなかった")
			}
		}()
		defer l.RecoverPanic("app.panic")
		panic("想定外の状態です sk-ant-api03-PANICKEYVALUE0123456789")
	}()

	data, err := os.ReadFile(filepath.Join(dir, baseName))
	if err != nil {
		t.Fatalf("動作ログを読めない: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "app.panic") {
		t.Errorf("パニックが記録されていない: %q", body)
	}
	if strings.Contains(body, "sk-ant-api03-PANICKEYVALUE0123456789") {
		t.Error("パニックのメッセージがマスキングを通っていない")
	}
	if !strings.Contains(body, "\"stack\"") {
		t.Error("スタックが記録されていない")
	}
}
