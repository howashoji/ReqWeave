package dialogue

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// testBudget は分割が起きる大きさの予算（コンテキスト長 5,000 トークンのモデル相当）。
func testBudget() ImportChunkBudget {
	return ImportChunkBudget{ContextWindow: 5000, FixedTokens: 200, MaxOutputTokens: 1500}
}

// sampleDocument は見出しと段落を持つ抽出テキスト（sections 節 × paras 段落）。
//
// 1 段落は複数行で構成する（段落の途中に行境界がある形。行境界で割っただけでは
// 段落境界にならないため、分割規則の検証が空振りしない）。
const paragraphLines = 4

func sampleDocument(sections, paras, chars int) string {
	var b strings.Builder
	for s := 1; s <= sections; s++ {
		fmt.Fprintf(&b, "## 第%d章 在庫管理業務\n\n", s)
		for p := 1; p <= paras; p++ {
			for l := 0; l < paragraphLines; l++ {
				fmt.Fprintf(&b, "%s\n", strings.Repeat("在庫の引当と欠品対応の運用。", chars))
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// 全チャンクを連結すると元テキストに戻る（改行以外の文字を落とさない）。
func assertNoTextLoss(t *testing.T, plan *ImportChunkPlan, original string) {
	t.Helper()
	var joined strings.Builder
	for _, c := range plan.Chunks {
		joined.WriteString(c.Text)
	}
	strip := func(s string) string { return strings.ReplaceAll(s, "\n", "") }
	if strip(joined.String()) != strip(original) {
		t.Errorf("分割でテキストが欠落・重複した（元 %d 文字 / 分割後 %d 文字）",
			len([]rune(strip(original))), len([]rune(strip(joined.String()))))
	}
}

// コンテキスト長に収まる資料は分割しない。
func TestPlanImportChunksNoSplitWhenFits(t *testing.T) {
	text := sampleDocument(1, 1, 3)
	plan, err := PlanImportChunks(text, testBudget(), 0)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	if plan.ChunkCount() != 1 {
		t.Fatalf("収まる資料が分割された: %d 件", plan.ChunkCount())
	}
	if plan.Chunks[0].Text != text {
		t.Error("分割なしのチャンクが原文と一致しない")
	}
	if plan.Chunks[0].StartLine != 1 || plan.Chunks[0].EndLine != countLines(text) {
		t.Errorf("行範囲が全文になっていない: L%d-L%d（全 %d 行）",
			plan.Chunks[0].StartLine, plan.Chunks[0].EndLine, countLines(text))
	}
}

// 予算超過時に分割し、境界は見出し・段落の先頭に落ちる。
func TestPlanImportChunksSplitsAtBlockBoundaries(t *testing.T) {
	text := sampleDocument(6, 2, 6)
	budget := testBudget()
	if aiprovider.EstimateTokens(text) <= budget.Available() {
		t.Fatalf("前提が崩れている: 予算内に収まる資料では分割を検証できない（%d ≤ %d）",
			aiprovider.EstimateTokens(text), budget.Available())
	}

	plan, err := PlanImportChunks(text, budget, 20)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	if plan.ChunkCount() < 2 {
		t.Fatalf("予算を超える資料が分割されていない: %d 件", plan.ChunkCount())
	}
	lines := strings.Split(text, "\n")
	for _, c := range plan.Chunks {
		if got := aiprovider.EstimateTokens(c.Text); got > budget.Available() {
			t.Errorf("チャンク %d が予算超過: %d > %d", c.Index, got, budget.Available())
		}
		if c.Index == 1 {
			continue
		}
		// 2 件目以降の先頭行は「見出し行」か「空行の直後」であること
		// （段落の途中で切らない）。
		first := lines[c.StartLine-1]
		prev := lines[c.StartLine-2]
		if !isHeading(first) && strings.TrimSpace(prev) != "" {
			t.Errorf("チャンク %d が段落の途中で切れている: 前行 %.20q / 先頭行 %.20q",
				c.Index, prev, first)
		}
	}
	assertNoTextLoss(t, plan, text)
}

// 行番号は 1 始まりで連続し、全文を覆う（根拠参照 IMP-nnn#Lm-Ln の材料）。
func TestPlanImportChunksLineRangesAreContiguous(t *testing.T) {
	text := sampleDocument(6, 2, 6)
	plan, err := PlanImportChunks(text, testBudget(), 20)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	want := 1
	for _, c := range plan.Chunks {
		if c.StartLine != want {
			t.Errorf("チャンク %d の開始行が不連続: L%d（期待 L%d）", c.Index, c.StartLine, want)
		}
		if c.EndLine < c.StartLine {
			t.Errorf("チャンク %d の行範囲が逆転: L%d-L%d", c.Index, c.StartLine, c.EndLine)
		}
		want = c.EndLine + 1
	}
	if last := plan.Chunks[len(plan.Chunks)-1].EndLine; last != countLines(text) {
		t.Errorf("最終行まで覆っていない: L%d（全 %d 行）", last, countLines(text))
	}
}

// チャンク番号は 1 始まりの連番（送信前プレビューの n/N 表示）。
func TestPlanImportChunksIndexesAndEstimate(t *testing.T) {
	text := sampleDocument(6, 2, 6)
	budget := testBudget()
	plan, err := PlanImportChunks(text, budget, 20)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	for i, c := range plan.Chunks {
		if c.Index != i+1 {
			t.Errorf("チャンク番号が連番でない: %d（期待 %d）", c.Index, i+1)
		}
	}
	// 概算は本文合計に「チャンク数ぶんの注入文脈」を加えた値（同一文脈を毎回送るため）。
	var bodies int
	for _, c := range plan.Chunks {
		bodies += aiprovider.EstimateTokens(c.Text)
	}
	if want := bodies + budget.FixedTokens*plan.ChunkCount(); plan.EstimatedTokens != want {
		t.Errorf("概算トークンが違う: %d（期待 %d）", plan.EstimatedTokens, want)
	}
}

// 分割数が上限を超える資料は分析を開始しない。
func TestPlanImportChunksRejectsOverLimit(t *testing.T) {
	text := sampleDocument(40, 3, 10)
	_, err := PlanImportChunks(text, testBudget(), DefaultMaxImportChunks)
	if err == nil {
		t.Fatal("上限を超える資料が受理された")
	}
	if !errors.Is(err, ErrImportTooLarge) {
		t.Fatalf("上限超過として区分されていない: %v", err)
	}
	if !strings.Contains(err.Error(), "範囲") {
		t.Errorf("範囲の絞り込みを案内していない: %v", err)
	}
}

// 上限は既定 10（調整してよい範囲は 5〜20）。
func TestDefaultMaxImportChunksInAllowedRange(t *testing.T) {
	if DefaultMaxImportChunks < 5 || DefaultMaxImportChunks > 20 {
		t.Errorf("分割数の上限が許容範囲外: %d", DefaultMaxImportChunks)
	}
}

// コンテキスト長が不明（0）なら超過を判定できないため分割しない。
func TestPlanImportChunksUnknownContextWindow(t *testing.T) {
	text := sampleDocument(6, 2, 6)
	plan, err := PlanImportChunks(text, ImportChunkBudget{MaxOutputTokens: 1500}, 0)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	if plan.ChunkCount() != 1 {
		t.Errorf("コンテキスト長不明で分割された: %d 件", plan.ChunkCount())
	}
}

func TestPlanImportChunksRejectsEmptyText(t *testing.T) {
	if _, err := PlanImportChunks("   \n\n", testBudget(), 0); err == nil {
		t.Error("空の抽出テキストが受理された")
	}
}

// 安全余裕は最大出力トークン以上を確保する。
func TestImportChunkBudgetKeepsSafetyMargin(t *testing.T) {
	b := ImportChunkBudget{ContextWindow: 100000, FixedTokens: 1000, MaxOutputTokens: 8000}
	got := b.Available()
	// 本文＋固定分＋最大出力＋安全余裕がコンテキスト長を超えないこと。
	if got+b.FixedTokens+b.MaxOutputTokens+b.MaxOutputTokens > b.ContextWindow {
		t.Errorf("安全余裕が最大出力トークンぶん確保されていない: %d", got)
	}
	if got != 100000-1000-8000-8000 {
		t.Errorf("割り当てが違う: %d", got)
	}
	// 最大出力が不明でも余裕を 0 にしない。
	unknown := ImportChunkBudget{ContextWindow: 10000}
	if unknown.Available() != 10000-minSafetyTokens {
		t.Errorf("最大出力不明時の安全余裕が確保されていない: %d", unknown.Available())
	}
	// 固定分だけでコンテキスト長を埋める場合は判定不能（0）。
	full := ImportChunkBudget{ContextWindow: 1000, FixedTokens: 5000, MaxOutputTokens: 100}
	if full.Available() != 0 {
		t.Errorf("固定分超過で 0 にならない: %d", full.Available())
	}
}

// 1 段落・1 行が単独で予算を超えても、テキストを落とさずに割る。
func TestPlanImportChunksSplitsOversizedBlockWithoutLoss(t *testing.T) {
	long := strings.Repeat("在庫の引当と欠品対応の運用を定義する。", 400) // 改行のない 1 行
	text := "## 第1章\n\n" + long
	plan, err := PlanImportChunks(text, testBudget(), 20)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	if plan.ChunkCount() < 2 {
		t.Fatalf("巨大な 1 行が分割されていない: %d 件", plan.ChunkCount())
	}
	for _, c := range plan.Chunks {
		if got := aiprovider.EstimateTokens(c.Text); got > testBudget().Available() {
			t.Errorf("チャンク %d が予算超過: %d", c.Index, got)
		}
	}
	assertNoTextLoss(t, plan, text)
}

// 見出し行の判定（Markdown の見出しのみ。ハッシュ記号で始まる本文を見出し扱いしない）。
func TestIsHeading(t *testing.T) {
	cases := map[string]bool{
		"# 章":         true,
		"###### 小見出し": true,
		"  ## 字下げ見出し": true,
		"####### 7つ":  false,
		"#見出しではない":    false,
		"本文 # 記号":     false,
		"":            false,
	}
	for line, want := range cases {
		if got := isHeading(line); got != want {
			t.Errorf("isHeading(%q) = %v, want %v", line, got, want)
		}
	}
}
