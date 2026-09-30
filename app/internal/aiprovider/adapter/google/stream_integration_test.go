//go:build integration

// 結合テスト（アダプタ × HTTP の SSE）。実キー・実 API は使わない。

package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// writeChunk は alt=sse の 1 チャンク（data 行のみ）を送出して即座に flush する。
func writeChunk(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

func sseHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
}

func textChunk(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf(`{"candidates":[{"content":{"role":"model","parts":[{"text":%s}]},"index":0}]}`, b)
}

const finalChunk = `{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":23,"thoughtsTokenCount":5,"totalTokenCount":39}}`

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
		Model:    "gemini-test",
		System:   "システムプロンプト",
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: "こんにちは"}},
		Effort:   aiprovider.MapEffort(aiprovider.ProviderGoogle, aiprovider.EffortStandard),
	}
}

// GenerateContentResponse のチャンク列を StreamEvent へ変換する。
// EventUsage は EventDone の直前に 1 回（推論トークンを含む）。
func TestStreamMessage(t *testing.T) {
	var body map[string]any
	var gotKey, gotPath, gotQuery string
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeChunk(t, w, textChunk("こん"))
		writeChunk(t, w, textChunk("にちは"))
		writeChunk(t, w, finalChunk)
	})

	ch, err := a.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collect(t, ch)

	if gotKey != dummyKey {
		t.Errorf("x-goog-api-key ヘッダが違う: %q", gotKey)
	}
	if !strings.Contains(gotPath, "streamGenerateContent") {
		t.Errorf("エンドポイントが違う: %q", gotPath)
	}
	if !strings.Contains(gotQuery, "alt=sse") {
		t.Errorf("SSE 指定が無い: %q", gotQuery)
	}
	// キーをクエリパラメータへ載せない（キーを URL に載せるとログに残りうるため）
	if strings.Contains(gotQuery, "key=") || strings.Contains(gotQuery, dummyKey) {
		t.Errorf("キーがクエリパラメータに載っている: %q", gotQuery)
	}
	if body["systemInstruction"] == nil {
		t.Errorf("システムプロンプトが送られていない: %v", body)
	}
	gen, _ := body["generationConfig"].(map[string]any)
	if gen == nil {
		t.Fatalf("生成設定が送られていない: %v", body)
	}
	if got := gen["maxOutputTokens"]; got != float64(8192) {
		t.Errorf("最大出力トークンが写像値でない: %v", got)
	}
	thinking, ok := gen["thinkingConfig"].(map[string]any)
	if !ok || thinking["thinkingLevel"] != "MEDIUM" {
		t.Errorf("推論努力レベルが送られていない: %v", gen["thinkingConfig"])
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
	if len(kinds) < 2 || kinds[len(kinds)-2] != aiprovider.EventUsage || kinds[len(kinds)-1] != aiprovider.EventDone {
		t.Fatalf("イベント列が違う: %v", kinds)
	}
	u := events[len(events)-2].Usage
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
		writeChunk(t, w, textChunk("途中まで"))
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

// RESOURCE_EXHAUSTED(429) は一時的、PERMISSION_DENIED は設定起因。
func TestStreamMessageError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		class  aiprovider.ErrorClass
	}{
		{"レート制限", http.StatusTooManyRequests, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`, aiprovider.ErrClassTransient},
		{"権限不足", http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"denied"}}`, aiprovider.ErrClassConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
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
		writeChunk(t, w, finalChunk)
	})

	req := testRequest()
	params, degraded := req.Effort.Degrade(aiprovider.ModelInfo{ID: "gemini-test", SupportsReasoning: false})
	if !degraded {
		t.Fatal("縮退判定が働いていない")
	}
	req.Effort = params
	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	gen, _ := body["generationConfig"].(map[string]any)
	if gen != nil {
		if _, ok := gen["thinkingConfig"]; ok {
			t.Errorf("縮退時に推論努力パラメータが送信されている: %v", gen["thinkingConfig"])
		}
	}
}

// キー取得に失敗した場合はチャネルを作らずエラーを返す。
func TestStreamMessageKeyError(t *testing.T) {
	a := New(stubKeys{err: errKeyNotSet}, "google/test")
	if _, err := a.StreamMessage(context.Background(), testRequest()); err == nil {
		t.Fatal("キー未設定でもエラーにならない")
	}
}

// 構造化出力: ResponseSchema を responseMimeType + responseJsonSchema へ写す。
func TestStreamMessageStructuredOutput(t *testing.T) {
	var body map[string]any
	a := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		sseHeader(w)
		writeChunk(t, w, finalChunk)
	})

	req := testRequest()
	req.ResponseSchema = json.RawMessage(`{"type":"object","properties":{"decisions":{"type":"array"}}}`)
	ch, err := a.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	cfg, ok := body["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig が送られていない: %v", body)
	}
	if got := cfg["responseMimeType"]; got != "application/json" {
		t.Errorf("responseMimeType = %v, want application/json", got)
	}
	schema, ok := cfg["responseJsonSchema"].(map[string]any)
	if !ok {
		t.Fatalf("responseJsonSchema が送られていない: %v", cfg)
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
		writeChunk(t, w, finalChunk)
	})

	ch, err := a.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collect(t, ch)

	cfg, _ := body["generationConfig"].(map[string]any)
	if _, has := cfg["responseMimeType"]; has {
		t.Errorf("非構造化なのに responseMimeType が送信されている: %v", cfg)
	}
	if _, has := cfg["responseJsonSchema"]; has {
		t.Errorf("非構造化なのに responseJsonSchema が送信されている: %v", cfg)
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
