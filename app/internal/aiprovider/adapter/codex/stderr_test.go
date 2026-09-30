package codex

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// recordedEvent は動作ログへ渡された 1 件。
type recordedEvent struct {
	level  aiprovider.EventLevel
	event  string
	fields map[string]string
}

// eventLog は記録先。**子プロセスの終了は別のゴルーチンから記録される**ため、
// 排他を取らずに読み書きすると取りこぼす（実際にテストが不定期に落ちた）。
type eventLog struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (l *eventLog) record(level aiprovider.EventLevel, event, message string, fields map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, recordedEvent{level: level, event: event, fields: fields})
}

func (l *eventLog) snapshot() []recordedEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]recordedEvent, len(l.events))
	copy(out, l.events)
	return out
}

func recordingAdapter(t *testing.T, log *eventLog) *Adapter {
	t.Helper()
	return NewWithOptions(stubKeys{key: dummyKey}, "codex/テスト", aiprovider.AdapterOptions{
		OnEvent: log.record,
	})
}

// 起動後の検査が終わる前に終わった場合だけ、標準エラー出力の末尾を残す。
func TestStderrTailIsKeptOnlyBeforeReady(t *testing.T) {
	const marker = "failed to initialize sqlite state runtime"

	t.Run("検査の前に終わったとき", func(t *testing.T) {
		newFakeEnv(t, fakeScenario{StderrOnStart: marker, ExitOnStart: true})
		log := &eventLog{}
		adapter := recordingAdapter(t, log)

		ch, err := adapter.StreamMessage(context.Background(), testRequest())
		if err != nil {
			t.Fatalf("送信を開始できない: %v", err)
		}
		last := lastEvent(t, collect(t, ch))
		if last.Kind != aiprovider.EventError {
			t.Fatalf("エラーで終わっていない: %+v", last)
		}
		if !strings.Contains(last.Err.Message, marker) {
			t.Errorf("起動に失敗した理由が伝わらない: %q", last.Err.Message)
		}
		waitFor(t, func() bool { return hasStderrTail(log.snapshot(), marker) }, "標準エラー出力の末尾が動作ログに残らない")
	})

	t.Run("検査の後に終わったとき", func(t *testing.T) {
		newFakeEnv(t, fakeScenario{StderrOnStart: marker, Turn: fakeTurn{Kind: "exit"}})
		log := &eventLog{}
		adapter := recordingAdapter(t, log)

		ch, err := adapter.StreamMessage(context.Background(), testRequest())
		if err != nil {
			t.Fatalf("送信を開始できない: %v", err)
		}
		last := lastEvent(t, collect(t, ch))
		if last.Kind != aiprovider.EventError {
			t.Fatalf("エラーで終わっていない: %+v", last)
		}
		if strings.Contains(last.Err.Message, marker) {
			t.Errorf("検査の後の標準エラー出力を画面へ出している: %q", last.Err.Message)
		}
		// 動作ログにも残さない（会話の内容が混じりうるため）。
		waitFor(t, func() bool { return hasEvent(log.snapshot(), eventProcessExited) }, "終了が記録されない")
		if hasStderrTail(log.snapshot(), marker) {
			t.Error("検査の後の標準エラー出力を動作ログへ残している")
		}
	})
}

func hasStderrTail(events []recordedEvent, marker string) bool {
	for _, ev := range events {
		if strings.Contains(ev.fields["stderr_tail"], marker) {
			return true
		}
	}
	return false
}

func hasEvent(events []recordedEvent, name string) bool {
	for _, ev := range events {
		if ev.event == name {
			return true
		}
	}
	return false
}

// 記録するのは起動・終了・検査の結果・エラーの分類に限り、やり取りの本文を残さない。
func TestProcessEventsDoNotCarryConversation(t *testing.T) {
	newFakeEnv(t, fakeScenario{})
	log := &eventLog{}
	adapter := recordingAdapter(t, log)

	req := testRequest()
	ch, err := adapter.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	collect(t, ch)
	if err := procManager.stopCurrent("テストの確認"); err != nil {
		t.Fatalf("子プロセスを止められない: %v", err)
	}

	recorded := log.snapshot()
	if !hasEvent(recorded, eventProcessStarted) || !hasEvent(recorded, eventProcessStopped) {
		t.Errorf("起動・停止が記録されていない: %+v", recorded)
	}
	for _, ev := range recorded {
		for key, value := range ev.fields {
			for _, secret := range []string{req.System, req.Messages[0].Content, dummyKey} {
				if strings.Contains(value, secret) {
					t.Errorf("記録に本文・キーが混じっている（%s=%q）", key, value)
				}
			}
		}
	}
}
