// Package openai は OpenAI 向けの ProviderAdapter 実装。
// 個別プロバイダ SDK の import は本パッケージ内に閉じ込める（依存規則。lint で機械検知）。
package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// defaultBaseURL は公式 API エンドポイント（設定による書き換え手段は設けない）。
const defaultBaseURL = "https://api.openai.com/v1"

// Adapter は OpenAI のアダプタ。キーは呼び出しの都度取得し、フィールドに保持しない。
type Adapter struct {
	keys    aiprovider.KeyProvider
	ref     aiprovider.KeyRef
	baseURL string // パッケージ内テストのみが差し替える
	// httpClient は接続確立タイムアウトを適用したクライアント。
	// 応答完了・無通信の打ち切りは StreamRetrying が ctx で行う。
	httpClient *http.Client
	// onPanic はストリーミング用ゴルーチンのパニックの記録先（nil なら記録しない）。
	onPanic aiprovider.PanicRecorder
}

func init() {
	aiprovider.Register(aiprovider.ProviderOpenAI, func(keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) aiprovider.Adapter {
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
func (a *Adapter) ID() aiprovider.ProviderID { return aiprovider.ProviderOpenAI }

func (a *Adapter) client(ctx context.Context) (sdk.Client, error) {
	key, err := a.keys.SecretKey(ctx, a.ref)
	if err != nil {
		return sdk.Client{}, err
	}
	return sdk.NewClient(
		option.WithBaseURL(a.baseURL),
		option.WithAPIKey(key), // 認証ヘッダ Authorization: Bearer
		option.WithHTTPClient(a.httpClient),
		option.WithMaxRetries(0), // 再試行は StreamRetrying が行う
	), nil
}

// ListModels はモデル一覧を取得し、既知一覧で補完して返す。
// OpenAI の応答はコンテキスト長を含まないため、既知一覧との突合で補う。
func (a *Adapter) ListModels(ctx context.Context) ([]aiprovider.ModelInfo, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var out []aiprovider.ModelInfo
	pager := client.Models.ListAutoPaging(ctx)
	for pager.Next() {
		m := pager.Current()
		out = append(out, aiprovider.ModelInfo{ID: m.ID, DisplayName: m.ID})
	}
	if err := pager.Err(); err != nil {
		return nil, normalizeError(err)
	}
	return aiprovider.MergeWithKnown(aiprovider.ProviderOpenAI, out), nil
}

// VerifyKey は疎通確認。
func (a *Adapter) VerifyKey(ctx context.Context) error {
	client, err := a.client(ctx)
	if err != nil {
		return err
	}
	model, ok := aiprovider.DefaultModelFor(aiprovider.ProviderOpenAI, aiprovider.TierLight)
	if !ok {
		return fmt.Errorf("既知モデル一覧に OpenAI の既定モデルがありません")
	}
	if _, err := client.Models.Get(ctx, model.ID); err != nil {
		return normalizeError(err)
	}
	return nil
}

// normalizeError は SDK 固有エラーを ProviderError へ正規化する。
//
// OpenAI の 429 はレート制限と残高不足の両義のため、code で区分を分ける。
func normalizeError(err error) error {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return aiprovider.NetworkError(aiprovider.ProviderOpenAI, err)
	}
	status := apiErr.StatusCode
	class := aiprovider.ClassifyHTTPStatus(status)
	code := apiErr.Code
	if status == 429 && strings.Contains(code, "insufficient_quota") {
		class = aiprovider.ErrClassConfig
	}
	if status == 400 {
		if strings.Contains(code, "model_not_found") || apiErr.Param == "model" {
			class = aiprovider.ErrClassConfig
		} else {
			class = aiprovider.ErrClassPermanent
		}
	}
	return &aiprovider.ProviderError{
		Class:      class,
		Provider:   aiprovider.ProviderOpenAI,
		HTTPStatus: status,
		Code:       code,
		Message:    apiErr.Message,
	}
}
