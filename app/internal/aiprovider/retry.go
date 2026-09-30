package aiprovider

// 本ファイルはタイムアウトと再試行を担う。
// 上位（対話エンジン）は StreamRetrying を通して呼び出し、アダプタの StreamMessage を直接使わない。

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"
)

// 利用者が変更できるタイムアウト。
const (
	DefaultConnectTimeout = 10 * time.Second
	MinConnectTimeout     = 5 * time.Second
	MaxConnectTimeout     = 60 * time.Second

	DefaultResponseTimeout = 300 * time.Second
	MinResponseTimeout     = 60 * time.Second
	MaxResponseTimeout     = 900 * time.Second
)

// 変更不可の実装定数。
const (
	// StreamIdleTimeout はストリーム無通信タイムアウト（チャンク間隔）。
	StreamIdleTimeout = 60 * time.Second
	// MaxRetries は自動再試行の上限。
	MaxRetries = 3
	// RetryAfterMax は RetryAfter 指示に従う上限。これを超える指示では再試行を打ち切る。
	RetryAfterMax = 60 * time.Second

	backoffInitial = 1 * time.Second
	backoffFactor  = 2
	backoffMax     = 30 * time.Second
	backoffJitter  = 0.25
)

// Timeouts はタイムアウトのうち設定で変更できる 2 項目。
//
// 値はアプリ設定（settings.json の ai_timeouts）に端末ごとに保存され、公開バインディング層が読んで注入する。
// 未設定なら既定値で動作する。
type Timeouts struct {
	Connect  time.Duration
	Response time.Duration
}

// DefaultTimeouts はタイムアウトの既定値。
func DefaultTimeouts() Timeouts {
	return Timeouts{Connect: DefaultConnectTimeout, Response: DefaultResponseTimeout}
}

// Normalize は許容範囲外・未設定の値を範囲内へ丸める（接続 5〜60 秒 / 応答完了 60〜900 秒）。
func (t Timeouts) Normalize() Timeouts {
	t.Connect = clampDuration(t.Connect, MinConnectTimeout, MaxConnectTimeout, DefaultConnectTimeout)
	t.Response = clampDuration(t.Response, MinResponseTimeout, MaxResponseTimeout, DefaultResponseTimeout)
	return t
}

func clampDuration(v, min, max, def time.Duration) time.Duration {
	switch {
	case v <= 0:
		return def
	case v < min:
		return min
	case v > max:
		return max
	default:
		return v
	}
}

// streamIdleTimeout は無通信タイムアウトの実効値。テストのみが差し替える
// （公開定数 StreamIdleTimeout が設計値）。
var streamIdleTimeout = StreamIdleTimeout

// jitterFraction は ±backoffJitter の範囲の乱数を返す。テストのみが差し替える。
var jitterFraction = func() float64 { return (rand.Float64()*2 - 1) * backoffJitter }

// sleepFor は待機（テストのみが差し替える）。
var sleepFor = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// backoffDuration は attempt 回目（0 起算）の待機時間を返す（指数・倍率 2・ジッタ ±25%・上限 30 秒）。
func backoffDuration(attempt int) time.Duration {
	base := float64(backoffInitial) * math.Pow(backoffFactor, float64(attempt))
	if base > float64(backoffMax) {
		base = float64(backoffMax)
	}
	d := time.Duration(base * (1 + jitterFraction()))
	if d < 0 {
		d = 0
	}
	return d
}

// retryWait は次の再試行までの待機時間を返す。ok=false のときは再試行を打ち切る。
//
// レート制限で RetryAfter の指示がある場合はバックオフ計算値に代えてその待機時間を用いる。
// 指示が RetryAfterMax を超える場合は再試行を打ち切る。
func retryWait(err *ProviderError, attempt int) (time.Duration, bool) {
	if err != nil && err.RetryAfter > 0 {
		if err.RetryAfter > RetryAfterMax {
			return 0, false
		}
		return err.RetryAfter, true
	}
	return backoffDuration(attempt), true
}

// SendRecorder は送信記録の発行先（実装は監査ログモジュール）。
//
// 本層は SendRecord に載せる値を ChatRequest のフィールドと呼び出し側の文脈ラベルだけから組み立てる。
// 認証ヘッダ・シークレットキー・キー参照名を載せる経路を持たない（送信記録にキーが入らないことを構造で担保する）。
type SendRecorder interface {
	// RecordSend は HTTP 送信の直前に呼ばれ、送信 ID を返す。
	// エラーを返した場合、本層は送信を行わない（記録の残らない送信を作らない）。
	RecordSend(rec SendRecord) (sendID string, err error)
	// RecordUsage は実績受信時に、送信 ID へ紐づく実績を追記する。
	// 実績が得られなかった呼び出しでは呼ばれない（欠測。推定値で代用しない）。
	// 追記の失敗は実装側で扱う（送信は完了しているため対話を止めない）。
	RecordUsage(sendID string, usage TokenUsage)
}

// SendRecord は監査へ渡す送信記録。
type SendRecord struct {
	Provider ProviderID
	Model    string
	Session  string   // S-nnnn。セッション文脈を持つ送信のみ
	Prompt   string   // 送信全文（System + Messages）
	Included []string // 送信文脈の内訳（対話エンジンが付与する文脈種別ラベル）
	// ImportRefs は取り込み分析の送信で対象資料を識別する（送信記録の import_refs）。
	// ChatRequest.ImportRefs をそのまま写す（通常の対話・生成では空）。
	ImportRefs []ImportRef
}

// RecordContext は呼び出し側（対話エンジン）が与える記録用の文脈ラベル。
type RecordContext struct {
	Session  string
	Included []string
}

// StreamOptions は StreamRetrying の設定。
type StreamOptions struct {
	Timeouts Timeouts
	// Recorder は送信記録の発行先。nil のときは記録しない（記録先を持たない疎通確認などで使う）。
	Recorder SendRecorder
	Context  RecordContext
	// OnPanic はストリーミング用ゴルーチンのパニックの記録先（nil なら記録しない）。
	OnPanic PanicRecorder
	// OnFailure は AI 呼び出しが失敗したときの記録先（nil なら記録しない）。
	// 記録先を知るのはバインディング層だけ（本層は動作ログのパッケージへ依存しない = OnPanic と同じ委譲の形）。
	OnFailure FailureRecorder
}

// FailureRecord は AI 呼び出しの失敗を動作ログへ残すための項目。
//
// 項目は AI 通信のエラーで動作ログに残す内容 = 分類・発生源・HTTPStatus・Code・
// 再試行回数・所要時間そのものである。**本文を持たない**（プロンプト・応答・発話・キーは
// 入れない。型に場所が無いことで構造的に担保する）。
type FailureRecord struct {
	Provider   ProviderID    // 発生源
	Class      ErrorClass    // 分類（設定起因 / 一時的 / 恒久的）
	Code       string        // 正規化コード（無いこともある）
	HTTPStatus int           // 呼び出し先が返した状態（送信前の失敗では 0）
	Attempts   int           // 実際に送った回数（初回を含む。送信前の失敗では 0）
	Elapsed    time.Duration // 呼び出しの開始から失敗までの時間
}

// FailureRecorder は失敗の記録先（実体は動作ログ。配線は公開バインディング層）。
type FailureRecorder func(FailureRecord)

// record は失敗を記録する（nil のときは何もしない）。
func (r FailureRecorder) record(perr *ProviderError, attempts int, elapsed time.Duration) {
	if r == nil || perr == nil {
		return
	}
	r(FailureRecord{
		Provider:   perr.Provider,
		Class:      perr.Class,
		Code:       perr.Code,
		HTTPStatus: perr.HTTPStatus,
		Attempts:   attempts,
		Elapsed:    elapsed,
	})
}

// BuildPrompt は送信全文（System + Messages）を監査記録用に組み立てる。
func BuildPrompt(req ChatRequest) string {
	var b strings.Builder
	if req.System != "" {
		fmt.Fprintf(&b, "[system]\n%s\n\n", req.System)
	}
	for _, m := range req.Messages {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", m.Role, m.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

// StreamRetrying はタイムアウト・再試行を適用したストリーミング対話。
//
// 再試行するのは ErrClassTransient のみ。ErrClassConfig / ErrClassPermanent は再試行しない。
// ストリーミング開始後（1 トークン以上受信後）のエラーは再試行せず、受信済み本文を保ったまま
// EventError で通知する（利用者への再実行手段の提示は上位のエラーカタログが行う）。
//
// 返すチャネルの契約は Adapter.StreamMessage と同じ（EventDone / EventError まで読み切る）。
func StreamRetrying(ctx context.Context, a Adapter, req ChatRequest, opts StreamOptions) (<-chan StreamEvent, error) {
	if a == nil {
		return nil, errors.New("アダプタが指定されていません")
	}
	// 同意ゲート。送信記録の発行前・アダプタ呼び出し前に止める
	// （同意のない資料を HTTP で送らないことを構造で担保する）。
	if perr := checkImportConsent(req, a.ID()); perr != nil {
		return nil, perr
	}
	t := opts.Timeouts.Normalize()

	sink, out := NewStream()
	go func() {
		// 自前のゴルーチンのパニックは記録なしでプロセスごと落ちる。
		defer RecoverStreamPanic(sink, a.ID(), opts.OnPanic)
		started := time.Now()
		attempts := 0
		// fail は失敗を動作ログへ記録してから sink を閉じる。
		// **失敗の出口をここ 1 つに絞る**（呼び出し側が記録を取りこぼすと、画面に原因が出ない
		// 回答モードでは切り分け手段が無くなる）。
		fail := func(perr *ProviderError) {
			opts.OnFailure.record(perr, attempts, time.Since(started))
			sink.Fail(perr)
		}
		var sendID string
		for attempt := 0; ; attempt++ {
			// 送信記録は試行ごとに発行する（実際に送った回数ぶん記録が残る）。
			id, err := recordSend(opts, a.ID(), req)
			if err != nil {
				fail(&ProviderError{Class: ErrClassConfig, Provider: a.ID(),
					Message: "送信記録を残せないため送信を中止しました: " + err.Error()})
				return
			}
			sendID = id

			// 応答完了タイムアウト（ストリーミング全体）。
			attemptCtx, cancel := context.WithTimeout(ctx, t.Response)
			ch, err := a.StreamMessage(attemptCtx, req)
			if err != nil {
				cancel()
				// 送信前の失敗（キー取得・入力不正）は再試行しない。
				fail(toProviderError(a.ID(), err))
				return
			}
			attempts++ // 実際に送った回数（動作ログの「再試行回数」の実体）
			res := forward(ctx, cancel, sink, ch)
			cancel()

			switch {
			case res.interrupted:
				recordUsage(opts, sendID, sink) // 中断時も受信済み実績を記録する
				sink.Done(true)                 // 利用者の中断
				return
			case res.err == nil:
				recordUsage(opts, sendID, sink)
				sink.Done(false)
				return
			case res.sawText:
				// ストリーミング開始後のエラーは再試行しない（受信済み本文は sink へ送出済み）。
				recordUsage(opts, sendID, sink)
				fail(res.err)
				return
			case !res.err.Retryable() || attempt >= MaxRetries:
				recordUsage(opts, sendID, sink)
				fail(res.err)
				return
			}
			wait, ok := retryWait(res.err, attempt)
			if !ok {
				fail(res.err)
				return
			}
			if err := sleepFor(ctx, wait); err != nil {
				recordUsage(opts, sendID, sink)
				sink.Done(true) // 待機中の中断
				return
			}
		}
	}()
	return out, nil
}

// recordSend は送信直前の記録を発行する（Recorder 未設定なら何もしない）。
func recordSend(opts StreamOptions, id ProviderID, req ChatRequest) (string, error) {
	if opts.Recorder == nil {
		return "", nil
	}
	return opts.Recorder.RecordSend(SendRecord{
		Provider: id,
		Model:    req.Model,
		Session:  opts.Context.Session,
		Prompt:   BuildPrompt(req),
		Included: opts.Context.Included,
		// 取り込み分析の送信では対象資料の識別を必ず含める（どの資料を送ったかを後から確かめられるように）。
		ImportRefs: req.ImportRefs,
	})
}

// recordUsage は受信済みの実績を送信記録へ紐づける（実績が無い場合は呼ばない = 欠測）。
func recordUsage(opts StreamOptions, sendID string, sink *StreamSink) {
	if opts.Recorder == nil || sendID == "" {
		return
	}
	u := sink.currentUsage()
	if u == nil {
		return
	}
	opts.Recorder.RecordUsage(sendID, *u)
}

// attemptResult は 1 回の試行の結果。
type attemptResult struct {
	sawText     bool // 1 トークン以上受信したか（再試行可否の判定に使う）
	interrupted bool // 利用者の中断（親 ctx のキャンセル）
	err         *ProviderError
}

// forward はアダプタのイベントを sink へ転送する。
//
// 終了イベント（EventUsage / EventDone / EventError）は転送せず戻り値と sink の実績へ集約する
// （再試行が起きても上位には終了イベントが 1 回だけ届く）。
// チャンク間隔が StreamIdleTimeout を超えた場合は一時的エラーとして打ち切る。
func forward(parent context.Context, stop context.CancelFunc, sink *StreamSink, ch <-chan StreamEvent) attemptResult {
	var res attemptResult
	idle := time.NewTimer(streamIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// 終了イベントを受け取らずにチャネルが閉じた（アダプタの契約違反）。
				if res.err == nil && !res.interrupted {
					res.err = &ProviderError{Class: ErrClassTransient, Message: "応答が途中で終了しました"}
				}
				return res
			}
			switch ev.Kind {
			case EventTextDelta:
				res.sawText = true
				sink.Text(ev.Text)
			case EventUsage:
				if ev.Usage != nil {
					sink.SetUsage(*ev.Usage)
				}
			case EventDone:
				if ev.Usage != nil {
					sink.SetUsage(*ev.Usage)
				}
				if ev.Interrupted {
					// 親 ctx が生きている場合の中断は応答完了タイムアウトであり、
					// 利用者の中断と区別して一時的エラーとして扱う。
					if parent.Err() != nil {
						res.interrupted = true
					} else {
						res.err = &ProviderError{Class: ErrClassTransient, Message: "応答が時間内に完了しませんでした"}
					}
				}
				drain(ch)
				return res
			case EventError:
				res.err = ev.Err
				if res.err == nil {
					res.err = &ProviderError{Class: ErrClassTransient, Message: "原因不明の失敗"}
				}
				drain(ch)
				return res
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(streamIdleTimeout)
		case <-idle.C:
			// 無通信タイムアウト。アダプタ側を止めてから読み切る
			// （止めずに読み切ろうとすると、送信の来ないチャネルで待ち続ける）。
			res.err = &ProviderError{Class: ErrClassTransient, Message: "応答が届かない状態が続きました"}
			stop()
			drain(ch)
			return res
		}
	}
}

// drain はチャネルを読み切る（アダプタ側の goroutine を終了させるため）。
func drain(ch <-chan StreamEvent) {
	for range ch {
	}
}

// toProviderError は任意のエラーを ProviderError へ寄せる。
func toProviderError(id ProviderID, err error) *ProviderError {
	var pErr *ProviderError
	if errors.As(err, &pErr) {
		return pErr
	}
	return &ProviderError{Class: ErrClassConfig, Provider: id, Message: err.Error()}
}

// AdapterOptions はアダプタ生成時の設定（変更可能なタイムアウトなど）。
//
// 未設定のフィールドは Normalize で既定値へ丸まる。
type AdapterOptions struct {
	Timeouts Timeouts
	// OnPanic はアダプタのストリーミング用ゴルーチンのパニックの記録先
	//（nil なら記録しない。配線は公開バインディング層 = エフォートの縮退の OnDegrade と同じ形）。
	OnPanic PanicRecorder
	// OnEvent は子プロセス型アダプタ（Codex App Server など）の動作ログ記録口（nil なら記録しない）。
	// 記録してよい範囲は EventRecorder の定義を参照（やり取りの本文は記録しない）。
	OnEvent EventRecorder
	// AuthMethod は認証方式（アプリ設定の auth_method。空 = secret_key）。
	// 認証方式を持つのは Codex App Server だけで、他のアダプタは無視する。
	AuthMethod AuthMethod
}

// NewHTTPClient は接続確立タイムアウトを適用した HTTP クライアントを返す。
//
// クライアント全体の Timeout は設定しない（ストリーミングの応答完了・無通信の打ち切りは
// StreamRetrying が ctx で行うため。ここで打ち切ると受信済み本文の保全ができない）。
// TLS は既定の検証を用い、無効化の経路を持たない（depcheck の tls-verification で検査する）。
func NewHTTPClient(t Timeouts) *http.Client {
	t = t.Normalize()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: t.Connect, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = t.Connect
	return &http.Client{Transport: transport}
}
