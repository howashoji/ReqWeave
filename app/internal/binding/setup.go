package binding

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	_ "github.com/howashoji/ReqWeave/app/internal/aiprovider/adapters" // 3社アダプタの登録
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// dataPolicyNotice は初期設定・プロバイダ変更時の注意喚起（初期設定画面と設定画面で表示する）。
const dataPolicyNotice = "送信内容は選択したプロバイダのデータ利用ポリシーに従って扱われます。" +
	"機密情報を外部の AI へ送ってよいかの組織ポリシー確認は利用者の責務です。"

// ProviderOption はプロバイダの選択肢（Anthropic・OpenAI・Google の 3 社と Codex App Server）。
type ProviderOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	// PolicyURL はデータ利用ポリシーの参照先。
	PolicyURL string `json:"policyUrl"`
	// Notice はそのプロバイダ固有の注意喚起（空なら表示しない）。
	// 共通の注意喚起（dataPolicyNotice）に足して表示する。
	Notice string `json:"notice,omitempty"`
	// AuthMethods は選べる認証方式（**2 つ持つのは codex だけ**）。
	// 1 件しか無いプロバイダでは認証方式の選択を画面に出さない。
	AuthMethods []AuthMethodOption `json:"authMethods,omitempty"`
	// macOSOnly は macOS 版でだけ選べるプロバイダか（画面へは出さない内部の条件）。
	macOSOnly bool
}

// AuthMethodOption は認証方式の選択肢。
type AuthMethodOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Description は方式ごとのポリシーと注意喚起（空なら表示しない）。
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

// codexAuthMethods は Codex App Server の認証方式。
var codexAuthMethods = []AuthMethodOption{
	{
		ID: string(aiprovider.AuthSecretKey), Label: "シークレットキー方式", Default: true,
		Description: "OpenAI のシークレットキーを登録して使います。キーは OS のセキュアストレージに保管されます。",
	},
	{
		ID: string(aiprovider.AuthChatGPTSignin), Label: "ChatGPT のアカウントでのサインイン",
		// 動作ログに呼び出し先の応答のクッキーが残ること・一時領域にあり消えることの開示。
		Description: "ブラウザで OpenAI のサインインのページを開いて許可します。" +
			"認証情報は Codex App Server が OS のセキュアストレージへ保管し、本システムは読み出しません。" +
			"Codex App Server の動作ログには呼び出し先の応答のクッキーが残りますが、" +
			"これは一時領域にあり、本システムの終了時と次回の起動時に消えます。",
	},
}

// providerOptions は対応プロバイダの定義。プロバイダ追加時はここへ 1 行足す。
var providerOptions = []ProviderOption{
	{ID: string(aiprovider.ProviderAnthropic), DisplayName: "Anthropic（Claude）", PolicyURL: "https://www.anthropic.com/legal/privacy"},
	{ID: string(aiprovider.ProviderOpenAI), DisplayName: "OpenAI", PolicyURL: "https://openai.com/policies/api-data-usage-policies/"},
	{ID: string(aiprovider.ProviderGoogle), DisplayName: "Google（Gemini）", PolicyURL: "https://ai.google.dev/gemini-api/terms"},
	{
		ID: "codex", DisplayName: "Codex App Server（OpenAI）",
		PolicyURL: "https://openai.com/policies/api-data-usage-policies/",
		// Codex が送信に付け足す内容の開示。
		Notice: "Codex App Server では、本システムが送る内容に加えて、Codex 自身が" +
			"道具の定義文・利用中の OS の版と CPU の種類・端末ごとの識別子を送ります。",
		// 認証方式を 2 つ持つのは Codex App Server だけ。
		AuthMethods: codexAuthMethods,
		// Windows 版では、Windows 実機での検証が済むまで提供しない。
		macOSOnly: true,
	},
}

// availableProviderOptions は当該 OS で選べるプロバイダを返す（macOS 版だけのプロバイダを他の OS では除く）。
func availableProviderOptions(goos string) []ProviderOption {
	out := make([]ProviderOption, 0, len(providerOptions))
	for _, p := range providerOptions {
		if p.macOSOnly && goos != "darwin" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// EffortOption はエフォート設定の選択肢（既定は標準）。
type EffortOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

// ModelOption はモデル選択の選択肢。
type ModelOption struct {
	ID            string `json:"id"`
	DisplayName   string `json:"displayName"`
	ContextWindow int    `json:"contextWindow"`
	MaxOutput     int    `json:"maxOutput"`
	Tier          string `json:"tier"`
	Recommended   bool   `json:"recommended"`
}

// ModelList はモデル一覧の取得結果。取得に失敗した場合は既知一覧へ縮退する。
type ModelList struct {
	Models []ModelOption `json:"models"`
	// FromKnownList は API 取得に失敗して埋め込みの既知一覧を使ったことを示す（黙って縮退しない）。
	FromKnownList bool   `json:"fromKnownList"`
	Notice        string `json:"notice,omitempty"`
}

// VerifyResult は疎通確認の結果。
type VerifyResult struct {
	OK bool `json:"ok"`
	// Reason は失敗理由の区分（認証エラー / ネットワークエラー / その他）。
	Reason string `json:"reason,omitempty"`
	// Detail は利用者向けの 1 文（原因 + 次の行動）。
	Detail string `json:"detail,omitempty"`
}

// SetupState は初期設定画面の初期表示に必要な状態。
type SetupState struct {
	Complete             bool             `json:"complete"`
	AuthorID             string           `json:"authorId"`
	DisplayName          string           `json:"displayName"`
	SuggestedDisplayName string           `json:"suggestedDisplayName"`
	Providers            []ProviderOption `json:"providers"`
	Efforts              []EffortOption   `json:"efforts"`
	DataPolicyNotice     string           `json:"dataPolicyNotice"`
}

// SetupRequest は初期設定の確定入力（初期設定画面の各ステップで選んだ内容）。
type SetupRequest struct {
	ProviderID string `json:"providerId"`
	Label      string `json:"label"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	// AuthMethod は認証方式（空 = シークレットキー方式）。
	AuthMethod  string `json:"authMethod,omitempty"`
	AuthorID    string `json:"authorId"`
	DisplayName string `json:"displayName"`
}

// supportsAuthMethod はそのプロバイダでその認証方式を選べるかを返す。
//
// 組み合わせの可否を持つのは**設定画面のプロバイダ一覧定義だけ**とする（保存層は列挙しない）。
func supportsAuthMethod(providerID, method string) bool {
	method = normalizedAuthMethod(method)
	for _, p := range providerOptions {
		if p.ID != providerID {
			continue
		}
		if len(p.AuthMethods) == 0 {
			// 認証方式の選択を持たないプロバイダはシークレットキー方式のみ。
			return method == string(aiprovider.AuthSecretKey)
		}
		for _, m := range p.AuthMethods {
			if m.ID == method {
				return true
			}
		}
		return false
	}
	return false
}

// normalizedAuthMethod は未設定をシークレットキー方式へ倒す。
func normalizedAuthMethod(method string) string {
	return string(aiprovider.NormalizeAuthMethod(aiprovider.AuthMethod(method)))
}

// usesSignIn は認証方式が ChatGPT のアカウントでのサインインかを返す。
func usesSignIn(method string) bool {
	return normalizedAuthMethod(method) == string(aiprovider.AuthChatGPTSignin)
}

// SetupState は初期設定の状態を返す。
func (a *API) SetupState() (SetupState, error) {
	settings, err := a.settings()
	if err != nil {
		return SetupState{}, err
	}
	name := settings.DisplayName
	if name == "" {
		name = projectstore.SuggestedDisplayName()
	}
	return SetupState{
		Complete:             settings.IsInitialSetupComplete(),
		AuthorID:             settings.AuthorID,
		DisplayName:          settings.DisplayName,
		SuggestedDisplayName: name,
		Providers:            availableProviderOptions(runtime.GOOS),
		Efforts:              effortOptions(),
		DataPolicyNotice:     dataPolicyNotice,
	}, nil
}

// effortOptions はエフォート 3 段階の選択肢を返す（説明はプロバイダごとの設定値への写像と食い違わないよう aiprovider の文言を使う）。
func effortOptions() []EffortOption {
	return []EffortOption{
		{ID: string(aiprovider.EffortLow), Label: "低", Description: aiprovider.EffortDescription(aiprovider.EffortLow)},
		{ID: string(aiprovider.EffortStandard), Label: "標準", Description: aiprovider.EffortDescription(aiprovider.EffortStandard), Default: true},
		{ID: string(aiprovider.EffortHigh), Label: "高", Description: aiprovider.EffortDescription(aiprovider.EffortHigh)},
	}
}

// RegisterKey は入力されたシークレットキーで疎通確認を行い、**成功した場合のみ**
// OS セキュアストレージへ登録する。
//
// 保存前に確認するため、失敗しても既存の登録（変更前の設定）は一切変わらない。
// キー本体はフロントエンドへ返さない。
func (a *API) RegisterKey(providerID, label, key string) (VerifyResult, error) {
	ref, err := keyRef(providerID, label)
	if err != nil {
		return VerifyResult{}, err
	}
	if strings.TrimSpace(key) == "" {
		return VerifyResult{OK: false, Reason: "キー未設定", Detail: "キーを入力してください。"}, nil
	}
	result, err := a.verifyWith(providerID, ref, staticKey{key: strings.TrimSpace(key)},
		string(aiprovider.AuthSecretKey))
	if err != nil {
		return VerifyResult{}, err
	}
	if !result.OK {
		return result, nil
	}
	if err := a.keys.Register(ref, key); err != nil {
		return VerifyResult{}, err
	}
	a.aiSettingsChanged() // キーの変更を子プロセス型アダプタへ伝える（古い設定の子プロセスを止める）
	return result, nil
}

// DeleteKey は登録済みキーを削除して「キー未設定」へ戻す。
func (a *API) DeleteKey(providerID, label string) error {
	ref, err := keyRef(providerID, label)
	if err != nil {
		return err
	}
	if err := a.keys.Delete(ref); err != nil {
		return err
	}
	a.aiSettingsChanged() // キーの削除を子プロセス型アダプタへ伝える（古い設定の子プロセスを止める）
	return nil
}

// VerifyKey は登録済みキーで疎通確認を行う（設定変更時にも使う）。
func (a *API) VerifyKey(providerID, label string) (VerifyResult, error) {
	ref, err := keyRef(providerID, label)
	if err != nil {
		return VerifyResult{}, err
	}
	return a.verify(providerID, ref)
}

func (a *API) verify(providerID string, ref keymanager.Ref) (VerifyResult, error) {
	return a.verifyWith(providerID, ref, keyProvider{a.keys}, string(aiprovider.AuthSecretKey))
}

// verifyWith は指定のキー取得元・認証方式で疎通確認する（保存前のキー検証にも使う）。
func (a *API) verifyWith(providerID string, ref keymanager.Ref, keys aiprovider.KeyProvider,
	authMethod string) (VerifyResult, error) {

	adapter, err := a.newAdapter(aiprovider.ProviderID(providerID), keys,
		aiprovider.KeyRef(ref.String()), a.adapterOptionsFor(authMethod))
	if err != nil {
		return VerifyResult{}, err
	}
	if err := adapter.VerifyKey(a.context()); err != nil {
		reason, detail := classifyVerifyError(err)
		return VerifyResult{OK: false, Reason: reason, Detail: detail}, nil
	}
	return VerifyResult{OK: true}, nil
}

// classifyVerifyError は疎通確認の失敗を利用者向けの区分と 1 文へ変換する
// （原因 + 次の行動の形。内部用語・生のコード値を出さない）。
func classifyVerifyError(err error) (reason, detail string) {
	if errors.Is(err, keymanager.ErrKeyNotSet) {
		return "キー未設定", "キーが登録されていません。キーを入力して登録してください。"
	}
	var pe *aiprovider.ProviderError
	if !errors.As(err, &pe) {
		return "その他", "疎通確認に失敗しました。時間をおいて再実行してください。"
	}
	switch pe.Class {
	case aiprovider.ErrClassConfig:
		if pe.HTTPStatus == 0 {
			return "その他", "設定を確認してください。プロバイダ・キー・モデルの組み合わせが受け付けられませんでした。"
		}
		return "認証エラー", "キーが受け付けられませんでした。キーを確認して登録し直してください。"
	case aiprovider.ErrClassTransient:
		return "ネットワークエラー", "プロバイダへ接続できませんでした。ネットワークを確認して再実行してください。"
	default:
		return "その他", "疎通確認に失敗しました。設定内容を確認してください。"
	}
}

// Models はモデル一覧を返す。取得できない場合は既知一覧へ縮退する。
//
// authMethod は**画面で選んでいる認証方式**（初期設定では設定がまだ無いため、保存済みの値では
// 足りない）。空のときだけ保存済みの値へ倒す。サインイン方式では
// キーへの参照名を持たないため、生成は providerAdapter に任せる。
func (a *API) Models(providerID, label, authMethod string) (ModelList, error) {
	if strings.TrimSpace(authMethod) == "" {
		authMethod = a.storedAuthMethod(providerID, label)
	}
	id := aiprovider.ProviderID(providerID)
	adapter, err := a.providerAdapter(providerID, label, authMethod)
	if err != nil {
		return ModelList{}, err
	}
	models, err := adapter.ListModels(a.context())
	if err != nil {
		known, knownErr := aiprovider.KnownModels(id)
		if knownErr != nil {
			return ModelList{}, knownErr
		}
		return ModelList{
			Models:        toModelOptions(known),
			FromKnownList: true,
			Notice:        "モデル一覧を取得できなかったため、アプリに登録済みの一覧を表示しています。",
		}, nil
	}
	return ModelList{Models: toModelOptions(models)}, nil
}

func toModelOptions(models []aiprovider.ModelInfo) []ModelOption {
	out := make([]ModelOption, 0, len(models))
	for _, m := range models {
		out = append(out, ModelOption{
			ID:            m.ID,
			DisplayName:   displayNameOrID(m),
			ContextWindow: m.ContextWindow,
			MaxOutput:     m.MaxOutput,
			Tier:          string(m.Tier),
			Recommended:   m.DefaultForTier && m.Tier == aiprovider.TierPrimary,
		})
	}
	return out
}

func displayNameOrID(m aiprovider.ModelInfo) string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.ID
}

// CompleteSetup は初期設定を確定して保存する（プロバイダ・モデル・エフォート・作成者の登録）。
func (a *API) CompleteSetup(req SetupRequest) error {
	if !isKnownProvider(req.ProviderID) {
		return fmt.Errorf("対応していない AI プロバイダです。一覧から選んでください。")
	}
	if req.Model == "" {
		return fmt.Errorf("モデルを選んでください。")
	}
	effort := req.Effort
	if effort == "" {
		effort = string(aiprovider.EffortStandard) // 変更しなかった場合は既定（標準）
	}
	authMethod := normalizedAuthMethod(req.AuthMethod)
	if !supportsAuthMethod(req.ProviderID, authMethod) {
		return fmt.Errorf("この AI プロバイダでは選べない認証方式です。一覧から選んでください。")
	}
	ref, err := keyRef(req.ProviderID, req.Label)
	if err != nil {
		return err
	}
	// サインイン方式はキーを登録しない（認証情報は Codex が保管する）。
	keyRefValue := ref.String()
	if usesSignIn(authMethod) {
		if err := a.requireSignedIn(); err != nil {
			return err
		}
		keyRefValue = ""
	} else {
		exists, err := a.keys.Exists(ref)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("キーが登録されていません。キーを入力して登録してください。")
		}
	}

	settings, err := a.settings()
	if err != nil {
		return err
	}
	if err := settings.RegisterAuthor(req.AuthorID, req.DisplayName); err != nil {
		return err
	}
	settings.Providers = upsertProvider(settings.Providers, projectstore.ProviderSetting{
		Label:      ref.Label,
		Provider:   req.ProviderID,
		Model:      req.Model,
		Effort:     effort,
		AuthMethod: authMethod,
		KeyRef:     keyRefValue,
	})
	settings.DefaultProvider = ref.Label
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		return err
	}
	a.aiSettingsChanged() // 子プロセス型アダプタへ設定変更を伝える（古い設定の子プロセスを止める）
	return nil
}

// upsertProvider は同じ表示名の設定を置き換え、無ければ追加する。
func upsertProvider(list []projectstore.ProviderSetting, p projectstore.ProviderSetting) []projectstore.ProviderSetting {
	for i := range list {
		if list[i].Label == p.Label {
			list[i] = p
			return list
		}
	}
	return append(list, p)
}

func isKnownProvider(id string) bool {
	for _, p := range availableProviderOptions(runtime.GOOS) {
		if p.ID == id {
			return true
		}
	}
	return false
}

// keyRef はプロバイダ ID と参照名からキー参照名を組み立てる。
func keyRef(providerID, label string) (keymanager.Ref, error) {
	if label == "" {
		label = "既定"
	}
	ref := keymanager.Ref{ProviderID: providerID, Label: label}
	if err := ref.Validate(); err != nil {
		return keymanager.Ref{}, err
	}
	return ref, nil
}

// staticKey は保存前のキー検証で使う一時的な取得元。値は 1 回の検証の間だけメモリに載る
// （キーを構造体・パッケージ変数へ保持しない）。
type staticKey struct{ key string }

func (s staticKey) SecretKey(context.Context, aiprovider.KeyRef) (string, error) { return s.key, nil }

// keyProvider は keymanager を抽象化層の KeyProvider へ橋渡しする。
type keyProvider struct{ m *keymanager.Manager }

func (k keyProvider) SecretKey(ctx context.Context, ref aiprovider.KeyRef) (string, error) {
	parsed, err := keymanager.ParseRef(string(ref))
	if err != nil {
		return "", err
	}
	return k.m.SecretKey(ctx, parsed)
}
