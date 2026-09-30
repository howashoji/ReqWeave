//go:build integration

// 画面応答の性能基準を**上限規模の実データ**で計測する。
//
// これまで未検証だった理由は「上限規模のテストデータを作る手段が無かった」ことなので、
// 生成は `tools/scalegen`（検証専用）に置き、本テストはそれを実際に走らせてから測る。
//
// ---- 何を測り、何を測っていないか ----------------------------------------
//
// この基準は**画面**の応答時間である。画面の応答は
// 「バインディング層より下（ディスク I/O・解釈）」＋「フロントエンドの描画」の 2 つからなる。
// アプリの webview を自動操作する手段が無いため（e2e は DOM 到達までで、操作はできない）、
// **2 つを別の段で測り、それぞれに配分した持ち分を課す**。
//
//	基準（画面応答）          本テスト（バインディング層）  実ブラウザ（*.layout.test.tsx）
//	(1) 開く→対話画面 3,000ms   2,000ms                      1,000ms
//	(2) 画面遷移・一覧 1,000ms     660ms                        340ms
//	(3) 履歴の走査・検索 2,000ms   1,330ms                        670ms
//
// 基準(3)の「検索結果表示」は対話履歴の全文検索が対象で、後から追加した
// 検索の計測がこれに当たる（それ以前はスクロール・絞り込みだけを測っていた）。
//
// 配分は 2:1（下が 2、描画が 1）。**合計は基準値と一致する**ため、両方が持ち分内なら
// 基準を満たす。片方が持ち分を超えたときは（合計が基準内でも）失敗する側に倒している
// ＝ 見逃しではなく過検出の向きで倒す。フロントエンド側は
// `frontend/src/screens/scale-response.layout.test.tsx`。
//
// 計測が空振り（規模が上限に達していない状態での計測）でないことは、
// 生成器が数え直した実測値を検査してから測ることで担保する（TestScaleResponse の前段）。

package binding

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 持ち分（上の表の「本テスト」列）。
const (
	budgetOpenDialogue  = 2000 * time.Millisecond
	budgetScreenSwitch  = 660 * time.Millisecond
	budgetHistoryBrowse = 1330 * time.Millisecond
)

// 試行回数（基準の受け入れ条件は 5 回計測して全て満たすこと）。
const scaleTrials = 5

// generatedScale は scalegen の標準出力から読んだ規模の実測値 1 件。
type generatedScale struct {
	kind    string
	current int64
	limit   int64
	level   string
}

// TestScaleResponse は上限規模のプロジェクトを生成し、画面ごとの応答時間を測る。
func TestScaleResponse(t *testing.T) {
	stub := &streamingStub{scripts: []string{dialogueQuestion, dialogueExtraction}}
	a, _ := newDialogueAPI(t, stub)

	root, scales := generateScaleProject(t, a.paths.Base)

	// ---- 空振り防止: 上限目安表の全 8 項目が上限に達していることを先に確かめる ----
	assertAtLimit(t, scales)

	// ---- (2) 一覧表示: プロジェクト一覧（50 プロジェクト） ----
	measure(t, "一覧表示: プロジェクト一覧（50 件）", budgetScreenSwitch, func() error {
		list, err := a.Projects()
		if err != nil {
			return err
		}
		if len(list) != 50 {
			return fmt.Errorf("プロジェクト一覧が 50 件でない: %d 件", len(list))
		}
		return nil
	})

	// ---- (1) プロジェクトを開いてから対話画面の表示に必要な取得が揃うまで ----
	//
	// 画面（Dialogue.tsx）が入場時に呼ぶ順序をそのまま辿る:
	// 開く → 変更要約 → 番号帯 → セッション再開 → 未承認候補 → 発話履歴 → 完成度 → 工程ガイド。
	// 規模の通知（ScaleStatus）は App が同時に引くため含める。
	measure(t, "(1) 開く→対話画面の表示に要る取得が揃うまで", budgetOpenDialogue, func() error {
		opened, err := a.OpenDialogueProject(root)
		if err != nil {
			return err
		}
		if len(opened.Sessions) != 100 {
			return fmt.Errorf("セッションが 100 件でない: %d 件", len(opened.Sessions))
		}
		if _, err := a.ChangeSummary(); err != nil {
			return err
		}
		if _, err := a.IDRangeWarnings(); err != nil {
			return err
		}
		first := opened.Sessions[0].ID
		if _, err := a.ResumeDialogueSession(first); err != nil {
			return err
		}
		if _, err := a.PendingCandidates(first); err != nil {
			return err
		}
		utterances, err := a.DialogueUtterances(first)
		if err != nil {
			return err
		}
		if len(utterances) != 1000 {
			return fmt.Errorf("発話履歴が 1,000 件でない: %d 件", len(utterances))
		}
		if _, err := a.DialogueCompleteness(); err != nil {
			return err
		}
		if _, err := a.WorkflowGuide(); err != nil {
			return err
		}
		if _, err := a.ScaleStatus(); err != nil {
			return err
		}
		return a.CloseDialogueProject()
	})

	// 以降の画面遷移はプロジェクトを開いた状態で測る（画面遷移は開いたまま行う操作）。
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("上限規模のプロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	// ---- (2) 画面間の遷移・一覧表示（主要 5 経路。対話画面からの遷移先） ----
	measure(t, "(2) 対話→要件項目一覧（1,000 件）", budgetScreenSwitch, func() error {
		list, err := a.Requirements()
		if err != nil {
			return err
		}
		if len(list) != 1000 {
			return fmt.Errorf("要件項目が 1,000 件でない: %d 件", len(list))
		}
		return nil
	})
	measure(t, "(2) 対話→決定事項・未決事項（500+500 件）", budgetScreenSwitch, func() error {
		decisions, err := a.Decisions()
		if err != nil {
			return err
		}
		issues, err := a.OpenIssues()
		if err != nil {
			return err
		}
		if _, err := a.ChangeHistory(); err != nil {
			return err
		}
		if len(decisions) != 500 || len(issues) != 500 {
			return fmt.Errorf("決定 %d 件 / 未決 %d 件（各 500 件でない）", len(decisions), len(issues))
		}
		return nil
	})
	measure(t, "(2) 対話→質問票一覧（100 件）", budgetScreenSwitch, func() error {
		list, err := a.Questionnaires()
		if err != nil {
			return err
		}
		if len(list) != 100 {
			return fmt.Errorf("質問票が 100 件でない: %d 件", len(list))
		}
		return nil
	})
	measure(t, "(2) 対話→既存資料一覧", budgetScreenSwitch, func() error {
		list, err := a.Imports()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("既存資料が 0 件（総量 500MB を埋めた資料が見えていない）")
		}
		return nil
	})
	measure(t, "(2) 対話→対話履歴（セッション 100 件の一覧）", budgetScreenSwitch, func() error {
		list, err := a.DialogueSessions()
		if err != nil {
			return err
		}
		if len(list) != 100 {
			return fmt.Errorf("セッションが 100 件でない: %d 件", len(list))
		}
		return nil
	})

	// ---- (3) 対話履歴の走査（最も発話の多いセッションの読み出し） ----

	// ---- (3) 対話履歴の全文検索（基準(3)の「検索結果表示」に当たる） ----
	//
	// **最悪ケースを測る**: filler に必ず含まれる語を引き、上限規模の全発話（10 万件）に当てる。
	// 索引を持たない逐次走査なので、これが検索の上限コストである。
	measure(t, "(3) 対話履歴の全文検索（全 10 万発話に当たる語）", budgetHistoryBrowse, func() error {
		found, err := a.SearchDialogueUtterances("在庫管理", "all", "all")
		if err != nil {
			return err
		}
		// 空振り防止: 上限規模の全発話（100 セッション × 1,000 発話）を走査し切っていること。
		if found.Total != 100*1000 {
			return fmt.Errorf("該当が 10 万件でない（走査し切っていない）: %d 件", found.Total)
		}
		if !found.Truncated || len(found.Hits) != projectstore.DefaultUtteranceSearchLimit {
			return fmt.Errorf("一覧が上限で切られていない: %d 件（truncated=%v）", len(found.Hits), found.Truncated)
		}
		return nil
	})

	// **末尾にしか無い語**での検索。走査が最後のセッションの最後の発話まで届くことを、
	// 件数ではなく到達点で確かめる（生成器の本文の先頭行は 1 発話ごとに一意）。
	measure(t, "(3) 対話履歴の全文検索（末尾の 1 発話にだけ当たる語）", budgetHistoryBrowse, func() error {
		found, err := a.SearchDialogueUtterances("S-0100 の 1000 番目の発話", "all", "all")
		if err != nil {
			return err
		}
		if found.Total != 1 {
			return fmt.Errorf("末尾の発話に到達していない: %d 件", found.Total)
		}
		if found.Hits[0].SessionID != "S-0100" || found.Hits[0].ID != "utt-01000" {
			return fmt.Errorf("該当が末尾の発話でない: %+v", found.Hits[0])
		}
		return nil
	})

	// 絞り込みと併用した検索。走査対象がフェーズで減ることを件数で確かめる。
	measure(t, "(3) 対話履歴の全文検索（フェーズで絞り込んだ検索）", budgetHistoryBrowse, func() error {
		found, err := a.SearchDialogueUtterances("在庫管理", "basic-design", "all")
		if err != nil {
			return err
		}
		// 生成規則（tools/scalegen）は 3 件ごとに基本設計。全件（10 万）より少なく、0 でもないこと。
		if found.Total == 0 || found.Total >= 100*1000 {
			return fmt.Errorf("絞り込みが効いた検索になっていない: %d 件", found.Total)
		}
		return nil
	})

	measure(t, "(3) 対話履歴: 発話 1,000 件のセッションの読み出し", budgetHistoryBrowse, func() error {
		list, err := a.DialogueUtterances("S-0100")
		if err != nil {
			return err
		}
		if len(list) != 1000 {
			return fmt.Errorf("発話が 1,000 件でない: %d 件", len(list))
		}
		return nil
	})
}

// generateScaleProject は tools/scalegen を実際にビルドして走らせ、
// 生成したプロジェクトのパスと、生成器が数え直した規模の実測値を返す。
func generateScaleProject(t *testing.T, appBase string) (string, []generatedScale) {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "scalegen")
	build := exec.Command("go", "build", "-o", bin, "github.com/howashoji/ReqWeave/app/tools/scalegen")
	build.Dir = repoAppDir(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("scalegen をビルドできない: %v\n%s", err, out)
	}

	// 併走プロジェクト（同時に扱うプロジェクトの上限）が兄弟として作られるため、専用の親を与える。
	parent := filepath.Join(t.TempDir(), "scale")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "在庫管理システム.rwv")

	started := time.Now()
	cmd := exec.Command(bin, "-root", root, "-app-base", appBase)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("上限規模のテストデータを生成できない: %v\n%s", err, out)
	}
	t.Logf("上限規模のテストデータを生成（%.1f 秒）:\n%s", time.Since(started).Seconds(), out)

	var scales []generatedScale
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 5 || f[0] != "scale" {
			continue
		}
		current, err1 := strconv.ParseInt(f[2], 10, 64)
		limit, err2 := strconv.ParseInt(f[3], 10, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("生成器の出力を読めない: %q", sc.Text())
		}
		scales = append(scales, generatedScale{kind: f[1], current: current, limit: limit, level: f[4]})
	}
	return root, scales
}

// assertAtLimit は想定する上限規模の全項目が上限に達していることを確かめる。
//
// **これが計測の空振り防止**である。1 項目でも上限未満なら「上限規模での実測」を名乗れない。
func assertAtLimit(t *testing.T, scales []generatedScale) {
	t.Helper()

	got := map[string]generatedScale{}
	for _, s := range scales {
		got[s.kind] = s
	}
	var lines []string
	for _, kind := range projectstore.ScaleKinds() {
		s, ok := got[string(kind)]
		if !ok {
			t.Fatalf("規模の実測値に %s（%s）が無い。上限規模になっていない状態で計測してはならない",
				kind, projectstore.ScaleLabelOf(kind))
		}
		if s.current < s.limit || s.level != projectstore.ScaleLevelExceeded {
			t.Fatalf("%s（%s）が上限に達していない: %d / %d（判定 %s）",
				kind, projectstore.ScaleLabelOf(kind), s.current, s.limit, s.level)
		}
		lines = append(lines, fmt.Sprintf("  %-16s %13d / %13d  %s",
			kind, s.current, s.limit, projectstore.ScaleLabelOf(kind)))
	}
	sort.Strings(lines)
	t.Logf("投入後の規模の実測値（全 %d 項目が上限到達）:\n%s",
		len(projectstore.ScaleKinds()), strings.Join(lines, "\n"))
}

// measure は step を scaleTrials 回実行し、全試行の実測値を記録して持ち分と突き合わせる。
//
// **最大値で判定する**（平均で判定すると、5 回のうち 1 回の基準超過を隠せる）。
func measure(t *testing.T, label string, budget time.Duration, step func() error) {
	t.Helper()

	samples := make([]time.Duration, 0, scaleTrials)
	for i := 0; i < scaleTrials; i++ {
		started := time.Now()
		if err := step(); err != nil {
			t.Fatalf("%s: %d 回目の計測が成立しない: %v", label, i+1, err)
		}
		samples = append(samples, time.Since(started))
	}

	var worst time.Duration
	texts := make([]string, 0, len(samples))
	for _, d := range samples {
		if d > worst {
			worst = d
		}
		texts = append(texts, fmt.Sprintf("%.0fms", float64(d.Microseconds())/1000))
	}
	t.Logf("%s: %s（最大 %.0fms / 持ち分 %.0fms）",
		label, strings.Join(texts, " "), float64(worst.Microseconds())/1000,
		float64(budget.Microseconds())/1000)
	if worst > budget {
		t.Errorf("%s が持ち分を超えた: 最大 %.0fms > %.0fms（画面応答の基準を満たさない）",
			label, float64(worst.Microseconds())/1000, float64(budget.Microseconds())/1000)
	}
}

// repoAppDir はリポジトリの app/ ディレクトリ（go build のモジュール解決に使う）。
func repoAppDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// テストの作業ディレクトリは app/internal/binding。
	return filepath.Dir(filepath.Dir(wd))
}
