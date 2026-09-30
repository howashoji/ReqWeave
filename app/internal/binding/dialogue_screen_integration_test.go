//go:build integration

// 結合テスト（対話バインディング × 実ファイル）。AI プロバイダのみ差し替える。

package binding

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const dialogueQuestion = "論点キー: background/current-state\n質問: 現状の在庫管理業務はどのように行っていますか。\n背景: 現状業務を記録するために確認します。"

const dialogueExtraction = `{
  "decisions": [{"topic_key": "background/current-state", "body": "Excel 台帳で管理している。",
    "rationale": "回答で明示", "evidence_refs": ["S-0001#utt-00002"]}],
  "open_issues": [], "requirement_updates": [], "term_candidates": [], "contradictions": []
}`

// streamingStub は対話用の応答を返すアダプタ。
type streamingStub struct {
	stubAdapter
	scripts  []string
	calls    int
	requests []aiprovider.ChatRequest
	// streamErr が非 nil のとき、送信のたびにこのエラーを返す（AI API 障害の再現）。
	streamErr *aiprovider.ProviderError
}

// reqs は送信済みリクエストを返す（文脈の検証用）。
func (s *streamingStub) reqs() []aiprovider.ChatRequest { return s.requests }

func (s *streamingStub) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
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
		if s.streamErr != nil {
			ch <- aiprovider.StreamEvent{Kind: aiprovider.EventError, Err: s.streamErr}
			return
		}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta, Text: body}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone}
	}()
	return ch, nil
}

// newDialogueAPI は初期設定済みの API と対話用プロジェクトを用意する。
//
// 対話ではアダプタの StreamMessage を使うため、生成関数が streamingStub 自身を返すようにする。
func newDialogueAPI(t *testing.T, stub *streamingStub) (*API, string) {
	t.Helper()
	paths := projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}
	label := keytest.Marker + uuid.NewString()
	a := &API{
		paths: paths,
		keys:  keymanager.New(),
		newAdapter: func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
			opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
			stub.stubAdapter.id = id
			stub.stubAdapter.keys = keys
			stub.stubAdapter.ref = ref
			return stub, nil
		},
	}
	// テストが中断されてもキーチェーンに残骸を残さない。
	keytest.TrackKey(t, keymanager.Ref{ProviderID: "anthropic", Label: label})
	if _, err := a.RegisterKey("anthropic", label, dummyKey); err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteSetup(SetupRequest{
		ProviderID: "anthropic", Label: label, Model: "claude-opus-5", Effort: "standard",
		AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤",
	}); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "在庫管理システム")
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	author, _ := settings.Author()
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム", Author: author,
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	_ = store.Close()
	return a, root
}

// 対話画面: 開く → セッション開始 → 質問 → 回答 → 候補 → 承認 → 完成度更新。
func TestDialogueBindingFlow(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion, dialogueExtraction}}
	a, root := newDialogueAPI(t, stub)

	opened, err := a.OpenDialogueProject(root)
	if err != nil {
		t.Fatalf("対話用に開けない: %v", err)
	}
	if opened.Phase != "requirements" || opened.TargetName != "在庫管理システム" {
		t.Errorf("開いた内容が違う: %+v", opened)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	if sess.ID != "S-0001" || sess.Type != projectstore.SessionOwner {
		t.Fatalf("セッションが違う: %+v", sess)
	}

	ok, err := a.AskNextQuestion(sess.ID)
	if err != nil || !ok {
		t.Fatalf("質問を要求できない: %v (ok=%v)", err, ok)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	utterances, err := a.DialogueUtterances(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(utterances) != 1 || !strings.Contains(utterances[0].Body, "質問:") {
		t.Fatalf("質問が保存されていない: %+v", utterances)
	}

	if _, err := a.SendAnswer(sess.ID, "Excel 台帳で管理しています。"); err != nil {
		t.Fatalf("回答を送れない: %v", err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingApproval)

	pending, err := a.PendingCandidates(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.Decisions) != 1 {
		t.Fatalf("候補が保全されていない: %+v", pending)
	}

	before, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	if before.Chapters[0].Percent != 0 {
		t.Errorf("承認前から充足している: %+v", before.Chapters[0])
	}

	result, err := a.ApproveDialogueCandidates(sess.ID, dialogue.ApprovalRequest{
		Decisions: []dialogue.DecisionApproval{{Candidate: pending.Decisions[0]}},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	if len(result.Applied.DecisionIDs) != 1 {
		t.Fatalf("承認結果が違う: %+v", result)
	}

	after, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	if after.Chapters[0].Percent == before.Chapters[0].Percent {
		t.Errorf("完成度が更新されない: %d → %d", before.Chapters[0].Percent, after.Chapters[0].Percent)
	}
	// 確定可否は同じ画面値から返る（UI 側で再計算しない。判定の正本をバックエンドの 1 か所に置く）。
	if after.Confirmation.Confirmable != dialogue.Confirmable(recordsOf(t, a)).Confirmable {
		t.Errorf("確定可否が食い違う: %+v", after.Confirmation)
	}
}

// AI キーが未設定・疎通未確認のときは対話を開始できず、キー未設定である理由が返る。
func TestDialogueBlockedWhenAINotReady(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)

	// 初期設定済みの状態からキーだけを削除する（作業者・プロバイダ設定は残る）。
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := settings.DefaultProviderSetting()
	if !ok {
		t.Fatal("既定プロバイダが無い")
	}
	if err := a.DeleteKey(provider.Provider, provider.Label); err != nil {
		t.Fatalf("キーを削除できない: %v", err)
	}

	if _, err := a.OpenDialogueProject(root); err == nil {
		t.Fatal("キー未設定でも対話を開けた")
	} else if !strings.Contains(err.Error(), "キー") {
		t.Errorf("キー未設定である理由が示されない: %v", err)
	}

	// 質問生成・回答送信も同じ前段で止まる（AI 実行口の共通前段）。
	if _, err := a.AskNextQuestion("S-0001"); err == nil {
		t.Error("キー未設定でも質問を要求できた")
	}
}

// 対話履歴に書き換え API が無い（読み出しのみ。監査の証跡として改変させない）。
func TestDialogueHistoryIsReadOnly(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	sess, _ := a.StartDialogueSession()
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	sessions, err := a.DialogueSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("セッション一覧が違う: %+v", sessions)
	}
	// 発話を書き換える公開メソッドが存在しないことは API の定義で担保する。
	// ここでは読み出しが参照専用（同じ内容を返す）ことを確認する。
	first, _ := a.DialogueUtterances(sess.ID)
	second, _ := a.DialogueUtterances(sess.ID)
	if len(first) != len(second) || first[0].Body != second[0].Body {
		t.Errorf("読み出しで内容が変わった")
	}
}

// 対話履歴の全文検索が、絞り込みと併用でき、参照専用で返る。
func TestSearchDialogueUtterances(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	sess, _ := a.StartDialogueSession()
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	utterances, err := a.DialogueUtterances(sess.ID)
	if err != nil || len(utterances) == 0 {
		t.Fatalf("発話が無い: %+v %v", utterances, err)
	}
	// 実際に保存された発話本文から語句を採る（固定文字列を書くと本文が変わったとき空振りする）。
	word := []rune(utterances[0].Body)
	if len(word) < 4 {
		t.Fatalf("検索語を採れる長さの発話が無い: %q", utterances[0].Body)
	}
	needle := string(word[:4])

	got, err := a.SearchDialogueUtterances(needle, "all", "all")
	if err != nil {
		t.Fatalf("検索に失敗: %v", err)
	}
	if got.Total < 1 {
		t.Fatalf("保存済みの発話が検索で見つからない（語句 %q）: %+v", needle, got)
	}
	hit := got.Hits[0]
	if hit.SessionID != sess.ID || hit.ID == "" || hit.At == "" || hit.Excerpt == "" {
		t.Errorf("結果の項目が揃っていない: %+v", hit)
	}
	if !strings.Contains(hit.Excerpt, needle) {
		t.Errorf("抜粋に該当箇所が入っていない: %q", hit.Excerpt)
	}
	if got.Limit != projectstore.DefaultUtteranceSearchLimit {
		t.Errorf("上限が画面へ伝わっていない: %+v", got)
	}

	// 絞り込みと併用する（対象外のフェーズを指定すると 0 件になる）。
	other := "basic-design"
	if sessions, _ := a.DialogueSessions(); len(sessions) > 0 && sessions[0].Phase == other {
		other = "requirements"
	}
	filtered, err := a.SearchDialogueUtterances(needle, other, "all")
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 0 {
		t.Errorf("フェーズの絞り込みが効いていない: %+v", filtered)
	}

	// 語句なしは拒否する（原因と次の行動の 1 文）。
	if _, err := a.SearchDialogueUtterances("   ", "all", "all"); err == nil {
		t.Error("空の語句が受理された")
	}

	// 参照専用: 検索しても発話は変わらない。
	after, _ := a.DialogueUtterances(sess.ID)
	if len(after) != len(utterances) || after[0].Body != utterances[0].Body {
		t.Error("検索で発話が変わった")
	}
}

// 中断して再開すると、提示済み質問と未決事項が復元される。
func TestDialogueResume(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	sess, _ := a.StartDialogueSession()
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)
	if err := a.SuspendDialogue(sess.ID); err != nil {
		t.Fatalf("中断に失敗: %v", err)
	}
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}

	// 開き直して再開する。
	opened, err := a.OpenDialogueProject(root)
	if err != nil {
		t.Fatalf("開き直せない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if len(opened.Sessions) != 1 || opened.Sessions[0].ID != sess.ID {
		t.Fatalf("既存セッションが一覧に出ない: %+v", opened.Sessions)
	}
	resumed, err := a.ResumeDialogueSession(sess.ID)
	if err != nil {
		t.Fatalf("再開に失敗: %v", err)
	}
	if resumed.State != dialogue.StateSuspended {
		t.Errorf("状態が復元されない: %+v", resumed)
	}
	if resumed.PresentedQuestion == nil || resumed.PresentedQuestion.TopicKey != "background/current-state" {
		t.Errorf("提示済み質問が復元されない: %+v", resumed.PresentedQuestion)
	}
}

// プロジェクトを開かずに対話操作を呼ぶと、理由つきで拒否される。
func TestDialogueRequiresOpenProject(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion}}
	a, _ := newDialogueAPI(t, stub)
	if _, err := a.StartDialogueSession(); err == nil {
		t.Error("開いていないのにセッションを開始できた")
	}
	if _, err := a.DialogueCompleteness(); err == nil {
		t.Error("開いていないのに完成度を取得できた")
	}
	if err := a.InterruptDialogue(); err != nil {
		t.Errorf("開いていない状態の中断でエラーになった: %v", err)
	}
}

// waitForState は対話状態が期待値になるまで待つ（イベントは非同期のため）。
func waitForState(t *testing.T, a *API, sessionID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := a.current()
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.engine.LoadState(sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if got.DialogueState == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("状態が %s にならない", want)
}

// recordsOf は現在のレコードを返す（確定可否の突き合わせ用）。
func recordsOf(t *testing.T, a *API) dialogue.Records {
	t.Helper()
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.engine.Records()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// 要件定義対話の基本フローを一巡させる。
//
// 配布物を起動する e2e では AI プロバイダを差し替えられない（エンドポイントの書き換え手段を
// 持たない／テストで実キーを使わない）ため、要件定義対話の一巡はバインディング層の結合テストで検証する。
func TestUC03FullCycle(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion, dialogueExtraction, dialogueQuestion}}
	a, root := newDialogueAPI(t, stub)

	// 1. プロジェクトを開く（対話の事前条件）。
	opened, err := a.OpenDialogueProject(root)
	if err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if opened.Phase != "requirements" {
		t.Fatalf("フェーズが違う: %+v", opened)
	}

	// 2. 対話を開始する。
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("対話を開始できない: %v", err)
	}

	// 3. 質問が提示される。
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatalf("質問を要求できない: %v", err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)
	utterances, err := a.DialogueUtterances(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(utterances) != 1 || !strings.Contains(utterances[0].Body, "背景:") {
		t.Fatalf("質問＋背景説明が提示されない: %+v", utterances)
	}

	// 4. 回答を送る。発話は AI 送信前に保存される。
	if _, err := a.SendAnswer(sess.ID, "Excel 台帳で管理しています。"); err != nil {
		t.Fatalf("回答を送れない: %v", err)
	}

	// 5. 抽出候補が提示される。
	waitForState(t, a, sess.ID, dialogue.StateAwaitingApproval)
	pending, err := a.PendingCandidates(sess.ID)
	if err != nil || pending == nil || len(pending.Decisions) != 1 {
		t.Fatalf("抽出候補が提示されない: %+v %v", pending, err)
	}

	before, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}

	// 6. 承認して反映する。
	result, err := a.ApproveDialogueCandidates(sess.ID, dialogue.ApprovalRequest{
		Decisions: []dialogue.DecisionApproval{{Candidate: pending.Decisions[0]}},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	if len(result.Applied.DecisionIDs) != 1 {
		t.Fatalf("決定事項が記録されない: %+v", result)
	}

	// 7. 完成度が更新される。
	after, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	if after.Chapters[0].Percent <= before.Chapters[0].Percent {
		t.Fatalf("完成度が更新されない: %d → %d", before.Chapters[0].Percent, after.Chapters[0].Percent)
	}
	// 決定事項一覧・要件項目一覧からも参照できる。
	decisions, err := a.Decisions()
	if err != nil || len(decisions) != 1 {
		t.Fatalf("決定事項一覧に出ない: %+v %v", decisions, err)
	}
	if len(decisions[0].Evidence) == 0 {
		t.Errorf("根拠が付与されていない: %+v", decisions[0])
	}

	// 8. 中断する。
	if err := a.SuspendDialogue(sess.ID); err != nil {
		t.Fatalf("中断に失敗: %v", err)
	}
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}

	// 9. 開き直して再開すると文脈が復元される。
	reopened, err := a.OpenDialogueProject(root)
	if err != nil {
		t.Fatalf("開き直せない: %v", err)
	}
	if len(reopened.Sessions) != 1 {
		t.Fatalf("既存セッションが出ない: %+v", reopened.Sessions)
	}
	resumed, err := a.ResumeDialogueSession(sess.ID)
	if err != nil {
		t.Fatalf("再開に失敗: %v", err)
	}
	if resumed.State != dialogue.StateSuspended || resumed.PresentedQuestion == nil {
		t.Fatalf("文脈が復元されない: %+v", resumed)
	}

	// 10. 再開後も対話を続けられる（既決の論点は再質問されない）。
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatalf("再開後に質問を要求できない: %v", err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)
	sent := stub.reqs()
	last := sent[len(sent)-1]
	if !strings.Contains(last.Messages[0].Content, "DEC-001") {
		t.Errorf("承認済みの決定が次の質問生成の文脈に載っていない")
	}
}
