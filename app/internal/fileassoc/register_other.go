//go:build !windows

package fileassoc

// Register は Windows 以外では何もしない。
//
// macOS はアプリ本体の Info.plist（CFBundleDocumentTypes = wails.json の
// fileAssociations から生成）が関連付けを持ち、アプリを配置した時点で
// LaunchServices が登録する。アプリ側からレジストリ相当の操作は行わない。
func Register() {}
