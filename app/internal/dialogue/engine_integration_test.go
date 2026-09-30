//go:build integration

// 結合テスト（対話エンジン × 実ファイル）。AI プロバイダはスタブへ差し替える（外部 API を呼ばない）。

package dialogue

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// stubAdapter は決められた応答を返すアダプタ。呼び出しごとに scripts の次の要素を使う。
type stubAdapter struct {
	scripts []string
	err     *aiprovider.ProviderError
	calls   int
	// hold が非 nil のとき、最初の応答を送出したあと解放されるまで待つ（中断の検証用）。
	hold chan struct{}
	reqs []aiprovider.ChatRequest
}

func (s *stubAdapter) ID() aiprovider.ProviderID { return aiprovider.ProviderAnthropic }

func (s *stubAdapter) ListModels(context.Context) ([]aiprovider.ModelInfo, error) { return nil, nil }

func (s *stubAdapter) VerifyKey(context.Context) error { return nil }

func (s *stubAdapter) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	idx := s.calls
	s.calls++
	s.reqs = append(s.reqs, req)
	ch := make(chan aiprovider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if s.err != nil {
			ch <- aiprovider.StreamEvent{Kind: aiprovider.EventError, Err: s.err}
			return
		}
		body := s.scripts[len(s.scripts)-1]
		if idx < len(s.scripts) {
			body = s.scripts[idx]
		}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta, Text: body}
		if s.hold != nil && idx == 0 {
			select {
			case <-s.hold:
			case <-ctx.Done():
			}
			ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone, Interrupted: true}
			return
		}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventUsage, Usage: &aiprovider.TokenUsage{InputTokens: 10, OutputTokens: 20}}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone, Usage: &aiprovider.TokenUsage{InputTokens: 10, OutputTokens: 20}}
	}()
	return ch, nil
}

func newTestEngine(t *testing.T, stub *stubAdapter) (*Engine, *projectstore.Store) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	e, err := New(Config{
		Store:   store,
		Adapter: stub,
		Model:   aiprovider.ModelInfo{ID: "claude-test", ContextWindow: 200000, MaxOutput: 8192, SupportsReasoning: true},
		Effort:  aiprovider.EffortStandard,
	})
	if err != nil {
		t.Fatalf("対話エンジンを作れない: %v", err)
	}
	return e, store
}

func collectEvents(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
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

const questionScript = "論点キー: background/current-state\n質問: 現状の在庫管理業務はどのように行っていますか。\n背景: 現状業務を記録し、改善対象を特定するために確認します。"

// 質問が「質問本文＋背景説明」の形式で 1 件ずつ生成され、発話として保存される。
func TestGenerateQuestion(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, store := newTestEngine(t, stub)
	sess, err := e.StartSession(PhaseRequirements)
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}

	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("質問生成に失敗: %v", err)
	}
	events := collectEvents(t, ch)

	last := events[len(events)-1]
	if last.Kind != EventDone || last.UtteranceID == "" {
		t.Fatalf("完了イベントが違う: %+v", events)
	}
	if last.TopicKey != "background/current-state" {
		t.Errorf("論点キーが違う: %+v", last)
	}
	if last.State != StateAwaitingAnswer {
		t.Errorf("状態が回答待ちにならない: %+v", last)
	}
	var streamed string
	for _, ev := range events {
		if ev.Kind == EventText {
			streamed += ev.Text
		}
	}
	if !strings.Contains(streamed, "現状の在庫管理業務") {
		t.Errorf("本文がストリーミングされていない: %q", streamed)
	}

	_, utterances, err := store.LoadSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(utterances) != 1 || utterances[0].Speaker != projectstore.SpeakerAgent {
		t.Fatalf("発話が保存されていない: %+v", utterances)
	}
	if !strings.Contains(utterances[0].Body, "質問:") || !strings.Contains(utterances[0].Body, "背景:") {
		t.Errorf("質問本文＋背景説明の形式で保存されていない:\n%s", utterances[0].Body)
	}

	// 提示済み質問が併置メタデータへ保存される。
	state, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.PresentedQuestion == nil || state.PresentedQuestion.TopicKey != "background/current-state" ||
		state.PresentedQuestion.FollowUpIndex != 1 || state.PresentedQuestion.Answered {
		t.Errorf("提示済み質問が保存されていない: %+v", state.PresentedQuestion)
	}
}

// 既決論点への質問は 1 回だけ作り直し、なお既決なら再確認として明示する。
func TestDecidedTopicIsRegenerated(t *testing.T) {
	stub := &stubAdapter{scripts: []string{
		"論点キー: background/current-state\n質問: 決定済みの論点への質問\n背景: 既決",
		"論点キー: background/current-state\n質問: それでも決定済みの論点\n背景: 既決",
	}}
	e, store := newTestEngine(t, stub)
	if _, err := store.CreateDecision(projectstore.Decision{TopicKey: "background/current-state",
		Body: "現状は手作業。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	sess, _ := e.StartSession(PhaseRequirements)

	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch)

	if stub.calls != 2 {
		t.Errorf("作り直しが 1 回だけ行われていない: %d 回呼び出し", stub.calls)
	}
	var replaced bool
	for _, ev := range events {
		if ev.Kind == EventReplaced {
			replaced = true
		}
	}
	if !replaced {
		t.Error("差し替えイベントが流れていない")
	}
	last := events[len(events)-1]
	if !last.Reconfirm {
		t.Errorf("再確認として提示されていない: %+v", last)
	}
	_, utterances, _ := store.LoadSession(sess.ID)
	if len(utterances) != 1 {
		t.Fatalf("発話数が違う（作り直し分が保存されている）: %d", len(utterances))
	}
	if !strings.HasPrefix(utterances[0].Body, ReconfirmNotice) {
		t.Errorf("再確認の明示表示が無い:\n%s", utterances[0].Body)
	}
}

// 追問上限に達した論点は継続せず、次の論点へ移る（AI の遵守に依存しない）。
func TestFollowUpLimitForcesNextTopic(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)

	// 標準（上限 3）で 3 回目まで提示済み・回答済みの状態を作る。
	state, _ := e.LoadState(sess.ID)
	state.PresentedQuestion = &PresentedQuestion{TopicKey: "background/current-state",
		FollowUpIndex: 3, Answered: true}
	if err := e.saveMeta(sess.ID, state); err != nil {
		t.Fatal(err)
	}

	records, _ := e.Records()
	completeness, _ := Completeness(PhaseRequirements, records)
	got, err := e.selectTopic(PhaseRequirements, records, completeness, state)
	if err != nil {
		t.Fatal(err)
	}
	if got.TopicKey == "background/current-state" && got.Reason == ReasonFollowUp {
		t.Errorf("上限に達した論点が継続された: %+v", got)
	}

	// 上限未満なら継続する。
	state.PresentedQuestion.FollowUpIndex = 2
	got, _ = e.selectTopic(PhaseRequirements, records, completeness, state)
	if got.TopicKey != "background/current-state" || got.FollowUpIndex != 3 {
		t.Errorf("上限未満で継続されない: %+v", got)
	}
}

// 中断は受信済み本文を中断発話として保存し、状態を中断にする。
func TestInterruptDuringGeneration(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	stub := &stubAdapter{scripts: []string{"論点キー: background/current-state\n質問: 途中まで"}, hold: hold}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := e.GenerateQuestion(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 最初の本文差分が届いてから中断する。
	select {
	case ev := <-ch:
		if ev.Kind != EventText {
			t.Fatalf("最初のイベントが本文差分でない: %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("本文差分が届かない")
	}
	cancel()
	events := collectEvents(t, ch)

	last := events[len(events)-1]
	if last.Kind != EventDone || !last.Interrupted {
		t.Fatalf("中断が通知されない: %+v", events)
	}
	if last.State != StateSuspended {
		t.Errorf("状態が中断にならない: %+v", last)
	}
	_, utterances, _ := store.LoadSession(sess.ID)
	if len(utterances) != 1 || !utterances[0].IsInterrupted() {
		t.Fatalf("中断発話が保存されていない: %+v", utterances)
	}
	if !strings.Contains(utterances[0].Body, "途中まで") {
		t.Errorf("受信済み本文が失われている: %q", utterances[0].Body)
	}
	// 中断発話は履歴の原文にも要約にも入らない。
	history, _ := CompressHistory(utterances, nil, HistoryBudget{})
	if strings.Contains(history.Text(), "途中まで") {
		t.Errorf("中断発話が履歴に含まれる:\n%s", history.Text())
	}
}

// 再試行の上限超過は障害停止 → 中断へ遷移し、エラー種別を提示する。
func TestFailureSuspendsSession(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript},
		err: &aiprovider.ProviderError{Class: aiprovider.ErrClassConfig, Provider: aiprovider.ProviderAnthropic,
			HTTPStatus: 401, Code: "unauthorized", Message: "invalid api key"}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)

	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch)

	last := events[len(events)-1]
	if last.Kind != EventError {
		t.Fatalf("エラーで終わらない: %+v", events)
	}
	if last.ErrorClass != "config" {
		t.Errorf("エラー種別が渡らない: %+v", last)
	}
	// プロバイダ固有コードも画面まで運ぶ
	// （利用者向けの文言は分類 × Code から公開バインディング層が作る）。
	if last.ErrorCode != "unauthorized" {
		t.Errorf("プロバイダ固有コードが渡らない: %+v", last)
	}
	if last.State != StateSuspended {
		t.Errorf("障害停止から中断へ遷移していない: %+v", last)
	}
	state, _ := e.LoadState(sess.ID)
	if state.DialogueState != StateSuspended {
		t.Errorf("保存された状態が中断でない: %+v", state)
	}
}

// 利用者の発話は AI 送信前に保存される。
func TestSubmitAnswerSavesBeforeExtraction(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	ch, _ := e.GenerateQuestion(context.Background(), sess.ID)
	collectEvents(t, ch)

	callsBefore := stub.calls
	id, err := e.SubmitAnswer(sess.ID, "手作業の Excel 台帳で管理しています。")
	if err != nil {
		t.Fatalf("回答の送信に失敗: %v", err)
	}
	if stub.calls != callsBefore {
		t.Errorf("保存前に AI を呼んでいる: %d → %d", callsBefore, stub.calls)
	}
	_, utterances, _ := store.LoadSession(sess.ID)
	if len(utterances) != 2 || utterances[1].ID != id || utterances[1].Speaker != projectstore.SpeakerUser {
		t.Fatalf("回答が保存されていない: %+v", utterances)
	}
	state, _ := e.LoadState(sess.ID)
	if state.DialogueState != StateExtracting {
		t.Errorf("状態が抽出中にならない: %+v", state)
	}
	if state.PresentedQuestion == nil || !state.PresentedQuestion.Answered {
		t.Errorf("提示済み質問が回答済みにならない: %+v", state.PresentedQuestion)
	}
	if _, err := e.SubmitAnswer(sess.ID, "  "); err == nil {
		t.Error("空の回答が受理された")
	}
}

// 再開時に中断時点の状態・提示済み質問・未承認候補・未決事項が復元される。
func TestResumeRestoresContext(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	ch, _ := e.GenerateQuestion(context.Background(), sess.ID)
	collectEvents(t, ch)

	if _, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "佐藤",
		Evidence: []string{"S-0001#utt-00001"}, Body: "棚卸の頻度"}); err != nil {
		t.Fatal(err)
	}
	// 未承認候補と要約ブロックを保全した状態で中断する。
	state, _ := e.LoadState(sess.ID)
	state.PendingCandidates = map[string]any{"decisions": []any{map[string]any{"topic_key": "scope/in-scope"}}}
	state.Summaries = []projectstore.SummaryBlock{{CoversUntil: "utt-00001", Body: "現状業務を確認中。"}}
	if err := e.saveMeta(sess.ID, state); err != nil {
		t.Fatal(err)
	}
	if err := e.Suspend(sess.ID); err != nil {
		t.Fatalf("中断に失敗: %v", err)
	}

	got, err := e.Resume(sess.ID)
	if err != nil {
		t.Fatalf("再開に失敗: %v", err)
	}
	if got.State != StateSuspended {
		t.Errorf("状態が復元されない: %+v", got)
	}
	if got.PresentedQuestion == nil || got.PresentedQuestion.TopicKey != "background/current-state" {
		t.Errorf("提示済み質問が復元されない: %+v", got.PresentedQuestion)
	}
	if !got.HasPendingCandidates {
		t.Error("未承認候補が失われている")
	}
	if got.RecentSummary != "現状業務を確認中。" {
		t.Errorf("直近の要約が復元されない: %q", got.RecentSummary)
	}
	if len(got.OpenIssues) != 1 {
		t.Errorf("未決事項が提示されない: %+v", got.OpenIssues)
	}
}

// 対話の送信は送信記録を発行し、文脈の内訳ラベルを含める。
func TestQuestionSendIsRecorded(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	rec := &fakeRecorder{usages: map[string]aiprovider.TokenUsage{}}
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	e, err := New(Config{Store: store, Adapter: stub, Recorder: rec,
		Model:  aiprovider.ModelInfo{ID: "claude-test", ContextWindow: 200000, MaxOutput: 8192},
		Effort: aiprovider.EffortStandard})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := e.StartSession(PhaseRequirements)
	ch, _ := e.GenerateQuestion(context.Background(), sess.ID)
	collectEvents(t, ch)

	if len(rec.sends) != 1 {
		t.Fatalf("送信記録の件数が違う: %d", len(rec.sends))
	}
	if rec.sends[0].Session != sess.ID {
		t.Errorf("セッション ID が記録されていない: %+v", rec.sends[0])
	}
	if len(rec.sends[0].Included) == 0 {
		t.Errorf("送信文脈の内訳が記録されていない: %+v", rec.sends[0])
	}
	if len(rec.usages) != 1 {
		t.Errorf("トークン実績が記録されていない: %+v", rec.usages)
	}
}

// fakeRecorder は送信記録の発行先のテスト実装。
type fakeRecorder struct {
	sends  []aiprovider.SendRecord
	usages map[string]aiprovider.TokenUsage
	n      int
}

func (f *fakeRecorder) RecordSend(rec aiprovider.SendRecord) (string, error) {
	if rec.Model == "" {
		return "", errors.New("モデルがありません")
	}
	f.sends = append(f.sends, rec)
	f.n++
	return "send-1", nil
}

func (f *fakeRecorder) RecordUsage(sendID string, usage aiprovider.TokenUsage) {
	f.usages[sendID] = usage
}

// answerAndExtract は質問 → 回答 → 抽出まで進める。
func answerAndExtract(t *testing.T, e *Engine, sessionID, answer string) []Event {
	t.Helper()
	ch, err := e.GenerateQuestion(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("質問生成に失敗: %v", err)
	}
	collectEvents(t, ch)
	if _, err := e.SubmitAnswer(sessionID, answer); err != nil {
		t.Fatalf("回答の送信に失敗: %v", err)
	}
	ch, err = e.Extract(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("抽出に失敗: %v", err)
	}
	return collectEvents(t, ch)
}

const extractionScript = `{
  "decisions": [{
    "topic_key": "background/current-state",
    "body": "現状は Excel 台帳で在庫を管理している。",
    "rationale": "回答で明示された",
    "evidence_refs": ["S-0001#utt-00002"]
  }],
  "open_issues": [{
    "topic": "棚卸の頻度を決める",
    "owner": "営業部 佐藤",
    "due": "2026-09-30",
    "needs_stakeholder": true,
    "blocks_requirement_ids": [],
    "evidence_refs": ["S-0001#utt-00002"]
  }],
  "requirement_updates": [{
    "operation": "create",
    "chapter": "functional-requirements",
    "title": "在庫引当",
    "body_after": "受注確定時に在庫を引き当てること。",
    "acceptance_criteria": ["受注確定から 3 秒以内に引当が完了すること"],
    "evidence_refs": ["S-0001#utt-00002"]
  }],
  "term_candidates": [{
    "term": "在庫引当", "english": "Stock Allocation",
    "definition": "受注に対して在庫を確保すること。", "evidence_refs": ["S-0001#utt-00002"]
  }],
  "contradictions": []
}`

// 回答を分析して 3 区分の候補を提示し、承認待ちへ遷移する。
func TestExtractPresentsCandidates(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	last := events[len(events)-1]
	if last.Kind != EventCandidates || last.Extraction == nil {
		t.Fatalf("候補が提示されない: %+v", events)
	}
	ex := last.Extraction
	if len(ex.Decisions) != 1 || len(ex.OpenIssues) != 1 || len(ex.RequirementUpdates) != 1 {
		t.Errorf("3 区分の候補が揃っていない: %+v", ex)
	}
	if ex.Decisions[0].MissingEvidence {
		t.Errorf("実在する根拠が参照欠落と判定された: %+v", ex.Decisions[0])
	}
	if last.State != StateAwaitingApproval {
		t.Errorf("承認待ちにならない: %+v", last)
	}

	// 未承認候補が保全され、読み直せる。
	pending, err := e.PendingExtraction(sess.ID)
	if err != nil {
		t.Fatalf("未承認候補を読み出せない: %v", err)
	}
	if pending == nil || len(pending.Decisions) != 1 {
		t.Fatalf("未承認候補が保全されていない: %+v", pending)
	}
}

// 抽出の入力に発話 ID を載せ、
// AI が根拠の ID を書き損じても抽出対象の回答を根拠として付ける。
//
// extractionScript はスタブが実在 ID を最初から知っているため、「AI が ID を知り得るか」を
// 検証できない（利用者報告の不具合をすり抜けた偽緑）。ここでは当て推量の ID を返させる。
func TestExtractFillsEvidenceFromAnswer(t *testing.T) {
	guessed := strings.ReplaceAll(extractionScript, "S-0001#utt-00002", "S-0001#utt-00099")
	stub := &stubAdapter{scripts: []string{questionScript, guessed}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	// 抽出の入力に、evidence_refs へ書くべき発話 ID が載っている。
	if len(stub.reqs) < 2 || len(stub.reqs[1].Messages) == 0 {
		t.Fatalf("抽出の呼び出しが記録されていない: %d 回", len(stub.reqs))
	}
	sent := stub.reqs[1].Messages[0].Content
	for _, want := range []string{"[エージェント S-0001#utt-00001]", "[担当者 S-0001#utt-00002]"} {
		if !strings.Contains(sent, want) {
			t.Errorf("抽出の入力に発話 ID %q が無い:\n%s", want, sent)
		}
	}

	last := events[len(events)-1]
	if last.Kind != EventCandidates || last.Extraction == nil {
		t.Fatalf("候補が提示されない: %+v", events)
	}
	ex := last.Extraction
	for name, c := range map[string]struct {
		refs    []string
		missing bool
	}{
		"決定事項": {ex.Decisions[0].EvidenceRefs, ex.Decisions[0].MissingEvidence},
		"未決事項": {ex.OpenIssues[0].EvidenceRefs, ex.OpenIssues[0].MissingEvidence},
		"要件項目": {ex.RequirementUpdates[0].EvidenceRefs, ex.RequirementUpdates[0].MissingEvidence},
	} {
		if len(c.refs) != 1 || c.refs[0] != "S-0001#utt-00002" || c.missing {
			t.Errorf("%s候補の根拠が回答の発話にならない: %v（参照欠落=%v）", name, c.refs, c.missing)
		}
	}

	// 補った根拠のまま承認でき、記録に根拠が残る（利用者報告では決定事項の記録で失敗していた）。
	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions:  []DecisionApproval{{Candidate: ex.Decisions[0]}},
		OpenIssues: []OpenIssueCandidate{ex.OpenIssues[0]},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	d, err := store.LoadDecision(result.DecisionIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Evidence) != 1 || d.Evidence[0] != "S-0001#utt-00002" {
		t.Errorf("決定事項に根拠が残っていない: %+v", d.Evidence)
	}
	issue, err := store.LoadOpenIssue(result.OpenIssueIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(issue.Evidence) != 1 || issue.Evidence[0] != "S-0001#utt-00002" {
		t.Errorf("未決事項に根拠が残っていない: %+v", issue.Evidence)
	}
}

// 旧版で保存された「根拠が空の未承認候補」も、読み直すときに抽出対象の回答で補う
// （利用者のプロジェクトは evidence_refs: null の候補を dialogue_state: applying のまま保存していた）。
func TestPendingExtractionFillsSavedEvidence(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	// 旧版が保存した形（根拠が空・参照欠落）に置き換え、反映中で止まった状態にする。
	state, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.PendingCandidates = &Extraction{
		Decisions:  []DecisionCandidate{{TopicKey: "scope/out-of-scope", Body: "開発AIの実行はスコープ外。", MissingEvidence: true}},
		OpenIssues: []OpenIssueCandidate{{Topic: "Excel 取り込みの扱い", Owner: "鈴木", MissingEvidence: true}},
	}
	if err := e.setState(sess.ID, state, StateApplying); err != nil {
		t.Fatal(err)
	}

	pending, err := e.PendingExtraction(sess.ID)
	if err != nil {
		t.Fatalf("未承認候補を読み出せない: %v", err)
	}
	for name, refs := range map[string][]string{
		"決定事項": pending.Decisions[0].EvidenceRefs, "未決事項": pending.OpenIssues[0].EvidenceRefs,
	} {
		if len(refs) != 1 || refs[0] != "S-0001#utt-00002" {
			t.Errorf("保存済みの%s候補に回答の発話が補われない: %v", name, refs)
		}
	}
	// 補った候補のまま押し直せる（旧版では決定事項の記録で失敗していた）。
	if _, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions:  []DecisionApproval{{Candidate: pending.Decisions[0]}},
		OpenIssues: []OpenIssueCandidate{pending.OpenIssues[0]},
	}); err != nil {
		t.Fatalf("補った候補で反映できない: %v", err)
	}
}

// 最後の往復が新しい質問で終わっている（候補の抽出元ではない）ときは補わない。
func TestPendingExtractionDoesNotFillFromLaterQuestion(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	state, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.PendingCandidates = &Extraction{
		Decisions: []DecisionCandidate{{TopicKey: "a/b", Body: "本文", MissingEvidence: true}},
	}
	if err := e.saveMeta(sess.ID, state); err != nil {
		t.Fatal(err)
	}
	// 回答のあとにエージェントの発話（次の質問）が続いている。
	if _, err := store.AppendUtterance(sess.ID, projectstore.Utterance{
		Speaker: projectstore.SpeakerAgent, Body: "次の質問です。"}); err != nil {
		t.Fatal(err)
	}
	pending, err := e.PendingExtraction(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Decisions[0].EvidenceRefs) != 0 || !pending.Decisions[0].MissingEvidence {
		t.Errorf("抽出元でない往復の発話を根拠にした: %+v", pending.Decisions[0])
	}
}

// 新規の要件項目の ID グループ・種別は利用者に入力させず、アプリが決める。
// AI の提案（形式を検証）→ 章観点の既定、の順。非機能要件の章は NFR- になる。
func TestApproveCreatesRequirementIDWithoutUserInput(t *testing.T) {
	script := `{
  "decisions": [],
  "open_issues": [],
  "requirement_updates": [
    {"operation": "create", "chapter": "functional-requirements", "id_group": "inv",
     "title": "在庫引当", "body_after": "受注確定時に在庫を引き当てること。",
     "acceptance_criteria": ["3 秒以内"], "evidence_refs": ["S-0001#utt-00002"]},
    {"operation": "create", "chapter": "scope", "id_group": "💩",
     "title": "スコープ外範囲の明記", "body_after": "開発AIの実行はスコープ外とする。",
     "evidence_refs": ["S-0001#utt-00002"]},
    {"operation": "create", "chapter": "non-functional-requirements", "id_group": null,
     "title": "応答時間", "body_after": "画面遷移は 1 秒以内。",
     "evidence_refs": ["S-0001#utt-00002"]}
  ],
  "term_candidates": [],
  "contradictions": []
}`
	stub := &stubAdapter{scripts: []string{questionScript, script}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	// 承認画面へ出す前に確定している（画面は表示するだけ）。
	wantResolved := []struct{ group, kind string }{
		{"INV", projectstore.RequirementFunctional},
		{"SCOPE", projectstore.RequirementFunctional},
		{"QUAL", projectstore.RequirementNonFunctional},
	}
	for i, w := range wantResolved {
		if c := ex.RequirementUpdates[i]; c.IDGroup != w.group || c.Kind != w.kind {
			t.Errorf("候補 %d（%s）の ID グループ・種別: %q / %q, want %q / %q",
				i+1, c.Title, c.IDGroup, c.Kind, w.group, w.kind)
		}
	}

	// 利用者は ID グループも種別も渡さずに承認する。
	var approvals []RequirementApproval
	for _, c := range ex.RequirementUpdates {
		approvals = append(approvals, RequirementApproval{Candidate: c})
	}
	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{RequirementUpdates: approvals})
	if err != nil {
		t.Fatalf("ID グループを入れずに承認できない: %v", err)
	}
	want := []string{"FR-INV-001", "FR-SCOPE-001", "NFR-QUAL-001"}
	if len(result.RequirementIDs) != len(want) {
		t.Fatalf("記録された要件項目が違う: %v", result.RequirementIDs)
	}
	for i, id := range want {
		if result.RequirementIDs[i] != id {
			t.Errorf("要件項目 %d の ID = %q, want %q", i+1, result.RequirementIDs[i], id)
		}
	}
	r, err := store.LoadRequirement("NFR-QUAL-001")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != projectstore.RequirementNonFunctional {
		t.Errorf("非機能要件の章の項目が機能要件として記録された: %+v", r.Kind)
	}
}

// 旧版で保存された未承認候補（id_group・kind を持たない）も、読み直すときに
// ID グループ・種別が確定する（利用者のプロジェクトに実在する形 = スコープ外範囲の明記）。
func TestPendingExtractionResolvesSavedRequirementIDs(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	state, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.PendingCandidates = &Extraction{RequirementUpdates: []RequirementCandidate{
		{Operation: OperationCreate, Chapter: "scope", Title: "スコープ外範囲の明記",
			BodyAfter: "開発AIの実行はスコープ外とする。", EvidenceRefs: []string{"S-0001#utt-00002"}},
		{Operation: OperationCreate, Chapter: "functional-requirements", Title: "在庫引当",
			BodyAfter: "受注確定時に引き当てる。", EvidenceRefs: []string{"S-0001#utt-00002"}, IDGroup: "INV",
			Kind: projectstore.RequirementFunctional},
	}}
	if err := e.saveMeta(sess.ID, state); err != nil {
		t.Fatal(err)
	}

	pending, err := e.PendingExtraction(sess.ID)
	if err != nil {
		t.Fatalf("未承認候補を読み出せない: %v", err)
	}
	if c := pending.RequirementUpdates[0]; c.IDGroup != "SCOPE" || c.Kind != projectstore.RequirementFunctional {
		t.Errorf("保存済み候補の ID グループ・種別が確定しない: %q / %q", c.IDGroup, c.Kind)
	}
	// 確定済みの値は変えない。
	if c := pending.RequirementUpdates[1]; c.IDGroup != "INV" {
		t.Errorf("確定済みの ID グループが変わった: %q", c.IDGroup)
	}
}

// 記録できない候補が 1 件でもあれば何も記録せず、承認待ちへ戻して押し直せる。
//
// 以前は決定事項を書いたあとで未決事項の検証に失敗し、候補が残ったまま「反映中」で止まっていた
// （押し直すと状態エラー、仮に通れば決定事項が重複記録される）。
func TestApproveValidatesBeforeWriting(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	noOwner := ex.OpenIssues[0]
	noOwner.Owner = ""
	_, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions:  []DecisionApproval{{Candidate: ex.Decisions[0]}},
		OpenIssues: []OpenIssueCandidate{noOwner},
	})
	if err == nil {
		t.Fatal("決める人が空の未決事項候補を反映できてしまった")
	}
	// 直す場所を利用者の言葉で示す（要件 ID・採番前の記録 ID を出さない）。
	if msg := err.Error(); !strings.Contains(msg, "「決める人」が空です") || !strings.Contains(msg, "棚卸の頻度") ||
		strings.Contains(msg, "FR-") || strings.Contains(msg, "DEC-") {
		t.Errorf("直すべき箇所が利用者の言葉で示されない: %v", err)
	}
	// 決定事項は検証を通る候補だが、1 件も書かれていない。
	if ds, _ := store.ListDecisions(); len(ds) != 0 {
		t.Fatalf("失敗したのに決定事項が記録された: %+v", ds)
	}
	// 承認待ちへ戻り、候補も残っている。
	state, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.DialogueState != StateAwaitingApproval {
		t.Errorf("失敗後に承認待ちへ戻らない: %s", state.DialogueState)
	}
	if pending, _ := e.PendingExtraction(sess.ID); pending == nil {
		t.Error("失敗後に候補が消えた")
	}

	// 直して押し直すと、決定事項は 1 件だけ記録される。
	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions:  []DecisionApproval{{Candidate: ex.Decisions[0]}},
		OpenIssues: []OpenIssueCandidate{ex.OpenIssues[0]},
	})
	if err != nil {
		t.Fatalf("直して押し直しても反映できない: %v", err)
	}
	if ds, _ := store.ListDecisions(); len(ds) != 1 || len(result.DecisionIDs) != 1 {
		t.Errorf("決定事項が重複または欠落した: %d 件", len(ds))
	}
}

// 旧版で反映に失敗して「反映中」のまま保存されたセッションでも、候補が残っていれば押し直せる
// （利用者のプロジェクトは dialogue_state: applying で止まっていた）。
func TestApproveRecoversSessionLeftApplying(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	state, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.setState(sess.ID, state, StateApplying); err != nil {
		t.Fatal(err)
	}
	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions: []DecisionApproval{{Candidate: ex.Decisions[0]}},
	})
	if err != nil {
		t.Fatalf("反映中のまま残ったセッションで押し直せない: %v", err)
	}
	if len(result.DecisionIDs) != 1 || result.State != StateQuestioning {
		t.Errorf("押し直しの結果が違う: %+v", result)
	}
}

// スキーマ検証に失敗したら 1 回だけ再要求し、なお失敗したら手動起票へ縮退する。
func TestExtractionSchemaRetryThenFallback(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, "候補はありません。", "やはり JSON では答えられません。"}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "回答です。")

	// 質問 1 回 + 抽出 2 回（初回 + 再要求 1 回）。
	if stub.calls != 3 {
		t.Errorf("再要求の回数が違う: %d 回呼び出し", stub.calls)
	}
	last := events[len(events)-1]
	if last.Kind != EventFallback {
		t.Fatalf("手動起票への縮退にならない: %+v", events)
	}
	if !strings.Contains(last.Text, "やはり JSON では答えられません") {
		t.Errorf("応答原文が提示されない: %+v", last)
	}
	if last.State != StateAwaitingApproval {
		t.Errorf("承認待ち（手動起票の入口）にならない: %+v", last)
	}
}

// 再要求で正しい JSON が返れば候補として提示する。
func TestExtractionSchemaRetrySucceeds(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, "説明だけの応答", extractionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "回答です。")

	last := events[len(events)-1]
	if last.Kind != EventCandidates {
		t.Fatalf("再要求後に候補が提示されない: %+v", events)
	}
}

// 承認した候補だけが記録され、根拠が自動付与される。
func TestApproveCandidatesRecords(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	// 決定事項と要件項目だけ承認し、未決事項・用語候補は破棄する。
	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions:          []DecisionApproval{{Candidate: ex.Decisions[0]}},
		RequirementUpdates: []RequirementApproval{{Candidate: ex.RequirementUpdates[0], Group: "INV"}},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	if len(result.DecisionIDs) != 1 || len(result.RequirementIDs) != 1 {
		t.Fatalf("承認結果が違う: %+v", result)
	}
	if len(result.OpenIssueIDs) != 0 || len(result.TermNames) != 0 {
		t.Errorf("破棄した候補が記録された: %+v", result)
	}
	if result.State != StateQuestioning {
		t.Errorf("反映後に質問生成中へ戻らない: %+v", result)
	}

	d, err := store.LoadDecision(result.DecisionIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Evidence) != 1 || d.Evidence[0] != "S-0001#utt-00002" {
		t.Errorf("根拠が自動付与されていない: %+v", d)
	}
	if !strings.Contains(d.Body, "Excel 台帳") {
		t.Errorf("決定本文が違う: %q", d.Body)
	}
	r, err := store.LoadRequirement(result.RequirementIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "FR-INV-001" || len(r.AcceptanceCriteria) != 1 {
		t.Errorf("要件項目が違う: %+v", r)
	}
	// 同じ承認で作られた決定事項が追跡連鎖として紐づく。
	if len(r.Decisions) != 1 || r.Decisions[0] != d.ID {
		t.Errorf("要件項目に決定事項が紐づいていない: %+v", r.Decisions)
	}
	// 用語集は破棄したので空のまま。
	terms, _ := store.LoadTerms()
	if len(terms.Terms) != 0 {
		t.Errorf("破棄した用語が記録された: %+v", terms.Terms)
	}
	// 未承認候補は消える。
	pending, _ := e.PendingExtraction(sess.ID)
	if pending != nil {
		t.Errorf("反映後も未承認候補が残っている: %+v", pending)
	}
}

// 編集して承認したときは編集後の本文で記録される。
func TestApproveEditedCandidate(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	edited := ex.Decisions[0]
	edited.Body = "現状は Excel 台帳と紙の受払簿を併用している。"
	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions: []DecisionApproval{{Candidate: edited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := store.LoadDecision(result.DecisionIDs[0])
	if !strings.Contains(d.Body, "紙の受払簿") {
		t.Errorf("編集後の本文で記録されていない: %q", d.Body)
	}
}

// 全候補を破棄したときは何も記録されない。
func TestDiscardAllCandidates(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, store := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{})
	if err != nil {
		t.Fatalf("全破棄に失敗: %v", err)
	}
	if len(result.DecisionIDs) != 0 || result.State != StateQuestioning {
		t.Errorf("全破棄の結果が違う: %+v", result)
	}
	decisions, _ := store.ListDecisions()
	issues, _ := store.ListOpenIssues()
	reqs, _ := store.ListRequirements()
	if len(decisions)+len(issues)+len(reqs) != 0 {
		t.Errorf("破棄した候補が記録された: 決定 %d / 未決 %d / 要件 %d", len(decisions), len(issues), len(reqs))
	}
	pending, _ := e.PendingExtraction(sess.ID)
	if pending != nil {
		t.Errorf("破棄後も未承認候補が残っている: %+v", pending)
	}
}

// 未決事項の決着は「決定の記録・未決の決着化・ブロックされていた要件の提示」を 1 操作で行う。
func TestResolveOpenIssueInOneOperation(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, store := newTestEngine(t, stub)

	issue, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "佐藤",
		Evidence: []string{"S-0001#utt-00001"}, Body: "引当のタイミング"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Body: "受注確定時に在庫を引き当てること。",
		BlockedBy: []string{issue.ID}})
	if err != nil {
		t.Fatal(err)
	}

	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "受注確定時に引き当てます。")
	ex := events[len(events)-1].Extraction

	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions: []DecisionApproval{{Candidate: ex.Decisions[0], ResolvesIssueIDs: []string{issue.ID}}},
	})
	if err != nil {
		t.Fatalf("決着に失敗: %v", err)
	}
	if len(result.ResolvedIssueIDs) != 1 || result.ResolvedIssueIDs[0] != issue.ID {
		t.Errorf("未決事項が決着していない: %+v", result)
	}
	if len(result.UnblockedRequirementIDs) != 1 || result.UnblockedRequirementIDs[0] != req.ID {
		t.Errorf("ブロックされていた要件項目が提示されない: %+v", result)
	}
	got, _ := store.LoadOpenIssue(issue.ID)
	if got.Status != projectstore.OpenIssueResolved || got.ResolvedBy != result.DecisionIDs[0] {
		t.Errorf("未決事項の状態が違う: %+v", got)
	}
}

// 承認結果は次の質問生成の文脈に即時反映される。
func TestApprovedRecordsAppearInNextContext(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript,
		"論点キー: background/purpose\n質問: 導入目的は何ですか。\n背景: 次の論点"}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	if _, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions: []DecisionApproval{{Candidate: ex.Decisions[0]}},
	}); err != nil {
		t.Fatal(err)
	}
	ch, err := e.GenerateQuestion(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch)

	// 直近の送信リクエストに、承認した決定事項が既決として載っている。
	sent := stub.reqs[len(stub.reqs)-1]
	body := sent.Messages[0].Content
	if !strings.Contains(body, "DEC-001") || !strings.Contains(body, "Excel 台帳") {
		t.Errorf("承認結果が次の質問生成の文脈に載っていない:\n%s", body)
	}
	if !strings.Contains(body, "既決の論点") {
		t.Errorf("既決論点として注入されていない:\n%s", body)
	}
}

// 承認できない状態では拒否する（承認操作なしの確定記録を作らない）。
func TestApproveRejectedOutsideApprovalState(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	if _, err := e.ApproveCandidates(sess.ID, ApprovalRequest{}); err == nil {
		t.Error("承認待ちでないのに承認できた")
	}
	if _, err := e.Extract(context.Background(), sess.ID); err == nil {
		t.Error("抽出中でないのに抽出できた")
	}
}

// 承認による記録は変更履歴へ残る（決定・未決・要件項目・用語）。
func TestApprovalWritesChangeHistory(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	logger, err := auditlog.New(store, "k.sato@example.co.jp")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Config{Store: store, Adapter: stub, Logger: logger,
		Model:  aiprovider.ModelInfo{ID: "claude-test", ContextWindow: 200000, MaxOutput: 8192},
		Effort: aiprovider.EffortStandard})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := e.StartSession(PhaseRequirements)
	events := answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")
	ex := events[len(events)-1].Extraction

	result, err := e.ApproveCandidates(sess.ID, ApprovalRequest{
		Decisions:          []DecisionApproval{{Candidate: ex.Decisions[0]}},
		OpenIssues:         []OpenIssueCandidate{ex.OpenIssues[0]},
		RequirementUpdates: []RequirementApproval{{Candidate: ex.RequirementUpdates[0], Group: "INV"}},
		TermCandidates:     []TermCandidate{ex.TermCandidates[0]},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}

	changes, err := auditlog.ReadChanges(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	targets := map[string]string{}
	for _, c := range changes {
		targets[c.Target] = c.Change
		if c.Author != "k.sato@example.co.jp" {
			t.Errorf("作業者が記録されていない: %+v", c)
		}
	}
	for _, want := range []string{result.DecisionIDs[0], result.OpenIssueIDs[0], result.RequirementIDs[0], "在庫引当"} {
		if _, ok := targets[want]; !ok {
			t.Errorf("変更履歴に %s が無い: %+v", want, targets)
		}
	}
	if targets[result.DecisionIDs[0]] != auditlog.ChangeCreated {
		t.Errorf("決定事項の変更種別が違う: %s", targets[result.DecisionIDs[0]])
	}
}

// スキーマをプロンプトではなく構造化出力で渡す方式の回帰テスト:
// 抽出の呼び出しは ChatRequest.ResponseSchema でスキーマを渡し、質問生成は非構造化のまま。
//
// スキーマ本体はプロンプトから外したため、ここが欠けると
// スキーマがどこからも渡らない状態になる。プロンプト側の検査だけでは検出できない。
func TestExtractionRequestCarriesResponseSchema(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionScript, extractionScript}}
	e, _ := newTestEngine(t, stub)
	sess, _ := e.StartSession(PhaseRequirements)
	answerAndExtract(t, e, sess.ID, "Excel 台帳で管理しています。")

	if len(stub.reqs) < 2 {
		t.Fatalf("AI 呼び出しが %d 回（質問生成と抽出の 2 回を期待）", len(stub.reqs))
	}
	question, extraction := stub.reqs[0], stub.reqs[len(stub.reqs)-1]

	if len(question.ResponseSchema) != 0 {
		t.Errorf("質問生成は非構造化のはずがスキーマを送っている: %s", question.ResponseSchema)
	}
	if len(extraction.ResponseSchema) == 0 {
		t.Fatal("抽出の ChatRequest に ResponseSchema が設定されていない（スキーマがどこからも渡らない）")
	}
	m, ok, err := extraction.SchemaMap()
	if err != nil || !ok {
		t.Fatalf("抽出のスキーマを map へ展開できない: ok=%v err=%v", ok, err)
	}
	// スキーマは JSON Schema として渡る（項目は properties の下にある）。
	if m["type"] != "object" {
		t.Errorf("抽出スキーマが JSON Schema でない（最上位が type: object でない）: %v", m["type"])
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("抽出スキーマに properties が無い: %v", m)
	}
	for _, key := range []string{"decisions", "open_issues", "requirement_updates", "term_candidates", "contradictions"} {
		if _, has := props[key]; !has {
			t.Errorf("抽出スキーマの properties に %q が無い", key)
		}
	}
}
