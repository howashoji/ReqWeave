package docgen

// 本ファイルは進捗レポートの章7「開発AIフィードバック集計」を担う。
//
// レポート本体（章1〜6 と組立て）は report.go が担う。章7 はフィードバックの分類と
// 一体で意味を持つため、集計の受け取りと章の組立てをここに分けて置く。
//
// 集計そのものは取り込みモジュール（importer.SummarizeFeedback）が表示時に算出する。
// レポート側は保存も再集計もしない（実レコードとの二重管理をしない）。

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/importer"
)

// FeedbackChapterTitle は進捗レポート章7 の見出し。
const FeedbackChapterTitle = "開発AIフィードバック集計"

// FeedbackChapter は進捗レポートの章7 を Markdown で組み立てる。
//
// フィードバックが 0 件の期間は**章ごと省略する**ため空文字を返す。
// 形式は成果物の形式（見出しは 3 階層以内・表は Markdown）に従う。
func FeedbackChapter(number int, summary *importer.FeedbackSummary) string {
	if summary == nil || summary.IsEmpty() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. %s\n\n", number, FeedbackChapterTitle)
	fmt.Fprintf(&b, "期間内のフィードバック: %d 件\n\n", summary.Total)
	b.WriteString("| 分類 | 件数 |\n|---|---|\n")
	for _, c := range summary.Counts {
		fmt.Fprintf(&b, "| %s | %d |\n", c.Label, c.Count)
	}
	if summary.Unclassified > 0 {
		fmt.Fprintf(&b, "| 未分類 | %d |\n", summary.Unclassified)
	}
	return b.String()
}
