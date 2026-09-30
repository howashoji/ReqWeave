package applog

// 診断情報の書き出しの単体テスト。
//
// 受け入れ条件の対応:
//   - 含まれるものが動作ログ・実行環境だけであること（業務データ・コアダンプを含めない）
//   - 秘密情報が平文でも Base64 でも残らないこと（記録の時点でマスキング済み）
//   - 動作ログを開けない端末でも実行環境だけは書き出せること

import (
	"archive/zip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/masking"
)

const dummyKey = "sk-ant-api03-DIAGNOSTICKEY0123456789"

func testEnv() DiagnosticEnvironment {
	return DiagnosticEnvironment{
		AppName: "ReqWeave", AppVersion: "0.1.0",
		OS: "macOS", OSVersion: "26.6.2", Arch: "arm64",
	}
}

// readArchive は書庫の中身を名前 → 内容で返す。
func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("書庫を開けない: %v", err)
	}
	defer r.Close()
	out := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("書庫の %s を開けない: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("書庫の %s を読めない: %v", f.Name, err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestDiagnosticsListsContents(t *testing.T) {
	l, _ := newTestLogger(t)
	l.Info(EventAppStart, "起動しました")
	l.Error(EventAppPanic, "想定外の状態です")

	d, err := l.Diagnostics(testEnv())
	if err != nil {
		t.Fatalf("診断情報を組み立てられない: %v", err)
	}

	if !strings.Contains(d.Environment, "macOS 26.6.2") || !strings.Contains(d.Environment, "ReqWeave 0.1.0") {
		t.Errorf("実行環境が入っていない: %q", d.Environment)
	}
	var names []string
	for _, item := range d.Items {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	want := []string{"app.log", environmentName}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("含まれるものが %v（期待 %v）", names, want)
	}
	if !strings.Contains(d.LogText, EventAppPanic) {
		t.Errorf("動作ログの本文が入っていない: %q", d.LogText)
	}
	if d.Truncated {
		t.Error("切っていないのに Truncated が真")
	}
	if d.TotalBytes != int64(len(d.LogText)) {
		t.Errorf("TotalBytes が %d（本文 %d バイト）", d.TotalBytes, len(d.LogText))
	}
}

// 世代がある場合は古い順に連結する（時系列で読めるようにする）。
func TestDiagnosticsOrdersGenerationsOldestFirst(t *testing.T) {
	l, dir := newTestLogger(t)
	l.Close()
	for name, body := range map[string]string{
		"app.2.log": `{"event":"最古"}` + "\n",
		"app.1.log": `{"event":"中間"}` + "\n",
		"app.log":   `{"event":"最新"}` + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), fileMode); err != nil {
			t.Fatalf("下ごしらえに失敗: %v", err)
		}
	}
	reopened := reopen(t, dir)

	d, err := reopened.Diagnostics(testEnv())
	if err != nil {
		t.Fatalf("診断情報を組み立てられない: %v", err)
	}
	if i, j, k := strings.Index(d.LogText, "最古"), strings.Index(d.LogText, "中間"), strings.Index(d.LogText, "最新"); !(i < j && j < k) {
		t.Errorf("古い順に並んでいない: %q", d.LogText)
	}
}

// 上限を超える本文は新しい側を残して切り、切ったことを伝える。
func TestDiagnosticsTruncatesLongLog(t *testing.T) {
	l, dir := newTestLogger(t)
	l.Close()
	body := strings.Repeat("x", previewMaxBytes) + "最新の行\n"
	if err := os.WriteFile(filepath.Join(dir, baseName), []byte(body), fileMode); err != nil {
		t.Fatalf("下ごしらえに失敗: %v", err)
	}
	reopened := reopen(t, dir)

	d, err := reopened.Diagnostics(testEnv())
	if err != nil {
		t.Fatalf("診断情報を組み立てられない: %v", err)
	}
	if !d.Truncated {
		t.Fatal("上限を超えたのに Truncated が偽")
	}
	if !strings.Contains(d.LogText, "最新の行") {
		t.Error("新しい側が残っていない")
	}
	if !strings.HasPrefix(d.LogText, truncationNotice) {
		t.Errorf("省略した旨の印が無い: %.60q", d.LogText)
	}
	if d.TotalBytes != int64(len(body)) {
		t.Errorf("TotalBytes が %d（期待 %d）", d.TotalBytes, len(body))
	}
}

func TestWriteDiagnosticsArchive(t *testing.T) {
	l, _ := newTestLogger(t)
	// 第一防衛をすり抜けた想定でキーを渡す。出力層のマスキングを通って書かれる。
	l.Error("test.leak", "キーが不正です "+dummyKey)

	dst := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := l.WriteDiagnostics(dst, testEnv()); err != nil {
		t.Fatalf("書き出せない: %v", err)
	}

	entries := readArchive(t, dst)
	if len(entries) != 2 {
		t.Fatalf("書庫の中身が %d 件（期待 2 件）: %v", len(entries), entries)
	}
	if _, ok := entries[environmentName]; !ok {
		t.Errorf("実行環境の説明が入っていない: %v", entries)
	}
	if !strings.Contains(entries[baseName], "test.leak") {
		t.Errorf("動作ログが入っていない: %q", entries[baseName])
	}

	// シークレットキーの非出力の確かめ方と同じ手順（平文・Base64 の両表現で全文検索）。
	all := strings.Join([]string{entries[environmentName], entries[baseName]}, "\n")
	if strings.Contains(all, dummyKey) {
		t.Error("書庫にキーが平文で残っている")
	}
	if strings.Contains(all, base64.StdEncoding.EncodeToString([]byte(dummyKey))) {
		t.Error("書庫にキーが Base64 で残っている")
	}
	if !strings.Contains(entries[baseName], masking.Masked) {
		t.Errorf("マスキングを通っていない: %q", entries[baseName])
	}
}

// 動作ログを開けない端末（nil ロガー）でも実行環境だけは渡せる。
func TestDiagnosticsWithoutLogger(t *testing.T) {
	var l *Logger

	d, err := l.Diagnostics(testEnv())
	if err != nil {
		t.Fatalf("診断情報を組み立てられない: %v", err)
	}
	if len(d.Items) != 1 || d.Items[0].Name != environmentName {
		t.Errorf("含まれるものが %v（期待 %s のみ）", d.Items, environmentName)
	}
	if d.LogText != "" {
		t.Errorf("動作ログが無いのに本文がある: %q", d.LogText)
	}

	dst := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := l.WriteDiagnostics(dst, testEnv()); err != nil {
		t.Fatalf("書き出せない: %v", err)
	}
	entries := readArchive(t, dst)
	if len(entries) != 1 || !strings.Contains(entries[environmentName], "macOS") {
		t.Errorf("書庫の中身が期待と違う: %v", entries)
	}
}

// OS の版を取得できない環境でも、欠測を偽の値で埋めない。
func TestDiagnosticEnvironmentWithoutOSVersion(t *testing.T) {
	env := testEnv()
	env.OSVersion = ""
	text := env.Text()
	if !strings.Contains(text, "版は取得できませんでした") {
		t.Errorf("欠測が明示されていない: %q", text)
	}
}

// 書庫の更新時刻がテストの実行時刻に依存しないこと（TZ 依存にしない）。
func TestWriteDiagnosticsUsesUTCSafeHeader(t *testing.T) {
	l, _ := newTestLogger(t)
	l.Info("x", "y")
	dst := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := l.WriteDiagnostics(dst, testEnv()); err != nil {
		t.Fatalf("書き出せない: %v", err)
	}
	r, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatalf("書庫を開けない: %v", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Modified.IsZero() || f.Modified.After(time.Now().Add(time.Minute)) {
			t.Errorf("%s の更新時刻が不正: %v", f.Name, f.Modified)
		}
	}
}
