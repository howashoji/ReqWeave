package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

// effectiveConfigFor は「起動引数どおりに効いた」状態の実効値を組み立てる（実測と同じ形）。
func effectiveConfigFor(t *testing.T, cfg launchConfig) map[string]any {
	t.Helper()
	config := map[string]any{}
	for _, exp := range cfg.expectations() {
		setConfigPath(config, exp.path, exp.want)
	}
	return config
}

func setConfigPath(root map[string]any, path string, value any) {
	keys := strings.Split(path, ".")
	cur := root
	for _, key := range keys[:len(keys)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = value
}

// 実効値が起動引数のとおりなら合格し、1 項目でも違えば不合格。
func TestVerifyEffectiveConfig(t *testing.T) {
	cfg := testLaunchConfig(authSecretKey)
	config := effectiveConfigFor(t, cfg)
	if err := cfg.verifyEffectiveConfig(config); err != nil {
		t.Fatalf("そのとおりの実効値で不合格になった: %v", err)
	}

	// 照合の対象は起動設定の全項目（抑止設定・道具・独自プロバイダ・モデル定義）。
	for _, path := range []string{
		"web_search", "analytics.enabled", "features.shell_tool", "features.browser_use",
		"history.persistence", "cli_auth_credentials_store", "model_provider",
		"model_providers.reqweave.base_url", "model_providers.reqweave.supports_websockets",
		"model_providers.reqweave.request_max_retries", "model_catalog_json",
		"include_environment_context", "include_permissions_instructions", "skills.bundled.enabled",
		"otel.metrics_exporter", "check_for_update_on_startup", "feedback.enabled",
	} {
		t.Run("欠落: "+path, func(t *testing.T) {
			broken := effectiveConfigFor(t, cfg)
			deleteConfigPath(broken, path)
			err := cfg.verifyEffectiveConfig(broken)
			if err == nil {
				t.Fatalf("%s が無いのに合格した", path)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("どの項目が合わないか分からない: %v", err)
			}
		})
	}

	t.Run("値が違う", func(t *testing.T) {
		broken := effectiveConfigFor(t, cfg)
		setConfigPath(broken, "web_search", "enabled")
		if err := cfg.verifyEffectiveConfig(broken); err == nil {
			t.Fatal("違う値で合格した")
		}
	})
	t.Run("呼び出し先の差し替え", func(t *testing.T) {
		broken := effectiveConfigFor(t, cfg)
		setConfigPath(broken, "model_providers.reqweave.base_url", "https://example.invalid/v1")
		if err := cfg.verifyEffectiveConfig(broken); err == nil {
			t.Fatal("呼び出し先が変わっても合格した")
		}
	})
}

func deleteConfigPath(root map[string]any, path string) {
	keys := strings.Split(path, ".")
	cur := root
	for _, key := range keys[:len(keys)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, keys[len(keys)-1])
}

// 実効値は JSON で届くため、数値は float64 で照合できること（型の取り違えで素通りしない）。
func TestVerifyEffectiveConfigWithJSONNumbers(t *testing.T) {
	cfg := testLaunchConfig(authSecretKey)
	body, err := json.Marshal(effectiveConfigFor(t, cfg))
	if err != nil {
		t.Fatalf("実効値を組み立てられない: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("実効値を解釈できない: %v", err)
	}
	if err := cfg.verifyEffectiveConfig(decoded); err != nil {
		t.Errorf("JSON 経由の実効値で不合格になった: %v", err)
	}
}

// 実行中の版が同梱の版と一致すること。
func TestVerifyVersion(t *testing.T) {
	version, err := bundledVersion()
	if err != nil {
		t.Fatalf("同梱物の定義を読めない: %v", err)
	}
	if err := verifyVersion("reqweave/" + version + " (Mac OS 26.6.2; arm64) unknown"); err != nil {
		t.Errorf("同梱の版で不合格になった: %v", err)
	}
	if err := verifyVersion("reqweave/0.999.0 (Mac OS 26.6.2; arm64)"); err == nil {
		t.Error("違う版で合格した")
	}
	if err := verifyVersion("よく分からない文字列"); err == nil {
		t.Error("版を読み取れないのに合格した")
	}
}

// 認証の種類が選択中の認証方式と一致すること。
func TestVerifyAccountType(t *testing.T) {
	secret := testLaunchConfig(authSecretKey)
	if err := secret.verifyAccountType("apiKey", true); err != nil {
		t.Errorf("キー方式で apiKey が不合格: %v", err)
	}
	if err := secret.verifyAccountType("chatgpt", true); err == nil {
		t.Error("キー方式なのに chatgpt で合格した")
	}
	if err := secret.verifyAccountType("", false); err == nil {
		t.Error("未認証のまま合格した")
	}

	signin := testLaunchConfig(authChatGPTSignin)
	if err := signin.verifyAccountType("chatgpt", true); err != nil {
		t.Errorf("サインインで chatgpt が不合格: %v", err)
	}
	if err := signin.verifyAccountType("", false); err != nil {
		t.Errorf("サインイン前の未認証が不合格: %v", err)
	}
	if err := signin.verifyAccountType("apiKey", true); err == nil {
		t.Error("サインインなのに apiKey で合格した")
	}
}
