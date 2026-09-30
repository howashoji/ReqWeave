//go:build integration

// 結合テスト（アダプタ × HTTP の SSE）。実キー・実 API は使わない。

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// writeSSE は 1 イベントを送出して即座に flush する。
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

const (
	msgStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":1}}}`
	blkStart = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	msgDelta = `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":23}}`
	msgStop  = `{"type":"message_stop"}`
)

func textDelta(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, b)
}

// collect はチャネルを読み切る（呼び出し側の契約 = EventDone / EventError まで読む）。
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
		Model:    "claude-test",
		System:   "システムプロンプト",
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "こんにちは"}},
		Effort:   aiprovider.MapEffort(aiprovider.ProviderAnthropic, aiprovider.EffortStandard),
	}
}

// 送信パラメータと SSE の型付きイベントの変換。
// EventUsage は EventDone の直前に 1 回。
func TestStreamMessage(t *testing.T) {
	var body map[string]any
	var gotAuth string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "message_start", msgStart)
		writeSSE(t, w, "content_block_start", blkStart)
		writeSSE(t, w, "content_block_delta", textDelta("こん"))
		writeSSE(t, w, "content_block_delta", textDelta("にちは"))
		writeSSE(t, w, "message_delta", msgDelta)
		writeSSE(t, w, "message_stop", msgStop)
	})

	ch, err := a.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collect(t, ch)

	if gotAuth != dummyKey {
		t.Errorf("x-api-key ヘッダが違う: %q", gotAuth)
	}
	if body["system"] == nil {
		t.Errorf("システムプロンプトが送られていない: %v", body)
	}
	if got := body["max_tokens"]; got != float64(8192) {
		t.Errorf("最大出力トークンが写像値でない: %v", got)
	}
	oc, ok := body["output_config"].(map[string]any)
	if !ok || oc["effort"] != aiprovider.ReasoningMedium {
		t.Errorf("推論努力レベルが送られていない: %v", body["output_config"])
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
	if u == nil || u.InputTokens != 11 || u.OutputTokens != 23 {
		t.Errorf("実績値が違う: %+v", u)
	}
	if events[3].Interrupted {
		t.Error("中断でないのに Interrupted が立っている")
	}
}

// 本層でバッファ・集約せず、応答完了前に最初の差分を受け取れる。
// ctx キャンセルは Interrupted 付き EventDone で閉じ、受信済み本文を失わない。
func TestStreamMessageInterrupt(t *testing.T) {
	release := make(chan struct{})
	// ハンドラの解放はサーバ停止（newTestAdapter の Cleanup）より先に行う必要があるため defer で閉じる。
	defer close(release)
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		sseHeader(w)
		writeSSE(t, w, "message_start", msgStart)
		writeSSE(t, w, "content_block_start", blkStart)
		writeSSE(t, w, "content_block_delta", textDelta("途中まで"))
		<-release // 応答を完了させない
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := a.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}

	// 応答完了前に最初の差分が届くこと（集約していない）。
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
	// 受信済みの入力トークン実績は保持して送出する。
	var sawUsage bool
	for _, ev := range events {
		if ev.Kind == aiprovider.EventUsage && ev.Usage.InputTokens == 11 {
			sawUsage = true
		}
	}
	if !sawUsage {
		t.Errorf("中断時に受信済み実績が送出されていない: %+v", events)
	}
}

// HTTP エラーは ProviderError へ正規化して EventError で通知する。
func TestStreamMessageError(t *testing.T) {
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limited"}}`))
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
	if last.Err.Class != aiprovider.ErrClassTransient {
		t.Errorf("レート制限が一時的エラーに分類されていない: %+v", last.Err)
	}
	if last.Err.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("ステータスが保持されていない: %+v", last.Err)
	}
}

// 推論努力パラメータ非対応モデルでは当該パラメータを送信から省略する。
func TestStreamMessageDegraded(t *testing.T) {
	var body map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "message_start", msgStart)
		writeSSE(t, w, "message_stop", msgStop)
	})

	req := testRequest()
	params, degraded := req.Effort.Degrade(aiprovider.ModelInfo{ID: "claude-test", SupportsReasoning: false})
	if !degraded {
		t.Fatal("縮退判定が働いていない")
	}
	req.Effort = params
	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	if _, ok := body["output_config"]; ok {
		t.Errorf("縮退時に推論努力パラメータが送信されている: %v", body["output_config"])
	}
	if got := body["max_tokens"]; got != float64(8192) {
		t.Errorf("縮退後も最大出力トークンで段階差を保つこと: %v", got)
	}
}

// キー取得に失敗した場合はチャネルを作らずエラーを返す。
func TestStreamMessageKeyError(t *testing.T) {
	a := New(stubKeys{err: errKeyNotSet}, "anthropic/test")
	if _, err := a.StreamMessage(context.Background(), testRequest()); err == nil {
		t.Fatal("キー未設定でもエラーにならない")
	}
}

// 構造化出力: ResponseSchema を output_config.format へ写す。
func TestStreamMessageStructuredOutput(t *testing.T) {
	var body map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeSSE(t, w, "message_start", msgStart)
		writeSSE(t, w, "message_delta", msgDelta)
		writeSSE(t, w, "message_stop", msgStop)
	})

	req := testRequest()
	req.ResponseSchema = json.RawMessage(`{"type":"object","properties":{"decisions":{"type":"array"}}}`)
	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	oc, ok := body["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config が送られていない: %v", body)
	}
	format, ok := oc["format"].(map[string]any)
	if !ok {
		t.Fatalf("output_config.format が送られていない: %v", oc)
	}
	if got := format["type"]; got != "json_schema" {
		t.Errorf("format.type = %v, want json_schema", got)
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
		writeSSE(t, w, "message_start", msgStart)
		writeSSE(t, w, "message_delta", msgDelta)
		writeSSE(t, w, "message_stop", msgStop)
	})

	ch, err := a.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	if oc, ok := body["output_config"].(map[string]any); ok {
		if _, has := oc["format"]; has {
			t.Errorf("非構造化なのに output_config.format が送信されている: %v", oc)
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
	if _, err := a.StreamMessage(context.Background(), req); err == nil {
		t.Fatal("不正なスキーマでエラーにならない")
	} else {
		var pErr *aiprovider.ProviderError
		if !asProviderError(err, &pErr) {
			t.Fatalf("ProviderError ではない: %T %v", err, err)
		}
		if pErr.Class != aiprovider.ErrClassPermanent {
			t.Errorf("Class = %v, want ErrClassPermanent", pErr.Class)
		}
	}
	if called {
		t.Error("入力不正なのに API を呼び出している")
	}
}
