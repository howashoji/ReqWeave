//go:build integration

// 結合テスト（フィードバックの分類・集計バインディング × 実ファイル）。

package binding

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/importer"
)

func importFeedback(t *testing.T, a *API, body string) *importer.Meta {
	t.Helper()
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := importer.New(s.store).Import(importer.Input{
		Kind: importer.KindDevAIFeedback, SourceName: "feedback.md", SourceFormat: importer.FormatMD,
		Content: []byte(body), ExtractionStatus: importer.StatusExtracted, ExtractedText: body,
	})
	if err != nil {
		t.Fatalf("フィードバックを取り込めない: %v", err)
	}
	return meta
}

// 担当者の操作で分類を付与・変更でき、集計へ反映される。
func TestSetFeedbackClassificationAndSummary(t *testing.T) {
	a, _, _ := openWithRecords(t)
	first := importFeedback(t, a, "# フィードバック 1\n\n引当の確認。\n")
	second := importFeedback(t, a, "# フィードバック 2\n\n出荷の確認。\n")

	// 選択肢はコード値とラベルの対応で返る（画面に生のコード値を出さない）。
	options := a.FeedbackClassificationOptions()
	if len(options) != len(importer.Classifications) {
		t.Fatalf("分類の選択肢が違う: %+v", options)
	}
	for _, o := range options {
		if o.Value == "" || o.Label == "" || o.Label == o.Value {
			t.Errorf("選択肢の表示ラベルが無い: %+v", o)
		}
	}

	if _, err := a.SetFeedbackClassification(first.ID, string(importer.ClassRequirementsGap)); err != nil {
		t.Fatalf("分類を付与できない: %v", err)
	}
	if _, err := a.SetFeedbackClassification(second.ID, string(importer.ClassNewRequest)); err != nil {
		t.Fatalf("分類を付与できない: %v", err)
	}

	got, err := a.FeedbackSummary("", "")
	if err != nil {
		t.Fatalf("集計に失敗: %v", err)
	}
	if got.Total != 2 || got.Unclassified != 0 {
		t.Fatalf("集計が違う: %+v", got)
	}
	counts := map[importer.Classification]int{}
	for _, c := range got.Counts {
		counts[c.Classification] = c.Count
	}
	if counts[importer.ClassRequirementsGap] != 1 || counts[importer.ClassNewRequest] != 1 {
		t.Errorf("分類別の件数が違う: %+v", got.Counts)
	}

	// 変更が集計へ即座に反映される（保存された集計値を読まない = 表示時算出）。
	if _, err := a.SetFeedbackClassification(second.ID, string(importer.ClassRequirementsGap)); err != nil {
		t.Fatalf("分類を変更できない: %v", err)
	}
	got, err = a.FeedbackSummary("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got.Counts {
		if c.Classification == importer.ClassRequirementsGap && c.Count != 2 {
			t.Errorf("変更が集計に反映されていない: %+v", got.Counts)
		}
	}

	// 値集合の外は拒否する。
	if _, err := a.SetFeedbackClassification(first.ID, "unknown"); err == nil {
		t.Error("値集合の外の分類が受理された")
	}
	// 分類の変更は変更履歴に残る（要件項目などの変更と同じ経路）。
	history, err := a.ChangeHistory()
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var found bool
	for _, h := range history {
		if h.Target == first.ID && strings.Contains(h.After, string(importer.ClassRequirementsGap)) {
			found = true
		}
	}
	if !found {
		t.Errorf("分類の付与が変更履歴に残っていない: %+v", history)
	}
}

// 期間指定は YYYY-MM-DD で受け取り、両端を含む（形式違いは日本語で拒否）。
func TestFeedbackSummaryPeriod(t *testing.T) {
	a, _, _ := openWithRecords(t)
	meta := importFeedback(t, a, "# フィードバック\n\n確認。\n")
	if _, err := a.SetFeedbackClassification(meta.ID, string(importer.ClassOther)); err != nil {
		t.Fatal(err)
	}
	// 期間指定はローカル暦日で解釈する（UTC の日付で切ると日本では 9 時間ずれる）。
	day := meta.ImportedAt.Local().Format("2006-01-02")

	inside, err := a.FeedbackSummary(day, day)
	if err != nil {
		t.Fatalf("当日の集計に失敗: %v", err)
	}
	if inside.Total != 1 {
		t.Errorf("当日の集計に含まれない（期間の両端を含むはず）: %+v", inside)
	}
	outside, err := a.FeedbackSummary("2000-01-01", "2000-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if !outside.IsEmpty() {
		t.Errorf("期間外が集計された: %+v", outside)
	}
	if _, err := a.FeedbackSummary("2026/08/31", ""); err == nil {
		t.Error("形式違いの期間が受理された")
	}
}
