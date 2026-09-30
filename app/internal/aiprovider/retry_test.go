package aiprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// scriptedAdapter は試行ごとに決められたイベント列を流すテスト用アダプタ。
type scriptedAdapter struct {
	scripts [][]StreamEvent
	calls   int
	// hold が非 nil のとき、最初の試行で全イベント送出後に閉じずに待つ（無通信タイムアウトの検証用）。
	hold chan struct{}
	// startErr が非 nil のとき StreamMessage 自体が失敗する。
	startErr error
}

func (s *scriptedAdapter) ID() ProviderID                                  { return ProviderAnthropic }
func (s *scriptedAdapter) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }
func (s *scriptedAdapter) VerifyKey(context.Context) error                 { return nil }

func (s *scriptedAdapter) StreamMessage(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	if s.startErr != nil {
		return nil, s.startErr
	}
	idx := s.calls
	s.calls++
	if idx >= len(s.scripts) {
		idx = len(s.scripts) - 1
	}
	events := s.scripts[idx]
	ch := make(chan StreamEvent, len(events)+1)
	go func() {
		for _, ev := range events {
			ch <- ev
		}
		if s.hold != nil && idx == 0 {
			select {
			case <-s.hold:
			case <-ctx.Done():
			}
			ch <- StreamEvent{Kind: EventDone, Interrupted: true}
		}
		close(ch)
	}()
	return ch, nil
}

func transientError() []StreamEvent {
	return []StreamEvent{{Kind: EventError, Err: &ProviderError{Class: ErrClassTransient, Provider: ProviderAnthropic, HTTPStatus: 503}}}
}

func configError() []StreamEvent {
	return []StreamEvent{{Kind: EventError, Err: &ProviderError{Class: ErrClassConfig, Provider: ProviderAnthropic, HTTPStatus: 401}}}
}

func okScript(text string) []StreamEvent {
	return []StreamEvent{
		{Kind: EventTextDelta, Text: text},
		{Kind: EventUsage, Usage: &TokenUsage{InputTokens: 5, OutputTokens: 7}},
		{Kind: EventDone, Usage: &TokenUsage{InputTokens: 5, OutputTokens: 7}},
	}
}

// stubSleep は待機を記録して即座に返す（テストを実時間で待たせない）。
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	orig := sleepFor
	sleepFor = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	t.Cleanup(func() { sleepFor = orig })
	return &waits
}

// fixJitter はジッタを 0 に固定する。
func fixJitter(t *testing.T) {
	t.Helper()
	orig := jitterFraction
	jitterFraction = func() float64 { return 0 }
	t.Cleanup(func() { jitterFraction = orig })
}

func collectEvents(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var out []StreamEvent
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

// 一時的エラーは自動再試行し、成功すれば利用者の再操作なしに続行する。
func TestRetryOnTransient(t *testing.T) {
	waits := stubSleep(t)
	fixJitter(t)
	a := &scriptedAdapter{scripts: [][]StreamEvent{transientError(), transientError(), okScript("本文")}}

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collectEvents(t, ch)

	if a.calls != 3 {
		t.Errorf("再試行回数が違う: %d", a.calls)
	}
	// 上位へ届く終了イベントは 1 回だけ（再試行の EventError は伝播しない）。
	var done, errs int
	var text string
	for _, ev := range events {
		switch ev.Kind {
		case EventDone:
			done++
		case EventError:
			errs++
		case EventTextDelta:
			text += ev.Text
		}
	}
	if done != 1 || errs != 0 {
		t.Fatalf("終了イベントが 1 回でない: %+v", events)
	}
	if text != "本文" {
		t.Errorf("本文が届いていない: %q", text)
	}
	// 指数バックオフ（初回 1 秒・倍率 2）。
	if len(*waits) != 2 || (*waits)[0] != time.Second || (*waits)[1] != 2*time.Second {
		t.Errorf("バックオフが指数でない: %v", *waits)
	}
}

// ErrClassConfig / ErrClassPermanent は再試行しない。
func TestNoRetryOnNonTransient(t *testing.T) {
	stubSleep(t)
	a := &scriptedAdapter{scripts: [][]StreamEvent{configError(), okScript("届いてはいけない")}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	events := collectEvents(t, ch)

	if a.calls != 1 {
		t.Errorf("設定起因エラーで再試行している: %d 回", a.calls)
	}
	last := events[len(events)-1]
	if last.Kind != EventError || last.Err.Class != ErrClassConfig {
		t.Fatalf("設定起因エラーが伝播していない: %+v", events)
	}
}

// 自動再試行の上限は 3 回。超過後はエラーを提示する。
func TestRetryLimit(t *testing.T) {
	stubSleep(t)
	fixJitter(t)
	a := &scriptedAdapter{scripts: [][]StreamEvent{transientError()}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	events := collectEvents(t, ch)

	if a.calls != MaxRetries+1 {
		t.Errorf("試行回数が違う（初回 + 再試行 %d 回）: %d", MaxRetries, a.calls)
	}
	last := events[len(events)-1]
	if last.Kind != EventError || last.Err.Class != ErrClassTransient {
		t.Fatalf("再試行超過後にエラーが提示されない: %+v", events)
	}
}

// ストリーミング開始後（1 トークン以上受信後）のエラーは再試行しない。
// 受信済み本文は上位へ渡す（中断と同じ扱い）。
func TestNoRetryAfterFirstToken(t *testing.T) {
	stubSleep(t)
	partial := []StreamEvent{
		{Kind: EventTextDelta, Text: "途中まで"},
		{Kind: EventError, Err: &ProviderError{Class: ErrClassTransient, HTTPStatus: 503}},
	}
	a := &scriptedAdapter{scripts: [][]StreamEvent{partial, okScript("再試行してはいけない")}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	events := collectEvents(t, ch)

	if a.calls != 1 {
		t.Errorf("開始後のエラーで再試行している: %d 回", a.calls)
	}
	if events[0].Kind != EventTextDelta || events[0].Text != "途中まで" {
		t.Errorf("受信済み本文が失われている: %+v", events)
	}
	last := events[len(events)-1]
	if last.Kind != EventError {
		t.Fatalf("EventError で閉じていない: %+v", events)
	}
}

// RetryAfter の指示があればバックオフ計算値に代えてその待機時間を用いる。
// 上限（60 秒）を超える指示では再試行を打ち切る。
func TestRetryAfter(t *testing.T) {
	t.Run("指示に従う", func(t *testing.T) {
		waits := stubSleep(t)
		rateLimited := []StreamEvent{{Kind: EventError, Err: &ProviderError{Class: ErrClassTransient, HTTPStatus: 429, RetryAfter: 7 * time.Second}}}
		a := &scriptedAdapter{scripts: [][]StreamEvent{rateLimited, okScript("ok")}}

		ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
		collectEvents(t, ch)

		if len(*waits) != 1 || (*waits)[0] != 7*time.Second {
			t.Errorf("RetryAfter の指示に従っていない: %v", *waits)
		}
	})
	t.Run("上限超過で打ち切り", func(t *testing.T) {
		waits := stubSleep(t)
		tooLong := []StreamEvent{{Kind: EventError, Err: &ProviderError{Class: ErrClassTransient, HTTPStatus: 429, RetryAfter: RetryAfterMax + time.Second}}}
		a := &scriptedAdapter{scripts: [][]StreamEvent{tooLong, okScript("再試行してはいけない")}}

		ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
		events := collectEvents(t, ch)

		if a.calls != 1 {
			t.Errorf("上限超過の指示で再試行している: %d 回", a.calls)
		}
		if len(*waits) != 0 {
			t.Errorf("打ち切りなのに待機している: %v", *waits)
		}
		if events[len(events)-1].Kind != EventError {
			t.Fatalf("エラーで閉じていない: %+v", events)
		}
	})
}

// 無通信が続いた場合は一時的エラーとして打ち切る（既定 60 秒）。
func TestStreamIdleTimeout(t *testing.T) {
	orig := streamIdleTimeout
	streamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = orig })
	stubSleep(t)
	fixJitter(t)

	hold := make(chan struct{})
	defer close(hold)
	a := &scriptedAdapter{
		scripts: [][]StreamEvent{{{Kind: EventTextDelta, Text: "先頭"}}, okScript("ok")},
		hold:    hold,
	}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	events := collectEvents(t, ch)

	last := events[len(events)-1]
	if last.Kind != EventError || last.Err.Class != ErrClassTransient {
		t.Fatalf("無通信タイムアウトが一時的エラーになっていない: %+v", events)
	}
	// 1 トークン受信後のため再試行しない。
	if a.calls != 1 {
		t.Errorf("開始後の無通信で再試行している: %d 回", a.calls)
	}
}

// 利用者の中断（親 ctx のキャンセル）は Interrupted 付き EventDone のまま通す。
func TestInterruptPassthrough(t *testing.T) {
	stubSleep(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &scriptedAdapter{scripts: [][]StreamEvent{{
		{Kind: EventTextDelta, Text: "途中まで"},
		{Kind: EventDone, Interrupted: true},
	}}}

	ch, _ := StreamRetrying(ctx, a, ChatRequest{Model: "m"}, StreamOptions{})
	events := collectEvents(t, ch)

	last := events[len(events)-1]
	if last.Kind != EventDone || !last.Interrupted {
		t.Fatalf("中断が通知されていない: %+v", events)
	}
}

// 応答完了タイムアウト（親 ctx が生きたままの打ち切り）は中断ではなく一時的エラーとして扱う。
func TestResponseTimeoutIsTransient(t *testing.T) {
	stubSleep(t)
	fixJitter(t)
	timedOut := []StreamEvent{{Kind: EventDone, Interrupted: true}}
	a := &scriptedAdapter{scripts: [][]StreamEvent{timedOut, okScript("ok")}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	events := collectEvents(t, ch)

	if a.calls != 2 {
		t.Errorf("応答完了タイムアウトで再試行していない: %d 回", a.calls)
	}
	last := events[len(events)-1]
	if last.Kind != EventDone || last.Interrupted {
		t.Fatalf("再試行後に正常終了していない: %+v", events)
	}
}

// 送信前の失敗（キー取得・入力不正）は再試行せずエラーを返す。
func TestStartErrorNotRetried(t *testing.T) {
	stubSleep(t)
	a := &scriptedAdapter{startErr: errors.New("キーが未設定です"), scripts: [][]StreamEvent{okScript("ok")}}

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{})
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collectEvents(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventError || last.Err.Class != ErrClassConfig {
		t.Fatalf("送信前の失敗が設定起因エラーになっていない: %+v", events)
	}
}

// 設定変更できる 2 項目の許容範囲（5〜60 秒 / 60〜900 秒）。
func TestTimeoutsNormalize(t *testing.T) {
	got := Timeouts{}.Normalize()
	if got != DefaultTimeouts() {
		t.Errorf("未設定が既定値にならない: %+v", got)
	}
	got = Timeouts{Connect: time.Second, Response: 10 * time.Second}.Normalize()
	if got.Connect != MinConnectTimeout || got.Response != MinResponseTimeout {
		t.Errorf("下限へ丸められていない: %+v", got)
	}
	got = Timeouts{Connect: time.Hour, Response: time.Hour}.Normalize()
	if got.Connect != MaxConnectTimeout || got.Response != MaxResponseTimeout {
		t.Errorf("上限へ丸められていない: %+v", got)
	}
	got = Timeouts{Connect: 30 * time.Second, Response: 120 * time.Second}.Normalize()
	if got.Connect != 30*time.Second || got.Response != 120*time.Second {
		t.Errorf("範囲内の値が変えられている: %+v", got)
	}
}

// バックオフは指数（初回 1 秒・倍率 2・上限 30 秒）、ジッタは ±25% に収まる。
func TestBackoffDuration(t *testing.T) {
	fixJitter(t)
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := backoffDuration(i); got != w {
			t.Errorf("attempt=%d の待機が違う: %v（期待 %v）", i, got, w)
		}
	}
	if got := backoffDuration(20); got != backoffMax {
		t.Errorf("上限で頭打ちにならない: %v", got)
	}

	origJitter := jitterFraction
	t.Cleanup(func() { jitterFraction = origJitter })
	for _, f := range []float64{-1, 1} {
		jitterFraction = func() float64 { return f * backoffJitter }
		got := backoffDuration(0)
		lo := time.Duration(float64(time.Second) * (1 - backoffJitter))
		hi := time.Duration(float64(time.Second) * (1 + backoffJitter))
		if got < lo || got > hi {
			t.Errorf("ジッタが ±25%% を超えている: %v", got)
		}
	}
}

// 接続確立タイムアウトを HTTP クライアントへ適用する
// （クライアント全体の Timeout は設定しない = ストリーミングを途中で切らない）。
func TestNewHTTPClient(t *testing.T) {
	c := NewHTTPClient(Timeouts{Connect: 20 * time.Second})
	if c.Timeout != 0 {
		t.Errorf("クライアント全体のタイムアウトが設定されている: %v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport の型が違う: %T", c.Transport)
	}
	if tr.TLSHandshakeTimeout != 20*time.Second {
		t.Errorf("接続確立タイムアウトが適用されていない: %v", tr.TLSHandshakeTimeout)
	}
	if tr.DialContext == nil {
		t.Error("DialContext が設定されていない（接続確立タイムアウトが効かない）")
	}
	// 未設定は既定値（10 秒）へ丸まる。
	def, _ := NewHTTPClient(Timeouts{}).Transport.(*http.Transport)
	if def.TLSHandshakeTimeout != DefaultConnectTimeout {
		t.Errorf("既定値が適用されていない: %v", def.TLSHandshakeTimeout)
	}
}

// fakeRecorder は送信記録の発行先のテスト実装。
type fakeRecorder struct {
	sends  []SendRecord
	usages map[string]TokenUsage
	err    error
	nextID int
}

func newFakeRecorder() *fakeRecorder { return &fakeRecorder{usages: map[string]TokenUsage{}} }

func (f *fakeRecorder) RecordSend(rec SendRecord) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.sends = append(f.sends, rec)
	f.nextID++
	return fmt.Sprintf("send-%d", f.nextID), nil
}

func (f *fakeRecorder) RecordUsage(sendID string, usage TokenUsage) {
	f.usages[sendID] = usage
}

// 送信直前に記録を発行し、実績を同じ送信 ID へ紐づける。
func TestRecordSendAndUsage(t *testing.T) {
	rec := newFakeRecorder()
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("本文")}}
	req := ChatRequest{
		Model:    "claude-test",
		System:   "システム指示",
		Messages: []Message{{Role: RoleUser, Content: "こんにちは"}},
	}
	opts := StreamOptions{Recorder: rec, Context: RecordContext{Session: "S-0001", Included: []string{"FR-INV-001"}}}

	ch, err := StreamRetrying(context.Background(), a, req, opts)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collectEvents(t, ch)

	if len(rec.sends) != 1 {
		t.Fatalf("送信記録の件数が違う: %d", len(rec.sends))
	}
	got := rec.sends[0]
	if got.Provider != ProviderAnthropic || got.Model != "claude-test" || got.Session != "S-0001" {
		t.Errorf("宛先・セッションが記録されていない: %+v", got)
	}
	if len(got.Included) != 1 || got.Included[0] != "FR-INV-001" {
		t.Errorf("送信文脈の内訳が記録されていない: %+v", got.Included)
	}
	if !strings.Contains(got.Prompt, "システム指示") || !strings.Contains(got.Prompt, "こんにちは") {
		t.Errorf("送信全文が記録されていない: %q", got.Prompt)
	}
	usage, ok := rec.usages["send-1"]
	if !ok {
		t.Fatalf("実績が送信 ID へ紐づいていない: %+v", rec.usages)
	}
	if usage.InputTokens != 5 || usage.OutputTokens != 7 {
		t.Errorf("実績値が違う: %+v", usage)
	}
}

// 実績が得られなかった呼び出しは欠測とし、実績を記録しない（推定値で代用しない）。
func TestRecordUsageOmittedWhenAbsent(t *testing.T) {
	rec := newFakeRecorder()
	noUsage := []StreamEvent{{Kind: EventTextDelta, Text: "本文"}, {Kind: EventDone}}
	a := &scriptedAdapter{scripts: [][]StreamEvent{noUsage}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{Recorder: rec})
	collectEvents(t, ch)

	if len(rec.sends) != 1 {
		t.Fatalf("送信記録の件数が違う: %d", len(rec.sends))
	}
	if len(rec.usages) != 0 {
		t.Errorf("実績が無いのに記録された: %+v", rec.usages)
	}
}

// 実際に送った回数ぶん送信記録が残る（再試行は送信のたびに記録）。
func TestRecordSendPerAttempt(t *testing.T) {
	stubSleep(t)
	fixJitter(t)
	rec := newFakeRecorder()
	a := &scriptedAdapter{scripts: [][]StreamEvent{transientError(), okScript("本文")}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{Recorder: rec})
	collectEvents(t, ch)

	if len(rec.sends) != 2 {
		t.Errorf("再試行ぶんの送信記録が残っていない: %d 件", len(rec.sends))
	}
	// 実績は最後の送信へ紐づく。
	if _, ok := rec.usages["send-2"]; !ok {
		t.Errorf("実績が最後の送信へ紐づいていない: %+v", rec.usages)
	}
}

// 送信記録を残せない場合は送信しない（記録の残らない送信を作らない）。
func TestSendBlockedWhenRecordFails(t *testing.T) {
	rec := newFakeRecorder()
	rec.err = errors.New("ディスクに書き込めません")
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("送ってはいけない")}}

	ch, _ := StreamRetrying(context.Background(), a, ChatRequest{Model: "m"}, StreamOptions{Recorder: rec})
	events := collectEvents(t, ch)

	if a.calls != 0 {
		t.Errorf("記録に失敗したのに送信された: %d 回", a.calls)
	}
	last := events[len(events)-1]
	if last.Kind != EventError || last.Err.Class != ErrClassConfig {
		t.Fatalf("送信中止が設定起因エラーで通知されない: %+v", events)
	}
}

// 送信記録に載る値は ChatRequest と文脈ラベルのみ
// （認証ヘッダ・キー・キー参照名を載せるフィールドを持たない = 構造的担保）。
func TestSendRecordHasNoSecretFields(t *testing.T) {
	forbidden := []string{"key", "secret", "token", "auth", "header", "credential"}
	rt := reflect.TypeOf(SendRecord{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, f := range forbidden {
			if strings.Contains(name, f) {
				t.Errorf("SendRecord に秘密情報を載せうるフィールドがある: %s", rt.Field(i).Name)
			}
		}
	}
}

// 送信全文は System と全メッセージを含む。
func TestBuildPrompt(t *testing.T) {
	got := BuildPrompt(ChatRequest{
		System: "システム指示",
		Messages: []Message{
			{Role: RoleUser, Content: "質問"},
			{Role: RoleAssistant, Content: "回答"},
		},
	})
	for _, want := range []string{"[system]", "システム指示", "[user]", "質問", "[assistant]", "回答"} {
		if !strings.Contains(got, want) {
			t.Errorf("送信全文に %q が含まれない:\n%s", want, got)
		}
	}
}
