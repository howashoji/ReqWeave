package codex

// 本ファイルは ProviderAdapter の実装。
//
// 上位（対話エンジン・ドキュメント生成）からは他のプロバイダと区別されない。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/appversion"
)

// clientInfo（`initialize` で渡す名前。Codex はこれを originator と User-Agent に載せる。実測）。
const (
	clientName  = "reqweave"
	clientTitle = "ReqWeave"
)

func clientVersion() string { return appversion.Version() }

func defaultEnviron() []string { return os.Environ() }

// Adapter は Codex App Server のアダプタ。
//
// キーは呼び出しの都度取得し、フィールドに保持しない。
// 子プロセスの起動時に標準入力から渡した後も本システム側は保持しない。
type Adapter struct {
	keys aiprovider.KeyProvider
	ref  aiprovider.KeyRef
	// auth は認証方式（アプリ設定の auth_method。未設定はシークレットキー方式）。
	auth     authMethod
	timeouts aiprovider.Timeouts
	onPanic  aiprovider.PanicRecorder
	rec      aiprovider.EventRecorder
	// verifyBaseURL は疎通確認（2 段目）の呼び出し先。パッケージ内のテストのみが差し替える。
	verifyBaseURL string
	httpClient    *http.Client
}

func init() {
	aiprovider.Register(ProviderCodex, func(keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
		opts aiprovider.AdapterOptions) aiprovider.Adapter {
		return NewWithOptions(keys, ref, opts)
	})
	// 子プロセスと一時領域の後片づけ。呼ぶのは公開バインディング層。
	aiprovider.RegisterLifecycle(ProviderCodex, aiprovider.LifecycleHooks{
		OnAppStart:        removeLeftoverWorkspace,
		OnAppStop:         func() error { return procManager.stopCurrent("本システムが終了するため") },
		OnSettingsChanged: func() error { return procManager.stopCurrent("AIプロバイダの設定が変わったため") },
	})
}

// New はアダプタを既定のタイムアウトで生成する。
func New(keys aiprovider.KeyProvider, ref aiprovider.KeyRef) *Adapter {
	return NewWithOptions(keys, ref, aiprovider.AdapterOptions{})
}

// NewWithOptions はアダプタをタイムアウト・認証方式の指定つきで生成する。
func NewWithOptions(keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) *Adapter {
	auth := authSecretKey
	if aiprovider.NormalizeAuthMethod(opts.AuthMethod) == aiprovider.AuthChatGPTSignin {
		auth = authChatGPTSignin
	}
	return &Adapter{
		keys:          keys,
		ref:           ref,
		auth:          auth,
		timeouts:      opts.Timeouts.Normalize(),
		onPanic:       opts.OnPanic,
		rec:           opts.OnEvent,
		verifyBaseURL: baseURLSecretKey,
		httpClient:    aiprovider.NewHTTPClient(opts.Timeouts),
	}
}

// ID はプロバイダ ID を返す。
func (a *Adapter) ID() aiprovider.ProviderID { return ProviderCodex }

// processKey は使い回してよい子プロセスの条件（認証方式・キーへの参照名）。
//
// **サインイン方式ではキーへの参照名を持たない**（アプリ設定も key_ref を持たない）。
// 認証方式が変われば別の子プロセスになる（保存先の設定が違うため）。
func (a *Adapter) processKey() processKey {
	if a.auth == authChatGPTSignin {
		return processKey{auth: authChatGPTSignin}
	}
	return processKey{auth: authSecretKey, keyRef: a.ref}
}

// ListModels はモデル一覧を返す。
//
// 手元のモデル定義を渡しているため、Codex はサーバへモデル一覧を取りに行かない
// （`model/list` は手元の定義を返す。実測）。
func (a *Adapter) ListModels(ctx context.Context) ([]aiprovider.ModelInfo, error) {
	connectCtx, cancel := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancel()

	proc, release, err := procManager.begin(connectCtx, a.processKey(), a.keys, a.rec)
	if err != nil {
		return nil, err
	}
	defer release()

	raw, err := proc.rpc.call(connectCtx, "model/list", map[string]any{"includeHidden": false})
	if err != nil {
		return nil, toProviderError(proc.callError(err))
	}
	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, permanentError(codeUnexpected, "Codex のモデル一覧を解釈できません")
	}
	models := make([]aiprovider.ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, aiprovider.ModelInfo{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			// コンテキスト長は手元のモデル定義の値。
			ContextWindow: catalogContextWindow(m.ID),
		})
	}
	return aiprovider.MergeWithKnown(ProviderCodex, models), nil
}

// VerifyKey は疎通確認。**2 段**で行う。
//
//  1. 子プロセスの起動と起動後の検査に合格すること
//     （設定画面では成功したのに最初の対話で codex_guard_failed で止まる状態を作らないため）
//  2. 本アダプタから OpenAI の API のモデル一覧を呼んでキーが通ること
//     （Codex に疎通確認の手段がなく、メッセージの送信はトークンを消費するため）
//
// 確認に使った子プロセスは、保存前のキーで起動している場合があるため必ず止める。
//
// **ChatGPT のアカウントでのサインインでは 1 段目だけ**とし、`account/read` で
// サインイン済みであることを確かめる（サインインの成功とアカウントの情報の取得を
// もって疎通確認の成功とする）。保存前のキーを試す場面が無いため、
// 確認用の使い捨ての子プロセスは起こさない。
func (a *Adapter) VerifyKey(ctx context.Context) error {
	if a.auth == authChatGPTSignin {
		return a.verifySignedIn(ctx)
	}
	connectCtx, cancel := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancel()

	key := a.processKey()
	key.verification = true
	proc, release, err := procManager.begin(connectCtx, key, a.keys, a.rec)
	if err != nil {
		// キーの取得の失敗（未設定など）はそのまま返す（呼び出し側が「キー未設定」として扱う）。
		return err
	}
	release()
	defer proc.stop("疎通確認が終わったため")

	return a.verifyKeyOverHTTP(ctx)
}

// verifySignedIn はサインイン済みであることを疎通確認とする。
func (a *Adapter) verifySignedIn(ctx context.Context) error {
	account, err := a.Account(ctx)
	if err != nil {
		return err
	}
	if !account.SignedIn {
		return configError(codeUnauthorized, "ChatGPT のアカウントにサインインしていません")
	}
	return nil
}

// verifyKeyOverHTTP は OpenAI の API のモデル一覧でキーが通るかを確かめる（トークンを消費しない）。
func (a *Adapter) verifyKeyOverHTTP(ctx context.Context) error {
	if a.keys == nil {
		return permanentError(codeGuardFailed, "キーの取得先が設定されていません")
	}
	secret, err := a.keys.SecretKey(ctx, a.ref)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.verifyBaseURL+"/models", nil)
	if err != nil {
		return transientError(codeProcessExited, "疎通確認の要求を組み立てられません")
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return aiprovider.NetworkError(ProviderCodex, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return &aiprovider.ProviderError{
		Class:      aiprovider.ClassifyHTTPStatus(resp.StatusCode),
		Provider:   ProviderCodex,
		HTTPStatus: resp.StatusCode,
		Code:       "unauthorized",
		Message:    fmt.Sprintf("キーが受け付けられませんでした（HTTP %d）", resp.StatusCode),
	}
}

func asProviderError(err error, target **aiprovider.ProviderError) bool {
	return errors.As(err, target)
}

func asRPCError(err error, target **rpcError) bool { return errors.As(err, target) }

func isRPCClosed(err error) bool { return errors.Is(err, errRPCClosed) }
