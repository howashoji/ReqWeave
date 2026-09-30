package main

// ライセンス表示の同梱の検査。
//
// アプリ本体には第三者のソフトウェア（Go のモジュール・npm のパッケージ）が入っており、MIT・BSD などは
// 「複製物にライセンス表示を含める」ことを条件にしている。バイナリの配布物にも、リポジトリ直上の
// LICENSE（本システム自身）と NOTICE（第三者のライセンス文。tools/noticegen が依存から作る）を入れる。
// 中身はリポジトリ直上の原本と 1 バイトも違わないことを見る（古い NOTICE を入れたまま出さない）。
//
// 置き場所:
//   - macOS はアプリ本体の中（ReqWeave.app/Contents/Resources/）。アプリと一緒に移り、自動更新でも入れ替わる。
//   - Windows は zip の直下（ReqWeave.exe の隣）。実行ファイル 1 個の配布なので、中に入れる場所が無い。

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// legalFiles は配布物へ必ず同梱するライセンス表示。
var legalFiles = []string{"LICENSE", "NOTICE"}

// legalPathFor は配布物の中でのライセンス表示の置き場所（最上位からの相対パス）を返す。
func legalPathFor(goos, appName, name string) string {
	if goos == "darwin" {
		return path.Join(appName, "Contents", "Resources", name)
	}
	return name
}

// checkLegal は配布物の中のライセンス表示が原本（legalDir の同名のファイル）と一致するかを見る。
// read は配布物の中のファイルを読む（最上位からの相対パス）。
func checkLegal(read func(rel string) ([]byte, error), goos, appName, legalDir string) []string {
	var problems []string
	for _, name := range legalFiles {
		want, err := os.ReadFile(filepath.Join(legalDir, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("ライセンス表示の原本を読めません（%s）: %v", filepath.Join(legalDir, name), err))
			continue
		}
		rel := legalPathFor(goos, appName, name)
		got, err := read(rel)
		if err != nil {
			problems = append(problems, fmt.Sprintf("ライセンス表示 %q が配布物にありません（%s）", name, rel))
			continue
		}
		if !bytes.Equal(got, want) {
			problems = append(problems, fmt.Sprintf("配布物の %s がリポジトリ直上の %s と違います（作り直す）", rel, name))
		}
	}
	return problems
}
