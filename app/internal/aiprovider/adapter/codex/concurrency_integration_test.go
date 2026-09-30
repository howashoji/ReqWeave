//go:build integration

package codex

// 同時実行の実測（同時に進むターンを 1 つに絞る設計の前提を確かめる）。
//
// 確かめること: **1 つの子プロセスで複数のスレッドを同時に扱えるか**。
//   - 2 つのスレッドでターンを同時に開始したとき、モデルへの要求が**同時に飛ぶ**か（重なるか）
//   - 応答が混ざらないか（各スレッドが自分の応答だけを受け取るか）
//   - `thread/tokenUsage/updated` の `total` が各スレッドへ正しく帰属するか
//
// 測り方: 本番と同じ起動設定のまま（`config/read` の照合まで本番の検査を通す）、独自プロバイダの
// `base_url` だけを手元の記録用サーバへ向ける（版を上げるときの要求本文の観測と同じ方法）。記録用サーバは
// **2 件目の要求が届くまで 1 件目の応答を返さない**（関門）。同時に飛べば関門が開き、
// 直列なら関門は開かずに時間切れで 1 件ずつ返る — どちらであるかが「重なり」の実測になる。
//
// アダプタの本番コードは通らない（`manager` が同時に進むターンを 1 つへ絞るため、
// アダプタ経由では Codex 側の可否を測れない）。そこで**このファイルの中だけ**で
// JSON-RPC を直接話す最小の実装を持つ。単体・結合テストの他の経路には影響しない。
//
// 実キーは使わない（ダミーのキー・127.0.0.1 の記録用サーバで完結する）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// 関門の待ち上限。同時に飛ばないときはここで時間切れになり、1 件ずつ返る。
const concurrencyGate = 3 * time.Second

// probeUsage は記録用サーバが返すトークン実績（スレッドごとに別の値にして帰属を見る）。
type probeUsage struct {
	input, output, reasoning int
}

// probeTurn は 1 本のスレッドで送る発話と、それに対して記録用サーバが返す内容。
type probeTurn struct {
	label  string // 要求本文に現れる目印（どちらの要求かを記録用サーバが見分ける）
	answer string
	usage  probeUsage
}

var probeTurns = []probeTurn{
	{label: "ALPHA", answer: "ALPHA-ANSWER：拠点ごとの在庫を確認します。",
		usage: probeUsage{input: 1111, output: 11, reasoning: 1}},
	{label: "BRAVO", answer: "BRAVO-ANSWER：発注点の算定方法を確認します。",
		usage: probeUsage{input: 2222, output: 22, reasoning: 2}},
}

// gateServer は「2 件そろうまで返さない」記録用サーバ（偽 Responses API）。
type gateServer struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	inFlight int
	arrived  int
	// overlapped は 2 件の要求が同時に処理中になったか（＝モデルへの要求が重なったか）。
	overlapped bool
	// order は要求が届いた順の目印。
	order []string
	gate  chan struct{}
	once  sync.Once
}

func newGateServer(t *testing.T) *gateServer {
	t.Helper()
	g := &gateServer{t: t, gate: make(chan struct{})}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.server.Close)
	// 後片づけでは先に関門を開ける（止めたままの要求を抱えて Close が待たないようにする）。
	t.Cleanup(func() { g.once.Do(func() { close(g.gate) }) })
	return g
}

// waitArrived は要求が n 件届くまで待つ（届かなければ false）。
func (g *gateServer) waitArrived(n int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		got := g.arrived
		g.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (g *gateServer) handle(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if !strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "responses") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	raw, _ := json.Marshal(body)
	turn, ok := turnForBody(string(raw))
	if !ok {
		g.t.Errorf("記録用サーバが見分けられない要求を受け取った")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	g.mu.Lock()
	g.inFlight++
	g.arrived++
	g.order = append(g.order, turn.label)
	if g.inFlight >= 2 {
		g.overlapped = true
		g.once.Do(func() { close(g.gate) })
	}
	g.mu.Unlock()

	// 2 件目が届けば即座に、届かなければ時間切れで返す（直列でも試験が止まらない）。
	select {
	case <-g.gate:
	case <-time.After(concurrencyGate):
	}

	writeSSE(w, turn)

	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
}

// turnForBody は要求本文に**最後に現れる**目印で、どちらの発話への要求かを見分ける。
//
// 要求本文には履歴も載るため、最初に現れる目印では直前の発話を取り違える。
func turnForBody(body string) (probeTurn, bool) {
	found, at := probeTurn{}, -1
	for _, turn := range probeTurns {
		if i := strings.LastIndex(body, turn.label); i > at {
			found, at = turn, i
		}
	}
	return found, at >= 0
}

func writeSSE(w http.ResponseWriter, turn probeTurn) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	message := map[string]any{
		"type": "message", "role": "assistant", "id": "msg_" + turn.label, "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": turn.answer, "annotations": []any{}}},
	}
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_" + turn.label}},
		{"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + turn.label,
				"status": "in_progress", "content": []any{}}},
		{"type": "response.output_text.delta", "item_id": "msg_" + turn.label, "output_index": 0,
			"content_index": 0, "delta": turn.answer},
		{"type": "response.output_item.done", "output_index": 0, "item": message},
		{"type": "response.completed", "response": map[string]any{"id": "resp_" + turn.label,
			"usage": map[string]any{
				"input_tokens":          turn.usage.input,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens":         turn.usage.output,
				"output_tokens_details": map[string]any{"reasoning_tokens": turn.usage.reasoning},
				"total_tokens":          turn.usage.input + turn.usage.output,
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

// rpcNote は子プロセスから届いた通知 1 件。
type rpcNote struct {
	method string
	params json.RawMessage
}

// probe は実バイナリの子プロセス 1 つと、その JSON-RPC の口。
//
// 本番の起動設定（launchConfig）・起動後の検査（版・config/read・skill）はそのまま通す。
// 違うのは「スレッドを 2 本同時に動かす」ことだけ。
type probe struct {
	t    *testing.T
	ws   *workspace
	cfg  launchConfig
	opts probeOptions
	cmd  *exec.Cmd
	rpc  *rpcClient

	mu   sync.Mutex
	subs map[string]chan rpcNote
	// requests は Codex から本システムへ届いた要求（抑止設定の下では 0 件のはず）。
	requests []string
	// accountNotes はスレッドに属さないアカウント系の通知（残量の届き方の観測）。
	accountNotes []rpcNote
	stopped      bool
}

// newProbe はシークレットキー方式の子プロセスを 1 つ起こす（従来の呼び出し口）。
func newProbe(t *testing.T, baseURL string) *probe {
	t.Helper()
	return newProbeWith(t, probeOptions{auth: authSecretKey, baseURL: baseURL})
}

// initialize は本番と同じ起動後の検査を通す。
//
// 認証方式で違うのは (1) シークレットキー方式だけが標準入力からキーを渡すこと、
// (2) 保存先を差し替えた場合の実効値の照合（signin_integration_test.go の
// verifyEffectiveConfigExcept）だけで、他は本番と同一である。
func (p *probe) initialize() {
	p.t.Helper()
	cfg := p.cfg
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	raw := p.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name": clientName, "title": clientTitle, "version": clientVersion(),
		},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	var initResult struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(raw, &initResult); err != nil {
		p.t.Fatalf("initialize の応答を解釈できません: %v", err)
	}
	// a. 実行中の Codex が同梱の版であること
	if err := verifyVersion(initResult.UserAgent); err != nil {
		p.t.Fatalf("同梱の版ではありません: %v", err)
	}
	if err := p.rpc.notify("initialized", map[string]any{}); err != nil {
		p.t.Fatalf("initialized を送れません: %v", err)
	}
	if cfg.auth == authSecretKey && !p.opts.noLogin {
		// キーは標準入力から渡す（ダミー。コマンドライン引数・環境変数に置かない）
		p.call(ctx, "account/login/start", map[string]any{"type": "apiKey", "apiKey": dummyKey})
	}

	// d. 認証の種類が選択中の認証方式と一致すること
	//    （noLogin は「前の起動の認証情報が残っているか」を見る検査であり、
	//     この検査そのものが観測の対象になるため呼び出し側で確かめる）
	if !p.opts.noLogin {
		accountType, hasAccount := p.accountType(ctx)
		if err := cfg.verifyAccountType(accountType, hasAccount); err != nil {
			p.t.Fatalf("認証の種類が合いません: %v", err)
		}
	}

	// b. config/read の実効値が本番の起動引数のとおりであること（観測が本番と同じ設定である担保）
	configRaw := p.call(ctx, "config/read", map[string]any{
		"includeLayers": false, "cwd": cfg.workDir,
	})
	var configResult struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(configRaw, &configResult); err != nil {
		p.t.Fatalf("config/read の応答を解釈できません: %v", err)
	}
	p.verifyLaunchedConfig(configResult.Config)
	// c. 利用者ホームの skill が 1 件も有効でないこと
	p.disableSkills(ctx, cfg.workDir)
}

// accountType は `account/read` の認証の種類（あれば）を返す。
func (p *probe) accountType(ctx context.Context) (string, bool) {
	p.t.Helper()
	raw := p.call(ctx, "account/read", map[string]any{})
	var body struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		p.t.Fatalf("account/read の応答を解釈できません: %v", err)
	}
	if body.Account == nil {
		return "", false
	}
	return body.Account.Type, true
}

func (p *probe) disableSkills(ctx context.Context, workDir string) {
	p.t.Helper()
	enabled := p.enabledSkills(ctx, workDir)
	for _, path := range enabled {
		p.call(ctx, "skills/config/write", map[string]any{"path": path, "enabled": false})
	}
	if remaining := p.enabledSkills(ctx, workDir); len(remaining) > 0 {
		// パスは利用者のホームを含むため件数だけを示す。
		p.t.Fatalf("利用者の skill を無効にできません（%d 件が有効のまま）", len(remaining))
	}
}

func (p *probe) enabledSkills(ctx context.Context, workDir string) []string {
	p.t.Helper()
	raw := p.call(ctx, "skills/list", map[string]any{"cwds": []string{workDir}, "forceReload": true})
	var result struct {
		Data []struct {
			Skills []struct {
				Path    string `json:"path"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		p.t.Fatalf("skills/list の応答を解釈できません: %v", err)
	}
	var paths []string
	for _, entry := range result.Data {
		for _, skill := range entry.Skills {
			if skill.Enabled && skill.Path != "" {
				paths = append(paths, skill.Path)
			}
		}
	}
	return paths
}

func (p *probe) call(ctx context.Context, method string, params any) json.RawMessage {
	p.t.Helper()
	raw, err := p.rpc.call(ctx, method, params)
	if err != nil {
		p.t.Fatalf("%s に失敗しました: %v", method, err)
	}
	return raw
}

// handleNotification はスレッドごとの受け口へ振り分ける（スレッド ID を持たない通知は捨てる）。
//
// アカウント系の通知（`account/rateLimits/updated` など）はスレッドに属さないため、
// 別の入れ物へ順に積む（並行時の届き方の観測）。
func (p *probe) handleNotification(method string, params json.RawMessage) {
	if strings.HasPrefix(method, "account/") {
		p.mu.Lock()
		p.accountNotes = append(p.accountNotes, rpcNote{method: method, params: params})
		p.mu.Unlock()
	}
	threadID := threadIDOf(params)
	if threadID == "" {
		return
	}
	select {
	case p.notes(threadID) <- rpcNote{method: method, params: params}:
	default:
		p.t.Errorf("通知の受け口があふれた（%s / %s）", threadID, method)
	}
}

func (p *probe) notes(threadID string) chan rpcNote {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.subs[threadID]
	if !ok {
		ch = make(chan rpcNote, 512)
		p.subs[threadID] = ch
	}
	return ch
}

func (p *probe) serverRequests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

// turnResult は 1 本のスレッドの結果。
type turnResult struct {
	label    string
	threadID string
	text     string
	usage    probeUsage
	status   string
	failure  string
	// itemTypes は現れた項目の種類（道具の呼び出しが混ざらないことの確認）。
	itemTypes []string
	// startedAt / endedAt はターンの開始と完了の時刻（重なりの実測に使う）。
	startedAt, endedAt time.Time
}

// runTurn は 1 本の使い捨てスレッドでターンを 1 回行う（stream.go と同じ写像）。
func (p *probe) runTurn(ctx context.Context, turn probeTurn) turnResult {
	out := turnResult{label: turn.label}

	raw, err := p.rpc.call(ctx, "thread/start", map[string]any{
		"ephemeral":        true,
		"cwd":              p.cfg.workDir,
		"sandbox":          "read-only",
		"approvalPolicy":   "never",
		"baseInstructions": "あなたは要件定義を支援するアシスタントです。日本語で答えてください。",
		"model":            "gpt-5.5",
	})
	if err != nil {
		out.failure = "thread/start: " + err.Error()
		return out
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.Thread.ID == "" {
		out.failure = "thread/start の応答を解釈できません"
		return out
	}
	out.threadID = started.Thread.ID
	notes := p.notes(out.threadID)

	out.startedAt = time.Now()
	if _, err := p.rpc.call(ctx, "turn/start", map[string]any{
		"threadId": out.threadID,
		"input": []map[string]any{{
			"type": "text", "text": turn.label + " の在庫要件を詰めたい。", "text_elements": []any{},
		}},
		"effort": "medium",
	}); err != nil {
		out.failure = "turn/start: " + err.Error()
		return out
	}

	for {
		select {
		case note := <-notes:
			switch note.method {
			case "item/started":
				if kind, ok := itemTypeOf(note.params); ok {
					out.itemTypes = append(out.itemTypes, kind)
				}
			case "item/agentMessage/delta":
				out.text += deltaOf(note.params)
			case "thread/tokenUsage/updated":
				if usage, ok := usageOf(note.params); ok {
					out.usage = probeUsage{
						input: usage.InputTokens, output: usage.OutputTokens, reasoning: usage.ReasoningTokens,
					}
				}
			case "turn/completed":
				status, turnErr := turnResultOf(note.params)
				out.status = status
				out.endedAt = time.Now()
				if turnErr != nil {
					out.failure = fmt.Sprintf("%+v", *turnErr)
				}
				return out
			case "error":
				if e, willRetry := turnErrorOf(note.params); e != nil && !willRetry {
					out.failure = fmt.Sprintf("%+v", *e)
				}
			}
		case <-ctx.Done():
			out.failure = "ターンが終わらない: " + ctx.Err().Error()
			out.endedAt = time.Now()
			return out
		}
	}
}

// 1 つの子プロセスで 2 本のスレッドのターンを同時に走らせ、
// **モデルへの要求が重なること・応答が混ざらないこと・トークン実績が各スレッドへ帰属すること**を
// 実バイナリで確かめる。
//
// ここで固定するのは「Codex 側が同時に扱えるか」という事実である
// （本システムが同時に流すかどうかは別の決定であり、アダプタ側は TestTurnsAreSerialized が固定する）。
func TestRealCodexRunsTwoThreadsConcurrently(t *testing.T) {
	gate := newGateServer(t)
	p := newProbe(t, gate.server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	results := make([]turnResult, len(probeTurns))
	var wg sync.WaitGroup
	for i, turn := range probeTurns {
		wg.Add(1)
		go func(i int, turn probeTurn) {
			defer wg.Done()
			results[i] = p.runTurn(ctx, turn)
		}(i, turn)
	}
	wg.Wait()

	for _, r := range results {
		t.Logf("%s: status=%q thread=%s usage=%+v text=%q failure=%q 所要=%v",
			r.label, r.status, r.threadID, r.usage, r.text, r.failure, r.endedAt.Sub(r.startedAt))
	}
	gate.mu.Lock()
	overlapped, order := gate.overlapped, append([]string(nil), gate.order...)
	gate.mu.Unlock()
	t.Logf("モデルへの要求が重なったか = %v（届いた順: %v）", overlapped, order)

	// 1. 2 本とも完了すること（片方が activeTurnNotSteerable 等で落ちれば同時には扱えない）
	for _, r := range results {
		if r.failure != "" {
			t.Fatalf("%s のターンが失敗した: %s", r.label, r.failure)
		}
		if r.status != "completed" {
			t.Fatalf("%s のターンが完了していない: status=%q", r.label, r.status)
		}
	}

	// 2. モデルへの要求が実際に重なること（1 件目の応答を止めたまま 2 件目が届いた）
	if !overlapped {
		t.Fatalf("2 本のターンが同時に走っていない（要求の到着順: %v）。"+
			"同時 1 ターンの前提が成り立つ", order)
	}

	// 3. 応答が混ざらないこと（各スレッドは自分の応答だけを受け取る）
	for i, r := range results {
		want := probeTurns[i]
		if r.text != want.answer {
			t.Errorf("%s の応答が違う: %q（want %q）", r.label, r.text, want.answer)
		}
		for _, other := range probeTurns {
			if other.label != r.label && strings.Contains(r.text, other.label) {
				t.Errorf("%s の応答に %s の内容が混ざっている: %q", r.label, other.label, r.text)
			}
		}
	}

	// 4. `thread/tokenUsage/updated` の total が各スレッドへ帰属すること
	//    （合算されていれば 1111+2222 のような値になる）
	for i, r := range results {
		want := probeTurns[i].usage
		if r.usage != want {
			t.Errorf("%s のトークン実績が違う: %+v（want %+v）", r.label, r.usage, want)
		}
	}

	// 5. 同時に走らせても道具の呼び出し・本システムへの要求は現れないこと
	for _, r := range results {
		for _, kind := range r.itemTypes {
			if !allowedItemTypes[kind] {
				t.Errorf("%s に想定外の項目が現れた: %q（全体: %v）", r.label, kind, r.itemTypes)
			}
		}
	}
	if reqs := p.serverRequests(); len(reqs) != 0 {
		t.Errorf("Codex から本システムへの要求が届いた: %v", reqs)
	}
}

// **同じスレッドでは** ターンが重ならず、
// 進行中のターンへ送った 2 つ目の `turn/start` は**同じターンへ畳み込まれる**。
//
// 1 本目のターンがモデルの応答を待っている間に同じスレッドへ 2 つ目の `turn/start` を送ると、
// Codex は誤り応答を返さず、**1 本目と同じターン ID** を返す。2 つ目の発話は同じターンの項目として
// 積まれ、モデルへの要求は 1 本目の応答が返ってから飛ぶ（`turn/completed` は 1 回だけ）。
// `thread/tokenUsage/updated` の `total` はそのスレッドの**累計**になる（各要求の実績の和）。
//
// これは 2 つの前提を固定する:
//   - 1 呼び出し = 使い捨てスレッド 1 つを守る限り、`total` は
//     その呼び出しの消費と一致する。スレッドを使い回すと累計になり一致しない。
//   - 並行に動かしたいときは**スレッドを分ける**必要がある（同じスレッドでは重ならない）。
func TestRealCodexFoldsSecondTurnIntoRunningTurnOnSameThread(t *testing.T) {
	gate := newGateServer(t)
	p := newProbe(t, gate.server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	raw := p.call(ctx, "thread/start", map[string]any{
		"ephemeral":        true,
		"cwd":              p.cfg.workDir,
		"sandbox":          "read-only",
		"approvalPolicy":   "never",
		"baseInstructions": "あなたは要件定義を支援するアシスタントです。日本語で答えてください。",
		"model":            "gpt-5.5",
	})
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.Thread.ID == "" {
		t.Fatalf("thread/start の応答を解釈できません: %v", err)
	}
	threadID := started.Thread.ID
	notes := p.notes(threadID)

	input := func(turn probeTurn) []map[string]any {
		return []map[string]any{{
			"type": "text", "text": turn.label + " の在庫要件を詰めたい。", "text_elements": []any{},
		}}
	}
	firstTurnID := turnIDOfResponse(t, p.call(ctx, "turn/start", map[string]any{
		"threadId": threadID, "input": input(probeTurns[0]), "effort": "medium",
	}))
	// 1 本目がモデルの応答を待っている状態にしてから 2 つ目を送る（記録用サーバが止めている）。
	if !gate.waitArrived(1, 30*time.Second) {
		t.Fatal("1 本目のモデルへの要求が記録用サーバへ届かない（前提が崩れている）")
	}

	raw2, secondErr := p.rpc.call(ctx, "turn/start", map[string]any{
		"threadId": threadID, "input": input(probeTurns[1]), "effort": "medium",
	})
	if secondErr != nil {
		t.Fatalf("進行中のスレッドへの 2 つ目の turn/start が断られた: %v", secondErr)
	}
	secondTurnID := turnIDOfResponse(t, raw2)
	if secondTurnID == "" {
		t.Fatal("2 つ目の turn/start がターンを返さなかった")
	}

	// 1 本目が止められている間に 2 つ目のモデルへの要求が飛ばないこと（＝同じスレッドでは重ならない）。
	if gate.waitArrived(2, concurrencyGate/2) {
		gate.mu.Lock()
		order := append([]string(nil), gate.order...)
		gate.mu.Unlock()
		t.Fatalf("同じスレッドでモデルへの要求が重なった（到着順: %v）", order)
	}

	// 関門が開いた後、**1 回だけ** turn/completed が届く（2 つ目は同じターンへ畳み込まれている）。
	var (
		completed      int
		completedTurns []string
		lastUsage      probeUsage
		agentTexts     []string
		// deltaTurns は本文差分がどのターンの下で届いたか（畳み込み先の確認）。
		deltaTurns []string
	)
	deadline := time.After(40 * time.Second)
	quiet := time.Duration(0)
collecting:
	for {
		select {
		case note := <-notes:
			switch note.method {
			case "item/agentMessage/delta":
				agentTexts = append(agentTexts, deltaOf(note.params))
				deltaTurns = append(deltaTurns, noteTurnID(note.params))
			case "thread/tokenUsage/updated":
				if usage, ok := usageOf(note.params); ok {
					lastUsage = probeUsage{
						input: usage.InputTokens, output: usage.OutputTokens, reasoning: usage.ReasoningTokens,
					}
				}
			case "turn/completed":
				status, turnErr := turnResultOf(note.params)
				if status != "completed" || turnErr != nil {
					t.Fatalf("ターンが完了しなかった: status=%q err=%+v", status, turnErr)
				}
				completed++
				completedTurns = append(completedTurns, completedTurnID(note.params))
				// 完了後もしばらく待ち、2 つ目のターンが別に立たないことを確かめる。
				quiet = 3 * time.Second
			}
		case <-time.After(quietOr(quiet, 40*time.Second)):
			if completed == 0 {
				t.Fatal("ターンが終わらない")
			}
			break collecting
		case <-deadline:
			t.Fatalf("ターンが終わらない（完了 %d 件）", completed)
		}
	}

	gate.mu.Lock()
	order := append([]string(nil), gate.order...)
	gate.mu.Unlock()
	t.Logf("モデルへの要求の到着順: %v / 完了したターン: %v / 応答: %v / 本文差分のターン: %v / 累計: %+v",
		order, completedTurns, agentTexts, deltaTurns, lastUsage)

	// 2 つ目の `turn/start` は別のターン ID を返すが、そのターンは動かない（完了通知も来ない）。
	if completed != 1 {
		t.Errorf("turn/completed = %d 回, want 1（2 つ目は進行中のターンへ畳み込まれる）", completed)
	}
	if len(completedTurns) != 1 || completedTurns[0] != firstTurnID {
		t.Errorf("完了したターン = %v, want [%s]（1 本目のターンだけが完了する）", completedTurns, firstTurnID)
	}
	for _, id := range completedTurns {
		if id == secondTurnID {
			t.Errorf("2 つ目のターン（%s）が独立して完了した", secondTurnID)
		}
	}
	// 2 つの応答は、いずれも 1 本目のターンの下で届く（畳み込み先）。
	if len(agentTexts) != 2 {
		t.Errorf("応答の件数 = %d, want 2", len(agentTexts))
	}
	for i, id := range deltaTurns {
		if id != firstTurnID {
			t.Errorf("%d 件目の応答が別のターンで届いた: %s（want %s）", i+1, id, firstTurnID)
		}
	}
	// モデルへの要求は 2 回、順番は送った順（後から送った発話は 1 本目の応答の後に飛ぶ）。
	if len(order) != 2 || order[0] != probeTurns[0].label || order[1] != probeTurns[1].label {
		t.Errorf("モデルへの要求の到着順 = %v, want [%s %s]",
			order, probeTurns[0].label, probeTurns[1].label)
	}
	// `total` はスレッドの累計（2 回分の和）になる。1 呼び出し = スレッド 1 つを守る前提の裏づけ。
	want := probeUsage{
		input:     probeTurns[0].usage.input + probeTurns[1].usage.input,
		output:    probeTurns[0].usage.output + probeTurns[1].usage.output,
		reasoning: probeTurns[0].usage.reasoning + probeTurns[1].usage.reasoning,
	}
	if lastUsage != want {
		t.Errorf("スレッドの累計 = %+v, want %+v（total は呼び出しごとではなくスレッドの累計）",
			lastUsage, want)
	}
}

// quietOr は静穏待ちの長さを返す（まだ完了していない間は長い方を使う）。
func quietOr(quiet, fallback time.Duration) time.Duration {
	if quiet > 0 {
		return quiet
	}
	return fallback
}

// noteTurnID は通知が属するターン ID（`turnId`）。
func noteTurnID(params json.RawMessage) string {
	var body struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return ""
	}
	return body.TurnID
}

// completedTurnID は `turn/completed` が示すターン ID。
func completedTurnID(params json.RawMessage) string {
	var body struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return ""
	}
	return body.Turn.ID
}

func turnIDOfResponse(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Turn.ID == "" {
		t.Fatalf("turn/start の応答を解釈できません: %v（%s）", err, string(raw))
	}
	return result.Turn.ID
}
