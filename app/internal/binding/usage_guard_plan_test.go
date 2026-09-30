package binding

// 単体テスト（構造の固定）: **ChatGPT のプランの残量は、トークン上限の判定に使われない**
// （プランの残量は目安の表示に留め、上限判定はプロジェクトの累計だけで行う）。
//
// 既存のテストは「残量の表示」「残量切れのエラー」「閲覧で取りに行かないこと」を固定しているが、
// **上限判定の側から見た検査**（判定の経路が残量に触れていないこと）が無かった。
// 振る舞いの検査では「たまたま今は使っていない」ことしか示せないため、
// usage_guard_test.go と同じ go/ast の走査で、判定の経路から残量へ到達しないことを固定する。

import "testing"

// planUsageFuncs は残量を読む関数（binding 側の入口）。
var planUsageFuncs = map[string]bool{
	"planUsage":      true, // アダプタの PlanUsageReporter を呼ぶ唯一の場所
	"CodexPlanUsage": true, // 画面向けの公開バインディング
}

// usageJudgementEntries はトークン上限の判定の経路。
var usageJudgementEntries = []string{
	"beginAICall",    // AI 呼び出しの共通前段（判定と停止）
	"usageStatusOf",  // 累計と上限から現在の状態を組み立てる
	"UsageStatusNow", // 画面へ返す現在の状態
}

func TestTokenLimitJudgementDoesNotUsePlanUsage(t *testing.T) {
	calls, _ := funcCalls(t, ".")

	// 前提: 走査の対象が実在すること（名前が変わって空振りするのを防ぐ）。
	for _, entry := range usageJudgementEntries {
		if _, ok := calls[entry]; !ok {
			t.Fatalf("上限判定の関数 %s が見つからない（名前が変わったら本テストを直す）", entry)
		}
	}
	for name := range planUsageFuncs {
		if _, ok := calls[name]; !ok {
			t.Fatalf("残量を読む関数 %s が見つからない（名前が変わったら本テストを直す）", name)
		}
	}
	// 検査が効いていることの確認: 残量を読む経路は、実際に残量へ到達する。
	if !reachesAny(calls, "CodexPlanUsage", planUsageFuncs) {
		t.Fatal("残量の表示の経路が残量へ到達しない（走査が働いていない）")
	}

	// 本題: 上限判定の経路は残量へ到達しない。
	for _, entry := range usageJudgementEntries {
		if reachesAny(calls, entry, planUsageFuncs) {
			t.Errorf("トークン上限の判定 %s が ChatGPT のプランの残量に触れている"+
				"（上限判定はプロジェクトの累計だけで行う）", entry)
		}
	}
}
