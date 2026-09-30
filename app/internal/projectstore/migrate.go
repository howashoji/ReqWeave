package projectstore

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Migrate は自版より古い形式のプロジェクトを自版へ移行する。
//
//  1. 移行前に自動退避を 1 世代作成する（移行前データを残す。自動退避と同じ仕組み）
//  2. 形式を自版へ移行する
//  3. migrated_from に旧バージョンを記録する
//
// 現行の形式バージョンは初版 "1.0" のため、(2) の段階で行う項目別の変換は無い。
// 非互換の形式変更を入れるときは、本関数の (2) にバージョン別の変換を追加する
// （退避と migrated_from の記録は形式によらず共通）。
func Migrate(paths AppPaths, root string, now time.Time) (backupPath string, err error) {
	path := filepath.Join(root, FileProject)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("プロジェクトデータを読み込めません（%s）: %w", path, err)
	}
	project, err := UnmarshalProject(data)
	if err != nil {
		return "", err
	}
	compat, err := project.FormatCompatibility()
	if err != nil {
		return "", err
	}
	switch compat {
	case CompatSame, CompatNewerMinor:
		return "", fmt.Errorf("移行の必要はありません（データ形式 %s）", project.FormatVersion)
	case CompatTooNew:
		found, _ := ParseFormatVersion(project.FormatVersion)
		current, _ := ParseFormatVersion(CurrentFormatVersion)
		return "", &ErrTooNew{Found: found, Current: current}
	}

	// (1) 移行前の退避（この世代も自動退避の保持世代数の対象）
	backupPath, err = AutoBackup(paths, root, project.ProjectID, now)
	if err != nil {
		return "", fmt.Errorf("移行前の自動退避に失敗したため移行を中止しました: %w", err)
	}

	// (2)(3) 形式の移行と旧バージョンの記録
	from := project.FormatVersion
	project.FormatVersion = CurrentFormatVersion
	project.MigratedFrom = from
	out, err := project.Marshal()
	if err != nil {
		return backupPath, err
	}
	if err := WriteFileAtomic(path, out); err != nil {
		return backupPath, err
	}
	return backupPath, nil
}
