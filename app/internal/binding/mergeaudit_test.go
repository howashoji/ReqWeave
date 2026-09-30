package binding

// 三面マージの記録（変更履歴の `merge-applied`）の表示値。

import (
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/sync"
)

// 選んだ解決は日本語の表示名で記録する（生のコード値を残さない）。
func TestMergeChoiceLabel(t *testing.T) {
	for _, choice := range []sync.Choice{sync.ChoiceTheirs, sync.ChoiceOurs, sync.ChoiceBoth, sync.ChoiceOpenIssue} {
		got := mergeChoiceLabel(choice)
		if got == "" || got == string(choice) {
			t.Errorf("%q の表示名が無い: %q", choice, got)
		}
	}
	// 未知の値でもコード値を出さない（フォールバックで生の値を表示しない）。
	if got := mergeChoiceLabel(sync.Choice("take-latest")); got != "解決不明" {
		t.Errorf("未知の解決の表示が違う: %q", got)
	}
}
