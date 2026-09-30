package updater

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// trustedKeysJSON はアプリへ埋め込む信頼公開鍵の集合（更新マニフェストの署名の検証に使う）。
//
// 公開鍵はリリース責任者が可搬媒体上で鍵ペアを生成して登録する。
// 集合が空なら更新は 1 件も適用されない（fail-closed）。
// 空を「検証省略」に読み替える経路を作ってはならない。
//
//go:embed trusted_keys.json
var trustedKeysJSON []byte

// TrustedKeys はアプリ埋め込みの信頼公開鍵集合を返す。
// 埋め込みファイルが壊れている場合は空集合と誤りを返す（fail-closed）。
func TrustedKeys() (KeySet, error) { return parseTrustedKeys(trustedKeysJSON) }

// parseTrustedKeys は信頼鍵ファイルを読む。埋め込みと同じ経路をテストから使う。
func parseTrustedKeys(raw []byte) (KeySet, error) {
	var doc struct {
		Keys []struct {
			ID        string `json:"id"`
			PublicKey string `json:"public_key"` // base64（標準・パディングあり）
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return KeySet{}, fmt.Errorf("信頼公開鍵の一覧を読み取れません: %w", err)
	}
	keys := make([]PublicKey, 0, len(doc.Keys))
	for i, k := range doc.Keys {
		decoded, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil {
			return KeySet{}, fmt.Errorf("信頼公開鍵 %d 件目（id=%q）を復号できません: %w", i+1, k.ID, err)
		}
		keys = append(keys, PublicKey{ID: k.ID, Key: decoded})
	}
	return NewKeySet(keys...)
}
