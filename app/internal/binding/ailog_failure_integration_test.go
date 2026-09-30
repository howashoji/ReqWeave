//go:build integration

// 結合テスト（AI 通信系の失敗で動作ログに残す内容）。
//
// 画面が呼ぶバインディングを通して AI 呼び出しを失敗させ、
//   - 失敗が動作ログへ `ai.call_failed` として残ること
//   - 記録項目が分類・発生源・HTTPStatus・正規化コード・再試行回数・所要時間であること
//   - プロンプト本文・発話・キーが残らないこと
//   - 成功した呼び出しでは記録が増えないこと
//
// を確認する。回答モードでは画面に「システム担当者へ連絡してください」としか出ないため
// （ステークホルダーは設定を直せないため）、この記録が失敗の切り分けの唯一の手掛かりになる。

package binding

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
)

// appLogRecords は動作ログの各行を map で返す（項目の中身まで見るため）。
func appLogRecords(t *testing.T, dir string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, line := range strings.Split(appLogBody(t, dir), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%d 行目が JSON Lines として読めない: %v", i+1, err)
		}
		out = append(out, rec)
	}
	return out
}

// findEvent は指定した event の行を返す（無ければ nil）。
func findEvent(records []map[string]any, event string) map[string]any {
	for _, r := range records {
		if r["event"] == event {
			return r
		}
	}
	return nil
}

func TestOperationLogRecordsAICallFailure(t *testing.T) {
	stub := &streamingStub{
		scripts: []string{dialogueQuestion},
		streamErr: &aiprovider.ProviderError{
			Class: aiprovider.ErrClassPermanent, Provider: aiprovider.ProviderAnthropic,
			HTTPStatus: 400, Code: "context_length_exceeded",
			Message: "prompt is too long: " + dialogueQuestion,
		},
	}
	a, root := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	// 失敗する呼び出しを起こす（失敗自体は画面へエラーとして返る）。
	_, _ = a.AskNextQuestion(sess.ID)
	waitForEvent(t, dir, "ai.call_failed")

	rec := findEvent(appLogRecords(t, dir), "ai.call_failed")
	if rec == nil {
		t.Fatalf("AI 呼び出しの失敗が記録されていない: %v", appLogEvents(t, dir))
	}
	fields, _ := rec["fields"].(map[string]any)
	if len(fields) == 0 {
		t.Fatalf("付随項目が記録されていない: %v", rec)
	}
	for key, want := range map[string]string{
		"provider":    "anthropic",
		"class":       "permanent",
		"http_status": "400",
		"code":        "context_length_exceeded",
	} {
		if got, _ := fields[key].(string); got != want {
			t.Errorf("%s が違う: %q（期待 %q）", key, got, want)
		}
	}
	for _, key := range []string{"attempts", "elapsed_ms"} {
		if got, _ := fields[key].(string); got == "" {
			t.Errorf("%s が記録されていない（AI 通信系の失敗で残す項目）", key)
		}
	}
}

func TestOperationLogAIFailureCarriesNoPromptOrKey(t *testing.T) {
	const leak = "在庫管理の現物の棚卸しは月末に行っています"
	stub := &streamingStub{
		scripts: []string{dialogueQuestion},
		streamErr: &aiprovider.ProviderError{
			Class: aiprovider.ErrClassTransient, Provider: aiprovider.ProviderAnthropic,
			HTTPStatus: 503,
			// プロバイダの生メッセージには本文が混ざりうる。動作ログへは渡さない。
			Message: "upstream error while processing: " + leak,
		},
	}
	a, root := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	_, _ = a.AskNextQuestion(sess.ID)
	waitForEvent(t, dir, "ai.call_failed")

	body := appLogBody(t, dir)
	// 走査が空振りしていないこと（対象の行が実在する）。
	if !strings.Contains(body, "ai.call_failed") {
		t.Fatal("AI 呼び出しの失敗が記録されていない（テストが成立していない）")
	}
	for _, secret := range []string{leak, dialogueQuestion, dummyKey} {
		if strings.Contains(body, secret) {
			t.Errorf("動作ログに残してはいけない文字列がある: %q", secret)
		}
	}
}

func TestOperationLogRecordsNoFailureOnSuccess(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	dir := attachAppLog(t, a)

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("質問を取得できない: ok=%v err=%v", ok, err)
	}
	// ストリーミングの完了まで待つ（記録は別ゴルーチンで進む）。
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	if hasEvent(appLogEvents(t, dir), "ai.call_failed") {
		t.Error("成功した呼び出しを失敗として記録している")
	}
	if stub.calls == 0 {
		t.Error("AI が 1 度も呼ばれていない（テストが成立していない）")
	}
}

// waitForEvent は動作ログに指定の event が現れるまで待つ
// （ストリーミングは別ゴルーチンで進むため、呼び出しの戻りと記録は同時ではない）。
func waitForEvent(t *testing.T, dir, event string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if hasEvent(appLogEvents(t, dir), event) {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s が 10s 以内に記録されない: %v", event, appLogEvents(t, dir))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
