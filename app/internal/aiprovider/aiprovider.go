// Package aiprovider は AIプロバイダ抽象化層。3社アダプタ・ストリーミング・再試行・エラー正規化・エフォート写像・送信記録を担う。上位（対話エンジン・ドキュメント生成）は本パッケージのインタフェースのみに依存する。
package aiprovider

import "context"

// ProviderID は対応プロバイダの識別子（API キーで呼ぶ 3 社）。
type ProviderID string

const (
	ProviderAnthropic ProviderID = "anthropic"
	ProviderOpenAI    ProviderID = "openai"
	ProviderGoogle    ProviderID = "google"
)

// KeyRef はキーへの参照名（キーマネージャが OS のセキュアストレージに保存するときのアカウント名）。
type KeyRef string

// KeyProvider はキーマネージャが実装する。
// アダプタは API 呼び出しの都度これでキーを取得し、構造体・パッケージ変数に保持しない（メモリ上にキーが残る時間を短くするため）。
type KeyProvider interface {
	SecretKey(ctx context.Context, ref KeyRef) (string, error)
}

// ModelTier はモデルの区分（エフォート写像で使う）。
type ModelTier string

const (
	TierPrimary ModelTier = "primary" // 上位モデル
	TierLight   ModelTier = "light"   // 軽量モデル
	TierOther   ModelTier = "other"   // 既知一覧にない・区分不明
)

// ModelInfo は選択可能なモデルの情報。
type ModelInfo struct {
	ID            string    `json:"id"`
	DisplayName   string    `json:"display_name"`
	ContextWindow int       `json:"context_window"` // 最大入力トークン。0 = 不明
	MaxOutput     int       `json:"max_output"`     // 最大出力トークン上限。0 = 不明
	Tier          ModelTier `json:"tier"`

	// DefaultForTier は当該 Tier のエフォート既定モデルか。
	DefaultForTier bool `json:"default_for_tier,omitempty"`
	// SupportsReasoning は推論努力パラメータを受け付けるか（縮退の判定に使う）。
	SupportsReasoning bool `json:"supports_reasoning,omitempty"`
}

// Adapter はプロバイダごとのアダプタ（ProviderAdapter）。
//
// 上位モジュールは常に本インタフェース越しに扱い、
// 個別プロバイダ SDK を import しない（依存規則。lint で機械検知）。
type Adapter interface {
	ID() ProviderID
	// ListModels はモデル一覧を返す。取得できないときは ErrClassTransient の ProviderError を返し、
	// 呼び出し側が既知一覧（KnownModels）へ縮退する。
	ListModels(ctx context.Context) ([]ModelInfo, error)
	// StreamMessage はストリーミング対話。
	//
	// 戻り値の error はリクエストを送れなかった場合（キー取得失敗・入力不正）のみ。
	// 送信後のエラーはチャネルの EventError で通知する。
	// チャネルは EventDone または EventError で必ず閉じるため、呼び出し側は読み切ること。
	// ctx のキャンセルは中断であり、EventDone{Interrupted:true} で閉じる。
	StreamMessage(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)
	// VerifyKey は疎通確認。最小トークンのテスト呼び出しで成否を返す。
	VerifyKey(ctx context.Context) error
}
