package sync

// 単体テスト（構造の固定）: **承認を経ずに競合を解決する経路を持たない**
// （後勝ち上書きの経路を持たない）。
//
// 保存時の楽観的競合検知を三面マージへ移したことに伴い、
// projectstore の conflict_api_test.go が担っていた構造的担保を同期側へ引き継ぐ。
//
// 固定するのは 3 点:
//  1. 承認を省く公開 API・公開の選択肢を持たない
//  2. Merger の実装は ThreeWayMerger のみ（承認を経ない別実装を公開しない）
//  3. 統合の実装（merge.go / threeway.go）が作業ツリーを書き換える git 操作・git の自動マージ機構を呼ばない
//     （コンフリクトマーカーを作業ツリーへ書かせない）

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// bypassNames は「承認を経ずに解決する」名前の禁止一覧（公開 API・公開の定数・公開フィールド）。
var bypassNames = map[string]bool{
	"ForceMerge":           true,
	"MergeWithoutApproval": true,
	"AutoResolve":          true,
	"ResolveAll":           true,
	"TakeTheirsAll":        true,
	"TakeOursAll":          true,
	"OverwriteWithTheirs":  true,
	"OverwriteWithOurs":    true,
	"ApplyLastWrite":       true,
	"ChoiceDefault":        true,
	"DefaultChoice":        true,
	"DefaultResolution":    true,
	"SkipApproval":         true,
}

func parseSyncPackage(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("パッケージを解析できない: %v", err)
	}
	pkg, ok := pkgs["sync"]
	if !ok {
		t.Fatalf("sync パッケージが見つからない: %v", pkgs)
	}
	return fset, pkg.Files
}

// 1. 承認を省く公開 API・公開の選択肢を持たない。
func TestNoApprovalBypassAPI(t *testing.T) {
	_, files := parseSyncPackage(t)
	found := map[string]bool{}
	exported := 0
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if node.Name.IsExported() {
					exported++
					if bypassNames[node.Name.Name] {
						found[node.Name.Name] = true
					}
				}
			case *ast.ValueSpec:
				for _, name := range node.Names {
					if name.IsExported() {
						exported++
						if bypassNames[name.Name] {
							found[name.Name] = true
						}
					}
				}
			case *ast.Field:
				for _, name := range node.Names {
					if name.IsExported() && bypassNames[name.Name] {
						found[name.Name] = true
					}
				}
			}
			return true
		})
	}
	// 走査が空振りしていないこと（何も見ていない状態で緑にしない）。
	if exported < 40 {
		t.Fatalf("公開識別子の走査が不足している: %d 件", exported)
	}
	if len(found) != 0 {
		t.Errorf("承認を経ずに競合を解決する公開 API・定数がある: %v", found)
	}

	// 承認の受け口が存在すること（置き換えが成立していること）。
	if _, ok := any(ThreeWayMerger{}).(Merger); !ok {
		t.Error("ThreeWayMerger が Merger を満たしていない")
	}
	var _ ConflictResolver = (*stubResolver)(nil)

	// 選択肢は 4 つで、いずれも空文字ではない（既定値のまま統合される値を作らない）。
	if len(ChoiceLabel) != 4 {
		t.Errorf("解決の選択肢の数が違う: %d", len(ChoiceLabel))
	}
	for choice, label := range ChoiceLabel {
		if choice == "" || label == "" {
			t.Errorf("空の選択肢がある: %q → %q", choice, label)
		}
		if !(Resolution{Choice: choice, Merged: "x", Adopt: ChoiceOurs, OpenIssueID: "ISS-001"}).valid() {
			t.Errorf("選択肢 %q が承認として成立しない", choice)
		}
	}
	// 既定値（空の Resolution）は承認として成立しない。
	if (Resolution{}).valid() {
		t.Error("空の解決が承認として成立している（既定の選択を置かない）")
	}
}

// 2. Merger の実装は ThreeWayMerger のみ。
func TestOnlyThreeWayMergerImplementsMerger(t *testing.T) {
	_, files := parseSyncPackage(t)
	impls := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "Merge" {
				continue
			}
			impls[receiverTypeName(fn)] = true
		}
	}
	if len(impls) != 1 || !impls["ThreeWayMerger"] {
		t.Errorf("Merger の実装が ThreeWayMerger だけでない: %v", impls)
	}
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// 3. 統合の実装が作業ツリーを書き換えず、git の自動マージ機構も使わない。
//
// 呼んでよい git のサブコマンドを実装ファイルごとに固定する。増やすときは、その操作が
// 作業ツリー・通常のインデックスに触れないことを確かめたうえで本一覧を更新する。
var mergeGitAllowlist = map[string]map[string]bool{
	// 読み取りのみ
	"Git": {"diff-tree": true, "cat-file": true},
	// 一時インデックス（GIT_INDEX_FILE）上の操作
	"GitEnv": {"read-tree": true, "update-index": true, "write-tree": true},
	// オブジェクトの書き込み
	"GitInput": {"hash-object": true},
}

func TestMergeImplementationDoesNotTouchWorkingTree(t *testing.T) {
	fset, files := parseSyncPackage(t)
	calls := 0
	for name, file := range files {
		base := name[strings.LastIndex(name, "/")+1:]
		if base != "merge.go" && base != "threeway.go" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			allowed, ok := mergeGitAllowlist[sel.Sel.Name]
			if !ok {
				return true
			}
			calls++
			sub := firstStringLiteral(call.Args)
			if sub == "" {
				t.Errorf("%s: git のサブコマンドを定数で書いていない（検査できない）", fset.Position(call.Pos()))
				return true
			}
			if !allowed[sub] {
				t.Errorf("%s: 統合の実装が許可外の git 操作を呼んでいる: %s(%q)",
					fset.Position(call.Pos()), sel.Sel.Name, sub)
			}
			return true
		})
	}
	if calls < 5 {
		t.Fatalf("git 呼び出しの走査が不足している: %d 件", calls)
	}
}

func firstStringLiteral(args []ast.Expr) string {
	for _, arg := range args {
		lit, ok := arg.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			return strings.Trim(lit.Value, `"`)
		}
	}
	return ""
}
