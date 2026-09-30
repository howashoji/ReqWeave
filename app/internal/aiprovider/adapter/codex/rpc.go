package codex

// 本ファイルは子プロセスとの JSON-RPC（1 行 1 メッセージ）のやり取り。
//
// 外部ライブラリを使わず標準ライブラリだけで実装する（外部通信と子プロセスの起動を増やさない）。
// **やり取りの本文（発話本文を含む）は記録しない**（動作ログに利用者の発話を残さないため）。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// rpcMessage は送受信する 1 メッセージ（要求・応答・通知を 1 つの型で扱う）。
type rpcMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// rpcError は JSON-RPC の誤り応答。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// rpcHandlers は子プロセスから届くものの受け口。
type rpcHandlers struct {
	// onNotification は通知（応答を返さないもの）。
	onNotification func(method string, params json.RawMessage)
	// onRequest は Codex から本システムへの要求（承認・利用者への問い合わせ等）。
	// 本システムの使い方では起きないはずのものであり、受け口は断るために持つ。
	onRequest func(id int64, method string, params json.RawMessage)
	// onClose は標準出力が閉じた（＝子プロセスが終わった）とき。
	onClose func(err error)
}

// rpcClient は 1 つの子プロセスとのやり取り。
type rpcClient struct {
	stdin io.WriteCloser

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan rpcMessage
	closed   bool
	closeErr error
}

// errRPCClosed は子プロセスが終わったために応答を受け取れない状態（Code は codex_process_exited）。
var errRPCClosed = errors.New("Codex App Server との接続が切れました")

func newRPCClient(stdin io.WriteCloser, stdout io.Reader, handlers rpcHandlers) *rpcClient {
	c := &rpcClient{stdin: stdin, pending: map[int64]chan rpcMessage{}}
	go c.readLoop(stdout, handlers)
	return c
}

// readLoop は 1 行 1 メッセージを読み、応答を待ち手へ、通知・要求を受け口へ渡す。
func (c *rpcClient) readLoop(stdout io.Reader, handlers rpcHandlers) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var readErr error
	for {
		line, err := readLine(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// 解釈できない行は捨てる（本文を記録しない。壊れた 1 行で接続を切らない）。
			continue
		}
		switch {
		case msg.ID != nil && msg.Method != "":
			if handlers.onRequest != nil {
				handlers.onRequest(*msg.ID, msg.Method, msg.Params)
			}
		case msg.ID != nil:
			c.deliver(*msg.ID, msg)
		case msg.Method != "":
			if handlers.onNotification != nil {
				handlers.onNotification(msg.Method, msg.Params)
			}
		}
	}
	c.close(readErr)
	if handlers.onClose != nil {
		handlers.onClose(readErr)
	}
}

// readLine は 1 行を返す（1 行が長くても切らない＝ bufio.Scanner の上限に当たらない）。
func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, more, err := reader.ReadLine()
		line = append(line, chunk...)
		if err != nil {
			return line, err
		}
		if !more {
			return line, nil
		}
	}
}

func (c *rpcClient) deliver(id int64, msg rpcMessage) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- msg
		close(ch)
	}
}

// call は要求を送って応答を待つ。ctx が切れたら待つのをやめる（子プロセスは止めない）。
func (c *rpcClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("要求を組み立てられません: %w", err)
	}

	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = errRPCClosed
		}
		return nil, err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(rpcMessage{ID: &id, Method: method, Params: body}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, errRPCClosed
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// notify は応答を待たない通知を送る（`initialized` など）。
func (c *rpcClient) notify(method string, params any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("通知を組み立てられません: %w", err)
	}
	return c.send(rpcMessage{Method: method, Params: body})
}

// respondError は Codex からの要求を断る。
func (c *rpcClient) respondError(id int64, code int, message string) error {
	return c.send(rpcMessage{ID: &id, Error: &rpcError{Code: code, Message: message}})
}

// respond は Codex からの要求へ結果を返す（承認要求には decline を返す）。
func (c *rpcClient) respond(id int64, result any) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("応答を組み立てられません: %w", err)
	}
	return c.send(rpcMessage{ID: &id, Result: body})
}

func (c *rpcClient) send(msg rpcMessage) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("メッセージを組み立てられません: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errRPCClosed
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("Codex App Server へ送れません: %w", err)
	}
	return nil
}

// close は待ち手をすべて起こして以後の送受信を止める。
func (c *rpcClient) close(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if err != nil {
		c.closeErr = fmt.Errorf("%w: %v", errRPCClosed, err)
	} else {
		c.closeErr = errRPCClosed
	}
	pending := c.pending
	c.pending = map[int64]chan rpcMessage{}
	c.mu.Unlock()

	for _, ch := range pending {
		close(ch)
	}
}

// closeStdin は標準入力を閉じる（子プロセスの終了の合図）。
func (c *rpcClient) closeStdin() error { return c.stdin.Close() }
