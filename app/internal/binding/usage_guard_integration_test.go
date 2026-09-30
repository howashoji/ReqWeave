//go:build integration

// 結合テスト（トークン上限の判定と AI 呼び出しの停止）。
// 実ファイル・実 ai-log を使い、公開バインディング経由で検証する。

package binding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// usageStub は応答のたびにトークン実績（EventUsage）を返すアダプタ。
// 「進行中の呼び出しは中断せず、完走させて実績を記録する」の検証に使う。
type usageStub struct {
	streamingStub
	tokensIn, tokensOut int
}

func (s *usageStub) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	idx := s.calls
	s.calls++
	s.requests = append(s.requests, req)
	body := s.scripts[len(s.scripts)-1]
	if idx < len(s.scripts) {
		body = s.scripts[idx]
	}
	ch := make(chan aiprovider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta, Text: body}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventUsage,
			Usage: &aiprovider.TokenUsage{InputTokens: s.tokensIn, OutputTokens: s.tokensOut}}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone}
	}()
	return ch, nil
}

// appendConsumption は ai-log へ「実績つきの送信 1 件」を直接追記する（他端末の消費の再現にも使う）。
func appendConsumption(t *testing.T, root, author string, at time.Time, tokens int, id string) {
	t.Helper()
	store, err := projectstore.Open(root, projectstore.Author{AuthorID: author, DisplayName: "追記"})
	if err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	defer store.Close()

	send := auditlog.AISendRecord{ID: id, At: at, Author: author, Provider: "anthropic",
		Model: "claude-opus-5", Session: "S-0001", Prompt: "[user]\n質問"}
	send.SetTokens(tokens, 0, 0)
	line, err := json.Marshal(send)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFile(auditlog.FileName(auditlog.DirAILog, at, author), append(line, '\n')); err != nil {
		t.Fatalf("ai-log へ追記できない: %v", err)
	}
}

// openWithUsageLimit は対話用に開いた API と、そのプロジェクトのパスを返す。
func openWithUsageLimit(t *testing.T, stub *usageStub) (*API, string) {
	t.Helper()
	a, root := newDialogueAPI(t, &stub.streamingStub)
	// newDialogueAPI は streamingStub をアダプタとして返すため、usageStub を返すよう差し替える。
	a.newAdapter = func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
		opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
		stub.stubAdapter.id = id
		stub.stubAdapter.keys = keys
		stub.stubAdapter.ref = ref
		return stub, nil
	}
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	return a, root
}

func setLimit(t *testing.T, a *API, tokensMax int, warnRatio *float64) {
	t.Helper()
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.SetUsageLimit(tokensMax, warnRatio); err != nil {
		t.Fatalf("上限を設定できない: %v", err)
	}
}

// 上限未設定では警告も停止も発生しない。
func TestNoUsageLimitAllowsAICalls(t *testing.T) {
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}, tokensIn: 1_000_000}
	a, _ := openWithUsageLimit(t, stub)

	status, err := a.UsageStatusNow()
	if err != nil {
		t.Fatalf("利用量を取得できない: %v", err)
	}
	if status.Level != UsageLevelNone || status.LimitTokens != nil || status.RemainingTokens != nil {
		t.Errorf("上限未設定なのに警告・上限が出ている: %+v", status)
	}

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("上限未設定で AI 呼び出しが止まった: %v (ok=%v)", err, ok)
	}
}

// 警告閾値以上・上限未満では呼び出しが成立し、残りトークン数が返る。
func TestUsageWarningAllowsCallAndReportsRemaining(t *testing.T) {
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}, tokensIn: 1}
	a, root := openWithUsageLimit(t, stub)
	setLimit(t, a, 1000, nil) // 警告閾値は既定 0.8 → 800

	appendConsumption(t, root, "t.suzuki@example.co.jp", time.Now().UTC(), 850, "other-1")

	status, err := a.UsageStatusNow()
	if err != nil {
		t.Fatalf("利用量を取得できない: %v", err)
	}
	if status.Level != UsageLevelWarn {
		t.Fatalf("警告状態になっていない: %+v", status)
	}
	if status.RemainingTokens == nil || *status.RemainingTokens != 150 {
		t.Errorf("残りトークン数が違う: %+v", status.RemainingTokens)
	}

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("警告状態で呼び出しが止まった（停止は上限到達時のみ）: %v (ok=%v)", err, ok)
	}
}

// 上限到達で AI 呼び出しを開始できない（プロバイダへ送信しない）。
// AI を使わない操作は続けられる。上限の引き上げ・解除で次回から再開できる。
func TestUsageLimitBlocksAICallsAndReleasesOnRaise(t *testing.T) {
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}, tokensIn: 1}
	a, root := openWithUsageLimit(t, stub)
	setLimit(t, a, 1000, nil)
	appendConsumption(t, root, "k.sato@example.co.jp", time.Now().UTC(), 1200, "over-1")

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	before := stub.calls
	_, err = a.AskNextQuestion(sess.ID)
	if err == nil {
		t.Fatal("上限到達なのに AI 呼び出しが開始された")
	}
	for _, want := range []string{"上限に達した", "オーナーが上限を変更すると再開できます", "AI を使わない操作"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("エラーカタログ「利用量上限系」の要素が無い（%q）: %v", want, err)
		}
	}
	if stub.calls != before {
		t.Errorf("上限到達なのにプロバイダへ送信された: %d → %d", before, stub.calls)
	}

	// AI を使わない操作は続けられる。
	if _, err := a.UsageStatusNow(); err != nil {
		t.Errorf("上限到達で利用量の表示ができない: %v", err)
	}
	from := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	to := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	if _, err := a.ProgressReport(ProgressReportRequest{From: from, To: to}); err != nil {
		t.Errorf("上限到達で進捗レポートが生成できない: %v", err)
	}
	if _, err := a.ExportDocuments(ExportRequestView{Destination: filepath.Join(t.TempDir(), "out")}); err != nil {
		// 成果物が無い場合の「出力対象なし」は許容する（AI 呼び出しの停止とは別の理由）。
		if strings.Contains(err.Error(), "上限") {
			t.Errorf("エクスポートが上限到達で止まった: %v", err)
		}
	}

	// 上限の引き上げで次回の開始判定から再開できる（アプリ再起動を要しない）。
	setLimit(t, a, 100_000, nil)
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("上限を引き上げても再開できない: %v (ok=%v)", err, ok)
	}
	// 送信はイベント経由の非同期のため、実際にプロバイダへ届くのを待つ。
	waitFor(t, func() bool { return stub.calls > before }, "上限引き上げ後もプロバイダへ送信されていない")
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	// 上限を下げると次の AI 操作（回答からの抽出）が止まる。回答自体は保存される（AI が止まっても手動で続けられるように）。
	setLimit(t, a, 1, nil)
	utteranceID, err := a.SendAnswer(sess.ID, "日次バッチで構いません")
	if err == nil {
		t.Fatal("上限到達なのに抽出が開始された")
	}
	if utteranceID == "" {
		t.Error("上限到達で回答そのものが保存されなかった（手動編集で続けられる必要がある）")
	}

	// 解除すると判定自体を行わない（警告・停止なし）。
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.ClearUsageLimit(); err != nil {
		t.Fatal(err)
	}
	status, err := a.UsageStatusNow()
	if err != nil {
		t.Fatalf("利用量を取得できない: %v", err)
	}
	if status.Level != UsageLevelNone || status.LimitTokens != nil {
		t.Errorf("解除後も上限判定が残っている: %+v", status)
	}
}

// 判定のたびに ai-log を再集計する（他端末が追記した消費を取り込む）。
func TestUsageJudgementPicksUpOtherTerminalConsumption(t *testing.T) {
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}, tokensIn: 1}
	a, root := openWithUsageLimit(t, stub)
	setLimit(t, a, 1000, nil)

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("最初の呼び出しが止まった: %v (ok=%v)", err, ok)
	}

	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	// 別端末（別の作業者・別のストアインスタンス）が上限を超える消費を追記する。
	appendConsumption(t, root, "t.suzuki@example.co.jp", time.Now().UTC(), 5000, "remote-1")

	// 次の AI 操作（回答からの抽出）が、他端末の消費を取り込んだ判定で止まること。
	// 「エラーになった」だけでは対話状態などの別要因と区別できないため、上限到達の文言まで確認する。
	_, err = a.SendAnswer(sess.ID, "日次バッチで構いません")
	if err == nil {
		t.Fatal("他端末の消費が判定に反映されていない（プロセス内の値だけで判定している）")
	}
	if !strings.Contains(err.Error(), "上限に達した") {
		t.Errorf("上限到達以外の理由で止まっている: %v", err)
	}
}

// 進行中の呼び出しは中断しない。完走させて実績を記録し、次の開始判定から停止が効く。
func TestInFlightCallCompletesAndRecordsUsage(t *testing.T) {
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}, tokensIn: 5000}
	a, root := openWithUsageLimit(t, stub)
	setLimit(t, a, 1000, nil)

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	// 開始時点の累計は 0 なので呼び出しは成立し、5000 トークンを消費して完走する。
	if ok, err := a.AskNextQuestion(sess.ID); err != nil || !ok {
		t.Fatalf("最初の呼び出しが止まった: %v (ok=%v)", err, ok)
	}
	waitFor(t, func() bool {
		total, err := auditlog.TotalUsage(root)
		return err == nil && total.Tokens.Total >= 5000
	}, "上限超過分を含む実績が ai-log に記録されない（進行中の呼び出しを中断している）")

	// 次の開始判定から停止が効く。
	if _, err := a.AskNextQuestion(sess.ID); err == nil {
		t.Fatal("上限超過後の次の呼び出しが止まらない")
	}
}

// waitFor は条件が成立するまで短く待つ（実績は非同期に記録される）。
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// appendSendWithoutUsage は実績行の無い送信（実績を受け取れなかった異常時の欠測）を 1 件追記する。
func appendSendWithoutUsage(t *testing.T, root, author string, at time.Time) {
	t.Helper()
	store, err := projectstore.Open(root, projectstore.Author{AuthorID: author, DisplayName: "追記"})
	if err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	defer store.Close()

	send := auditlog.AISendRecord{ID: "missing-1", At: at, Author: author, Provider: "anthropic",
		Model: "claude-opus-5", Session: "S-0001", Prompt: "[user]\n質問"}
	line, err := json.Marshal(send)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFile(auditlog.FileName(auditlog.DirAILog, at, author), append(line, '\n')); err != nil {
		t.Fatalf("ai-log へ追記できない: %v", err)
	}
}

// snapshotProject はプロジェクトフォルダの相対パス → 内容ハッシュを返す（表示のみで変化しないことの確認用）。
func snapshotProjectFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatalf("ファイル一覧を取得できない: %v", err)
	}
	return out
}
