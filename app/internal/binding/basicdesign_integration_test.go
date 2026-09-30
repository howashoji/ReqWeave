//go:build integration

// 結合テスト（基本設計フェーズの対話）。要件定義の確定後に同じ経路で対話が回ることを確認する。

package binding

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const designQuestion = "論点キー: architecture/decisions\n質問: 全体構成をどう分けますか。\n背景: FR-INV-001 を満たす構成を決めるために確認します。選択肢: (a) 三層 (b) 単層。"

const designExtraction = `{
  "decisions": [{"topic_key": "architecture/decisions", "body": "三層構成とする。",
    "rationale": "FR-INV-001 の性能要件のため", "evidence_refs": ["S-0001#utt-00002"]}],
  "open_issues": [], "requirement_updates": [], "term_candidates": [], "contradictions": []
}`

// 要件定義の確定 → 基本設計フェーズへ移行 → 同じ経路で対話・抽出・承認・充足率が動く。
func TestBasicDesignDialogueAfterConfirmation(t *testing.T) {
	a, store, stub := openForConfirm(t)

	// 要件定義を確定して基本設計フェーズへ移る。
	if _, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	if err := a.MoveToBasicDesign(); err != nil {
		t.Fatalf("フェーズ移行に失敗: %v", err)
	}
	// フェーズ変更を対話エンジンへ反映するため開き直す。
	root := store.Root()
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
	opened, err := a.OpenDialogueProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Phase != dialogue.PhaseBasicDesign {
		t.Fatalf("基本設計フェーズになっていない: %+v", opened)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	stub.scripts = []string{designQuestion, designExtraction}
	stub.calls = 0
	stub.requests = nil

	// 基本設計セッションを開始して質問を得る。
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("基本設計の対話を開始できない: %v", err)
	}
	if sess.Phase != dialogue.PhaseBasicDesign {
		t.Fatalf("セッションのフェーズが違う: %+v", sess)
	}
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)

	// 質問に根拠要件 ID と選択肢を添える規律がプロンプトに載る。
	sent := stub.requests[len(stub.requests)-1]
	if !strings.Contains(sent.System, "要件項目 ID") || !strings.Contains(sent.System, "トレードオフ") {
		t.Errorf("基本設計フェーズの規律が指示されていない:\n%s", sent.System)
	}
	// 要件定義フェーズの決定事項・要件項目が文脈に載る。
	if !strings.Contains(sent.Messages[0].Content, "FR-INV-001") {
		t.Errorf("確定済みの要件項目が文脈に載っていない:\n%s", sent.Messages[0].Content)
	}
	// 基本設計の章観点（基本設計書の 8 文書）で充足率を算出する。
	view, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Chapters) != 8 {
		t.Fatalf("基本設計の章観点数が違う: %d", len(view.Chapters))
	}
	if view.Chapters[0].ChapterID != "architecture" {
		t.Errorf("章観点の並びが違う: %+v", view.Chapters[0])
	}

	// 回答 → 抽出 → 承認まで同じ経路で動く。
	if _, err := a.SendAnswer(sess.ID, "三層構成にします。"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingApproval)
	pending, err := a.PendingCandidates(sess.ID)
	if err != nil || pending == nil || len(pending.Decisions) != 1 {
		t.Fatalf("基本設計の抽出候補が出ない: %+v %v", pending, err)
	}
	result, err := a.ApproveDialogueCandidates(sess.ID, dialogue.ApprovalRequest{
		Decisions: []dialogue.DecisionApproval{{Candidate: pending.Decisions[0]}},
	})
	if err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}
	if len(result.Applied.DecisionIDs) != 1 {
		t.Fatalf("決定事項が記録されない: %+v", result)
	}

	// 承認した決定が基本設計の章観点の充足率へ反映される。
	after, err := a.DialogueCompleteness()
	if err != nil {
		t.Fatal(err)
	}
	if after.Chapters[0].Percent == 0 {
		t.Errorf("基本設計の充足率が更新されない: %+v", after.Chapters[0])
	}
	// 決定事項は章観点 ID つきで記録され、基本設計書の章割当に載る。
	d, err := s.store.LoadDecision(result.Applied.DecisionIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.TopicKey, "architecture/") {
		t.Errorf("章観点 ID が付いていない: %+v", d)
	}
}
