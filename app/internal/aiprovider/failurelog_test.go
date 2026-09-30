package aiprovider

// 単体テスト: AI 呼び出しの失敗が動作ログの記録先へ 1 回だけ渡り、
// 記録する内容（分類・発生源・HTTPStatus・Code・再試行回数・所要時間）が揃うこと。
//
// 画面には「原因＋次の行動」の 1 文しか出ず、回答モードでは「システム担当者へ連絡してください」
// しか出ない。この記録が失敗の切り分けの唯一の手掛かりになる。

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// recordFailures は OnFailure に渡った記録を集める。
func recordFailures(opts *StreamOptions) *[]FailureRecord {
	var got []FailureRecord
	opts.OnFailure = func(r FailureRecord) { got = append(got, r) }
	return &got
}

func TestFailureIsRecordedOnceWithClassAndStatus(t *testing.T) {
	a := &scriptedAdapter{scripts: [][]StreamEvent{configError()}}
	opts := StreamOptions{}
	got := recordFailures(&opts)

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{}, opts)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collectEvents(t, ch)

	if len(*got) != 1 {
		t.Fatalf("失敗の記録は 1 件であること: %d 件 %+v", len(*got), *got)
	}
	rec := (*got)[0]
	if rec.Class != ErrClassConfig {
		t.Errorf("分類が違う: %v", rec.Class)
	}
	if rec.Provider != ProviderAnthropic {
		t.Errorf("発生源が違う: %v", rec.Provider)
	}
	if rec.HTTPStatus != 401 {
		t.Errorf("HTTPStatus が違う: %d", rec.HTTPStatus)
	}
	if rec.Attempts != 1 {
		t.Errorf("送った回数が違う: %d（設定起因は再試行しない）", rec.Attempts)
	}
	if rec.Elapsed <= 0 {
		t.Errorf("所要時間が記録されていない: %v", rec.Elapsed)
	}
}

func TestFailureRecordsActualSendCount(t *testing.T) {
	fixJitter(t)
	stubSleep(t)
	// 一時的エラーを返し続ける = 初回 + 再試行 MaxRetries 回。
	a := &scriptedAdapter{scripts: [][]StreamEvent{transientError()}}
	opts := StreamOptions{}
	got := recordFailures(&opts)

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{}, opts)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collectEvents(t, ch)

	if len(*got) != 1 {
		t.Fatalf("失敗の記録は 1 件であること（再試行のたびに記録しない）: %d 件", len(*got))
	}
	rec := (*got)[0]
	want := MaxRetries + 1
	if rec.Attempts != want {
		t.Errorf("送った回数が実際と合わない: %d（期待 %d = 初回 + 再試行 %d 回）", rec.Attempts, want, MaxRetries)
	}
	if rec.Attempts != a.calls {
		t.Errorf("記録した回数 %d がアダプタの呼び出し回数 %d と一致しない", rec.Attempts, a.calls)
	}
	if rec.Class != ErrClassTransient || rec.HTTPStatus != 503 {
		t.Errorf("最後の失敗の分類・状態が記録されていない: %+v", rec)
	}
}

func TestSuccessRecordsNoFailure(t *testing.T) {
	a := &scriptedAdapter{scripts: [][]StreamEvent{okScript("こんにちは")}}
	opts := StreamOptions{}
	got := recordFailures(&opts)

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{}, opts)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collectEvents(t, ch)
	// 走査が空振りしていないこと（本文を受け取れている）。
	if len(events) == 0 {
		t.Fatal("イベントが 1 件も届いていない（テストが成立していない）")
	}
	if len(*got) != 0 {
		t.Errorf("成功した呼び出しを記録してはいけない（失敗だけを記録する）: %+v", *got)
	}
}

func TestSendFailureBeforeRequestIsRecordedWithoutStatus(t *testing.T) {
	// 送信前の失敗（キー取得・入力不正）。呼び出し先からの応答が無いので状態は 0。
	a := &scriptedAdapter{startErr: errors.New("キーを取得できません")}
	opts := StreamOptions{}
	got := recordFailures(&opts)

	ch, err := StreamRetrying(context.Background(), a, ChatRequest{}, opts)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collectEvents(t, ch)

	if len(*got) != 1 {
		t.Fatalf("失敗の記録は 1 件であること: %d 件", len(*got))
	}
	rec := (*got)[0]
	if rec.HTTPStatus != 0 {
		t.Errorf("送信前の失敗では HTTPStatus を持たない: %d", rec.HTTPStatus)
	}
	if rec.Attempts != 0 {
		t.Errorf("送信に至っていないので送った回数は 0: %d", rec.Attempts)
	}
	if rec.Provider != ProviderAnthropic {
		t.Errorf("発生源が違う: %v", rec.Provider)
	}
}

// TestFailureRecordCarriesNoMessageBody は、記録の型が本文を運べないことを確かめる
// （プロンプト・応答・発話・キーを入れる場所が無い）。
//
// 文字列の走査ではなく**型の形**で見るので、将来 Message 等を足すと落ちる。
func TestFailureRecordCarriesNoMessageBody(t *testing.T) {
	allowed := map[string]bool{
		"Provider": true, "Class": true, "Code": true,
		"HTTPStatus": true, "Attempts": true, "Elapsed": true,
	}
	rt := reflect.TypeOf(FailureRecord{})
	if rt.NumField() != len(allowed) {
		t.Fatalf("FailureRecord の項目数が変わっている: %d（期待 %d）", rt.NumField(), len(allowed))
	}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !allowed[name] {
			t.Errorf("FailureRecord に想定外の項目がある: %s（本文・キーを動作ログへ運ばせない）", name)
		}
	}
}

func TestFailureRecorderNilDoesNotPanic(t *testing.T) {
	a := &scriptedAdapter{scripts: [][]StreamEvent{configError()}}
	ch, err := StreamRetrying(context.Background(), a, ChatRequest{}, StreamOptions{})
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	events := collectEvents(t, ch)
	if len(events) == 0 {
		t.Fatal("イベントが 1 件も届いていない（テストが成立していない）")
	}
	// 記録先が無くても失敗はそのまま上位へ届く。
	last := events[len(events)-1]
	if last.Kind != EventError {
		t.Errorf("失敗が上位へ届いていない: %v", last.Kind)
	}
}

func TestFailureElapsedIsMeasuredFromStart(t *testing.T) {
	fixJitter(t)
	waits := stubSleep(t)
	a := &scriptedAdapter{scripts: [][]StreamEvent{transientError()}}
	opts := StreamOptions{}
	got := recordFailures(&opts)

	start := time.Now()
	ch, err := StreamRetrying(context.Background(), a, ChatRequest{}, opts)
	if err != nil {
		t.Fatalf("開始に失敗: %v", err)
	}
	collectEvents(t, ch)
	total := time.Since(start)

	if len(*got) != 1 {
		t.Fatalf("失敗の記録は 1 件であること: %d 件", len(*got))
	}
	if rec := (*got)[0]; rec.Elapsed > total {
		t.Errorf("所要時間が実測を超えている: %v > %v", rec.Elapsed, total)
	}
	// 待機を差し替えているため実時間は伸びない。待機が入ったこと自体は確認する。
	if len(*waits) == 0 {
		t.Error("再試行の待機が発生していない（テストが成立していない）")
	}
}
