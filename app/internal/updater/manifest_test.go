package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func validManifestJSON(t *testing.T) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte("payload"))
	m := Manifest{
		Schema:  SchemaID,
		Version: "1.2.3",
		Assets: []Asset{{
			OS: "darwin", Arch: ArchUniversal,
			URL:    "https://github.com/howashoji/ReqWeave/releases/download/v1.2.3/ReqWeave-macos.zip",
			SHA256: hex.EncodeToString(sum[:]), Size: 7,
		}},
		Signatures: []Signature{{KeyID: "k1", Sig: "AAAA"}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// 受け入れ条件: 版番号・URL・SHA-256・署名・対象 OS/アーキテクチャを持つ JSON を読み書きできること。
func TestParseManifestRoundTrip(t *testing.T) {
	m, err := ParseManifest(validManifestJSON(t))
	if err != nil {
		t.Fatalf("正しいマニフェストが読めない: %v", err)
	}
	if m.Version != "1.2.3" || len(m.Assets) != 1 || m.Assets[0].OS != "darwin" {
		t.Fatalf("読み取り結果が想定外: %+v", m)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseManifest(raw)
	if err != nil {
		t.Fatalf("書き出したものが読めない: %v", err)
	}
	if again.Assets[0].SHA256 != m.Assets[0].SHA256 {
		t.Fatal("往復でハッシュが変わった")
	}
}

// 受け入れ条件: 必須フィールド欠落の JSON は読み込み時に誤りになること。
func TestParseManifestRejectsInvalid(t *testing.T) {
	base := map[string]any{}
	if err := json.Unmarshal(validManifestJSON(t), &base); err != nil {
		t.Fatal(err)
	}
	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(validManifestJSON(t), &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	asset := func(m map[string]any) map[string]any {
		return m["assets"].([]any)[0].(map[string]any)
	}

	cases := map[string]struct {
		raw  []byte
		kind Kind
	}{
		"JSON として壊れている":        {[]byte("{not json"), KindMalformed},
		"schema が無い":           {mutate(func(m map[string]any) { delete(m, "schema") }), KindUnsupportedSchema},
		"schema が別形式":          {mutate(func(m map[string]any) { m["schema"] = "other/9" }), KindUnsupportedSchema},
		"version が無い":          {mutate(func(m map[string]any) { delete(m, "version") }), KindMalformed},
		"version が semver でない": {mutate(func(m map[string]any) { m["version"] = "v1.2" }), KindMalformed},
		"assets が空":            {mutate(func(m map[string]any) { m["assets"] = []any{} }), KindMalformed},
		"assets が無い":           {mutate(func(m map[string]any) { delete(m, "assets") }), KindMalformed},
		"signatures が空":        {mutate(func(m map[string]any) { m["signatures"] = []any{} }), KindMalformed},
		"signatures が無い":       {mutate(func(m map[string]any) { delete(m, "signatures") }), KindMalformed},
		"署名の key_id が空":        {mutate(func(m map[string]any) { m["signatures"].([]any)[0].(map[string]any)["key_id"] = "" }), KindMalformed},
		"署名の sig が空":           {mutate(func(m map[string]any) { m["signatures"].([]any)[0].(map[string]any)["sig"] = "" }), KindMalformed},
		"os が空":                {mutate(func(m map[string]any) { asset(m)["os"] = "" }), KindMalformed},
		"arch が空":              {mutate(func(m map[string]any) { asset(m)["arch"] = "" }), KindMalformed},
		"url が http":           {mutate(func(m map[string]any) { asset(m)["url"] = "http://example.com/a.zip" }), KindMalformed},
		"url が file":           {mutate(func(m map[string]any) { asset(m)["url"] = "file:///tmp/a.zip" }), KindMalformed},
		"sha256 が短い":           {mutate(func(m map[string]any) { asset(m)["sha256"] = "abcd" }), KindMalformed},
		"sha256 が大文字":          {mutate(func(m map[string]any) { asset(m)["sha256"] = strings.ToUpper(strings.Repeat("ab", 32)) }), KindMalformed},
		"sha256 が 16 進でない":     {mutate(func(m map[string]any) { asset(m)["sha256"] = strings.Repeat("g", 64) }), KindMalformed},
		"size が 0":             {mutate(func(m map[string]any) { asset(m)["size"] = 0 }), KindMalformed},
		"size が負":              {mutate(func(m map[string]any) { asset(m)["size"] = -1 }), KindMalformed},
	}
	for name, c := range cases {
		_, err := ParseManifest(c.raw)
		if err == nil {
			t.Errorf("%s: 受理された", name)
			continue
		}
		if k, _ := KindOf(err); k != c.kind {
			t.Errorf("%s: Kind = %q, want %q（%v）", name, k, c.kind, err)
		}
	}
}

func TestParseManifestRejectsDuplicateAsset(t *testing.T) {
	m, err := ParseManifest(validManifestJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	m.Assets = append(m.Assets, m.Assets[0])
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(raw); err == nil {
		t.Fatal("同じ os/arch の配布物が重複していても受理された")
	}
}

// 未知のフィールドは無視する（将来の形式追加で古いアプリが壊れないようにする）。
func TestParseManifestIgnoresUnknownFields(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(validManifestJSON(t), &m); err != nil {
		t.Fatal(err)
	}
	m["released_at"] = "2026-09-01T00:00:00Z"
	m["assets"].([]any)[0].(map[string]any)["notes"] = "第2版"
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(raw); err != nil {
		t.Fatalf("未知のフィールドで拒否された: %v", err)
	}
}

// SigningPayload は配布物の並び順に依存しない（署名ツールと検証側で順序がずれても同じ署名対象になる）。
func TestSigningPayloadIsOrderIndependent(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	h := hex.EncodeToString(sum[:])
	a := Asset{OS: "darwin", Arch: ArchUniversal, URL: "https://example.com/a", SHA256: h, Size: 1}
	b := Asset{OS: "windows", Arch: "amd64", URL: "https://example.com/b", SHA256: h, Size: 2}

	p1 := SigningPayload(Manifest{Schema: SchemaID, Version: "1.0.0", Assets: []Asset{a, b}})
	p2 := SigningPayload(Manifest{Schema: SchemaID, Version: "1.0.0", Assets: []Asset{b, a}})
	if string(p1) != string(p2) {
		t.Fatalf("並び順で署名対象が変わる:\n%s\n---\n%s", p1, p2)
	}
	// 署名対象に署名そのものは含まない（署名を足しても対象は変わらない）。
	p3 := SigningPayload(Manifest{Schema: SchemaID, Version: "1.0.0", Assets: []Asset{a, b},
		Signatures: []Signature{{KeyID: "k", Sig: "s"}}})
	if string(p1) != string(p3) {
		t.Fatal("署名が署名対象に含まれている")
	}
}

// SigningPayload はフィールドが 1 つでも違えば別の署名対象になる。
func TestSigningPayloadChangesWithEveryField(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	h := hex.EncodeToString(sum[:])
	base := Manifest{Schema: SchemaID, Version: "1.0.0",
		Assets: []Asset{{OS: "darwin", Arch: ArchUniversal, URL: "https://example.com/a", SHA256: h, Size: 1}}}
	want := string(SigningPayload(base))

	mutations := map[string]func(m *Manifest){
		"version": func(m *Manifest) { m.Version = "1.0.1" },
		"os":      func(m *Manifest) { m.Assets[0].OS = "windows" },
		"arch":    func(m *Manifest) { m.Assets[0].Arch = "amd64" },
		"url":     func(m *Manifest) { m.Assets[0].URL = "https://example.com/b" },
		"sha256":  func(m *Manifest) { m.Assets[0].SHA256 = strings.Repeat("0", 64) },
		"size":    func(m *Manifest) { m.Assets[0].Size = 2 },
	}
	for name, mutate := range mutations {
		m := base
		m.Assets = append([]Asset{}, base.Assets...)
		mutate(&m)
		if string(SigningPayload(m)) == want {
			t.Errorf("%s を変えても署名対象が変わらない", name)
		}
	}
}

func TestAssetSelection(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	h := hex.EncodeToString(sum[:])
	m := Manifest{Assets: []Asset{
		{OS: "darwin", Arch: ArchUniversal, URL: "https://e/x", SHA256: h, Size: 1},
		{OS: "windows", Arch: "amd64", URL: "https://e/y", SHA256: h, Size: 1},
	}}
	// macOS は Universal Binary が arm64 / amd64 のどちらからも選ばれる。
	for _, arch := range []string{"arm64", "amd64", ArchUniversal} {
		if a, ok := m.Asset("darwin", arch); !ok || a.Arch != ArchUniversal {
			t.Errorf("darwin/%s で universal が選ばれない", arch)
		}
	}
	if a, ok := m.Asset("windows", "amd64"); !ok || a.URL != "https://e/y" {
		t.Error("windows/amd64 の完全一致が選ばれない")
	}
	if _, ok := m.Asset("linux", "amd64"); ok {
		t.Error("対象外の OS で配布物が返った")
	}
}
