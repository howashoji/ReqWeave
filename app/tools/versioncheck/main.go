package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "versioncheck: 対象ディレクトリを解決できません: %v\n", err)
		os.Exit(2)
	}
	problems, err := check(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "versioncheck: 検査に失敗しました: %v\n%s\n", err, fixHint)
		os.Exit(2)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "✘ 版番号の正本と複製が食い違っています %d 件:\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintf(os.Stderr, "  %s\n", fixHint)
		os.Exit(1)
	}
	fmt.Println("versioncheck: 版番号の正本と wails.json の productVersion が一致しています")
}
