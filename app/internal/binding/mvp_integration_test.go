//go:build integration

// 結合テスト（単独利用 MVP の一巡）。
//
// 対話 → 要件定義書の生成 → 確定 → 基本設計の対話 → 基本設計書の生成 → 確定 → エクスポートを
// 1 本で通す。AI はテスト用スタブで決定的に行い、実キーは使わない（シークレットキーをテストに持ち込まない）。

package binding

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const mvpRequirementQuestion = "論点キー: functional-requirements/list\n質問: 在庫引当はいつ行いますか。\n背景: 機能要件を記録するために確認します。"

const mvpRequirementExtraction = `{
  "decisions": [{"topic_key": "functional-requirements/list", "body": "受注確定時に在庫を引き当てる。",
    "rationale": "回答で明示", "evidence_refs": ["S-0001#utt-00002"]}],
  "open_issues": [],
  "requirement_updates": [{"operation": "create", "chapter": "functional-requirements",
    "title": "在庫引当", "body_after": "受注確定時に在庫を引き当てること。",
    "acceptance_criteria": ["受注確定から 3 秒以内に引当が完了すること"],
    "evidence_refs": ["S-0001#utt-00002"]}],
  "term_candidates": [], "contradictions": []
}`

const mvpDesignQuestion = "論点キー: architecture/decisions\n質問: 全体構成をどう分けますか。\n背景: FR-INV-001 を満たす構成を決めます。選択肢: (a) 三層 (b) 単層（トレードオフ: 保守性と実装量）。"

const mvpDesignExtraction = `{
  "decisions": [{"topic_key": "architecture/decisions", "body": "三層構成とする。",
    "rationale": "FR-INV-001 の保守性のため", "evidence_refs": ["S-0002#utt-00002"]},
    {"topic_key": "architecture/traced-requirements", "body": "FR-INV-001 を全体構成の根拠とする。",
    "rationale": "追跡連鎖のため", "evidence_refs": ["S-0002#utt-00002"]}],
  "open_issues": [], "requirement_updates": [], "term_candidates": [], "contradictions": []
}`

// mvpChapterBody は章生成のスタブ応答（要件 ID を参照し、曖昧語を含まない）。
const mvpChapterBody = "## 本文\n\nFR-INV-001 に基づき、受注確定時に在庫を引き当てる。"

// 要件定義書の生成からエクスポートまでの一巡（単独利用 MVP）。
func TestSingleUserMVPCycle(t *testing.T) {
	stub := &streamingStub{scripts: []string{mvpRequirementQuestion}}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}

	// --- 要件定義の対話（1 往復）で要件項目と決定事項を作る。
	sess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AskNextQuestion(sess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingAnswer)
	stub.scripts = []string{mvpRequirementExtraction}
	stub.calls = 0
	if _, err := a.SendAnswer(sess.ID, "受注確定時に引き当てます。"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, sess.ID, dialogue.StateAwaitingApproval)
	pending, err := a.PendingCandidates(sess.ID)
	if err != nil || pending == nil {
		t.Fatalf("抽出候補が出ない: %v", err)
	}
	if _, err := a.ApproveDialogueCandidates(sess.ID, dialogue.ApprovalRequest{
		Decisions:          []dialogue.DecisionApproval{{Candidate: pending.Decisions[0]}},
		RequirementUpdates: []dialogue.RequirementApproval{{Candidate: pending.RequirementUpdates[0], Group: "INV"}},
	}); err != nil {
		t.Fatalf("承認に失敗: %v", err)
	}

	// --- 要件定義書の生成。
	stub.scripts = []string{mvpChapterBody}
	stub.calls = 0
	if err := a.GenerateDocument(projectstore.DocKindRequirements, false, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatalf("要件定義書の生成に失敗: %v", err)
	}
	waitForChapters(t, a, projectstore.DocKindRequirements, 16)

	// --- 確定 → 基本設計フェーズへ移行。
	check, err := a.ConfirmCheck(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Confirmable {
		t.Fatalf("確定できない: %+v", check)
	}
	confirmed, err := a.ConfirmDocument(projectstore.DocKindRequirements, WorkStart{Mode: projectstore.ReservationExclusive})
	if err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	if confirmed.Version != 1 {
		t.Fatalf("要件定義の確定版が v1 でない: %+v", confirmed)
	}
	if err := a.MoveToBasicDesign(); err != nil {
		t.Fatalf("基本設計フェーズへ移行できない: %v", err)
	}

	// フェーズ変更を反映するため開き直す。
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	s, err = a.current()
	if err != nil {
		t.Fatal(err)
	}

	// --- 基本設計の対話。
	stub.scripts = []string{mvpDesignQuestion}
	stub.calls = 0
	designSess, err := a.StartDialogueSession()
	if err != nil {
		t.Fatalf("基本設計の対話を開始できない: %v", err)
	}
	if _, err := a.AskNextQuestion(designSess.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, designSess.ID, dialogue.StateAwaitingAnswer)
	stub.scripts = []string{mvpDesignExtraction}
	stub.calls = 0
	if _, err := a.SendAnswer(designSess.ID, "三層構成にします。"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, a, designSess.ID, dialogue.StateAwaitingApproval)
	designPending, err := a.PendingCandidates(designSess.ID)
	if err != nil || designPending == nil || len(designPending.Decisions) != 2 {
		t.Fatalf("基本設計の抽出候補が出ない: %+v %v", designPending, err)
	}
	if _, err := a.ApproveDialogueCandidates(designSess.ID, dialogue.ApprovalRequest{
		Decisions: []dialogue.DecisionApproval{
			{Candidate: designPending.Decisions[0]}, {Candidate: designPending.Decisions[1]},
		},
	}); err != nil {
		t.Fatalf("基本設計の承認に失敗: %v", err)
	}

	// --- 基本設計書の生成と確定。
	stub.scripts = []string{mvpChapterBody}
	stub.calls = 0
	if err := a.GenerateDocument(projectstore.DocKindBasicDesign, false, WorkStart{Mode: projectstore.ReservationExclusive}); err != nil {
		t.Fatalf("基本設計書の生成に失敗: %v", err)
	}
	// 基本設計は 8 章観点 + 共通 3 文書（用語集・決定・未決）= 11 ファイル。
	waitForChapters(t, a, projectstore.DocKindBasicDesign, 11)
	designConfirmed, err := a.ConfirmDocument(projectstore.DocKindBasicDesign, WorkStart{Mode: projectstore.ReservationExclusive})
	if err != nil {
		t.Fatalf("基本設計の確定に失敗: %v", err)
	}
	if designConfirmed.Version != 1 {
		t.Fatalf("基本設計の確定版が v1 でない: %+v", designConfirmed)
	}

	// 確定版が 2 つ（要件定義 v1・基本設計 v1）残っている。
	reqChapters, err := s.store.LoadVersion(projectstore.DocKindRequirements, 1)
	if err != nil || len(reqChapters) != 16 {
		t.Fatalf("要件定義 v1 が失われた: %d 件 %v", len(reqChapters), err)
	}
	designChapters, err := s.store.LoadVersion(projectstore.DocKindBasicDesign, 1)
	if err != nil || len(designChapters) != 11 {
		t.Fatalf("基本設計 v1 が失われた: %d 件 %v", len(designChapters), err)
	}

	// --- エクスポート。
	dest := filepath.Join(t.TempDir(), "handoff")
	exported, err := a.ExportDocuments(ExportRequestView{
		Destination: dest, Requirements: 1, BasicDesign: 1,
		IncludeBasicDesign: true, AcceptWarnings: true,
	})
	if err != nil {
		t.Fatalf("エクスポートに失敗: %v", err)
	}
	if !exported.Exported {
		t.Fatalf("出力されない: %+v", exported)
	}

	// 出力の完全性: 要件定義 13 + 共通 3 + 基本設計 8 + 導入・様式・レポート 3 = 27 ファイル。
	if len(exported.Files) != 27 {
		t.Errorf("出力ファイル数が違う: %d\n%v", len(exported.Files), exported.Files)
	}
	for _, want := range []string{
		"CLAUDE.md", "feedback-template.md", "export-report.md",
		"00-project/glossary.md", "00-project/decisions.md", "00-project/issues.md",
		"10-requirements/00-index.md", "10-requirements/12-risks-assumptions.md",
		"20-basic-design/01-architecture.md", "20-basic-design/08-error-handling-security.md",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s が出力されていない: %v", want, err)
		}
	}

	// 導入ファイルの相対リンクがすべて出力フォルダ内で解決できる。
	intro, err := os.ReadFile(filepath.Join(dest, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	links := regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(string(intro), -1)
	if len(links) < 20 {
		t.Errorf("導入ファイルのリンクが少ない: %d 件", len(links))
	}
	for _, m := range links {
		if strings.HasPrefix(m[1], "/") || strings.Contains(m[1], "://") {
			t.Errorf("絶対パス・外部リンクが含まれる: %s", m[1])
			continue
		}
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("リンク先が解決できない: %s", m[1])
		}
	}
	// 成果物に要件 ID が載り、追跡できる。
	fr, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash("10-requirements/06-functional-requirements.md")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fr), "FR-INV-001") {
		t.Errorf("機能要件の章に要件 ID が無い:\n%s", fr)
	}
}
