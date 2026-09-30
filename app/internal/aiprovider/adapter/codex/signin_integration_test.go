//go:build integration

package codex

// ChatGPT のアカウントでのサインイン方式の結合テスト。
//
// 本ファイルが確かめるのは、サインイン方式を提供する条件（次の 4 つ）である。
//
//   - 要求本文に利用者ホームの skill の一覧・作業フォルダ外の画像が含まれないこと
//   - シークレットキー方式との差が**認証のヘッダだけ**であること（同じ発話で 2 方式を走らせて突き合わせる）
//   - Codex のファイルに認証情報が残らないこと（`authorization: Bearer` に続く値・`refresh_token` に
//     続く値が 0 件、JWT の形が呼び出し先の応答のクッキー以外に無い）
//   - サインイン方式での同時実行で `account/rateLimits/updated` がどう届くか
//
// **本物のアカウント・実キーは使わない**。ChatGPT のアカウントは**偽のトークン**で再現する
// （実機の検証と同じ方法。`auth.json` に JWT の形の access_token・id_token と
// refresh_token を置く）。モデルの呼び出し先は 127.0.0.1 の記録用サーバで、それ以外の外部接続は
// 誰も待ち受けていないアドレスへ向ける（本物の OpenAI・chatgpt.com へは接続しない）。
//
// **本番の起動設定との違いは 2 点だけ**で、いずれも観測のために必要なもの:
//  1. 独自プロバイダの `base_url` を記録用サーバへ向ける（要求本文の観測手段）
//  2. 認証情報の保存先を `keyring` → `file` にする（偽のトークンを読ませるため。本物の
//     サインインは OS セキュアストレージへ書くので、偽のトークンでは `keyring` を使えない）
//
// 2 は起動引数の 1 つだけを差し替え、差し替えたこと自体を `config/read` の実効値で確かめる
// （verifyEffectiveConfigExcept）。他の項目はすべて本番の期待値と照合する。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 偽のトークンで ChatGPT のアカウントを再現する
// ---------------------------------------------------------------------------

// fakeChatGPTPlan は偽のアカウントのプラン種別（実機の Free とは別の値にして、
// 応答から読み取った値であることを見分けられるようにする）。
const (
	fakeChatGPTPlan      = "plus"
	fakeChatGPTAccountID = "acct-reqweave-test-0000"
	fakeChatGPTEmail     = "codex-probe@example.invalid"
)

// fakeChatGPTTokens は偽の認証情報一式。**値はテストの中でしか存在しない**。
type fakeChatGPTTokens struct {
	access  string
	id      string
	refresh string
}

func newFakeChatGPTTokens() fakeChatGPTTokens {
	now := time.Now().Unix()
	claims := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_plan_type":                 fakeChatGPTPlan,
			"chatgpt_account_id":                fakeChatGPTAccountID,
			"chatgpt_user_id":                   "user-reqweave-test-0000",
			"chatgpt_subscription_active_start": now - 86400,
		},
		"email": fakeChatGPTEmail,
		"iat":   now,
		"exp":   now + 86400,
	}
	return fakeChatGPTTokens{
		access:  jwtShaped(claims, "probe-access"),
		id:      jwtShaped(claims, "probe-id"),
		refresh: "rt-reqweave-test-REFRESH-0000",
	}
}

// jwtShaped は JWT の形（3 つの部分を `.` でつないだもの）の文字列を作る。署名はしない。
func jwtShaped(claims map[string]any, audience string) string {
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	payload := map[string]any{"aud": audience}
	for k, v := range claims {
		payload[k] = v
	}
	return enc(map[string]any{"alg": "none", "typ": "JWT"}) + "." + enc(payload) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("signature-"+audience))
}

// authJSONName は偽のトークンを置くファイル（Codex が保存先 `file` のときに読む）。
const authJSONName = "auth.json"

// write は CODEX_HOME へ偽の `auth.json` を置く。
func (f fakeChatGPTTokens) write(t *testing.T, home string) {
	t.Helper()
	body := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      f.id,
			"access_token":  f.access,
			"refresh_token": f.refresh,
			"account_id":    fakeChatGPTAccountID,
		},
		"last_refresh": time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("偽の認証情報を組み立てられません: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, authJSONName), raw, 0o600); err != nil {
		t.Fatalf("偽の認証情報を置けません: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 子プロセスの起こし方（認証方式を選べる版）
// ---------------------------------------------------------------------------

// probeOptions は子プロセス 1 つの起こし方。
type probeOptions struct {
	// auth は認証方式（本番の launchConfig にそのまま渡る）。
	auth authMethod
	// baseURL は独自プロバイダの呼び出し先（記録用サーバ）。
	baseURL string
	// base は一時領域の親。空なら毎回新しい場所（同じ場所で起こし直す検査だけが指定する）。
	base string
	// storeOverride は認証情報の保存先の差し替え（空なら本番のまま）。
	storeOverride string
	// noLogin はキーを渡さず、起動後の検査 (d)（認証の種類の照合）も行わない。
	// 「前の起動の認証情報が残っているか」を観測する検査だけが使う。
	noLogin bool
	// prepareHome は子プロセスの起動前に CODEX_HOME へ置くもの（偽の auth.json）。
	prepareHome func(t *testing.T, home string)
}

// newProbeWith は本番と同じ起動設定で子プロセスを 1 つ起こし、起動後の検査まで通す。
func newProbeWith(t *testing.T, opts probeOptions) *probe {
	t.Helper()
	binary := os.Getenv("REQWEAVE_CODEX_BIN")
	if binary == "" {
		binary = defaultCodexBinary
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatalf("同梱物の場所を解決できません: %v", err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("同梱する版の Codex がありません（`make codex` で用意する。別の実行ファイルを使うときは "+
			"REQWEAVE_CODEX_BIN で指定する）: %v", err)
	}

	base := opts.base
	if base == "" {
		base = t.TempDir()
	}
	origLocation, origSecret, origChatGPT := workspaceLocationFn, baseURLSecretKey, baseURLChatGPT
	workspaceLocationFn = func() (workspacePaths, error) {
		return workspacePaths{
			root: filepath.Join(base, "codex"),
			lock: filepath.Join(base, "codex.lock"),
		}, nil
	}
	if opts.auth == authChatGPTSignin {
		baseURLChatGPT = opts.baseURL
	} else {
		baseURLSecretKey = opts.baseURL
	}
	t.Cleanup(func() {
		workspaceLocationFn, baseURLSecretKey, baseURLChatGPT = origLocation, origSecret, origChatGPT
	})

	ws, err := acquireWorkspace()
	if err != nil {
		t.Fatalf("一時領域を確保できません: %v", err)
	}

	cfg := launchConfig{
		auth:        opts.auth,
		codexHome:   ws.home(),
		workDir:     ws.work(),
		catalogPath: ws.catalog(),
	}
	if opts.prepareHome != nil {
		opts.prepareHome(t, ws.home())
	}
	args := cfg.launchArgs()
	if opts.storeOverride != "" {
		args = replaceCredentialsStoreArg(t, args, cfg, opts.storeOverride)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = ws.work()
	cmd.Env = cfg.launchEnv([]string{
		"HOME=" + os.Getenv("HOME"), // keyring と skill の検出に要る（差し替えると macOS で keyring が使えなくなる）
		// モデルの呼び出し以外の外部接続の受け皿（誰も待ち受けていないアドレス）。
		// **本物の chatgpt.com へ出ていかないことの担保**（`account/rateLimits/read` は
		// 独自プロバイダの base_url ではなく chatgpt.com へ行く = 本ファイルの実測）。
		"HTTPS_PROXY=http://" + deadAddress(t),
		"NO_PROXY=127.0.0.1,localhost",
	})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("標準入力を開けません: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("標準出力を開けません: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Codex App Server を起動できません: %v", err)
	}

	p := &probe{t: t, ws: ws, cfg: cfg, opts: opts, cmd: cmd, subs: map[string]chan rpcNote{}}
	p.rpc = newRPCClient(stdin, stdout, rpcHandlers{
		onNotification: p.handleNotification,
		onRequest: func(id int64, method string, _ json.RawMessage) {
			// 抑止設定の下では起きない。届いたら記録して断る。
			p.mu.Lock()
			p.requests = append(p.requests, method)
			p.mu.Unlock()
			_ = p.rpc.respondError(id, -32601, "not supported")
		},
	})
	t.Cleanup(p.shutdown)

	p.initialize()
	return p
}

// shutdown は子プロセスを止めて一時領域を消す（本番の stop と同じ順序。2 回呼んでも安全）。
func (p *probe) shutdown() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	_ = p.rpc.closeStdin()
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-done
	}
	_ = p.ws.release()
}

// replaceCredentialsStoreArg は認証情報の保存先の起動引数**だけ**を差し替える。
//
// 差し替えは 1 か所に限る（他の引数が巻き添えで変わっていないことを件数で固定する）。
func replaceCredentialsStoreArg(t *testing.T, args []string, cfg launchConfig, store string) []string {
	t.Helper()
	from := fmt.Sprintf("cli_auth_credentials_store=%q", cfg.credentialsStore())
	to := fmt.Sprintf("cli_auth_credentials_store=%q", store)
	out := append([]string(nil), args...)
	replaced := 0
	for i, arg := range out {
		if arg == from {
			out[i] = to
			replaced++
		}
	}
	if replaced != 1 {
		t.Fatalf("保存先の起動引数を 1 つだけ差し替えられません（%d 件。本番の引数が変わった可能性）", replaced)
	}
	return out
}

// verifyLaunchedConfig は `config/read` の実効値を本番の期待値と照合する。
//
// 保存先を差し替えているときは、その 1 項目だけを差し替え後の値で照合する（他はすべて本番の期待値）。
func (p *probe) verifyLaunchedConfig(config map[string]any) {
	p.t.Helper()
	if p.opts.storeOverride == "" {
		if err := p.cfg.verifyEffectiveConfig(config); err != nil {
			p.t.Fatalf("本番の起動設定になっていません: %v", err)
		}
		return
	}
	verifyEffectiveConfigExcept(p.t, p.cfg, config,
		map[string]any{"cli_auth_credentials_store": p.opts.storeOverride})
}

// verifyEffectiveConfigExcept は本番の期待値のうち、指定した項目だけを別の期待値に置き換えて照合する。
func verifyEffectiveConfigExcept(t *testing.T, cfg launchConfig, config map[string]any, replaced map[string]any) {
	t.Helper()
	var mismatches []string
	seen := map[string]bool{}
	for _, exp := range cfg.expectations() {
		want := exp.want
		if alt, ok := replaced[exp.path]; ok {
			want, seen[exp.path] = alt, true
		}
		got, ok := lookupPath(config, exp.path)
		if !ok {
			mismatches = append(mismatches, exp.path+": 実効値に現れない")
			continue
		}
		if !sameConfigValue(got, want) {
			mismatches = append(mismatches, fmt.Sprintf("%s: %s（期待 %s）",
				exp.path, formatConfigValue(got), formatConfigValue(want)))
		}
	}
	for path := range replaced {
		if !seen[path] {
			mismatches = append(mismatches, path+": 本番の照合対象に無い（差し替えの指定が古い）")
		}
	}
	if len(mismatches) > 0 {
		t.Fatalf("起動設定が期待どおりでありません: %s", strings.Join(mismatches, " / "))
	}
}

// newSignInProbe は偽のトークンで再現した ChatGPT のアカウントの子プロセスを起こす。
func newSignInProbe(t *testing.T, baseURL string, tokens fakeChatGPTTokens) *probe {
	t.Helper()
	return newProbeWith(t, probeOptions{
		auth:          authChatGPTSignin,
		baseURL:       baseURL,
		storeOverride: "file",
		prepareHome:   func(t *testing.T, home string) { tokens.write(t, home) },
	})
}

// ---------------------------------------------------------------------------
// 記録用サーバ（偽 Responses API。要求のヘッダ・本文を全件残す）
// ---------------------------------------------------------------------------

// recordedCall は記録用サーバが受け取った要求 1 件。
type recordedCall struct {
	method  string
	path    string
	headers http.Header
	body    map[string]any
	raw     string
}

// scriptedReply は 1 件の応答（付ける応答ヘッダと SSE の事象）。
type scriptedReply struct {
	headers map[string]string
	events  []map[string]any
}

// recordServer は要求を全件記録し、指示された応答を返す偽 Responses API。
//
// gateWidth を 2 以上にすると、その数の要求がそろうまで応答を返さない（同時実行の関門）。
type recordServer struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	calls    []recordedCall
	inFlight int
	arrived  int
	// overlapped は要求が同時に処理中になったか。
	overlapped bool
	order      []string

	gateWidth int
	gate      chan struct{}
	once      sync.Once

	// reply は n 件目（1 起点）の要求への応答を決める。
	reply func(n int, body map[string]any) scriptedReply
}

func newRecordServer(t *testing.T, reply func(n int, body map[string]any) scriptedReply) *recordServer {
	t.Helper()
	if reply == nil {
		reply = func(int, map[string]any) scriptedReply {
			return scriptedReply{events: textReply("ALPHA", defaultAnswer)}
		}
	}
	r := &recordServer{t: t, gate: make(chan struct{}), reply: reply}
	r.server = httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(r.server.Close)
	t.Cleanup(func() { r.once.Do(func() { close(r.gate) }) })
	return r
}

const defaultAnswer = "承知しました。拠点ごとの在庫を確認します。"

func (r *recordServer) handle(w http.ResponseWriter, req *http.Request) {
	body := map[string]any{}
	raw := ""
	if req.Method == http.MethodPost {
		buf := make([]byte, 0)
		chunk := make([]byte, 32*1024)
		for {
			n, err := req.Body.Read(chunk)
			buf = append(buf, chunk[:n]...)
			if err != nil {
				break
			}
		}
		raw = string(buf)
		_ = json.Unmarshal(buf, &body)
	}
	r.mu.Lock()
	r.calls = append(r.calls, recordedCall{
		method: req.Method, path: req.URL.Path, headers: req.Header.Clone(), body: body, raw: raw,
	})
	n := 0
	for _, c := range r.calls {
		if c.method == http.MethodPost {
			n++
		}
	}
	r.mu.Unlock()

	if !strings.HasSuffix(strings.TrimSuffix(req.URL.Path, "/"), "responses") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	label := ""
	if turn, ok := turnForBody(raw); ok {
		label = turn.label
	}
	r.mu.Lock()
	r.inFlight++
	r.arrived++
	r.order = append(r.order, label)
	if r.gateWidth > 0 && r.inFlight >= r.gateWidth {
		r.overlapped = true
		r.once.Do(func() { close(r.gate) })
	}
	r.mu.Unlock()

	if r.gateWidth > 0 {
		select {
		case <-r.gate:
		case <-time.After(concurrencyGate):
		}
	}

	scripted := r.reply(n, body)
	w.Header().Set("Content-Type", "text/event-stream")
	for k, v := range scripted.headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, ev := range scripted.events {
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], payload)
		if flusher != nil {
			flusher.Flush()
		}
	}

	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()
}

// posts は記録した POST の要求だけを返す。
func (r *recordServer) posts() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedCall
	for _, c := range r.calls {
		if c.method == http.MethodPost {
			out = append(out, c)
		}
	}
	return out
}

func (r *recordServer) waitArrived(n int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := r.arrived
		r.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// textReply は本文だけを返す SSE の事象列。
func textReply(id, text string) []map[string]any {
	message := map[string]any{
		"type": "message", "role": "assistant", "id": "msg_" + id, "status": "completed",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
	}
	return []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_" + id}},
		{"type": "response.output_item.added", "output_index": 0,
			"item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id,
				"status": "in_progress", "content": []any{}}},
		{"type": "response.output_text.delta", "item_id": "msg_" + id, "output_index": 0,
			"content_index": 0, "delta": text},
		{"type": "response.output_item.done", "output_index": 0, "item": message},
		{"type": "response.completed", "response": map[string]any{"id": "resp_" + id, "usage": map[string]any{
			"input_tokens": 1234, "input_tokens_details": map[string]any{"cached_tokens": 0},
			"output_tokens": 56, "output_tokens_details": map[string]any{"reasoning_tokens": 7},
			"total_tokens": 1290,
		}}},
	}
}

// viewImageReply は「モデルが作業フォルダ外の画像の読み取りを求めた」状態を作る
// （道具の定義が無くても呼び出しは返せる。実測）。
func viewImageReply(id, path string) []map[string]any {
	args, _ := json.Marshal(map[string]any{"path": path})
	item := map[string]any{
		"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id,
		"name": "view_image", "arguments": string(args), "status": "completed",
	}
	return []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_" + id}},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": map[string]any{"id": "resp_" + id, "usage": map[string]any{
			"input_tokens": 10, "input_tokens_details": map[string]any{"cached_tokens": 0},
			"output_tokens": 1, "output_tokens_details": map[string]any{"reasoning_tokens": 0},
			"total_tokens": 11,
		}}},
	}
}

// ---------------------------------------------------------------------------
// 検査 11: 要求本文の観測（サインイン方式）
// ---------------------------------------------------------------------------

// probeUtterance は 2 方式の突き合わせに使う発話（同じ内容を両方へ送る）。
var probeUtterance = probeTurn{label: "ALPHA", answer: defaultAnswer}

// 偽のトークンで再現した ChatGPT のアカウントで要求本文を観測し、
// (a) 利用者ホームの skill の一覧が 0 件 (b) 作業フォルダ外の画像が 0 件
// (c) シークレットキー方式との差が**認証のヘッダだけ**であることを確かめる。
//
// **実行ごとに変わる識別子（起動ごとの installation-id・スレッド/セッション/要求の ID など）が
// あるため、単純な 2 方式の比較では「方式による差」と「実行ごとの差」を区別できない**。
// そこで**シークレットキー方式を 2 回**走らせて実行ごとの差を先に測り（対照）、
// 方式をまたいだ差がその範囲（＋認証のヘッダ）に収まることを見る。
func TestRealCodexSignInSendsSameRequestAsSecretKeyExceptAuthHeaders(t *testing.T) {
	tokens := newFakeChatGPTTokens()

	// 対照: 同じ方式・同じ発話を 2 回（実行ごとに変わるものを測る）
	firstCall := observeOneTurn(t, probeOptions{auth: authSecretKey})
	secondCall := observeOneTurn(t, probeOptions{auth: authSecretKey})
	// 本番: 偽のトークンで再現した ChatGPT のアカウント
	signInCall := observeOneTurn(t, probeOptions{
		auth: authChatGPTSignin, storeOverride: "file",
		prepareHome: func(t *testing.T, home string) { tokens.write(t, home) },
	})

	// (a)(b) 送ってはならないもの
	for _, forbidden := range []string{
		".agents",                  // 利用者ホームの skill の一覧
		"skills_instructions",      // 同梱 skill の一覧
		"environment_context",      // 端末の環境
		"permissions instructions", // 権限の説明
		"input_image",              // 作業フォルダ外の画像
	} {
		if strings.Contains(signInCall.raw, forbidden) {
			t.Errorf("サインイン方式の要求本文に %q が含まれている", forbidden)
		}
	}

	// (c-1) 本文の差
	runToRun := jsonDiffPaths(firstCall.body, secondCall.body, "")
	crossMethod := jsonDiffPaths(firstCall.body, signInCall.body, "")
	t.Logf("本文の差（同じ方式で 2 回）= %v / （方式をまたいで）= %v", runToRun, crossMethod)

	// 送信内容そのもの（指示文・発話・道具・モデル・推論の強さ・出力の形式）は、
	// 実行ごとにも方式をまたいでも変わってはならない。
	for _, diff := range append(append([]string(nil), runToRun...), crossMethod...) {
		if contentBodyPath(diff) {
			t.Errorf("送信内容が変わっている: %s", diff)
		}
	}
	// 方式をまたいだ差は、同じ方式の 2 回でも出る差だけであること。
	if extra := notIn(crossMethod, runToRun); len(extra) > 0 {
		t.Errorf("認証方式によって要求本文が変わっている: %v", extra)
	}

	// (c-2) ヘッダの差
	runToRunHeaders := headerDiff(firstCall.headers, secondCall.headers)
	crossHeaders := headerDiff(firstCall.headers, signInCall.headers)
	t.Logf("ヘッダの差（同じ方式で 2 回）= %v / （方式をまたいで）= %v",
		sortedHeaderNames(runToRunHeaders), sortedHeaderNames(crossHeaders))
	for name, how := range crossHeaders {
		if authHeaderNames[name] || runToRunHeaders[name] != "" {
			continue
		}
		t.Errorf("認証以外のヘッダが認証方式で違う: %s（%s）", name, how)
	}
	// 認証のヘッダが実際に差し替わっていること（差が 0 件なら観測が成立していない）。
	for _, name := range []string{"authorization", "chatgpt-account-id"} {
		if crossHeaders[name] == "" {
			t.Errorf("認証のヘッダ %s に差が無い（偽のアカウントが効いていない）", name)
		}
		if runToRunHeaders[name] != "" {
			t.Errorf("認証のヘッダ %s が同じ方式の 2 回でも変わる（対照として使えない）", name)
		}
	}
	if got := signInCall.headers.Get("Authorization"); got != "Bearer "+tokens.access {
		t.Errorf("サインイン方式の認証ヘッダが偽のアクセストークンでない（長さ %d）", len(got))
	}
	if got := signInCall.headers.Get("ChatGPT-Account-ID"); got != fakeChatGPTAccountID {
		t.Errorf("アカウントの識別子が違う: %q", got)
	}
	// シークレットキー方式ではアカウントの識別子を付けない。
	if got := firstCall.headers.Get("ChatGPT-Account-ID"); got != "" {
		t.Errorf("シークレットキー方式にアカウントの識別子が付いている: %q", got)
	}
}

// observeOneTurn は子プロセスを 1 つ起こして同じ発話を 1 回送り、モデルへの要求を 1 件返す。
func observeOneTurn(t *testing.T, opts probeOptions) recordedCall {
	t.Helper()
	server := newRecordServer(t, nil)
	opts.baseURL = server.server.URL
	p := newProbeWith(t, opts)
	result := runProbeTurn(t, p, probeUtterance)
	p.shutdown()
	if result.status != "completed" {
		t.Fatalf("ターンが完了していない: %q", result.status)
	}
	posts := server.posts()
	if len(posts) != 1 {
		t.Fatalf("モデルへの要求の件数が違う: %d 件（1 件のはず）", len(posts))
	}
	return posts[0]
}

// contentBodyPaths は「送信内容そのもの」を載せる要求本文の項目（Responses API）。
var contentBodyPaths = []string{"instructions", "input", "tools", "model", "reasoning", "text",
	"tool_choice", "parallel_tool_calls", "include", "store", "stream"}

// contentBodyPath は差分のパスが送信内容そのものを指すか。
func contentBodyPath(diff string) bool {
	path, _, _ := strings.Cut(diff, ":")
	for _, name := range contentBodyPaths {
		if path == name || strings.HasPrefix(path, name+".") || strings.HasPrefix(path, name+"[") {
			return true
		}
	}
	return false
}

// notIn は a のうち b に無いものを返す。
func notIn(a, b []string) []string {
	known := map[string]bool{}
	for _, item := range b {
		known[item] = true
	}
	var out []string
	for _, item := range a {
		if !known[item] {
			out = append(out, item)
		}
	}
	return out
}

// authHeaderNames は認証方式で変わってよいヘッダ（小文字）。
//
// `authorization` は方式そのもの、`chatgpt-account-id` はアカウントの識別子で、
// どちらも「認証のヘッダ」である（「違いは認証のヘッダだけ」の判定）。
var authHeaderNames = map[string]bool{
	"authorization":      true,
	"chatgpt-account-id": true,
}

// headerDiff は 2 つのヘッダの差を「名前 → どう違うか」で返す（値は書かない）。
func headerDiff(a, b http.Header) map[string]string {
	out := map[string]string{}
	names := map[string]bool{}
	for name := range a {
		names[strings.ToLower(name)] = true
	}
	for name := range b {
		names[strings.ToLower(name)] = true
	}
	for name := range names {
		left, right := a.Get(name), b.Get(name)
		switch {
		case left == right:
		case left == "":
			out[name] = "サインイン方式にだけある"
		case right == "":
			out[name] = "シークレットキー方式にだけある"
		default:
			out[name] = "値が違う"
		}
	}
	return out
}

func sortedHeaderNames(diff map[string]string) []string {
	names := make([]string, 0, len(diff))
	for name, how := range diff {
		names = append(names, name+"("+how+")")
	}
	sort.Strings(names)
	return names
}

// jsonDiffPaths は 2 つの JSON の値が違う場所をパスで返す（値そのものは返さない）。
func jsonDiffPaths(a, b any, path string) []string {
	at := func(key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok {
			return []string{path + ": 型が違う"}
		}
		var out []string
		keys := map[string]bool{}
		for k := range left {
			keys[k] = true
		}
		for k := range right {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			lv, lok := left[k]
			rv, rok := right[k]
			switch {
			case !lok:
				out = append(out, at(k)+": サインイン方式にだけある")
			case !rok:
				out = append(out, at(k)+": シークレットキー方式にだけある")
			default:
				out = append(out, jsonDiffPaths(lv, rv, at(k))...)
			}
		}
		return out
	case []any:
		right, ok := b.([]any)
		if !ok {
			return []string{path + ": 型が違う"}
		}
		if len(left) != len(right) {
			return []string{fmt.Sprintf("%s: 件数が違う（%d / %d）", path, len(left), len(right))}
		}
		var out []string
		for i := range left {
			out = append(out, jsonDiffPaths(left[i], right[i], fmt.Sprintf("%s[%d]", path, i))...)
		}
		return out
	default:
		if fmt.Sprintf("%v", a) != fmt.Sprintf("%v", b) {
			return []string{path + ": 値が違う"}
		}
		return nil
	}
}

// runProbeTurn は 1 本の使い捨てスレッドでターンを 1 回行い、失敗ならその場で止める。
func runProbeTurn(t *testing.T, p *probe, turn probeTurn) turnResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out := p.runTurn(ctx, turn)
	if out.failure != "" {
		t.Fatalf("%s のターンが失敗した: %s", turn.label, out.failure)
	}
	return out
}

// 偽のトークンの ChatGPT のアカウントでも、モデルが求めた**作業フォルダ外の画像**は読み取られず、
// 要求本文に画像が 0 件であること（手元のモデル定義が開く前に拒否する）。
func TestRealCodexSignInRefusesImageOutsideWorkspace(t *testing.T) {
	tokens := newFakeChatGPTTokens()
	outside := filepath.Join(t.TempDir(), "outside.png")
	// 本物の PNG（1×1）。読み取られれば要求本文に base64 の画像として現れる。
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatalf("画像を用意できません: %v", err)
	}
	if err := os.WriteFile(outside, png, 0o600); err != nil {
		t.Fatalf("画像を置けません: %v", err)
	}

	server := newRecordServer(t, func(n int, _ map[string]any) scriptedReply {
		if n == 1 {
			return scriptedReply{events: viewImageReply("1", outside)}
		}
		return scriptedReply{events: textReply("2", defaultAnswer)}
	})
	p := newSignInProbe(t, server.server.URL, tokens)
	result := runProbeTurn(t, p, probeUtterance)
	if result.status != "completed" {
		t.Fatalf("ターンが完了していない: %q", result.status)
	}

	posts := server.posts()
	if len(posts) < 2 {
		t.Fatalf("モデルへの要求が %d 件（画像の要求と、その結果を受けた 2 件目が要る）", len(posts))
	}
	for i, call := range posts {
		if strings.Contains(call.raw, "input_image") {
			t.Errorf("%d 件目の要求本文に画像が含まれている", i+1)
		}
		if strings.Contains(call.raw, base64.StdEncoding.EncodeToString(png)) {
			t.Errorf("%d 件目の要求本文に画像の中身が含まれている", i+1)
		}
	}
	// 拒否の理由が「画像入力に対応しないモデルだから」であること
	// （ファイルを開く前の拒否。理由を見ないと、別の理由で送られなかった場合と区別できない）。
	const refusal = "view_image is not allowed because you do not support image inputs"
	if !strings.Contains(posts[1].raw, refusal) {
		t.Errorf("2 件目の要求本文に拒否の文言が無い（別の理由で送られなかった可能性）")
	}
	// 道具の呼び出しに対して Codex から本システムへの承認要求は来ない。
	if reqs := p.serverRequests(); len(reqs) != 0 {
		t.Errorf("Codex から本システムへの要求が届いた: %v", reqs)
	}
}

// ---------------------------------------------------------------------------
// 検査 13: 認証情報の残留（終了前と終了後）
// ---------------------------------------------------------------------------

// credentialPatterns は認証情報の残留を判定する 3 つの形。
//
// **値の有無**を見る（語だけの行は認証情報ではない。実測で `refresh_token_url_override_present` が現れた）。
// JSON がエスケープされて（`\"refresh_token\":\"…\"`）記録されることがあるため、
// 区切りに `\` を含める。
var (
	bearerValuePattern  = regexp.MustCompile(`(?i)bearer[\s:="'\\]+([A-Za-z0-9._\-]{8,})`)
	refreshValuePattern = regexp.MustCompile(`(?i)refresh_token[\s:="'\\]+([A-Za-z0-9._\-]{8,})`)
	jwtShapePattern     = regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{4,}`)
)

// credentialHit は 1 つのファイルで見つかったものの内訳（**値は持たない**）。
type credentialHit struct {
	path    string
	bearer  int
	refresh int
	jwt     int
	token   int // 偽のトークンそのもの（平文・Base64）
}

func (h credentialHit) String() string {
	return fmt.Sprintf("%s{bearer:%d refresh:%d jwt:%d token:%d}",
		h.path, h.bearer, h.refresh, h.jwt, h.token)
}

// scanCredentials は配下の全ファイルを走査し、認証情報の 3 つの形と偽のトークンの件数を数える。
//
// 返すのは**ファイルの相対パスと件数だけ**（値は出力しない）。
func scanCredentials(t *testing.T, root string, tokens fakeChatGPTTokens) []credentialHit {
	t.Helper()
	needles := [][]byte{}
	for _, value := range []string{tokens.access, tokens.id, tokens.refresh} {
		needles = append(needles, keyNeedles(value)...)
	}
	var hits []credentialHit
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil // 実行中に消えるファイルがある
		}
		hit := credentialHit{path: relativeTo(root, path)}
		hit.bearer = len(bearerValuePattern.FindAll(body, -1))
		hit.refresh = len(refreshValuePattern.FindAll(body, -1))
		hit.jwt = len(jwtShapePattern.FindAll(body, -1))
		for _, needle := range needles {
			hit.token += countBytes(body, needle)
		}
		if hit.bearer+hit.refresh+hit.jwt+hit.token > 0 {
			hits = append(hits, hit)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ファイルを走査できません（%s）: %v", root, err)
	}
	return hits
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return filepath.Base(path)
}

func countBytes(body, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	return strings.Count(string(body), string(needle))
}

// **本システムの終了前と終了後**の 2 回、Codex が作るファイルに認証情報が残らないことを確かめる。
//
// 検査の対象は要件のとおり「**本システムが起動した Codex App Server が作成・更新したファイル**」で、
// 一時領域の全ファイルと、利用者の `~/.codex` のうち**この実行で新しくできた・書き換わったもの**である。
// 利用者が以前から持っているファイル（別の用途の Codex CLI の記録・同梱資料など）は対象にしない
// （中身も読まない）。実測では利用者の `~/.codex` にはもともと `Bearer`・`refresh_token`・JWT の形を
// 含む無関係なファイルが多数あり、**全走査にすると要件と関係のない既存データを数えてしまう**。
//
// 偽のトークンで再現しているため、`auth.json`（**本テストが置いたもの**。本番は保存先が
// `keyring` でありこのファイルは作られない）だけは当然トークンを含む。それ以外のファイルが
// 1 つでも当たれば失敗である。
func TestRealCodexSignInLeavesNoCredentialsInFiles(t *testing.T) {
	tokens := newFakeChatGPTTokens()
	base := t.TempDir()
	userCodex := ""
	if home := os.Getenv("HOME"); home != "" {
		userCodex = filepath.Join(home, ".codex")
	}
	before := snapshotTree(t, userCodex)

	server := newRecordServer(t, nil)
	p := newProbeWith(t, probeOptions{
		auth: authChatGPTSignin, baseURL: server.server.URL, base: base,
		storeOverride: "file",
		prepareHome:   func(t *testing.T, home string) { tokens.write(t, home) },
	})
	if result := runProbeTurn(t, p, probeUtterance); result.status != "completed" {
		t.Fatalf("ターンが完了していない: %q", result.status)
	}

	root := filepath.Join(base, "codex")
	// 前提の確認: Codex の動作ログが実際に書かれている（空のフォルダを探して 0 件にしない）。
	// 動作ログには発話本文が残る（既知）。それを見つけられることで、
	// 「走査の対象が本物の動作ログである」ことを示す。
	assertActionLogHasUtterance(t, root)

	// 終了前（一時領域が残っている間）
	planted := filepath.Join("home", authJSONName)
	var unexpected []string
	for _, hit := range scanCredentials(t, root, tokens) {
		if hit.path == planted {
			// 本テストが置いた偽の auth.json。**本番には存在しない**（保存先は keyring）。
			t.Logf("偽の認証情報を置いたファイル（本番には無い）: %s", hit)
			continue
		}
		unexpected = append(unexpected, hit.String())
	}
	if len(unexpected) > 0 {
		t.Errorf("終了前: 一時領域に認証情報を含むファイルがある: %v", unexpected)
	}
	assertUserCodexUntouched(t, userCodex, before, tokens, "終了前")

	// 終了後（一時領域はフォルダごと消える）
	p.shutdown()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("終了後も一時領域が残っている: %v", err)
	}
	if got := scanCredentials(t, base, tokens); len(got) > 0 {
		t.Errorf("終了後: 認証情報を含むファイルが残っている: %v", got)
	}
	assertUserCodexUntouched(t, userCodex, before, tokens, "終了後")
}

// actionLogWait は Codex の動作ログに発話本文が現れるまで待つ上限。
//
// 動作ログは Codex の内部で SQLite へ**非同期に**書かれるため、JSON-RPC のターンが完了した時点で
// 読めているとは限らない。単体で実行すると常に間に合うが、他のパッケージと並行する検証ゲートや
// 負荷の高い環境では間に合わず、**認証情報の走査が「まだ書かれていないファイル」を対象にしてしまう**
// （= 偽緑の入口）。待つのは前提の成立までで、判定そのものは緩めない。
const actionLogWait = 15 * time.Second

// assertActionLogHasUtterance は Codex の動作ログが実在し、発話本文を含むことを確かめる。
//
// 「空のファイルを探して 0 件」を防ぐための前提の確認である（検索の対象が本物であることの担保）。
// 書き出しが非同期のため actionLogWait まで待ち、現れなければ失敗させる。
func assertActionLogHasUtterance(t *testing.T, root string) {
	t.Helper()
	needle := []byte(probeUtterance.label + " の在庫要件を詰めたい。")
	deadline := time.Now().Add(actionLogWait)
	for {
		if found := findActionLogWithUtterance(root, needle); found != "" {
			t.Logf("走査した動作ログ（発話本文を含む）: %s", found)
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("発話本文を含む Codex の動作ログが %s 以内に見つからない（走査の対象が本物であることを示せない）", actionLogWait)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// findActionLogWithUtterance は発話本文を含む動作ログの相対パスを返す（無ければ空文字）。
func findActionLogWithUtterance(root string, needle []byte) string {
	found := ""
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() || found != "" {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "logs_") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(body), string(needle)) {
			found = relativeTo(root, path)
		}
		return nil
	})
	return found
}

// assertUserCodexUntouched は利用者の `~/.codex` に、この実行で作られた・書き換わったファイルが
// 無いことを確かめる（利用者の ~/.codex と ~/.agents は変更しない）。
//
// 変わったものがあれば、その中身だけを認証情報で走査する。
func assertUserCodexUntouched(t *testing.T, root string, before map[string]string, tokens fakeChatGPTTokens, when string) {
	t.Helper()
	if root == "" {
		t.Fatal("利用者のホームが分からない（~/.codex を検査できない）")
	}
	changed := changedSince(t, root, before)
	if len(changed) == 0 {
		return
	}
	t.Errorf("%s: 利用者の ~/.codex のファイルが作成・更新された: %v", when, changed)
	for _, rel := range changed {
		for _, hit := range scanCredentials(t, filepath.Join(root, rel), tokens) {
			t.Errorf("%s: そのファイルに認証情報がある: %s", when, hit)
		}
	}
}

// snapshotTree は配下のファイルの「大きさと更新時刻」を控える（**中身は読まない**）。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if root == "" {
		return out
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[relativeTo(root, path)] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatalf("%s を控えられません: %v", root, err)
	}
	return out
}

// changedSince は控えた時点から新しくできた・書き換わったファイルの相対パスを返す。
func changedSince(t *testing.T, root string, before map[string]string) []string {
	t.Helper()
	var changed []string
	for path, stamp := range snapshotTree(t, root) {
		if before[path] != stamp {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

// 検査が効いていることを、検査自身の入力で固定する（見つけられない検査で 0 件にしない）。
//
// 実測では、`refresh_token` は**記録の項目名**としても現れた
// （`refresh_token_url_override_present`）。**値のある行だけ**を数えることをここで固定する。
func TestCredentialScanFindsValuesAndIgnoresFieldNames(t *testing.T) {
	tokens := newFakeChatGPTTokens()
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("%s を置けません: %v", name, err)
		}
	}
	write("leak_bearer.txt", `{"headers":{"authorization":"Bearer sk-live-EXAMPLE-0123456789"}}`)
	write("leak_bearer_escaped.txt", `"headers=\"authorization\": \"Bearer `+tokens.access+`\""`)
	write("leak_refresh.txt", `{"refresh_token":"`+tokens.refresh+`"}`)
	write("leak_jwt.txt", "cookie: __oailb="+tokens.id)
	write("benign_names.txt", `refresh_token_url_override_present=false bearer_token_present=false`)
	write("benign_null.txt", `{"refresh_token":null,"authorization":""}`)

	byName := map[string]credentialHit{}
	for _, hit := range scanCredentials(t, root, tokens) {
		byName[hit.path] = hit
	}
	if got := byName["leak_bearer.txt"]; got.bearer == 0 {
		t.Errorf("`Bearer` に続く値を見つけられない: %+v", got)
	}
	if got := byName["leak_bearer_escaped.txt"]; got.bearer == 0 {
		t.Errorf("エスケープされた JSON の `Bearer` の値を見つけられない: %+v", got)
	}
	if got := byName["leak_refresh.txt"]; got.refresh == 0 || got.token == 0 {
		t.Errorf("`refresh_token` に続く値を見つけられない: %+v", got)
	}
	if got := byName["leak_jwt.txt"]; got.jwt == 0 {
		t.Errorf("JWT の形を見つけられない: %+v", got)
	}
	if got, found := byName["benign_names.txt"]; found {
		t.Errorf("項目名だけの行を認証情報として数えている: %+v", got)
	}
	if got, found := byName["benign_null.txt"]; found {
		t.Errorf("値の無い項目を認証情報として数えている: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// 検査 15: サインイン方式での同時実行と `account/rateLimits/updated`
// ---------------------------------------------------------------------------

// rateLimitHeaders は Codex が残量として読む応答ヘッダ（同梱版のバイナリに現れる名前）。
func rateLimitHeaders(limitID string, usedPercent, windowMins int, resetAt int64) map[string]string {
	return map[string]string{
		"x-codex-active-limit":           limitID,
		"x-codex-limit-name":             limitID + "-name",
		"x-codex-primary-used-percent":   fmt.Sprintf("%d", usedPercent),
		"x-codex-primary-window-minutes": fmt.Sprintf("%d", windowMins),
		"x-codex-primary-reset-at":       fmt.Sprintf("%d", resetAt),
	}
}

// **サインイン方式でも**スレッドを分ければターンは並行に進む。このとき
// `account/rateLimits/updated` がどう届くか（件数・スレッドへの帰属）を実測する。
func TestRealCodexSignInRunsTwoThreadsConcurrentlyAndReportsRateLimits(t *testing.T) {
	tokens := newFakeChatGPTTokens()
	resetAt := time.Now().Add(3 * time.Hour).Unix()
	// 発話ごとに違う残量を返す（どちらの値が残るか＝帰属が見える）。
	percents := map[string]int{probeTurns[0].label: 11, probeTurns[1].label: 77}

	server := newRecordServer(t, nil)
	server.gateWidth = 2
	server.reply = func(_ int, body map[string]any) scriptedReply {
		raw, _ := json.Marshal(body)
		turn, ok := turnForBody(string(raw))
		if !ok {
			t.Errorf("記録用サーバが見分けられない要求を受け取った")
			return scriptedReply{events: textReply("X", defaultAnswer)}
		}
		return scriptedReply{
			headers: rateLimitHeaders("codex", percents[turn.label], 300, resetAt),
			events:  usageReply(turn),
		}
	}

	p := newSignInProbe(t, server.server.URL, tokens)
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

	server.mu.Lock()
	overlapped, order := server.overlapped, append([]string(nil), server.order...)
	server.mu.Unlock()
	for _, r := range results {
		t.Logf("%s: status=%q thread=%s usage=%+v text=%q failure=%q",
			r.label, r.status, r.threadID, r.usage, r.text, r.failure)
	}
	t.Logf("モデルへの要求が重なったか = %v（届いた順: %v）", overlapped, order)

	for _, r := range results {
		if r.failure != "" {
			t.Fatalf("%s のターンが失敗した: %s", r.label, r.failure)
		}
		if r.status != "completed" {
			t.Fatalf("%s のターンが完了していない: %q", r.label, r.status)
		}
	}
	if !overlapped {
		t.Fatalf("サインイン方式で 2 本のターンが同時に走っていない（到着順: %v）", order)
	}
	for i, r := range results {
		if r.text != probeTurns[i].answer {
			t.Errorf("%s の応答が違う: %q", r.label, r.text)
		}
		if r.usage != probeTurns[i].usage {
			t.Errorf("%s のトークン実績が違う: %+v（want %+v）", r.label, r.usage, probeTurns[i].usage)
		}
	}

	// `account/rateLimits/updated` の届き方（本検査の主目的）。
	notes := p.accountNotifications()
	var updates []aiproviderRateLimitObservation
	for _, note := range notes {
		if note.method != "account/rateLimits/updated" {
			continue
		}
		updates = append(updates, observeRateLimits(t, note.params))
	}
	t.Logf("アカウント系の通知: %d 件 / rateLimits/updated: %d 件 → %+v", len(notes), len(updates), updates)

	if len(updates) == 0 {
		t.Fatal("`account/rateLimits/updated` が 1 件も届かない（残量の更新経路が成立していない）")
	}
	// 1. 通知は**スレッドに属さない**（`threadId` を持たない）。
	//    持たないため、並行に走らせると「最後に届いた値」だけが残る（planUsageStore は 1 つ）。
	for _, u := range updates {
		if u.threadID != "" {
			t.Errorf("rateLimits/updated がスレッドに属している: %q（並行時の帰属の前提が変わる）", u.threadID)
		}
	}
	// 2. 応答ヘッダの値がそのまま届くこと（どちらの発話の値かは届いた順で決まる）。
	want := map[int]bool{percents[probeTurns[0].label]: true, percents[probeTurns[1].label]: true}
	for _, u := range updates {
		if !want[u.usedPercent] {
			t.Errorf("届いた使用率が応答ヘッダの値でない: %d（want %v）", u.usedPercent, want)
		}
		if u.windowMins != 300 {
			t.Errorf("枠の長さが応答ヘッダの値でない: %d", u.windowMins)
		}
	}
	// 3. 本システムが保持する残量は、通知を解釈できる形であること（画面の残量表示の元）。
	last := updates[len(updates)-1]
	if !last.parsed {
		t.Error("最後の通知を残量として解釈できない（PlanUsage が空のままになる）")
	}
}

// aiproviderRateLimitObservation は届いた通知 1 件の中身（観測用）。
type aiproviderRateLimitObservation struct {
	threadID    string
	usedPercent int
	windowMins  int
	parsed      bool
}

func observeRateLimits(t *testing.T, params json.RawMessage) aiproviderRateLimitObservation {
	t.Helper()
	out := aiproviderRateLimitObservation{threadID: threadIDOf(params)}
	usage, ok := parsePlanUsage(params)
	out.parsed = ok
	if ok && len(usage.Windows) > 0 {
		out.usedPercent = usage.Windows[0].UsedPercent
		out.windowMins = usage.Windows[0].WindowDurationMins
	}
	return out
}

// accountNotifications はスレッドに属さないアカウント系の通知を返す。
func (p *probe) accountNotifications() []rpcNote {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rpcNote(nil), p.accountNotes...)
}

// usageReply は発話ごとに別のトークン実績を返す SSE の事象列（concurrency の writeSSE と同じ内容）。
func usageReply(turn probeTurn) []map[string]any {
	events := textReply(turn.label, turn.answer)
	events[len(events)-1] = map[string]any{
		"type": "response.completed", "response": map[string]any{
			"id": "resp_" + turn.label,
			"usage": map[string]any{
				"input_tokens":          turn.usage.input,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens":         turn.usage.output,
				"output_tokens_details": map[string]any{"reasoning_tokens": turn.usage.reasoning},
				"total_tokens":          turn.usage.input + turn.usage.output,
			},
		},
	}
	return events
}

// ---------------------------------------------------------------------------
// 検査 12 の一部: 認証情報が一時領域の**外**（OS セキュアストレージ）に置かれること
// ---------------------------------------------------------------------------

// 保存先 `keyring`（**サインイン方式の本番の設定**）では、
//   - 一時領域をフォルダごと作り直しても同じ場所ならサインインが続く（= 認証情報は一時領域の外にある）
//   - 別の場所で起動すると見つからない（= 項目は CODEX_HOME の場所に結びつく）
//   - サインアウトすると消える（= OS セキュアストレージのエントリが削除される）
//
// **本物の ChatGPT のアカウントは使えない**ため、同じ保存先の仕組みをダミーのキーで確かめる。
// 「ChatGPT のトークンが OS セキュアストレージに入ること」自体は本物のアカウントが要る（未確認）。
func TestRealCodexKeyringEntrySurvivesWorkspaceRecreationAndIsRemovedOnSignOut(t *testing.T) {
	server := newRecordServer(t, nil)
	base := t.TempDir()
	other := t.TempDir()
	// 保存先は `keyring`（サインイン方式の本番の設定）。渡す資格はダミーのキーにする
	// （本物の ChatGPT のアカウントは使えないため。**保存の仕組み**を確かめる検査である）。
	opts := func(at string, noLogin bool) probeOptions {
		return probeOptions{
			auth: authSecretKey, baseURL: server.server.URL, base: at,
			storeOverride: "keyring", noLogin: noLogin,
		}
	}
	// 利用者の OS セキュアストレージに、もともと Codex の項目があるか（補助の確認の前提）。
	entryBefore, hadEntry := codexKeychainEntry(t)
	// 後始末: どの経路で失敗しても、利用者の OS セキュアストレージへ項目を残さない。
	t.Cleanup(func() { signOutQuietly(t, base, server.server.URL) })

	// 1 回目: ダミーのキーでサインインする（initialize が login/start を送る）。
	first := newProbeWith(t, opts(base, false))
	first.shutdown()
	if entry, ok := codexKeychainEntry(t); !hadEntry {
		if !ok {
			t.Error("OS セキュアストレージに Codex の項目ができていない")
		} else {
			t.Logf("OS セキュアストレージにできた項目: %s", entry)
		}
	} else {
		t.Logf("利用者の Codex の項目がもともとある（%s）ため、"+
			"OS セキュアストレージ側の確認は行わない（Codex 側の確認だけで判定する）", entryBefore)
	}

	// 2 回目: 同じ場所（一時領域は acquireWorkspace がフォルダごと作り直す）。
	// キーを渡さずに認証の種類を見る = 認証情報が一時領域の**外**にあることの確認。
	sameSpot := newProbeWith(t, opts(base, true))
	gotType, hasAccount := sameSpot.accountTypeNow()
	sameSpot.shutdown()
	if !hasAccount || gotType != "apiKey" {
		t.Fatalf("一時領域を作り直したらサインインが失われた: type=%q あり=%v"+
			"（認証情報が一時領域の中にあることになる）", gotType, hasAccount)
	}

	// 3 回目: 別の場所。ここでは見つからない（項目は CODEX_HOME の場所に結びつく）。
	elsewhere := newProbeWith(t, opts(other, true))
	otherType, otherHas := elsewhere.accountTypeNow()
	elsewhere.shutdown()
	if otherHas {
		t.Errorf("別の場所で起動してもサインインが見つかる: type=%q（項目が場所に結びついていない）", otherType)
	}

	// 4 回目: 元の場所でサインアウトし、5 回目で消えていることを確かめる。
	signOut := newProbeWith(t, opts(base, true))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	signOut.call(ctx, "account/logout", map[string]any{})
	cancel()
	signOut.shutdown()

	after := newProbeWith(t, opts(base, true))
	afterType, afterHas := after.accountTypeNow()
	after.shutdown()
	if afterHas {
		t.Errorf("サインアウトしても認証情報が残っている: type=%q", afterType)
	}
	if _, ok := codexKeychainEntry(t); ok && !hadEntry {
		t.Error("サインアウトしても OS セキュアストレージの項目が残っている")
	}
}

// codexKeychainEntry は OS セキュアストレージの Codex の項目（名前だけ）を返す。
//
// **値（パスワード）は取り出さない**（`-g` を付けない）。macOS 以外では確認しない。
func codexKeychainEntry(t *testing.T) (string, bool) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return "", false
	}
	out, err := exec.Command("security", "find-generic-password", "-s", "Codex Auth").CombinedOutput()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, `"acct"`) {
			return strings.TrimSpace(line), true
		}
	}
	return "", true
}

// accountTypeNow は今の認証の種類を返す（起動後の検査の (d) と同じ呼び出し）。
func (p *probe) accountTypeNow() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.accountType(ctx)
}

// signOutQuietly は後始末のサインアウト（既に消えていれば何も起きない）。
func signOutQuietly(t *testing.T, base, baseURL string) {
	t.Helper()
	origLocation := workspaceLocationFn
	workspaceLocationFn = func() (workspacePaths, error) {
		return workspacePaths{
			root: filepath.Join(base, "codex"),
			lock: filepath.Join(base, "codex.lock"),
		}, nil
	}
	defer func() { workspaceLocationFn = origLocation }()

	binary := os.Getenv("REQWEAVE_CODEX_BIN")
	if binary == "" {
		binary = defaultCodexBinary
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		return
	}
	ws, err := acquireWorkspace()
	if err != nil {
		return
	}
	defer func() { _ = ws.release() }()
	cfg := launchConfig{auth: authChatGPTSignin, codexHome: ws.home(), workDir: ws.work(), catalogPath: ws.catalog()}
	cmd := exec.Command(binary, cfg.launchArgs()...)
	cmd.Dir = ws.work()
	cmd.Env = cfg.launchEnv([]string{"HOME=" + os.Getenv("HOME")})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	rpc := newRPCClient(stdin, stdout, rpcHandlers{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := rpc.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": clientName, "title": clientTitle, "version": clientVersion()},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err == nil {
		_ = rpc.notify("initialized", map[string]any{})
		_, _ = rpc.call(ctx, "account/logout", map[string]any{})
	}
	_ = rpc.closeStdin()
	_ = cmd.Wait()
}
