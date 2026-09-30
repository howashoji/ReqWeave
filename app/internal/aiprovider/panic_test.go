package aiprovider

// ストリーミング用ゴルーチンのパニックの扱いの単体テスト。
//
// 受け入れ条件の対応:
//   - パニックが起きても記録なしでプロセスが落ちない（恒久的エラーとしてストリームが閉じる）
//   - パニックの内容は利用者向けの文言へ載らない（記録先だけが受け取る）
//   - 記録先が未設定でも落ちない

import (
	"context"
	"strings"
	"testing"
)

// panickingAdapter は StreamMessage の中でパニックするテスト用アダプタ。
// StreamRetrying は自前のゴルーチンから StreamMessage を呼ぶため、
// ここでのパニックは「ゴルーチンの中のパニック」になる。
type panickingAdapter struct{ value string }

func (p *panickingAdapter) ID() ProviderID                                  { return ProviderAnthropic }
func (p *panickingAdapter) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }
func (p *panickingAdapter) VerifyKey(context.Context) error                 { return nil }
func (p *panickingAdapter) StreamMessage(context.Context, ChatRequest) (<-chan StreamEvent, error) {
	panic(p.value)
}

func TestStreamRetryingSurvivesAdapterPanic(t *testing.T) {
	const secretish = "想定外の状態 /Users/tanaka/案件A/project.yaml"
	var recorded []any
	a := &panickingAdapter{value: secretish}

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{
		OnPanic: func(r any) { recorded = append(recorded, r) },
	})
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}

	var perr *ProviderError
	var kinds int
	for ev := range ch {
		kinds++
		if ev.Kind == EventError {
			perr = ev.Err
		}
	}
	if kinds == 0 {
		t.Fatal("チャネルが閉じられず、イベントも届かなかった")
	}
	if perr == nil {
		t.Fatal("パニックがエラーとして届かなかった")
	}
	if perr.Code != CodeInternalPanic {
		t.Errorf("コードが %q（期待 %q）", perr.Code, CodeInternalPanic)
	}
	if perr.Retryable() {
		t.Error("パニックを再試行対象にしている（同じ入力で再発する）")
	}
	if strings.Contains(perr.Message, "/Users/tanaka") || strings.Contains(perr.Error(), "/Users/tanaka") {
		t.Errorf("パニックの内容が利用者向けの文言へ載っている: %q", perr.Error())
	}
	if len(recorded) != 1 {
		t.Fatalf("記録先の呼び出しが %d 回（期待 1）", len(recorded))
	}
	if got, ok := recorded[0].(string); !ok || got != secretish {
		t.Errorf("記録先が受け取った値が違う: %v", recorded[0])
	}
}

// 記録先が未設定でも落ちないこと（動作ログを開けない端末）。
func TestStreamRetryingPanicWithoutRecorder(t *testing.T) {
	a := &panickingAdapter{value: "boom"}

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	var perr *ProviderError
	for ev := range ch {
		if ev.Kind == EventError {
			perr = ev.Err
		}
	}
	if perr == nil || perr.Code != CodeInternalPanic {
		t.Fatalf("パニックがエラーとして届かなかった: %+v", perr)
	}
}

// アダプタ側のゴルーチン（StreamMessage が返したあとに回るもの）でも同じ扱いになること。
func TestRecoverStreamPanicFailsStream(t *testing.T) {
	sink, ch := NewStream()
	var recorded any

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer RecoverStreamPanic(sink, ProviderOpenAI, func(r any) { recorded = r })
		sink.Text("途中まで")
		panic("応答の解釈に失敗")
	}()
	<-done

	var text string
	var perr *ProviderError
	for ev := range ch {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventError:
			perr = ev.Err
		}
	}
	if text != "途中まで" {
		t.Errorf("受信済みの本文が失われた: %q", text)
	}
	if perr == nil || perr.Provider != ProviderOpenAI || perr.Code != CodeInternalPanic {
		t.Fatalf("恒久的エラーとして閉じられていない: %+v", perr)
	}
	if recorded != "応答の解釈に失敗" {
		t.Errorf("記録先が受け取った値が違う: %v", recorded)
	}
}

// パニックしていないときは何もしないこと（正常終了を横取りしない）。
func TestRecoverStreamPanicWithoutPanic(t *testing.T) {
	sink, ch := NewStream()
	called := false

	func() {
		defer RecoverStreamPanic(sink, ProviderGoogle, func(any) { called = true })
		sink.Done(false)
	}()

	var kinds []EventKind
	for ev := range ch {
		kinds = append(kinds, ev.Kind)
	}
	if called {
		t.Error("パニックしていないのに記録先を呼んだ")
	}
	if len(kinds) != 1 || kinds[0] != EventDone {
		t.Errorf("正常終了が横取りされた: %v", kinds)
	}
}
