package dialogue

// 推論努力パラメータの縮退の通知。
//
// 縮退は「送信から省略する」だけで利用者には見えないため、**起きたことが動作ログへ残る**ことが
// 事後の説明可能性の唯一の担保になる。判定と通知を
// effortParams 1 か所に閉じてあることを、ここで固定する。

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
	t.Run("推論努力を受け付けないモデルでは縮退を通知する", func(t *testing.T) {
		var notified []string
		e := &Engine{cfg: Config{
			Adapter:   idAdapter{},
			Effort:    aiprovider.EffortHigh,
			Model:     aiprovider.ModelInfo{ID: "claude-legacy", SupportsReasoning: false, MaxOutput: 16384},
			OnDegrade: func(m aiprovider.ModelInfo) { notified = append(notified, m.ID) },
		}}
		got := e.effortParams()
		if got.ReasoningLevel != "" {
			t.Errorf("推論努力レベルが送信対象のまま残った: %q", got.ReasoningLevel)
		}
		if len(notified) != 1 || notified[0] != "claude-legacy" {
			t.Errorf("縮退の通知が %v（期待 [claude-legacy] 1 回）", notified)
		}
	})

	t.Run("受け付けるモデルでは通知しない", func(t *testing.T) {
		notified := 0
		e := &Engine{cfg: Config{
			Adapter:   idAdapter{},
			Effort:    aiprovider.EffortHigh,
			Model:     aiprovider.ModelInfo{ID: "claude-opus-5", SupportsReasoning: true, MaxOutput: 16384},
			OnDegrade: func(aiprovider.ModelInfo) { notified++ },
		}}
		got := e.effortParams()
		if got.ReasoningLevel != aiprovider.ReasoningHigh {
			t.Errorf("推論努力レベルが %q（期待 %q）", got.ReasoningLevel, aiprovider.ReasoningHigh)
		}
		if notified != 0 {
			t.Errorf("縮退していないのに %d 回通知された", notified)
		}
	})

	t.Run("通知先が無くても落ちない", func(t *testing.T) {
		e := &Engine{cfg: Config{
			Adapter: idAdapter{},
			Effort:  aiprovider.EffortStandard,
			Model:   aiprovider.ModelInfo{ID: "claude-legacy", SupportsReasoning: false},
		}}
		if got := e.effortParams(); got.ReasoningLevel != "" {
			t.Errorf("推論努力レベルが %q（期待 空）", got.ReasoningLevel)
		}
	})
}
