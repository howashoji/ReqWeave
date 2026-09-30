package docgen

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/importer"
)

func summary(total, unclassified int, counts map[importer.Classification]int) *importer.FeedbackSummary {
	out := &importer.FeedbackSummary{Total: total, Unclassified: unclassified}
	for _, c := range importer.Classifications {
		out.Counts = append(out.Counts, importer.ClassificationCount{
			Classification: c, Label: importer.ClassificationLabel(c), Count: counts[c]})
	}
	return out
}

// 章7: 分類別の件数が表で載る（集計は取り込みモジュールの算出値をそのまま使う）。
func TestFeedbackChapter(t *testing.T) {
	got := FeedbackChapter(7, summary(3, 1, map[importer.Classification]int{
		importer.ClassRequirementsGap: 2}))

	if !strings.HasPrefix(got, "## 7. "+FeedbackChapterTitle) {
		t.Fatalf("章見出しが違う:\n%s", got)
	}
	for _, want := range []string{"期間内のフィードバック: 3 件",
		"| " + importer.ClassificationLabel(importer.ClassRequirementsGap) + " | 2 |",
		"| 未分類 | 1 |"} {
		if !strings.Contains(got, want) {
			t.Errorf("章に %q が無い:\n%s", want, got)
		}
	}
	// コード値を画面・レポートへ出さない（ラベルで表示する）。
	if strings.Contains(got, string(importer.ClassRequirementsGap)) {
		t.Errorf("分類のコード値がレポートに出ている:\n%s", got)
	}
	// 0 件の分類も表に残す（分母が分かるようにする）。
	if !strings.Contains(got, "| "+importer.ClassificationLabel(importer.ClassOther)+" | 0 |") {
		t.Errorf("0 件の分類が表から落ちている:\n%s", got)
	}
}

// フィードバック 0 件の期間は章ごと省略する。
func TestFeedbackChapterOmittedWhenEmpty(t *testing.T) {
	if got := FeedbackChapter(7, summary(0, 0, nil)); got != "" {
		t.Errorf("0 件の期間で章が出力された:\n%s", got)
	}
	if got := FeedbackChapter(7, nil); got != "" {
		t.Errorf("集計が無いときに章が出力された:\n%s", got)
	}
}

// 未分類が 0 件のときは未分類の行を出さない（不要な行で表を膨らませない）。
func TestFeedbackChapterOmitsUnclassifiedRow(t *testing.T) {
	got := FeedbackChapter(7, summary(2, 0, map[importer.Classification]int{
		importer.ClassNewRequest: 2}))
	if strings.Contains(got, "未分類") {
		t.Errorf("未分類 0 件で行が出ている:\n%s", got)
	}
}
