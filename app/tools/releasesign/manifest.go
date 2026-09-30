package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/updater"
)

// assetSpec は配布物 1 件の指定（`os/arch=パス` の形で受け取る）。
type assetSpec struct {
	OS   string
	Arch string
	Path string
}

// parseAssetSpec は `darwin/universal=build/bin/ReqWeave-macos.zip` を解釈する。
func parseAssetSpec(s string) (assetSpec, error) {
	idAndPath := strings.SplitN(s, "=", 2)
	if len(idAndPath) != 2 {
		return assetSpec{}, fmt.Errorf("配布物の指定 %q が `os/arch=パス` の形ではありません", s)
	}
	osArch := strings.SplitN(idAndPath[0], "/", 2)
	if len(osArch) != 2 || osArch[0] == "" || osArch[1] == "" {
		return assetSpec{}, fmt.Errorf("配布物の指定 %q の os/arch が読み取れません", s)
	}
	if strings.TrimSpace(idAndPath[1]) == "" {
		return assetSpec{}, fmt.Errorf("配布物の指定 %q にパスがありません", s)
	}
	return assetSpec{OS: osArch[0], Arch: osArch[1], Path: idAndPath[1]}, nil
}

// hashAsset は配布物の SHA-256 とサイズを実ファイルから求める。
func hashAsset(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("配布物を開けません（%s）: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("配布物を読めません（%s）: %w", path, err)
	}
	if n == 0 {
		return "", 0, fmt.Errorf("配布物が空です（%s）", path)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// downloadURL は GitHub Releases の資産 URL を組み立てる。
// アプリ側の取得先（updater.ManifestURL）と同じリポジトリを指す。
func downloadURL(version, fileName string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s",
		updater.ReleasesOwner, updater.ReleasesRepo, version, fileName)
}

// buildManifest は配布物からマニフェストを組み立て、指定の鍵すべてで署名する。
//
// 鍵を 2 本渡すと二重署名になる（鍵ローテーションの移行期間に、新旧どちらの鍵を信頼するアプリでも検証できるように）。
func buildManifest(version string, assets []updater.Asset,
	keys []struct {
		ID   string
		Priv ed25519.PrivateKey
	}) (updater.Manifest, error) {
	if len(keys) == 0 {
		return updater.Manifest{}, fmt.Errorf("署名する鍵が指定されていません")
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].ID() < assets[j].ID() })
	m := updater.Manifest{Schema: updater.SchemaID, Version: version, Assets: assets}

	payload := updater.SigningPayload(m)
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k.ID] {
			return updater.Manifest{}, fmt.Errorf("同じ鍵 ID %q が 2 回指定されています", k.ID)
		}
		seen[k.ID] = true
		m.Signatures = append(m.Signatures, updater.Signature{
			KeyID: k.ID,
			Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(k.Priv, payload)),
		})
	}
	return m, nil
}

// writeManifest はマニフェストを JSON で書き出す。
func writeManifest(path string, m updater.Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("マニフェストを組み立てられません: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("マニフェストを保存できません（%s）: %w", path, err)
	}
	return nil
}
