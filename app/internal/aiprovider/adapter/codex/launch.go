package codex

// 本ファイルは起動の設定。
//
// 起動引数・環境変数は**アダプタ内の定数**であり、設定・利用者の環境から変えられる経路を設けない
// （API の呼び出し先を定数にしているのと同じ構造的担保）。未知の設定キーは Codex に黙って無視されるため、
// 渡しただけで効いたとみなさず、起動後に `config/read` の実効値を照合する（guard.go の検査 b）。

import (
	"fmt"
	"sort"
	"strings"
)

// authMethod は認証方式（アプリ設定の `providers[].auth_method`）。
type authMethod string

const (
	// authSecretKey はシークレットキー方式。キーは起動時に標準入力から渡し、Codex 側に複製を作らせない。
	authSecretKey authMethod = "secret_key"
	// authChatGPTSignin は ChatGPT のアカウントでのサインイン。
	authChatGPTSignin authMethod = "chatgpt_signin"
)

// 独自プロバイダ。組み込みの `openai` プロバイダは WebSocket を先に試し、
// その接続の記録として認証情報を Codex の動作ログへ残すため使わない（実測）。
const providerName = "reqweave"

// 呼び出し先はアダプタ内の定数であり、設定による書き換え手段を設けない。
// パッケージ内のテストのみが差し替える（実バイナリの確認で手元の記録用サーバへ向けるため）。
var (
	// baseURLSecretKey はシークレットキー方式の呼び出し先（OpenAI の API）。
	baseURLSecretKey = "https://api.openai.com/v1"
	// baseURLChatGPT はサインインの呼び出し先。
	baseURLChatGPT = "https://chatgpt.com/backend-api/codex"
)

// disabledFeatures は `--disable` で落とす機能（道具を持たせない）。
//
// 残る道具は `update_plan` `request_user_input` `apply_patch` `view_image` の 4 種で、
// `view_image` は手元のモデル定義（catalog.go）で拒否させる（いずれも実測）。
var disabledFeatures = []string{
	"apps", "browser_use", "computer_use", "image_generation", "multi_agent", "plugins",
	"unified_exec", "goals", "hooks", "tool_suggest", "in_app_browser", "memories",
	"shell_snapshot", "skill_mcp_dependency_install", "personality",
	// 要求の圧縮を止めるのは、要求本文の観測（版を上げるときの再確認）で本文を読めるようにし、
	// 本番と観測の設定を同一に保つため。
	"enable_request_compression",
}

// launchConfig は 1 つの子プロセスの起動条件。
type launchConfig struct {
	auth authMethod
	// codexHome は Codex の一時領域の `home/`（CODEX_HOME と CODEX_SQLITE_HOME の両方）。
	codexHome string
	// workDir はスレッドの作業フォルダ（空のまま。プロジェクトのフォルダを渡さない）。
	workDir string
	// catalogPath は手元のモデル定義の書き出し先。
	catalogPath string
}

// credentialsStore は認証情報の保存先（シークレットキー方式はメモリのみ、サインインは OS のセキュアストレージ。ファイルには置かせない）。
func (c launchConfig) credentialsStore() string {
	if c.auth == authChatGPTSignin {
		return "keyring"
	}
	return "ephemeral"
}

// baseURL は独自プロバイダの呼び出し先。
func (c launchConfig) baseURL() string {
	if c.auth == authChatGPTSignin {
		return baseURLChatGPT
	}
	return baseURLSecretKey
}

// launchArgs は `codex app-server` へ渡す引数を組み立てる。
//
// 接続口は既定の標準入出力だけとし、`--listen` で他の接続口を開かない。
func (c launchConfig) launchArgs() []string {
	args := []string{"app-server"}
	for _, override := range c.overrides() {
		args = append(args, "-c", override)
	}
	for _, feature := range disabledFeatures {
		args = append(args, "--disable", feature)
	}
	return args
}

// overrides は `-c` で渡す設定（値は TOML として解釈される）。
func (c launchConfig) overrides() []string {
	return []string{
		// 既定の外部接続を止める
		"analytics.enabled=false",
		`otel.metrics_exporter="none"`,
		"check_for_update_on_startup=false",
		"feedback.enabled=false",
		`web_search="disabled"`,
		// 道具を持たせない（残りは --disable）
		"features.shell_tool=false",
		// 端末の情報を付けない
		"include_environment_context=false",
		"include_permissions_instructions=false",
		"skills.bundled.enabled=false",
		// 会話の記録を残さない（スレッドは ephemeral で作る）
		`history.persistence="none"`,
		// 認証情報の保存先
		fmt.Sprintf("cli_auth_credentials_store=%q", c.credentialsStore()),
		// 呼び出し先（独自プロバイダ。再試行は抽象化層の StreamRetrying に一本化する）
		fmt.Sprintf("model_provider=%q", providerName),
		fmt.Sprintf("model_providers.%s={ name=%q, base_url=%q, wire_api=\"responses\", "+
			"requires_openai_auth=true, supports_websockets=false, "+
			"request_max_retries=0, stream_max_retries=0 }",
			providerName, providerName, c.baseURL()),
		// 画像の読み取りを拒否させる手元のモデル定義
		fmt.Sprintf("model_catalog_json=%q", c.catalogPath),
	}
}

// 子プロセスへ渡す環境変数。**許可リスト方式**で組み立てる。
//
// 利用者の環境にある `OPENAI_*`・その他の `CODEX_*`・`RUST_LOG`・`SSLKEYLOGFILE`（TLS の鍵を
// ファイルへ書かせる）等は引き継がない。`PATH` は OS 標準の場所だけを渡す。
const fixedPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// passThroughEnv は値をそのまま引き継ぐ環境変数（HOME は keyring と skill の検出に要る）。
var passThroughEnv = []string{"HOME", "USER", "LOGNAME", "TMPDIR", "LANG"}

// launchEnv は子プロセスへ渡す環境変数を組み立てる。
//
// environ は本システムの環境（`os.Environ()` の形。テストのみが差し替える）。
// ここに**無いものは渡らない**（許可リスト方式の構造的担保）。
func (c launchConfig) launchEnv(environ []string) []string {
	source := map[string]string{}
	for _, entry := range environ {
		if name, value, ok := strings.Cut(entry, "="); ok {
			source[name] = value
		}
	}
	env := map[string]string{
		"PATH":              fixedPath,
		"CODEX_HOME":        c.codexHome,
		"CODEX_SQLITE_HOME": c.codexHome,
	}
	copyEnv := func(key string) {
		if v := source[key]; v != "" {
			env[key] = v
		}
	}
	for _, key := range passThroughEnv {
		copyEnv(key)
	}
	// ロケール（LC_*）はそのまま引き継ぐ。値は利用者の表示設定であり、端末の識別には使えない。
	for name := range source {
		if strings.HasPrefix(name, "LC_") {
			copyEnv(name)
		}
	}
	// プロキシは本システムが OS 設定（環境）から解決した値を渡す。
	if v := firstNonEmpty(source, "HTTPS_PROXY", "https_proxy"); v != "" {
		env["HTTPS_PROXY"] = v
	}
	if v := firstNonEmpty(source, "NO_PROXY", "no_proxy"); v != "" {
		env["NO_PROXY"] = v
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func firstNonEmpty(source map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := source[k]; v != "" {
			return v
		}
	}
	return ""
}
