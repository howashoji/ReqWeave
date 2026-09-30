// codexfetch は同梱する Codex を取得して照合し、universal binary に組み立てる。
//
// 版・取得元・アーキテクチャごとの SHA-256 は**同梱物の定義**（adapter/codex/bundle.json）が正本で、
// ここではそれを読むだけである（定義と取得物が食い違ったら止める）。
//
//	go run ./tools/codexfetch            # build/codex/codex を用意する（既にあれば何もしない）
//	go run ./tools/codexfetch -force     # 取り直す
//
// 取得物は書庫・実行ファイルの両方を SHA-256 で照合し、合わなければ**組み立てない**
// （改ざん・取り違えのまま配布物に入らないようにする）。組み立てた後は、
// 両アーキテクチャを含むこと・版が定義どおりであることを実際に確かめる。
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/codex"
)

// 取得元。定義の release_tag と組み合わせて使う。
const (
	// downloadBase は実行ファイル（リリース物）の取得元。
	downloadBase = "https://github.com/openai/codex/releases/download"
	// sourceBase はライセンス表示（リポジトリ内のファイル）の取得元。
	sourceBase = "https://raw.githubusercontent.com/openai/codex"
)

func main() {
	out := flag.String("out", filepath.Join("build", "codex"), "組み立て先のフォルダ")
	cache := flag.String("cache", filepath.Join("build", "codex", "cache"), "取得物の置き場")
	force := flag.Bool("force", false, "既にあっても取り直す")
	flag.Parse()

	if err := run(*out, *cache, *force); err != nil {
		fmt.Fprintln(os.Stderr, "codexfetch:", err)
		os.Exit(1)
	}
}

func run(outDir, cacheDir string, force bool) error {
	def, err := codex.Bundle()
	if err != nil {
		return err
	}
	target := filepath.Join(outDir, "codex")

	if !force {
		if err := verifyUniversal(target, def); err == nil && licensesPresent(outDir, def) {
			fmt.Printf("codexfetch: 同梱物は用意済み（%s / %s）\n", def.Version, target)
			return nil
		}
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("置き場を作れません: %w", err)
	}

	var parts []string
	for _, artifact := range def.Artifacts {
		path, err := fetchArtifact(def, artifact, cacheDir, force)
		if err != nil {
			return err
		}
		parts = append(parts, path)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("組み立て先を作れません: %w", err)
	}
	// 2 アーキテクチャを 1 つの実行ファイルにまとめる（起動時に実行中の CPU に合う方が使われる）。
	args := append([]string{"-create", "-output", target}, parts...)
	if out, err := exec.Command("lipo", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("universal binary を組み立てられません: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return fmt.Errorf("実行権限を付けられません: %w", err)
	}
	if err := verifyUniversal(target, def); err != nil {
		return err
	}
	// ライセンス表示を同梱物の隣へ置く（Apache-2.0 の 4 項。配布物に License の写しと NOTICE を添える）。
	for _, license := range def.Licenses {
		dest := filepath.Join(outDir, license.Name)
		if force || !hashMatches(dest, license.SHA256) {
			url := fmt.Sprintf("%s/%s/%s", sourceBase, def.ReleaseTag, license.Path)
			fmt.Printf("codexfetch: 取得中 %s\n", url)
			if err := download(url, dest); err != nil {
				return err
			}
		}
		if !hashMatches(dest, license.SHA256) {
			return fmt.Errorf("ライセンス表示の SHA-256 が定義と違います: %s", license.Name)
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	fmt.Printf("codexfetch: 用意しました（%s / %s / %.0f MB）\n", def.Version, target, float64(info.Size())/(1024*1024))
	return nil
}

// fetchArtifact は 1 アーキテクチャ分を取得し、書庫と実行ファイルの両方を SHA-256 で照合する。
func fetchArtifact(def codex.BundleDefinition, artifact codex.BundleArtifact, cacheDir string, force bool) (string, error) {
	archive := filepath.Join(cacheDir, artifact.Asset)
	if force || !hashMatches(archive, artifact.ArchiveSHA256) {
		url := fmt.Sprintf("%s/%s/%s", downloadBase, def.ReleaseTag, artifact.Asset)
		fmt.Printf("codexfetch: 取得中 %s\n", url)
		if err := download(url, archive); err != nil {
			return "", err
		}
	}
	if !hashMatches(archive, artifact.ArchiveSHA256) {
		return "", fmt.Errorf("取得物の SHA-256 が定義と違います: %s", artifact.Asset)
	}

	binary := filepath.Join(cacheDir, artifact.Binary)
	if force || !hashMatches(binary, artifact.BinarySHA256) {
		if err := extract(archive, artifact.Binary, binary); err != nil {
			return "", err
		}
	}
	if !hashMatches(binary, artifact.BinarySHA256) {
		return "", fmt.Errorf("展開した実行ファイルの SHA-256 が定義と違います: %s", artifact.Binary)
	}
	return binary, nil
}

func download(url, dest string) error {
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("取得できません: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("取得できません（HTTP %d）: %s", resp.StatusCode, url)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("書き出せません: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("書き出せません: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// extract は書庫（tar.gz）から目的の 1 ファイルだけを取り出す。
func extract(archive, name, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("書庫を開けません: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("書庫を読めません: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return fmt.Errorf("書庫に %s がありません", name)
		}
		if err != nil {
			return fmt.Errorf("書庫を読めません: %w", err)
		}
		if filepath.Base(header.Name) != name {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return fmt.Errorf("書き出せません: %w", err)
		}
		if _, err := io.Copy(out, reader); err != nil {
			out.Close()
			return fmt.Errorf("書き出せません: %w", err)
		}
		return out.Close()
	}
}

// licensesPresent はライセンス表示が定義どおりに置かれているかを返す。
func licensesPresent(outDir string, def codex.BundleDefinition) bool {
	for _, license := range def.Licenses {
		if !hashMatches(filepath.Join(outDir, license.Name), license.SHA256) {
			return false
		}
	}
	return true
}

func hashMatches(path, want string) bool {
	got, err := sha256File(path)
	return err == nil && got == want
}

func sha256File(path string) (string, error) {
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

// verifyUniversal は組み立てた実行ファイルが、定義どおりのアーキテクチャと版かを確かめる。
func verifyUniversal(path string, def codex.BundleDefinition) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("同梱物がありません: %s", path)
	}
	out, err := exec.Command("lipo", "-archs", path).Output()
	if err != nil {
		return fmt.Errorf("アーキテクチャを確認できません: %w", err)
	}
	archs := strings.Fields(string(out))
	for _, want := range []string{"arm64", "x86_64"} {
		if !contains(archs, want) {
			return fmt.Errorf("同梱物に %s が含まれていません（%v）", want, archs)
		}
	}
	version, err := exec.Command(path, "--version").Output()
	if err != nil {
		return fmt.Errorf("版を確認できません: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(version)))
	if len(fields) == 0 || fields[len(fields)-1] != def.Version {
		return fmt.Errorf("同梱物の版が定義と違います: %q（定義 %s）", strings.TrimSpace(string(version)), def.Version)
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
