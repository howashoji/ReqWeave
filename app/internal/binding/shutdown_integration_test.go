//go:build integration

package binding

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// leftoverLocks はプロジェクトフォルダに残っているロックファイルを返す。
func leftoverLocks(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "locks"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("locks を読めない: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// 閉じたときには背景処理（成果物生成）が終わっている。
//
// 生成は背景のゴルーチンで走り、終わりにロックを解放する。待たずに閉じると、
// 閉じた後にロックの解放や書き込みが走り、フォルダを消した直後に再作成される等の事故になる
// （結合テストの後片づけが「directory not empty」で落ちて判明した）。
func TestCloseDialogueProjectWaitsForBackgroundWork(t *testing.T) {
	a, store, stub := openForConfirm(t)
	stub.scripts = []string{"## 本文\n\n生成された内容です。FR-INV-001 を参照します。"}
	root := store.Root()

	if err := a.GenerateDocument(projectstore.DocKindRequirements, false,
		WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatalf("生成を開始できない: %v", err)
	}
	// 生成の完了を待たずに閉じる（利用者が生成中に画面を離れる操作に相当）。
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatalf("閉じられない: %v", err)
	}

	// 戻った時点で背景処理は終わっており、ロックが残っていない。
	if names := leftoverLocks(t, root); len(names) != 0 {
		t.Errorf("閉じた後にロックが残っている（背景処理の完了を待っていない）: %v", names)
	}
	// 同じプロジェクトをすぐ開き直せる（残ったロックに阻まれない）。
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("閉じた直後に開き直せない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
}

// アプリの終了で、開いているプロジェクトを閉じる（保存キューを流し切る）。
//
// Store.Close が保存キューを流し切る唯一の経路であり、
// 終了経路がここを通らないと、キューへ残った書き込みが完了しないまま終わりうる。
func TestShutdownClosesOpenProject(t *testing.T) {
	a, store, _ := openForConfirm(t)
	root := store.Root()

	a.Shutdown(context.Background())

	// 閉じたので「開いているプロジェクト」は無い。
	if _, err := a.current(); err == nil {
		t.Error("終了してもプロジェクトが開いたままになっている")
	}
	if names := leftoverLocks(t, root); len(names) != 0 {
		t.Errorf("終了後にロックが残っている: %v", names)
	}
}
