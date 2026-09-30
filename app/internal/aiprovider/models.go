package aiprovider

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// knownModelsJSON はアプリ埋め込みの既知一覧（API からモデル一覧を取れないときの縮退先）。
//
// モデル改廃時の更新はこの JSON の修正のみで済ませる（アプリの版更新で配布する。
// アプリ再ビルドなしの外部更新はしない = 改ざん面を増やさない）。
//
// 収録値の出典（2026-08-27 時点の各社公式ドキュメントで確認）:
//   - Anthropic: platform.claude.com のモデル一覧（コンテキスト長・最大出力を明記）
//   - OpenAI: developers.openai.com のモデル一覧（同上）
//   - Google: ai.google.dev のモデル一覧（トークン上限の記載が無いため 0 = 不明とし、
//     API 応答の inputTokenLimit / outputTokenLimit を優先する）
//   - Codex App Server: 同梱する版の Codex に埋め込まれた既定のモデル定義（
//     `go run ./tools/codexcatalog` が写した adapter/codex/model_catalog.json が出典。
//     最大出力トークンは定義に無く、`turn/start` へも送らないため 0 = 不明とする）
//
//go:embed models.json
var knownModelsJSON []byte

var (
	knownOnce   sync.Once
	knownModels map[ProviderID][]ModelInfo
	knownErr    error
)

func loadKnownModels() (map[ProviderID][]ModelInfo, error) {
	knownOnce.Do(func() {
		var raw map[string][]ModelInfo
		if err := json.Unmarshal(knownModelsJSON, &raw); err != nil {
			knownErr = fmt.Errorf("既知モデル一覧を解釈できません: %w", err)
			return
		}
		knownModels = make(map[ProviderID][]ModelInfo, len(raw))
		for k, v := range raw {
			knownModels[ProviderID(k)] = v
		}
	})
	return knownModels, knownErr
}

// KnownModels は既知一覧のモデルを返す（API 取得失敗時の縮退先）。
func KnownModels(p ProviderID) ([]ModelInfo, error) {
	all, err := loadKnownModels()
	if err != nil {
		return nil, err
	}
	models := all[p]
	out := make([]ModelInfo, len(models))
	copy(out, models)
	return out, nil
}

// KnownModel は既知一覧から 1 件を引く。
func KnownModel(p ProviderID, id string) (ModelInfo, bool) {
	models, err := KnownModels(p)
	if err != nil {
		return ModelInfo{}, false
	}
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// DefaultModelFor は当該 Tier のエフォート既定モデルを返す（エフォート写像のモデル選択）。
func DefaultModelFor(p ProviderID, tier ModelTier) (ModelInfo, bool) {
	models, err := KnownModels(p)
	if err != nil {
		return ModelInfo{}, false
	}
	for _, m := range models {
		if m.Tier == tier && m.DefaultForTier {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// MergeWithKnown は API 取得したモデル一覧を既知一覧で補完する。
//
//   - コンテキスト長・最大出力が API 応答に含まれない（0）場合のみ既知一覧の値で補う
//     （コンテキスト長は API 応答値を優先する）。
//   - 既知一覧に無いモデルは Tier=TierOther・コンテキスト長不明のまま選択可能に含める。
func MergeWithKnown(p ProviderID, fetched []ModelInfo) []ModelInfo {
	out := make([]ModelInfo, 0, len(fetched))
	for _, m := range fetched {
		known, ok := KnownModel(p, m.ID)
		if !ok {
			if m.Tier == "" {
				m.Tier = TierOther
			}
			out = append(out, m)
			continue
		}
		if m.DisplayName == "" {
			m.DisplayName = known.DisplayName
		}
		if m.ContextWindow == 0 {
			m.ContextWindow = known.ContextWindow
		}
		if m.MaxOutput == 0 {
			m.MaxOutput = known.MaxOutput
		}
		m.Tier = known.Tier
		m.DefaultForTier = known.DefaultForTier
		m.SupportsReasoning = known.SupportsReasoning
		out = append(out, m)
	}
	return out
}
