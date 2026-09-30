//go:build integration

package codex

// 結合テスト（アダプタ × **実バイナリの Codex App Server**）。
//
// 単体テストは偽の Codex を相手に本システム側の writing を確かめる。ここでは**実バイナリ**を相手に、
// 次の「実バイナリの性質に依存する部分」を確かめる（これが崩れると起動後の検査で毎回止まる）。
//
//   - 起動引数どおりの実効値が `config/read` に現れ、起動後の検査に合格する
//   - 使い捨てスレッド 1 つ・ターン 1 つで対話が成立し、トークン実績が届く
//   - 要求本文に、置き換えた指示文だけが載り、利用者ホームの skill の一覧・端末の環境が載らない
//   - モデルへ渡る道具が 4 種のまま増えていない（画像の読み取りは手元のモデル定義が拒否する）
//   - 終了後に一時領域がフォルダごと消える
//
// **本物の OpenAI へは接続しない**（モデルの呼び出しは 127.0.0.1 の偽 Responses API、
// それ以外の外部接続は届かないプロキシへ向ける）。認証はダミーのキー（実キーは使わない）。
//
// 実バイナリは**同梱する版そのもの**（`make codex` が用意する build/codex/codex）を使う。
// 用意されていなければ失敗する（黙って飛ばさない）。別の実行ファイルで確かめたいときは
// 環境変数 REQWEAVE_CODEX_BIN で差し替える。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// defaultCodexBinary は `make codex` が用意する同梱物（本パッケージからの相対）。
const defaultCodexBinary = "../../../../build/codex/codex"

// realCodexEnv は実バイナリと偽 Responses API を用意する。
type realCodexEnv struct {
	base     string
	server   *httptest.Server
	mu       sync.Mutex
	requests []map[string]any
}

func newRealCodexEnv(t *testing.T) *realCodexEnv {
	t.Helper()
	binary := os.Getenv("REQWEAVE_CODEX_BIN")
	if binary == "" {
		binary = defaultCodexBinary
	}
	// **絶対パスにする**（子プロセスの作業フォルダは一時領域であり、相対パスでは解決できない）。
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("同梱物の場所を解決できません: %v", err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("同梱する版の Codex がありません（`make codex` で用意する。別の実行ファイルを使うときは "+
			"REQWEAVE_CODEX_BIN で指定する）: %v", err)
	}

	env := &realCodexEnv{base: t.TempDir()}
	env.server = httptest.NewServer(http.HandlerFunc(env.handle))

	// 外部接続の受け皿（誰も待ち受けていないポート）。モデルの呼び出し以外が外へ出ないことの担保。
	deadProxy := deadAddress(t)

	origLocation, origExe, origEnviron := workspaceLocationFn, executablePath, environFn
	origBase := baseURLSecretKey
	workspaceLocationFn = func() (workspacePaths, error) {
		return workspacePaths{
			root: filepath.Join(env.base, "codex"),
			lock: filepath.Join(env.base, "codex.lock"),
		}, nil
	}
	executablePath = func() (string, error) { return binary, nil }
	environFn = func() []string {
		return []string{
			"HOME=" + os.Getenv("HOME"), // keyring と skill の検出に要る（差し替えると macOS で keyring が使えなくなる）
			"HTTPS_PROXY=http://" + deadProxy,
			"NO_PROXY=127.0.0.1,localhost",
		}
	}
	baseURLSecretKey = env.server.URL

	t.Cleanup(func() {
		_ = procManager.stopCurrent("テストの終了")
		workspaceLocationFn, executablePath, environFn = origLocation, origExe, origEnviron
		baseURLSecretKey = origBase
		env.server.Close()
	})
	return env
}

// deadAddress は誰も待ち受けていないアドレスを返す。
func deadAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("アドレスを確保できない: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

// handle は偽の Responses API（要求本文を記録し、ストリーミングの応答を返す）。
func (e *realCodexEnv) handle(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.requests = append(e.requests, body)
		e.mu.Unlock()
	}
	if !strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "responses") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	text := "承知しました。拠点ごとの在庫を確認します。"
	if format, ok := lookupPath(body, "text.format.type"); ok && format == "json_schema" {
		text = `{"answer":"ok","note":null}`
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	message := map[string]any{
		"type": "message", "role": "assistant", "id": "msg_1", "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_1"}},
		{"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"type": "message", "role": "assistant", "id": "msg_1",
				"status": "in_progress", "content": []any{}}},
		{"type": "response.output_text.delta", "item_id": "msg_1", "output_index": 0,
			"content_index": 0, "delta": text},
		{"type": "response.output_item.done", "output_index": 0, "item": message},
		{"type": "response.completed", "response": map[string]any{"id": "resp_1", "usage": map[string]any{
			"input_tokens": 1234, "input_tokens_details": map[string]any{"cached_tokens": 100},
			"output_tokens": 56, "output_tokens_details": map[string]any{"reasoning_tokens": 7},
			"total_tokens": 1290,
		}}},
	}
	for _, ev := range events {
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (e *realCodexEnv) lastRequest(t *testing.T) map[string]any {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.requests) - 1; i >= 0; i-- {
		if _, ok := e.requests[i]["input"]; ok {
			return e.requests[i]
		}
	}
	t.Fatal("モデルへの要求が 1 件も届いていない")
	return nil
}

func realCodexRequest() aiprovider.ChatRequest {
	return aiprovider.ChatRequest{
		Model:  "gpt-5.5",
		System: "あなたは要件定義を支援するアシスタントです。日本語で答えてください。",
		Messages: []aiprovider.Message{
			{Role: aiprovider.RoleUser, Content: "在庫管理の要件を詰めたい。"},
			{Role: aiprovider.RoleAssistant, Content: "拠点数を教えてください。"},
			{Role: aiprovider.RoleUser, Content: "拠点は 3 つです。"},
		},
		Effort: aiprovider.MapEffort(ProviderCodex, aiprovider.EffortStandard),
	}
}

// 実バイナリで起動後の検査に合格し、対話が成立する。
func TestRealCodexPassesGuardAndCompletesTurn(t *testing.T) {
	env := newRealCodexEnv(t)
	adapter := NewWithOptions(stubKeys{key: dummyKey}, "codex/結合テスト", aiprovider.AdapterOptions{})

	ch, err := adapter.StreamMessage(context.Background(), realCodexRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	events := collect(t, ch)
	last := lastEvent(t, events)
	if last.Kind != aiprovider.EventError {
		// 期待どおり（正常終了）。エラーのときは下で内容を出す。
	}
	if last.Kind != aiprovider.EventDone {
		t.Fatalf("正常終了していない: %+v（エラー: %+v）", last, last.Err)
	}
	if got := textOf(events); !strings.Contains(got, "在庫") {
		t.Errorf("応答の本文が届いていない: %q", got)
	}
	if last.Usage == nil || last.Usage.InputTokens != 1234 || last.Usage.OutputTokens != 56 {
		t.Errorf("トークン実績が届いていない: %+v", last.Usage)
	}

	// 要求本文の確認（版を上げるときの観測と同じ方法）
	request := env.lastRequest(t)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("要求本文を読めない: %v", err)
	}
	text := string(body)

	instructions, _ := request["instructions"].(string)
	if instructions != "あなたは要件定義を支援するアシスタントです。日本語で答えてください。" {
		t.Errorf("指示文が置き換わっていない（%d 文字）", len(instructions))
	}
	for _, forbidden := range []string{
		".agents",                  // 利用者ホームの skill の一覧
		"skills_instructions",      // 同梱 skill の一覧
		"environment_context",      // 端末の環境
		"permissions instructions", // 権限の説明
		"input_image",              // 作業フォルダ外の画像
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("要求本文に %q が含まれている", forbidden)
		}
	}
	// 履歴が Responses の項目として載り、最後の発話が続く
	for _, want := range []string{"在庫管理の要件を詰めたい。", "拠点数を教えてください。", "拠点は 3 つです。"} {
		if !strings.Contains(text, want) {
			t.Errorf("要求本文に %q が無い", want)
		}
	}
	// モデルへ渡る道具は 4 種のまま（増えていないこと）
	tools := toolNames(request)
	allowed := map[string]bool{"update_plan": true, "request_user_input": true, "apply_patch": true, "view_image": true}
	for _, name := range tools {
		if !allowed[name] {
			t.Errorf("想定外の道具がモデルへ渡っている: %q（全体: %v）", name, tools)
		}
	}
	if len(tools) > len(allowed) {
		t.Errorf("道具の種類が増えている: %v", tools)
	}
	// 推論努力が写っていること
	if effort, ok := lookupPath(request, "reasoning.effort"); ok && effort != "medium" {
		t.Errorf("推論努力が違う: %v", effort)
	}
}

func toolNames(request map[string]any) []string {
	list, _ := request["tools"].([]any)
	var out []string
	for _, item := range list {
		tool, _ := item.(map[string]any)
		name, _ := tool["name"].(string)
		if name == "" {
			name, _ = tool["type"].(string)
		}
		out = append(out, name)
	}
	return out
}

// 実バイナリでも構造化出力が strict で通り、変換で加えた null が取り除かれる。
func TestRealCodexStructuredOutput(t *testing.T) {
	newRealCodexEnv(t)
	adapter := NewWithOptions(stubKeys{key: dummyKey}, "codex/結合テスト", aiprovider.AdapterOptions{})

	req := realCodexRequest()
	req.ResponseSchema = json.RawMessage(`{
	  "type": "object", "additionalProperties": false,
	  "required": ["answer"],
	  "properties": {"answer": {"type": "string"}, "note": {"type": ["string", "null"]}}
	}`)
	ch, err := adapter.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	events := collect(t, ch)
	if last := lastEvent(t, events); last.Kind != aiprovider.EventDone {
		t.Fatalf("正常終了していない: %+v（エラー: %+v）", last, last.Err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(textOf(events)), &got); err != nil {
		t.Fatalf("応答が JSON でない: %v（%q）", err, textOf(events))
	}
	if _, ok := got["note"]; ok {
		t.Errorf("変換で加えたプロパティの null が残っている: %+v", got)
	}
}

// 子プロセスを止めると一時領域がフォルダごと消える。
func TestRealCodexRemovesWorkspaceOnStop(t *testing.T) {
	env := newRealCodexEnv(t)
	adapter := NewWithOptions(stubKeys{key: dummyKey}, "codex/結合テスト", aiprovider.AdapterOptions{})

	ch, err := adapter.StreamMessage(context.Background(), realCodexRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	collect(t, ch)

	root := filepath.Join(env.base, "codex")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("実行中に一時領域が無い: %v", err)
	}
	// Codex は一時領域に自分の記録（動作ログ・状態）を作る＝消す対象がある状態で止める。
	entries, err := os.ReadDir(filepath.Join(root, "home"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("Codex の一時領域に何も作られていない（前提が崩れている）: %v", err)
	}

	if err := procManager.stopCurrent("テストの確認"); err != nil {
		t.Fatalf("子プロセスを止められない: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("一時領域が残っている: %v", err)
	}
	// 利用者の Codex 設定（~/.codex）へは触れていないこと
	if home := os.Getenv("HOME"); home != "" {
		if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); err == nil {
			// 既に存在する環境もあるため、ここでは内容を変えていないことまでは確かめない。
			t.Log("利用者の ~/.codex/config.toml は存在する（本テストは触れていない）")
		}
	}
}
