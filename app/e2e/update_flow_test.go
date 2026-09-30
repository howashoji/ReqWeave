//go:build e2e

// e2e（自動更新の実機確認）: 実際の配布物を利用者領域へ置いた状態から、
// 更新の検知 → 完全性検証 → 適用 → 再起動までを実機で通す（改ざんされた配布物を適用しないことも含む）。
//
// 通信そのもの（HTTPS での取得）は `internal/updater/apply_integration_test.go` が
// TLS サーバと実ファイルで検証している（test-integration 段。同一マシンで実行）。
// 本テストが担うのは、そこでは代替できない **実際の .app バンドルを実機の利用者領域で
// 置き換え、置き換わったアプリが起動すること**。
//
// 実行: make -C app test-e2e（先に make -C app dist が要る）

package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/appversion"
	"github.com/howashoji/ReqWeave/app/internal/updater"
)

// newVersion は「配信された新版」の版番号。現行版（VERSION の正本）より必ず大きくする。
const newVersion = "99.0.0"

// signedManifest は配布物 1 件ぶんの更新マニフェストを作り、生成した鍵で署名して返す。
// 署名鍵は本テスト内で生成する（実運用の秘密鍵には触れない）。
func signedManifest(t *testing.T, assetPath, url, version string) (raw []byte, keys updater.KeySet, asset updater.Asset) {
	t.Helper()
	data, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("配布物を読めません: %v", err)
	}
	sum := sha256.Sum256(data)
	asset = updater.Asset{
		OS: "darwin", Arch: updater.ArchUniversal, URL: url,
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data)),
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := updater.Manifest{Schema: updater.SchemaID, Version: version, Assets: []updater.Asset{asset}}
	sig := ed25519.Sign(priv, updater.SigningPayload(m))
	m.Signatures = []updater.Signature{{
		KeyID: "e2e-test-key", Sig: base64.StdEncoding.EncodeToString(sig),
	}}
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	keys, err = updater.NewKeySet(updater.PublicKey{ID: "e2e-test-key", Key: pub})
	if err != nil {
		t.Fatal(err)
	}
	return raw, keys, asset
}

// repackage は取り出し済みの配布物一式から、更新配信用の配布物を作り直す。
// 中身も形式も make dist と同じ（アプリ本体 1 個 + 手順書、**dmg**）。
//
// **ここで zip を作らないこと**。zip にすると更新の適用が zip 経路を通ってしまい、
// 実際に配信する dmg 経路（internal/updater.extractDMG）が e2e で一度も動かない。
func repackage(t *testing.T, srcDir, outName string) string {
	t.Helper()
	// **毎回まっさらな場所へ作ってから置き場所へ移す**。
	// 同じパスへ `-ov` で作り直すと、直前の dmg 操作と競合して
	// `hdiutil create` が exit 1 で落ちることがある（gates の連続実行で実測）。
	out := filepath.Join(srcDir, outName)
	staging := filepath.Join(t.TempDir(), "made.dmg")
	root := filepath.Join(t.TempDir(), "dmgroot")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ReqWeave.app", "初回起動の手順.md"} {
		if o, err := exec.Command("ditto", filepath.Join(srcDir, name),
			filepath.Join(root, name)).CombinedOutput(); err != nil {
			t.Fatalf("更新配信用の中身を用意できません（%s）: %v\n%s", name, err, o)
		}
	}
	if o, err := exec.Command("hdiutil", "create", "-volname", "ReqWeave", "-srcfolder", root,
		"-format", "UDZO", staging).CombinedOutput(); err != nil {
		t.Fatalf("更新配信用の配布物を作れません: %v\n%s", err, o)
	}
	if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
		t.Fatalf("古い配布物を消せません: %v", err)
	}
	if err := os.Rename(staging, out); err != nil {
		t.Fatalf("配布物を置き場所へ移せません: %v", err)
	}
	// `hdiutil create` の切り離し取りこぼしを持ち越さない。
	t.Cleanup(func() {
		sweepMountLeak(t, staging)
		sweepMountLeak(t, out)
	})
	return out
}

// TestUpdateAppliesToRealBundleAndRestarts は、実機の利用者領域に置いた実際の配布物へ
// 新版を適用し、置き換わったアプリが起動することを確認する。
func TestUpdateAppliesToRealBundleAndRestarts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("root で実行している。管理者権限なしの確認にならないため、通常の利用者で実行すること")
	}
	appBundle, deployDir := deployDistribution(t)

	// 現行版が起動することを先に確かめる（更新前の基準）。
	home := userAreaDir(t, ".reqweave-e2e-home")
	before, _ := launchAndWaitReady(t, launchOptions{
		bin: filepath.Join(appBundle, "Contents", "MacOS", "ReqWeave"), home: home, timeout: 60 * time.Second})
	beforeInfo, err := os.Stat(appBundle)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("更新前の起動: %v", before.Round(time.Millisecond))

	// 「配信された新版」を別の場所に用意する（別ディレクトリ = 適用後に実体が入れ替わったと判別できる）。
	releaseDir := userAreaDir(t, ".reqweave-e2e-release")
	extractDist(t, distArchive(t), releaseDir)
	archive := repackage(t, releaseDir, "ReqWeave-"+newVersion+"-macos.dmg")

	// 検知: マニフェストの署名検証と版比較。
	raw, keys, asset := signedManifest(t, archive, "https://example.invalid/ReqWeave.zip", newVersion)
	m, err := updater.VerifyManifest(raw, keys)
	if err != nil {
		t.Fatalf("更新マニフェストの検証に失敗: %v", err)
	}
	current, err := appversion.Current()
	if err != nil {
		t.Fatal(err)
	}
	latest, err := appversion.ParseSemver(m.Version)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Compare(current) <= 0 {
		t.Fatalf("新版として検知されない: 配信 %s / 現行 %s", m.Version, current)
	}
	t.Logf("更新を検知: 現行 %s → 配信 %s", current, m.Version)

	// 完全性検証: 実ファイルの中身がマニフェストの記載と一致すること（手順3 の二段目）。
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := updater.VerifyAsset(body, asset); err != nil {
		t.Fatalf("正しい配布物が完全性検証に落ちた: %v", err)
	}

	// 適用: 管理者権限なしで、利用者領域の .app をその場で置き換える。
	applier, err := updater.NewApplier(appBundle)
	if err != nil {
		t.Fatalf("適用器を作れません（利用者領域の判定に失敗）: %v", err)
	}
	applied, err := applier.Apply(archive, m.Version)
	if err != nil {
		t.Fatalf("適用に失敗: %v", err)
	}
	if applied.TargetPath != appBundle || !applied.RestartRequired {
		t.Errorf("適用結果が想定と違う: %+v", applied)
	}
	afterInfo, err := os.Stat(appBundle)
	if err != nil {
		t.Fatalf("適用後に .app がありません: %v", err)
	}
	if os.SameFile(beforeInfo, afterInfo) {
		t.Error("適用後も同じ実体のまま（置き換わっていない）")
	}
	if _, err := os.Stat(filepath.Join(deployDir, ".reqweave-update")); !os.IsNotExist(err) {
		t.Errorf("作業場所が残っている: %v", err)
	}

	// 再起動: 置き換わったアプリが起動して画面まで到達する。
	after, line := launchAndWaitReady(t, launchOptions{
		bin: filepath.Join(appBundle, "Contents", "MacOS", "ReqWeave"), home: home, timeout: 60 * time.Second})
	t.Logf("適用後の起動: %v / %s", after.Round(time.Millisecond), line)
	// 判定は**初回起動**の基準で行う。起動時間の 5 秒の基準は
	// 「インストール・**更新の直後の初回起動**」を除いている（初回は 15 秒）。
	// 置き換わった .app は OS が走査し直すため、ここは 2 回目以降の基準が当たらない区間である
	// （実測: 9.437s / 5.850s。同梱後の初回起動 6.937s と同じ範囲）。
	if after > firstStartupBudget {
		t.Errorf("更新適用後の初回起動が基準を超過: %v（基準 %v = 初回起動の基準）",
			after, firstStartupBudget)
	}
}

// TestTamperedUpdateIsRejectedAndCurrentVersionSurvives は、改ざんされた配布物が
// 適用されず、現行版がそのまま使えることを実機で確認する（更新マニフェストの署名とハッシュで改ざんを検出する）。
func TestTamperedUpdateIsRejectedAndCurrentVersionSurvives(t *testing.T) {
	appBundle, _ := deployDistribution(t)
	beforeInfo, err := os.Stat(appBundle)
	if err != nil {
		t.Fatal(err)
	}

	releaseDir := userAreaDir(t, ".reqweave-e2e-release")
	extractDist(t, distArchive(t), releaseDir)
	archive := repackage(t, releaseDir, "ReqWeave-"+newVersion+"-macos.dmg")
	_, _, asset := signedManifest(t, archive, "https://example.invalid/ReqWeave.zip", newVersion)

	// 配信物を 1 バイト書き換える（マニフェストは正規のまま = 中身だけの改ざん）。
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)/2] ^= 0xFF
	if err := os.WriteFile(archive, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := updater.VerifyAsset(body, asset); err == nil {
		t.Fatal("改ざんした配布物が完全性検証を通過した（改ざんされた更新を利用者の環境へ適用してしまう）")
	} else {
		t.Logf("完全性検証が拒否（利用者向けの文言）: %v", err)
	}

	// 検証に落ちた配布物は適用しない。現行版は触られていないこと。
	afterInfo, err := os.Stat(appBundle)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Error("検証に落ちたのに現行版が置き換わっている")
	}
	home := userAreaDir(t, ".reqweave-e2e-home")
	elapsed, line := launchAndWaitReady(t, launchOptions{
		bin: filepath.Join(appBundle, "Contents", "MacOS", "ReqWeave"), home: home, timeout: 60 * time.Second})
	t.Logf("現行版はそのまま起動: %v / %s", elapsed.Round(time.Millisecond), line)
}

// TestUpdatePreservesCodeSignature は、更新で置き換わった .app の**コード署名が壊れていない**ことを
// 実機で確認する。
//
// 署名済みの配布物を素朴なファイルコピーで展開すると、実行権限・拡張属性が落ちて署名が壊れ、
// **更新した瞬間に Gatekeeper がアプリを拒否するようになる**（利用者から見ると「更新したら
// 起動しなくなった」）。取り出しに `ditto` を使う理由がここにあり、それを実物で押さえる。
//
// **Developer ID 証明書を持たない環境でも回るように ad-hoc 署名で確かめる**。
// 検証したい性質は「取り出しが署名を保つか」であり、署名の種類には依存しない
// （リリース物の Developer ID 署名・公証は make dist-release と distcheck -require-signature が見る）。
func TestUpdatePreservesCodeSignature(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("root で実行している。管理者権限なしの確認にならないため、通常の利用者で実行すること")
	}
	appBundle, _ := deployDistribution(t)

	// 「配信された新版」を用意し、**更新前の版と区別できる識別子**で署名し直す。
	releaseDir := userAreaDir(t, ".reqweave-e2e-release-sig")
	extractDist(t, distArchive(t), releaseDir)
	newApp := filepath.Join(releaseDir, "ReqWeave.app")
	const identifier = "net.howashoji.reqweave.e2e-signed"
	if out, err := exec.Command("codesign", "--force", "--sign", "-",
		"--identifier", identifier, newApp).CombinedOutput(); err != nil {
		t.Fatalf("配信物へ署名できません: %v\n%s", err, out)
	}
	if out, err := exec.Command("codesign", "--verify", "--strict", newApp).CombinedOutput(); err != nil {
		t.Fatalf("署名した配信物がその場で検証に落ちた（前提が崩れている）: %v\n%s", err, out)
	}
	archive := repackage(t, releaseDir, "ReqWeave-"+newVersion+"-macos.dmg")

	// 適用（dmg のマウント → ditto での取り出し → 原子的置換）。
	applier, err := updater.NewApplier(appBundle)
	if err != nil {
		t.Fatalf("適用器を作れません: %v", err)
	}
	if _, err := applier.Apply(archive, newVersion); err != nil {
		t.Fatalf("適用に失敗: %v", err)
	}

	// 置き換わった .app の署名が壊れていないこと。
	out, err := exec.Command("codesign", "--verify", "--strict", "--verbose=2", appBundle).CombinedOutput()
	t.Logf("適用後の codesign --verify:\n%s", strings.TrimSpace(string(out)))
	if err != nil {
		t.Errorf("更新後の .app の署名が壊れている（Gatekeeper に拒否される）: %v", err)
	}
	// 見ているのが**置き換わった後**の実体であること（更新前の版を見て緑になっていないこと）。
	disp, _ := exec.Command("codesign", "--display", "--verbose=2", appBundle).CombinedOutput()
	if !strings.Contains(string(disp), "Identifier="+identifier) {
		t.Errorf("更新後の .app が配信物の署名を持たない（置き換わっていないか署名が失われた）:\n%s", disp)
	}
}
