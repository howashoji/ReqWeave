// Package google は Google（Gemini API）向けの ProviderAdapter 実装。
// 個別プロバイダ SDK の import は本パッケージ内に閉じ込める（依存規則。lint で機械検知）。
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "google.golang.org/genai"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// defaultBaseURL は公式 API エンドポイント（設定による書き換え手段は設けない）。
// 空文字を渡すと SDK の既定（公式エンドポイント）が使われるが、明示して固定する。
const defaultBaseURL = "https://generativelanguage.googleapis.com/"

// Adapter は Google のアダプタ。キーは呼び出しの都度取得し、フィールドに保持しない。
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
	aiprovider.Register(aiprovider.ProviderGoogle, func(keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) aiprovider.Adapter {
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
func (a *Adapter) ID() aiprovider.ProviderID { return aiprovider.ProviderGoogle }

// client は呼び出しごとにキーを取得してクライアントを作る。
// キーは x-goog-api-key ヘッダで送られる（SDK 既定）。クエリパラメータ方式は使わない（キーを URL に載せるとログに残りうるため）。
func (a *Adapter) client(ctx context.Context) (*sdk.Client, error) {
	key, err := a.keys.SecretKey(ctx, a.ref)
	if err != nil {
		return nil, err
	}
	client, err := sdk.NewClient(ctx, &sdk.ClientConfig{
		APIKey:      key,
		Backend:     sdk.BackendGeminiAPI,
		HTTPClient:  a.httpClient,
		HTTPOptions: sdk.HTTPOptions{BaseURL: a.baseURL},
	})
	if err != nil {
		return nil, aiprovider.NetworkError(aiprovider.ProviderGoogle, err)
	}
	return client, nil
}

// ListModels はモデル一覧を取得し、既知一覧で補完して返す。
// Google は応答が inputTokenLimit / outputTokenLimit を含むため、API 応答値を優先する。
func (a *Adapter) ListModels(ctx context.Context) ([]aiprovider.ModelInfo, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var out []aiprovider.ModelInfo
	config := &sdk.ListModelsConfig{}
	for {
		page, err := client.Models.List(ctx, config)
		if err != nil {
			return nil, normalizeError(err)
		}
		for _, m := range page.Items {
			if m == nil {
				continue
			}
			out = append(out, aiprovider.ModelInfo{
				ID:            strings.TrimPrefix(m.Name, "models/"),
				DisplayName:   m.DisplayName,
				ContextWindow: int(m.InputTokenLimit),
				MaxOutput:     int(m.OutputTokenLimit),
			})
		}
		if page.NextPageToken == "" {
			break
		}
		config.PageToken = page.NextPageToken
	}
	return aiprovider.MergeWithKnown(aiprovider.ProviderGoogle, out), nil
}

// VerifyKey は疎通確認。
func (a *Adapter) VerifyKey(ctx context.Context) error {
	client, err := a.client(ctx)
	if err != nil {
		return err
	}
	model, ok := aiprovider.DefaultModelFor(aiprovider.ProviderGoogle, aiprovider.TierLight)
	if !ok {
		return fmt.Errorf("既知モデル一覧に Google の既定モデルがありません")
	}
	if _, err := client.Models.Get(ctx, model.ID, nil); err != nil {
		return normalizeError(err)
	}
	return nil
}

// normalizeError は SDK 固有エラーを ProviderError へ正規化する。
//
// Google API 標準の status（RESOURCE_EXHAUSTED = 一時的 / PERMISSION_DENIED・UNAUTHENTICATED = 設定起因）を見る。
func normalizeError(err error) error {
	var apiErr sdk.APIError
	if !errors.As(err, &apiErr) {
		return aiprovider.NetworkError(aiprovider.ProviderGoogle, err)
	}
	class := aiprovider.ClassifyHTTPStatus(apiErr.Code)
	switch apiErr.Status {
	case "RESOURCE_EXHAUSTED", "UNAVAILABLE", "DEADLINE_EXCEEDED":
		class = aiprovider.ErrClassTransient
	case "PERMISSION_DENIED", "UNAUTHENTICATED", "NOT_FOUND":
		class = aiprovider.ErrClassConfig
	case "INVALID_ARGUMENT":
		class = aiprovider.ErrClassPermanent
	}
	return &aiprovider.ProviderError{
		Class:      class,
		Provider:   aiprovider.ProviderGoogle,
		HTTPStatus: apiErr.Code,
		Code:       apiErr.Status,
		Message:    apiErr.Message,
	}
}
