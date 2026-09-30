package projectstore

import (
	"fmt"
	"testing"
)

// 8 種すべてに上限目安とラベルがあること。
// 表を分けたときの「1 種だけ抜ける」を捕まえる（上限目安の表の 8 種が唯一の入力）。
func TestScaleKindsCoverAllLimits(t *testing.T) {
	kinds := ScaleKinds()
	if len(kinds) != 8 {
		t.Fatalf("上限の対象が 8 種ではありません: %d 種", len(kinds))
	}
	want := map[ScaleKind]int64{
		ScaleSessions: 100, ScaleUtterances: 1000, ScaleRequirements: 1000,
		ScaleDecisions: 500, ScaleOpenIssues: 500, ScaleQuestionnaires: 100,
		ScaleTotalBytes: 500 * 1024 * 1024, ScaleProjects: 50,
	}
	if len(want) != len(kinds) {
		t.Fatalf("期待表と対象表の件数が違う: 期待 %d / 対象 %d", len(want), len(kinds))
	}
	for _, k := range kinds {
		if got := ScaleLimitOf(k); got != want[k] {
			t.Errorf("%s の上限目安が想定の値と違う: %d（期待 %d）", k, got, want[k])
		}
		if ScaleLabelOf(k) == "" {
			t.Errorf("%s の利用者向け表記が無い", k)
		}
	}
}

// 100% 到達で警告、80%（既定）到達で予告。境界値で固定する。
//
// 期待値は上限目安と既定閾値 80% から**手で導いた**値を書く
// （実行結果を写さない）。上限が 100 で割り切れない種別があるため、
// 割合ではなく件数そのものを並べる。
func TestScaleLevelBoundaries(t *testing.T) {
	// 各行: 予告閾値の直前 / 予告閾値ちょうど / 上限の直前 / 上限ちょうど / 上限超過
	table := map[ScaleKind][5]int64{
		ScaleSessions:       {79, 80, 99, 100, 101},
		ScaleUtterances:     {799, 800, 999, 1000, 1001},
		ScaleRequirements:   {799, 800, 999, 1000, 1001},
		ScaleDecisions:      {399, 400, 499, 500, 501},
		ScaleOpenIssues:     {399, 400, 499, 500, 501},
		ScaleQuestionnaires: {79, 80, 99, 100, 101},
		ScaleTotalBytes:     {419430399, 419430400, 524287999, 524288000, 524288001},
		ScaleProjects:       {39, 40, 49, 50, 51},
	}
	wantLevels := [5]string{
		ScaleLevelNone, ScaleLevelWarn, ScaleLevelWarn, ScaleLevelExceeded, ScaleLevelExceeded,
	}
	labels := [5]string{"予告閾値の直前", "予告閾値ちょうど", "上限の直前", "上限ちょうど", "上限超過"}

	kinds := ScaleKinds()
	if len(table) != len(kinds) {
		t.Fatalf("境界値表が 8 種を網羅していない: %d 種", len(table))
	}
	for _, k := range kinds {
		row, ok := table[k]
		if !ok {
			t.Fatalf("%s の境界値が表に無い", k)
		}
		for i, current := range row {
			t.Run(fmt.Sprintf("%s/%s", k, labels[i]), func(t *testing.T) {
				got := EvaluateScale(k, current, "", DefaultScaleWarnRatio)
				if got.Level != wantLevels[i] {
					t.Errorf("現在値 %d / 上限 %d の判定が %q（期待 %q）",
						current, ScaleLimitOf(k), got.Level, wantLevels[i])
				}
				if got.Current != current || got.Limit != ScaleLimitOf(k) {
					t.Errorf("現在値・上限が返り値に載っていない: %+v", got)
				}
				if got.Label == "" {
					t.Errorf("対象項目の表記が返り値に載っていない: %+v", got)
				}
			})
		}
	}
}

// 予告閾値は 70〜90% の範囲で設定でき、範囲外・未設定は既定 80% へ丸める。
func TestScaleWarnRatioRange(t *testing.T) {
	for _, in := range []float64{0.70, 0.75, 0.80, 0.85, 0.90} {
		if got := ScaleWarnRatioOrDefault(in); got != in {
			t.Errorf("許容範囲内の閾値 %v が %v へ変えられた", in, got)
		}
	}
	for _, in := range []float64{0, 0.5, 0.6999, 0.9001, 1.0, -1} {
		if got := ScaleWarnRatioOrDefault(in); got != DefaultScaleWarnRatio {
			t.Errorf("許容範囲外の閾値 %v が既定へ丸められていない: %v", in, got)
		}
	}
	// 閾値を変えると予告の開始点が動くこと（設定が効いていることの確認）。
	limit := ScaleLimitOf(ScaleSessions) // 100
	if got := EvaluateScale(ScaleSessions, 70, "", 0.70); got.Level != ScaleLevelWarn {
		t.Errorf("閾値 70%% で 70/%d が予告にならない: %q", limit, got.Level)
	}
	if got := EvaluateScale(ScaleSessions, 70, "", 0.90); got.Level != ScaleLevelNone {
		t.Errorf("閾値 90%% で 70/%d が予告になっている: %q", limit, got.Level)
	}
	if got := EvaluateScale(ScaleSessions, 90, "", 0.90); got.Level != ScaleLevelWarn {
		t.Errorf("閾値 90%% で 90/%d が予告にならない: %q", limit, got.Level)
	}
}

// 発話の上限は「1 セッションあたり」のため、対象セッションを返り値で示すこと。
func TestScaleUtterancesCarryScope(t *testing.T) {
	got := EvaluateScale(ScaleUtterances, 1000, "S-0007", DefaultScaleWarnRatio)
	if got.Level != ScaleLevelExceeded {
		t.Errorf("1,000 発話が上限到達にならない: %q", got.Level)
	}
	if got.Scope != "S-0007" {
		t.Errorf("対象セッションが返り値に載っていない: %q", got.Scope)
	}
}
