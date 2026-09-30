package aiprovider

// 構造の機械検査。
//
// ストリーミング用ゴルーチンのパニックは main の recover に届かず、記録なしでプロセスごと落ちる
// （実測で確認済み）。個別の実行テストでは「アダプタが自前で起こすゴルーチン」を再現できない
// （プロバイダ SDK のストリームが要る）ため、**先頭に RecoverStreamPanic の defer があること**を
// 構文木で検査して構造的に担保する。ゴルーチンを足したときの付け忘れもここで落ちる。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// recoverFuncName は各ゴルーチンの先頭に必要な defer の関数名。
const recoverFuncName = "RecoverStreamPanic"

func TestStreamGoroutinesRecoverPanic(t *testing.T) {
	// 抽象化層本体とアダプタ 3 社（プロバイダの追加先も含む）。
	dirs := []string{
		".",
		filepath.Join("adapter", "anthropic"),
		filepath.Join("adapter", "openai"),
		filepath.Join("adapter", "google"),
	}
	found := 0
	for _, dir := range dirs {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("%s を解析できない: %v", dir, err)
		}
		for _, pkg := range pkgs {
			for name, file := range pkg.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					gostmt, ok := n.(*ast.GoStmt)
					if !ok {
						return true
					}
					lit, ok := gostmt.Call.Fun.(*ast.FuncLit)
					if !ok {
						// 関数値を go で起動する形は本層に無い。増えたらここで気づける。
						t.Errorf("%s: 関数リテラルでないゴルーチンがある（recover の付け先を確認すること）",
							fset.Position(gostmt.Pos()))
						return true
					}
					found++
					if !startsWithRecover(lit) {
						t.Errorf("%s: ゴルーチンの先頭に defer %s(...) が無い（%s）",
							fset.Position(gostmt.Pos()), recoverFuncName, filepath.Base(name))
					}
					return true
				})
			}
		}
	}
	// 対象が消えていないこと（検査が空振りしていないことの確認）。
	if found < 4 {
		t.Errorf("検査したゴルーチンが %d 本（期待 4 本以上。検査対象の取りこぼし）", found)
	}
}

// startsWithRecover はゴルーチンの本体の最初の文が RecoverStreamPanic の defer かを返す。
func startsWithRecover(lit *ast.FuncLit) bool {
	if lit.Body == nil || len(lit.Body.List) == 0 {
		return false
	}
	d, ok := lit.Body.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	switch fn := d.Call.Fun.(type) {
	case *ast.Ident: // 同一パッケージ（retry.go）
		return fn.Name == recoverFuncName
	case *ast.SelectorExpr: // アダプタ（aiprovider.RecoverStreamPanic）
		return fn.Sel.Name == recoverFuncName
	}
	return false
}
