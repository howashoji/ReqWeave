// distcheck は配布用書庫の中身を検査する。
//
// 「作ったつもりで中身が違う」配布物を公開しないための機械検査。
// `make dist` / `make dist-windows` の最後で走り、問題があれば非ゼロ終了する。
package main

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"strings"
)

// requiredDocs は配布物へ必ず同梱する文書（OS の警告を越えて初回起動する手順）。
var requiredDocs = []string{"初回起動の手順.md"}

// appNameFor は OS ごとのアプリ本体の名前。
func appNameFor(goos string) (string, error) {
	switch goos {
	case "darwin":
		return "ReqWeave.app", nil
	case "windows":
		return "ReqWeave.exe", nil
	}
	return "", fmt.Errorf("対応していない OS: %q（darwin / windows）", goos)
}

// checkArchive は書庫の中身を検査し、問題の一覧と、検査できなかった項目の申告を返す。
func checkArchive(archivePath, goos, legalDir string) ([]string, []string, error) {
	appName, err := appNameFor(goos)
	if err != nil {
		return nil, nil, err
	}
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("配布物を開けません（%s）: %w", archivePath, err)
	}
	defer r.Close()

	// 最上位の項目を集める。
	top := map[string]bool{}
	var problems []string
	appEntries := 0
	for _, f := range r.File {
		name := path.Clean(f.Name)
		if strings.HasPrefix(name, "../") || path.IsAbs(name) {
			problems = append(problems, fmt.Sprintf("書庫の外を指す項目がある: %q", f.Name))
			continue
		}
		if f.Mode()&0x8000000 != 0 { // os.ModeSymlink
			problems = append(problems, fmt.Sprintf("シンボリックリンクが含まれる: %q", f.Name))
		}
		// macOS の資源フォークやゴミを持ち込まない。
		base := path.Base(name)
		if base == ".DS_Store" || strings.HasPrefix(name, "__MACOSX/") {
			problems = append(problems, fmt.Sprintf("不要なファイルが含まれる: %q", f.Name))
		}
		first := strings.SplitN(name, "/", 2)[0]
		top[first] = true
	}
	for name := range top {
		if name == appName {
			appEntries++
		}
	}

	// 1. アプリ本体がちょうど 1 件あること（アップデータの選択条件 = internal/updater.appEntry）。
	if appEntries != 1 {
		problems = append(problems, fmt.Sprintf("アプリ本体 %q が %d 件（1 件であること）", appName, appEntries))
	}
	// 2. 同梱の手順書があること。
	for _, doc := range requiredDocs {
		if !top[doc] {
			problems = append(problems, fmt.Sprintf("同梱の手順書が無い: %q", doc))
		}
	}
	// 3. 最上位に想定外の項目が無いこと（配布物は「本体＋手順書」、Windows はさらにライセンス表示）。
	allowed := map[string]bool{appName: true}
	for _, doc := range requiredDocs {
		allowed[doc] = true
	}
	if goos == "windows" {
		for _, name := range legalFiles {
			allowed[name] = true
		}
	}
	for name := range top {
		if !allowed[name] {
			problems = append(problems, fmt.Sprintf("配布物に想定外の項目がある: %q", name))
		}
	}
	// 3.5 ライセンス表示が入っていて、リポジトリ直上の原本と一致すること。
	problems = append(problems, checkLegal(func(rel string) ([]byte, error) {
		f, err := r.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	}, goos, appName, legalDir)...)
	// 4. ビルド環境の情報が混入していないこと。**dmg と同じ検査を zip にも当てる**
	//    （Windows の配布物は zip であり、混入は macOS と同じように起きる = 実測 1085 件）。
	leaks, notes, lerr := checkLeaks(&r.Reader)
	if lerr != nil {
		return nil, nil, lerr
	}
	problems = append(problems, leaks...)

	// 5. macOS は実行ファイルが Universal Binary であることは
	//    書庫の中身だけでは判定できないため、呼び出し側（main）が展開せずに
	//    Mach-O のヘッダを読んで確かめる。
	return problems, notes, nil
}

// universalCheck は macOS の実行ファイルが Apple Silicon・Intel の双方に対応するかを
// 書庫の中の Mach-O ヘッダから判定する。
//
// 判定できないとき（Windows 向け書庫など）は ok=false, checked=false を返す。
func universalCheck(archivePath string) (checked bool, ok bool, detail string, err error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return false, false, "", fmt.Errorf("配布物を開けません（%s）: %w", archivePath, err)
	}
	defer r.Close()

	const execPath = "ReqWeave.app/Contents/MacOS/ReqWeave"
	for _, f := range r.File {
		if path.Clean(f.Name) != execPath {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false, false, "", fmt.Errorf("実行ファイルを開けません: %w", err)
		}
		defer rc.Close()
		head := make([]byte, 8)
		if _, err := rc.Read(head); err != nil {
			return false, false, "", fmt.Errorf("実行ファイルの先頭を読めません: %w", err)
		}
		// fat binary（universal）のマジックは 0xcafebabe / 0xbebafeca（バイト順違い）。
		magic := uint32(head[0])<<24 | uint32(head[1])<<16 | uint32(head[2])<<8 | uint32(head[3])
		if magic == 0xcafebabe || magic == 0xbebafeca {
			arches := uint32(head[4])<<24 | uint32(head[5])<<16 | uint32(head[6])<<8 | uint32(head[7])
			return true, arches >= 2, fmt.Sprintf("universal binary（%d アーキテクチャ）", arches), nil
		}
		return true, false, "単一アーキテクチャの実行ファイル（universal ではない）", nil
	}
	return false, false, "", nil
}
