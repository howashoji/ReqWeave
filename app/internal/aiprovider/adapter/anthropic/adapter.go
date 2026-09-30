// Package anthropic は Anthropic 向けの ProviderAdapter 実装。
// 個別プロバイダ SDK の import は本パッケージ内に閉じ込める（依存規則。lint で機械検知）。
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// defaultBaseURL は公式 API エンドポイント（設定による書き換え手段は設けない）。
const defaultBaseURL = "https://api.anthropic.com"

// Adapter は Anthropic のアダプタ。キーは呼び出しの都度取得し、フィールドに保持しない。
type Adapter struct {
	keys aiprovider.KeyProvider
	ref  aiprovider.KeyRef
	// baseURL は既定で公式エンドポイント。パッケージ内テストのみが差し替える
	// （公開 API・設定からの書き換え経路は無い）。
	baseURL string
	// httpClient は接続確立タイムアウトを適用したクライアント。
	// 応答完了・無通信の打ち切りは StreamRetrying が ctx で行う。
	httpClient *http.Client
	// onPanic はストリーミング用ゴルーチンのパニックの記録先（nil なら記録しない）。
	onPanic aiprovider.PanicRecorder
}

func init() {
	aiprovider.Register(aiprovider.ProviderAnthropic, func(keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) aiprovider.Adapter {
		return NewWithOptions(keys, ref, opts)
	})
}

// New はアダプタを既定のタイムアウトで生成する。
func New(keys aiprovider.KeyProvider, ref aiprovider.KeyRef) *Adapter {
	return NewWithOptions(keys, ref, aiprovider.AdapterOptions{})
}

// NewWithOptions はアダプタをタイムアウト指定つきで生成する。
func NewWithOptions(keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) *Adapter {
	return &Adapter{
		keys:       keys,
		ref:        ref,
		baseURL:    defaultBaseURL,
		httpClient: aiprovider.NewHTTPClient(opts.Timeouts),
		onPanic:    opts.OnPanic,
	}
}

// ID はプロバイダ ID を返す。
func (a *Adapter) ID() aiprovider.ProviderID { return aiprovider.ProviderAnthropic }

// client は呼び出しごとにキーを取得してクライアントを作る。
// SDK のデバッグログ機能は使用しない。
func (a *Adapter) client(ctx context.Context) (sdk.Client, error) {
	key, err := a.keys.SecretKey(ctx, a.ref)
	if err != nil {
		return sdk.Client{}, err
	}
	return sdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(a.baseURL),
		option.WithAPIKey(key), // 認証ヘッダ x-api-key
		option.WithHTTPClient(a.httpClient),
		option.WithMaxRetries(0), // 再試行は StreamRetrying が行う
	), nil
}

// ListModels はモデル一覧を取得し、既知一覧で補完して返す。
func (a *Adapter) ListModels(ctx context.Context) ([]aiprovider.ModelInfo, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var out []aiprovider.ModelInfo
	pager := client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})
	for pager.Next() {
		m := pager.Current()
		out = append(out, aiprovider.ModelInfo{
			ID:            m.ID,
			DisplayName:   m.DisplayName,
			ContextWindow: int(m.MaxInputTokens),
			MaxOutput:     int(m.MaxTokens),
		})
	}
	if err := pager.Err(); err != nil {
		return nil, normalizeError(err)
	}
	return aiprovider.MergeWithKnown(aiprovider.ProviderAnthropic, out), nil
}

// VerifyKey は疎通確認。
// 既定モデルの取得を最小の呼び出しとして用い、認証と選択モデルの利用可否を確認する。
func (a *Adapter) VerifyKey(ctx context.Context) error {
	client, err := a.client(ctx)
	if err != nil {
		return err
	}
	model, ok := aiprovider.DefaultModelFor(aiprovider.ProviderAnthropic, aiprovider.TierLight)
	if !ok {
		return fmt.Errorf("既知モデル一覧に Anthropic の既定モデルがありません")
	}
	if _, err := client.Models.Get(ctx, model.ID, sdk.ModelGetParams{}); err != nil {
		return normalizeError(err)
	}
	return nil
}

// normalizeError は SDK 固有エラーを ProviderError へ正規化する。
//
// Message にはプロバイダの生 JSON のみを載せる（リクエストヘッダを含めない = キーを露出させない）。
func normalizeError(err error) error {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return aiprovider.NetworkError(aiprovider.ProviderAnthropic, err)
	}
	status := apiErr.StatusCode
	class := aiprovider.ClassifyHTTPStatus(status)
	// 529（overloaded）は Anthropic 固有の過負荷応答 = 一時的
	if status == 529 {
		class = aiprovider.ErrClassTransient
	}
	code := string(apiErr.Type())
	if status == 400 {
		// 400 のうちモデル指定不正は設定起因、それ以外（入力不正・コンテキスト長超過）は恒久的
		if code == "not_found_error" {
			class = aiprovider.ErrClassConfig
		} else {
			class = aiprovider.ErrClassPermanent
		}
	}
	return &aiprovider.ProviderError{
		Class:      class,
		Provider:   aiprovider.ProviderAnthropic,
		HTTPStatus: status,
		Code:       code,
		Message:    apiErr.RawJSON(),
	}
}
