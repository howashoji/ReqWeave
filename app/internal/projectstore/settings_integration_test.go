//go:build integration

package projectstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testPaths(t *testing.T) AppPaths {
	t.Helper()
	return AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
}

// 未作成のアプリ設定は既定値として読める（初回起動 → 初期設定へ誘導）。
func TestLoadSettingsWhenMissing(t *testing.T) {
	s, err := LoadSettings(testPaths(t))
	if err != nil {
		t.Fatalf("既定値を読めません: %v", err)
	}
	if s.IsInitialSetupComplete() {
		t.Error("未設定なのに初期設定完了と判定された")
	}
	if s.SettingsVersion != CurrentSettingsVersion {
		t.Errorf("既定の settings_version が違う: %q", s.SettingsVersion)
	}
}

// アプリ設定の往復。キー本体を書き込まない。
func TestSaveAndLoadSettings(t *testing.T) {
	p := testPaths(t)
	s := NewSettings()
	if err := s.RegisterAuthor("K.Sato@Example.co.jp", "佐藤"); err != nil {
		t.Fatal(err)
	}
	s.Providers = []ProviderSetting{{
		Label: "社内 Claude", Provider: "anthropic", Model: "claude-x",
		Effort: EffortStandard, KeyRef: "reqweave/anthropic/1",
	}}
	s.DefaultProvider = "社内 Claude"
	s.AddRecentProject(filepath.Join(t.TempDir(), "proj"), 10)

	if err := SaveSettings(p, s); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	raw, err := os.ReadFile(p.SettingsFile())
	if err != nil {
		t.Fatalf("保存されていない: %v", err)
	}
	for _, forbidden := range []string{"sk-ant-", "sk-", "AIza"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("アプリ設定にキーらしき文字列が含まれる（%q）:\n%s", forbidden, raw)
		}
	}

	got, err := LoadSettings(p)
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	if got.AuthorID != "k.sato@example.co.jp" || got.DisplayName != "佐藤" {
		t.Errorf("作業者が往復しない: %+v", got)
	}
	if !got.IsInitialSetupComplete() {
		t.Error("初期設定完了と判定されない")
	}
	ps, ok := got.DefaultProviderSetting()
	if !ok || ps.Model != "claude-x" || ps.KeyRef != "reqweave/anthropic/1" {
		t.Errorf("プロバイダ設定が往復しない: %+v", ps)
	}
	if len(got.RecentProjects) != 1 {
		t.Errorf("最近開いた一覧が往復しない: %v", got.RecentProjects)
	}
}

// 保存は原子的（プロジェクトデータと同じ方式 = 一時ファイル → rename）。
func TestSaveSettingsIsAtomic(t *testing.T) {
	p := testPaths(t)
	s := NewSettings()
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAuthor("k.sato@example.co.jp", "佐藤"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("settings.json を直接上書きしている（一時ファイル → rename になっていない）")
	}
	for _, name := range mustReadDir(t, p.Base) {
		if strings.HasPrefix(name, ".") {
			t.Errorf("一時ファイルが残っている: %s", name)
		}
	}
}

// 自版より新しいメジャー形式の設定は書き換えない。
func TestSaveSettingsRefusesTooNew(t *testing.T) {
	p := testPaths(t)
	s := NewSettings()
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	s.SettingsVersion = "2.0"
	err = SaveSettings(p, s)
	if err == nil {
		t.Fatal("新しいメジャー形式の設定が書き込まれた")
	}
	var tooNew *ErrTooNew
	if !asErrTooNew(err, &tooNew) {
		t.Fatalf("ErrTooNew ではない: %T %v", err, err)
	}
	after, err := os.ReadFile(p.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("書き込みを拒否したのにファイルが変わった")
	}
}

// OS 標準のアプリ設定領域を使う。
func TestDefaultAppPaths(t *testing.T) {
	p, err := DefaultAppPaths()
	if err != nil {
		t.Fatalf("既定の保存先を特定できません: %v", err)
	}
	if !strings.HasSuffix(p.Base, AppID) {
		t.Errorf("保存先が AppID で終わっていない: %q", p.Base)
	}
	if !filepath.IsAbs(p.Base) {
		t.Errorf("保存先が絶対パスでない: %q", p.Base)
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(p.Base, filepath.Join("Library", "Application Support")) {
			t.Errorf("macOS の保存先が Application Support でない: %q", p.Base)
		}
	case "windows":
		if !strings.Contains(strings.ToLower(p.Base), "appdata") {
			t.Errorf("Windows の保存先が APPDATA でない: %q", p.Base)
		}
	}
}

// OS ログオンユーザーは正規化（小文字化）して扱う。
func TestCurrentOSUserIsNormalized(t *testing.T) {
	got, err := CurrentOSUser()
	if err != nil {
		t.Fatalf("OS ユーザーを取得できません: %v", err)
	}
	if got == "" {
		t.Fatal("OS ユーザー名が空")
	}
	if got != strings.ToLower(got) {
		t.Errorf("小文字化されていない: %q", got)
	}
	if strings.TrimSpace(got) != got {
		t.Errorf("前後に空白が残っている: %q", got)
	}
}
