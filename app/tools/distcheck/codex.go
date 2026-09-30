package main

// 同梱した Codex の検査。
//
// 同梱物の定義（adapter/codex/bundle.json）が正本で、ここでは**配布物に実際に入っているもの**が
// その定義どおりかを見る。アーキテクチャごとの中身を取り出して SHA-256 で照合するため、
// 「片方のアーキテクチャだけ差し替えた」「版だけ書き換えた」状態を検出できる
// （アイコンの同期検査 = tools/iconcheck と同じ考え方）。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/codex"
)

// lipoArch は Go のアーキテクチャ名を lipo の名前へ写す。
var lipoArch = map[string]string{"arm64": "arm64", "amd64": "x86_64"}

// checkBundledCodex は配布物の中の同梱 Codex を検査する。
//
// appPath はマウントした dmg の中のアプリ本体。problems は不合格の理由、notes は記録する事実。
func checkBundledCodex(appPath string, require bool) (problems []string, notes []string) {
	def, err := codex.Bundle()
	if err != nil {
		return []string{"同梱物の定義を読めません: " + err.Error()}, nil
	}
	path := filepath.Join(appPath, codex.BundledExecutableRelPath)
	if _, err := os.Stat(path); err != nil {
		return []string{fmt.Sprintf(
			"同梱の Codex がありません（%s）。make codex で用意してから配布物を作ること",
			codex.BundledExecutableRelPath)}, nil
	}

	// アーキテクチャごとの中身を取り出して照合する（実行はしない）。
	for _, artifact := range def.Artifacts {
		arch, ok := lipoArch[artifact.GOARCH]
		if !ok {
			problems = append(problems, "同梱物の定義に想定外のアーキテクチャがあります: "+artifact.GOARCH)
			continue
		}
		got, err := thinSHA256(path, arch)
		if err != nil {
			problems = append(problems, fmt.Sprintf("同梱の Codex から %s を取り出せません: %v", arch, err))
			continue
		}
		if got != artifact.BinarySHA256 {
			problems = append(problems, fmt.Sprintf(
				"同梱の Codex（%s）が定義と違います: %s（定義 %s）", arch, got, artifact.BinarySHA256))
		}
	}

	// ライセンス表示（Apache-2.0 の 4 項。License の写しと NOTICE を配布物に添える）。
	dir := filepath.Dir(path)
	for _, license := range def.Licenses {
		got, err := sha256Of(filepath.Join(dir, license.Name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("同梱の Codex の %s がありません（Apache-2.0 の 4 項）", license.Name))
			continue
		}
		if got != license.SHA256 {
			problems = append(problems, fmt.Sprintf("同梱の Codex の %s が定義と違います", license.Name))
		}
	}

	// 署名（公証の必須条件。同梱物は取得元の Developer ID 署名のまま入る）。
	info, err := inspectCodesign(path)
	switch {
	case err != nil:
		problems = append(problems, "同梱の Codex の署名を読めません: "+err.Error())
	case info.developerID() == "":
		if require {
			problems = append(problems, "同梱の Codex に Developer ID 署名がありません（公証の必須条件）")
		} else {
			notes = append(notes, "同梱の Codex: 署名の検査は未実施")
		}
	default:
		notes = append(notes, "同梱の Codex の署名: "+info.developerID())
		if out, verr := exec.Command("codesign", "--verify", "--strict", "--verbose=2", path).CombinedOutput(); verr != nil {
			problems = append(problems, "同梱の Codex の署名が壊れています: "+oneLine(string(out)))
		}
		if !info.hardened && require {
			problems = append(problems, "同梱の Codex の hardened runtime が有効ではありません（公証の必須条件）")
		}
	}

	if len(problems) == 0 {
		notes = append(notes, fmt.Sprintf("同梱の Codex: %s（%d アーキテクチャ・定義と一致）", def.Version, len(def.Artifacts)))
	}
	return problems, notes
}

// thinSHA256 は universal binary から 1 アーキテクチャ分を取り出して SHA-256 を返す。
func thinSHA256(path, arch string) (string, error) {
	tmp, err := os.CreateTemp("", "distcheck-codex-")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	if out, err := exec.Command("lipo", "-thin", arch, path, "-output", tmp.Name()).CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return sha256Of(tmp.Name())
}

func sha256Of(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
