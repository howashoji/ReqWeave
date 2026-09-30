package binding

// 本ファイルは開発AIフィードバックの分類と集計の公開バインディング。
//
// 分類の付与・変更は**担当者の操作**でのみ行う（AI は分類しない）。
// 集計は表示のたびに取り込みモジュールが算出し、集計値を保存しない（保存した集計と実データが食い違わないように）。
// 取り込み画面のその他の操作は imports_screen.go が持つ。

import (
	"fmt"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// FeedbackClassificationOption は分類の選択肢（画面のラベルとコード値の対応）。
type FeedbackClassificationOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FeedbackClassificationOptions は分類の選択肢を定義順で返す（値集合の正本は取り込みモジュール）。
//
// 画面は本一覧からラベルを引く（生のコード値を表示しない）。
func (a *API) FeedbackClassificationOptions() []FeedbackClassificationOption {
	out := make([]FeedbackClassificationOption, 0, len(importer.Classifications))
	for _, c := range importer.Classifications {
		out = append(out, FeedbackClassificationOption{
			Value: string(c), Label: importer.ClassificationLabel(c)})
	}
	return out
}

// SetFeedbackClassification は分類を付与・変更する（担当者の操作）。
//
// 空文字は未分類へ戻す。値集合の検証は取り込みモジュールが書き込み前に行う。
func (a *API) SetFeedbackClassification(importID, classification string) (*importer.Meta, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "フィードバックの分類"); err != nil {
		return nil, err
	}
	im := importer.New(s.store)
	before, err := im.Load(importID)
	if err != nil {
		return nil, err
	}
	updated, err := im.SetClassification(importID, importer.Classification(strings.TrimSpace(classification)))
	if err != nil {
		return nil, err
	}
	a.recordChange(s, auditlog.ChangeRecord{Target: importID, Change: auditlog.ChangeUpdated,
		Before: "classification: " + string(before.Classification),
		After:  "classification: " + string(updated.Classification)})
	return updated, nil
}

// FeedbackSummary は分類別・期間別の件数を返す（表示時算出）。
//
// from / to は空文字で無制限。日付（YYYY-MM-DD）で受け取り、to はその日の終わりまでを含む。
// 日付は表示タイムゾーン（ローカル）の暦日として解釈する（進捗レポートと同一の解釈）。
func (a *API) FeedbackSummary(from, to string) (*importer.FeedbackSummary, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	fromAt, err := parsePeriodBound(from, false)
	if err != nil {
		return nil, err
	}
	toAt, err := parsePeriodBound(to, true)
	if err != nil {
		return nil, err
	}
	return importer.New(s.store).SummarizeFeedback(fromAt, toAt)
}

// parsePeriodBound は期間指定（YYYY-MM-DD）を UTC の時刻へ直す。
// endOfDay が true のときはその日の 23:59:59 まで含める（期間の両端を含む集計）。
//
// 日付は**表示タイムゾーン（ローカル）の暦日**として解釈する。保存値は UTC だが、
// 画面で指定するのはローカルの暦日であり、UTC 暦日で解釈すると JST では
// 9 時間ずれた期間になる。進捗レポート（ProgressReport）と同一の解釈にそろえてある。
func parsePeriodBound(value string, endOfDay bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("期間は YYYY-MM-DD の形式で指定してください: %q", value)
	}
	if endOfDay {
		at = at.Add(24*time.Hour - time.Second)
	}
	return at.UTC(), nil
}
