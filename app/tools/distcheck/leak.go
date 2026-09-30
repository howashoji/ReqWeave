package main

import (
	"fmt"
	"io/fs"
	"os/user"
	"path"
	"strings"
)

/*
 * 配布物への「ビルドした環境の情報」の混入検査。
 *
 * 2026-09-04、利用者から「配布時に見知らぬ相手へ自分の情報を渡さないでほしい」との指摘を受けて
 * 実測したところ、**実行ファイルにビルドしたマシンの利用者名とディレクトリ構成が
 * 1940 件埋め込まれていた**（`-trimpath` を付けていなかったため）。Windows の実行ファイルにも
 * 1085 件あった。手で検索して気づいたのであり、機械では検査していなかった。**毎回の保証にする**。
 *
 * 検査するもの:
 *   1. 設定・資格情報のファイルが同梱されていないこと（アプリは初期状態で配られる）
 *   2. **実行ファイルに利用者ホーム配下の絶対パスが無いこと**（`-trimpath` の効果を直に確かめる）
 *   3. どのファイルにも、**ビルドしたマシンの利用者のホームのパス**が現れないこと
 *
 * 2 を実行ファイルだけに限るのは、同梱の手順書が `C:\Users\<あなたのユーザー名>\…` のような
 * **説明のための例**を持つため。一般形を全ファイルへ当てると正しい記述を落とす。
 * 逆に 3 は実際の利用者名まで含めたパスで照合するため、全ファイルへ当てても手順書の例には当たらない。
 *
 * `-trimpath` を付けて作った実行ファイルでは 2 の対象パターンが実測 0 件であることを確認済み
 * （2026-09-04。macOS universal binary）。よって「0 件」を基準にしてよい。
 */

// forbiddenNames は配布物に入っていてはならないファイル（名前で判定）。
// 設定・鍵・資格情報の類は、アプリが利用者の端末で作るものであり配布物には無い。
var forbiddenNames = []string{
	"settings.json", ".env", "credentials", "credentials.json",
	"id_rsa", "id_ed25519", ".netrc", ".git-credentials",
}

// forbiddenExts は配布物に入っていてはならない拡張子（鍵・証明書の秘密部）。
var forbiddenExts = []string{".key", ".pem", ".p12", ".pfx", ".p8", ".keychain-db"}

// homePathMarkers は「利用者ホーム配下の絶対パス」の一般形。実行ファイルにだけ当てる。
var homePathMarkers = []string{"/Users/", `\Users\`, "/home/"}

// checkLeaks は配布物の中身に、設定・資格情報・ビルド環境の痕跡が無いことを確かめる。
//
// fsys は配布物の中身を根に持つファイルシステム（dmg はマウント先の os.DirFS、
// zip は *zip.Reader）。problems は不合格の理由、notes は検査できなかった項目の申告。
func checkLeaks(fsys fs.FS) (problems []string, notes []string, err error) {
	// ビルドしたマシンの利用者名。取れない場合は検査 3 を「未実施」と申告して緑にする
	// （黙って飛ばさない。アイコンの検査と同じ方針）。検査 2 は利用者名に依らず動く。
	// **短い利用者名での取りこぼし・誤検出を避けるため、必ずホームのパス形で照合する**
	// （裸の名前で数えると "go" のような名前が binary 中に無数に当たる）。
	var userMarkers []string
	if u, uerr := user.Current(); uerr == nil && u.Username != "" {
		userMarkers = []string{"/Users/" + u.Username, `\Users\` + u.Username, "/home/" + u.Username}
	} else {
		notes = append(notes, "ビルドしたマシンの利用者名を取得できず、利用者名での照合は未実施（実行ファイルの絶対パス検査は実施）")
	}

	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // 読めない項目は他の検査（中身の一覧）が拾う
		}
		name := d.Name()
		// ボリュームに OS が作る隠しメタデータは配布物の中身ではない。
		if d.IsDir() && (name == ".Trashes" || name == ".fseventsd") {
			return fs.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		lower := strings.ToLower(name)
		for _, forbidden := range forbiddenNames {
			if lower == forbidden {
				problems = append(problems, fmt.Sprintf(
					"設定・資格情報のファイルが同梱されている: %q（配布物は初期状態であること）", p))
			}
		}
		for _, ext := range forbiddenExts {
			if strings.HasSuffix(lower, ext) {
				problems = append(problems, fmt.Sprintf("鍵・証明書のファイルが同梱されている: %q", p))
			}
		}

		body, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return nil
		}
		text := string(body)

		if isExecutable(p) {
			for _, marker := range homePathMarkers {
				if n := strings.Count(text, marker); n > 0 {
					problems = append(problems, fmt.Sprintf(
						"実行ファイルにビルド環境の絶対パス %q が %d 件混入している: %q（Go のビルドへ -trimpath を付けること）",
						marker, n, p))
				}
			}
		}
		for _, marker := range userMarkers {
			if n := strings.Count(text, marker); n > 0 {
				problems = append(problems, fmt.Sprintf(
					"ビルドしたマシンの利用者のホームのパス %q が %d 件混入している: %q", marker, n, p))
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, fmt.Errorf("配布物の中身を走査できません: %w", walkErr)
	}
	return problems, notes, nil
}

// isExecutable は配布物の中でアプリ本体の実行ファイルにあたるかを、位置と名前で判定する。
// macOS は ReqWeave.app/Contents/MacOS/<名前>、Windows は <名前>.exe。
func isExecutable(p string) bool {
	if strings.HasSuffix(strings.ToLower(p), ".exe") {
		return true
	}
	return strings.Contains(path.Clean(p), "/Contents/MacOS/")
}
