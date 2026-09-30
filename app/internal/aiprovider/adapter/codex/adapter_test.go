package codex

// 単体テスト（本システム側の writing）。子プロセスには偽の Codex（fake_codex_test.go）を使う。
// 実バイナリでの確認は adapter_integration_test.go。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// fakeEnv は 1 つのテストで使う偽の子プロセスの環境。
type fakeEnv struct {
	base string // 一時領域の親（scenario.json / rpc.jsonl を置く）
}

// newFakeEnv は一時領域の場所・実行ファイル・環境を差し替え、指示書を書き出す。
func newFakeEnv(t *testing.T, scenario fakeScenario) *fakeEnv {
	t.Helper()
	base := t.TempDir()
	body, err := json.Marshal(scenario)
	if err != nil {
		t.Fatalf("指示書を組み立てられない: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "scenario.json"), body, 0o600); err != nil {
		t.Fatalf("指示書を書けない: %v", err)
	}

	origLocation, origExe, origEnviron := workspaceLocationFn, executablePath, environFn
	workspaceLocationFn = func() (workspacePaths, error) {
		return workspacePaths{root: filepath.Join(base, "codex"), lock: filepath.Join(base, "codex.lock")}, nil
	}
	executablePath = func() (string, error) { return os.Args[0], nil }
	environFn = func() []string { return []string{"HOME=" + base} }
	t.Cleanup(func() {
		_ = procManager.stopCurrent("テストの終了")
		workspaceLocationFn, executablePath, environFn = origLocation, origExe, origEnviron
	})
	return &fakeEnv{base: base}
}

// received は fake が受け取ったメッセージを返す。
func (e *fakeEnv) received(t *testing.T) []rpcMessage {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(e.base, "rpc.jsonl"))
	if err != nil {
		return nil
	}
	var out []rpcMessage
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("記録を解釈できない: %v", err)
		}
		out = append(out, msg)
	}
	return out
}

// paramsOf は最初に見つかった当該メソッドのパラメータを返す。
func paramsOf(t *testing.T, messages []rpcMessage, method string) map[string]any {
	t.Helper()
	for _, msg := range messages {
		if msg.Method == method {
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("%s のパラメータを解釈できない: %v", method, err)
			}
			return params
		}
	}
	t.Fatalf("%s が送られていない", method)
	return nil
}

func countOf(messages []rpcMessage, method string) int {
	n := 0
	for _, msg := range messages {
		if msg.Method == method {
			n++
		}
	}
	return n
}

// stubKeys は疎通確認・起動時のキー取得元（ダミーキー。実キーは使わない）。
type stubKeys struct {
	key string
	err error
}

func (s stubKeys) SecretKey(context.Context, aiprovider.KeyRef) (string, error) {
	return s.key, s.err
}

const dummyKey = "sk-test-DUMMY-0000000000"

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	return NewWithOptions(stubKeys{key: dummyKey}, "codex/テスト", aiprovider.AdapterOptions{})
}

func testRequest() aiprovider.ChatRequest {
	return aiprovider.ChatRequest{
		Model:  "gpt-5.5",
		System: "あなたは要件定義を支援します。",
		Messages: []aiprovider.Message{
			{Role: aiprovider.RoleUser, Content: "在庫管理の要件を詰めたい。"},
			{Role: aiprovider.RoleAssistant, Content: "拠点数を教えてください。"},
			{Role: aiprovider.RoleUser, Content: "拠点は 3 つです。"},
		},
		Effort: aiprovider.MapEffort(ProviderCodex, aiprovider.EffortStandard),
	}
}

// collect はチャネルを読み切る（終了イベントが来ない場合は失敗させる）。
func collect(t *testing.T, ch <-chan aiprovider.StreamEvent) []aiprovider.StreamEvent {
	t.Helper()
	var out []aiprovider.StreamEvent
	timeout := time.After(20 * time.Second)
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

func lastEvent(t *testing.T, events []aiprovider.StreamEvent) aiprovider.StreamEvent {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("イベントが 1 件も来ていない")
	}
	return events[len(events)-1]
}

func textOf(events []aiprovider.StreamEvent) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Kind == aiprovider.EventTextDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

// 1 回の呼び出しが使い捨てスレッド 1 つ・ターン 1 つに写り、
// 本文差分・トークン実績・完了がイベントへ変換される。
func TestStreamMessageMapsCallToEphemeralThreadAndTurn(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Text: "承知しました。拠点ごとの在庫を確認します。", Deltas: 3}})
	adapter := newTestAdapter(t)

	ch, err := adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	events := collect(t, ch)

	if got := textOf(events); got != "承知しました。拠点ごとの在庫を確認します。" {
		t.Errorf("本文が違う: %q", got)
	}
	if n := countKind(events, aiprovider.EventTextDelta); n != 3 {
		t.Errorf("本文差分の件数 = %d, want 3（本層で集約しない）", n)
	}
	done := lastEvent(t, events)
	if done.Kind != aiprovider.EventDone || done.Interrupted {
		t.Fatalf("最後のイベントが正常終了でない: %+v", done)
	}
	if done.Usage == nil || done.Usage.InputTokens != 1234 || done.Usage.OutputTokens != 56 || done.Usage.ReasoningTokens != 7 {
		t.Errorf("トークン実績が違う: %+v", done.Usage)
	}

	messages := env.received(t)
	thread := paramsOf(t, messages, "thread/start")
	if thread["ephemeral"] != true {
		t.Error("使い捨てのスレッドになっていない（会話の全文記録が残る）")
	}
	if thread["sandbox"] != "read-only" || thread["approvalPolicy"] != "never" {
		t.Errorf("スレッドの権限設定が違う: %+v", thread)
	}
	if thread["baseInstructions"] != "あなたは要件定義を支援します。" {
		t.Errorf("指示文が置き換わっていない: %v", thread["baseInstructions"])
	}
	if cwd, _ := thread["cwd"].(string); !strings.HasSuffix(cwd, filepath.Join("codex", "work")) {
		t.Errorf("作業フォルダが一時領域でない: %v", thread["cwd"])
	}
	// 履歴は inject_items、最後の利用者の発話だけが turn/start。
	inject := paramsOf(t, messages, "thread/inject_items")
	items, _ := inject["items"].([]any)
	if len(items) != 2 {
		t.Errorf("履歴の件数 = %d, want 2", len(items))
	}
	turn := paramsOf(t, messages, "turn/start")
	input, _ := turn["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("ターンの入力が 1 件でない: %+v", turn["input"])
	}
	first, _ := input[0].(map[string]any)
	if first["text"] != "拠点は 3 つです。" {
		t.Errorf("ターンの入力が最後の利用者の発話でない: %v", first["text"])
	}
	if turn["effort"] != "medium" {
		t.Errorf("推論努力が写っていない: %v", turn["effort"])
	}
	// 最大出力トークンは送らない（turn/start に指定が無い）。
	for _, key := range []string{"maxOutputTokens", "max_output_tokens"} {
		if _, ok := turn[key]; ok {
			t.Errorf("最大出力トークンを送っている: %v", turn[key])
		}
	}
}

func countKind(events []aiprovider.StreamEvent, kind aiprovider.EventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// 構造化出力は完了時に 1 回で返し、変換で加えた null を取り除く。
func TestStreamMessageStructuredOutputStripsAddedNulls(t *testing.T) {
	newFakeEnv(t, fakeScenario{Turn: fakeTurn{Text: `{"answer":"ok","note":null}`}})
	adapter := newTestAdapter(t)

	req := testRequest()
	req.ResponseSchema = json.RawMessage(`{
	  "type": "object", "additionalProperties": false,
	  "required": ["answer"],
	  "properties": {
	    "answer": {"type": "string"},
	    "note": {"type": ["string", "null"]}
	  }
	}`)
	ch, err := adapter.StreamMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	events := collect(t, ch)

	if n := countKind(events, aiprovider.EventTextDelta); n != 1 {
		t.Errorf("構造化出力の本文が %d 回に分かれた（完了時に 1 回で返す）", n)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(textOf(events)), &body); err != nil {
		t.Fatalf("応答が JSON でない: %v（%q）", err, textOf(events))
	}
	if _, ok := body["note"]; ok {
		t.Errorf("変換で加えたプロパティの null が残っている: %+v", body)
	}
	if body["answer"] != "ok" {
		t.Errorf("本文が変わっている: %+v", body)
	}
	if lastEvent(t, events).Kind != aiprovider.EventDone {
		t.Errorf("正常終了で閉じていない: %+v", lastEvent(t, events))
	}
}

// 送る前に分かる不正は送信せずに恒久的エラーで返す。
func TestStreamMessageRejectsInvalidRequests(t *testing.T) {
	newFakeEnv(t, fakeScenario{})
	adapter := newTestAdapter(t)

	cases := []struct {
		name string
		req  func() aiprovider.ChatRequest
		code string
	}{
		{"モデル未指定", func() aiprovider.ChatRequest {
			r := testRequest()
			r.Model = ""
			return r
		}, codeInvalidRequest},
		{"メッセージ無し", func() aiprovider.ChatRequest {
			r := testRequest()
			r.Messages = nil
			return r
		}, codeInvalidRequest},
		{"最後が AI の発話", func() aiprovider.ChatRequest {
			r := testRequest()
			r.Messages = append(r.Messages, aiprovider.Message{Role: aiprovider.RoleAssistant, Content: "はい"})
			return r
		}, codeInvalidRequest},
		{"追加プロパティを許すスキーマ", func() aiprovider.ChatRequest {
			r := testRequest()
			r.ResponseSchema = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)
			return r
		}, codeInvalidSchema},
		{"JSON でないスキーマ", func() aiprovider.ChatRequest {
			r := testRequest()
			r.ResponseSchema = json.RawMessage(`{`)
			return r
		}, codeInvalidSchema},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch, err := adapter.StreamMessage(context.Background(), c.req())
			if ch != nil {
				t.Fatal("不正なリクエストで送信が始まった")
			}
			var pErr *aiprovider.ProviderError
			if !errors.As(err, &pErr) {
				t.Fatalf("正規化エラーでない: %v", err)
			}
			if pErr.Class != aiprovider.ErrClassPermanent || pErr.Code != c.code {
				t.Errorf("分類・Code が違う: %v / %q", pErr.Class, pErr.Code)
			}
		})
	}
}

// 起動後の検査に 1 つでも落ちたら AI 呼び出しを行わず、子プロセスを止める。
func TestGuardFailuresBlockTheCall(t *testing.T) {
	cases := []struct {
		name     string
		scenario fakeScenario
	}{
		{"版が同梱と違う", fakeScenario{Version: "0.999.0"}},
		{"抑止設定が実効値に無い", fakeScenario{DropConfigKeys: []string{"web_search"}}},
		{"道具の無効化が効いていない", fakeScenario{DropConfigKeys: []string{"features.browser_use"}}},
		{"認証の種類が違う", fakeScenario{AccountType: "chatgpt"}},
		{"未認証のまま", fakeScenario{AccountType: "none"}},
		{"利用者の skill を無効にできない", fakeScenario{
			Skills:           []fakeSkill{{Path: "/Users/tester/.agents/skills/x/SKILL.md", Enabled: true}},
			SkillsWriteFails: true,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv(t, c.scenario)
			adapter := newTestAdapter(t)

			ch, err := adapter.StreamMessage(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("送信を開始できない: %v", err)
			}
			events := collect(t, ch)
			last := lastEvent(t, events)
			if last.Kind != aiprovider.EventError {
				t.Fatalf("検査に落ちたのにエラーで終わっていない: %+v", last)
			}
			if last.Err.Class != aiprovider.ErrClassPermanent || last.Err.Code != codeGuardFailed {
				t.Errorf("分類・Code が違う: %v / %q（%s）", last.Err.Class, last.Err.Code, last.Err.Message)
			}
			// AI 呼び出し（ターン）を始めていないこと
			if n := countOf(env.received(t), "turn/start"); n != 0 {
				t.Errorf("検査に落ちたのに AI 呼び出しを行った（turn/start %d 回）", n)
			}
			// 子プロセスを止め、一時領域も残さないこと
			if procManager.current() != nil {
				t.Error("検査に落ちた子プロセスが動いたままになっている")
			}
			if _, err := os.Stat(filepath.Join(env.base, "codex")); !os.IsNotExist(err) {
				t.Errorf("一時領域が残っている: %v", err)
			}
		})
	}
}

// 起動後と毎回の thread/start の直前に skill を無効にし、0 件を確かめる。
func TestSkillsAreDisabledBeforeEveryThread(t *testing.T) {
	skills := []fakeSkill{
		{Path: "/Users/tester/.agents/skills/a/SKILL.md", Enabled: true},
		{Path: "/Users/tester/.agents/skills/b/SKILL.md", Enabled: true},
	}
	env := newFakeEnv(t, fakeScenario{Skills: skills})
	adapter := newTestAdapter(t)

	for i := 0; i < 2; i++ {
		ch, err := adapter.StreamMessage(context.Background(), testRequest())
		if err != nil {
			t.Fatalf("送信を開始できない: %v", err)
		}
		if last := lastEvent(t, collect(t, ch)); last.Kind != aiprovider.EventDone {
			t.Fatalf("%d 回目が正常に終わらない: %+v", i+1, last)
		}
	}
	messages := env.received(t)
	if n := countOf(messages, "skills/config/write"); n != len(skills) {
		t.Errorf("無効化の呼び出し = %d 回, want %d", n, len(skills))
	}
	// 起動後（2 回: 一覧→確認）＋ thread/start の直前に毎回 1 回以上
	if n := countOf(messages, "skills/list"); n < 4 {
		t.Errorf("skills/list = %d 回（起動後と毎回の thread/start の直前に呼ぶ）", n)
	}
	// 2 回目の呼び出しでも thread/start の前に確かめていること
	order := methodOrder(messages)
	threadIndexes := indexesOf(order, "thread/start")
	if len(threadIndexes) != 2 {
		t.Fatalf("thread/start の回数 = %d, want 2", len(threadIndexes))
	}
	if !hasBefore(order, "skills/list", threadIndexes[1], threadIndexes[0]) {
		t.Error("2 回目の thread/start の前に skill を確かめ直していない")
	}
}

func methodOrder(messages []rpcMessage) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Method)
	}
	return out
}

func indexesOf(order []string, method string) []int {
	var out []int
	for i, m := range order {
		if m == method {
			out = append(out, i)
		}
	}
	return out
}

// hasBefore は after より前・before より後に method があるかを返す。
func hasBefore(order []string, method string, before, after int) bool {
	for i := after + 1; i < before; i++ {
		if order[i] == method {
			return true
		}
	}
	return false
}

// 道具の項目・Codex からの要求を検知したらターンを止め、子プロセスも止める。
func TestToolAttemptStopsTurnAndProcess(t *testing.T) {
	cases := []struct {
		name     string
		scenario fakeScenario
	}{
		{"道具の項目が始まった", fakeScenario{Turn: fakeTurn{Kind: "tool", ItemType: "commandExecution"}}},
		{"画像の読み取りが始まった", fakeScenario{Turn: fakeTurn{Kind: "tool", ItemType: "imageView"}}},
		{"承認を求められた", fakeScenario{Turn: fakeTurn{Kind: "server_request"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv(t, c.scenario)
			adapter := newTestAdapter(t)

			ch, err := adapter.StreamMessage(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("送信を開始できない: %v", err)
			}
			last := lastEvent(t, collect(t, ch))
			if last.Kind != aiprovider.EventError {
				t.Fatalf("道具の検知でエラーになっていない: %+v", last)
			}
			if last.Err.Class != aiprovider.ErrClassPermanent || last.Err.Code != codeToolAttempt {
				t.Errorf("分類・Code が違う: %v / %q", last.Err.Class, last.Err.Code)
			}
			// 中断を送っていること
			if n := countOf(env.received(t), "turn/interrupt"); n == 0 {
				t.Error("ターンを止めていない")
			}
			waitFor(t, func() bool { return procManager.current() == nil }, "子プロセスが止まらない")
		})
	}
}

func waitFor(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error(message)
}

// 中断は turn/interrupt を送り、中断として閉じる。
func TestInterruptSendsTurnInterrupt(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang"}})
	adapter := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := adapter.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	// ターンが始まってから中断する
	waitFor(t, func() bool { return countOf(env.received(t), "turn/start") > 0 }, "ターンが始まらない")
	cancel()

	last := lastEvent(t, collect(t, ch))
	if last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("中断として閉じていない: %+v", last)
	}
	if n := countOf(env.received(t), "turn/interrupt"); n == 0 {
		t.Error("turn/interrupt を送っていない")
	}
}

// turn/start の応答より先に中断されても、応答を待ってターンの ID を得て turn/interrupt を送り、中断として閉じる。
// turn/start は送った時点で Codex 側のターンが始まりうるため、応答を待たずに諦めると、止めるべきターンが分からず、
// 中断が設定の誤りとして報告される（負荷の高いときに実際に起きた順序を、応答の保留で必ず作る）。
func TestInterruptBeforeTurnStartReply(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang", HoldStartReply: true}})
	adapter := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := adapter.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	waitFor(t, func() bool { return countOf(env.received(t), "turn/start") > 0 }, "turn/start が届かない")
	// fake は印を置くまで turn/start に応答しないので、中断は必ず応答より先になる。
	cancel()
	if err := os.WriteFile(filepath.Join(env.base, fakeReleaseStartReply), nil, 0o600); err != nil {
		t.Fatalf("応答の保留を解けない: %v", err)
	}

	last := lastEvent(t, collect(t, ch))
	if last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("中断として閉じていない: %+v", last)
	}
	if n := countOf(env.received(t), "turn/interrupt"); n == 0 {
		t.Fatal("始まったターンへ turn/interrupt を送っていない")
	}
	params := paramsOf(t, env.received(t), "turn/interrupt")
	if params["threadId"] != "thread-1" || params["turnId"] != "turn-1" {
		t.Errorf("turn/interrupt が始まったターンを指していない: %v", params)
	}
}

// 中断の後も turn/start の応答が上限まで来なければ、子プロセスを止めて中断として閉じる
// （ターンの ID が分からず turn/interrupt で止められないため、止める手段は子プロセスの停止しか無い）。
func TestInterruptWithoutTurnStartReplyStopsProcess(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang", HoldStartReply: true}})
	adapter := newTestAdapter(t)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := adapter.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	waitFor(t, func() bool { return countOf(env.received(t), "turn/start") > 0 }, "turn/start が届かない")
	// 待ちの上限は差し替えない（上限を読むゴルーチンがテストの終わりより後まで動くため、差し替えるとデータ競合になる）。
	cancel() // 解放の印は置かない＝ turn/start の応答は来ない

	last := lastEvent(t, collect(t, ch))
	if last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("中断として閉じていない: %+v", last)
	}
	waitFor(t, func() bool { return procManager.current() == nil }, "止められないターンを抱えた子プロセスが残っている")
}

// どの段階で中断しても中断として閉じ、ターンを始めない（設定の誤りや一時的なエラーにしない）。
// 各段階の要求への応答を止めておき、「応答より先に中断した」順序を時間ではなく構造で作る。
func TestInterruptAtEachStageClosesAsInterrupted(t *testing.T) {
	cases := []struct {
		name string
		hold fakeHold
	}{
		{"子プロセスの起動中", fakeHold{Method: "initialize"}},
		{"起動後の検査中", fakeHold{Method: "config/read"}},
		{"呼び出しごとの skill の確かめ直し中", fakeHold{Method: "skills/list", Skip: 1}},
		{"スレッドの作成中", fakeHold{Method: "thread/start"}},
		{"履歴の投入中", fakeHold{Method: "thread/inject_items"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang"}, HoldReply: c.hold})
			adapter := newTestAdapter(t)

			ctx, cancel := context.WithCancel(context.Background())
			ch, err := adapter.StreamMessage(ctx, testRequest())
			if err != nil {
				t.Fatalf("送信を開始できない: %v", err)
			}
			waitFor(t, func() bool { return countOf(env.received(t), c.hold.Method) > c.hold.Skip },
				c.hold.Method+" が届かない")
			// fake は印を置くまで応答しないので、中断は必ず応答より先になる。
			cancel()
			if err := os.WriteFile(filepath.Join(env.base, fakeReleaseStartReply), nil, 0o600); err != nil {
				t.Fatalf("応答の保留を解けない: %v", err)
			}

			last := lastEvent(t, collect(t, ch))
			if last.Kind != aiprovider.EventDone || !last.Interrupted {
				t.Fatalf("中断として閉じていない: %+v", last)
			}
			if n := countOf(env.received(t), "turn/start"); n != 0 {
				t.Errorf("中断の後にターンを始めた（turn/start %d 回）", n)
			}
		})
	}
}

// 呼び出し元が生きているのに接続確立のタイムアウトで切れたものは、中断ではなく一時的エラーのまま返す
// （利用者は中断していないので、上位が再試行できるように分類を保つ）。
func TestConnectTimeoutStaysTransient(t *testing.T) {
	cases := []struct {
		name string
		hold fakeHold
	}{
		{"子プロセスの起動中", fakeHold{Method: "initialize"}},
		{"スレッドの作成中", fakeHold{Method: "thread/start"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang"}, HoldReply: c.hold})
			adapter := newTestAdapter(t)
			// 応答は保留したままなので、接続確立のタイムアウトが必ず先に来る。
			adapter.timeouts.Connect = 300 * time.Millisecond

			ch, err := adapter.StreamMessage(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("送信を開始できない: %v", err)
			}
			last := lastEvent(t, collect(t, ch))
			if err := os.WriteFile(filepath.Join(env.base, fakeReleaseStartReply), nil, 0o600); err != nil {
				t.Fatalf("応答の保留を解けない: %v", err)
			}
			if last.Kind != aiprovider.EventError {
				t.Fatalf("一時的エラーで閉じていない: %+v", last)
			}
			if last.Err.Class != aiprovider.ErrClassTransient || last.Err.Code != codeProcessExited {
				t.Errorf("分類・Code が違う: %v / %q", last.Err.Class, last.Err.Code)
			}
		})
	}
}

// 順番待ち（先の呼び出しのターンが終わるのを待っている間）に中断しても、中断として閉じる。
func TestInterruptWhileWaitingForTurnSlot(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "hang", HoldStartReply: true}})
	adapter := newTestAdapter(t)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first, err := adapter.StreamMessage(firstCtx, testRequest())
	if err != nil {
		t.Fatalf("先の送信を開始できない: %v", err)
	}
	// 先の呼び出しは turn/start の応答待ちで止まり、順番を持ったままになる。
	waitFor(t, func() bool { return countOf(env.received(t), "turn/start") > 0 }, "先のターンが始まらない")

	ctx, cancel := context.WithCancel(context.Background())
	second, err := adapter.StreamMessage(ctx, testRequest())
	if err != nil {
		t.Fatalf("後の送信を開始できない: %v", err)
	}
	cancel() // 順番は先の呼び出しが持っているので、後の呼び出しは順番を取る前に中断される

	last := lastEvent(t, collect(t, second))
	if last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("順番待ちの中断が中断として閉じていない: %+v", last)
	}

	// 先の呼び出しも中断して読み切る（子プロセスを残さない）。
	cancelFirst()
	if err := os.WriteFile(filepath.Join(env.base, fakeReleaseStartReply), nil, 0o600); err != nil {
		t.Fatalf("応答の保留を解けない: %v", err)
	}
	if last := lastEvent(t, collect(t, first)); last.Kind != aiprovider.EventDone || !last.Interrupted {
		t.Fatalf("先の呼び出しが中断として閉じていない: %+v", last)
	}
}

// 子プロセスの異常終了は一時的エラー（次の呼び出しで起動し直す）。
func TestProcessExitIsTransient(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "exit"}})
	adapter := newTestAdapter(t)

	ch, err := adapter.StreamMessage(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	last := lastEvent(t, collect(t, ch))
	if last.Kind != aiprovider.EventError {
		t.Fatalf("エラーで終わっていない: %+v", last)
	}
	if last.Err.Class != aiprovider.ErrClassTransient || last.Err.Code != codeProcessExited {
		t.Errorf("分類・Code が違う: %v / %q", last.Err.Class, last.Err.Code)
	}
	waitFor(t, func() bool { return procManager.current() == nil }, "終わった子プロセスが残っている")
	// 一時領域を残さない
	waitFor(t, func() bool {
		_, err := os.Stat(filepath.Join(env.base, "codex"))
		return os.IsNotExist(err)
	}, "一時領域が残っている")
}

// Codex の誤りが分類・Code へ写る（実際の通知の形で確かめる）。
func TestTurnErrorIsNormalized(t *testing.T) {
	cases := []struct {
		info  string
		class aiprovider.ErrorClass
		code  string
	}{
		{"contextWindowExceeded", aiprovider.ErrClassPermanent, "context_window_exceeded"},
		{"usageLimitExceeded", aiprovider.ErrClassConfig, "plan_usage_exhausted"},
		{"unauthorized", aiprovider.ErrClassConfig, "unauthorized"},
		{"serverOverloaded", aiprovider.ErrClassTransient, "serverOverloaded"},
		{"sandboxError", aiprovider.ErrClassPermanent, codeUnexpected},
	}
	for _, c := range cases {
		t.Run(c.info, func(t *testing.T) {
			newFakeEnv(t, fakeScenario{Turn: fakeTurn{Kind: "error", ErrorInfo: c.info, ErrorMessage: "test failure"}})
			adapter := newTestAdapter(t)

			ch, err := adapter.StreamMessage(context.Background(), testRequest())
			if err != nil {
				t.Fatalf("送信を開始できない: %v", err)
			}
			last := lastEvent(t, collect(t, ch))
			if last.Kind != aiprovider.EventError {
				t.Fatalf("エラーで終わっていない: %+v", last)
			}
			if last.Err.Class != c.class || last.Err.Code != c.code {
				t.Errorf("分類・Code が違う: %v / %q", last.Err.Class, last.Err.Code)
			}
			if last.Err.Message != "test failure" {
				t.Errorf("Codex の message が伝わっていない: %q", last.Err.Message)
			}
		})
	}
}

// 1 つの子プロセスで同時に進むターンは 1 つ（先着順に直列化）。
func TestTurnsAreSerialized(t *testing.T) {
	env := newFakeEnv(t, fakeScenario{Turn: fakeTurn{Text: "はい", DelayMs: 150}})
	adapter := newTestAdapter(t)

	var wg sync.WaitGroup
	results := make([]aiprovider.StreamEvent, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ch, err := adapter.StreamMessage(context.Background(), testRequest())
			if err != nil {
				t.Errorf("送信を開始できない: %v", err)
				return
			}
			events := collect(t, ch)
			results[i] = events[len(events)-1]
		}(i)
	}
	wg.Wait()

	for i, last := range results {
		if last.Kind != aiprovider.EventDone {
			t.Errorf("%d 番目が正常に終わっていない: %+v", i, last)
		}
	}
	// 子プロセスは 1 つ（起動し直していない）で、ターンは重ならない。
	messages := env.received(t)
	if n := countOf(messages, "initialize"); n != 1 {
		t.Errorf("子プロセスの起動 = %d 回, want 1", n)
	}
	order := methodOrder(messages)
	starts := indexesOf(order, "turn/start")
	if len(starts) != 2 {
		t.Fatalf("turn/start の回数 = %d, want 2", len(starts))
	}
	// 2 つ目のターンの前に、1 つ目のスレッドの後片づけ（thread/unsubscribe）が済んでいる
	if !hasBefore(order, "thread/unsubscribe", starts[1], starts[0]) {
		t.Error("前のターンが終わる前に次のターンが始まっている（直列化できていない）")
	}
}

// モデル一覧は手元の定義から返り、既知一覧の Tier で補完される。
func TestListModelsUsesLocalCatalog(t *testing.T) {
	newFakeEnv(t, fakeScenario{})
	adapter := newTestAdapter(t)

	models, err := adapter.ListModels(context.Background())
	if err != nil {
		t.Fatalf("モデル一覧を取得できない: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("モデルが 1 件も返らない")
	}
	found := false
	for _, m := range models {
		if m.ID != "gpt-5.5" {
			continue
		}
		found = true
		if m.ContextWindow != 272000 {
			t.Errorf("コンテキスト長が手元の定義と違う: %d", m.ContextWindow)
		}
		if m.Tier != aiprovider.TierPrimary || !m.DefaultForTier {
			t.Errorf("既知一覧の区分が反映されていない: %+v", m)
		}
	}
	if !found {
		t.Errorf("既定のモデルが一覧に無い: %+v", models)
	}
}
