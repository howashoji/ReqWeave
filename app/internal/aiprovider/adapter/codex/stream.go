package codex

// 本ファイルは呼び出しの写像。
//
// `StreamMessage` 1 回を、**使い捨てのスレッド 1 つ・ターン 1 つ**に写す
// （Codex はスレッド単位で会話を保持するが、本層は呼び出しをまたいでスレッドを使い回さない）。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// allowedItemTypes は抑止設定の下で現れてよい項目の種類。
// これ以外（commandExecution / fileChange / mcpToolCall / webSearch / imageView 等）は道具の呼び出しであり、
// 抑止設定が効いていない疑いがあるため、ターンを止めて子プロセスも止める。
var allowedItemTypes = map[string]bool{
	"userMessage":  true,
	"agentMessage": true,
	"reasoning":    true,
}

// StreamMessage はストリーミング対話。
//
// 戻り値の error は**送る前に分かる不正**のみ（モデル未指定・発話の並びの不正・変換できないスキーマ）。
// 子プロセスの起動・検査の失敗は、再試行の判断が要るためチャネルの EventError で通知する。
func (a *Adapter) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	if req.Model == "" {
		return nil, permanentError(codeInvalidRequest, "モデルが指定されていません")
	}
	if len(req.Messages) == 0 {
		return nil, permanentError(codeInvalidRequest, "送信するメッセージがありません")
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != aiprovider.RoleUser {
		// Codex のターンは「利用者の発話 1 件」で始まる。
		return nil, permanentError(codeInvalidRequest, "最後のメッセージが利用者の発話ではありません")
	}
	schema, shape, err := convertSchema(req.ResponseSchema)
	if err != nil {
		return nil, permanentError(codeInvalidSchema, err.Error())
	}

	sink, out := aiprovider.NewStream()
	go func() {
		// 自前のゴルーチンのパニックは記録なしでプロセスごと落ちる。
		defer aiprovider.RecoverStreamPanic(sink, ProviderCodex, a.onPanic)
		a.runTurn(ctx, req, schema, shape, sink)
	}()
	return out, nil
}

// runTurn は子プロセスの確保からターンの完了までを行う。
func (a *Adapter) runTurn(ctx context.Context, req aiprovider.ChatRequest,
	schema map[string]any, shape *schemaShape, sink *aiprovider.StreamSink) {

	// 接続確立のタイムアウトは、順番待ち・子プロセスの起動・`thread/start` までに効く。
	connectCtx, cancelConnect := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancelConnect()

	// closeIfInterrupted は、失敗の原因が呼び出し元の ctx が切れたこと（利用者の中断・応答の時間切れ）なら
	// 中断として閉じる。どの段階で切れても中断は中断として返す（上位は分類で再試行・表示を決めるため）。
	// 呼び出し元が生きているのに切れたもの（接続確立のタイムアウト）はここでは扱わない。
	closeIfInterrupted := func() bool {
		if ctx.Err() == nil {
			return false
		}
		sink.Done(true)
		return true
	}

	proc, release, err := procManager.begin(connectCtx, a.processKey(), a.keys, a.rec)
	if err != nil {
		if closeIfInterrupted() {
			return
		}
		sink.Fail(a.connectError(ctx, err))
		return
	}
	defer release()

	// 毎回の `thread/start` の直前に skill を確かめ直す（実行中に利用者が skill を足した場合に備える）。
	if err := proc.disableSkills(connectCtx); err != nil {
		_ = proc.stop("skill を無効にできなかったため")
		if closeIfInterrupted() {
			return
		}
		sink.Fail(toProviderError(err))
		return
	}

	threadID, err := proc.startThread(connectCtx, req)
	if err != nil {
		if closeIfInterrupted() {
			return
		}
		sink.Fail(a.connectError(ctx, err))
		return
	}
	defer proc.unsubscribeThread(threadID)

	if err := proc.injectHistory(ctx, threadID, req.Messages); err != nil {
		if closeIfInterrupted() {
			return
		}
		sink.Fail(toProviderError(err))
		return
	}

	session := newTurnSession(threadID, shape != nil)
	proc.setSession(session)
	defer func() {
		proc.setSession(nil)
		session.close()
	}()

	turnID, err := proc.startTurn(ctx, threadID, req, schema)
	if err != nil {
		// 応答を上限まで待っても来なかったときは、Codex 側のターンを止められないので子プロセスを止める。
		if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			go proc.stop("中断が時間内に完了しなかったため")
		}
		if closeIfInterrupted() {
			return
		}
		sink.Fail(toProviderError(err))
		return
	}
	session.setTurnID(turnID)

	session.run(ctx, proc, sink, shape)
}

// connectError は子プロセスの確保・`thread/start` までの失敗を正規化する。
func (a *Adapter) connectError(parent context.Context, err error) *aiprovider.ProviderError {
	if parent.Err() == nil && (err == context.DeadlineExceeded || err == context.Canceled) {
		// 親が生きているのに切れたのは接続確立のタイムアウト。
		return transientError(codeProcessExited, "Codex App Server の準備が時間内に終わりませんでした")
	}
	return toProviderError(err)
}

// toProviderError は正規化済みのエラーをそのまま返し、それ以外は設定起因として扱う。
//
// ここへ来る「正規化されていないエラー」はキーの取得の失敗（キー未設定など）であり、
// 再試行しても解消しない。分類の分からないものを一時的にすると再試行で悪化するため、
// 抽象化層の共通の扱い（aiprovider の再試行層）と同じく設定起因へ倒す。
func toProviderError(err error) *aiprovider.ProviderError {
	var pErr *aiprovider.ProviderError
	if asProviderError(err, &pErr) {
		return pErr
	}
	return configError("", err.Error())
}

// startThread は使い捨てのスレッドを作る。
func (p *process) startThread(ctx context.Context, req aiprovider.ChatRequest) (string, error) {
	raw, err := p.rpc.call(ctx, "thread/start", map[string]any{
		// 会話の全文記録・スレッドの記録を作らせない
		"ephemeral": true,
		// プロジェクトのフォルダを渡さない（一時領域の空フォルダ）
		"cwd":            p.cfg.workDir,
		"sandbox":        "read-only",
		"approvalPolicy": "never",
		// Codex 既定の指示文（21,335 文字）を置き換える
		"baseInstructions": req.System,
		"model":            req.Model,
	})
	if err != nil {
		return "", p.callError(err)
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Thread.ID == "" {
		return "", permanentError(codeUnexpected, "Codex がスレッドを返しませんでした")
	}
	return result.Thread.ID, nil
}

// injectHistory は最後の利用者の発話より前の発話をスレッドの履歴へ入れる。
func (p *process) injectHistory(ctx context.Context, threadID string, messages []aiprovider.Message) error {
	history := messages[:len(messages)-1]
	if len(history) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(history))
	for _, m := range history {
		contentType := "input_text"
		role := "user"
		if m.Role == aiprovider.RoleAssistant {
			contentType = "output_text"
			role = "assistant"
		}
		items = append(items, map[string]any{
			"type": "message", "role": role,
			"content": []map[string]any{{"type": contentType, "text": m.Content}},
		})
	}
	if _, err := p.rpc.call(ctx, "thread/inject_items", map[string]any{
		"threadId": threadID, "items": items,
	}); err != nil {
		return p.callError(err)
	}
	return nil
}

// startTurn はターンを開始する。
//
// 最大出力トークンは**送らない**（`turn/start` に指定が無い）。
func (p *process) startTurn(ctx context.Context, threadID string, req aiprovider.ChatRequest,
	schema map[string]any) (string, error) {

	last := req.Messages[len(req.Messages)-1]
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{{
			"type": "text", "text": last.Content, "text_elements": []any{},
		}},
	}
	// 推論努力レベル。空文字は非対応モデルでの縮退 = 送らない。
	if req.Effort.ReasoningLevel != "" {
		params["effort"] = req.Effort.ReasoningLevel
	}
	if schema != nil {
		params["outputSchema"] = schema
	}
	// turn/start は送った時点で Codex 側のターンが始まりうる。中断されても応答（ターンの ID）までは待つ。
	// 待たずに諦めると、turn/interrupt を送る先が分からず、ターンが Codex 側で走り続ける。
	callCtx, cancelCall := graceAfter(ctx, interruptGrace)
	defer cancelCall()
	raw, err := p.rpc.call(callCtx, "turn/start", params)
	if err != nil {
		return "", p.callError(err)
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", permanentError(codeUnexpected, "Codex がターンを返しませんでした")
	}
	return result.Turn.ID, nil
}

// graceAfter は parent が切れてから grace だけ遅れて切れる context を返す。
//
// parent が切れる前は parent の値を引き継ぎ、切れない。返した cancel は必ず呼ぶ。
func graceAfter(parent context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	stop := context.AfterFunc(parent, func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancel()
		case <-ctx.Done():
		}
	})
	return ctx, func() {
		stop()
		cancel()
	}
}

// unsubscribeThread は使い終わった使い捨てスレッドを手放す（失敗しても呼び出しの結果に影響しない）。
func (p *process) unsubscribeThread(threadID string) {
	ctx, cancel := context.WithTimeout(context.Background(), interruptGrace)
	defer cancel()
	_, _ = p.rpc.call(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID})
}

// callError は要求の失敗を正規化する（JSON-RPC の誤り応答・接続の切断・待ちの打ち切り）。
func (p *process) callError(err error) error {
	var rpcErr *rpcError
	if asRPCError(err, &rpcErr) {
		return normalizeRPCError(rpcErr)
	}
	if isRPCClosed(err) {
		return transientError(codeProcessExited, p.exitDetail())
	}
	return err
}

// sessionEvent は進行中のターンへ届く出来事。
type sessionEvent struct {
	method string
	params json.RawMessage
	// internal は本システム側で起きたこと（子プロセスの終了・道具の検知）。
	internal string
	detail   string
}

const (
	internalProcessExited = "process_exited"
	internalToolAttempt   = "tool_attempt"
)

// turnSession は進行中のターン 1 つ（同時に 1 つだけ）。
type turnSession struct {
	threadID   string
	structured bool

	mu     sync.Mutex
	turnID string

	events    chan sessionEvent
	done      chan struct{}
	closeOnce sync.Once
}

func newTurnSession(threadID string, structured bool) *turnSession {
	return &turnSession{
		threadID:   threadID,
		structured: structured,
		events:     make(chan sessionEvent, 64),
		done:       make(chan struct{}),
	}
}

func (s *turnSession) setTurnID(id string) {
	s.mu.Lock()
	s.turnID = id
	s.mu.Unlock()
}

func (s *turnSession) currentTurnID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnID
}

func (s *turnSession) close() { s.closeOnce.Do(func() { close(s.done) }) }

// push は出来事を渡す（ターンが終わっていれば捨てる）。
func (s *turnSession) push(ev sessionEvent) {
	select {
	case <-s.done:
	case s.events <- ev:
	}
}

// handleNotification は子プロセスからの通知を受ける（読み取りゴルーチンから呼ばれる）。
func (s *turnSession) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "item/started", "item/agentMessage/delta", "item/completed",
		"thread/tokenUsage/updated", "error", "turn/completed":
	default:
		return
	}
	if threadID := threadIDOf(params); threadID != "" && threadID != s.threadID {
		return // 別のスレッドの通知（使い終わったスレッドの残り）は無視する
	}
	s.push(sessionEvent{method: method, params: params})
}

func (s *turnSession) processExited(detail string) {
	s.push(sessionEvent{internal: internalProcessExited, detail: detail})
}

func (s *turnSession) toolAttempt(detail string) {
	s.push(sessionEvent{internal: internalToolAttempt, detail: detail})
}

func threadIDOf(params json.RawMessage) string {
	var head struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &head); err != nil {
		return ""
	}
	return head.ThreadID
}

// run はターンが終わるまで出来事を受け、StreamEvent へ写す。
func (s *turnSession) run(ctx context.Context, proc *process, sink *aiprovider.StreamSink, shape *schemaShape) {
	var finalMessage string
	var lastError *turnError

	for {
		select {
		case ev := <-s.events:
			switch {
			case ev.internal == internalProcessExited:
				sink.Fail(transientError(codeProcessExited, ev.detail))
				return
			case ev.internal == internalToolAttempt:
				proc.interrupt(s.threadID, s.currentTurnID())
				go proc.stop("道具の呼び出しを検知したため")
				sink.Fail(permanentError(codeToolAttempt, ev.detail))
				return
			}
			switch ev.method {
			case "item/started":
				if kind, ok := itemTypeOf(ev.params); ok && !allowedItemTypes[kind] {
					// 抑止設定の下では起きない項目。抑止が効いていない疑いがあるため子プロセスも止める。
					proc.rec.Warn(eventToolAttempt, "Codex App Server が想定外の項目を開始しました",
						map[string]string{"item_type": kind})
					proc.interrupt(s.threadID, s.currentTurnID())
					go proc.stop("道具の呼び出しを検知したため")
					sink.Fail(permanentError(codeToolAttempt, "Codex が "+kind+" を始めました"))
					return
				}
			case "item/agentMessage/delta":
				if s.structured {
					continue // 構造化出力は完了時に変換して 1 回で返す（schema.go の変換を通すため）
				}
				if delta := deltaOf(ev.params); delta != "" {
					sink.Text(delta)
				}
			case "item/completed":
				if text, ok := agentMessageTextOf(ev.params); ok {
					finalMessage = text
				}
			case "thread/tokenUsage/updated":
				if usage, ok := usageOf(ev.params); ok {
					sink.SetUsage(usage)
				}
			case "error":
				if e, willRetry := turnErrorOf(ev.params); e != nil && !willRetry {
					lastError = e
				}
			case "turn/completed":
				status, turnErr := turnResultOf(ev.params)
				switch status {
				case "completed":
					if s.structured {
						body, err := stripAddedNulls([]byte(finalMessage), shape)
						if err != nil {
							sink.Fail(permanentError(codeUnexpected, "応答を組み立て直せません"))
							return
						}
						sink.Text(string(body))
					}
					sink.Done(false)
				case "interrupted":
					sink.Done(true)
				default:
					if turnErr == nil {
						turnErr = lastError
					}
					sink.Fail(normalizeTurnError(turnErr))
				}
				return
			}
		case <-ctx.Done():
			// 利用者の中断または応答完了・無通信のタイムアウト。
			s.finishInterrupted(proc, sink)
			return
		}
	}
}

// finishInterrupted は中断を Codex へ伝え、`interrupted` を待ってから閉じる。
func (s *turnSession) finishInterrupted(proc *process, sink *aiprovider.StreamSink) {
	proc.interrupt(s.threadID, s.currentTurnID())
	deadline := time.After(interruptGrace)
	for {
		select {
		case ev := <-s.events:
			if ev.internal == internalProcessExited {
				sink.Done(true)
				return
			}
			if ev.method == "thread/tokenUsage/updated" {
				// 中断時もそれまでの実績は記録する。
				if usage, ok := usageOf(ev.params); ok {
					sink.SetUsage(usage)
				}
				continue
			}
			if ev.method == "turn/completed" {
				sink.Done(true)
				return
			}
		case <-deadline:
			// 待ちの上限（実装時に決めた値）を超えたら子プロセスを止め、同じ扱いにする。
			go proc.stop("中断が時間内に完了しなかったため")
			sink.Done(true)
			return
		}
	}
}

// interrupt は進行中のターンを止める（応答は待たない）。
func (p *process) interrupt(threadID, turnID string) {
	if threadID == "" || turnID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), interruptGrace)
	defer cancel()
	_, _ = p.rpc.call(ctx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID})
}

func itemTypeOf(params json.RawMessage) (string, bool) {
	var body struct {
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &body); err != nil || body.Item.Type == "" {
		return "", false
	}
	return body.Item.Type, true
}

func deltaOf(params json.RawMessage) string {
	var body struct {
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return ""
	}
	return body.Delta
}

func agentMessageTextOf(params json.RawMessage) (string, bool) {
	var body struct {
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &body); err != nil || body.Item.Type != "agentMessage" {
		return "", false
	}
	return body.Item.Text, true
}

// usageOf はこのスレッドの累計を実績として返す（Codex が付け足した分を含む。トークン上限はこの値で効かせる）。
func usageOf(params json.RawMessage) (aiprovider.TokenUsage, bool) {
	var body struct {
		TokenUsage struct {
			Total struct {
				InputTokens           int `json:"inputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return aiprovider.TokenUsage{}, false
	}
	total := body.TokenUsage.Total
	return aiprovider.TokenUsage{
		InputTokens:     total.InputTokens,
		OutputTokens:    total.OutputTokens,
		ReasoningTokens: total.ReasoningOutputTokens,
	}, true
}

func turnErrorOf(params json.RawMessage) (*turnError, bool) {
	var body struct {
		Error     *turnError `json:"error"`
		WillRetry bool       `json:"willRetry"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return nil, false
	}
	return body.Error, body.WillRetry
}

func turnResultOf(params json.RawMessage) (string, *turnError) {
	var body struct {
		Turn struct {
			Status string     `json:"status"`
			Error  *turnError `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return "", nil
	}
	return strings.ToLower(body.Turn.Status), body.Turn.Error
}
