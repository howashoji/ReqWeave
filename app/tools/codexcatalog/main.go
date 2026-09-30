// codexcatalog は Codex へ渡す「手元のモデル定義」を生成する。
//
// 同梱する版の Codex に埋め込まれた既定の定義（`codex debug models --bundled`）を取り出し、
// **全モデルの入力を「文字のみ」にする以外は変えずに** JSON として書き出す。
// この定義を `model_catalog_json` で渡すと、Codex は画像入力に対応しないモデルでの `view_image` を
// **ファイルを開く前に**拒否し、サーバからモデル一覧を取りに行かない（実機で確かめた）。
//
// 版を上げるたびに作り直す。生成物は adapter/codex へ埋め込む。
//
// 使い方:
//
//	go run ./tools/codexcatalog -codex <codex の実行ファイル> \
//	    -out internal/aiprovider/adapter/codex/model_catalog.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	codexPath := flag.String("codex", "", "同梱する版の codex 実行ファイル（必須）")
	out := flag.String("out", "internal/aiprovider/adapter/codex/model_catalog.json", "生成先")
	bundlePath := flag.String("bundle", "internal/aiprovider/adapter/codex/bundle.json", "同梱物の定義（版の照合に使う）")
	flag.Parse()

	if err := run(*codexPath, *out, *bundlePath); err != nil {
		fmt.Fprintln(os.Stderr, "codexcatalog:", err)
		os.Exit(1)
	}
}

func run(codexPath, out, bundlePath string) error {
	if codexPath == "" {
		return fmt.Errorf("-codex に同梱する版の実行ファイルを指定してください")
	}
	want, err := bundleVersion(bundlePath)
	if err != nil {
		return err
	}
	got, err := version(codexPath)
	if err != nil {
		return err
	}
	// 別の版のモデル定義を混ぜない（同梱物の定義と生成物の食い違いを起こさない）。
	if got != want {
		return fmt.Errorf("codex の版が同梱物の定義と違います: 実行ファイル %q / 定義 %q", got, want)
	}

	catalog, err := bundledCatalog(codexPath)
	if err != nil {
		return err
	}
	if err := textOnly(catalog); err != nil {
		return err
	}
	body, err := json.MarshalIndent(catalog, "", " ")
	if err != nil {
		return fmt.Errorf("モデル定義を書き出せません: %w", err)
	}
	return os.WriteFile(out, append(body, '\n'), 0o644)
}

func bundleVersion(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("同梱物の定義を読めません: %w", err)
	}
	var def struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &def); err != nil {
		return "", fmt.Errorf("同梱物の定義を解釈できません: %w", err)
	}
	if def.Version == "" {
		return "", fmt.Errorf("同梱物の定義に版がありません: %s", path)
	}
	return def.Version, nil
}

// version は `codex --version`（例 "codex-cli 0.135.0-alpha.1"）から版を取り出す。
func version(codexPath string) (string, error) {
	out, err := exec.Command(codexPath, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("codex の版を取得できません: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", fmt.Errorf("codex の版を読み取れません: %q", string(out))
	}
	return fields[len(fields)-1], nil
}

// bundledCatalog は埋め込みの既定のモデル定義を取り出す。
//
// 利用者の `~/.codex` を読ませない（CODEX_HOME を一時フォルダにする）。`--bundled` は
// サーバからの更新を行わず、同梱の実行ファイルに埋め込まれた定義だけを出す。
func bundledCatalog(codexPath string) (map[string]any, error) {
	home, err := os.MkdirTemp("", "codexcatalog")
	if err != nil {
		return nil, fmt.Errorf("一時フォルダを作れません: %w", err)
	}
	defer os.RemoveAll(home)

	cmd := exec.Command(codexPath, "debug", "models", "--bundled")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+filepath.Clean(home))
	cmd.Stderr = os.Stderr
	body, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("既定のモデル定義を取得できません: %w", err)
	}
	var catalog map[string]any
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("既定のモデル定義を解釈できません: %w", err)
	}
	return catalog, nil
}

// textOnly は全モデルの入力を「文字のみ」にする（他の項目は変えない）。
func textOnly(catalog map[string]any) error {
	models, ok := catalog["models"].([]any)
	if !ok || len(models) == 0 {
		return fmt.Errorf("既定のモデル定義に models がありません")
	}
	for i, m := range models {
		model, ok := m.(map[string]any)
		if !ok {
			return fmt.Errorf("models[%d] がオブジェクトではありません", i)
		}
		model["input_modalities"] = []any{"text"}
	}
	return nil
}
