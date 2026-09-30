package aiprovider

import (
	"strings"
	"testing"
)

// 実装時決定: 実測より過小にならない保守的推定であること。
//
// 各ケースの upperMeasured は、3社の BPE トークナイザで当該文字列が取り得る**実測の上限**として
// 置いた値（英語は 1 トークン ≒ 4 文字、日本語は 1 文字 ≒ 1 トークンで、記号・稀字はこれを上回る）。
// 推定値がこの上限以上であれば、コンテキスト長超過を招く過小推定は起きない。
func TestEstimateTokensIsConservative(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		upperMeasured int
	}{
		{"空文字", "", 0},
		{"英単語", "hello", 2},
		{"英文", "Hello, world! This is a requirements definition tool.", 16},
		{"日本語短文", "こんにちは", 5},
		{"日本語長文", "在庫引当のタイミングは受注確定時とし、欠品時は自動でバックオーダーを起票する。", 38},
		{"英日混在", "FR-DLG-001 質問の生成: メタモデルの章観点に基づき 1 件ずつ生成する", 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateTokens(tc.text)
			if got < tc.upperMeasured {
				t.Errorf("推定が実測上限を下回る（過小推定）: %d < %d", got, tc.upperMeasured)
			}
		})
	}
}

// 文字が増えれば推定も増える（対話エンジンの予算計算が単調性に依存する）。
func TestEstimateTokensMonotonic(t *testing.T) {
	base := EstimateTokens("要件定義")
	longer := EstimateTokens("要件定義書")
	if longer <= base {
		t.Errorf("文字数が増えても推定が増えない: %d → %d", base, longer)
	}
	if EstimateTokens("") != 0 {
		t.Errorf("空文字が 0 でない: %d", EstimateTokens(""))
	}
}

// リクエスト全体の概算は System と全メッセージを含み、メッセージ数の固定費を加える。
func TestEstimateRequestTokens(t *testing.T) {
	req := ChatRequest{
		System: "システムプロンプト",
		Messages: []Message{
			{Role: RoleUser, Content: "質問への回答です"},
			{Role: RoleAssistant, Content: "了解しました"},
		},
	}
	got := EstimateRequestTokens(req)
	parts := EstimateTokens(req.System) + EstimateTokens(req.Messages[0].Content) + EstimateTokens(req.Messages[1].Content)
	if got <= parts {
		t.Errorf("メッセージごとの固定費が加算されていない: %d（本文計 %d）", got, parts)
	}
	if EstimateRequestTokens(ChatRequest{}) != 0 {
		t.Error("空のリクエストが 0 でない")
	}
}

// 大きな入力でも桁が崩れない（送信前の上限判定で使う）。
func TestEstimateTokensLargeInput(t *testing.T) {
	text := strings.Repeat("在庫管理単位はロット単位とする。", 1000) // 16 文字 × 1000
	got := EstimateTokens(text)
	if got < 16000 {
		t.Errorf("大きな入力で過小推定になっている: %d", got)
	}
}

// 入力長に対して単調非減少であること
// （分割判定がプレフィックス長の逆転で壊れないことを、全プレフィックスで確認する）。
func TestEstimateTokensMonotonicOverPrefixes(t *testing.T) {
	text := "FR-DLG-001 在庫の引当は受注確定時に行う。Lot 単位で管理し、欠品時は BO（バックオーダー）を起票する。"
	runes := []rune(text)
	prev := 0
	for i := 0; i <= len(runes); i++ {
		got := EstimateTokens(string(runes[:i]))
		if got < prev {
			t.Fatalf("プレフィックス %d 文字で推定が減った: %d → %d", i, prev, got)
		}
		prev = got
	}
	if prev != EstimateTokens(text) {
		t.Errorf("全文の推定が一致しない: %d / %d", prev, EstimateTokens(text))
	}
}
