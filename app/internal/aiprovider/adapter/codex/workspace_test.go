package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// useTempWorkspace は一時領域の場所をテスト用へ差し替える。
func useTempWorkspace(t *testing.T) workspacePaths {
	t.Helper()
	base := t.TempDir()
	paths := workspacePaths{root: filepath.Join(base, "codex"), lock: filepath.Join(base, "codex.lock")}
	orig := workspaceLocationFn
	workspaceLocationFn = func() (workspacePaths, error) { return paths, nil }
	t.Cleanup(func() { workspaceLocationFn = orig })
	return paths
}

// 一時領域は作り直され、手放すとフォルダごと消える。
func TestWorkspaceIsRecreatedAndRemoved(t *testing.T) {
	paths := useTempWorkspace(t)

	// 前回の残り（異常終了で残ったもの）を置いておく。
	if err := os.MkdirAll(filepath.Join(paths.root, "home"), 0o700); err != nil {
		t.Fatalf("残りを作れない: %v", err)
	}
	leftover := filepath.Join(paths.root, "home", "logs_2.sqlite")
	if err := os.WriteFile(leftover, []byte("前回の動作ログ"), 0o600); err != nil {
		t.Fatalf("残りを作れない: %v", err)
	}

	ws, err := acquireWorkspace()
	if err != nil {
		t.Fatalf("一時領域を確保できない: %v", err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Error("前回の残りが消えていない")
	}
	for _, dir := range []string{ws.home(), ws.work()} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s が作られていない: %v", dir, err)
		}
	}
	// 手元のモデル定義が書き出され、入力が文字のみであること。
	body, err := os.ReadFile(ws.catalog())
	if err != nil {
		t.Fatalf("モデル定義が書き出されていない: %v", err)
	}
	var catalog struct {
		Models []catalogModel `json:"models"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatalf("モデル定義を解釈できない: %v", err)
	}
	if len(catalog.Models) == 0 {
		t.Fatal("モデル定義が空")
	}
	for _, m := range catalog.Models {
		if len(m.InputModalities) != 1 || m.InputModalities[0] != "text" {
			t.Errorf("%s の入力が文字のみでない: %v", m.Slug, m.InputModalities)
		}
	}

	if err := ws.release(); err != nil {
		t.Fatalf("一時領域を手放せない: %v", err)
	}
	if _, err := os.Stat(paths.root); !os.IsNotExist(err) {
		t.Error("一時領域がフォルダごと消えていない")
	}
	// 手放した後は取り直せる（排他が解けている）。
	again, err := acquireWorkspace()
	if err != nil {
		t.Fatalf("手放した後に取り直せない: %v", err)
	}
	_ = again.release()
}

// 2 つ目のプロセスは一時領域の排他を取れない（自動再試行の対象にしない一時的エラー）。
func TestWorkspaceIsExclusive(t *testing.T) {
	useTempWorkspace(t)

	first, err := acquireWorkspace()
	if err != nil {
		t.Fatalf("一時領域を確保できない: %v", err)
	}
	defer first.release()

	if _, err := acquireWorkspace(); !errors.Is(err, errWorkspaceBusy) {
		t.Fatalf("2 つ目が排他を取れてしまった: %v", err)
	}
}

// 本システムの起動時に、使われていない残りを削除する（異常終了への備え）。
func TestRemoveLeftoverWorkspace(t *testing.T) {
	paths := useTempWorkspace(t)
	if err := os.MkdirAll(filepath.Join(paths.root, "home"), 0o700); err != nil {
		t.Fatalf("残りを作れない: %v", err)
	}

	if err := removeLeftoverWorkspace(); err != nil {
		t.Fatalf("残りを削除できない: %v", err)
	}
	if _, err := os.Stat(paths.root); !os.IsNotExist(err) {
		t.Error("残りが消えていない")
	}
	// 残りが無い状態でも失敗しない。
	if err := removeLeftoverWorkspace(); err != nil {
		t.Errorf("残りが無いのに失敗した: %v", err)
	}
}

// 使用中（別のウィンドウが動かしている）の一時領域は消さない。
func TestRemoveLeftoverWorkspaceKeepsInUse(t *testing.T) {
	useTempWorkspace(t)

	ws, err := acquireWorkspace()
	if err != nil {
		t.Fatalf("一時領域を確保できない: %v", err)
	}
	defer ws.release()
	marker := filepath.Join(ws.home(), "使用中")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("印を置けない: %v", err)
	}

	if err := removeLeftoverWorkspace(); err != nil {
		t.Fatalf("失敗した: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("使用中の一時領域を消してしまった: %v", err)
	}
}
