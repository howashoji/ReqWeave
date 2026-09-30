// releasesign はリリース時に更新マニフェストを作り、ed25519 で署名する道具。
// アプリのアップデータは、埋め込みの信頼鍵でこの署名を検証してから更新を適用する。
//
// **秘密鍵はリポジトリ・ビルド環境に置かない**。本ツールは秘密鍵を
// 「値」ではなく「ファイルパス」でのみ受け取る（コマンドライン引数に鍵の値を書くと
// `ps` から読めるため）。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// signingKeyFile は秘密鍵ファイルの中身。可搬媒体上に置く。
type signingKeyFile struct {
	// ID は鍵の識別子。マニフェストの署名欄と信頼鍵一覧の照合に使う。
	ID string `json:"id"`
	// Material は ed25519 の 64 バイト秘密鍵（base64）。
	//
	// 本フィールドの値は**表示・ログ・エラー文言に出さない**。
	Material string `json:"material"`
}

// trustedKeyEntry は信頼鍵一覧（internal/updater/trusted_keys.json）へ載せる 1 件。
type trustedKeyEntry struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
}

// generateKeyPair は新しい鍵ペアを作る。ID は公開鍵の先頭 8 バイトの 16 進。
func generateKeyPair() (signingKeyFile, trustedKeyEntry, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return signingKeyFile{}, trustedKeyEntry{}, fmt.Errorf("鍵を生成できません: %w", err)
	}
	id := "rw-" + hex.EncodeToString(pub[:8])
	return signingKeyFile{ID: id, Material: base64.StdEncoding.EncodeToString(priv)},
		trustedKeyEntry{ID: id, PublicKey: base64.StdEncoding.EncodeToString(pub)}, nil
}

// writeKeyFile は秘密鍵ファイルを本人だけが読める権限で書く。
//
// 書き込み先がこのリポジトリの中なら**拒否する**（誤って版管理へ入る事故を機械的に防ぐ）。
func writeKeyFile(path string, key signingKeyFile, repoRoot string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("保存先のパスを解決できません: %w", err)
	}
	if repoRoot != "" && withinDir(repoRoot, abs) {
		return fmt.Errorf("秘密鍵をリポジトリの中（%s 配下）へ保存できません。"+
			"可搬媒体などリポジトリの外の場所を指定してください", repoRoot)
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("保存先に既にファイルがあります。既存の鍵を上書きしません: %s", abs)
	}
	raw, err := json.MarshalIndent(key, "", "  ")
	if err != nil {
		return fmt.Errorf("鍵ファイルを組み立てられません: %w", err)
	}
	// 0600（本人のみ読み書き）。
	if err := os.WriteFile(abs, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("鍵ファイルを保存できません: %w", err)
	}
	return nil
}

// loadKeyFile は秘密鍵ファイルを読む。
//
// 誤りの文言に鍵の中身を載せない（パス・種別のみ）。
func loadKeyFile(path string) (string, ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("鍵ファイルを読めません（%s）: %w", path, err)
	}
	var f signingKeyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", nil, fmt.Errorf("鍵ファイルの形式を読み取れません（%s）", path)
	}
	if strings.TrimSpace(f.ID) == "" {
		return "", nil, fmt.Errorf("鍵ファイルに鍵 ID がありません（%s）", path)
	}
	material, err := base64.StdEncoding.DecodeString(f.Material)
	if err != nil {
		return "", nil, fmt.Errorf("鍵ファイルの鍵素材を復号できません（%s）", path)
	}
	if len(material) != ed25519.PrivateKeySize {
		return "", nil, fmt.Errorf("鍵ファイルの鍵素材の長さが不正です（%s）", path)
	}
	return f.ID, ed25519.PrivateKey(material), nil
}

// withinDir は child が dir と同じか配下にあるかを返す。
func withinDir(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
