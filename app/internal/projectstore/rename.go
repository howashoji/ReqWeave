package projectstore

// 対象システム名の変更と、フォルダ名の追従。
//
// **業務データを変えるのはアプリ内の明示操作に限る**。
// Finder でフォルダ名を変えても `project.yaml` には反映しない（フォルダ名は識別の根拠にしない）。
// 逆向き、つまりアプリで名前を変えたときはフォルダ名を追従させ、
// 名前が 2 つに分かれたままにならないようにする。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetTargetSystemName は対象システム名を変更する。
//
// 変更前の名前を返す（呼び出し側が変更履歴へ記録するため。記録の担い手は
// メンバー・上限設定と同じくバインディング層）。
// 値が変わらないときは何も書かず、旧名をそのまま返す。
func (s *Store) SetTargetSystemName(name string) (before string, err error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("対象システム名を入力してください。")
	}
	before = s.Project().TargetSystemName
	if before == trimmed {
		return before, nil
	}
	if err := s.UpdateProject(func(p *Project) error {
		p.TargetSystemName = trimmed
		return nil
	}); err != nil {
		return "", err
	}
	return before, nil
}

// RenameProjectFolder はプロジェクトフォルダの名前を `<対象システム名>.reqweave` へ合わせる
// （アプリで名前を変えたときの追従）。移動後のパスを返す。
//
// **呼び出し前にプロジェクトを閉じておくこと**（開いたまま動かすと、書き込み先が消えた場所を指す）。
// 既に合っている場合は何もせず現在のパスを返す。
func RenameProjectFolder(root, targetSystemName string) (string, error) {
	parent := filepath.Dir(root)
	want := ProjectFolderName(targetSystemName)
	if filepath.Base(root) == want {
		return root, nil
	}
	next, err := UniqueProjectPath(parent, targetSystemName)
	if err != nil {
		return "", err
	}
	if err := os.Rename(root, next); err != nil {
		return "", fmt.Errorf("フォルダの名前を変えられませんでした。プロジェクトを閉じてからもう一度お試しください。")
	}
	// 名前が変わってから印を付ける（macOS で 1 個のファイルに見せる）。
	_ = markAsPackage(next)
	return next, nil
}
