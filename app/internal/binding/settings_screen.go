package binding

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ProviderConfigView は設定画面に出す 1 プロバイダ設定。
// キー本体・キー参照名の中身は載せない（キーは画面へ渡さず OS のセキュアストレージにだけ置く）。
type ProviderConfigView struct {
	Label         string `json:"label"`
	ProviderID    string `json:"providerId"`
	ProviderLabel string `json:"providerLabel"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
	EffortLabel   string `json:"effortLabel"`
	// AuthMethod は認証方式（未設定はシークレットキー方式）。
	AuthMethod string `json:"authMethod"`
	// AuthMethodLabel は認証方式のラベル（生のコード値を画面へ出さない）。
	AuthMethodLabel string `json:"authMethodLabel"`
	// KeyState は「登録済み」/「未設定」。キーは常にマスク表示で、平文の再表示機能を持たない。
	// **サインイン方式ではキーを持たない**ため「—」を返す（設定画面はサインインの状態を別に出す）。
	KeyState  string `json:"keyState"`
	KeyMasked string `json:"keyMasked"`
	IsDefault bool   `json:"isDefault"`
	PolicyURL string `json:"policyUrl"`
	// Notice はプロバイダ固有の注意喚起（ProviderOption.Notice と同じ内容。空なら表示しない）。
	Notice string `json:"notice,omitempty"`
}

// SettingsView は設定画面の表示内容。
type SettingsView struct {
	// AuthorID は表示のみ（本人は変更できない。訂正はオーナーが行う）。
	AuthorID         string               `json:"authorId"`
	DisplayName      string               `json:"displayName"`
	Providers        []ProviderConfigView `json:"providers"`
	Options          []ProviderOption     `json:"options"`
	Efforts          []EffortOption       `json:"efforts"`
	DataPolicyNotice string               `json:"dataPolicyNotice"`
	// AIReady は AI 呼び出しを伴う機能を使えるか。
	AIReady bool `json:"aiReady"`
	// AIBlockedReason は使えない理由と次の行動（設定画面への誘導）。
	AIBlockedReason string `json:"aiBlockedReason,omitempty"`
	// Theme は画面テーマ（端末ごとに保持する）。
	Theme string `json:"theme"`
	// AITimeouts は AI 呼び出しのタイムアウト（端末ごとに保持する）。
	AITimeouts AITimeoutsView `json:"aiTimeouts"`
}

// AITimeoutsView は AI 呼び出しタイムアウトの現在値と許容範囲。
//
// 許容範囲を画面へ返すことで、範囲の値を UI 側に二重に持たせない。
type AITimeoutsView struct {
	ConnectSeconds     int `json:"connectSeconds"`
	ResponseSeconds    int `json:"responseSeconds"`
	MinConnectSeconds  int `json:"minConnectSeconds"`
	MaxConnectSeconds  int `json:"maxConnectSeconds"`
	MinResponseSeconds int `json:"minResponseSeconds"`
	MaxResponseSeconds int `json:"maxResponseSeconds"`
}

// UpdateProviderRequest は設定変更の入力。
type UpdateProviderRequest struct {
	Label      string `json:"label"`
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
	// AuthMethod は認証方式（空 = シークレットキー方式）。
	AuthMethod  string `json:"authMethod,omitempty"`
	MakeDefault bool   `json:"makeDefault"`
}

// SettingsView は現在の設定を返す。
func (a *API) SettingsView() (SettingsView, error) {
	settings, err := a.settings()
	if err != nil {
		return SettingsView{}, err
	}
	view := SettingsView{
		AuthorID:         settings.AuthorID,
		DisplayName:      settings.DisplayName,
		Options:          availableProviderOptions(runtime.GOOS),
		Efforts:          effortOptions(),
		DataPolicyNotice: dataPolicyNotice,
	}
	for _, p := range settings.Providers {
		authMethod := p.AuthMethodOrDefault()
		// サインイン方式はキーを持たない。キーの登録状態は問い合わせない。
		registered := false
		if !usesSignIn(authMethod) {
			if ref, err := keyRef(p.Provider, p.Label); err == nil {
				registered, _ = a.keys.Exists(ref)
			}
		}
		item := ProviderConfigView{
			Label:           p.Label,
			ProviderID:      p.Provider,
			ProviderLabel:   providerDisplayName(p.Provider),
			Model:           p.Model,
			Effort:          p.Effort,
			EffortLabel:     effortLabel(p.Effort),
			AuthMethod:      authMethod,
			AuthMethodLabel: authMethodLabel(p.Provider, authMethod),
			KeyState:        keyStateLabel(registered),
			KeyMasked:       keyMaskedText(registered),
			IsDefault:       p.Label == settings.DefaultProvider,
			PolicyURL:       providerPolicyURL(p.Provider),
			Notice:          providerNotice(p.Provider),
		}
		if usesSignIn(authMethod) {
			item.KeyState, item.KeyMasked = "—", "—"
		}
		view.Providers = append(view.Providers, item)
	}
	ready, reason := a.aiReadiness(settings)
	view.AIReady = ready
	view.AIBlockedReason = reason
	view.Theme = settings.ThemeOrDefault()
	view.AITimeouts = aiTimeoutsView(settings)
	return view, nil
}

// aiTimeoutsView は保存値（未設定・範囲外は許容範囲へ丸めた値）と許容範囲を返す。
func aiTimeoutsView(settings *projectstore.Settings) AITimeoutsView {
	var stored aiprovider.Timeouts
	if settings.AITimeouts != nil {
		stored = aiprovider.Timeouts{
			Connect:  time.Duration(settings.AITimeouts.ConnectSeconds) * time.Second,
			Response: time.Duration(settings.AITimeouts.ResponseSeconds) * time.Second,
		}
	}
	effective := stored.Normalize()
	return AITimeoutsView{
		ConnectSeconds:     int(effective.Connect / time.Second),
		ResponseSeconds:    int(effective.Response / time.Second),
		MinConnectSeconds:  int(aiprovider.MinConnectTimeout / time.Second),
		MaxConnectSeconds:  int(aiprovider.MaxConnectTimeout / time.Second),
		MinResponseSeconds: int(aiprovider.MinResponseTimeout / time.Second),
		MaxResponseSeconds: int(aiprovider.MaxResponseTimeout / time.Second),
	}
}

// PaneWidthsView は 3 ペインの幅の現在値と許容範囲。
//
// 許容範囲を画面へ返すことで、範囲の値を UI 側に二重に持たせない（AITimeoutsView と同じ方針）。
type PaneWidthsView struct {
	Versions int `json:"versions"`
	Checks   int `json:"checks"`
	Min      int `json:"min"`
	Max      int `json:"max"`
}

// PaneWidths は保存済みの 3 ペインの幅を返す（未設定・範囲外は既定値へ丸める）。
//
// 画面の組み立て時に呼ぶため、プロジェクトを開く前・初期設定の完了前でも呼べる。
func (a *API) PaneWidths() (PaneWidthsView, error) {
	view := PaneWidthsView{
		Versions: projectstore.DefaultVersionsPaneWidth,
		Checks:   projectstore.DefaultChecksPaneWidth,
		Min:      projectstore.MinPaneWidth,
		Max:      projectstore.MaxPaneWidth,
	}
	settings, err := a.settings()
	if err != nil {
		// 設定を読めなくても既定幅で表示できる（表示設定のため機能は止めない）。
		return view, nil
	}
	w := settings.PaneWidthsOrDefault()
	view.Versions, view.Checks = w.Versions, w.Checks
	return view, nil
}

// SetPaneWidths は 3 ペインの幅を端末ごとのアプリ設定へ保存する。
//
// 範囲外は保存せず、許容範囲を添えた 1 文で拒否する（丸めて黙って別の値を保存しない = SetAITimeouts と同じ）。
func (a *API) SetPaneWidths(versions, checks int) error {
	min, max := projectstore.MinPaneWidth, projectstore.MaxPaneWidth
	if versions < min || versions > max || checks < min || checks > max {
		return fmt.Errorf("ペインの幅が範囲外です。%d〜%d の範囲で指定してください。", min, max)
	}
	settings, err := a.settings()
	if err != nil {
		return err
	}
	settings.PaneWidths = &projectstore.PaneWidths{Versions: versions, Checks: checks}
	return projectstore.SaveSettings(a.paths, settings)
}

// SetAITimeouts は AI 呼び出しのタイムアウトを端末ごとのアプリ設定へ保存する。
//
// 範囲外は保存せず、許容範囲を添えた 1 文で拒否する（丸めて黙って別の値を保存しない）。
func (a *API) SetAITimeouts(connectSeconds, responseSeconds int) error {
	min, max := int(aiprovider.MinConnectTimeout/time.Second), int(aiprovider.MaxConnectTimeout/time.Second)
	if connectSeconds < min || connectSeconds > max {
		return fmt.Errorf("接続の待ち時間が範囲外です。%d〜%d 秒で指定してください。", min, max)
	}
	min, max = int(aiprovider.MinResponseTimeout/time.Second), int(aiprovider.MaxResponseTimeout/time.Second)
	if responseSeconds < min || responseSeconds > max {
		return fmt.Errorf("応答の待ち時間が範囲外です。%d〜%d 秒で指定してください。", min, max)
	}
	settings, err := a.settings()
	if err != nil {
		return err
	}
	settings.AITimeouts = &projectstore.AITimeouts{
		ConnectSeconds:  connectSeconds,
		ResponseSeconds: responseSeconds,
	}
	return projectstore.SaveSettings(a.paths, settings)
}

// Theme は保存済みの画面テーマを返す（未設定は既定のダーク）。
// 起動直後の適用に使うため、初期設定の完了前でも呼べる。
func (a *API) Theme() (string, error) {
	settings, err := a.settings()
	if err != nil {
		return projectstore.ThemeDark, err
	}
	return settings.ThemeOrDefault(), nil
}

// SetTheme は画面テーマを端末ごとのアプリ設定へ保存する。
func (a *API) SetTheme(theme string) error {
	settings, err := a.settings()
	if err != nil {
		return err
	}
	if err := settings.SetTheme(theme); err != nil {
		return err
	}
	return projectstore.SaveSettings(a.paths, settings)
}

// IntroShown は紹介スライドの初回の自動表示を終えたかどうかを返す。
// 起動直後の表示判定に使うため、初期設定の完了前でも呼べる。
// 設定を読めないときは true を返す（案内の表示に失敗して起動を妨げない）。
func (a *API) IntroShown() (bool, error) {
	settings, err := a.settings()
	if err != nil {
		return true, err
	}
	return settings.IntroShown, nil
}

// MarkIntroShown は紹介スライドの初回の自動表示を終えたことを記録する（アプリ設定の intro_shown）。
// 最後まで送った場合と飛ばした場合のいずれからも呼ぶ。
// 設定画面からの再表示では呼ばない（記録を変えない）。
func (a *API) MarkIntroShown() error {
	settings, err := a.settings()
	if err != nil {
		return err
	}
	if settings.IntroShown {
		return nil
	}
	settings.IntroShown = true
	return projectstore.SaveSettings(a.paths, settings)
}

// AIReady は AI 呼び出しを伴う機能の可否を返す（未設定なら AI 機能を止める判定）。
func (a *API) AIReady() (SettingsView, error) {
	settings, err := a.settings()
	if err != nil {
		return SettingsView{}, err
	}
	ready, reason := a.aiReadiness(settings)
	return SettingsView{AIReady: ready, AIBlockedReason: reason}, nil
}

// aiReadiness は既定プロバイダのキーが登録済みかで判定する。
// キーは疎通確認に成功したときだけ保存する（RegisterKey）ため、登録済み = 確認済みとして扱う。
//
// **サインイン方式ではサインイン済みを「キー登録済み」と同等に扱う**。
// サインアウト済みと分かっているときだけブロックし、
// 起動直後などの「分からない」状態は使える前提とする（失効は AI 呼び出しのエラーで気づく）。
func (a *API) aiReadiness(settings *projectstore.Settings) (bool, string) {
	p, ok := settings.DefaultProviderSetting()
	if !ok {
		return false, "AI プロバイダが未設定です。設定でプロバイダとキーを登録してください。"
	}
	if usesSignIn(p.AuthMethodOrDefault()) {
		// **そのプロバイダがサインイン方式を提供している場合に限る**。
		// 保存層は組み合わせの可否を持たないため、設定を手で書き換えると
		// サインインを提供しないプロバイダに chatgpt_signin が入りうる。その設定で
		// 「使える」と答えると、キーが無いまま操作を始めさせてしまう。
		if !supportsAuthMethod(p.Provider, p.AuthMethodOrDefault()) {
			return false, "AI プロバイダの設定が不正です。設定で登録し直してください。"
		}
		if a.signedOutKnown() {
			return false, "ChatGPT のアカウントにサインインしていません。設定でサインインしてください。"
		}
		return true, ""
	}
	ref, err := keyRef(p.Provider, p.Label)
	if err != nil {
		return false, "AI プロバイダの設定が不正です。設定で登録し直してください。"
	}
	registered, err := a.keys.Exists(ref)
	if err != nil {
		return false, "キーの状態を確認できません。設定で登録し直してください。"
	}
	if !registered {
		return false, "キーが未登録です。設定でキーを登録してください。"
	}
	return true, ""
}

// SaveProviderConfig はプロバイダ設定（モデル・エフォート・既定）を保存する。
//
// プロバイダ自体の変更・キーの変更は RegisterKey（保存前に疎通確認）を経て行う。
// キーが未登録の設定を既定にはできない（未設定なら AI 機能を止める判定と揃える）。
func (a *API) SaveProviderConfig(req UpdateProviderRequest) error {
	if !isKnownProvider(req.ProviderID) {
		return fmt.Errorf("対応していない AI プロバイダです。一覧から選んでください。")
	}
	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("モデルを選んでください。")
	}
	if !isKnownEffort(req.Effort) {
		return fmt.Errorf("エフォートを選んでください。")
	}
	authMethod := normalizedAuthMethod(req.AuthMethod)
	if !supportsAuthMethod(req.ProviderID, authMethod) {
		return fmt.Errorf("この AI プロバイダでは選べない認証方式です。一覧から選んでください。")
	}
	ref, err := keyRef(req.ProviderID, req.Label)
	if err != nil {
		return err
	}
	// サインイン方式はキーを持たない。既定にできる条件はサインイン済みであること。
	keyRefValue := ref.String()
	if usesSignIn(authMethod) {
		keyRefValue = ""
		if req.MakeDefault {
			if err := a.requireSignedIn(); err != nil {
				return err
			}
		}
	} else {
		registered, err := a.keys.Exists(ref)
		if err != nil {
			return err
		}
		if !registered && req.MakeDefault {
			return fmt.Errorf("キーが未登録のため既定にできません。先にキーを登録してください。")
		}
	}

	settings, err := a.settings()
	if err != nil {
		return err
	}
	settings.Providers = upsertProvider(settings.Providers, projectstore.ProviderSetting{
		Label:      ref.Label,
		Provider:   req.ProviderID,
		Model:      req.Model,
		Effort:     req.Effort,
		AuthMethod: authMethod,
		KeyRef:     keyRefValue,
	})
	if req.MakeDefault || settings.DefaultProvider == "" {
		settings.DefaultProvider = ref.Label
	}
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		return err
	}
	a.aiSettingsChanged() // 子プロセス型アダプタへ設定変更を伝える（設定の変更は子プロセスを止める時機の 1 つ）
	return nil
}

// SetDisplayName は表示名を変更する（利用者 ID は変更しない）。
func (a *API) SetDisplayName(name string) error {
	settings, err := a.settings()
	if err != nil {
		return err
	}
	if err := settings.SetDisplayName(name); err != nil {
		return err
	}
	return projectstore.SaveSettings(a.paths, settings)
}

func providerDisplayName(id string) string {
	for _, p := range providerOptions {
		if p.ID == id {
			return p.DisplayName
		}
	}
	return "対応外のプロバイダ"
}

func providerPolicyURL(id string) string {
	for _, p := range providerOptions {
		if p.ID == id {
			return p.PolicyURL
		}
	}
	return ""
}

// authMethodLabel は認証方式のラベル（プロバイダ一覧定義が正本。未知は「不明」にして生のコード値を出さない）。
func authMethodLabel(providerID, method string) string {
	method = normalizedAuthMethod(method)
	for _, p := range providerOptions {
		if p.ID != providerID {
			continue
		}
		for _, m := range p.AuthMethods {
			if m.ID == method {
				return m.Label
			}
		}
	}
	if method == string(aiprovider.AuthSecretKey) {
		// 認証方式の選択を持たないプロバイダはシークレットキー方式のみ（画面には出さない）。
		return "シークレットキー方式"
	}
	return labelUnknown
}

// providerNotice はプロバイダ固有の注意喚起（プロバイダ自身が送信に付け足す内容の開示など）。
func providerNotice(id string) string {
	for _, p := range providerOptions {
		if p.ID == id {
			return p.Notice
		}
	}
	return ""
}

func effortLabel(effort string) string {
	switch effort {
	case string(aiprovider.EffortLow):
		return "低"
	case string(aiprovider.EffortStandard):
		return "標準"
	case string(aiprovider.EffortHigh):
		return "高"
	default:
		return "設定なし"
	}
}

func isKnownEffort(effort string) bool {
	switch effort {
	case string(aiprovider.EffortLow), string(aiprovider.EffortStandard), string(aiprovider.EffortHigh):
		return true
	default:
		return false
	}
}

// keyStateLabel / keyMaskedText はキーの状態表示（平文の再表示機能を持たない）。
func keyStateLabel(registered bool) string {
	if registered {
		return "登録済み"
	}
	return "未設定"
}

func keyMaskedText(registered bool) string {
	if registered {
		return "••••••••"
	}
	return "—"
}
