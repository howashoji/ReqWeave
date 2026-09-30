package aiprovider

import "testing"

// エフォート写像表と一致すること。
func TestMapEffortMatchesDesignTable(t *testing.T) {
	cases := []struct {
		provider ProviderID
		effort   Effort
		want     EffortParams
	}{
		// モデル選択: 低 = 軽量 / 標準・高 = 上位。推論努力は 3 社とも同じレベル体系
		{ProviderAnthropic, EffortLow, EffortParams{ModelTier: TierLight, MaxOutputTokens: 4096, ReasoningLevel: "low"}},
		{ProviderAnthropic, EffortStandard, EffortParams{ModelTier: TierPrimary, MaxOutputTokens: 8192, ReasoningLevel: "medium"}},
		{ProviderAnthropic, EffortHigh, EffortParams{ModelTier: TierPrimary, MaxOutputTokens: 16384, ReasoningLevel: "high"}},
		{ProviderGoogle, EffortLow, EffortParams{ModelTier: TierLight, MaxOutputTokens: 4096, ReasoningLevel: "low"}},
		{ProviderGoogle, EffortStandard, EffortParams{ModelTier: TierPrimary, MaxOutputTokens: 8192, ReasoningLevel: "medium"}},
		{ProviderGoogle, EffortHigh, EffortParams{ModelTier: TierPrimary, MaxOutputTokens: 16384, ReasoningLevel: "high"}},
		{ProviderOpenAI, EffortLow, EffortParams{ModelTier: TierLight, MaxOutputTokens: 4096, ReasoningLevel: "low"}},
		{ProviderOpenAI, EffortStandard, EffortParams{ModelTier: TierPrimary, MaxOutputTokens: 8192, ReasoningLevel: "medium"}},
		{ProviderOpenAI, EffortHigh, EffortParams{ModelTier: TierPrimary, MaxOutputTokens: 16384, ReasoningLevel: "high"}},
	}
	for _, c := range cases {
		got := MapEffort(c.provider, c.effort)
		if got != c.want {
			t.Errorf("%s / %s: got %+v, want %+v", c.provider, c.effort, got, c.want)
		}
	}
}

// 未知の段階は既定（標準）として扱う（既定 = 標準）。
func TestMapEffortDefaultsToStandard(t *testing.T) {
	if got, want := MapEffort(ProviderAnthropic, Effort("unknown")), MapEffort(ProviderAnthropic, EffortStandard); got != want {
		t.Errorf("未知の段階が標準に倒れていない: %+v", got)
	}
}

// 段階間の大小関係（低 < 標準 < 高）を崩さない（写像の値を調整しても守る条件）。
func TestEffortStagesAreMonotonic(t *testing.T) {
	for _, p := range []ProviderID{ProviderAnthropic, ProviderOpenAI, ProviderGoogle} {
		low := MapEffort(p, EffortLow)
		std := MapEffort(p, EffortStandard)
		high := MapEffort(p, EffortHigh)
		if !(low.MaxOutputTokens < std.MaxOutputTokens && std.MaxOutputTokens < high.MaxOutputTokens) {
			t.Errorf("%s: 最大出力トークンの大小関係が崩れている: %d %d %d",
				p, low.MaxOutputTokens, std.MaxOutputTokens, high.MaxOutputTokens)
		}
		if low.ReasoningLevel != ReasoningLow || std.ReasoningLevel != ReasoningMedium || high.ReasoningLevel != ReasoningHigh {
			t.Errorf("%s: 推論努力レベルの段階が違う: %q %q %q",
				p, low.ReasoningLevel, std.ReasoningLevel, high.ReasoningLevel)
		}
	}
}

// 既定値がモデルの上限を超える場合はモデル上限へ丸める。
func TestClampToModel(t *testing.T) {
	params := MapEffort(ProviderAnthropic, EffortHigh)
	clamped := params.ClampToModel(ModelInfo{MaxOutput: 4096})
	if clamped.MaxOutputTokens != 4096 {
		t.Errorf("モデル上限へ丸められていない: %d", clamped.MaxOutputTokens)
	}
	// 上限不明（0）のときは丸めない
	if got := params.ClampToModel(ModelInfo{MaxOutput: 0}); got.MaxOutputTokens != params.MaxOutputTokens {
		t.Errorf("上限不明のモデルで丸められた: %d", got.MaxOutputTokens)
	}
	// 上限が十分大きいときはそのまま
	if got := params.ClampToModel(ModelInfo{MaxOutput: 128000}); got.MaxOutputTokens != params.MaxOutputTokens {
		t.Errorf("不要な丸めが起きた: %d", got.MaxOutputTokens)
	}
}

// 推論努力パラメータ非対応モデルでは当該パラメータを省略し、
// モデル選択・最大出力トークンの段階差は保つ。
func TestDegrade(t *testing.T) {
	for _, p := range []ProviderID{ProviderAnthropic, ProviderOpenAI, ProviderGoogle} {
		high := MapEffort(p, EffortHigh)
		degraded, ok := high.Degrade(ModelInfo{SupportsReasoning: false})
		if !ok {
			t.Errorf("%s: 縮退が報告されない", p)
		}
		if degraded.ReasoningLevel != "" {
			t.Errorf("%s: 推論努力パラメータが省略されていない: %+v", p, degraded)
		}
		if degraded.ModelTier != high.ModelTier || degraded.MaxOutputTokens != high.MaxOutputTokens {
			t.Errorf("%s: 縮退でモデル選択・最大出力の段階差が失われた: %+v", p, degraded)
		}
		// 対応モデルでは縮退しない
		if got, ok := high.Degrade(ModelInfo{SupportsReasoning: true}); ok || got != high {
			t.Errorf("%s: 対応モデルで縮退した: %+v %v", p, got, ok)
		}
		// 送るレベルが無い（既に省略済み）場合は縮退扱いにしない
		none := EffortParams{ModelTier: TierLight, MaxOutputTokens: 4096}
		if _, ok := none.Degrade(ModelInfo{SupportsReasoning: false}); ok {
			t.Errorf("%s: 省略すべきパラメータが無いのに縮退と報告された", p)
		}
	}
}

func TestEffortDescriptionMentionsStageDifference(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range []Effort{EffortLow, EffortStandard, EffortHigh} {
		d := EffortDescription(e)
		if d == "" {
			t.Errorf("%s の説明が空", e)
		}
		if seen[d] {
			t.Errorf("%s の説明が他の段階と同一: %q", e, d)
		}
		seen[d] = true
	}
}

// 低で選ぶ Anthropic の軽量モデル（Claude Haiku 4.5）は
// 推論努力パラメータ非対応のため、縮退で省略され、段階差はモデル選択と最大出力トークンで成立する。
func TestLowEffortDegradesOnAnthropicLightModel(t *testing.T) {
	light, ok := DefaultModelFor(ProviderAnthropic, TierLight)
	if !ok {
		t.Fatal("Anthropic の軽量モデルが既知一覧にない")
	}
	if light.SupportsReasoning {
		t.Fatalf("既知一覧が %s を推論努力対応としている（実 API は effort 非対応）", light.ID)
	}

	params := MapEffort(ProviderAnthropic, EffortLow)
	degraded, wasDegraded := params.Degrade(light)
	if !wasDegraded {
		t.Error("非対応モデルなのに縮退しない")
	}
	if degraded.ReasoningLevel != "" {
		t.Errorf("推論努力レベルが省略されていない: %q", degraded.ReasoningLevel)
	}
	// 段階差はモデル選択と最大出力トークンで残る
	standard := MapEffort(ProviderAnthropic, EffortStandard)
	if degraded.ModelTier == standard.ModelTier || degraded.MaxOutputTokens >= standard.MaxOutputTokens {
		t.Errorf("縮退後に段階差が失われた: %+v vs %+v", degraded, standard)
	}
}
