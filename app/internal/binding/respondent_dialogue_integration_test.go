//go:build integration

// 結合テスト（回答モードの AI 対話バインディング × 実ファイル × 実動作ログ）。
//
// 差し替えるのは AI プロバイダのアダプタだけで、受け渡しファイル・回答作業領域・動作ログは実物を使う。
// 見るのは AI 対話回答の受け入れ条件と、動作ログの回答モードの allowlist:
//
//   - 事前表示の確認を終えるまで AI プロバイダへの呼び出しが 1 回も発生しない
//   - 送る内容が事前に示した範囲に収まる（宛先・各種 ID・保護の材料が入らない）
//   - モデルは推奨・エフォートは標準（利用者に選ばせない）
//   - 動作ログに発話・質問文・宛先・ファイル名が残らず、質問票 ID は残る

package binding

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// respondAIModels は代役プロバイダが返すモデル一覧（推奨が 1 件だけある）。
func respondAIModels() []aiprovider.ModelInfo {
	return []aiprovider.ModelInfo{
		{ID: "claude-haiku-4-5", Tier: aiprovider.TierOther, MaxOutput: 8000},
		{ID: "claude-opus-5", Tier: aiprovider.TierPrimary, DefaultForTier: true, MaxOutput: 32000},
	}
}

// newRespondAIFixture は回答モードの AI 対話を試せる一式を返す（設定・プロジェクトは無い）。
//
// キーの参照名はテスト用へ差し替える（**利用者のセキュアストレージへ固定名の項目を作らない**）。
func newRespondAIFixture(t *testing.T, stub *streamingStub) (*API, string, string, string) {
	t.Helper()
	src, passcode := issueForRespond(t)
	a := newRespondAPI(t)
	a.keys = keymanager.New()
	a.newAdapter = func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
		opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
		stub.stubAdapter.id = id
		stub.stubAdapter.keys = keys
		stub.stubAdapter.ref = ref
		stub.stubAdapter.opts = opts
		return stub, nil
	}
	logDir := attachAppLog(t, a)

	original := respondAILabel
	respondAILabel = keytest.Marker + uuid.NewString()
	t.Cleanup(func() { respondAILabel = original })
	keytest.TrackKey(t, keymanager.Ref{ProviderID: "anthropic", Label: respondAILabel})
	return a, src, passcode, logDir
}

// 有効化 → 対話 → 回答欄へ移す → 確定までを通す。
func TestRespondAIDialogueRoundTrip(t *testing.T) {
	stub := &streamingStub{scripts: []string{
		"締め作業で手作業になっている工程を、名前と担当者で挙げていくと書きやすくなります。",
		"「請求書の突合を経理が手作業で行っている」と書けば十分です。",
	}}
	stub.stubAdapter.models = respondAIModels()
	a, src, passcode, logDir := newRespondAIFixture(t, stub)

	opened, err := a.OpenQuestionnaireFile(src, passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}

	// 1. 既定は無効。確認前は有効化もキー登録もできず、AI を 1 回も呼ばない。
	view, err := a.RespondAIDialogue()
	if err != nil {
		t.Fatal(err)
	}
	if view.Enabled || len(view.Utterances) != 0 {
		t.Fatalf("既定で有効になっている: %+v", view)
	}
	if _, err := a.EnableRespondAI("anthropic", "secret_key", false); err == nil {
		t.Fatal("確認前に有効化できた")
	}
	if _, err := a.RegisterRespondAIKey("anthropic", dummyKey, false); err == nil {
		t.Fatal("確認前にキーを登録できた")
	}
	if stub.calls != 0 || stub.stubAdapter.verifyCall != 0 {
		t.Fatalf("確認前に AI を呼んだ: stream=%d verify=%d", stub.calls, stub.stubAdapter.verifyCall)
	}

	// 2. 送信範囲の事前表示 → 確認 → キー登録（疎通確認つき）→ 有効化。
	scope := a.RespondAIScope("anthropic")
	if len(scope.Sent) == 0 || len(scope.NotSent) == 0 || scope.NoRecordNotice == "" {
		t.Fatalf("事前表示の内容が足りない: %+v", scope)
	}
	result, err := a.RegisterRespondAIKey("anthropic", dummyKey, true)
	if err != nil {
		t.Fatalf("キーを登録できない: %v", err)
	}
	if !result.OK || stub.stubAdapter.verifyCall != 1 {
		t.Fatalf("疎通確認を経ていない: %+v verify=%d", result, stub.stubAdapter.verifyCall)
	}
	view, err = a.EnableRespondAI("anthropic", "secret_key", true)
	if err != nil {
		t.Fatalf("有効化できない: %v", err)
	}
	if !view.Enabled || view.ProviderLabel == "" || view.CanSignOut {
		t.Fatalf("有効化後の表示が違う: %+v", view)
	}

	// 3. 1 往復目。
	view, err = a.SendRespondAIMessage("2 つめの質問が何を聞いているのか分かりません。")
	if err != nil {
		t.Fatalf("対話に失敗: %v", err)
	}
	if len(view.Utterances) != 2 {
		t.Fatalf("発話が %d 件（本人 + AI の 2 件のはず）: %+v", len(view.Utterances), view.Utterances)
	}
	if view.Utterances[0].SpeakerLabel != "あなた" || view.Utterances[1].SpeakerLabel != "AI" {
		t.Errorf("話者の表示が違う: %+v", view.Utterances)
	}
	if view.Utterances[1].Body != stub.scripts[0] {
		t.Errorf("AI の応答が保存されていない: %q", view.Utterances[1].Body)
	}

	// 4. 送った内容が事前に示した範囲に収まる（質問票全体は送り、宛先・ID・保護の材料は送らない）。
	if len(stub.requests) != 1 {
		t.Fatalf("送信回数が %d 回", len(stub.requests))
	}
	req := stub.requests[0]
	if !strings.Contains(req.System, "月末の締め作業") || !strings.Contains(req.System, "在庫の引き当て") {
		t.Errorf("質問票全体が載っていない（AI が他の質問との関係を踏まえられない）:\n%s", req.System)
	}
	for _, forbidden := range []string{"佐藤", "営業部", opened.QuestionnaireID, "STK-", "ISS-", passcode} {
		if forbidden == "" {
			t.Fatal("検査対象の値が空（テストが成立していない）")
		}
		if strings.Contains(req.System, forbidden) {
			t.Errorf("送ってはいけない値が送信本文に現れた: %q", forbidden)
		}
	}
	for _, q := range opened.Questions {
		if strings.Contains(req.System, q.ID) {
			t.Errorf("質問 ID が送信本文に現れた: %q", q.ID)
		}
	}
	// モデルは推奨・エフォートは標準（利用者に選ばせない）。
	if req.Model != "claude-opus-5" {
		t.Errorf("推奨モデルを使っていない: %q", req.Model)
	}
	want := aiprovider.MapEffort("anthropic", aiprovider.EffortStandard).
		ClampToModel(aiprovider.ModelInfo{MaxOutput: 32000})
	if req.Effort != want {
		t.Errorf("エフォートが標準固定になっていない: %+v（期待 %+v）", req.Effort, want)
	}
	// 発話履歴は本画面の分だけ（この時点では本人の 1 件）。
	if len(req.Messages) != 1 || req.Messages[0].Role != aiprovider.RoleUser {
		t.Errorf("発話履歴の載せ方が違う: %+v", req.Messages)
	}

	// 5. 2 往復目では履歴が積まれる。
	if _, err := a.SendRespondAIMessage("では、どう書けばよいですか。"); err != nil {
		t.Fatalf("2 往復目に失敗: %v", err)
	}
	if got := len(stub.requests[1].Messages); got != 3 {
		t.Errorf("2 往復目の発話履歴が %d 件（本人・AI・本人の 3 件のはず）", got)
	}

	// 6. AI の応答を自由記述の質問へ下書きとして移す（選択肢の質問へは入れない）。
	freeQ, choiceQ := "", ""
	for _, q := range opened.Questions {
		if q.AnswerFormat == projectstore.AnswerFormatFree {
			freeQ = q.ID
		}
		if q.AnswerFormat == projectstore.AnswerFormatChoice {
			choiceQ = q.ID
		}
	}
	if freeQ == "" || choiceQ == "" {
		t.Fatal("フィクスチャに自由記述・選択肢の質問が揃っていない")
	}
	last := view.Utterances[len(view.Utterances)-1]
	draft, err := a.RespondAIAnswerDraft(freeQ, last.ID, RespondDraftReplace)
	if err != nil {
		t.Fatalf("回答欄へ移せない: %v", err)
	}
	if draft.FreeText != stub.scripts[0] {
		t.Errorf("下書きの中身が違う: %q", draft.FreeText)
	}
	if _, err := a.RespondAIAnswerDraft(choiceQ, last.ID, RespondDraftReplace); err == nil {
		t.Error("選択肢の質問へ自動で書き込めた（選ぶのは本人）")
	}

	// 7. 下書きは保存されていない（本人が回答欄で確定して初めて回答になる）。
	progress, err := a.RespondProgress()
	if err != nil {
		t.Fatal(err)
	}
	if progress.Answered != 0 {
		t.Errorf("下書きを移しただけで回答済みになった: %+v", progress)
	}

	// 8. 動作ログ: 有効化の事実と質問票 ID は残り、本文は残らない（動作ログの allowlist）。
	body := appLogBody(t, logDir)
	if !strings.Contains(body, "respond.ai_enabled") {
		t.Error("有効化の記録が無い")
	}
	if !strings.Contains(body, opened.QuestionnaireID) {
		t.Error("質問票 ID が記録されていない（担当者が切り分けられない）")
	}
	for _, forbidden := range []string{
		"2 つめの質問が何を聞いているのか", stub.scripts[0],
		"月末の締め作業", "佐藤", "営業部", exchange.ExtIssue,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("動作ログに残してはいけない内容が出た: %q", forbidden)
		}
	}
	for _, q := range opened.Questions {
		if strings.Contains(body, q.ID) {
			t.Errorf("動作ログに質問 ID が出た: %q", q.ID)
		}
	}
}

// 失敗は回答モードの様式で示し、動作ログへ分類と質問票 ID を残す。
func TestRespondAIFailureUsesRespondWordingAndIsLogged(t *testing.T) {
	stub := &streamingStub{
		scripts: []string{"届かない応答"},
		streamErr: &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient,
			Provider: "anthropic", Code: "rate_limited", HTTPStatus: 429,
			Message: "upstream said: too many requests"},
	}
	stub.stubAdapter.models = respondAIModels()
	a, src, passcode, logDir := newRespondAIFixture(t, stub)

	opened, err := a.OpenQuestionnaireFile(src, passcode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnableRespondAI("anthropic", "secret_key", true); err != nil {
		t.Fatal(err)
	}
	_, err = a.SendRespondAIMessage("1 つめの質問について相談させてください。")
	if err == nil {
		t.Fatal("失敗したのにエラーにならない")
	}
	if err.Error() != msgRespondAIUnavailable {
		t.Errorf("回答モードの様式になっていない: %v", err)
	}

	body := appLogBody(t, logDir)
	if !strings.Contains(body, "ai.call_failed") || !strings.Contains(body, "429") {
		t.Errorf("失敗の記録が足りない:\n%s", body)
	}
	if !strings.Contains(body, opened.QuestionnaireID) {
		t.Error("失敗の記録に質問票 ID が無い（回答モードの allowlist では質問票 ID を残す）")
	}
	if strings.Contains(body, "1 つめの質問について相談") || strings.Contains(body, "upstream said") {
		t.Errorf("動作ログに発話・内部の詳細が出た:\n%s", body)
	}
	// 本人の発話は残る（送信の前に保存する）。AI の発話は付かない。
	view, err := a.RespondAIDialogue()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Utterances) != 1 || view.Utterances[0].SpeakerLabel != "あなた" {
		t.Errorf("失敗時の発話の残り方が違う: %+v", view.Utterances)
	}
}

// blockingStub は中断を試すための代役（1 文字返してから中断を待つ）。
type blockingStub struct {
	stubAdapter
	started chan struct{}
}

func (s *blockingStub) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	ch := make(chan aiprovider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta, Text: "途中まで届いた応答"}
		close(s.started)
		<-ctx.Done()
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone, Interrupted: true}
	}()
	return ch, nil
}

// 担当者モードの対話と同方式: 中断すると受信済みの本文が中断発話として残る。
func TestRespondAIMessageCanBeInterrupted(t *testing.T) {
	stub := &blockingStub{started: make(chan struct{})}
	stub.stubAdapter.models = respondAIModels()
	src, passcode := issueForRespond(t)
	a := newRespondAPI(t)
	a.keys = keymanager.New()
	a.newAdapter = func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
		opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
		stub.stubAdapter.id = id
		return stub, nil
	}
	attachAppLog(t, a)

	if _, err := a.OpenQuestionnaireFile(src, passcode); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnableRespondAI("anthropic", "secret_key", true); err != nil {
		t.Fatal(err)
	}
	// 何も動いていないうちは中断できない（押せる操作を偽らない）。
	if err := a.CancelRespondAIMessage(); err == nil {
		t.Error("応答が無いのに中断できた")
	}
	go func() {
		<-stub.started
		_ = a.CancelRespondAIMessage()
	}()
	view, err := a.SendRespondAIMessage("途中で止めます。")
	if err != nil {
		t.Fatalf("中断はエラーにしない: %v", err)
	}
	if len(view.Utterances) != 2 {
		t.Fatalf("発話が %d 件（本人 + 中断の 2 件のはず）: %+v", len(view.Utterances), view.Utterances)
	}
	if view.Utterances[1].Body != "途中まで届いた応答" {
		t.Errorf("受信済みの本文が残っていない: %q", view.Utterances[1].Body)
	}
	if view.Running {
		t.Error("中断後も受信中のままになっている")
	}
}
