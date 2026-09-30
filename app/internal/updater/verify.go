package updater

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
)

// PublicKey は更新マニフェストの署名検証に使う公開鍵。
type PublicKey struct {
	ID  string
	Key ed25519.PublicKey
}

// KeySet は信頼する公開鍵の集合。鍵ローテーション中は新旧 2 鍵が入る。
//
// 空の KeySet はどの署名も受理しない（fail-closed）。鍵が未登録のうちは
// 更新が一切適用されないのが正しい振る舞いであり、素通りさせてはならない。
type KeySet struct {
	keys map[string]ed25519.PublicKey
}

// NewKeySet は公開鍵の集合を作る。ID の重複は誤りとして返す。
func NewKeySet(keys ...PublicKey) (KeySet, error) {
	set := KeySet{keys: make(map[string]ed25519.PublicKey, len(keys))}
	for _, k := range keys {
		id := strings.TrimSpace(k.ID)
		if id == "" {
			return KeySet{}, fmt.Errorf("公開鍵の ID が空です")
		}
		if len(k.Key) != ed25519.PublicKeySize {
			return KeySet{}, fmt.Errorf("公開鍵 %q の長さが %d バイトではありません", id, ed25519.PublicKeySize)
		}
		if _, dup := set.keys[id]; dup {
			return KeySet{}, fmt.Errorf("公開鍵の ID %q が重複しています", id)
		}
		set.keys[id] = k.Key
	}
	return set, nil
}

// Len は集合に含まれる鍵の数を返す。
func (s KeySet) Len() int { return len(s.keys) }

// IDs は集合に含まれる鍵 ID を昇順で返す（診断・テスト用）。
func (s KeySet) IDs() []string {
	out := make([]string, 0, len(s.keys))
	for id := range s.keys {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// VerifyManifest は更新マニフェストを読み、信頼する鍵のいずれかによる署名が
// 成立することを確認する（更新の流れの (3) の一段目）。
//
// 鍵ローテーション中の二重署名は「いずれか 1 つが成立すれば受理」で扱う。
// 旧鍵を埋め込んだ版・新鍵を埋め込んだ版のどちらも同じマニフェストを受理できる。
func VerifyManifest(raw []byte, keys KeySet) (Manifest, error) {
	m, err := ParseManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if keys.Len() == 0 {
		// 鍵が未登録の状態は「改ざんの疑い」ではないので、原因も文言も分ける。
		return Manifest{}, &Error{Kind: KindNoTrustedKeys,
			msg: "このアプリでは更新を確認できません。配布元から最新版を入手してください"}
	}
	payload := SigningPayload(m)
	for _, sig := range m.Signatures {
		pub, known := keys.keys[sig.KeyID]
		if !known {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(sig.Sig)
		if err != nil || len(decoded) != ed25519.SignatureSize {
			continue
		}
		if ed25519.Verify(pub, payload, decoded) {
			return m, nil
		}
	}
	return Manifest{}, &Error{Kind: KindUntrusted,
		msg: "更新ファイルの発行元を確認できないため、現行版のまま更新を中止しました。時間をおいて再実行してください"}
}

// VerifyAsset は配布物の中身がマニフェストの記載と一致することを確認する
// （更新の流れの (3) の二段目）。サイズ・SHA-256 のどちらか一方でも
// 食い違えば誤りを返す。
func VerifyAsset(data []byte, a Asset) error {
	if int64(len(data)) != a.Size {
		return &Error{Kind: KindSizeMismatch,
			msg:   "更新ファイルが壊れているため、現行版のまま更新を中止しました。時間をおいて再実行してください",
			cause: fmt.Errorf("size = %d, want %d", len(data), a.Size)}
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != a.SHA256 {
		return &Error{Kind: KindHashMismatch,
			msg:   "更新ファイルが壊れているため、現行版のまま更新を中止しました。時間をおいて再実行してください",
			cause: fmt.Errorf("sha256 mismatch")}
	}
	return nil
}

// VerifyAssetStream は配布物を読みながら検証する（全体をメモリに載せない経路。
// 適用側が使う）。読み切った時点でサイズと SHA-256 を照合する。
func VerifyAssetStream(r io.Reader, a Asset) error {
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, a.Size+1))
	if err != nil {
		return &Error{Kind: KindNetwork,
			msg: "更新ファイルを取得できませんでした。通信を確認して再実行してください", cause: err}
	}
	if n != a.Size {
		return &Error{Kind: KindSizeMismatch,
			msg:   "更新ファイルが壊れているため、現行版のまま更新を中止しました。時間をおいて再実行してください",
			cause: fmt.Errorf("size = %d, want %d", n, a.Size)}
	}
	if !bytes.Equal(h.Sum(nil), mustHex(a.SHA256)) {
		return &Error{Kind: KindHashMismatch,
			msg:   "更新ファイルが壊れているため、現行版のまま更新を中止しました。時間をおいて再実行してください",
			cause: fmt.Errorf("sha256 mismatch")}
	}
	return nil
}

// mustHex は検査済みの SHA-256 文字列をバイト列へ戻す。
// ParseManifest / validateSHA256Hex を通ったものだけが渡る。
func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}
