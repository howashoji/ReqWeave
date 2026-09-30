package main

import (
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/appversion"
	"github.com/howashoji/ReqWeave/app/internal/updater"
)

const usage = `releasesign — 更新マニフェストの作成・署名・検証

  releasesign keygen -out <リポジトリ外のパス>
      新しい署名鍵ペアを作る。秘密鍵は指定パスへ 0600 で保存し、
      信頼鍵一覧へ貼る公開鍵の行を標準出力へ出す。
      **リポジトリ配下への保存は拒否する**。

  releasesign sign -version <x.y.z> -asset <os/arch=パス> [-asset ...] -out <manifest.json>
      配布物の SHA-256 とサイズを実ファイルから求め、マニフェストを作って署名する。
      鍵は環境変数 REQWEAVE_RELEASE_KEY_FILE（パスの列。: 区切り）で渡す。
      2 本渡すと二重署名になる（鍵ローテーション）。

  releasesign verify -manifest <manifest.json> [-asset <os/arch=パス> ...]
      アプリ埋め込みの信頼鍵でマニフェストを検証する。-asset を渡すと
      その実ファイルのハッシュ・サイズも照合する。

秘密鍵は「値」ではなく「ファイルパス」でのみ受け取る（引数に値を書くと ps から読めるため）。
`

// keyFileEnv は秘密鍵ファイルのパスを渡す環境変数（値そのものは渡さない）。
const keyFileEnv = "REQWEAVE_RELEASE_KEY_FILE"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = runKeygen(os.Args[2:])
	case "sign":
		err = runSign(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "releasesign: 未知のサブコマンド %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasesign: %v\n", err)
		os.Exit(1)
	}
}

func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "秘密鍵の保存先（リポジトリの外。可搬媒体を推奨）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("-out に秘密鍵の保存先を指定してください")
	}
	key, pub, err := generateKeyPair()
	if err != nil {
		return err
	}
	if err := writeKeyFile(*out, key, repoRoot()); err != nil {
		return err
	}
	entry, err := json.MarshalIndent(pub, "    ", "  ")
	if err != nil {
		return err
	}
	// 秘密鍵の値は出さない。出すのは保存先のパスと、公開鍵の行だけ。
	fmt.Printf("秘密鍵を保存しました: %s（権限 0600）\n", *out)
	fmt.Printf("鍵 ID: %s\n\n", key.ID)
	fmt.Printf("次の 1 件を app/internal/updater/trusted_keys.json の keys へ追加してください:\n\n    %s\n\n", entry)
	fmt.Print("秘密鍵はこの端末に残さず、可搬媒体 2 部（正・予備）へ移してください。\n")
	return nil
}

func runSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	version := fs.String("version", "", "公開する版番号（x.y.z）。省略時は正本の VERSION を使う")
	out := fs.String("out", "update-manifest.json", "マニフェストの出力先")
	var specs multiFlag
	fs.Var(&specs, "asset", "配布物の指定 `os/arch=パス`（繰り返し可）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("-asset で配布物を 1 件以上指定してください")
	}
	v := strings.TrimSpace(*version)
	if v == "" {
		v = appversion.Version()
	}
	if _, err := appversion.ParseSemver(v); err != nil {
		return fmt.Errorf("版番号が不正です: %w", err)
	}

	assets, err := assetsFromSpecs(v, specs)
	if err != nil {
		return err
	}
	keys, err := loadKeysFromEnv()
	if err != nil {
		return err
	}
	m, err := buildManifest(v, assets, keys)
	if err != nil {
		return err
	}
	if err := writeManifest(*out, m); err != nil {
		return err
	}
	fmt.Printf("マニフェストを作成しました: %s（版 %s / 配布物 %d 件 / 署名 %d 件）\n",
		*out, v, len(m.Assets), len(m.Signatures))
	for _, a := range m.Assets {
		fmt.Printf("  %s  %s  %d バイト\n", a.ID(), a.SHA256, a.Size)
	}
	fmt.Println("公開する前に releasesign verify で署名と配布物の照合を確かめ、" +
		"配布物とこのマニフェストを GitHub Releases へ公開してください。")
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	manifestPath := fs.String("manifest", "", "検証するマニフェスト")
	var specs multiFlag
	fs.Var(&specs, "asset", "実ファイルも照合する場合の指定 `os/arch=パス`（繰り返し可）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" {
		return fmt.Errorf("-manifest に検証するマニフェストを指定してください")
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("マニフェストを読めません（%s）: %w", *manifestPath, err)
	}
	keys, err := updater.TrustedKeys()
	if err != nil {
		return err
	}
	if keys.Len() == 0 {
		return fmt.Errorf("アプリ埋め込みの信頼鍵が 1 件も登録されていません。" +
			"keygen で作った公開鍵を app/internal/updater/trusted_keys.json へ追加してください")
	}
	m, err := updater.VerifyManifest(raw, keys)
	if err != nil {
		return fmt.Errorf("署名の検証に失敗しました: %w", err)
	}
	fmt.Printf("署名を検証しました: 版 %s（信頼鍵 %v）\n", m.Version, keys.IDs())

	for _, s := range specs {
		spec, err := parseAssetSpec(s)
		if err != nil {
			return err
		}
		asset, ok := m.Asset(spec.OS, spec.Arch)
		if !ok {
			return fmt.Errorf("マニフェストに %s/%s の配布物がありません", spec.OS, spec.Arch)
		}
		data, err := os.ReadFile(spec.Path)
		if err != nil {
			return fmt.Errorf("配布物を読めません（%s）: %w", spec.Path, err)
		}
		if err := updater.VerifyAsset(data, asset); err != nil {
			return fmt.Errorf("%s の照合に失敗しました: %w", spec.Path, err)
		}
		fmt.Printf("  %s  照合一致（%s）\n", asset.ID(), spec.Path)
	}
	return nil
}

// assetsFromSpecs は指定された実ファイルからマニフェストの配布物欄を組み立てる。
func assetsFromSpecs(version string, specs []string) ([]updater.Asset, error) {
	var assets []updater.Asset
	for _, s := range specs {
		spec, err := parseAssetSpec(s)
		if err != nil {
			return nil, err
		}
		sum, size, err := hashAsset(spec.Path)
		if err != nil {
			return nil, err
		}
		assets = append(assets, updater.Asset{
			OS: spec.OS, Arch: spec.Arch,
			URL:    downloadURL(version, filepath.Base(spec.Path)),
			SHA256: sum, Size: size,
		})
	}
	return assets, nil
}

// loadKeysFromEnv は環境変数が指すパスから署名鍵を読む。
//
// **鍵の値そのものは環境変数にもコマンドライン引数にも置かない**（パスのみ）。
func loadKeysFromEnv() ([]struct {
	ID   string
	Priv ed25519.PrivateKey
}, error) {
	paths := strings.Split(os.Getenv(keyFileEnv), string(os.PathListSeparator))
	var keys []struct {
		ID   string
		Priv ed25519.PrivateKey
	}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, priv, err := loadKeyFile(p)
		if err != nil {
			return nil, err
		}
		keys = append(keys, struct {
			ID   string
			Priv ed25519.PrivateKey
		}{ID: id, Priv: priv})
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("環境変数 %s に秘密鍵ファイルのパスを指定してください"+
			"（2 本以上は %q 区切り。鍵の値そのものは渡さない）",
			keyFileEnv, string(os.PathListSeparator))
	}
	return keys, nil
}

// repoRoot は本ツールから見たリポジトリのルート（app/ の親）を返す。
// 特定できない場合は空文字（保存先の検査を諦めるのではなく、呼び出し側が空扱いする）。
func repoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// multiFlag は繰り返し指定できる文字列フラグ。
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
