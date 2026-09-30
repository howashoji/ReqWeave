package codex

// 本ファイルは単体テスト用の「偽の Codex App Server」。
//
// テストのバイナリ自身を子プロセスとして起動し（`app-server` を第 1 引数に受け取ったら本ファイルの
// fake が動く）、実バイナリと同じ形の JSON-RPC を話す。実バイナリを使う確認は結合テスト
// （adapter_integration_test.go）で行い、ここでは**本システム側の writing（起動設定の組み立て・
// 起動後の検査・イベント変換・中断・後片づけ）**を、故障注入を含めて確かめる。
//
// fake は受け取った起動引数から `config/read` の実効値を組み立てる（渡した設定が実効値へ現れる、
// という実バイナリの性質を写したもの）。実バイナリでの照合は結合テストが受け持つ。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeScenario は fake の振る舞い（一時領域の親フォルダの scenario.json で渡す）。
type fakeScenario struct {
	// Version は `initialize` の userAgent に載せる版（空なら同梱物の定義の版）。
	Version string `json:"version"`
	// DropConfigKeys は `config/read` の実効値から落とすキー（起動後の検査の故障注入）。
	DropConfigKeys []string `json:"drop_config_keys"`
	// AccountType は `account/read` が返す認証の種類（空なら apiKey。"none" で未認証）。
	AccountType string `json:"account_type"`
	// Skills は `skills/list` が返す skill（path → 有効か）。
	Skills []fakeSkill `json:"skills"`
	// SkillsWriteFails は `skills/config/write` を効かせない（無効化できない状態の再現）。
	SkillsWriteFails bool `json:"skills_write_fails"`
	// StderrOnStart は起動直後に標準エラー出力へ書く内容。
	StderrOnStart string `json:"stderr_on_start"`
	// ExitOnStart は起動直後に終了する（起動後の検査の前に終わる場面）。
	ExitOnStart bool `json:"exit_on_start"`
	// Turn はターンの振る舞い。
	Turn fakeTurn `json:"turn"`
	// Account はサインインと残量の振る舞い（定義は fake_account_test.go）。
	Account fakeAccount `json:"account"`
	// HoldReply は指定した要求への応答を、テストが解放の印（fakeReleaseStartReply）を置くまで止める
	// （呼び出しの各段階で「応答より先に中断した」順序を作るため）。
	HoldReply fakeHold `json:"hold_reply"`
}

// fakeHold は応答を止める要求。Method への要求のうち、先頭から Skip 件を通した次の 1 件だけを止める。
type fakeHold struct {
	Method string `json:"method"`
	Skip   int    `json:"skip"`
}

type fakeSkill struct {
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

type fakeTurn struct {
	// Kind は "text"（既定）/ "tool" / "server_request" / "error" / "exit" / "hang" /
	// "hang_ignore_interrupt"。
	Kind string `json:"kind"`
	// Text は返す本文（"text" のとき。空なら既定の文）。
	Text string `json:"text"`
	// Deltas は本文を分けて送る数（既定 1）。
	Deltas int `json:"deltas"`
	// ItemType は "tool" のときに開始する項目の種類。
	ItemType string `json:"item_type"`
	// ErrorInfo は "error" のときの codexErrorInfo（文字列）。
	ErrorInfo string `json:"error_info"`
	// ErrorMessage は "error" のときの message。
	ErrorMessage string `json:"error_message"`
	// Usage はトークン実績（input / output / reasoning）。
	Usage []int `json:"usage"`
	// DelayMs は turn/start の応答を遅らせる時間。
	DelayMs int `json:"delay_ms"`
	// HoldStartReply は turn/start の応答を、テストが解放の印（fakeReleaseStartReply）を置くまで止める。
	// 時間で遅らせるのと違い、「応答より先に中断した」という順序をテストの側で保証できる。
	HoldStartReply bool `json:"hold_start_reply"`
}

// fakeReleaseStartReply は保留した応答（turn/start の HoldStartReply・HoldReply）を解く印（一時領域の親フォルダに置く）。
const fakeReleaseStartReply = "release_start_reply"

// TestMain は、第 1 引数が app-server のときだけ fake として動く（それ以外は通常のテスト）。
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "app-server" {
		runFakeCodex()
		return
	}
	os.Exit(m.Run())
}

// fakeState は fake の実行中の状態。
type fakeState struct {
	scenario fakeScenario
	config   map[string]any
	skills   []fakeSkill
	out      *bufio.Writer
	// recordPath は受け取ったメッセージの記録先（テストが送信内容を確かめるために使う）。
	recordPath string
	threadID   string
	turnID     string
	// held は HoldReply の対象の要求を受けた回数。
	held int
	// writeMu は応答・通知の書き出しを 1 行ずつにする（保留した応答は別のゴルーチンから書くため）。
	writeMu sync.Mutex
	// interrupted はターンの中断を受けたか。
	interrupted chan struct{}
}

func runFakeCodex() {
	home := os.Getenv("CODEX_HOME")
	state := &fakeState{
		config:      configFromArgs(os.Args[2:]),
		out:         bufio.NewWriter(os.Stdout),
		interrupted: make(chan struct{}, 1),
	}
	// 一時領域の親フォルダに置かれた指示書を読む（本システム側の経路には触れない）。
	if body, err := os.ReadFile(filepath.Join(home, "..", "..", "scenario.json")); err == nil {
		_ = json.Unmarshal(body, &state.scenario)
	}
	state.skills = state.scenario.Skills
	state.recordPath = filepath.Join(home, "..", "..", "rpc.jsonl")
	if state.scenario.StderrOnStart != "" {
		fmt.Fprintln(os.Stderr, state.scenario.StderrOnStart)
	}
	if state.scenario.ExitOnStart {
		os.Exit(3)
	}
	for _, key := range state.scenario.DropConfigKeys {
		deletePath(state.config, key)
	}

	reader := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := reader.ReadString('\n')
		if len(strings.TrimSpace(line)) > 0 {
			var msg rpcMessage
			if err := json.Unmarshal([]byte(line), &msg); err == nil {
				state.handle(msg)
			}
		}
		if err != nil {
			return // 標準入力が閉じた = 終了（実バイナリと同じ挙動）
		}
	}
}

func (s *fakeState) write(msg rpcMessage) {
	body, _ := json.Marshal(msg)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.out.Write(append(body, '\n'))
	s.out.Flush()
}

func (s *fakeState) reply(id int64, result any) {
	body, _ := json.Marshal(result)
	s.write(rpcMessage{ID: &id, Result: body})
}

func (s *fakeState) notify(method string, params any) {
	body, _ := json.Marshal(params)
	s.write(rpcMessage{Method: method, Params: body})
}

// record は受け取ったメッセージを 1 行 1 件で残す（テストの検査用）。
func (s *fakeState) record(msg rpcMessage) {
	f, err := os.OpenFile(s.recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	body, _ := json.Marshal(msg)
	f.Write(append(body, '\n'))
}

func (s *fakeState) handle(msg rpcMessage) {
	s.record(msg)
	if hold := s.scenario.HoldReply; msg.ID != nil && hold.Method != "" && msg.Method == hold.Method {
		s.held++
		if s.held == hold.Skip+1 {
			// 保留の間も標準入力を読み続ける（止められたら実バイナリと同じくすぐ終わるため）。
			go func() {
				s.waitReleaseStartReply()
				s.respond(msg)
			}()
			return
		}
	}
	s.respond(msg)
}

// respond は受け取ったメッセージに応える。
func (s *fakeState) respond(msg rpcMessage) {
	if msg.ID == nil {
		if msg.Method == "turn/interrupt" {
			s.signalInterrupted()
		}
		return
	}
	id := *msg.ID
	// サインイン・残量の要求。扱ったものは以下へ流さない（fake_account_test.go）。
	if s.handleAccount(id, msg) {
		return
	}
	switch msg.Method {
	case "initialize":
		version := s.scenario.Version
		if version == "" {
			version, _ = bundledVersion()
		}
		s.reply(id, map[string]any{
			"userAgent":      fmt.Sprintf("%s/%s (Mac OS 26.6.2; arm64) unknown", clientName, version),
			"codexHome":      os.Getenv("CODEX_HOME"),
			"platformFamily": "unix", "platformOs": "macos",
		})
	case "account/login/start":
		s.reply(id, map[string]any{"type": "apiKey"})
	case "account/read":
		switch s.scenario.AccountType {
		case "none":
			s.reply(id, map[string]any{"account": nil, "requiresOpenaiAuth": true})
		case "":
			s.reply(id, map[string]any{"account": map[string]any{"type": "apiKey"}, "requiresOpenaiAuth": true})
		default:
			s.reply(id, map[string]any{
				"account":            map[string]any{"type": s.scenario.AccountType, "planType": "free", "email": "x@example.invalid"},
				"requiresOpenaiAuth": true,
			})
		}
	case "config/read":
		s.reply(id, map[string]any{"config": s.config, "origins": map[string]any{}})
	case "skills/list":
		list := make([]map[string]any, 0, len(s.skills))
		for _, skill := range s.skills {
			list = append(list, map[string]any{
				"name": filepath.Base(filepath.Dir(skill.Path)), "description": "テスト用",
				"path": skill.Path, "scope": "user", "enabled": skill.Enabled,
			})
		}
		s.reply(id, map[string]any{"data": []map[string]any{{"cwd": ".", "skills": list}}})
	case "skills/config/write":
		var params struct {
			Path    string `json:"path"`
			Enabled bool   `json:"enabled"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		if !s.scenario.SkillsWriteFails {
			for i := range s.skills {
				if s.skills[i].Path == params.Path {
					s.skills[i].Enabled = params.Enabled
				}
			}
		}
		s.reply(id, map[string]any{})
	case "model/list":
		s.reply(id, map[string]any{"data": []map[string]any{
			{"id": "gpt-5.5", "displayName": "GPT-5.5", "model": "gpt-5.5", "hidden": false,
				"isDefault": true, "description": "", "defaultReasoningEffort": "medium",
				"supportedReasoningEfforts": []any{}},
			{"id": "gpt-5.4-mini", "displayName": "GPT-5.4-Mini", "model": "gpt-5.4-mini", "hidden": false,
				"isDefault": false, "description": "", "defaultReasoningEffort": "medium",
				"supportedReasoningEfforts": []any{}},
		}})
	case "thread/start":
		s.threadID = "thread-1"
		s.reply(id, map[string]any{"thread": map[string]any{"id": s.threadID}, "model": "gpt-5.5"})
	case "thread/inject_items":
		s.reply(id, map[string]any{})
	case "thread/unsubscribe":
		s.reply(id, map[string]any{})
	case "turn/interrupt":
		// 実バイナリと同じく要求として受けて応答し、進行中のターンを interrupted で終わらせる。
		s.signalInterrupted()
		s.reply(id, map[string]any{})
	case "turn/start":
		s.turnID = "turn-1"
		if s.scenario.Turn.DelayMs > 0 {
			time.Sleep(time.Duration(s.scenario.Turn.DelayMs) * time.Millisecond)
		}
		if s.scenario.Turn.HoldStartReply {
			// 保留の間も標準入力を読み続ける（止められたら実バイナリと同じくすぐ終わるため）。
			go func() {
				s.waitReleaseStartReply()
				s.reply(id, map[string]any{"turn": map[string]any{"id": s.turnID, "status": "inProgress"}})
				s.runTurn(msg.Params)
			}()
			return
		}
		s.reply(id, map[string]any{"turn": map[string]any{"id": s.turnID, "status": "inProgress"}})
		go s.runTurn(msg.Params)
	default:
		s.write(rpcMessage{ID: &id, Error: &rpcError{Code: -32601, Message: "method not found: " + msg.Method}})
	}
}

// signalInterrupted は進行中のターンへ中断を知らせる（重ねて届いても 1 回分だけ保つ）。
func (s *fakeState) signalInterrupted() {
	select {
	case s.interrupted <- struct{}{}:
	default:
	}
}

// waitReleaseStartReply は解放の印が置かれるまで待つ（上限つき。印が来なくても応答は返す）。
func (s *fakeState) waitReleaseStartReply() {
	mark := filepath.Join(filepath.Dir(s.recordPath), fakeReleaseStartReply)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mark); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *fakeState) runTurn(params json.RawMessage) {
	turn := s.scenario.Turn
	base := map[string]any{"threadId": s.threadID, "turnId": s.turnID}

	switch turn.Kind {
	case "exit":
		os.Exit(1)
	case "tool":
		itemType := turn.ItemType
		if itemType == "" {
			itemType = "commandExecution"
		}
		s.notify("item/started", merge(base, map[string]any{
			"item": map[string]any{"type": itemType, "id": "item-1"},
		}))
		<-s.interrupted
		s.notify("turn/completed", map[string]any{"threadId": s.threadID,
			"turn": map[string]any{"id": s.turnID, "status": "interrupted"}})
		return
	case "server_request":
		id := int64(9001)
		body, _ := json.Marshal(map[string]any{"threadId": s.threadID, "command": []string{"ls"}})
		s.write(rpcMessage{ID: &id, Method: "item/commandExecution/requestApproval", Params: body})
		<-s.interrupted
		s.notify("turn/completed", map[string]any{"threadId": s.threadID,
			"turn": map[string]any{"id": s.turnID, "status": "interrupted"}})
		return
	case "error":
		info := turn.ErrorInfo
		if info == "" {
			info = "other"
		}
		message := turn.ErrorMessage
		if message == "" {
			message = "失敗しました"
		}
		s.notify("error", merge(base, map[string]any{
			"error":     map[string]any{"message": message, "codexErrorInfo": info},
			"willRetry": false,
		}))
		s.notify("turn/completed", map[string]any{"threadId": s.threadID,
			"turn": map[string]any{"id": s.turnID, "status": "failed",
				"error": map[string]any{"message": message, "codexErrorInfo": info}}})
		return
	case "hang_ignore_interrupt":
		// 中断を受けても終わらない（待ちの上限を超える場面）。
		select {}
	case "hang":
		<-s.interrupted
		s.notify("turn/completed", map[string]any{"threadId": s.threadID,
			"turn": map[string]any{"id": s.turnID, "status": "interrupted"}})
		return
	}

	text := turn.Text
	if text == "" {
		text = "こんにちは。確認しました。"
	}
	// 構造化出力（outputSchema つき）では JSON 文字列を返す（実バイナリと同じ）。
	if strings.Contains(string(params), `"outputSchema"`) && turn.Text == "" {
		text = `{"answer":"ok","note":null}`
	}
	s.notify("item/started", merge(base, map[string]any{
		"item": map[string]any{"type": "userMessage", "id": "item-0"},
	}))
	s.notify("item/started", merge(base, map[string]any{
		"item": map[string]any{"type": "agentMessage", "id": "msg-1", "text": ""},
	}))
	for _, chunk := range split(text, turn.Deltas) {
		s.notify("item/agentMessage/delta", merge(base, map[string]any{"itemId": "msg-1", "delta": chunk}))
	}
	s.notify("item/completed", merge(base, map[string]any{
		"item": map[string]any{"type": "agentMessage", "id": "msg-1", "text": text},
	}))
	usage := turn.Usage
	if len(usage) != 3 {
		usage = []int{1234, 56, 7}
	}
	// 対話の途中で届く残量（指示があるときだけ = fake_account_test.go）。
	s.emitRateLimitsDuringTurn()
	s.notify("thread/tokenUsage/updated", merge(base, map[string]any{
		"tokenUsage": map[string]any{
			"total": map[string]any{"totalTokens": usage[0] + usage[1], "inputTokens": usage[0],
				"cachedInputTokens": 0, "outputTokens": usage[1], "reasoningOutputTokens": usage[2]},
			"last": map[string]any{"totalTokens": usage[0] + usage[1], "inputTokens": usage[0],
				"cachedInputTokens": 0, "outputTokens": usage[1], "reasoningOutputTokens": usage[2]},
			"modelContextWindow": 258400,
		},
	}))
	s.notify("turn/completed", map[string]any{"threadId": s.threadID,
		"turn": map[string]any{"id": s.turnID, "status": "completed"}})
}

func merge(base, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func split(text string, parts int) []string {
	runes := []rune(text)
	if parts <= 1 || parts > len(runes) {
		return []string{text}
	}
	size := len(runes) / parts
	var out []string
	for i := 0; i < parts; i++ {
		start := i * size
		end := start + size
		if i == parts-1 {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
	}
	return out
}

// configFromArgs は起動引数から `config/read` の実効値を組み立てる。
//
// 実バイナリは渡された `-c` と `--disable` を実効値へ反映する（実測）。
// fake はその性質だけを写す（値の解釈は TOML の最小の形に限る）。
func configFromArgs(args []string) map[string]any {
	config := map[string]any{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c":
			if i+1 < len(args) {
				key, value, ok := strings.Cut(args[i+1], "=")
				if ok {
					setPath(config, key, parseTOMLValue(value))
				}
				i++
			}
		case "--disable":
			if i+1 < len(args) {
				setPath(config, "features."+args[i+1], false)
				i++
			}
		}
	}
	return config
}

// deletePath は実効値から 1 項目を落とす（起動後の検査の故障注入）。
func deletePath(root map[string]any, path string) {
	keys := strings.Split(path, ".")
	cur := root
	for _, key := range keys[:len(keys)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, keys[len(keys)-1])
}

func setPath(root map[string]any, path string, value any) {
	keys := strings.Split(path, ".")
	cur := root
	for _, key := range keys[:len(keys)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = value
}

// parseTOMLValue は本システムが渡す形（真偽値・文字列・数値・インラインテーブル）だけを読む。
func parseTOMLValue(value string) any {
	value = strings.TrimSpace(value)
	switch value {
	case "true":
		return true
	case "false":
		return false
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) && len(value) >= 2 {
		return value[1 : len(value)-1]
	}
	if n, err := strconv.ParseFloat(value, 64); err == nil {
		return n
	}
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		table := map[string]any{}
		for _, entry := range splitInlineTable(value[1 : len(value)-1]) {
			key, raw, ok := strings.Cut(entry, "=")
			if !ok {
				continue
			}
			table[strings.TrimSpace(key)] = parseTOMLValue(raw)
		}
		return table
	}
	return value
}

// splitInlineTable はインラインテーブルの項目をカンマで分ける（値に , を含まない前提）。
func splitInlineTable(body string) []string {
	var out []string
	for _, entry := range strings.Split(body, ",") {
		if strings.TrimSpace(entry) != "" {
			out = append(out, strings.TrimSpace(entry))
		}
	}
	return out
}
