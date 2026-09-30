package binding

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// BackupGenerationView は自動退避の 1 世代。
type BackupGenerationView struct {
	Path string `json:"path"`
	// CreatedAt は UTC の ISO 8601。表示時にローカルへ変換する。
	CreatedAt string `json:"createdAt"`
	SizeBytes int64  `json:"sizeBytes"`
}

// BackupPreview は復元前の確認内容（project_id の重複を提示する）。
type BackupPreview struct {
	SourcePath       string `json:"sourcePath"`
	ProjectID        string `json:"projectId"`
	TargetSystemName string `json:"targetSystemName"`
	// DuplicatePath は同じ project_id の既知プロジェクトがある場合その場所。
	DuplicatePath string `json:"duplicatePath,omitempty"`
	Notice        string `json:"notice,omitempty"`
}

// BackupProject は手動バックアップを単一ファイルへ出力する。
// 出力先は OS のファイル保存ダイアログで選ぶ。
func (a *API) BackupProject(projectPath string) (string, error) {
	if !projectstore.IsProjectFolder(projectPath) {
		return "", fmt.Errorf("プロジェクトフォルダではありません。対象を確認してください。")
	}
	if a.ctx == nil {
		return "", fmt.Errorf("保存先を選べません。アプリを再起動してください。")
	}
	name := filepath.Base(projectPath) + "-" + time.Now().UTC().Format("20060102T150405Z") + ".zip"
	dst, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "バックアップの保存先",
		DefaultFilename: name,
	})
	if err != nil {
		return "", fmt.Errorf("保存先を選べませんでした。もう一度お試しください。")
	}
	if dst == "" {
		return "", nil
	}
	if err := projectstore.WriteManualBackupZip(projectPath, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// ChooseBackupFile は復元元のバックアップファイルを選ぶ。
func (a *API) ChooseBackupFile() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("ファイルを選べません。アプリを再起動してください。")
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   "復元するバックアップファイル",
		Filters: []wailsruntime.FileFilter{{DisplayName: "バックアップ (*.zip)", Pattern: "*.zip"}},
	})
}

// BackupGenerations は自動退避の世代一覧を新しい順に返す。
func (a *API) BackupGenerations(projectPath string) ([]BackupGenerationView, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, projectstore.FileProject))
	if err != nil {
		return nil, fmt.Errorf("プロジェクトデータを読み込めません。フォルダの場所を確認してください。")
	}
	project, err := projectstore.UnmarshalProject(data)
	if err != nil {
		return nil, err
	}
	gens, err := projectstore.ListBackupGenerations(a.paths, project.ProjectID)
	if err != nil {
		return nil, err
	}
	out := make([]BackupGenerationView, 0, len(gens))
	for _, g := range gens {
		out = append(out, BackupGenerationView{
			Path:      g.Path,
			CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
			SizeBytes: g.Size,
		})
	}
	return out, nil
}

// PreviewBackup は復元前にバックアップの内容を確認する。
func (a *API) PreviewBackup(source string) (BackupPreview, error) {
	project, err := projectstore.PeekBackupProjectID(source)
	if err != nil {
		return BackupPreview{}, err
	}
	preview := BackupPreview{
		SourcePath:       source,
		ProjectID:        project.ProjectID,
		TargetSystemName: project.TargetSystemName,
	}
	settings, err := a.settings()
	if err != nil {
		return BackupPreview{}, err
	}
	for _, path := range settings.RecentProjectPaths() {
		data, err := os.ReadFile(filepath.Join(path, projectstore.FileProject))
		if err != nil {
			continue
		}
		existing, err := projectstore.UnmarshalProject(data)
		if err != nil {
			continue
		}
		if existing.ProjectID == project.ProjectID {
			preview.DuplicatePath = path
			preview.Notice = "同じプロジェクトが既にあります。置き換えるか、複製として保持するかを選んでください。"
			break
		}
	}
	return preview, nil
}

// RestoreBackup はバックアップを新しいフォルダへ復元する。
//
// 既存フォルダを上書きしない（復元先は存在しないか空であること）。
// project_id が既存プロジェクトと重複する場合:
//   - keepAsCopy = true  … 複製として保持する（新しい project_id を採番する = 同一性の衝突を避ける）
//   - keepAsCopy = false … 置き換える（元のプロジェクトを一覧から外す。フォルダは削除しない）
func (a *API) RestoreBackup(source, destination string, keepAsCopy bool) (ProjectSummary, error) {
	if strings.TrimSpace(destination) == "" {
		return ProjectSummary{}, fmt.Errorf("復元先のフォルダを選んでください。")
	}
	settings, err := a.settings()
	if err != nil {
		return ProjectSummary{}, err
	}
	author, ok := settings.Author()
	if !ok {
		return ProjectSummary{}, fmt.Errorf("メールアドレス（利用者 ID）が未登録です。設定でメールアドレスを登録してください。")
	}
	preview, err := a.PreviewBackup(source)
	if err != nil {
		return ProjectSummary{}, err
	}
	if _, err := projectstore.RestoreZip(source, destination); err != nil {
		return ProjectSummary{}, err
	}
	if preview.DuplicatePath != "" {
		if keepAsCopy {
			if _, err := projectstore.ReassignProjectID(destination, author); err != nil {
				return ProjectSummary{}, err
			}
		} else if err := a.forgetProject(preview.DuplicatePath); err != nil {
			return ProjectSummary{}, err
		}
	}

	settings, err = a.settings()
	if err != nil {
		return ProjectSummary{}, err
	}
	settings.AddRecentProject(destination, 0)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		return ProjectSummary{}, err
	}
	summary := a.summarize(destination, author)
	// 自動退避の zip には同期の管理情報を含めない。復元した作業コピーは、
	// 同期の操作の前に同期先から取り直す（無確認で同期先へ書かない）。
	if summary.Available && summary.Notice == "" && syncmod.NeedsReattach(destination) {
		summary.Notice = "同期先の情報はバックアップに含まれていません。同期する前に、同期の画面で同期先から取り直してください。"
	}
	return summary, nil
}
