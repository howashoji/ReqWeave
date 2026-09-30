// signidentity は macOS の配布物へ署名するときの署名 ID（証明書の SHA-1）を 1 つに決める。
//
//	security find-identity -v -p codesigning | go run ./tools/signidentity [-identity <SHA-1 か名前>]
//
// 決まれば SHA-1 を標準出力へ 1 行で出し、終了コード 0 で終わる（どの証明書に決まったかは標準エラーへ出す）。
// 決まらなければ理由と候補を標準エラーへ出し、標準出力には何も出さずに終了コード 1 で終わる。
//
// codesign へ名前を渡すと、同じ名前の証明書がキーチェーンに 2 枚あるとき（作り直した証明書など）に
// ambiguous で止まる。SHA-1 なら 1 枚に定まるので、Makefile は常にここで決めた SHA-1 を codesign へ渡す。
// キーチェーンを読むのは呼び出し側（security）で、この道具は標準入力の文字列しか見ない（試験で固定の入力を与えられるように）。
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("signidentity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	explicit := fs.String("identity", "", "使う証明書の SHA-1（16 進数 40 桁）か名前。空なら Developer ID Application の証明書が 1 枚だけのときにそれを使う")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	listing, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "signidentity: 標準入力を読めません: %v\n", err)
		return 2
	}
	id, err := resolve(string(listing), *explicit)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stderr, "署名 ID: %s（%s）\n", id.SHA1, id.Name)
	fmt.Fprintln(stdout, id.SHA1)
	return 0
}
