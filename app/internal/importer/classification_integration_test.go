//go:build integration

// 結合テスト（フィードバック分類・集計 × 実ファイル）。

package importer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newFeedbackImport(t *testing.T, im *Importer, body string) *Meta {
	t.Helper()
	fromTemplate := true
	meta, err := im.Import(Input{
		Kind: KindDevAIFeedback, SourceName: "feedback.md", SourceFormat: FormatMD,
		Content: []byte(body), ExtractionStatus: StatusExtracted, ExtractedText: body,
		FromTemplate: &fromTemplate,
	})
	if err != nil {
		t.Fatalf("フィードバックを取り込めない: %v", err)
	}
	return meta
}

// 分類を付与・変更でき、値集合の外は拒否される。原本は変わらない。
func TestSetClassification(t *testing.T) {
	im, s := newTestImporter(t)
	const body = "# フィードバック\n\n引当のタイミングを確認したいです。\n"
	meta := newFeedbackImport(t, im, body)
	if meta.Classification != "" {
		t.Fatalf("取り込み直後に分類が付いている: %q", meta.Classification)
	}

	got, err := im.SetClassification(meta.ID, ClassRequirementsGap)
	if err != nil {
		t.Fatalf("分類を付与できない: %v", err)
	}
	if got.Classification != ClassRequirementsGap {
		t.Errorf("分類が付いていない: %q", got.Classification)
	}
	// 実ファイルから読み直しても残っている。
	reloaded, err := im.Load(meta.ID)
	if err != nil || reloaded.Classification != ClassRequirementsGap {
		t.Fatalf("分類が保存されていない: %+v %v", reloaded, err)
	}

	// 変更できる。
	if _, err := im.SetClassification(meta.ID, ClassNewRequest); err != nil {
		t.Fatalf("分類を変更できない: %v", err)
	}
	// 空文字で未分類へ戻せる。
	cleared, err := im.SetClassification(meta.ID, "")
	if err != nil || cleared.Classification != "" {
		t.Fatalf("未分類へ戻せない: %+v %v", cleared, err)
	}

	// 値集合の外は書き込み前に拒否する。
	if _, err := im.SetClassification(meta.ID, "unknown-class"); err == nil {
		t.Error("値集合の外の分類が受理された")
	}
	after, err := im.Load(meta.ID)
	if err != nil || after.Classification != "" {
		t.Errorf("拒否したのに分類が書かれている: %+v %v", after, err)
	}

	// 原本・抽出テキストは変わらない（取り込み後は不変）。
	source, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(SourcePath(meta.ID, FormatMD))))
	if err != nil || !bytes.Equal(source, []byte(body)) {
		t.Errorf("原本が変わっている: %v", err)
	}
	extracted, err := im.ReadExtracted(meta.ID)
	if err != nil || extracted != body {
		t.Errorf("抽出テキストが変わっている: %v", err)
	}
}

// 分類を付けられるのは開発AIフィードバックだけ（種別の取り違えを拒否する）。
func TestSetClassificationRejectsOtherKinds(t *testing.T) {
	im, _ := newTestImporter(t)
	meta, err := im.Import(Input{Kind: KindMaterial, SourceName: "資料.md", SourceFormat: FormatMD,
		Content: []byte("本文"), ExtractionStatus: StatusExtracted, ExtractedText: "本文"})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	if _, err := im.SetClassification(meta.ID, ClassOther); err == nil {
		t.Fatal("資料に分類が付けられた")
	}
}

// 分類別・期間別の件数を表示時に算出し、集計値を保存しない。
func TestSummarizeFeedback(t *testing.T) {
	im, s := newTestImporter(t)
	a := newFeedbackImport(t, im, "A")
	b := newFeedbackImport(t, im, "B")
	c := newFeedbackImport(t, im, "C")
	// 資料・議事録は集計対象外。
	if _, err := im.Import(Input{Kind: KindMaterial, SourceName: "資料.md", SourceFormat: FormatMD,
		Content: []byte("本文"), ExtractionStatus: StatusExtracted, ExtractedText: "本文"}); err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	if _, err := im.SetClassification(a.ID, ClassRequirementsGap); err != nil {
		t.Fatal(err)
	}
	if _, err := im.SetClassification(b.ID, ClassRequirementsGap); err != nil {
		t.Fatal(err)
	}
	if _, err := im.SetClassification(c.ID, ClassNewRequest); err != nil {
		t.Fatal(err)
	}

	got, err := im.SummarizeFeedback(time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("集計に失敗: %v", err)
	}
	if got.Total != 3 {
		t.Fatalf("フィードバック件数が違う（資料が混ざっていないか）: %d", got.Total)
	}
	if len(got.Counts) != len(Classifications) {
		t.Fatalf("分類別の行数が違う（0 件の分類も含めるはず）: %d", len(got.Counts))
	}
	counts := map[Classification]int{}
	for _, row := range got.Counts {
		counts[row.Classification] = row.Count
	}
	if counts[ClassRequirementsGap] != 2 || counts[ClassNewRequest] != 1 || counts[ClassOther] != 0 {
		t.Errorf("分類別の件数が違う: %+v", got.Counts)
	}
	if got.Unclassified != 0 {
		t.Errorf("未分類の件数が違う: %d", got.Unclassified)
	}

	// 未分類は分類別の件数に混ぜず、独立して数える。
	if _, err := im.SetClassification(c.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err = im.SummarizeFeedback(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Unclassified != 1 || got.Total != 3 {
		t.Errorf("未分類の扱いが違う: %+v", got)
	}

	// 集計値をファイルへ保存しない（プロジェクト内に集計ファイルが増えない）。
	var extra []string
	err = filepath.Walk(s.Root(), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		name := strings.ToLower(filepath.Base(p))
		if strings.Contains(name, "summary") || strings.Contains(name, "count") ||
			strings.Contains(name, "stats") {
			extra = append(extra, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("走査に失敗: %v", err)
	}
	if len(extra) != 0 {
		t.Errorf("集計値が保存されている: %v", extra)
	}
}

// 期間で絞り込める（取り込み日時が基準。両端を含む）。
func TestSummarizeFeedbackByPeriod(t *testing.T) {
	im, _ := newTestImporter(t)
	meta := newFeedbackImport(t, im, "A")
	if _, err := im.SetClassification(meta.ID, ClassDesignGap); err != nil {
		t.Fatal(err)
	}
	at := meta.ImportedAt

	inside, err := im.SummarizeFeedback(at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if inside.Total != 1 {
		t.Errorf("期間内が集計されない: %+v", inside)
	}
	after, err := im.SummarizeFeedback(at.Add(time.Hour), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !after.IsEmpty() {
		t.Errorf("期間外が集計された: %+v", after)
	}
	// 期間外でも分類別の行は返す（表の形は保つ）。
	if len(after.Counts) != len(Classifications) {
		t.Errorf("期間外で分類別の行が欠けている: %+v", after.Counts)
	}
}
