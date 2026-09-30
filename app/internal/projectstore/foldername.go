package projectstore

// 本ファイルはプロジェクトフォルダの名前付けを担う。
//
// フォルダ名は `<対象システム名>.reqweave`。**表示上の手掛かりであり、識別の根拠にしない**
// （プロジェクトの判定は `project.yaml` の存在、識別は `project.yaml` の `project_id`）。
// 利用者がフォルダ名を変えても開けること。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ProjectFolderExt はプロジェクトフォルダの拡張子。
const ProjectFolderExt = ".reqweave"

// folderNameMaxRunes は安全化後に残す対象システム名の長さ（文字数）。
//
// 上限を置く理由: パス全体の長さ制限（Windows の既定は 260 文字）に、
// プロジェクト内の最も深いパス（`documents/requirements/v99/` 配下の章ファイル等）を足しても
// 収まるようにするため。**バイト数ではなく文字数**で数える（日本語の名前を極端に短くしない）。
const folderNameMaxRunes = 60

// ProjectFolderName は対象システム名からフォルダ名を作る。
//
// 名前に使えない文字は `_` へ置き換える。置き換えの結果が空になる場合は `project` を使う
// （名前が付けられずに作成が止まる状態を作らない）。
func ProjectFolderName(targetSystemName string) string {
	return safeFolderBase(targetSystemName) + ProjectFolderExt
}

// safeFolderBase は対象システム名を、macOS / Windows の双方で有効なフォルダ名へ直す。
//
// 除く文字: パス区切り・Windows が禁じる文字（`< > : " | ? *`）・制御文字。
// 末尾の `.` と空白は Windows が扱えないため落とす。
func safeFolderBase(name string) string {
	const forbidden = `<>:"/\|?*`
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsControl(r), strings.ContainsRune(forbidden, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := []rune(b.String())
	if len(out) > folderNameMaxRunes {
		out = out[:folderNameMaxRunes]
	}
	// 末尾の `.` と空白は Windows が保存できない（作った直後に開けなくなる）。
	trimmed := strings.TrimRight(string(out), ". ")
	if trimmed == "" {
		return "project"
	}
	return trimmed
}

// UniqueProjectPath は parent の下に作れるプロジェクトフォルダのパスを返す。
//
// 同名が既にある場合は `名前 2.reqweave` `名前 3.reqweave` … と連番を付ける。
// **既存を上書きしない**ことが目的であり、番号の意味は利用者に説明しない（表示上の区別のため）。
func UniqueProjectPath(parent, targetSystemName string) (string, error) {
	base := safeFolderBase(targetSystemName)
	for i := 1; i <= 100; i++ {
		name := base + ProjectFolderExt
		if i > 1 {
			name = fmt.Sprintf("%s %d%s", base, i, ProjectFolderExt)
		}
		path := filepath.Join(parent, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("同じ名前のプロジェクトが多すぎます。対象システム名を変えてください。")
}

// MarkProjectAsPackage はプロジェクトフォルダを、その OS で「1 個のファイル」に見せる。
// macOS 以外では何もしない。
//
// 作成時は CreateProject が呼ぶ。移行（改名）のあとにも呼ぶ必要があるため公開する。
func MarkProjectAsPackage(root string) error { return markAsPackage(root) }
