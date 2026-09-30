package projectstore

// アプリ設定 1.7（`providers[].auth_method`）の単体テスト。

import (
	"strings"
	"testing"
)

// アプリ設定の形式バージョンは **1.7**（auth_method の追加による minor 増分）。
//
// 期待値は定数ではなくリテラルで固定する（定数と突き合わせると自己参照になり、
// 版を上げ忘れても緑になる = caveat-testing「既定値を定数と突き合わせると自己参照になる」）。
func TestCurrentSettingsVersionIs17(t *testing.T) {
	if CurrentSettingsVersion != "1.7" {
		t.Errorf("アプリ設定の形式バージョンが違う: %q, want \"1.7\"",
			CurrentSettingsVersion)
	}
}

// **`auth_method` が未設定の 1.6 の設定をそのまま読めて `secret_key` として扱う**。
// 保存すると自版（1.7）へ更新される（アプリ設定は移行前退避を伴わない）。
func TestSettings16WithoutAuthMethodIsReadAsSecretKey(t *testing.T) {
	js := strings.Replace(validSettingsJSON, `"settings_version": "1.3"`, `"settings_version": "1.6"`, 1)
	s, err := UnmarshalSettings([]byte(js))
	if err != nil {
		t.Fatalf("1.6 の設定を読めない: %v", err)
	}
	p, ok := s.DefaultProviderSetting()
	if !ok {
		t.Fatal("既定のプロバイダ設定が読めない")
	}
	if p.AuthMethod != "" {
		t.Errorf("未設定の auth_method が勝手に埋まっている: %q", p.AuthMethod)
	}
	if p.AuthMethodOrDefault() != "secret_key" {
		t.Errorf("未設定が secret_key として扱われない: %q", p.AuthMethodOrDefault())
	}
	if p.KeyRef == "" {
		t.Error("1.6 の key_ref が失われている")
	}
	compat, err := s.VersionCompatibility()
	if err != nil {
		t.Fatal(err)
	}
	if compat != CompatNeedsMigration {
		t.Errorf("1.6 が移行対象と判定されない: %v", compat)
	}

	paths := AppPaths{Base: t.TempDir()}
	if err := SaveSettings(paths, s); err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	saved, err := LoadSettings(paths)
	if err != nil {
		t.Fatalf("読み直しに失敗: %v", err)
	}
	if saved.SettingsVersion != "1.7" {
		t.Errorf("保存後の形式バージョンが違う: %q, want \"1.7\"", saved.SettingsVersion)
	}
}

// `auth_method` が `secret_key` / `chatgpt_signin` 以外の設定は保存層が拒否する。
func TestSettingsRejectInvalidAuthMethod(t *testing.T) {
	js := strings.Replace(validSettingsJSON,
		`"effort": "standard"`, `"effort": "standard", "auth_method": "oauth"`, 1)
	_, err := UnmarshalSettings([]byte(js))
	if err == nil {
		t.Fatal("不正な認証方式が受理された")
	}
	if !strings.Contains(err.Error(), "認証方式") {
		t.Errorf("拒否の理由が認証方式でない: %v", err)
	}
	// 生のコード値を含む設定を保存できないこと（読み込みだけでなく書き込みでも弾く）。
	s, err := UnmarshalSettings([]byte(validSettingsJSON))
	if err != nil {
		t.Fatal(err)
	}
	s.Providers[0].AuthMethod = "oauth"
	if _, err := s.Marshal(); err == nil {
		t.Error("不正な認証方式のまま組み立てられた")
	}
}

// `chatgpt_signin` の要素は `key_ref` を持たない
// （認証情報は Codex が保管し、本システムは参照名も持たない）。
func TestSettingsChatGPTSignInHasNoKeyRef(t *testing.T) {
	js := strings.Replace(validSettingsJSON,
		`"effort": "standard"`, `"effort": "standard", "auth_method": "chatgpt_signin"`, 1)
	if _, err := UnmarshalSettings([]byte(js)); err == nil {
		t.Fatal("key_ref を持つサインイン方式の設定が受理された")
	}

	// key_ref を持たない形は受理し、書き出しにも key_ref が現れない。
	js = strings.Replace(js, `, "key_ref": "reqweave/anthropic/1"`, "", 1)
	js = strings.Replace(js, `"provider": "anthropic"`, `"provider": "codex"`, 1)
	s, err := UnmarshalSettings([]byte(js))
	if err != nil {
		t.Fatalf("サインイン方式の設定を読めない: %v", err)
	}
	if s.Providers[0].AuthMethodOrDefault() != "chatgpt_signin" {
		t.Errorf("認証方式が読めていない: %q", s.Providers[0].AuthMethodOrDefault())
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	if strings.Contains(string(out), "key_ref") {
		t.Errorf("サインイン方式の設定に key_ref が書かれている:\n%s", out)
	}
	if !strings.Contains(string(out), `"auth_method": "chatgpt_signin"`) {
		t.Errorf("認証方式が保存されていない:\n%s", out)
	}
}

// 認証方式の値は保存層と抽象化層で同じ 2 値であること（食い違うと設定を読めなくなる）。
func TestAuthMethodConstantsAreTheTwoValues(t *testing.T) {
	if AuthMethodSecretKey != "secret_key" || AuthMethodChatGPTSignin != "chatgpt_signin" {
		t.Errorf("認証方式の値が違う: %q / %q", AuthMethodSecretKey, AuthMethodChatGPTSignin)
	}
}

// `secret_key` の要素に `key_ref` が無い設定を**保存層は拒否しない**。
//
// 拒否すると、その 1 か所の欠けでアプリ設定ファイル全体が読めなくなり、利用者が設定画面から
// 直せなくなる。欠けは「キー未設定」として扱い、AI 機能のブロックに倒す。
func TestSecretKeySettingWithoutKeyRefIsAccepted(t *testing.T) {
	js := strings.Replace(validSettingsJSON, `"key_ref": "reqweave/anthropic/`, `"unused_ref": "reqweave/anthropic/`, 1)
	if js == validSettingsJSON {
		t.Fatalf("テストの前提が崩れている: validSettingsJSON に key_ref が無い\n%s", validSettingsJSON)
	}
	s, err := UnmarshalSettings([]byte(js))
	if err != nil {
		t.Fatalf("key_ref の無い secret_key の設定を読めない（拒否してはならない）: %v", err)
	}
	p, ok := s.DefaultProviderSetting()
	if !ok {
		t.Fatal("既定のプロバイダ設定が読めない")
	}
	if p.KeyRef != "" {
		t.Errorf("key_ref が空でない: %q", p.KeyRef)
	}
	if p.AuthMethodOrDefault() != "secret_key" {
		t.Errorf("認証方式が secret_key でない: %q", p.AuthMethodOrDefault())
	}
	// 読めた設定をそのまま書き戻せる（保存側の検査でも拒否されない）。
	if _, err := s.Marshal(); err != nil {
		t.Fatalf("読めた設定を書き出せない: %v", err)
	}
}
