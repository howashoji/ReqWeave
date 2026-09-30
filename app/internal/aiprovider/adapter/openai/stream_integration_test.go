//go:build integration

// 結合テスト（アダプタ × HTTP の SSE）。実キー・実 API は使わない。

package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

func writeSSE(t *testing.T, w http.ResponseWriter, event, data string) {
	t.Helper()
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	w.(http.Flusher).Flush()
}

func sseHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
}

func textDelta(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf(`{"type":"response.output_text.delta","sequence_number":1,"item_id":"item_1","output_index":0,"content_index":0,"delta":%s,"logprobs":[]}`, b)
}

const respCompleted = `{"type":"response.completed","sequence_number":9,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-test","status":"completed","output":[],"parallel_tool_calls":false,"tool_choice":"auto","tools":[],"usage":{"input_tokens":11,"input_tokens_details":{"cached_tokens":0},"output_tokens":23,"output_tokens_details":{"reasoning_tokens":5},"total_tokens":34}}}`

func collect(t *testing.T, ch <-chan aiprovider.StreamEvent) []aiprovider.StreamEvent {
	t.Helper()
	var out []aiprovider.StreamEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatalf("チャネルが閉じられない（受信済み: %+v）", out)
		}
	}
}

func testRequest() aiprovider.ChatRequest {
	return aiprovider.ChatRequest{
		Model:    "gpt-test",
		System:   "システムプロンプト",
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "こんにちは"}},
		Effort:   aiprovider.MapEffort(aiprovider.ProviderOpenAI, aiprovider.EffortHigh),
	}
}

// Responses 系統の差分イベントを StreamEvent へ変換する。
// EventUsage は EventDone の直前に 1 回（推論トークンを含む）。
func TestStreamMessage(t *testing.T) {
	var body map[string]any
	var gotAuth string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "response.output_text.delta", textDelta("こん"))
		writeSSE(t, w, "response.output_text.delta", textDelta("にちは"))
		writeSSE(t, w, "response.completed", respCompleted)
	})

	ch, err := a.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collect(t, ch)

	if gotAuth != "Bearer "+dummyKey {
		t.Errorf("Authorization ヘッダが違う: %q", gotAuth)
	}
	if body["instructions"] != "システムプロンプト" {
		t.Errorf("システムプロンプトが送られていない: %v", body["instructions"])
	}
	if got := body["max_output_tokens"]; got != float64(16384) {
		t.Errorf("最大出力トークンが写像値でない: %v", got)
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != aiprovider.ReasoningHigh {
		t.Errorf("推論努力レベルが送られていない: %v", body["reasoning"])
	}
	if body["store"] != false {
		t.Errorf("送信データをプロバイダ側に保存しない設定になっていない: %v", body["store"])
	}

	var text string
	var kinds []aiprovider.EventKind
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == aiprovider.EventTextDelta {
			text += ev.Text
		}
	}
	if text != "こんにちは" {
		t.Errorf("本文が違う: %q", text)
	}
	if len(kinds) != 4 || kinds[2] != aiprovider.EventUsage || kinds[3] != aiprovider.EventDone {
		t.Fatalf("イベント列が違う: %v", kinds)
	}
	u := events[2].Usage
	if u == nil || u.InputTokens != 11 || u.OutputTokens != 23 || u.ReasoningTokens != 5 {
		t.Errorf("実績値が違う: %+v", u)
	}
}

// 応答完了前に最初の差分が届く。中断は Interrupted 付き EventDone。
func TestStreamMessageInterrupt(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		sseHeader(w)
		writeSSE(t, w, "response.output_text.delta", textDelta("途中まで"))
		<-release
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := a.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	select {
	case ev := <-ch:
		if ev.Kind != aiprovider.EventTextDelta || ev.Text != "途中まで" {
			t.Fatalf("最初のイベントが本文差分でない: %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("応答完了前に差分が届かない")
	}

	cancel()
	events := collect(t, ch)
	last := events[len(events)-1]
	if last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("中断が Interrupted 付き EventDone で通知されない: %+v", events)
	}
}

// 429 のうち残高不足（insufficient_quota）は設定起因、それ以外は一時的。
func TestStreamMessageError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		class aiprovider.ErrorClass
	}{
		{"レート制限", `{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit_exceeded"}}`, aiprovider.ErrClassTransient},
		{"残高不足", `{"error":{"message":"quota","type":"insufficient_quota","code":"insufficient_quota"}}`, aiprovider.ErrClassConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(tc.body))
			})
			ch, err := a.StreamMessage(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("開始に失敗: %v", err)
			}
			events := collect(t, ch)
			last := events[len(events)-1]
			if last.Kind != aiprovider.EventError || last.Err == nil {
				t.Fatalf("EventError で閉じていない: %+v", events)
			}
			if last.Err.Class != tc.class {
				t.Errorf("区分が違う: %v（期待 %v）", last.Err.Class, tc.class)
			}
		})
	}
}

// 縮退時は推論努力パラメータを送信から省略する。
func TestStreamMessageDegraded(t *testing.T) {
	var body map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "response.completed", respCompleted)
	})

	req := testRequest()
	params, degraded := req.Effort.Degrade(aiprovider.ModelInfo{ID: "gpt-test", SupportsReasoning: false})
	if !degraded {
		t.Fatal("縮退判定が働いていない")
	}
	req.Effort = params
	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	if _, ok := body["reasoning"]; ok {
		t.Errorf("縮退時に推論努力パラメータが送信されている: %v", body["reasoning"])
	}
}

// キー取得に失敗した場合はチャネルを作らずエラーを返す。
func TestStreamMessageKeyError(t *testing.T) {
	a := New(stubKeys{err: errKeyNotSet}, "openai/test")
	if _, err := a.StreamMessage(context.Background(), testRequest()); err == nil {
		t.Fatal("キー未設定でもエラーにならない")
	}
}

// 構造化出力: ResponseSchema を text.format の json_schema へ写す。
func TestStreamMessageStructuredOutput(t *testing.T) {
	var body map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "response.completed", respCompleted)
	})

	req := testRequest()
	req.ResponseSchema = json.RawMessage(`{"type":"object","properties":{"decisions":{"type":"array"}}}`)
	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text が送られていない: %v", body)
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format が送られていない: %v", text)
	}
	if got := format["type"]; got != "json_schema" {
		t.Errorf("format.type = %v, want json_schema", got)
	}
	if got, _ := format["name"].(string); got == "" {
		t.Errorf("format.name が空（Responses API の必須項目）: %v", format)
	}
	schema, ok := format["schema"].(map[string]any)
	if !ok {
		t.Fatalf("format.schema が送られていない: %v", format)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props["decisions"] == nil {
		t.Errorf("スキーマの内容が渡っていない: %v", schema)
	}
}

// 非構造化（ResponseSchema なし）では構造化出力の指定を一切送らない。
func TestStreamMessageWithoutStructuredOutput(t *testing.T) {
	var body map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "response.completed", respCompleted)
	})

	ch, err := a.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	if text, ok := body["text"].(map[string]any); ok {
		if _, has := text["format"]; has {
			t.Errorf("非構造化なのに text.format が送信されている: %v", text)
		}
	}
}

// 壊れたスキーマは送信前に入力不正として弾く（ErrClassPermanent）。
func TestStreamMessageInvalidSchema(t *testing.T) {
	called := false
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		sseHeader(w)
	})

	req := testRequest()
	req.ResponseSchema = json.RawMessage(`{ここは JSON ではない`)
	_, err := a.StreamMessage(context.Background(), req)
	if err == nil {
		t.Fatal("不正なスキーマでエラーにならない")
	}
	var pErr *aiprovider.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("ProviderError ではない: %T %v", err, err)
	}
	if pErr.Class != aiprovider.ErrClassPermanent {
		t.Errorf("Class = %v, want ErrClassPermanent", pErr.Class)
	}
	if called {
		t.Error("入力不正なのに API を呼び出している")
	}
}
