package aiprovider

// 本ファイルは送信前のトークン上限の判定に使うトークン概算を担う。
// 対話エンジンはこれで「注入文脈＋最大出力トークン＋安全余裕」がコンテキスト長に収まるかを判定する。

// トークン概算の係数（実装時決定: 実測より過小にならない保守的推定であること）。
//
// 3社の BPE トークナイザでは概ね ASCII 4 文字 ≒ 1 トークン、日本語 1 文字 ≒ 1 トークンだが、
// 記号・稀な文字は 1 文字が複数トークンに割れる。過小推定はコンテキスト長超過を招くため、
// ASCII は 3 文字 = 1 トークン、非 ASCII は 1 文字 = 1.5 トークンで見積もる。
const (
	asciiCharsPerToken = 3
	wideTokenNumerator = 3 // 非 ASCII 1 文字あたり 3/2 トークン
	wideTokenDenom     = 2
)

// messageOverheadTokens は 1 メッセージあたりの役割・区切りの固定費（保守的な多めの値）。
const messageOverheadTokens = 8

// EstimateTokens はテキストのトークン数を保守的に概算する。
//
// プロバイダのカウント API は使わない（呼び出しのたびに往復を増やさないため）。
// 返す値は実測を下回らないことを意図しており、上振れは安全側として許容する。
func EstimateTokens(text string) int {
	var ascii, wide int
	for _, r := range text {
		if r < 0x80 {
			ascii++
			continue
		}
		wide++
	}
	tokens := (ascii + asciiCharsPerToken - 1) / asciiCharsPerToken
	tokens += (wide*wideTokenNumerator + wideTokenDenom - 1) / wideTokenDenom
	return tokens
}

// EstimateRequestTokens は送信リクエスト全体の入力トークン数を保守的に概算する。
func EstimateRequestTokens(req ChatRequest) int {
	total := EstimateTokens(req.System)
	for _, m := range req.Messages {
		total += EstimateTokens(m.Content) + messageOverheadTokens
	}
	return total
}
