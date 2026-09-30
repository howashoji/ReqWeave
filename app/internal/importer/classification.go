package importer

// 本ファイルは開発AIフィードバックの分類を担う（開発 AI からの問い合わせを分類し、
// 要件定義・基本設計で確認すべきだった論点がどれだけ残ったかを実測するため）。
//
// 値集合は本ファイルの Classifications が正本であり、アプリの版更新でのみ保守する
// （外部から値を追加する経路を設けない）。
// 付与・変更は**担当者の操作**に限る（AI は分類しない。実測値の恣意性を避ける）。
// 集計値は保存せず、表示のたびに import.yaml 群から算出する（実レコードとの二重管理禁止）。

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Classifications は分類の値集合（表示順の正本でもある）。
var Classifications = []Classification{
	ClassRequirementsGap, ClassDesignGap, ClassNewRequest, ClassClarification, ClassOther,
}

// ClassificationLabel は画面表示の日本語ラベル（内部コード値を画面に出さない）。
func ClassificationLabel(c Classification) string {
	switch c {
	case ClassRequirementsGap:
		return "要件定義時に確認すべきだった論点"
	case ClassDesignGap:
		return "基本設計で決めるべきだった論点"
	case ClassNewRequest:
		return "新規要望"
	case ClassClarification:
		return "成果物で回答可能な確認"
	case ClassOther:
		return "その他"
	default:
		return "未分類"
	}
}

// SetClassification は開発AIフィードバックの分類を付与・変更する。
//
// 更新するのは import.yaml の `classification` だけであり、原本 `source.<拡張子>` と
// 抽出テキスト `extracted.md` は書き換えない（取り込み後は不変）。
// 空文字を渡すと分類を外す（未分類へ戻す）。
func (im *Importer) SetClassification(id string, c Classification) (*Meta, error) {
	meta, err := im.Load(id)
	if err != nil {
		return nil, err
	}
	if meta.Kind != KindDevAIFeedback {
		return nil, fmt.Errorf("分類を付けられるのは開発AIフィードバックだけです（%s の種別は %s）", id, meta.Kind)
	}
	meta.Classification = c
	// 値集合の検証は書き込み前に行う（不正値をファイルへ残さない）。
	if err := meta.Validate(); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("取り込みのメタデータを組み立てられません（%s）: %w", id, err)
	}
	if err := im.store.WriteFile(MetaPath(id), data); err != nil {
		return nil, err
	}
	return im.Load(id)
}

// ClassificationCount は 1 分類の件数（表示時算出。保存しない）。
type ClassificationCount struct {
	Classification Classification `json:"classification"`
	Label          string         `json:"label"`
	Count          int            `json:"count"`
}

// FeedbackSummary は分類別・期間別の集計結果（進捗レポートの章にも使う）。
type FeedbackSummary struct {
	// From / To は集計期間（ゼロ値は無制限）。
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Total は期間内の開発AIフィードバック件数（未分類を含む）。
	Total int `json:"total"`
	// Counts は分類別の件数（Classifications の順。0 件の分類も含む）。
	Counts []ClassificationCount `json:"counts"`
	// Unclassified は分類が未付与の件数。
	Unclassified int `json:"unclassified"`
}

// IsEmpty は期間内のフィードバックが 0 件かを返す（進捗レポートの章省略に使う）。
func (s FeedbackSummary) IsEmpty() bool { return s.Total == 0 }

// SummarizeFeedback は分類別・期間別の件数を算出する（表示時算出）。
//
// 期間は取り込み日時（`imported_at`）で絞り込む。from / to のゼロ値は無制限、
// 範囲は from 以上 to 以下（両端を含む）とする。
// 集計値はどこにも保存しない（実レコードとの二重管理禁止）。
func (im *Importer) SummarizeFeedback(from, to time.Time) (*FeedbackSummary, error) {
	metas, err := im.List()
	if err != nil {
		return nil, err
	}
	out := &FeedbackSummary{From: from, To: to}
	byClass := map[Classification]int{}
	for _, m := range metas {
		if m.Kind != KindDevAIFeedback || !inPeriod(m.ImportedAt, from, to) {
			continue
		}
		out.Total++
		if m.Classification == "" {
			out.Unclassified++
			continue
		}
		byClass[m.Classification]++
	}
	for _, c := range Classifications {
		out.Counts = append(out.Counts, ClassificationCount{
			Classification: c, Label: ClassificationLabel(c), Count: byClass[c]})
	}
	return out, nil
}

// inPeriod は取り込み日時が期間内かを返す（ゼロ値は無制限）。
func inPeriod(at, from, to time.Time) bool {
	if !from.IsZero() && at.Before(from) {
		return false
	}
	if !to.IsZero() && at.After(to) {
		return false
	}
	return true
}
