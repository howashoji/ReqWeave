// Package fileassoc は受け渡しファイル（質問票の .rwvq / 返送の .rwva）を
// OS のファイル関連付けへ登録する（ファイルをダブルクリックして本システムで開けるように）。
//
// OS ごとの担い手が違う:
//   - macOS: アプリ本体の Info.plist（CFBundleDocumentTypes）が持つ。配置しただけで
//     LaunchServices が拾うため、アプリからの登録操作は不要（本パッケージは何もしない）。
//   - Windows: 配布形態が zip（インストーラを持たない）のため、
//     アプリ自身が**利用者領域**（HKCU\Software\Classes）へ登録する。
//     HKLM（全ユーザー）へは書かない＝**管理者権限を要さない**（管理者権限なしで導入・利用できるようにする）。
//
// 登録できなくてもアプリの利用は続けられる（関連付けからの起動が使えなくなるだけで、
// アプリ内の操作からは受け渡しファイルを開ける）。そのため失敗は致命扱いしない。
package fileassoc

// quote は Windows のコマンド行の引用（値をそのまま二重引用符で囲む）。
// Go の %q は円記号を \\ へ拡張してしまい、レジストリ値としては壊れるため使わない。
func quote(s string) string { return `"` + s + `"` }

// ProgID は Windows の関連付けで使う識別子（他のアプリと衝突しない名前空間）。
const (
	ProgIDQuestionnaire = "ReqWeave.Questionnaire" // .rwvq = 発行用（質問票）
	ProgIDReturn        = "ReqWeave.Return"        // .rwva = 返送用（回答）
)

// association は 1 拡張子分の関連付け仕様。
type association struct {
	Ext    string // ".rwvq" など（先頭のドットを含む）
	ProgID string
	Label  string // エクスプローラの「種類」列に出る説明
}

// associations は登録対象（受け渡しファイルの 2 拡張子。増やすときはここだけを変える）。
var associations = []association{
	{Ext: ".rwvq", ProgID: ProgIDQuestionnaire, Label: "ReqWeave 質問票"},
	{Ext: ".rwva", ProgID: ProgIDReturn, Label: "ReqWeave 返送ファイル"},
}

// RegistryEntry は Windows レジストリへ書く 1 項目（キーと既定値）。
// キーはすべて HKEY_CURRENT_USER からの相対パスであり、HKLM を含まない。
type RegistryEntry struct {
	// Key は HKCU からの相対パス（例: `Software\Classes\.rwvq`）。
	Key string
	// Value は当該キーの既定値。
	Value string
}

// windowsEntries は実行ファイルのパスから、書き込むべきレジストリ項目を組み立てる。
//
// パスに空白（`C:\Users\...\Program Files` 等）が含まれても壊れないよう、
// 起動コマンドは実行ファイルと引数の双方を引用符で囲む。
func windowsEntries(exe string) []RegistryEntry {
	var entries []RegistryEntry
	for _, a := range associations {
		entries = append(entries,
			RegistryEntry{Key: `Software\Classes\` + a.Ext, Value: a.ProgID},
			RegistryEntry{Key: `Software\Classes\` + a.ProgID, Value: a.Label},
			RegistryEntry{Key: `Software\Classes\` + a.ProgID + `\DefaultIcon`, Value: quote(exe) + ",0"},
			RegistryEntry{Key: `Software\Classes\` + a.ProgID + `\shell\open\command`, Value: quote(exe) + " " + quote("%1")},
		)
	}
	return entries
}
