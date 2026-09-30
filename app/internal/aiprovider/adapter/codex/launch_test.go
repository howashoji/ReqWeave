package codex

import (
	"strings"
	"testing"
)

func testLaunchConfig(auth authMethod) launchConfig {
	return launchConfig{
		auth:        auth,
		codexHome:   "/tmp/rw/codex/home",
		workDir:     "/tmp/rw/codex/work",
		catalogPath: "/tmp/rw/codex/model_catalog.json",
	}
}

// 起動引数に抑止設定一式が含まれること（渡し漏れを検知する）。
func TestLaunchArgsCoverSuppressionSettings(t *testing.T) {
	args := strings.Join(testLaunchConfig(authSecretKey).launchArgs(), " ")

	if !strings.HasPrefix(args, "app-server") {
		t.Errorf("副命令が app-server でない: %q", args)
	}
	for _, want := range []string{
		"analytics.enabled=false",
		`otel.metrics_exporter="none"`,
		"check_for_update_on_startup=false",
		"feedback.enabled=false",
		`web_search="disabled"`,
		"features.shell_tool=false",
		"include_environment_context=false",
		"include_permissions_instructions=false",
		"skills.bundled.enabled=false",
		`history.persistence="none"`,
		`cli_auth_credentials_store="ephemeral"`,
		`model_provider="reqweave"`,
		`base_url="https://api.openai.com/v1"`,
		`supports_websockets=false`,
		`request_max_retries=0`,
		`stream_max_retries=0`,
		`model_catalog_json="/tmp/rw/codex/model_catalog.json"`,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("起動引数に %q が無い", want)
		}
	}
	// 道具を落とす --disable 16 種
	if len(disabledFeatures) != 16 {
		t.Errorf("--disable の種類 = %d, want 16", len(disabledFeatures))
	}
	for _, feature := range disabledFeatures {
		if !strings.Contains(args, "--disable "+feature) {
			t.Errorf("--disable %s が無い", feature)
		}
	}
	// 他の接続口を開かない
	for _, forbidden := range []string{"--listen", "daemon", "proxy"} {
		if strings.Contains(args, forbidden) {
			t.Errorf("使わないはずの指定がある: %q", forbidden)
		}
	}
}

// 認証方式で変わるのは保存先と呼び出し先だけ。
func TestLaunchArgsDifferByAuthMethod(t *testing.T) {
	signin := strings.Join(testLaunchConfig(authChatGPTSignin).launchArgs(), " ")
	if !strings.Contains(signin, `cli_auth_credentials_store="keyring"`) {
		t.Error("サインインの保存先が keyring でない")
	}
	if !strings.Contains(signin, `base_url="https://chatgpt.com/backend-api/codex"`) {
		t.Error("サインインの呼び出し先が違う")
	}
}

// 子プロセスへ渡す環境は許可リストだけ（利用者の環境を引き継がない）。
func TestLaunchEnvIsAllowList(t *testing.T) {
	environ := []string{
		"HOME=/Users/tester",
		"USER=tester",
		"LANG=ja_JP.UTF-8",
		"LC_ALL=ja_JP.UTF-8",
		"TMPDIR=/var/folders/tmp/",
		"PATH=/Users/tester/bin:/usr/local/bin",
		"OPENAI_API_KEY=sk-should-not-be-passed-0000",
		"CODEX_HOME=/Users/tester/.codex",
		"CODEX_SQLITE_HOME=/Users/tester/.codex",
		"RUST_LOG=trace",
		"SSLKEYLOGFILE=/tmp/keys.log",
		"HTTPS_PROXY=http://proxy.example:8080",
		"NO_PROXY=localhost",
		"AWS_SECRET_ACCESS_KEY=should-not-be-passed",
	}
	env := testLaunchConfig(authSecretKey).launchEnv(environ)
	got := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		got[name] = value
	}

	want := map[string]string{
		"HOME":              "/Users/tester",
		"USER":              "tester",
		"LANG":              "ja_JP.UTF-8",
		"LC_ALL":            "ja_JP.UTF-8",
		"TMPDIR":            "/var/folders/tmp/",
		"PATH":              fixedPath,
		"CODEX_HOME":        "/tmp/rw/codex/home",
		"CODEX_SQLITE_HOME": "/tmp/rw/codex/home",
		"HTTPS_PROXY":       "http://proxy.example:8080",
		"NO_PROXY":          "localhost",
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	for _, forbidden := range []string{"OPENAI_API_KEY", "RUST_LOG", "SSLKEYLOGFILE", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := got[forbidden]; ok {
			t.Errorf("引き継いではいけない環境変数が渡っている: %s", forbidden)
		}
	}
	if len(got) != len(want) {
		t.Errorf("渡す環境変数の数 = %d, want %d（%v）", len(got), len(want), got)
	}
	// 利用者の PATH をそのまま渡さない（OS 標準の場所だけ）
	if strings.Contains(got["PATH"], "/Users/tester/bin") {
		t.Errorf("利用者の PATH を引き継いでいる: %q", got["PATH"])
	}
}

// キーはコマンドライン引数・環境変数に置かない（標準入力から渡す）。
func TestLaunchNeverCarriesTheKey(t *testing.T) {
	cfg := testLaunchConfig(authSecretKey)
	all := strings.Join(append(cfg.launchArgs(), cfg.launchEnv([]string{"OPENAI_API_KEY=" + dummyKey})...), " ")
	if strings.Contains(all, dummyKey) {
		t.Error("キーが起動引数・環境変数に現れている")
	}
}
