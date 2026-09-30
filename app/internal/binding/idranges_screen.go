package binding

// 本ファイルは ID の番号帯の残量（共同プロジェクトで作業者ごとに確保した採番の区間）。
//
// 画面は「残りが少ない」「使い切った」種別を warning として受け取り、同期（取り込み・反映）へ誘導する。
// 内部の種別キー・区間の数値をそのまま出さず、日本語の種別名と残件数で示す。
// 番号帯を使わないプロジェクト（同期先未設定 = 単独利用）では空を返す。

import (
	"fmt"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// idRangeKindLabels は番号帯の対象種別の日本語表示（番号帯を持つ種別はこれで全部）。
var idRangeKindLabels = map[string]string{
	projectstore.RangeSession:       "対話セッション",
	projectstore.RangeQuestionnaire: "質問票",
	projectstore.RangeImport:        "取り込み資料",
	projectstore.RangeStakeholder:   "ステークホルダー",
	projectstore.RangePerspective:   "ヒアリング観点",
	projectstore.RangeRequirement:   "要件項目",
	projectstore.RangeDecision:      "決定事項",
	projectstore.RangeOpenIssue:     "未決事項",
}

func idRangeKindLabel(key string) string {
	if label, ok := idRangeKindLabels[key]; ok {
		return label
	}
	return "その他の種別"
}

// IDRangeWarningView は番号帯の残りが少ない種別 1 件。
type IDRangeWarningView struct {
	// KindLabel は対象種別の表示名（内部キーを画面へ出さない）。
	KindLabel string `json:"kindLabel"`
	// Remaining は残りの採番可能な件数。
	Remaining int `json:"remaining"`
	// Exhausted は使い切ったか（新規の作成ができない）。
	Exhausted bool `json:"exhausted"`
	// Message は原因＋次の行動の 1 文。
	Message string `json:"message"`
}

// IDRangeWarnings は番号帯の残りが少ない・使い切った種別を返す。
//
// 番号帯を使わないプロジェクトでは空。残りが十分な種別は含めない（警告だけを画面へ渡す）。
func (a *API) IDRangeWarnings() ([]IDRangeWarningView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	statuses, err := s.store.IDRangeStatuses(0)
	if err != nil {
		return nil, err
	}
	out := make([]IDRangeWarningView, 0, len(statuses))
	for _, st := range statuses {
		if !st.Warn {
			continue
		}
		label := idRangeKindLabel(st.Key)
		view := IDRangeWarningView{KindLabel: label, Remaining: st.Remaining, Exhausted: st.Exhausted}
		if st.Exhausted {
			view.Message = fmt.Sprintf(
				"%sの番号を使い切ったため、新しく作成できません。同期（取り込み・反映）を実行してください。", label)
		} else {
			view.Message = fmt.Sprintf(
				"%sの番号の残りが %d 件です。同期（取り込み・反映）を実行すると新しい番号がまとめて確保されます。",
				label, st.Remaining)
		}
		out = append(out, view)
	}
	return out, nil
}
