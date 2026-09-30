package binding

// 本ファイルは既存プロジェクトを `<対象システム名>.reqweave` へ移す操作を担う。
//
// **起動時に無断で改名しない**。利用者のデータの置き場所を断りなく変えると、
// 利用者が作ったショートカット・クラウド同期・他アプリからの参照が黙って壊れる。
// 一覧で対象を示し、利用者の確認を得てから 1 件ずつ実行する。移行しない選択も残す。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// MigratableProject は拡張子が付いていないプロジェクト 1 件（プロジェクト一覧の移行導線）。
type MigratableProject struct {
	Path string `json:"path"`
	// TargetSystemName は移行後のフォルダ名の元になる名前。
	TargetSystemName string `json:"targetSystemName"`
	// NewName は移行後のフォルダ名（利用者へ結果を先に見せる）。
	NewName string `json:"newName"`
}

// MigratableProjects は移行できるプロジェクトを返す（読み出しのみ。何も変えない）。
//
// 対象は「一覧にあり、フォルダ名が `.reqweave` で終わっていない」もの。
// 読み込めないプロジェクトは対象にしない（壊れているものを触らない）。
func (a *API) MigratableProjects() ([]MigratableProject, error) {
	settings, err := a.settings()
	if err != nil {
		return nil, err
	}
	author, _ := settings.Author()
	out := make([]MigratableProject, 0)
	for _, path := range settings.RecentProjectPaths() {
		if strings.HasSuffix(path, projectstore.ProjectFolderExt) {
			continue
		}
		summary := a.summarize(path, author)
		if summary.Notice != "" || summary.TargetSystemName == "" {
			continue
		}
		out = append(out, MigratableProject{
			Path:             path,
			TargetSystemName: summary.TargetSystemName,
			NewName:          projectstore.ProjectFolderName(summary.TargetSystemName),
		})
	}
	return out, nil
}

// MigrateProject は 1 件を `<対象システム名>.reqweave` へ改名し、一覧のパスを更新する。
//
// 順序は **改名 → 設定の更新**。逆にすると、改名に失敗したときに一覧が実体を指さなくなる。
// この順序でも「改名は済んだが設定の更新前に落ちる」ことは起こりうるため、
// 一覧の読み出し側が `.reqweave` を付けた名前も探す（`RecentProjectPaths` の解決）。
func (a *API) MigrateProject(path string) (ProjectSummary, error) {
	settings, err := a.settings()
	if err != nil {
		return ProjectSummary{}, err
	}
	author, _ := settings.Author()

	if !projectstore.IsProjectFolder(path) {
		return ProjectSummary{}, fmt.Errorf("このフォルダはプロジェクトデータではありません。場所を確認してください。")
	}
	if strings.HasSuffix(path, projectstore.ProjectFolderExt) {
		return a.summarize(path, author), nil
	}

	summary := a.summarize(path, author)
	if summary.Notice != "" || summary.TargetSystemName == "" {
		return ProjectSummary{}, fmt.Errorf("このプロジェクトを読み込めません。フォルダの場所と共有フォルダの接続を確認してください。")
	}

	parent := filepath.Dir(path)
	next, err := projectstore.UniqueProjectPath(parent, summary.TargetSystemName)
	if err != nil {
		return ProjectSummary{}, err
	}
	if err := os.Rename(path, next); err != nil {
		return ProjectSummary{}, fmt.Errorf("フォルダの名前を変えられませんでした。プロジェクトを閉じてからもう一度お試しください。")
	}
	// 名前が変わってから印を付ける（macOS で 1 個のファイルに見せる）。
	_ = projectstore.MarkProjectAsPackage(next)

	settings.ReplaceRecentProject(path, next)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		// 実体は移っている。一覧の解決が `.reqweave` を付けた名前も探すため、開けなくはならない。
		return ProjectSummary{}, fmt.Errorf("名前は変えましたが、一覧の保存に失敗しました。アプリを再起動してください。")
	}
	return a.summarize(next, author), nil
}

// RenameProject は対象システム名を変更し、フォルダ名も追従させる。
//
// 手順は **(1) 名前の変更 → (2) 変更履歴 → (3) フォルダ名の追従 → (4) 一覧のパス更新**。
// **(3) に失敗しても (1) は残す**（名前の変更が主で、フォルダ名は表示のための写しのため）。
// オーナーのみ（削除・上限設定と同じ扱い）。
func (a *API) RenameProject(path, targetSystemName string) (ProjectSummary, error) {
	name := strings.TrimSpace(targetSystemName)
	if name == "" {
		return ProjectSummary{}, fmt.Errorf("対象システム名を入力してください。")
	}
	settings, err := a.settings()
	if err != nil {
		return ProjectSummary{}, err
	}
	author, _ := settings.Author()

	summary := a.summarize(path, author)
	if !summary.Available {
		return ProjectSummary{}, fmt.Errorf("%s", summary.Notice)
	}
	if summary.Role != "" && summary.Role != projectstore.RoleOwner {
		return ProjectSummary{}, fmt.Errorf("対象システム名の変更はオーナーだけができます。オーナーに依頼してください。")
	}

	store, err := a.openProject(path)
	if err != nil {
		return ProjectSummary{}, err
	}
	before, err := store.SetTargetSystemName(name)
	if err != nil {
		_ = store.Close()
		return ProjectSummary{}, err
	}
	if before != name {
		a.recordChange(&dialogueSession{store: store}, auditlog.ChangeRecord{
			At: time.Now().UTC(), Author: store.Author().AuthorID,
			Target: "project", Change: auditlog.ChangeProjectRenamed,
			Before: before, After: name,
		})
	}
	// フォルダを動かす前に閉じる（開いたまま動かすと書き込み先が消えた場所を指す）。
	if err := store.Close(); err != nil {
		return ProjectSummary{}, err
	}

	next, renameErr := projectstore.RenameProjectFolder(path, name)
	if renameErr != nil {
		// 名前の変更は済んでいる。フォルダ名だけが古いまま残る（次の変更でまた合わせられる）。
		return a.summarize(path, author), nil
	}
	if next != path {
		settings.ReplaceRecentProject(path, next)
		if err := projectstore.SaveSettings(a.paths, settings); err != nil {
			// 実体は移っている。一覧の解決が `.reqweave` を付けた名前も探すため、開けなくはならない。
			return ProjectSummary{}, fmt.Errorf("名前は変えましたが、一覧の保存に失敗しました。アプリを再起動してください。")
		}
	}
	return a.summarize(next, author), nil
}

// ForgetProject は一覧から取り除く（**データは消さない**）。
//
// 実体が見つからなくなったプロジェクトを一覧から外すための操作。
// 「削除」は実体を消す操作で、実体が無いと実行できないため、外す手段を別に持つ。
func (a *API) ForgetProject(path string) error {
	settings, err := a.settings()
	if err != nil {
		return err
	}
	if !settings.ForgetRecentProject(path) {
		return fmt.Errorf("この項目は一覧にありません。")
	}
	return projectstore.SaveSettings(a.paths, settings)
}
