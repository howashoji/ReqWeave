package projectstore

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validSettingsJSON = `{
  "settings_version": "1.3",
  "author_id": "k.sato@example.co.jp",
  "display_name": "佐藤",
  "providers": [
    {"label": "社内 Claude", "provider": "anthropic", "model": "claude-x", "effort": "standard", "key_ref": "reqweave/anthropic/1"}
  ],
  "default_provider": "社内 Claude",
  "recent_projects": ["/Users/x/projects/inv"]
}`

func TestUnmarshalSettings(t *testing.T) {
	s, err := UnmarshalSettings([]byte(validSettingsJSON))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if s.AuthorID != "k.sato@example.co.jp" || s.DisplayName != "佐藤" {
		t.Errorf("作業者が読めていない: %+v", s)
	}
	p, ok := s.DefaultProviderSetting()
	if !ok || p.Provider != "anthropic" || p.Effort != EffortStandard || p.KeyRef != "reqweave/anthropic/1" {
		t.Errorf("既定プロバイダが読めていない: %+v %v", p, ok)
	}
	if !s.IsInitialSetupComplete() {
		t.Error("初期設定完了と判定されない")
	}
}

// settings.json の値集合・整合。
func TestSettingsValidateRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"settings_version が不正": strings.Replace(validSettingsJSON, `"1.3"`, `"x"`, 1),
		"theme が値集合外":          strings.Replace(validSettingsJSON, `"display_name": "佐藤",`, `"display_name": "佐藤", "theme": "sepia",`, 1),
		// プロバイダ ID は形式だけを検査する（対応の可否はレジストリと設定画面が持つ）
		"プロバイダ ID の形式が不正":      strings.Replace(validSettingsJSON, `"anthropic"`, `"Bedrock 2"`, 1),
		"プロバイダが空":              strings.Replace(validSettingsJSON, `"provider": "anthropic"`, `"provider": ""`, 1),
		"エフォートが値集合外":           strings.Replace(validSettingsJSON, `"standard"`, `"medium"`, 1),
		"モデルが空":                strings.Replace(validSettingsJSON, `"model": "claude-x"`, `"model": ""`, 1),
		"default_provider が不在": strings.Replace(validSettingsJSON, `"default_provider": "社内 Claude"`, `"default_provider": "別の設定"`, 1),
		"author_id が正規化されていない": strings.Replace(validSettingsJSON, `"k.sato@example.co.jp"`, `"K.Sato@Example.co.jp"`, 1),
		"key_ref がキー本体らしい":     strings.Replace(validSettingsJSON, `"reqweave/anthropic/1"`, `"sk-ant-xxxx"`, 1),
		// sync_credentials は参照名（<project_id>/<認証方式>）のみ。値らしいものは拒否する
		"sync_credentials が値らしい": strings.Replace(validSettingsJSON, `"display_name": "佐藤",`,
			`"display_name": "佐藤", "sync_credentials": {"p1": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},`, 1),
		"sync_credentials の参照名が別プロジェクト": strings.Replace(validSettingsJSON, `"display_name": "佐藤",`,
			`"display_name": "佐藤", "sync_credentials": {"p1": "p2/token"},`, 1),
		"sync_credentials の方式部が長すぎる": strings.Replace(validSettingsJSON, `"display_name": "佐藤",`,
			`"display_name": "佐藤", "sync_credentials": {"p1": "p1/`+strings.Repeat("x", 40)+`"},`, 1),
	}
	for name, js := range cases {
		t.Run(name, func(t *testing.T) {
			if s, err := UnmarshalSettings([]byte(js)); err == nil {
				t.Errorf("不正な settings.json が受理された: %+v", s)
			}
		})
	}
}

func TestSettingsDuplicateLabelRejected(t *testing.T) {
	s := &Settings{
		SettingsVersion: CurrentSettingsVersion,
		Providers: []ProviderSetting{
			{Label: "A", Provider: "openai", Model: "m", Effort: EffortHigh, KeyRef: "r1"},
			{Label: "A", Provider: "google", Model: "m", Effort: EffortLow, KeyRef: "r2"},
		},
		DefaultProvider: "A",
	}
	if err := s.Validate(); err == nil {
		t.Error("表示名が重複した providers が受理された")
	}
}

// プロバイダを 1 社足すときに本パッケージを変更しないこと。
// アプリ設定の検査は ID の形式だけを見る（対応の可否はアダプタのレジストリと設定画面が持つ）。
func TestSettingsAcceptsProviderIDNotImplementedYet(t *testing.T) {
	for _, id := range []string{"codex", "azure-openai", "bedrock"} {
		s := &Settings{
			SettingsVersion: CurrentSettingsVersion,
			Providers: []ProviderSetting{
				{Label: "新プロバイダ", Provider: id, Model: "m", Effort: EffortStandard, KeyRef: "r1"},
			},
			DefaultProvider: "新プロバイダ",
		}
		if err := s.Validate(); err != nil {
			t.Errorf("プロバイダ %q の settings.json が拒否された（projectstore が列挙を持ってしまっている）: %v", id, err)
		}
	}
	for _, id := range []string{"", "Anthropic", "open ai", "openai/gpt", "-openai", "openai-"} {
		s := &Settings{
			SettingsVersion: CurrentSettingsVersion,
			Providers: []ProviderSetting{
				{Label: "不正", Provider: id, Model: "m", Effort: EffortStandard, KeyRef: "r1"},
			},
			DefaultProvider: "不正",
		}
		if err := s.Validate(); err == nil {
			t.Errorf("形式が不正なプロバイダ ID %q が受理された", id)
		}
	}
}

// 初期設定前（providers なし）は既定値として成立する。
func TestNewSettingsIsIncompleteButValid(t *testing.T) {
	s := NewSettings()
	if err := s.Validate(); err != nil {
		t.Errorf("初期状態が不正と判定された: %v", err)
	}
	if s.IsInitialSetupComplete() {
		t.Error("初期状態が「初期設定完了」と判定された")
	}
}

// 未知フィールドを書き戻しで削除しない。
func TestSettingsUnknownFieldsSurviveRoundTrip(t *testing.T) {
	js := strings.Replace(validSettingsJSON, `"settings_version": "1.3"`,
		`"settings_version": "1.8", "future_flag": true`, 1)
	s, err := UnmarshalSettings([]byte(js))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	compat, err := s.VersionCompatibility()
	if err != nil {
		t.Fatal(err)
	}
	if compat != CompatNewerMinor {
		t.Errorf("互換判定が違う: %v", compat)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	if !strings.Contains(string(out), `"future_flag"`) {
		t.Errorf("未知フィールドが失われた:\n%s", out)
	}
}

func TestSettingsVersionCompatibility(t *testing.T) {
	cases := map[string]Compatibility{
		CurrentSettingsVersion: CompatSame,
		"1.0":                  CompatNeedsMigration, // 初版（theme 追加前）
		"1.1":                  CompatNeedsMigration, // ai_timeouts 追加前
		"1.2":                  CompatNeedsMigration, // sync_credentials 追加前
		"1.3":                  CompatNeedsMigration, // intro_shown 追加前
		"1.4":                  CompatNeedsMigration, // pane_widths 追加前
		"1.5":                  CompatNeedsMigration, // recent_projects の project_id 追加前
		"1.6":                  CompatNeedsMigration, // providers[].auth_method 追加前
		"1.8":                  CompatNewerMinor,
		"0.9":                  CompatNeedsMigration,
		"2.0":                  CompatTooNew,
	}
	for v, want := range cases {
		s := &Settings{SettingsVersion: v}
		got, err := s.VersionCompatibility()
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if got != want {
			t.Errorf("%s: got %v, want %v", v, got, want)
		}
	}
}

// 利用者 ID は登録時に正規化し、以後本人は変更できない。
func TestRegisterAuthor(t *testing.T) {
	s := NewSettings()
	if err := s.RegisterAuthor("  K.Sato@Example.CO.JP ", " 佐藤 "); err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if s.AuthorID != "k.sato@example.co.jp" {
		t.Errorf("利用者 ID が正規化されていない: %q", s.AuthorID)
	}
	if s.DisplayName != "佐藤" {
		t.Errorf("表示名が整形されていない: %q", s.DisplayName)
	}
	if err := s.RegisterAuthor("other@example.co.jp", "別人"); err == nil {
		t.Error("本人による利用者 ID の変更が許された")
	}
	if err := s.RegisterAuthor("k.sato@example.co.jp", "佐藤 一郎"); err != nil {
		t.Errorf("同じ利用者 ID の再登録が拒否された: %v", err)
	}
	if err := s.SetDisplayName("  "); err == nil {
		t.Error("空の表示名が受理された")
	}
	if err := s.SetDisplayName("佐藤（営業）"); err != nil {
		t.Fatal(err)
	}
	if s.DisplayName != "佐藤（営業）" || s.AuthorID != "k.sato@example.co.jp" {
		t.Errorf("表示名の変更で利用者 ID が変わった: %+v", s)
	}
	a, ok := s.Author()
	if !ok || a.AuthorID != "k.sato@example.co.jp" || a.DisplayName != "佐藤（営業）" {
		t.Errorf("作業者の解決が違う: %+v %v", a, ok)
	}
	if _, ok := NewSettings().Author(); ok {
		t.Error("未登録なのに作業者が解決された")
	}
	if err := s.RegisterAuthor("ksato", "佐藤"); err == nil {
		t.Error("形式不正の利用者 ID が受理された")
	}
}

func TestAddRecentProject(t *testing.T) {
	s := NewSettings()
	s.AddRecentProject("/a/x", 3)
	s.AddRecentProject("/a/y", 3)
	s.AddRecentProject("/a/x", 3)
	if got := s.RecentProjectPaths(); len(got) != 2 || got[0] != "/a/x" || got[1] != "/a/y" {
		t.Errorf("最近開いた一覧が違う: %v", got)
	}
	for _, p := range []string{"/a/1", "/a/2", "/a/3"} {
		s.AddRecentProject(p, 3)
	}
	if len(s.RecentProjects) != 3 {
		t.Errorf("上限を超えた: %v", s.RecentProjectPaths())
	}
	if s.RecentProjects[0].Path != "/a/3" {
		t.Errorf("先頭が最新でない: %v", s.RecentProjectPaths())
	}
}

// 変更要約を確認した取り込みの位置を要素ごとに持ち、
// 開き直し（先頭への積み直し）で失わない。
func TestRecentProjectAcknowledgedIncorporation(t *testing.T) {
	s := NewSettings()
	s.AddRecentProject("/a/x", 5)
	s.AddRecentProject("/a/y", 5)
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	s.AcknowledgeIncorporation("/a/x", "abc123", at)

	got, ok := s.RecentProject("/a/x")
	if !ok || got.AcknowledgedIncorporation != "abc123" || !got.AcknowledgedAt.Equal(at) {
		t.Fatalf("確認済みの位置が記録されていない: %+v %v", got, ok)
	}
	if other, _ := s.RecentProject("/a/y"); other.AcknowledgedIncorporation != "" {
		t.Errorf("別のプロジェクトに位置が付いた: %+v", other)
	}

	// 開き直しても位置を保つ
	s.AddRecentProject("/a/x", 5)
	if got, _ := s.RecentProject("/a/x"); got.AcknowledgedIncorporation != "abc123" {
		t.Errorf("開き直しで確認済みの位置が失われた: %+v", got)
	}
	// 一覧から取り除くと位置も消える
	s.RemoveRecentProject("/a/x")
	if _, ok := s.RecentProject("/a/x"); ok {
		t.Error("一覧から取り除けていない")
	}
}

// 後方互換: 1.2 以前の recent_projects（文字列の配列）を読める。
func TestRecentProjectsAcceptsLegacyStringForm(t *testing.T) {
	var s Settings
	const legacy = `{"settings_version":"1.2","recent_projects":["/a/x","/a/y"]}`
	if err := json.Unmarshal([]byte(legacy), &s); err != nil {
		t.Fatalf("1.2 以前の設定を読めない: %v", err)
	}
	if got := s.RecentProjectPaths(); len(got) != 2 || got[0] != "/a/x" || got[1] != "/a/y" {
		t.Fatalf("旧形式のパスが失われた: %v", got)
	}
	for _, p := range s.RecentProjects {
		if p.AcknowledgedIncorporation != "" || !p.AcknowledgedAt.IsZero() {
			t.Errorf("旧形式から確認済みの位置が作られた: %+v", p)
		}
	}

	// 新形式（1.3）も読める
	var v13 Settings
	const current = `{"settings_version":"1.3","recent_projects":[` +
		`{"path":"/a/x","acknowledged_incorporation":"abc","acknowledged_at":"2026-09-02T10:00:00Z"}]}`
	if err := json.Unmarshal([]byte(current), &v13); err != nil {
		t.Fatalf("1.3 の設定を読めない: %v", err)
	}
	got, ok := v13.RecentProject("/a/x")
	if !ok || got.AcknowledgedIncorporation != "abc" || got.AcknowledgedAt.IsZero() {
		t.Errorf("新形式の要素を読めていない: %+v %v", got, ok)
	}
}

// アプリ設定領域の配置（設定・自動退避・派生インデックス・動作ログ）。
func TestAppPathsLayout(t *testing.T) {
	p := AppPaths{Base: filepath.Join("base", AppID)}
	cases := map[string]string{
		p.SettingsFile():             filepath.Join("base", AppID, "settings.json"),
		p.BackupsDir():               filepath.Join("base", AppID, "backups"),
		p.ProjectBackupsDir("pid-1"): filepath.Join("base", AppID, "backups", "pid-1"),
		p.CacheDir():                 filepath.Join("base", AppID, "cache"),
		p.LogsDir():                  filepath.Join("base", AppID, "logs"),
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("配置が違う: got %q, want %q", got, want)
		}
	}
}

// テーマは任意・未設定は既定のダーク・値集合は 2 値。
func TestSettingsTheme(t *testing.T) {
	s := NewSettings()
	if s.Theme != "" {
		t.Errorf("初期状態でテーマが入っている: %q", s.Theme)
	}
	if s.ThemeOrDefault() != ThemeDark {
		t.Errorf("未設定の既定がダークでない: %q", s.ThemeOrDefault())
	}
	if err := s.SetTheme(ThemeLight); err != nil {
		t.Fatalf("設定に失敗: %v", err)
	}
	if s.ThemeOrDefault() != ThemeLight {
		t.Errorf("設定が反映されない: %q", s.ThemeOrDefault())
	}
	if err := s.SetTheme("sepia"); err == nil {
		t.Error("値集合外のテーマが受理された")
	}
	if s.Theme != ThemeLight {
		t.Errorf("失敗した設定で値が壊れた: %q", s.Theme)
	}
	// 未入力は書き出しに現れない
	empty := NewSettings()
	out, err := empty.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "theme") {
		t.Errorf("未設定のテーマがキーごと書き出された: %s", out)
	}
}

// ai_timeouts を保持し、書き戻しでも失われない。
func TestSettingsAITimeoutsRoundTrip(t *testing.T) {
	js := strings.Replace(validSettingsJSON, `"display_name": "佐藤",`,
		`"display_name": "佐藤", "ai_timeouts": {"connect_seconds": 30, "response_seconds": 120},`, 1)
	s, err := UnmarshalSettings([]byte(js))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if s.AITimeouts == nil || s.AITimeouts.ConnectSeconds != 30 || s.AITimeouts.ResponseSeconds != 120 {
		t.Fatalf("ai_timeouts が読めていない: %+v", s.AITimeouts)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	if !strings.Contains(string(out), `"connect_seconds"`) {
		t.Errorf("書き戻しで ai_timeouts が失われた:\n%s", out)
	}

	// 未設定のときはキーごと出力しない（既定値で動作する）。
	plain, err := UnmarshalSettings([]byte(validSettingsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if plain.AITimeouts != nil {
		t.Errorf("未設定なのに値が入っている: %+v", plain.AITimeouts)
	}
	out, _ = plain.Marshal()
	if strings.Contains(string(out), "ai_timeouts") {
		t.Errorf("未設定でキーが出力されている:\n%s", out)
	}
}

// sync_credentials は参照名のみを保持し、書き戻しでも失われない。
func TestSettingsSyncCredentialsRoundTrip(t *testing.T) {
	s, err := UnmarshalSettings([]byte(validSettingsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SyncCredentialRef("p1"); ok {
		t.Error("未登録なのに参照名が返った")
	}
	if err := s.SetSyncCredentialRef("p1", "p1/token"); err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if err := s.SetSyncCredentialRef("p2", "p2/ssh_key"); err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	for _, bad := range []string{"", "token", "p2/token", "p1/", "p1/a b"} {
		if err := s.SetSyncCredentialRef("p1", bad); err == nil {
			t.Errorf("形式外の参照名 %q が受理された", bad)
		}
	}
	if ref, ok := s.SyncCredentialRef("p1"); !ok || ref != "p1/token" {
		t.Errorf("失敗した登録で値が壊れた: %q %v", ref, ok)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"sync_credentials"`) || !strings.Contains(string(out), `"p1": "p1/token"`) {
		t.Errorf("書き戻しで sync_credentials が失われた:\n%s", out)
	}
	back, err := UnmarshalSettings(out)
	if err != nil {
		t.Fatal(err)
	}
	if ref, ok := back.SyncCredentialRef("p2"); !ok || ref != "p2/ssh_key" {
		t.Errorf("読み戻した参照名が違う: %q %v", ref, ok)
	}

	s.RemoveSyncCredentialRef("p1")
	s.RemoveSyncCredentialRef("p2")
	s.RemoveSyncCredentialRef("none")
	out, _ = s.Marshal()
	if strings.Contains(string(out), "sync_credentials") {
		t.Errorf("全削除後もキーが出力されている:\n%s", out)
	}
}

// 範囲外の値でも読み込みを止めない（丸めは利用側 = aiprovider の Normalize）。
func TestSettingsAITimeoutsOutOfRangeIsAccepted(t *testing.T) {
	js := strings.Replace(validSettingsJSON, `"display_name": "佐藤",`,
		`"display_name": "佐藤", "ai_timeouts": {"connect_seconds": 99999, "response_seconds": -1},`, 1)
	s, err := UnmarshalSettings([]byte(js))
	if err != nil {
		t.Fatalf("手編集の不正値で読み込みが止まった: %v", err)
	}
	if s.AITimeouts.ConnectSeconds != 99999 {
		t.Errorf("値が書き換えられている: %+v", s.AITimeouts)
	}
}

// intro_shown は既知フィールドとして往復し、
// 未設定は false（＝まだ表示していない）として扱う。
func TestSettingsIntroShownRoundTrip(t *testing.T) {
	s, err := UnmarshalSettings([]byte(validSettingsJSON))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if s.IntroShown {
		t.Error("intro_shown の無い設定が「表示済み」と読まれた")
	}

	s.IntroShown = true
	s.SettingsVersion = CurrentSettingsVersion
	out, err := s.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	if !strings.Contains(string(out), `"intro_shown": true`) {
		t.Errorf("intro_shown が書き出されない:\n%s", out)
	}

	back, err := UnmarshalSettings(out)
	if err != nil {
		t.Fatalf("読み直しに失敗: %v", err)
	}
	if !back.IntroShown {
		t.Error("intro_shown が復元されない")
	}
	// 既知フィールドなので「未知フィールドの保全」経路には入らない（二重出力にならない）
	if strings.Count(string(out), "intro_shown") != 1 {
		t.Errorf("intro_shown が重複して出力された:\n%s", out)
	}
}
