package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/updater"
)

type keyPair struct {
	ID   string
	Priv ed25519.PrivateKey
	Pub  updater.PublicKey
}

// newKeyPair は本ツールの生成経路（generateKeyPair）で鍵を作り、
// 検証側が使える形へほどく。実鍵は使わない。
func newKeyPair(t *testing.T) keyPair {
	t.Helper()
	priv, pub, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	material, err := base64.StdEncoding.DecodeString(priv.Material)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(pub.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if priv.ID != pub.ID {
		t.Fatalf("秘密鍵の ID %q と公開鍵の ID %q が違う", priv.ID, pub.ID)
	}
	return keyPair{ID: priv.ID, Priv: ed25519.PrivateKey(material),
		Pub: updater.PublicKey{ID: pub.ID, Key: pubBytes}}
}

func signerOf(k keyPair) struct {
	ID   string
	Priv ed25519.PrivateKey
} {
	return struct {
		ID   string
		Priv ed25519.PrivateKey
	}{ID: k.ID, Priv: k.Priv}
}

// writeAsset はテスト用の配布物ファイルを書く。
func writeAsset(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 受け入れ条件: ツールが作ったマニフェストをアップデータの検証器（internal/updater）が受理すること（生成→検証の往復）。
func TestSignedManifestIsAcceptedByVerifier(t *testing.T) {
	dir := t.TempDir()
	mac := writeAsset(t, dir, "ReqWeave-macos.zip", "macos distribution bytes")
	win := writeAsset(t, dir, "ReqWeave-windows.zip", "windows distribution bytes")

	assets, err := assetsFromSpecs("0.2.0", []string{
		"darwin/universal=" + mac,
		"windows/amd64=" + win,
	})
	if err != nil {
		t.Fatal(err)
	}
	k := newKeyPair(t)
	m, err := buildManifest("0.2.0", assets, []struct {
		ID   string
		Priv ed25519.PrivateKey
	}{signerOf(k)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	keys, err := updater.NewKeySet(k.Pub)
	if err != nil {
		t.Fatal(err)
	}
	got, err := updater.VerifyManifest(raw, keys)
	if err != nil {
		t.Fatalf("自分で署名したマニフェストが検証器に拒否された: %v", err)
	}
	if got.Version != "0.2.0" || len(got.Assets) != 2 {
		t.Fatalf("読み取り結果が想定外: %+v", got)
	}

	// 配布物の中身も検証器の照合を通ること（ハッシュ・サイズが実ファイル由来であること）。
	for _, spec := range []struct {
		goos, arch, path string
	}{{"darwin", updater.ArchUniversal, mac}, {"windows", "amd64", win}} {
		a, ok := got.Asset(spec.goos, spec.arch)
		if !ok {
			t.Fatalf("%s/%s の配布物が無い", spec.goos, spec.arch)
		}
		data, err := os.ReadFile(spec.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := updater.VerifyAsset(data, a); err != nil {
			t.Fatalf("%s の照合に失敗: %v", spec.path, err)
		}
	}
}

// 受け入れ条件: 二重署名のマニフェストが、新旧どちらの鍵を埋め込んだ版でも受理されること。
func TestDualSignedManifestAcceptedByEitherKey(t *testing.T) {
	dir := t.TempDir()
	asset := writeAsset(t, dir, "ReqWeave-macos.zip", "distribution bytes")
	assets, err := assetsFromSpecs("0.3.0", []string{"darwin/universal=" + asset})
	if err != nil {
		t.Fatal(err)
	}
	oldKey, newKey := newKeyPair(t), newKeyPair(t)

	m, err := buildManifest("0.3.0", assets, []struct {
		ID   string
		Priv ed25519.PrivateKey
	}{signerOf(oldKey), signerOf(newKey)})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Signatures) != 2 {
		t.Fatalf("署名が %d 件、want 2", len(m.Signatures))
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for name, only := range map[string]keyPair{"旧鍵埋め込み版": oldKey, "新鍵埋め込み版": newKey} {
		keys, err := updater.NewKeySet(only.Pub)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := updater.VerifyManifest(raw, keys); err != nil {
			t.Errorf("%s が二重署名マニフェストを受理しない: %v", name, err)
		}
	}
}

func TestBuildManifestRejectsBadInput(t *testing.T) {
	k := newKeyPair(t)
	assets := []updater.Asset{{OS: "darwin", Arch: updater.ArchUniversal,
		URL: "https://example.com/a", SHA256: strings.Repeat("a", 64), Size: 1}}

	if _, err := buildManifest("0.1.0", assets, nil); err == nil {
		t.Error("鍵なしで署名できてしまった")
	}
	dup := []struct {
		ID   string
		Priv ed25519.PrivateKey
	}{signerOf(k), signerOf(k)}
	if _, err := buildManifest("0.1.0", assets, dup); err == nil {
		t.Error("同じ鍵 ID の二重指定を受け付けた")
	}
}

// 受け入れ条件: 秘密鍵の値が標準出力・エラー文言に出ないこと（合成鍵で検査）。
func TestKeyMaterialNeverAppearsInOutput(t *testing.T) {
	dir := t.TempDir()
	priv, pub, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "release.key")
	if err := writeKeyFile(keyPath, priv, ""); err != nil {
		t.Fatal(err)
	}

	// 誤りの文言に鍵素材が出ないこと（読めない・壊れている・長さ不正の 3 経路）。
	broken := filepath.Join(dir, "broken.key")
	if err := os.WriteFile(broken, []byte(`{"id":"x","material":"`+priv.Material[:10]+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "missing.key"), broken} {
		if _, _, err := loadKeyFile(p); err == nil {
			t.Errorf("%s: 誤りを返さなかった", p)
		} else if strings.Contains(err.Error(), priv.Material[:10]) {
			t.Errorf("%s: 誤りの文言に鍵素材が出ている: %v", p, err)
		}
	}

	// 正常系で読めること、公開鍵の行に鍵素材が出ないこと。
	id, loaded, err := loadKeyFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if id != priv.ID || len(loaded) != ed25519.PrivateKeySize {
		t.Fatalf("読み取り結果が想定外: id=%q len=%d", id, len(loaded))
	}
	entry, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(entry), priv.Material) {
		t.Fatalf("公開鍵の行に秘密鍵の値が含まれる: %s", entry)
	}
	// 公開鍵は秘密鍵の後半 32 バイトと同じ値になる。行に載るのは公開鍵だけであること。
	if !strings.Contains(string(entry), pub.PublicKey) {
		t.Fatalf("公開鍵の行に公開鍵が無い: %s", entry)
	}
}

// 受け入れ条件: 秘密鍵をコマンドライン引数で受け取る経路が存在しないこと。
func TestNoFlagAcceptsKeyMaterial(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	// 鍵の「値」を受け取るフラグを作っていないこと。受け取るのはパスだけ。
	for _, forbidden := range []string{
		`fs.String("key"`, `fs.String("private`, `fs.String("secret`, `fs.String("material`,
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("鍵の値を受け取るフラグがある: %s", forbidden)
		}
	}
	// 環境変数から受け取るのは「パス」であること（名前で意図を固定する）。
	if !strings.Contains(text, `keyFileEnv = "REQWEAVE_RELEASE_KEY_FILE"`) {
		t.Error("鍵ファイルのパスを受け取る環境変数の定義が見当たらない")
	}
}

// 秘密鍵をリポジトリ配下へ保存できないこと（誤って版管理へ入る事故の機械的な防止）。
func TestWriteKeyFileRefusesRepository(t *testing.T) {
	repo := t.TempDir()
	priv, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(repo, "app", "internal", "updater", "release.key")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeKeyFile(inside, priv, repo); err == nil {
		t.Fatal("リポジトリ配下へ秘密鍵を保存できてしまった")
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatal("拒否したはずのファイルが作られている")
	}

	// リポジトリの外なら保存でき、権限は 0600。
	outside := filepath.Join(t.TempDir(), "release.key")
	if err := writeKeyFile(outside, priv, repo); err != nil {
		t.Fatalf("リポジトリ外への保存が拒否された: %v", err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("秘密鍵の権限 = %v, want 0600", info.Mode().Perm())
	}
	// 既存ファイルを上書きしない。
	if err := writeKeyFile(outside, priv, repo); err == nil {
		t.Fatal("既存の鍵を上書きしてしまった")
	}
}

func TestParseAssetSpec(t *testing.T) {
	got, err := parseAssetSpec("darwin/universal=build/bin/ReqWeave-macos.zip")
	if err != nil {
		t.Fatal(err)
	}
	if got.OS != "darwin" || got.Arch != "universal" || got.Path != "build/bin/ReqWeave-macos.zip" {
		t.Fatalf("解釈結果が想定外: %+v", got)
	}
	for _, bad := range []string{"darwin/universal", "=path", "darwin=path", "/universal=path", "darwin/=path", "darwin/universal="} {
		if _, err := parseAssetSpec(bad); err == nil {
			t.Errorf("不正な指定 %q を受け付けた", bad)
		}
	}
}

func TestHashAssetUsesRealFile(t *testing.T) {
	dir := t.TempDir()
	p := writeAsset(t, dir, "a.zip", "hello")
	sum, size, err := hashAsset(p)
	if err != nil {
		t.Fatal(err)
	}
	// "hello" の SHA-256（外部の既知値。実装の出力からコピーしない）。
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if sum != want {
		t.Fatalf("SHA-256 = %q, want %q", sum, want)
	}
	if size != 5 {
		t.Fatalf("サイズ = %d, want 5", size)
	}
	// 空ファイルは配布物として認めない。
	if _, _, err := hashAsset(writeAsset(t, dir, "empty.zip", "")); err == nil {
		t.Error("空の配布物を受け付けた")
	}
	if _, _, err := hashAsset(filepath.Join(dir, "missing.zip")); err == nil {
		t.Error("存在しない配布物を受け付けた")
	}
}

// 配布物 URL がアプリ側の取得先と同じリポジトリを指すこと。
func TestDownloadURLMatchesUpdaterRepository(t *testing.T) {
	got := downloadURL("0.2.0", "ReqWeave-macos.zip")
	want := "https://github.com/" + updater.ReleasesOwner + "/" + updater.ReleasesRepo +
		"/releases/download/v0.2.0/ReqWeave-macos.zip"
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "https://") {
		t.Fatalf("URL が https でない: %q", got)
	}
}

// 受け入れ条件: リポジトリに秘密鍵ファイル・鍵素材が存在しないこと。
func TestRepositoryContainsNoPrivateKeyMaterial(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	// 鍵ファイルらしい拡張子・名前
	badNames := []string{".key", ".pem", "id_ed25519", "id_rsa", ".p12", ".pfx"}
	// 秘密鍵らしい中身（PEM の見出し）。
	// `"material"` のような一般的な語では判定しない（フロントエンドの生成物に当たる）。
	badContents := []string{"PRIVATE KEY-----", "BEGIN OPENSSH PRIVATE"}

	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 読めない場所は飛ばす（.git の一時ファイル等）
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "site" || base == ".gates" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for _, bad := range badNames {
			if strings.HasSuffix(strings.ToLower(info.Name()), bad) {
				t.Errorf("鍵ファイルらしいファイルがリポジトリにある: %s", rel)
			}
		}
		if info.Size() > 1<<20 {
			return nil
		}
		// Go / テストのソースは検査対象から外す（文字列としての言及があるため）。
		if strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, bad := range badContents {
			if strings.Contains(string(b), bad) {
				t.Errorf("秘密鍵らしい内容を含むファイルがある: %s（%q）", rel, bad)
			}
		}
		// 本ツールの鍵ファイル形式そのもの（id + 64 バイトの material）を検出する。
		if looksLikeSigningKey(b) {
			t.Errorf("本ツールの秘密鍵ファイルがリポジトリにある: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// looksLikeSigningKey は本ツールの秘密鍵ファイル形式かを判定する。
func looksLikeSigningKey(b []byte) bool {
	var f signingKeyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return false
	}
	if f.ID == "" || f.Material == "" {
		return false
	}
	material, err := base64.StdEncoding.DecodeString(f.Material)
	if err != nil {
		return false
	}
	return len(material) == ed25519.PrivateKeySize
}
