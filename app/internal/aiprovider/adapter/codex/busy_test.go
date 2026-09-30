package codex

import (
	"context"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// 別のウィンドウが一時領域を使っているときは一時的エラーで返し、
// **自動再試行の対象にしない**（相手が閉じるまで解消せず、再試行は待ち時間を延ばすだけ）。
//
// 再試行層を通した経路で確かめる（アダプタ単体では再試行は起きないため）。
func TestWorkspaceBusyIsNotRetried(t *testing.T) {
	newFakeEnv(t, fakeScenario{})

	// 別のウィンドウが使っている状態を作る。
	held, err := acquireWorkspace()
	if err != nil {
		t.Fatalf("一時領域を確保できない: %v", err)
	}
	defer held.release()

	adapter := newTestAdapter(t)
	start := time.Now()
	ch, err := aiprovider.StreamRetrying(context.Background(), adapter, testRequest(), aiprovider.StreamOptions{})
	if err != nil {
		t.Fatalf("再試行層を通せない: %v", err)
	}
	events := collect(t, ch)
	elapsed := time.Since(start)

	last := lastEvent(t, events)
	if last.Kind != aiprovider.EventError {
		t.Fatalf("エラーで終わっていない: %+v", last)
	}
	if last.Err.Code != codeWorkspaceBusy {
		t.Errorf("Code = %q, want %q", last.Err.Code, codeWorkspaceBusy)
	}
	if last.Err.Class != aiprovider.ErrClassTransient {
		t.Errorf("分類 = %v, want transient（利用者の操作で再実行できる）", last.Err.Class)
	}
	// 再試行していれば初回のバックオフ（1 秒 ±25%）だけで 0.75 秒以上かかる。
	if elapsed > 500*time.Millisecond {
		t.Errorf("自動再試行している（%v かかった）", elapsed)
	}
}
