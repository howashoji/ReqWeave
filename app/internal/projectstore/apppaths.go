package projectstore

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// AppID はアプリ設定領域のフォルダ名（配布物の CFBundleIdentifier と一致させる）。
const AppID = "net.howashoji.reqweave"

// AppPaths はアプリ設定領域配下の配置。
//
//	<Base>/settings.json        アプリ設定
//	<Base>/backups/<project_id> 自動退避
//	<Base>/cache/<project_id>   派生インデックス
//	<Base>/logs/                動作ログ
type AppPaths struct {
	Base string
}

// DefaultAppPaths は OS 標準のアプリ設定領域を返す。
// macOS: ~/Library/Application Support/<AppID> / Windows: %APPDATA%\<AppID>
func DefaultAppPaths() (AppPaths, error) {
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return AppPaths{Base: filepath.Join(appData, AppID)}, nil
		}
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return AppPaths{}, fmt.Errorf("ホームフォルダを特定できません: %w", err)
		}
		return AppPaths{Base: filepath.Join(home, "Library", "Application Support", AppID)}, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return AppPaths{}, fmt.Errorf("アプリ設定の保存先を特定できません: %w", err)
	}
	return AppPaths{Base: filepath.Join(dir, AppID)}, nil
}

// SettingsFile はアプリ設定ファイルのパス。
func (p AppPaths) SettingsFile() string { return filepath.Join(p.Base, "settings.json") }

// BackupsDir は自動退避の保存先。
func (p AppPaths) BackupsDir() string { return filepath.Join(p.Base, "backups") }

// ProjectBackupsDir はプロジェクト単位の自動退避の保存先。
func (p AppPaths) ProjectBackupsDir(projectID string) string {
	return filepath.Join(p.BackupsDir(), projectID)
}

// CacheDir は派生インデックスの保存先。
func (p AppPaths) CacheDir() string { return filepath.Join(p.Base, "cache") }

// LogsDir は動作ログの保存先（プロジェクト非依存）。
func (p AppPaths) LogsDir() string { return filepath.Join(p.Base, "logs") }

// SyncDir は同期モジュールが管理する設定ファイルの置き場所（認証の受け渡し資材と git 設定）。
// 端末内にのみ存在し、同期の対象に含めない。
func (p AppPaths) SyncDir() string { return filepath.Join(p.Base, "sync") }
