package docgen

// 推論努力パラメータの縮退（モデルが指定の段階に対応しないときに近い段階へ落とす）の通知。対話エンジン側と同じ扱い。

import (
	"context"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// idAdapter は effortParams が使う ID() だけを持つ最小のアダプタ。
type idAdapter struct{}

func (idAdapter) ID() aiprovider.ProviderID { return aiprovider.ProviderAnthropic }
func (idAdapter) ListModels(context.Context) ([]aiprovider.ModelInfo, error) {
	return nil, nil
}
func (idAdapter) StreamMessage(context.Context, aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	return nil, nil
}
func (idAdapter) VerifyKey(context.Context) error { return nil }

func TestEffortParamsNotifiesDegrade(t *testing.T) {
	var notified []string
	g := &Generator{cfg: Config{
		Adapter:   idAdapter{},
		Effort:    aiprovider.EffortHigh,
		Model:     aiprovider.ModelInfo{ID: "claude-legacy", SupportsReasoning: false, MaxOutput: 16384},
		OnDegrade: func(m aiprovider.ModelInfo) { notified = append(notified, m.ID) },
	}}
	if got := g.effortParams(); got.ReasoningLevel != "" {
		t.Errorf("推論努力レベルが送信対象のまま残った: %q", got.ReasoningLevel)
	}
	if len(notified) != 1 || notified[0] != "claude-legacy" {
		t.Errorf("縮退の通知が %v（期待 [claude-legacy] 1 回）", notified)
	}

	supported := &Generator{cfg: Config{
		Adapter:   idAdapter{},
		Effort:    aiprovider.EffortHigh,
		Model:     aiprovider.ModelInfo{ID: "claude-opus-5", SupportsReasoning: true, MaxOutput: 16384},
		OnDegrade: func(aiprovider.ModelInfo) { t.Error("縮退していないのに通知された") },
	}}
	if got := supported.effortParams(); got.ReasoningLevel != aiprovider.ReasoningHigh {
		t.Errorf("推論努力レベルが %q（期待 %q）", got.ReasoningLevel, aiprovider.ReasoningHigh)
	}
}
