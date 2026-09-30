package codex

// 本ファイルは子プロセスの管理と起動後の検査の実行。
//
// - 起動の時機: 必要になったとき（最初の AI 呼び出し・疎通確認）。本システムの起動時には起動しない。
// - 停止の時機: (a) 本システムの終了 / (b) AIプロバイダ・認証方式・キーの変更 /
//   (c) AI 呼び出しの無い時間が続いたとき / (d) 起動後の検査の不合格・道具の検知・異常時。
// - 同時に動く子プロセスは 1 つ、同時に進むターンも 1 つ（Codex 側の並行の可否は concurrency_integration_test.go で実測する）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/masking"
)

// 実装時に決めた値（括弧内は許容範囲）。**パッケージ内のテストのみが短く差し替える**
// （待ち時間そのものではなく、時間が来たときの振る舞いを確かめるため）。
var (
	// idleStopAfter は AI 呼び出しの無い時間が続いたときに子プロセスを止めるまで（許容 5〜30 分）。
	idleStopAfter = 10 * time.Minute
	// stopGrace は標準入力を閉じてから強制終了するまで（許容 3〜10 秒。実測の終了は 0.01〜0.02 秒）。
	stopGrace = 5 * time.Second
	// interruptGrace は中断を送ってから子プロセスを止めるまで（許容 3〜10 秒）。
	interruptGrace = 5 * time.Second
)

// 動作ログのイベント名（記録するのは子プロセスの起動・終了・検査の結果・エラーの分類に限る）。
const (
	eventProcessStarted = "ai.codex_process_started"
	eventProcessStopped = "ai.codex_process_stopped"
	eventProcessExited  = "ai.codex_process_exited"
	eventGuardFailed    = "ai.codex_guard_failed"
	eventToolAttempt    = "ai.codex_tool_attempt"
)

// processKey は「同じ子プロセスを使い回してよい条件」。
//
// 認証方式・キーへの参照名が変われば別の子プロセスにする（停止の時機 (b)）。
type processKey struct {
	auth   authMethod
	keyRef aiprovider.KeyRef
	// verification は疎通確認のための使い捨て（保存前のキーで起動したもの）。使い回さない。
	verification bool
}

// manager は端末内で 1 つだけ動く子プロセスと、ターンの直列化を持つ。
type manager struct {
	// slot はターンの直列化。容量 1。
	slot chan struct{}
	mu   sync.Mutex
	proc *process
}

var procManager = newManager()

func newManager() *manager {
	m := &manager{slot: make(chan struct{}, 1)}
	m.slot <- struct{}{}
	return m
}

// process は動いている子プロセス 1 つ。
type process struct {
	key processKey
	cfg launchConfig
	ws  *workspace
	cmd *exec.Cmd
	rpc *rpcClient
	rec aiprovider.EventRecorder

	stderr *tailWriter

	readerDone chan struct{}
	cleanup    sync.Once

	mu      sync.Mutex
	dead    bool
	deadErr error
	// stopping は本システムの都合で止めている最中か（警告として記録しないため）。
	stopping bool
	// ready は起動後の検査まで終わったか（標準エラー出力を記録してよいのはこれより前だけ）。
	ready   bool
	session *turnSession
	idle    *time.Timer
}

// begin はターンの順番を取り、必要なら子プロセスを起動して返す。
//
// 待っている間も ctx（接続確立のタイムアウト）が効く。
// 返した release は、呼び出しの終わりに必ず呼ぶ。
func (m *manager) begin(ctx context.Context, key processKey, keys aiprovider.KeyProvider,
	rec aiprovider.EventRecorder) (*process, func(), error) {

	select {
	case <-m.slot:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() {
		m.armIdleTimer()
		select {
		case m.slot <- struct{}{}:
		default:
		}
	}
	proc, err := m.ensure(ctx, key, keys, rec)
	if err != nil {
		release()
		return nil, nil, err
	}
	return proc, release, nil
}

// ensure は使える子プロセスを返す（無い・条件が違う・終わっている場合は起動し直す）。
func (m *manager) ensure(ctx context.Context, key processKey, keys aiprovider.KeyProvider,
	rec aiprovider.EventRecorder) (*process, error) {

	m.mu.Lock()
	current := m.proc
	if current != nil && current.key == key && !current.isDead() && !key.verification {
		m.mu.Unlock()
		return current, nil
	}
	m.proc = nil
	m.mu.Unlock()

	if current != nil {
		_ = current.stop("設定の変更または再起動のため")
	}

	proc, err := startProcess(ctx, key, keys, rec)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.proc = proc
	m.mu.Unlock()
	return proc, nil
}

// forget は終わった子プロセスを管理から外す。
func (m *manager) forget(p *process) {
	m.mu.Lock()
	if m.proc == p {
		m.proc = nil
	}
	m.mu.Unlock()
}

// current は動いている子プロセスを返す（無ければ nil）。
func (m *manager) current() *process {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proc
}

// stopCurrent は動いている子プロセスを止める（本システムの終了・設定の変更 = 停止の時機 (a)(b)）。
func (m *manager) stopCurrent(reason string) error {
	m.mu.Lock()
	p := m.proc
	m.proc = nil
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.stop(reason)
}

// armIdleTimer は AI 呼び出しの無い時間が続いたときの停止を仕掛ける（停止の時機 (c)）。
func (m *manager) armIdleTimer() {
	p := m.current()
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.idle != nil {
		p.idle.Stop()
	}
	p.idle = time.AfterFunc(idleStopAfter, func() {
		if m.current() == p {
			_ = m.stopCurrent("AI 呼び出しの無い時間が続いたため")
		}
	})
	p.mu.Unlock()
}

// startProcess は子プロセスを起動し、起動後の検査に合格したものだけを返す。
func startProcess(ctx context.Context, key processKey, keys aiprovider.KeyProvider,
	rec aiprovider.EventRecorder) (*process, error) {

	if !supportedOS() {
		return nil, permanentError(codeGuardFailed, "この OS では Codex App Server を使えません")
	}
	exe, err := executablePath()
	if err != nil {
		return nil, permanentError(codeGuardFailed, err.Error())
	}
	ws, err := acquireWorkspace()
	if err != nil {
		if errors.Is(err, errWorkspaceBusy) {
			return nil, workspaceBusyError(err.Error())
		}
		return nil, transientError(codeProcessExited, err.Error())
	}

	cfg := launchConfig{
		auth:        key.auth,
		codexHome:   ws.home(),
		workDir:     ws.work(),
		catalogPath: ws.catalog(),
	}
	cmd := exec.Command(exe, cfg.launchArgs()...)
	cmd.Dir = ws.work()
	cmd.Env = cfg.launchEnv(environFn())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = ws.release()
		return nil, transientError(codeProcessExited, "Codex App Server を起動できません")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = ws.release()
		return nil, transientError(codeProcessExited, "Codex App Server を起動できません")
	}
	p := &process{
		key:        key,
		cfg:        cfg,
		ws:         ws,
		cmd:        cmd,
		rec:        rec,
		stderr:     newTailWriter(),
		readerDone: make(chan struct{}),
	}
	cmd.Stderr = p.stderr

	if err := cmd.Start(); err != nil {
		_ = ws.release()
		return nil, transientError(codeProcessExited, "Codex App Server を起動できません")
	}
	p.rpc = newRPCClient(stdin, stdout, rpcHandlers{
		onNotification: p.handleNotification,
		onRequest:      p.handleServerRequest,
		onClose: func(err error) {
			// 「本システムが止めた最中か」は**標準出力が閉じた時点**で捕まえる。
			// 後から止めた場合にまで停止扱いにすると、自分から終わった記録（標準エラー出力の末尾を含む）が
			// 消える（起動に失敗した子プロセスを止める経路で実際に起きた）。
			stopping := p.isStopping()
			close(p.readerDone)
			go p.handleExit(err, stopping)
		},
	})
	rec.Info(eventProcessStarted, "Codex App Server を起動しました", map[string]string{
		"auth_method": string(key.auth),
	})

	if err := p.initialize(ctx, keys, key); err != nil {
		_ = p.stop("起動後の検査に合格しなかったため")
		return nil, err
	}
	return p, nil
}

// environFn は本システムの環境（パッケージ内のテストのみが差し替える）。
var environFn = defaultEnviron

// initialize は起動から検査までを行う（guard.go の a〜d）。
func (p *process) initialize(ctx context.Context, keys aiprovider.KeyProvider, key processKey) error {
	if _, err := bundledVersion(); err != nil {
		return permanentError(codeGuardFailed, err.Error())
	}
	raw, err := p.rpc.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    clientName,
			"title":   clientTitle,
			"version": clientVersion(),
		},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return p.startupFailure(err)
	}
	var initResult struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(raw, &initResult); err != nil {
		return p.guardFailure("Codex の応答を解釈できません")
	}
	// a. 実行中の Codex が同梱の版であること
	if err := verifyVersion(initResult.UserAgent); err != nil {
		return p.guardFailure(err.Error())
	}

	if err := p.rpc.notify("initialized", map[string]any{}); err != nil {
		return p.startupFailure(err)
	}

	// シークレットキー方式: キーは**標準入力から**渡す（コマンドライン引数・環境変数に置かない。`ps` などで他のプロセスから読めるため）。
	if key.auth == authSecretKey {
		if keys == nil {
			return permanentError(codeGuardFailed, "キーの取得先が設定されていません")
		}
		secret, err := keys.SecretKey(ctx, key.keyRef)
		if err != nil {
			return err // キー未設定・取得失敗は呼び出し側（バインディング）が扱う
		}
		if _, err := p.rpc.call(ctx, "account/login/start", map[string]any{
			"type": "apiKey", "apiKey": secret,
		}); err != nil {
			return p.startupFailure(err)
		}
	}

	// d. 認証の種類が選択中の認証方式と一致すること
	accountRaw, err := p.rpc.call(ctx, "account/read", map[string]any{})
	if err != nil {
		return p.startupFailure(err)
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err := json.Unmarshal(accountRaw, &account); err != nil {
		return p.guardFailure("Codex の応答を解釈できません")
	}
	accountType := ""
	if account.Account != nil {
		accountType = account.Account.Type
	}
	if err := p.cfg.verifyAccountType(accountType, account.Account != nil); err != nil {
		return p.guardFailure(err.Error())
	}

	// b. config/read の実効値が起動引数のとおりであること
	configRaw, err := p.rpc.call(ctx, "config/read", map[string]any{
		"includeLayers": false,
		"cwd":           p.cfg.workDir,
	})
	if err != nil {
		return p.startupFailure(err)
	}
	var configResult struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(configRaw, &configResult); err != nil {
		return p.guardFailure("Codex の応答を解釈できません")
	}
	if err := p.cfg.verifyEffectiveConfig(configResult.Config); err != nil {
		return p.guardFailure(err.Error())
	}

	// c. 利用者ホームの skill が 1 件も有効でないこと（skills.go）
	if err := p.disableSkills(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	p.ready = true
	p.mu.Unlock()
	return nil
}

// guardFailure は起動後の検査の不合格（恒久的。子プロセスは呼び出し側が止める）。
func (p *process) guardFailure(detail string) error {
	p.rec.Warn(eventGuardFailed, "Codex App Server の起動後の検査に合格しませんでした",
		map[string]string{"detail": masking.Mask(detail)})
	return permanentError(codeGuardFailed, detail)
}

// startupFailure は起動の途中で子プロセスが応答しなくなった状態（一時的）。
func (p *process) startupFailure(err error) error {
	if errors.Is(err, errRPCClosed) {
		return transientError(codeProcessExited, p.exitDetail())
	}
	var rpcErr *rpcError
	if errors.As(err, &rpcErr) {
		return normalizeRPCError(rpcErr)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return transientError(codeProcessExited, "Codex App Server の起動が時間内に終わりませんでした")
	}
	return transientError(codeProcessExited, err.Error())
}

// exitDetail は子プロセスが応答しない・終わったことを示す 1 文を返す。
//
// **標準エラー出力の末尾を添えるのは、起動後の検査が終わる前に終わった場合に限る**
// （それ以降は会話の内容が混じりうるため添えない）。
func (p *process) exitDetail() string {
	const base = "Codex App Server が応答しませんでした"
	if p.isReady() {
		return base
	}
	tail := strings.TrimSpace(p.stderr.String())
	if tail == "" {
		return base
	}
	return base + ": " + masking.Mask(tail)
}

// isReady は起動後の検査まで終わっているかを返す。
func (p *process) isReady() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ready
}

// isStopping は本システムの都合で止めている最中かを返す。
func (p *process) isStopping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopping
}

func (p *process) isDead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dead
}

// handleExit は子プロセスが終わったときの後始末（標準出力が閉じた時点で呼ばれる）。
func (p *process) handleExit(readErr error, stopping bool) {
	p.mu.Lock()
	p.dead = true
	p.deadErr = readErr
	session := p.session
	p.mu.Unlock()

	if session != nil {
		session.processExited(p.exitDetail())
	}
	// サインインを待っている呼び出しも起こす（待ち続けさせない）。
	loginTracker.failAll(errRPCClosed)
	procManager.forget(p)
	p.finish()
	if stopping {
		// 本システムが止めた場合は「停止しました」を stop 側が記録する（二重に警告しない）。
		return
	}
	fields := map[string]string{}
	if !p.isReady() {
		// 起動後の検査が終わる前の終了だけ、標準エラー出力の末尾を残す。
		if tail := strings.TrimSpace(p.stderr.String()); tail != "" {
			fields["stderr_tail"] = masking.Mask(tail)
		}
	}
	p.rec.Warn(eventProcessExited, "Codex App Server が終了しました", fields)
}

// stop は子プロセスを止めて一時領域を消す。
func (p *process) stop(reason string) error {
	p.mu.Lock()
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	p.dead = true
	p.stopping = true
	p.mu.Unlock()

	// 標準入力を閉じると Codex は終了する（実測 0.01〜0.02 秒）。
	_ = p.rpc.closeStdin()
	select {
	case <-p.readerDone:
	case <-time.After(stopGrace):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.readerDone
	}
	err := p.finish()
	p.rec.Info(eventProcessStopped, "Codex App Server を停止しました", map[string]string{"reason": reason})
	return err
}

// finish はプロセスの後始末と一時領域の削除（1 回だけ行う）。
func (p *process) finish() error {
	var err error
	p.cleanup.Do(func() {
		_ = p.cmd.Wait()
		err = p.ws.release()
	})
	return err
}

// handleNotification は通知を進行中のターンへ渡す。
//
// アカウント系の通知はターンに属さない（サインインの待ち受け・残量の保持）。
// **残量は上位へのイベントにはしない**。
func (p *process) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "account/login/completed":
		handleLoginCompleted(params)
		return
	case "account/rateLimits/updated":
		handleRateLimitsUpdated(params)
		return
	}
	p.mu.Lock()
	session := p.session
	p.mu.Unlock()
	if session != nil {
		session.handleNotification(method, params)
	}
}

// handleServerRequest は Codex から本システムへの要求を**断る**（道具の実行や承認を Codex に任せないため）。
//
// 抑止設定の下では起きない。起きた場合は抑止設定が効いていない疑いがあるため、
// 進行中のターンを恒久的エラーで止め、子プロセスも止める。
func (p *process) handleServerRequest(id int64, method string, _ json.RawMessage) {
	if strings.Contains(method, "requestApproval") {
		_ = p.rpc.respond(id, map[string]any{"decision": "decline"})
	} else {
		_ = p.rpc.respondError(id, -32601, "not supported")
	}
	p.rec.Warn(eventToolAttempt, "Codex App Server から想定外の要求が届きました",
		map[string]string{"method": method})

	p.mu.Lock()
	session := p.session
	p.mu.Unlock()
	if session != nil {
		session.toolAttempt(fmt.Sprintf("Codex が %s を要求しました", method))
		return
	}
	go p.stop("想定外の要求を受けたため")
}

// setSession は進行中のターンを差し替える（同時に進むターンは 1 つ）。
func (p *process) setSession(s *turnSession) {
	p.mu.Lock()
	p.session = s
	p.mu.Unlock()
}

// tailWriter は標準エラー出力の末尾だけを保つ（全文は保たない）。
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailWriter() *tailWriter { return &tailWriter{max: 2048} }

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = w.buf[len(w.buf)-w.max:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
