package projectstore

// クラウド同期領域の検知。
//
// 本システムの共同作業は「各メンバーが**自端末に作業コピー**を持ち、共有は明示操作」という
// 形をとる。作業コピーをクラウド同期のフォルダへ置くと、
// **アプリの知らないもう 1 つの同期経路**ができる。2 台の端末から同じプロジェクトを開くと、
// 同期クライアントが勝手に混ぜたり衝突コピーを作ったりして、作業コピーが壊れうる。
//
// **判定は当て推量である**（パスの形で見るしかない）。したがって
// **作業を止めない**。注意を促すだけにとどめ、誤検知でも実害が出ないようにする。
// 見落とし（検知できないクラウド同期）もありうるため、これを唯一の防波堤にしない。

import (
	"path/filepath"
	"strings"
)

// cloudProviders は path の一区画がこの名前で始まるときに、その提供元とみなす。
//
// macOS の新しい形（File Provider）は `~/Library/CloudStorage/<提供元>-<アカウント>`。
// 併せて、両 OS で使われる従来の置き場所も見る。
var cloudProviders = []struct {
	prefix string
	label  string
}{
	{"Box", "Box"},
	{"Dropbox", "Dropbox"},
	{"GoogleDrive", "Google ドライブ"},
	{"Google Drive", "Google ドライブ"},
	{"OneDrive", "OneDrive"},
	{"iCloud Drive", "iCloud Drive"},
	{"Creative Cloud Files", "Creative Cloud"},
}

// CloudSyncedLocation は path がクラウド同期のフォルダの下に見えるかを返す。
//
// 戻り値の label は利用者へ見せる提供元名（空 = 検知しなかった）。
func CloudSyncedLocation(path string) (label string, detected bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	parts := strings.Split(filepath.ToSlash(abs), "/")
	for i, part := range parts {
		// macOS の iCloud は名前で分からない（`Library/Mobile Documents`）。
		if part == "Mobile Documents" && i > 0 && parts[i-1] == "Library" {
			return "iCloud Drive", true
		}
		for _, p := range cloudProviders {
			if part == p.prefix || strings.HasPrefix(part, p.prefix+"-") || strings.HasPrefix(part, p.prefix+" - ") {
				return p.label, true
			}
		}
	}
	return "", false
}

// CloudSyncWarning は、その場所へ置くことの注意を 1 文で返す（検知しなければ空）。
//
// **禁じない**。原因と、何に気をつければよいかだけを言う（利用者向けの文言の様式）。
func CloudSyncWarning(path string) string {
	label, ok := CloudSyncedLocation(path)
	if !ok {
		return ""
	}
	return label + " の同期フォルダのようです。ここへ置いても作業はできますが、" +
		"別の端末から同じプロジェクトを開くと内容が壊れることがあります。" +
		"複数人で進めるときは、この端末だけの場所へ置き、共有は同期先の設定で行ってください。"
}
