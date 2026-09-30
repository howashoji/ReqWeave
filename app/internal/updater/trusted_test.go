package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// 埋め込みの信頼鍵ファイルが読めること。
// 鍵の集合が空なら更新を適用しない（fail-closed）。
func TestTrustedKeysParses(t *testing.T) {
	set, err := TrustedKeys()
	if err != nil {
		t.Fatalf("埋め込みの信頼鍵を読めない: %v", err)
	}
	// 空でも読めること自体は成立させる。空なら更新が適用されない
	// （TestVerifyManifestFailsClosedWithEmptyKeySet がその振る舞いを固定している）。
	for _, id := range set.IDs() {
		if strings.TrimSpace(id) == "" {
			t.Error("空の鍵 ID が登録されている")
		}
	}
}

// 秘密鍵を思わせるフィールドが埋め込みファイルに無いこと（秘密鍵はアプリに置かない）。
func TestTrustedKeysFileHasNoPrivateMaterial(t *testing.T) {
	text := string(trustedKeysJSON)
	for _, forbidden := range []string{"private", "secret", "seed", "PRIVATE KEY"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Errorf("信頼鍵ファイルに %q を含む記述がある", forbidden)
		}
	}
}

// 埋め込みと同じ経路（parseTrustedKeys）で、鍵を登録した状態の検証が成立すること。
// 実鍵を登録したあとに期待される振る舞いを、合成鍵で固定しておく。
func TestParseTrustedKeysLoadsRegisteredKeys(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = 0x77
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)

	doc := map[string]any{"keys": []any{
		map[string]any{"id": "release-2026", "public_key": base64.StdEncoding.EncodeToString(pub)},
	}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	set, err := parseTrustedKeys(raw)
	if err != nil {
		t.Fatalf("鍵ファイルを読めない: %v", err)
	}
	if set.Len() != 1 || set.IDs()[0] != "release-2026" {
		t.Fatalf("読み取り結果が想定外: %v", set.IDs())
	}

	// この鍵で署名したマニフェストが、埋め込み経路で得た集合で受理されること。
	m := baseManifest(t, []byte("distribution bytes"))
	manifest := signed(t, m, signer{"release-2026", priv})
	if _, err := VerifyManifest(manifest, set); err != nil {
		t.Fatalf("登録した鍵の署名が受理されない: %v", err)
	}
}

func TestParseTrustedKeysRejectsBrokenFile(t *testing.T) {
	cases := map[string][]byte{
		"JSON が壊れている": []byte("{"),
		"base64 が不正":  []byte(`{"keys":[{"id":"a","public_key":"!!!not base64!!!"}]}`),
		"鍵の長さが不正":     []byte(`{"keys":[{"id":"a","public_key":"AAAA"}]}`),
		"ID が空":       []byte(`{"keys":[{"id":"","public_key":"` + base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)) + `"}]}`),
	}
	for name, raw := range cases {
		if _, err := parseTrustedKeys(raw); err == nil {
			t.Errorf("%s: 誤りを返さなかった", name)
		}
	}
}
