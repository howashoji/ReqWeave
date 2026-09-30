// versioncheck は版番号の正本（internal/appversion/VERSION）と、
// 版番号を複製している箇所（wails.json の productVersion）の一致を検査する。
//
// アイコンの原本と生成物の同期検査（tools/iconcheck）と同じ「正本と複製のずれを
// lint 段で捕まえる」型。版番号を上げ忘れたまま配布物を作ることを防ぐ。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// versionPath は版番号の正本（app/ からの相対パス）。
const versionPath = "internal/appversion/VERSION"

// wailsPath は版番号を複製している設定ファイル（app/ からの相対パス）。
const wailsPath = "wails.json"

const fixHint = "対処: " + versionPath + " を正本として、" + wailsPath +
	" の info.productVersion を同じ値に揃えること。"

// check は正本と複製を突き合わせ、問題の一覧を返す。
func check(root string) ([]string, error) {
	rawVersion, err := os.ReadFile(filepath.Join(root, versionPath))
	if err != nil {
		return nil, fmt.Errorf("版番号の正本を読めません（%s）: %w", versionPath, err)
	}
	version := strings.TrimSpace(string(rawVersion))

	var problems []string
	if err := validateSemver(version); err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", versionPath, err))
	}

	rawWails, err := os.ReadFile(filepath.Join(root, wailsPath))
	if err != nil {
		return nil, fmt.Errorf("%s を読めません: %w", wailsPath, err)
	}
	var cfg struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rawWails, &cfg); err != nil {
		return nil, fmt.Errorf("%s を JSON として読めません: %w", wailsPath, err)
	}
	if cfg.Info.ProductVersion == "" {
		problems = append(problems,
			fmt.Sprintf("%s: info.productVersion が空または不在", wailsPath))
	} else if cfg.Info.ProductVersion != version {
		problems = append(problems, fmt.Sprintf(
			"%s: info.productVersion = %q（正本 %s は %q）",
			wailsPath, cfg.Info.ProductVersion, versionPath, version))
	}
	return problems, nil
}

// validateSemver は major.minor.patch 形式（前置ゼロ・接頭辞・付加情報なし）を要求する。
// 判定規則の正本は internal/appversion.ParseSemver で、本検査はその部分集合を
// 外部依存なしに再実装したもの（lint は app モジュールのビルドに依存しない）。
func validateSemver(v string) error {
	if v == "" {
		return fmt.Errorf("版番号が空です")
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return fmt.Errorf("版番号 %q は major.minor.patch 形式ではありません", v)
	}
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return fmt.Errorf("版番号 %q の第 %d 要素 %q が数字ではありません", v, i+1, p)
		}
		if len(p) > 1 && p[0] == '0' {
			return fmt.Errorf("版番号 %q の第 %d 要素 %q に先頭 0 があります", v, i+1, p)
		}
		if _, err := strconv.Atoi(p); err != nil {
			return fmt.Errorf("版番号 %q の第 %d 要素 %q を数値にできません", v, i+1, p)
		}
	}
	return nil
}
