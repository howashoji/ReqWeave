//go:build windows

package fileassoc

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Register は受け渡しファイルの関連付けを **HKCU**（利用者領域）へ登録する。
//
// 管理者権限を要さない（HKLM へは書かない）。既に現在の実行ファイルで
// 登録済みなら何も書かない（起動のたびにレジストリを触らない）。
// 失敗しても呼び出し元へ返さない（関連付けが使えないだけでアプリは利用できる）。
func Register() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	entries := windowsEntries(exe)
	if registeredFor(entries) {
		return
	}
	changed := false
	for _, e := range entries {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, e.Key, registry.SET_VALUE)
		if err != nil {
			continue
		}
		if err := k.SetStringValue("", e.Value); err == nil {
			changed = true
		}
		_ = k.Close()
	}
	if changed {
		notifyShell()
	}
}

// registeredFor は全項目が既に同じ値で登録済みかを返す。
func registeredFor(entries []RegistryEntry) bool {
	for _, e := range entries {
		k, err := registry.OpenKey(registry.CURRENT_USER, e.Key, registry.QUERY_VALUE)
		if err != nil {
			return false
		}
		got, _, err := k.GetStringValue("")
		_ = k.Close()
		if err != nil || got != e.Value {
			return false
		}
	}
	return true
}

// notifyShell はエクスプローラへ関連付けの変更を知らせる（反映に再ログオンを要さない）。
func notifyShell() {
	const shcneAssocChanged = 0x08000000
	const shcnfIDList = 0x0000
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	_, _, _ = shell32.NewProc("SHChangeNotify").Call(shcneAssocChanged, shcnfIDList, 0, 0)
}
