// depcheck はモジュール間の依存規則（どの層が何を import し、何を起動してよいか）と、
// 設計上の構造的な担保（TLS の証明書検証を無効にしない・AI 呼び出しの同意確認を迂回しない）を機械検知する。
// `make lint` から実行され、違反が 1 件でもあれば非ゼロ終了する。
//
// 規則は設計（モジュールの分担と通信経路）を写したもの。検査を通すために規則のほうを緩めない。
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// frontendTargets は行単位検査の対象（app/ からの相対パス）。
var frontendTargets = []string{"frontend/src", "frontend/index.html"}

// distTargets は配布物に取り込まれる最終成果物（未ビルドなら黙って飛ばす）。
var distTargets = []string{"frontend/dist"}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depcheck: 対象ディレクトリを解決できません: %v\n", err)
		os.Exit(2)
	}

	violations, err := run(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "depcheck: 走査に失敗しました: %v\n", err)
		os.Exit(2)
	}
	if len(violations) == 0 {
		fmt.Printf("depcheck: 依存規則違反なし（Go import %d 規則 / Go 識別子 %d 規則 / フロントエンド %d 規則）\n", len(goRules)+len(goDirRules), len(goTextRules), len(frontendRules))
		return
	}
	fmt.Fprintf(os.Stderr, "✘ 依存規則違反 %d 件:\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  %s\n", v)
	}
	os.Exit(1)
}

func run(root string) ([]Violation, error) {
	violations, err := CheckGo(root)
	if err != nil {
		return nil, err
	}
	goText, err := CheckGoText(root)
	if err != nil {
		return nil, err
	}
	violations = append(violations, goText...)
	fe, err := CheckFrontend(root, frontendTargets)
	if err != nil {
		return nil, err
	}
	violations = append(violations, fe...)
	dist, err := CheckBundledAssets(root, distTargets)
	if err != nil {
		return nil, err
	}
	return append(violations, dist...), nil
}
