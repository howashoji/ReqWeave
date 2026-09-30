package aiprovider

import (
	"encoding/json"
	"testing"
)

// MaxOutputTokens が未指定なら写像値を使う。
func TestEffectiveMaxOutputTokens(t *testing.T) {
	req := ChatRequest{Effort: EffortParams{MaxOutputTokens: 8192}}
	if got := req.EffectiveMaxOutputTokens(); got != 8192 {
		t.Errorf("写像値が使われていない: %d", got)
	}
	req.MaxOutputTokens = 1000
	if got := req.EffectiveMaxOutputTokens(); got != 1000 {
		t.Errorf("明示値が優先されていない: %d", got)
	}
}

// EventUsage は EventDone の直前に必ず 1 回。
func TestSinkUsageBeforeDone(t *testing.T) {
	sink, ch := NewStream()
	sink.Text("あ")
	sink.SetUsage(TokenUsage{InputTokens: 10})
	sink.Text("い")
	sink.SetUsage(TokenUsage{OutputTokens: 20, ReasoningTokens: 5})
	sink.Done(false)

	events := collectEvents(t, ch)
	kinds := make([]EventKind, len(events))
	for i, ev := range events {
		kinds[i] = ev.Kind
	}
	want := []EventKind{EventTextDelta, EventTextDelta, EventUsage, EventDone}
	if len(kinds) != len(want) {
		t.Fatalf("イベント列が違う: %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("イベント列が違う: %v", kinds)
		}
	}
	u := events[2].Usage
	if u == nil || u.InputTokens != 10 || u.OutputTokens != 20 || u.ReasoningTokens != 5 {
		t.Errorf("実績値が合算されていない: %+v", u)
	}
	if events[3].Interrupted {
		t.Error("中断でないのに Interrupted が立っている")
	}
	// EventDone にも同じ実績を載せる（StreamEvent の定義）。
	if events[3].Usage == nil || *events[3].Usage != *u {
		t.Errorf("EventDone に実績が載っていない: %+v", events[3].Usage)
	}
}

// 実績値が得られない場合は Usage を送出しない（推定値で代用しない）。
func TestSinkNoUsageWhenAbsent(t *testing.T) {
	sink, ch := NewStream()
	sink.Text("本文")
	sink.Done(false)

	events := collectEvents(t, ch)
	for _, ev := range events {
		if ev.Kind == EventUsage {
			t.Fatalf("実績が無いのに EventUsage が送出された: %+v", ev)
		}
	}
	if len(events) != 2 || events[1].Kind != EventDone {
		t.Fatalf("EventDone で終わっていない: %+v", events)
	}
	// 欠測は Usage=nil の EventDone として上位へ伝える。
	if events[1].Usage != nil {
		t.Errorf("実績が無いのに EventDone に値が載っている: %+v", events[1].Usage)
	}
}

// 中断は Interrupted 付きの EventDone で閉じる。
func TestSinkInterrupted(t *testing.T) {
	sink, ch := NewStream()
	sink.Text("途中まで")
	sink.Done(true)

	events := collectEvents(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventDone || !last.Interrupted {
		t.Fatalf("中断が通知されていない: %+v", last)
	}
	if events[0].Text != "途中まで" {
		t.Errorf("受信済み本文が失われている: %+v", events[0])
	}
}

// 中断・異常終了でも受信済みの実績があれば送出する。
func TestSinkFailEmitsUsage(t *testing.T) {
	sink, ch := NewStream()
	sink.SetUsage(TokenUsage{InputTokens: 7})
	sink.Fail(&ProviderError{Class: ErrClassTransient, Provider: ProviderAnthropic})

	events := collectEvents(t, ch)
	if len(events) != 2 {
		t.Fatalf("イベント数が違う: %+v", events)
	}
	if events[0].Kind != EventUsage || events[0].Usage.InputTokens != 7 {
		t.Errorf("実績が送出されていない: %+v", events[0])
	}
	if events[1].Kind != EventError || events[1].Err == nil {
		t.Errorf("エラーが送出されていない: %+v", events[1])
	}
}

// 二重終了・終了後の送出でチャネルが壊れないこと（アダプタの実装ミスをテストで押さえる）。
func TestSinkClosedIsIdempotent(t *testing.T) {
	sink, ch := NewStream()
	sink.Done(false)
	sink.Text("閉じた後")
	sink.Done(true)
	sink.Fail(&ProviderError{})

	events := collectEvents(t, ch)
	if len(events) != 1 || events[0].Kind != EventDone {
		t.Fatalf("閉じた後の送出が反映されている: %+v", events)
	}
}

// SchemaMap は構造化出力の要求有無と、JSON として解釈できるかを返す。
//
// 本層はスキーマの**内容**を検証しない（検証・再要求・縮退は対話エンジンの責務）。
// ここで判定するのは「アダプタが自社パラメータへ写せる形か」だけである。
func TestChatRequestSchemaMap(t *testing.T) {
	t.Run("未指定は非構造化", func(t *testing.T) {
		m, ok, err := ChatRequest{}.SchemaMap()
		if err != nil {
			t.Fatalf("エラーになった: %v", err)
		}
		if ok || m != nil {
			t.Errorf("ok=%v m=%v, want false / nil", ok, m)
		}
	})

	t.Run("JSON Schema を map へ展開する", func(t *testing.T) {
		req := ChatRequest{ResponseSchema: json.RawMessage(`{"type":"object","properties":{"decisions":{"type":"array"}}}`)}
		m, ok, err := req.SchemaMap()
		if err != nil {
			t.Fatalf("エラーになった: %v", err)
		}
		if !ok {
			t.Fatal("ok=false（構造化出力として認識されていない）")
		}
		if m["type"] != "object" {
			t.Errorf("type = %v, want object", m["type"])
		}
		props, _ := m["properties"].(map[string]any)
		if props["decisions"] == nil {
			t.Errorf("properties が失われている: %v", m)
		}
	})

	t.Run("JSON として不正ならエラー", func(t *testing.T) {
		req := ChatRequest{ResponseSchema: json.RawMessage(`{壊れている`)}
		if _, ok, err := req.SchemaMap(); err == nil {
			t.Errorf("エラーにならない（ok=%v）", ok)
		}
	})

	t.Run("オブジェクト以外はエラー", func(t *testing.T) {
		req := ChatRequest{ResponseSchema: json.RawMessage(`["配列は JSON Schema のルートにならない"]`)}
		if _, ok, err := req.SchemaMap(); err == nil {
			t.Errorf("エラーにならない（ok=%v）", ok)
		}
	})
}
