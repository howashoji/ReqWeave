package aiprovider

import "testing"

// 既知一覧は各プロバイダの上位・軽量の既定モデルを持つ（エフォート写像のモデル選択に使う）。
func TestKnownModelsHaveTierDefaults(t *testing.T) {
	for _, p := range []ProviderID{ProviderAnthropic, ProviderOpenAI, ProviderGoogle} {
		models, err := KnownModels(p)
		if err != nil {
			t.Fatalf("%s: 既知一覧を読めません: %v", p, err)
		}
		if len(models) == 0 {
			t.Errorf("%s: 既知一覧が空", p)
			continue
		}
		defaults := map[ModelTier]int{}
		ids := map[string]bool{}
		for _, m := range models {
			if m.ID == "" || m.DisplayName == "" {
				t.Errorf("%s: ID / 表示名が空のモデルがある: %+v", p, m)
			}
			if ids[m.ID] {
				t.Errorf("%s: モデル ID が重複している: %s", p, m.ID)
			}
			ids[m.ID] = true
			switch m.Tier {
			case TierPrimary, TierLight, TierOther:
			default:
				t.Errorf("%s: 区分が不正: %+v", p, m)
			}
			if m.DefaultForTier {
				defaults[m.Tier]++
			}
			if m.MaxOutput < 0 || m.ContextWindow < 0 {
				t.Errorf("%s: トークン上限が負: %+v", p, m)
			}
		}
		for _, tier := range []ModelTier{TierPrimary, TierLight} {
			if defaults[tier] != 1 {
				t.Errorf("%s: %s の既定モデルが %d 件（1 件であること）", p, tier, defaults[tier])
			}
		}
		for _, tier := range []ModelTier{TierPrimary, TierLight} {
			if _, ok := DefaultModelFor(p, tier); !ok {
				t.Errorf("%s: %s の既定モデルを引けない", p, tier)
			}
		}
	}
}

// 呼び出し側が返り値を書き換えても既知一覧が壊れないこと。
func TestKnownModelsReturnsCopy(t *testing.T) {
	first, err := KnownModels(ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("既知一覧が空")
	}
	original := first[0].ID
	first[0].ID = "壊した"
	second, err := KnownModels(ProviderAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].ID != original {
		t.Errorf("既知一覧が書き換わった: %q", second[0].ID)
	}
}

// API 応答値を優先し、欠落（0）のみ既知一覧で補完する。
// 既知一覧に無いモデルは TierOther として選択可能に含める。
func TestMergeWithKnown(t *testing.T) {
	known, err := KnownModels(ProviderOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	target := known[0]

	fetched := []ModelInfo{
		{ID: target.ID}, // 応答にトークン上限が無い → 既知一覧で補完
		{ID: target.ID + "-x", ContextWindow: 123, MaxOutput: 45}, // 既知一覧に無い → そのまま含める
	}
	got := MergeWithKnown(ProviderOpenAI, fetched)
	if len(got) != 2 {
		t.Fatalf("件数が違う: %d", len(got))
	}
	if got[0].ContextWindow != target.ContextWindow || got[0].MaxOutput != target.MaxOutput {
		t.Errorf("既知一覧で補完されていない: %+v", got[0])
	}
	if got[0].DisplayName != target.DisplayName || got[0].Tier != target.Tier {
		t.Errorf("表示名・区分が補完されていない: %+v", got[0])
	}
	if got[1].Tier != TierOther {
		t.Errorf("既知一覧に無いモデルの区分が TierOther でない: %+v", got[1])
	}
	if got[1].ContextWindow != 123 || got[1].MaxOutput != 45 {
		t.Errorf("API 応答値が失われた: %+v", got[1])
	}

	// API 応答値がある場合はそちらを優先する（Google のコンテキスト長）
	fetchedWithValues := []ModelInfo{{ID: target.ID, ContextWindow: 999, MaxOutput: 888}}
	merged := MergeWithKnown(ProviderOpenAI, fetchedWithValues)
	if merged[0].ContextWindow != 999 || merged[0].MaxOutput != 888 {
		t.Errorf("API 応答値が既知一覧で上書きされた: %+v", merged[0])
	}
}
