package codex

// 本ファイルは起動後の検査。**fail-closed** であり、
// 4 つの検査（版・実効値・skill・認証の種類）のいずれか 1 つでも合わなければ
// 子プロセスを止め、AI 呼び出しを行わない。
//
// 未知の設定キーは Codex に黙って無視される（実測）ため、
// 起動引数を渡しただけで効いたとみなさない。照合の対象は launch.go の起動設定の全項目。

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// configExpectation は `config/read` の実効値 1 項目の期待値。
type configExpectation struct {
	path string // ドット区切りのパス（例 "model_providers.reqweave.base_url"）
	want any
}

// expectations は起動設定の全項目に対応する期待値を返す。
//
// 実効値のどこに現れるかは実測済み（`--disable` の各機能は `features`、
// 独自プロバイダは `model_providers`、手元のモデル定義は `model_catalog_json`）。
func (c launchConfig) expectations() []configExpectation {
	provider := "model_providers." + providerName + "."
	list := []configExpectation{
		{"analytics.enabled", false},
		{"otel.metrics_exporter", "none"},
		{"check_for_update_on_startup", false},
		{"feedback.enabled", false},
		{"web_search", "disabled"},
		{"features.shell_tool", false},
		{"include_environment_context", false},
		{"include_permissions_instructions", false},
		{"skills.bundled.enabled", false},
		{"history.persistence", "none"},
		{"cli_auth_credentials_store", c.credentialsStore()},
		{"model_provider", providerName},
		{provider + "name", providerName},
		{provider + "base_url", c.baseURL()},
		{provider + "wire_api", "responses"},
		{provider + "requires_openai_auth", true},
		{provider + "supports_websockets", false},
		{provider + "request_max_retries", 0.0},
		{provider + "stream_max_retries", 0.0},
		{"model_catalog_json", c.catalogPath},
	}
	for _, feature := range disabledFeatures {
		list = append(list, configExpectation{"features." + feature, false})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].path < list[j].path })
	return list
}

// verifyEffectiveConfig は `config/read` の実効値を期待値と照合する（検査 b）。
//
// 合わない項目があれば、**何が合わなかったか**を返す（版の更新で抑止設定が黙って効かなくなる
// ことに気づくため。値はいずれもアダプタ内の定数と一時領域のパスであり、秘密情報を含まない）。
func (c launchConfig) verifyEffectiveConfig(config map[string]any) error {
	var mismatches []string
	for _, exp := range c.expectations() {
		got, ok := lookupPath(config, exp.path)
		if !ok {
			mismatches = append(mismatches, fmt.Sprintf("%s: 実効値に現れない", exp.path))
			continue
		}
		if !sameConfigValue(got, exp.want) {
			mismatches = append(mismatches, fmt.Sprintf("%s: %s（期待 %s）",
				exp.path, formatConfigValue(got), formatConfigValue(exp.want)))
		}
	}
	if len(mismatches) == 0 {
		return nil
	}
	return fmt.Errorf("Codex の起動設定が効いていません: %s", strings.Join(mismatches, " / "))
}

// lookupPath はドット区切りのパスで JSON の値を引く。
func lookupPath(root map[string]any, path string) (any, bool) {
	cur := any(root)
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// sameConfigValue は JSON から取り出した値と期待値を比べる（数値は JSON の float64 で届く）。
func sameConfigValue(got, want any) bool {
	switch w := want.(type) {
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case string:
		g, ok := got.(string)
		return ok && g == w
	case float64:
		g, ok := got.(float64)
		return ok && g == w
	default:
		return false
	}
}

func formatConfigValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case string:
		return strconv.Quote(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// verifyVersion は実行中の Codex が同梱の版かを確かめる（検査 a）。
//
// `initialize` の応答の `userAgent` は `<clientInfo.name>/<版> (<OS>; <CPU>) …` の形
// （実測）。
func verifyVersion(userAgent string) error {
	want, err := bundledVersion()
	if err != nil {
		return err
	}
	got, ok := versionFromUserAgent(userAgent)
	if !ok {
		return fmt.Errorf("Codex の版を確認できません")
	}
	if got != want {
		return fmt.Errorf("Codex の版が同梱のものと違います: %s（同梱 %s）", got, want)
	}
	return nil
}

func versionFromUserAgent(userAgent string) (string, bool) {
	head, _, _ := strings.Cut(userAgent, " ")
	_, version, ok := strings.Cut(head, "/")
	if !ok || version == "" {
		return "", false
	}
	return version, true
}

// verifyAccountType は認証の種類が選択中の認証方式と一致するかを確かめる（検査 d）。
//
// サインイン方式では、サインイン前は未認証（account が無い）であることを含めて許す。
func (c launchConfig) verifyAccountType(accountType string, hasAccount bool) error {
	switch c.auth {
	case authChatGPTSignin:
		if !hasAccount || accountType == "chatgpt" {
			return nil
		}
		return fmt.Errorf("Codex の認証の種類が選択中の認証方式と違います: %s（期待 chatgpt）", accountType)
	default:
		if hasAccount && accountType == "apiKey" {
			return nil
		}
		if !hasAccount {
			return fmt.Errorf("Codex にシークレットキーを渡せていません")
		}
		return fmt.Errorf("Codex の認証の種類が選択中の認証方式と違います: %s（期待 apiKey）", accountType)
	}
}
