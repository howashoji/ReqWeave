package updater

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// testKey は決定的な鍵ペアを作る（実鍵は使わない）。
func testKey(t *testing.T, seedByte byte) (PublicKey, ed25519.PrivateKey) {
	t.Helper()
	seed := bytes.Repeat([]byte{seedByte}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	return PublicKey{ID: "key-" + hex.EncodeToString([]byte{seedByte}), Key: priv.Public().(ed25519.PublicKey)}, priv
}

// asset はバイト列から整合の取れた Asset を作る。
func asset(t *testing.T, goos, arch string, data []byte) Asset {
	t.Helper()
	sum := sha256.Sum256(data)
	return Asset{
		OS: goos, Arch: arch,
		URL:    "https://github.com/howashoji/ReqWeave/releases/download/v0.2.0/ReqWeave-" + goos + ".zip",
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(data)),
	}
}

// signer は署名に使う鍵 1 本。
type signer struct {
	ID   string
	Priv ed25519.PrivateKey
}

// signed は指定の鍵で署名したマニフェスト JSON を返す。
func signed(t *testing.T, m Manifest, signers ...signer) []byte {
	t.Helper()
	m.Signatures = nil
	payload := SigningPayload(m)
	for _, s := range signers {
		m.Signatures = append(m.Signatures, Signature{
			KeyID: s.ID,
			Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(s.Priv, payload)),
		})
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func baseManifest(t *testing.T, data []byte) Manifest {
	t.Helper()
	return Manifest{
		Schema:  SchemaID,
		Version: "0.2.0",
		Assets: []Asset{
			asset(t, "darwin", ArchUniversal, data),
			asset(t, "windows", "amd64", append(append([]byte{}, data...), 'w')),
		},
	}
}

// 受け入れ条件: 埋め込み公開鍵で署名検証が成立するマニフェストが受理されること。
func TestVerifyManifestAcceptsTrustedSignature(t *testing.T) {
	data := []byte("distribution bytes")
	pub, priv := testKey(t, 0x11)
	raw := signed(t, baseManifest(t, data), signer{pub.ID, priv})

	keys, err := NewKeySet(pub)
	if err != nil {
		t.Fatal(err)
	}
	m, err := VerifyManifest(raw, keys)
	if err != nil {
		t.Fatalf("信頼鍵の署名が受理されない: %v", err)
	}
	if m.Version != "0.2.0" {
		t.Fatalf("Version = %q, want 0.2.0", m.Version)
	}
}

// 受け入れ条件: 署名を 1 バイト改変したとき検証が不合格になること。
func TestVerifyManifestRejectsTamperedSignature(t *testing.T) {
	data := []byte("distribution bytes")
	pub, priv := testKey(t, 0x11)
	raw := signed(t, baseManifest(t, data), signer{pub.ID, priv})

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signatures[0].Sig)
	if err != nil {
		t.Fatal(err)
	}
	sig[0] ^= 0x01 // 1 バイトだけ反転
	m.Signatures[0].Sig = base64.StdEncoding.EncodeToString(sig)
	tampered, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	keys, _ := NewKeySet(pub)
	if _, err := VerifyManifest(tampered, keys); err == nil {
		t.Fatal("改変した署名が受理された")
	} else if k, _ := KindOf(err); k != KindUntrusted {
		t.Fatalf("Kind = %q, want %q", k, KindUntrusted)
	}
}

// 受け入れ条件: マニフェスト本文（版番号・URL・ハッシュ）の改変で署名検証が不合格になること。
func TestVerifyManifestRejectsTamperedPayload(t *testing.T) {
	data := []byte("distribution bytes")
	pub, priv := testKey(t, 0x11)
	keys, _ := NewKeySet(pub)

	mutations := map[string]func(m *Manifest){
		"版番号": func(m *Manifest) { m.Version = "9.9.9" },
		"URL": func(m *Manifest) {
			m.Assets[0].URL = "https://evil.example.com/ReqWeave.zip"
		},
		"ハッシュ": func(m *Manifest) {
			m.Assets[0].SHA256 = strings.Repeat("a", 64)
		},
		"サイズ": func(m *Manifest) { m.Assets[0].Size = m.Assets[0].Size + 1 },
	}
	for name, mutate := range mutations {
		raw := signed(t, baseManifest(t, data), signer{pub.ID, priv})
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		mutate(&m)
		tampered, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyManifest(tampered, keys); err == nil {
			t.Errorf("%s を改変したマニフェストが受理された", name)
		}
	}
}

// 受け入れ条件: 二重署名を旧鍵版・新鍵版の双方が受理すること（鍵ローテーション）。
func TestVerifyManifestAcceptsDualSignatureWithEitherKey(t *testing.T) {
	data := []byte("distribution bytes")
	oldPub, oldPriv := testKey(t, 0x21)
	newPub, newPriv := testKey(t, 0x22)
	raw := signed(t, baseManifest(t, data), signer{oldPub.ID, oldPriv}, signer{newPub.ID, newPriv})

	for name, only := range map[string]PublicKey{"旧鍵埋め込み版": oldPub, "新鍵埋め込み版": newPub} {
		keys, err := NewKeySet(only)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyManifest(raw, keys); err != nil {
			t.Errorf("%s が二重署名マニフェストを受理しない: %v", name, err)
		}
	}
	// 移行完了後（新旧どちらも埋め込む版）も受理する。
	both, _ := NewKeySet(oldPub, newPub)
	if _, err := VerifyManifest(raw, both); err != nil {
		t.Errorf("新旧両鍵を埋め込んだ版が受理しない: %v", err)
	}
}

// 受け入れ条件: どちらの鍵でも検証できないマニフェストが不合格になること。
func TestVerifyManifestRejectsUnknownKey(t *testing.T) {
	data := []byte("distribution bytes")
	attackerPub, attackerPriv := testKey(t, 0x31)
	trustedPub, _ := testKey(t, 0x32)
	raw := signed(t, baseManifest(t, data), signer{attackerPub.ID, attackerPriv})

	keys, _ := NewKeySet(trustedPub)
	if _, err := VerifyManifest(raw, keys); err == nil {
		t.Fatal("信頼していない鍵の署名が受理された")
	} else if k, _ := KindOf(err); k != KindUntrusted {
		t.Fatalf("Kind = %q, want %q", k, KindUntrusted)
	}

	// 鍵 ID だけ信頼鍵に似せても、鍵本体が違えば通らない。
	spoof := signed(t, baseManifest(t, data), signer{trustedPub.ID, attackerPriv})
	if _, err := VerifyManifest(spoof, keys); err == nil {
		t.Fatal("鍵 ID を偽装した署名が受理された")
	}
}

// 信頼鍵が空のとき、どの署名も受理しない（fail-closed）。
func TestVerifyManifestFailsClosedWithEmptyKeySet(t *testing.T) {
	data := []byte("distribution bytes")
	pub, priv := testKey(t, 0x41)
	raw := signed(t, baseManifest(t, data), signer{pub.ID, priv})

	empty, err := NewKeySet()
	if err != nil {
		t.Fatal(err)
	}
	err = func() error { _, e := VerifyManifest(raw, empty); return e }()
	if err == nil {
		t.Fatal("信頼鍵が空なのに受理された")
	}
	// 鍵未登録は「改ざんの疑い」と区別して扱う（表示文言も次の行動も違う）。
	if k, _ := KindOf(err); k != KindNoTrustedKeys {
		t.Fatalf("Kind = %q, want %q", k, KindNoTrustedKeys)
	}
	// 同じ鍵集合で、署名が壊れている場合は改ざん側の種別になる（両者が潰し合っていないこと）。
	trusted, _ := NewKeySet(pub)
	other, otherPriv := testKey(t, 0x42)
	_ = other
	bad := signed(t, baseManifest(t, data), signer{pub.ID, otherPriv})
	if _, err := VerifyManifest(bad, trusted); err == nil {
		t.Fatal("鍵の合わない署名が受理された")
	} else if k, _ := KindOf(err); k != KindUntrusted {
		t.Fatalf("Kind = %q, want %q", k, KindUntrusted)
	}
}

// 受け入れ条件: 配布物を 1 バイト改変したとき SHA-256 照合が不一致になること。
func TestVerifyAssetDetectsSingleByteChange(t *testing.T) {
	data := []byte("distribution bytes for the update package")
	a := asset(t, "darwin", ArchUniversal, data)

	if err := VerifyAsset(data, a); err != nil {
		t.Fatalf("正しい配布物が不合格になった: %v", err)
	}
	for i := range data {
		mutated := append([]byte{}, data...)
		mutated[i] ^= 0x01
		err := VerifyAsset(mutated, a)
		if err == nil {
			t.Fatalf("%d バイト目を改変した配布物が受理された", i)
		}
		if k, _ := KindOf(err); k != KindHashMismatch {
			t.Fatalf("%d バイト目: Kind = %q, want %q", i, k, KindHashMismatch)
		}
	}
}

func TestVerifyAssetDetectsSizeChange(t *testing.T) {
	data := []byte("distribution bytes")
	a := asset(t, "darwin", ArchUniversal, data)
	err := VerifyAsset(append(append([]byte{}, data...), 'x'), a)
	if err == nil {
		t.Fatal("長さの違う配布物が受理された")
	}
	if k, _ := KindOf(err); k != KindSizeMismatch {
		t.Fatalf("Kind = %q, want %q", k, KindSizeMismatch)
	}
}

func TestVerifyAssetStreamMatchesVerifyAsset(t *testing.T) {
	data := []byte("distribution bytes for streaming verification")
	a := asset(t, "darwin", ArchUniversal, data)

	if err := VerifyAssetStream(bytes.NewReader(data), a); err != nil {
		t.Fatalf("正しい配布物が不合格になった: %v", err)
	}
	mutated := append([]byte{}, data...)
	mutated[3] ^= 0x01
	if err := VerifyAssetStream(bytes.NewReader(mutated), a); err == nil {
		t.Fatal("改変した配布物が受理された")
	}
	// 途中で切れた場合はサイズ不一致。
	if err := VerifyAssetStream(bytes.NewReader(data[:len(data)-1]), a); err == nil {
		t.Fatal("短い配布物が受理された")
	}
	// 余分に長い場合もサイズ不一致（LimitReader は Size+1 まで読む）。
	if err := VerifyAssetStream(bytes.NewReader(append(append([]byte{}, data...), 'x')), a); err == nil {
		t.Fatal("長い配布物が受理された")
	}
}

// 受け入れ条件: 検証不合格時のエラーが URL・鍵素材・ハッシュの生値を含まないこと。
func TestErrorMessagesDoNotLeakDetails(t *testing.T) {
	data := []byte("distribution bytes")
	attackerPub, attackerPriv := testKey(t, 0x51)
	trustedPub, _ := testKey(t, 0x52)
	keys, _ := NewKeySet(trustedPub)
	m := baseManifest(t, data)
	raw := signed(t, m, signer{attackerPub.ID, attackerPriv})

	secrets := []string{
		m.Assets[0].URL,
		m.Assets[0].SHA256,
		base64.StdEncoding.EncodeToString(trustedPub.Key),
		base64.StdEncoding.EncodeToString(attackerPub.Key),
	}

	var msgs []string
	if _, err := VerifyManifest(raw, keys); err != nil {
		msgs = append(msgs, err.Error())
	}
	if err := VerifyAsset([]byte("wrong"), m.Assets[0]); err != nil {
		msgs = append(msgs, err.Error())
	}
	if err := VerifyAssetStream(bytes.NewReader([]byte("wrong")), m.Assets[0]); err != nil {
		msgs = append(msgs, err.Error())
	}
	if len(msgs) != 3 {
		t.Fatalf("失敗するはずの 3 経路のうち %d 件しか失敗しなかった", len(msgs))
	}
	for _, msg := range msgs {
		for _, secret := range secrets {
			if strings.Contains(msg, secret) {
				t.Errorf("利用者向け文言に内部情報が出ている: %q", msg)
			}
		}
		if strings.Contains(msg, "https://") || strings.Contains(msg, "sha256") {
			t.Errorf("利用者向け文言に URL / 内部用語が出ている: %q", msg)
		}
	}
}

func TestNewKeySetRejectsBadKeys(t *testing.T) {
	pub, _ := testKey(t, 0x61)
	if _, err := NewKeySet(PublicKey{ID: "", Key: pub.Key}); err == nil {
		t.Error("ID が空の鍵を受け付けた")
	}
	if _, err := NewKeySet(PublicKey{ID: "short", Key: []byte{1, 2, 3}}); err == nil {
		t.Error("長さの不正な鍵を受け付けた")
	}
	if _, err := NewKeySet(pub, pub); err == nil {
		t.Error("ID の重複を受け付けた")
	}
}
