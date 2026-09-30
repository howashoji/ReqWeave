package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	// 署名・公証を必須にするか。
	// **make dist は付けない**（開発・検証ゲート用の未署名の配布物を作るため）。
	// **make dist-release は付ける**（リリース物は署名・公証が無ければ不合格）。
	requireSignature := flag.Bool("require-signature", false,
		"Developer ID 署名と公証を必須にする（リリース物の検査。make dist-release 用）")
	// ライセンス表示の原本の置き場所。make は app/ で動くので、既定はリポジトリ直上。
	legalDir := flag.String("legal-dir", "..", "ライセンス表示（LICENSE・NOTICE）の原本があるディレクトリ")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "使い方: distcheck [-require-signature] [-legal-dir <ディレクトリ>] <配布物（dmg / zip）> <darwin|windows>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	archivePath, goos := flag.Arg(0), flag.Arg(1)

	var problems []string
	// **macOS は dmg、Windows は zip**。形式は拡張子で決める。
	// zip の darwin 配布物も従来どおり検査できる（過去の配布物・テスト用）。
	if strings.EqualFold(filepath.Ext(archivePath), ".dmg") {
		if goos != "darwin" {
			fmt.Fprintf(os.Stderr, "distcheck: dmg は macOS の配布物です（指定: %q）\n", goos)
			os.Exit(2)
		}
		appName, err := appNameFor(goos)
		if err != nil {
			fmt.Fprintf(os.Stderr, "distcheck: 検査に失敗しました: %v\n", err)
			os.Exit(2)
		}
		p, notes, err := checkDMG(archivePath, appName, *legalDir, *requireSignature)
		if err != nil {
			fmt.Fprintf(os.Stderr, "distcheck: 検査に失敗しました: %v\n", err)
			os.Exit(2)
		}
		problems = p
		for _, n := range notes {
			fmt.Printf("distcheck: %s\n", n)
		}
	} else {
		if *requireSignature && goos == "darwin" {
			// 署名・公証は dmg でしか成立しない（stapler は zip へチケットを添付できない）。
			// 黙って未検査で通さない。
			fmt.Fprintln(os.Stderr, "distcheck: 署名・公証つきの macOS 配布物は dmg で作ること")
			os.Exit(2)
		}
		p, notes, err := checkArchive(archivePath, goos, *legalDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "distcheck: 検査に失敗しました: %v\n", err)
			os.Exit(2)
		}
		problems = p
		for _, n := range notes {
			fmt.Printf("distcheck: %s\n", n)
		}
		if goos == "darwin" {
			checked, ok, detail, err := universalCheck(archivePath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "distcheck: 検査に失敗しました: %v\n", err)
				os.Exit(2)
			}
			switch {
			case !checked:
				problems = append(problems, "実行ファイルが見つからず、対応アーキテクチャを確認できません")
			case !ok:
				problems = append(problems, "Apple Silicon・Intel の双方に対応していません（"+detail+
					"。make dist は -platform darwin/universal で作ること）")
			default:
				fmt.Printf("distcheck: %s\n", detail)
			}
		}
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "✘ 配布物の中身が想定と違います %d 件:\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		os.Exit(1)
	}
	fmt.Printf("distcheck: %s の中身は想定どおり（アプリ本体 1 件 + 同梱の手順書 + ライセンス表示）\n", archivePath)
}
