package projectstore

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CurrentSettingsVersion はアプリ設定の形式バージョン（新旧の版をまたいで読み書きできるように持つ）。
// 初版 "1.0" → "1.1"（theme を追加）→ "1.2"（ai_timeouts を追加）
// → "1.3"（sync_credentials を追加。recent_projects の要素拡張も同版）
// → "1.4"（intro_shown を追加）
// → "1.5"（pane_widths を追加。3 ペインの画面の左右の幅）
// → "1.6"（recent_projects の要素へ project_id を追加）
// → "1.7"（providers[] へ auth_method を追加）。いずれも minor 増分。
const CurrentSettingsVersion = "1.7"

// 画面テーマ（settings.json の theme。既定はダーク）。
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// providerIDPattern は settings.json の provider に受理する ID の形。
//
// **本パッケージで対応プロバイダを列挙しない**。対応の可否を決めるのは
// アダプタのレジストリ（aiprovider.RegisteredProviders）と設定画面のプロバイダ一覧定義
// （binding の providerOptions / isKnownProvider）であり、ここで 3 社を固定列挙すると
// プロバイダを 1 社足すたびに、アダプタと一覧定義以外（本パッケージ）まで変更することになる。
// アプリ設定の読み書きでは形式だけを検査し、未登録プロバイダはアダプタ生成時に
// 「対応していない AI プロバイダです」で弾く（aiprovider.NewAdapterWithOptions）。
var providerIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// エフォート段階（settings.json の effort。既定は標準）。
const (
	EffortLow      = "low"
	EffortStandard = "standard"
	EffortHigh     = "high"
)

// 認証方式（settings.json の auth_method。値の正本は aiprovider.AuthMethod）。
//
// **本パッケージは aiprovider へ依存しない**（保存層は抽象化層の下に居る）ため、
// 同じ 2 値をここにも置く。食い違いは binding 側のテストが機械検知する。
const (
	AuthMethodSecretKey     = "secret_key"
	AuthMethodChatGPTSignin = "chatgpt_signin"
)

// ProviderSetting は 1 プロバイダぶんの設定。
// キー本体は保持しない（OS セキュアストレージのエントリ参照名のみ。設定ファイルから鍵が漏れないように）。
type ProviderSetting struct {
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	// AuthMethod は認証方式（形式 1.7 で追加。任意。**未設定は secret_key** とみなす
	// ＝ 1.6 以前の設定をそのまま読める）。保存層はこの 2 値のいずれかであることだけを検査し、
	// プロバイダとの組み合わせの可否は設定画面のプロバイダ一覧定義が判定する。
	AuthMethod string `json:"auth_method,omitempty"`
	// KeyRef は OS セキュアストレージのエントリ参照名。
	// **chatgpt_signin の要素は持たない**（認証情報は Codex が保管し、参照名も持たない）。
	KeyRef string `json:"key_ref,omitempty"`
}

// AuthMethodOrDefault は認証方式を返す（未設定はシークレットキー方式）。
func (p ProviderSetting) AuthMethodOrDefault() string {
	if p.AuthMethod == "" {
		return AuthMethodSecretKey
	}
	return p.AuthMethod
}

// AITimeouts は AI 呼び出しのタイムアウト（settings.json の ai_timeouts）。
//
// 秒で保持する。0（未設定）・許容範囲外の値は利用時に許容範囲へ丸める
// （範囲・既定値の正本は aiprovider.Timeouts.Normalize）。
// 設定ファイルの手編集で不正値が入っても動作を止めないため、Validate では弾かない。
type AITimeouts struct {
	ConnectSeconds  int `json:"connect_seconds,omitempty"`
	ResponseSeconds int `json:"response_seconds,omitempty"`
}

// PaneWidths は 3 ペインの画面（文書・承認系）の左右のペインの幅（px。settings.json の pane_widths）。
//
// 端末ごとの好みであり、プロジェクトデータへは書かない（表示設定のみ = theme と同じ扱い）。
// 0（未設定）・許容範囲外の値は利用時に丸める（範囲は下の MinPaneWidth / MaxPaneWidth）。
// 設定ファイルの手編集で不正値が入っても動作を止めないため、Validate では弾かない。
type PaneWidths struct {
	// Versions は左（対象・版の一覧）、Checks は右（チェック結果・操作）の幅。
	Versions int `json:"versions,omitempty"`
	Checks   int `json:"checks,omitempty"`
}

// RecentProject は最近開いたプロジェクト 1 件（settings.json の `recent_projects` の要素）。
//
// **変更要約を最後に確認した取り込みの位置**を端末ごとに持ち、プロジェクトデータへは書かない
// （位置の意味は同期モジュールが定める端末内の識別子であり、利用者へは出さない）。
type RecentProject struct {
	// Path はプロジェクトフォルダの絶対パス（アプリ設定は端末ごとで移動しないため絶対パス可）。
	Path string `json:"path"`
	// ProjectID はプロジェクトの識別子（`project.yaml` の `project_id`）。
	//
	// **一覧の追跡をパスだけに頼らないため**に持つ。利用者がフォルダの名前を変えると
	// パスは実体を指さなくなるが、識別子は変わらない（「フォルダ名は識別の
	// 根拠にしない」を一覧側でも満たす）。空 = 古い形式で記録されたもの。
	ProjectID string `json:"project_id,omitempty"`
	// AcknowledgedIncorporation は変更要約を最後に確認した取り込みの位置。空 = 未確認。
	AcknowledgedIncorporation string `json:"acknowledged_incorporation,omitempty"`
	// AcknowledgedAt は確認した日時（UTC）。ゼロ値 = 未確認。
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
}

// UnmarshalJSON は 1.2 以前の文字列形式（パスのみ）も受け付ける（古い版が書いた設定を読めるように）。
func (r *RecentProject) UnmarshalJSON(data []byte) error {
	var path string
	if err := json.Unmarshal(data, &path); err == nil {
		*r = RecentProject{Path: path}
		return nil
	}
	type alias RecentProject
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("recent_projects の要素を解釈できません: %w", err)
	}
	*r = RecentProject(out)
	return nil
}

// Settings は settings.json（アプリ設定）。
type Settings struct {
	SettingsVersion string            `json:"settings_version"`
	AuthorID        string            `json:"author_id,omitempty"`
	DisplayName     string            `json:"display_name,omitempty"`
	Providers       []ProviderSetting `json:"providers"`
	DefaultProvider string            `json:"default_provider"`
	// Theme は画面テーマ（"" = 未設定 = 既定のダーク）。
	// アプリ設定は OS アカウントごとの領域にあるため、保持は端末ごとになる。
	Theme string `json:"theme,omitempty"`
	// AITimeouts は AI 呼び出しのタイムアウト。nil = 未設定（既定値で動作）。
	AITimeouts *AITimeouts `json:"ai_timeouts,omitempty"`
	// PaneWidths は 3 ペインの左右の幅。nil = 未設定（既定幅で表示）。
	PaneWidths *PaneWidths `json:"pane_widths,omitempty"`
	// RecentProjects は最近開いたプロジェクト。
	// 各要素は絶対パスに加えて「変更要約を最後に確認した取り込みの位置」を持つ。
	// 1.2 以前の `["<パス>", ...]` 形式も読める（RecentProject.UnmarshalJSON）。
	RecentProjects []RecentProject `json:"recent_projects,omitempty"`
	// IntroShown は紹介スライドの初回の自動表示を終えたかどうか。
	// false（未設定を含む）= まだ表示していない。初期設定が未完了の通常起動で自動表示する。
	// 設定画面からの再表示では変更しない。端末ごとの保持でプロジェクトデータへは書かない。
	IntroShown bool `json:"intro_shown,omitempty"`
	// SyncCredentials は同期先の認証情報への参照。
	// project_id → OS セキュアストレージの参照名（`<project_id>/<認証方式>` = keymanager.SyncRef）。
	// **認証情報の値は保持しない**（設定ファイルから漏れないように）。端末ごとに持ち、同期の対象に含めない。
	SyncCredentials map[string]string `json:"sync_credentials,omitempty"`

	// unknown は自版が知らないフィールド。書き戻しで削除しない（新しい版が足した項目を古い版が消さないように）。
	unknown map[string]json.RawMessage
}

var knownSettingsFields = map[string]bool{
	"settings_version": true, "author_id": true, "display_name": true,
	"providers": true, "default_provider": true, "theme": true,
	"ai_timeouts": true, "recent_projects": true, "sync_credentials": true,
	"intro_shown": true, "pane_widths": true,
}

// maxSyncCredentialKindLen は参照名の認証方式部の長さの上限。認証情報の値（トークン・鍵）を
// 誤って参照名の位置へ書く実装ミスを検知するための閾値（方式名は "ssh_key" / "token" 程度）。
const maxSyncCredentialKindLen = 32

// settingsMu は同一プロセス内の settings.json 書き込みを直列化する
// （プロジェクトデータの保存キューに相当する役割をアプリ設定側で担う）。
var settingsMu sync.Mutex

// NewSettings は初期設定前の既定値を返す。
func NewSettings() *Settings {
	return &Settings{SettingsVersion: CurrentSettingsVersion, Providers: []ProviderSetting{}}
}

// LoadSettings はアプリ設定を読む。ファイルが無い場合は既定値を返す（初回起動 = 初期設定へ誘導）。
func LoadSettings(p AppPaths) (*Settings, error) {
	data, err := os.ReadFile(p.SettingsFile())
	if os.IsNotExist(err) {
		return NewSettings(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("アプリ設定を読み込めません（%s）: %w", p.SettingsFile(), err)
	}
	return UnmarshalSettings(data)
}

// UnmarshalSettings は settings.json のバイト列を解釈する。未知フィールドは保持する。
func UnmarshalSettings(data []byte) (*Settings, error) {
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("アプリ設定を解釈できません: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("アプリ設定を解釈できません: %w", err)
	}
	for k, v := range raw {
		if knownSettingsFields[k] {
			continue
		}
		if s.unknown == nil {
			s.unknown = map[string]json.RawMessage{}
		}
		s.unknown[k] = v
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Marshal は settings.json のバイト列を組み立てる（未知フィールドを復元する）。
func (s *Settings) Marshal() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	known, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("アプリ設定を組み立てられません: %w", err)
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(known, &merged); err != nil {
		return nil, fmt.Errorf("アプリ設定を組み立てられません: %w", err)
	}
	for k, v := range s.unknown {
		merged[k] = v
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("アプリ設定を組み立てられません: %w", err)
	}
	return append(out, '\n'), nil
}

// SaveSettings はアプリ設定を原子的に書き込む（プロジェクトデータの保存と同じ方式）。
//
// 自版より新しいメジャー形式の設定は書き換えない（新しい版の設定を古い版が壊さないように）。
// 自版より古い形式は自版へ更新して保存する（アプリ設定は再作成可能なため、
// プロジェクトデータのような移行前退避は伴わない）。
func SaveSettings(p AppPaths, s *Settings) error {
	compat, err := s.VersionCompatibility()
	if err != nil {
		return err
	}
	if compat == CompatTooNew {
		found, _ := ParseFormatVersion(s.SettingsVersion)
		current, _ := ParseFormatVersion(CurrentSettingsVersion)
		return &ErrTooNew{Found: found, Current: current}
	}
	if compat == CompatNeedsMigration {
		s.SettingsVersion = CurrentSettingsVersion
	}
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.SettingsFile()), dataDirMode); err != nil {
		return fmt.Errorf("アプリ設定の保存先を作成できません: %w", err)
	}
	return WriteFileAtomic(p.SettingsFile(), data)
}

// VersionCompatibility は settings_version と自版との関係を返す。
func (s *Settings) VersionCompatibility() (Compatibility, error) {
	v, err := ParseFormatVersion(s.SettingsVersion)
	if err != nil {
		return 0, fmt.Errorf("settings.json の settings_version が不正です: %w", err)
	}
	cur, err := ParseFormatVersion(CurrentSettingsVersion)
	if err != nil {
		return 0, err
	}
	switch {
	case v.Major > cur.Major:
		return CompatTooNew, nil
	case v.Major < cur.Major, v.Major == cur.Major && v.Minor < cur.Minor:
		return CompatNeedsMigration, nil
	case v.Minor > cur.Minor:
		return CompatNewerMinor, nil
	default:
		return CompatSame, nil
	}
}

// Validate は settings.json の値集合・整合を検証する。
func (s *Settings) Validate() error {
	if _, err := ParseFormatVersion(s.SettingsVersion); err != nil {
		return fmt.Errorf("settings.json の settings_version が不正です: %w", err)
	}
	if s.AuthorID != "" {
		normalized, err := NormalizeAuthorID(s.AuthorID)
		if err != nil {
			return fmt.Errorf("settings.json の author_id が不正です: %w", err)
		}
		if normalized != s.AuthorID {
			return fmt.Errorf("settings.json の author_id が正規化されていません: %q（正: %q）", s.AuthorID, normalized)
		}
	}
	switch s.Theme {
	case "", ThemeDark, ThemeLight:
	default:
		return fmt.Errorf("settings.json の theme が %q / %q 以外です: %q", ThemeDark, ThemeLight, s.Theme)
	}
	labels := map[string]bool{}
	for i, p := range s.Providers {
		if p.Label == "" {
			return fmt.Errorf("settings.json の providers[%d] に表示名（label）がありません", i)
		}
		if labels[p.Label] {
			return fmt.Errorf("settings.json の providers に同じ表示名が重複しています: %q", p.Label)
		}
		labels[p.Label] = true
		if p.Provider == "" {
			return fmt.Errorf("settings.json の %q にプロバイダがありません", p.Label)
		}
		if !providerIDPattern.MatchString(p.Provider) {
			return fmt.Errorf("settings.json の %q のプロバイダ ID の形式が不正です: %q（英小文字・数字・ハイフンのみ）", p.Label, p.Provider)
		}
		switch p.Effort {
		case EffortLow, EffortStandard, EffortHigh:
		default:
			return fmt.Errorf("settings.json の %q のエフォートが %q / %q / %q 以外です: %q",
				p.Label, EffortLow, EffortStandard, EffortHigh, p.Effort)
		}
		if p.Model == "" {
			return fmt.Errorf("settings.json の %q にモデルがありません", p.Label)
		}
		if strings.Contains(strings.ToLower(p.KeyRef), "sk-") {
			// キー本体を key_ref に入れる実装ミスを検知する（設定ファイルに鍵を残さない）
			return fmt.Errorf("settings.json の %q の key_ref がキー本体のように見えます（参照名のみを保持すること）", p.Label)
		}
		switch p.AuthMethod {
		case "", AuthMethodSecretKey:
		case AuthMethodChatGPTSignin:
			// サインイン方式の要素は key_ref を持たない（認証情報は Codex が保管する）。
			if p.KeyRef != "" {
				return fmt.Errorf("settings.json の %q は %q のため key_ref を持てません（認証情報は AI プロバイダ側が保管します）",
					p.Label, AuthMethodChatGPTSignin)
			}
		default:
			return fmt.Errorf("settings.json の %q の認証方式が %q / %q 以外です: %q",
				p.Label, AuthMethodSecretKey, AuthMethodChatGPTSignin, p.AuthMethod)
		}
	}
	for projectID, ref := range s.SyncCredentials {
		if err := validateSyncCredentialRef(projectID, ref); err != nil {
			return err
		}
	}
	if len(s.Providers) == 0 {
		if s.DefaultProvider != "" {
			return fmt.Errorf("settings.json に providers が無いのに default_provider が設定されています")
		}
		return nil
	}
	if s.DefaultProvider == "" {
		return fmt.Errorf("settings.json の default_provider がありません")
	}
	if !labels[s.DefaultProvider] {
		return fmt.Errorf("settings.json の default_provider が providers にありません: %q", s.DefaultProvider)
	}
	return nil
}

// IsInitialSetupComplete は初期設定が完了しているかを返す。
// 利用者 ID の登録とプロバイダ設定の両方が揃っていることを条件とする（初期設定の画面の手順と同じ）。
func (s *Settings) IsInitialSetupComplete() bool {
	return s.AuthorID != "" && s.DefaultProvider != "" && len(s.Providers) > 0
}

// DefaultProviderSetting は既定のプロバイダ設定を返す。
func (s *Settings) DefaultProviderSetting() (ProviderSetting, bool) {
	for _, p := range s.Providers {
		if p.Label == s.DefaultProvider {
			return p, true
		}
	}
	return ProviderSetting{}, false
}

// RegisterAuthor は初期設定で利用者 ID と表示名を登録する。
// 利用者 ID は登録後に本人が変更できない（訂正はプロジェクト側のオーナー操作）。
func (s *Settings) RegisterAuthor(authorID, displayName string) error {
	normalized, err := NormalizeAuthorID(authorID)
	if err != nil {
		return err
	}
	if s.AuthorID != "" && s.AuthorID != normalized {
		return fmt.Errorf("メールアドレス（利用者 ID）は登録後に変更できません（訂正はプロジェクトのオーナーが行います）")
	}
	if strings.TrimSpace(displayName) == "" {
		return fmt.Errorf("表示名を入力してください")
	}
	s.AuthorID = normalized
	s.DisplayName = strings.TrimSpace(displayName)
	return nil
}

// SetTheme は画面テーマを設定する（端末ごとの保持）。
func (s *Settings) SetTheme(theme string) error {
	switch theme {
	case ThemeDark, ThemeLight:
		s.Theme = theme
		return nil
	default:
		return fmt.Errorf("テーマはダークかライトから選んでください。")
	}
}

// ThemeOrDefault は保存済みのテーマを返す（未設定は既定のダーク）。
func (s *Settings) ThemeOrDefault() string {
	if s.Theme == ThemeLight {
		return ThemeLight
	}
	return ThemeDark
}

// 3 ペインの幅の既定値と許容範囲（px）。
//
// 既定は左 220 / 右 360（右は候補の本文を読む場所のため広い）。
// 許容範囲は「中央の作業領域が潰れない」ことを担保するための下限・上限であり、
// 画面側でも同じ値を使う（範囲を UI に二重に持たせない）。
const (
	DefaultVersionsPaneWidth = 220
	DefaultChecksPaneWidth   = 360
	MinPaneWidth             = 160
	MaxPaneWidth             = 640
)

// PaneWidthsOrDefault は保存済みの幅を許容範囲へ丸めて返す（未設定・範囲外は既定値）。
func (s *Settings) PaneWidthsOrDefault() PaneWidths {
	out := PaneWidths{Versions: DefaultVersionsPaneWidth, Checks: DefaultChecksPaneWidth}
	if s.PaneWidths == nil {
		return out
	}
	if v := s.PaneWidths.Versions; v >= MinPaneWidth && v <= MaxPaneWidth {
		out.Versions = v
	}
	if c := s.PaneWidths.Checks; c >= MinPaneWidth && c <= MaxPaneWidth {
		out.Checks = c
	}
	return out
}

// SetDisplayName は表示名のみを変更する（利用者 ID は変更しない）。
func (s *Settings) SetDisplayName(displayName string) error {
	if strings.TrimSpace(displayName) == "" {
		return fmt.Errorf("表示名を入力してください")
	}
	s.DisplayName = strings.TrimSpace(displayName)
	return nil
}

// AddRecentProject は最近開いたプロジェクトを先頭へ積む（端末ごとの設定のため絶対パス可）。
//
// 既に積まれているプロジェクトは**確認済みの位置を保ったまま**先頭へ移す（開き直しで要約が巻き戻らない）。
func (s *Settings) AddRecentProject(path string, max int) {
	abs := absPath(path)
	head := RecentProject{Path: abs, ProjectID: projectIDOf(abs)}
	if existing, ok := s.RecentProject(abs); ok {
		head = existing
		// 古い形式で記録されたもの（識別子なし）は、ここで補う。
		if head.ProjectID == "" {
			head.ProjectID = projectIDOf(abs)
		}
	}
	out := []RecentProject{head}
	for _, p := range s.RecentProjects {
		if p.Path == abs {
			continue
		}
		out = append(out, p)
		if max > 0 && len(out) >= max {
			break
		}
	}
	s.RecentProjects = out
}

// RecentProjectPaths は最近開いたプロジェクトのパスだけを順に返す。
func (s *Settings) RecentProjectPaths() []string {
	out := make([]string, 0, len(s.RecentProjects))
	for _, p := range s.RecentProjects {
		out = append(out, ResolveProjectPath(p.Path, p.ProjectID))
	}
	return out
}

// ReplaceRecentProject は一覧の 1 件のパスを差し替える（移行でフォルダ名が変わったとき）。
//
// 開いた日時などの付随情報は保つ（名前が変わっただけで履歴が消えないようにする）。
func (s *Settings) ReplaceRecentProject(oldPath, newPath string) {
	from, to := absPath(oldPath), absPath(newPath)
	for i := range s.RecentProjects {
		if s.RecentProjects[i].Path == from {
			s.RecentProjects[i].Path = to
			return
		}
	}
}

// ResolveProjectPath は記録されたパスから、実在するプロジェクトフォルダを解決する
// （移行でフォルダ名が変わった場合と、利用者がフォルダ名を変えた場合に一覧から消えないように）。
//
// 探す順:
//
//  1. 記録どおりの場所（あればそれ）
//  2. `.reqweave` を付けた名前（移行が「改名 → 設定の更新」の途中で落ちた場合）
//  3. **同じ親フォルダの中で `project_id` が一致するもの**（利用者がフォルダ名を変えた場合）
//
// 3 が要るのは、プロジェクトを 1 個のファイルに見せた結果、
// **Finder で名前を変えるのが当然の操作になった**ため。パスだけで追うと一覧から消えて
// 「無くなった」ように見える。識別の根拠は `project_id` であり、
// 一覧の追跡もそれに合わせる。
//
// projectID が空（古い形式の記録）のときは 3 を行わない（当てずっぽうで別のプロジェクトを
// 指さないため）。
func ResolveProjectPath(path, projectID string) string {
	if path == "" {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if !strings.HasSuffix(path, ProjectFolderExt) {
		if withExt := path + ProjectFolderExt; IsProjectFolder(withExt) {
			return withExt
		}
	}
	if found, ok := findProjectByID(filepath.Dir(path), projectID); ok {
		return found
	}
	return path
}

// findProjectByID は parent の直下から project_id が一致するプロジェクトを探す。
//
// 直下だけを見る（深く掘ると、退避や複製を誤って掴む）。
func findProjectByID(parent, projectID string) (string, bool) {
	if projectID == "" || parent == "" {
		return "", false
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(parent, e.Name())
		data, err := os.ReadFile(filepath.Join(candidate, FileProject))
		if err != nil {
			continue
		}
		project, err := UnmarshalProject(data)
		if err != nil {
			continue
		}
		if project.ProjectID == projectID {
			return candidate, true
		}
	}
	return "", false
}

// RecentProject はパスに対応する要素を返す（無ければ ok = false）。
func (s *Settings) RecentProject(path string) (RecentProject, bool) {
	abs := absPath(path)
	for _, p := range s.RecentProjects {
		if p.Path == abs {
			return p, true
		}
	}
	return RecentProject{}, false
}

// AcknowledgeIncorporation は変更要約を確認した取り込みの位置を記録する。
//
// 一覧に無いプロジェクトのときは要素を追加する（開いているプロジェクトは必ず一覧にあるが、
// 取りこぼしても位置を失わないようにする）。
func (s *Settings) AcknowledgeIncorporation(path, incorporation string, at time.Time) {
	abs := absPath(path)
	for i := range s.RecentProjects {
		if s.RecentProjects[i].Path == abs {
			s.RecentProjects[i].AcknowledgedIncorporation = incorporation
			s.RecentProjects[i].AcknowledgedAt = at.UTC()
			return
		}
	}
	s.RecentProjects = append(s.RecentProjects, RecentProject{
		Path: abs, AcknowledgedIncorporation: incorporation, AcknowledgedAt: at.UTC()})
}

// RemoveRecentProject は一覧から取り除く（確認済みの位置も一緒に消える）。
func (s *Settings) RemoveRecentProject(path string) {
	abs := absPath(path)
	out := make([]RecentProject, 0, len(s.RecentProjects))
	for _, p := range s.RecentProjects {
		if p.Path != abs {
			out = append(out, p)
		}
	}
	s.RecentProjects = out
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// validateSyncCredentialRef は sync_credentials の 1 要素が「参照名」の形式であることを検証する。
// 値（トークン・鍵）を書いてしまう実装ミスを settings.json の読み書き時点で止める（設定ファイルに認証情報を残さない）。
func validateSyncCredentialRef(projectID, ref string) error {
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("settings.json の sync_credentials にプロジェクト ID の無い要素があります")
	}
	kind, ok := strings.CutPrefix(ref, projectID+"/")
	if !ok || kind == "" || len(kind) > maxSyncCredentialKindLen || strings.ContainsAny(kind, "/ \t\r\n") {
		return fmt.Errorf("settings.json の sync_credentials[%q] が参照名の形式（<プロジェクト ID>/<認証方式>）ではありません（認証情報の値は保持しないこと）", projectID)
	}
	return nil
}

// SyncCredentialRef はプロジェクトの同期先の認証情報の参照名を返す。未登録なら ok = false。
func (s *Settings) SyncCredentialRef(projectID string) (string, bool) {
	ref, ok := s.SyncCredentials[projectID]
	return ref, ok && ref != ""
}

// SetSyncCredentialRef はプロジェクトの同期先の認証情報の参照名を登録する（値は受け取らない）。
func (s *Settings) SetSyncCredentialRef(projectID, ref string) error {
	if err := validateSyncCredentialRef(projectID, ref); err != nil {
		return err
	}
	if s.SyncCredentials == nil {
		s.SyncCredentials = map[string]string{}
	}
	s.SyncCredentials[projectID] = ref
	return nil
}

// RemoveSyncCredentialRef はプロジェクトの参照名を外す。未登録でも何もしない。
func (s *Settings) RemoveSyncCredentialRef(projectID string) {
	delete(s.SyncCredentials, projectID)
	if len(s.SyncCredentials) == 0 {
		s.SyncCredentials = nil
	}
}

// Author は登録済みの作業者を返す。未登録の場合は ok = false
// （作業者を確認できないと共有プロジェクトを開けない）。
func (s *Settings) Author() (Author, bool) {
	if s.AuthorID == "" {
		return Author{}, false
	}
	name := s.DisplayName
	if name == "" {
		name = s.AuthorID
	}
	return Author{AuthorID: s.AuthorID, DisplayName: name}, true
}

// CurrentOSUser は正規化済みの OS ログオンユーザー名を返す。
// macOS = 短縮名の小文字化 / Windows = `ドメイン\ユーザー名` の小文字化。
// 変更履歴の author-binding イベントに記録する値。
func CurrentOSUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("作業者を確認できないため共有プロジェクトを開けません: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(u.Username))
	if name == "" {
		return "", fmt.Errorf("作業者を確認できないため共有プロジェクトを開けません（OS ユーザー名が空です）")
	}
	return name, nil
}

// SuggestedDisplayName は表示名の既定値（OS のフルネーム）を返す。
// 取得できない場合は空文字を返す（取得できるかどうかを識別の成立条件にしない）。
func SuggestedDisplayName() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	return strings.TrimSpace(u.Username)
}

// projectIDOf はプロジェクトフォルダの識別子を読む（読めなければ空）。
//
// 一覧の追跡をパスだけに頼らないために使う。読めなくても一覧への追加は妨げない。
func projectIDOf(root string) string {
	data, err := os.ReadFile(filepath.Join(root, FileProject))
	if err != nil {
		return ""
	}
	project, err := UnmarshalProject(data)
	if err != nil {
		return ""
	}
	return project.ProjectID
}

// ResolveRecentProjects は一覧の各件を実在する場所へ解決し、変わったものがあれば true を返す。
//
// 利用者がフォルダ名を変えた場合に、**次からは探し直さずに済むよう記録を直す**。
// 併せて、識別子を持たない古い記録に識別子を補う。
func (s *Settings) ResolveRecentProjects() bool {
	changed := false
	for i := range s.RecentProjects {
		resolved := ResolveProjectPath(s.RecentProjects[i].Path, s.RecentProjects[i].ProjectID)
		if resolved != s.RecentProjects[i].Path {
			s.RecentProjects[i].Path = resolved
			changed = true
		}
		if s.RecentProjects[i].ProjectID == "" {
			if id := projectIDOf(s.RecentProjects[i].Path); id != "" {
				s.RecentProjects[i].ProjectID = id
				changed = true
			}
		}
	}
	return changed
}

// ForgetRecentProject は一覧から 1 件を取り除く（**データは消さない**）。
//
// 実体が見つからなくなったものを一覧から外すために使う。
// 「削除」は実体を消す操作であり、実体が無いと実行できないため、外す手段を別に持つ。
func (s *Settings) ForgetRecentProject(path string) bool {
	abs := absPath(path)
	out := make([]RecentProject, 0, len(s.RecentProjects))
	removed := false
	for _, p := range s.RecentProjects {
		if p.Path == abs {
			removed = true
			continue
		}
		out = append(out, p)
	}
	s.RecentProjects = out
	return removed
}
