package aiprovider

// Effort はエフォート設定の 3 段階（既定は標準）。
type Effort string

const (
	EffortLow      Effort = "low"
	EffortStandard Effort = "standard"
	EffortHigh     Effort = "high"
)

// EffortParams はエフォート写像の結果（呼び出し時にプロバイダ固有値へ変換した値）。
//
// 指定先: Anthropic = output_config.effort / OpenAI = reasoning.effort /
// Google = thinking_level / Codex App Server = turn/start の effort。
// **Codex App Server では最大出力トークンを送らない**（指定する手段がない）。
type EffortParams struct {
	ModelTier       ModelTier // モデル選択（軽量 / 上位）
	MaxOutputTokens int
	// ReasoningLevel は推論努力レベル。プロバイダ側の指定先は
	// Anthropic: output_config.effort / OpenAI: reasoning.effort / Google: thinking_level。
	// "" = 送信しない（非対応モデルでの縮退）。
	ReasoningLevel string
}

// 推論努力レベル（xhigh / max は対話用途では採らない）。
const (
	ReasoningLow    = "low"
	ReasoningMedium = "medium"
	ReasoningHigh   = "high"
)

// effortRow はエフォート写像表の 1 段。
type effortRow struct {
	tier            ModelTier
	maxOutputTokens int
	reasoningLevel  string
}

// effortTable はエフォート写像表（低 / 標準 / 高）。
//
// 「標準」と「高」は同じ Tier（上位モデル）で、推論努力レベルと最大出力トークンで段階差をつける。
// 追問深さ（低=1 / 標準=3 / 高=5）は対話エンジンが持ち、本層は写像しない。
var effortTable = map[Effort]effortRow{
	EffortLow:      {tier: TierLight, maxOutputTokens: 4096, reasoningLevel: ReasoningLow},
	EffortStandard: {tier: TierPrimary, maxOutputTokens: 8192, reasoningLevel: ReasoningMedium},
	EffortHigh:     {tier: TierPrimary, maxOutputTokens: 16384, reasoningLevel: ReasoningHigh},
}

// MapEffort はエフォート段階をプロバイダ固有の呼び出しパラメータへ写像する。
// 未知の段階は既定（標準）として扱う。
//
// 3 社とも同じレベル体系（low / medium / high）へ写像する。
// 指定先だけがプロバイダごとに異なり、その差はアダプタが吸収する。
func MapEffort(p ProviderID, e Effort) EffortParams {
	row, ok := effortTable[e]
	if !ok {
		row = effortTable[EffortStandard]
	}
	return EffortParams{
		ModelTier:       row.tier,
		MaxOutputTokens: row.maxOutputTokens,
		ReasoningLevel:  row.reasoningLevel,
	}
}

// ClampToModel は選択モデルの上限に合わせて写像結果を丸める。
// モデルの最大出力が不明（0）の場合は丸めない。
func (p EffortParams) ClampToModel(m ModelInfo) EffortParams {
	if m.MaxOutput > 0 && p.MaxOutputTokens > m.MaxOutput {
		p.MaxOutputTokens = m.MaxOutput
	}
	return p
}

// Degrade は推論努力パラメータを持たないモデル向けの縮退。
//
//  1. 当該パラメータを送信から省略する（未知パラメータをエラーにしない）
//  2. モデル選択・最大出力トークンの 2 写像で段階差を保つ
//
// 縮退が起きたことは動作ログへ記録する（利用者への通知はしない）ため、
// 呼び出し側が判定できるよう degraded を返す。記録の口は対話エンジン・ドキュメント生成の
// effortParams（Config.OnDegrade）に閉じてあり、本層はロガーを知らない。
func (p EffortParams) Degrade(m ModelInfo) (params EffortParams, degraded bool) {
	if m.SupportsReasoning {
		return p, false
	}
	if p.ReasoningLevel == "" {
		return p, false
	}
	p.ReasoningLevel = ""
	return p, true
}

// EffortDescription は利用者向けの効果説明（設定画面に出す）。
// 写像表と齟齬が出ない表現にする。
func EffortDescription(e Effort) string {
	switch e {
	case EffortLow:
		return "軽量モデル・追問少なめ（トークン消費が最も小さい）"
	case EffortHigh:
		return "上位モデル・最大の掘り下げ（トークン消費が最も大きい）"
	default:
		return "上位モデル・標準の掘り下げ（既定）"
	}
}
