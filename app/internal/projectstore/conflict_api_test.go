package projectstore

// 単体テスト（構造の固定）: **基準版なしで共有レコードを書き換える公開 API を持たない**
// （後勝ち上書きの経路を持たない）。
//
// 公開メソッドの一覧を go/ast で走査して検証する（実装が増えたときに機械で検出する）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// unguardedWriters は「基準版を取らずに既存の共有レコードを書き換える」名前の禁止一覧。
//
// 過去に公開していた API 名を含める（外部から呼べる形で復活していないことを確かめる）。
var unguardedWriters = map[string]bool{
	"UpdateRequirement": true,
	"UpdateOpenIssue":   true,
	"ResolveOpenIssue":  true,
	"UpsertTerm":        true,
}

func TestNoUnguardedSharedRecordWriteAPI(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("パッケージを解析できない: %v", err)
	}
	pkg, ok := pkgs["projectstore"]
	if !ok {
		t.Fatalf("projectstore パッケージが見つからない: %v", pkgs)
	}

	found := map[string]bool{}
	methods := 0
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			methods++
			if unguardedWriters[fn.Name.Name] {
				found[fn.Name.Name] = true
			}
		}
	}
	// 走査が空振りしていないこと（メソッドを 1 つも見ていない状態で緑にしない）。
	if methods < 20 {
		t.Fatalf("公開メソッドの走査が不足している: %d 件", methods)
	}
	if len(found) != 0 {
		t.Errorf("基準版を取らない公開 API が存在する（後勝ち上書きの経路になる）: %v", found)
	}

	// 代わりに基準版を要求する API があること（置き換えが成立していること）。
	guarded := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			if strings.HasSuffix(fn.Name.Name, "Guarded") {
				guarded[fn.Name.Name] = true
			}
		}
	}
	for _, want := range []string{"UpdateRequirementGuarded", "UpdateOpenIssueGuarded",
		"ResolveOpenIssueGuarded", "UpsertTermGuarded"} {
		if !guarded[want] {
			t.Errorf("基準版を要求する API が無い: %s", want)
		}
	}
}
