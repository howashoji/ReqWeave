package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// shorten は待ち時間を短くする（時間そのものではなく、時間が来たときの振る舞いを確かめるため）。
func shorten(t *testing.T, target *time.Duration, d time.Duration) {
	t.Helper()
	orig := *target
	*target = d
	t.Cleanup(func() { *target = orig })
}

// AI 呼び出しの無い時間が続いたら子プロセスを止め、一時領域も消す。
func TestIdleStopsProcessAndRemovesWorkspace(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{})
	shorten(t, &idleStopAfter, 100*time.Millisecond)
	adapter := newTestAdapter(t)

	ch, err := adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	if last := lastEvent(t, collect(t, ch)); last.Kind != aiprovider.EventDone {
		t.Fatalf("正常に終わっていない: %+v", last)
	}

	waitFor(t, func() bool { return procManager.current() == nil }, "待機で子プロセスが止まらない")
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(env.base, "codex"))
		return os.IsNotExist(err)
	}, "待機で止めた後に一時領域が残っている")

	// 止めた後の呼び出しで起動し直せること。
	ch, err = adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("止めた後に送信を開始できない: %v", err)
	}
	if last := lastEvent(t, collect(t, ch)); last.Kind != aiprovider.EventDone {
		t.Fatalf("止めた後の呼び出しが正常に終わらない: %+v", last)
	}
}

// AIプロバイダ・認証方式・キーの変更で子プロセスを止める。
func TestSettingsChangeStopsProcess(t *testing.T) {
	newFakeEnv(t, fakeScenario{})
	adapter := newTestAdapter(t)

	ch, err := adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	collect(t, ch)
	if procManager.current() == nil {
		t.Fatal("呼び出しの後に子プロセスが残っていない（前提が崩れている）")
	}

	// 公開バインディング層が呼ぶ口（プロバイダを列挙しない）。
	aiprovider.SettingsChanged(nil)

	if procManager.current() != nil {
		t.Error("設定の変更で子プロセスが止まらない")
	}
}

// 中断の待ちが上限を超えたら子プロセスを止め、中断として閉じる。
func TestInterruptTimeoutStopsProcess(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang_ignore_interrupt"}})
	shorten(t, &interruptGrace, 200*time.Millisecond)
	adapter := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := adapter.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	waitFor(t, func() bool { return countOf(env.received(t), "turn/start") > 0 }, "ターンが始まらない")
	cancel()

	last := lastEvent(t, collect(t, ch))
	if last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("中断として閉じていない: %+v", last)
	}
	waitFor(t, func() bool { return procManager.current() == nil }, "中断が終わらない子プロセスを止めていない")
}
