package guide

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

/*
 * 工程ガイドの導出。
 *
 * 期待値は要件定義フェーズの業務の流れの分岐から導いた（実行結果を写していない）。
 * 「次にやること」が状態から**実際に変わる**ことを、分岐ごとに 1 件ずつ確かめる。
 */

// 要件がそろい、成果物も確定済みの状態（各テストはここから 1 つだけ崩す）。
func settled() State {
	return State{
		Requirements: 12, Completeness: 100,
		HasDraftDocument: true, ConfirmedDocument: true,
	}
}

func TestDeriveOrdersByWhoIsWaiting(t *testing.T) {
	// 返送された回答は、相手を待たせた分だけ最優先で反映する。
	s := settled()
	s.AnsweredQuestionnaires = 1
	s.UnanalyzedImports = 3 // 同時に未分析の資料があっても、回答の反映が先。
	got := Derive(s)
	if got.StageID != "import-answers" {
		t.Fatalf("回答の反映が最優先のはず: StageID=%q", got.StageID)
	}
	if got.Target != "import-answers" {
		t.Fatalf("移動先が回答取込でない: %q", got.Target)
	}
}

func TestDeriveTellsUnanalyzedImportsWithCount(t *testing.T) {
	s := settled()
	s.UnanalyzedImports = 3
	got := Derive(s)
	if got.StageID != "imports" {
		t.Fatalf("資料の取り込み工程のはず: %q", got.StageID)
	}
	// 件数を文に含める（「まだあります」だけでは何件か分からない）。
	if want := "3 件"; !contains(got.Next, want) {
		t.Fatalf("件数が入っていない: %q", got.Next)
	}
}

func TestDeriveStartsWithDialogueWhenNoRequirements(t *testing.T) {
	got := Derive(State{})
	if got.StageID != "dialogue" {
		t.Fatalf("要件が無いときは対話のはず: %q", got.StageID)
	}
	if got.StageIndex != 2 || got.StageTotal != 6 {
		t.Fatalf("工程の位置が違う: %d / %d", got.StageIndex, got.StageTotal)
	}
}

func TestDeriveContinuesDialogueWhileChaptersUnfilled(t *testing.T) {
	s := settled()
	s.Completeness = 40
	got := Derive(s)
	if got.StageID != "dialogue" {
		t.Fatalf("章が埋まっていないときは対話のはず: %q", got.StageID)
	}
}

func TestDerivePointsAtBlockingIssues(t *testing.T) {
	s := settled()
	s.BlockingIssues = 2
	got := Derive(s)
	if got.Target != "records" {
		t.Fatalf("未決事項の画面へ導くはず: %q", got.Target)
	}
	if !contains(got.Next, "2 件") {
		t.Fatalf("件数が入っていない: %q", got.Next)
	}
}

func TestDerivePointsAtDraftRequirements(t *testing.T) {
	s := settled()
	s.DraftRequirements = 5
	got := Derive(s)
	if got.Target != "requirements" {
		t.Fatalf("要件項目の画面へ導くはず: %q", got.Target)
	}
}

func TestDeriveAsksToGenerateDocument(t *testing.T) {
	s := settled()
	s.HasDraftDocument = false
	s.ConfirmedDocument = false
	got := Derive(s)
	if got.StageID != "documents" {
		t.Fatalf("成果物の生成のはず: %q", got.StageID)
	}
}

func TestDeriveAsksToConfirm(t *testing.T) {
	s := settled()
	s.ConfirmedDocument = false
	s.Confirmable = true
	got := Derive(s)
	if got.StageID != "confirm" {
		t.Fatalf("確定の工程のはず: %q", got.StageID)
	}
}

func TestDeriveExplainsWhyConfirmIsNotPossible(t *testing.T) {
	// 生成済みだが確定できない（理由は成果物画面の確定前チェックが持つ）。
	s := settled()
	s.ConfirmedDocument = false
	s.Confirmable = false
	got := Derive(s)
	if got.Target != "documents" {
		t.Fatalf("成果物の画面へ導くはず: %q", got.Target)
	}
	if got.Next == "" {
		t.Fatal("次にやることが空になっている")
	}
}

func TestQuestionnaireNoteAppearsOnlyWhileIssuesRemain(t *testing.T) {
	// 未決がある間は「関係者へ聞く」道を添える。
	s := State{OpenIssues: 4}
	if note := Derive(s).Note; note == "" {
		t.Fatal("未決事項があるのに質問票への案内が無い")
	}
	// 発行済みで回答待ちなら、待っていることを伝える。
	s = State{OpenIssues: 4, IssuedQuestionnaires: 2}
	got := Derive(s)
	if !contains(got.Note, "2 件") || !contains(got.Note, "回答待ち") {
		t.Fatalf("回答待ちの件数が伝わらない: %q", got.Note)
	}
	// 未決が無ければ添えない（要らない案内を出さない）。
	if note := Derive(State{}).Note; note != "" {
		t.Fatalf("未決が無いのに案内が出ている: %q", note)
	}
}

func TestEveryStageIsReachableAndLabelled(t *testing.T) {
	// 工程の並びと導出結果が食い違っていないこと（ラベル・位置の取りこぼしを防ぐ）。
	for _, s := range Stages {
		if s.ID == "" || s.Label == "" {
			t.Fatalf("工程の定義が欠けている: %+v", s)
		}
	}
	seen := map[string]bool{}
	for _, st := range []State{
		{AnsweredQuestionnaires: 1},
		{UnanalyzedImports: 1},
		{},
		{Requirements: 1, Completeness: 100, BlockingIssues: 1},
		{Requirements: 1, Completeness: 100},
		{Requirements: 1, Completeness: 100, HasDraftDocument: true, Confirmable: true},
	} {
		g := Derive(st)
		if g.StageLabel == "" {
			t.Fatalf("工程名が空: %+v", g)
		}
		if g.StageIndex < 1 || g.StageIndex > len(Stages) {
			t.Fatalf("工程の位置が範囲外: %d", g.StageIndex)
		}
		if g.Next == "" || g.Target == "" || g.Button == "" {
			t.Fatalf("次にやること・移動先・操作名のいずれかが空: %+v", g)
		}
		seen[g.StageID] = true
	}
	// 「質問票」以外の 5 工程は、上の状態で必ず現れる。
	for _, id := range []string{"imports", "dialogue", "import-answers", "documents", "confirm"} {
		if !seen[id] {
			t.Fatalf("工程 %q に到達する状態が無い（導出の分岐と工程の並びが食い違っている）", id)
		}
	}
	// 「質問票」は**現在地としては返さない**。誰に聞くかは担当者の判断であり、
	// 状態から機械的に決められないため、道として添えるだけにする（Note）。
	if seen["questionnaires"] {
		t.Fatal("質問票を現在地として返している（担当者の判断を機械が決めてしまう）")
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestEveryStageDescribesItsPurposeAndScreen(t *testing.T) {
	// 工程の全体像は、この定義だけを見せる。
	// 欠けていると「画面名だけが並ぶ一覧」になり、何をする工程かが分からない。
	seenPurpose := map[string]string{}
	for _, s := range Stages {
		if s.Purpose == "" || s.Screen == "" || s.Target == "" {
			t.Fatalf("工程 %q の説明・画面・行き先のいずれかが空: %+v", s.ID, s)
		}
		if !strings.HasSuffix(s.Purpose, "。") {
			t.Fatalf("工程 %q の説明が 1 文になっていない: %q", s.ID, s.Purpose)
		}
		if prev, ok := seenPurpose[s.Purpose]; ok {
			t.Fatalf("工程 %q と %q の説明が同一（写し間違い）: %q", prev, s.ID, s.Purpose)
		}
		seenPurpose[s.Purpose] = s.ID
	}
}

// TestStageTargetsExistInScreen は、工程の行き先が画面側の view 名の集合に収まっていることを確かめる。
//
// 画面側は知らない行き先に対して移動操作を出さない（押しても何も起きないボタンを作らない）ため、
// 食い違っても**画面は静かに壊れる**（その工程だけ移動できない）。ここで機械的に突き合わせる。
func TestStageTargetsExistInScreen(t *testing.T) {
	const screen = "../../frontend/src/screens/Dialogue.tsx"
	b, err := os.ReadFile(screen)
	if err != nil {
		t.Fatalf("画面のソースを読めない（検査が空振りする）: %v", err)
	}
	views := viewNames(string(b))
	if len(views) < 5 {
		t.Fatalf("view 名を取り出せていない（検査が空振りする）: %v", views)
	}
	for _, s := range Stages {
		if !views[s.Target] {
			t.Fatalf("工程 %q の行き先 %q が画面の view に無い（その工程へ移動できない）", s.ID, s.Target)
		}
	}
}

// viewNames は `const VIEWS = [...] as const` の並びから view 名を取り出す。
func viewNames(src string) map[string]bool {
	out := map[string]bool{}
	i := strings.Index(src, "const VIEWS")
	if i < 0 {
		return out
	}
	rest := src[i:]
	j := strings.Index(rest, "]")
	if j < 0 {
		return out
	}
	for _, m := range regexp.MustCompile(`'([a-z-]+)'`).FindAllStringSubmatch(rest[:j], -1) {
		out[m[1]] = true
	}
	return out
}
