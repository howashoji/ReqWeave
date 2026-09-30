package binding

// 単体テスト（構造の固定）: **AI 呼び出しを伴う公開バインディングは、例外なく共通前段
// （beginAICall）を通る**（トークン上限の判定を画面や機能ごとに分散させない）。
//
// 実装が増えたときに機械で検出するため、go/ast で呼び出し関係を走査して判定する:
//  1. dialogue / docgen で AI 送信の唯一の入口（aiprovider.StreamRetrying）へ
//     到達する公開メソッドを求める。
//  2. binding のうち、それらへ到達する関数を求める（= AI 実行口）。
//  3. その全てが beginAICall へ到達することを検証する。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// aiCallBases は「この式に対するメソッド呼び出しは AI 実行口とみなす」基底式（binding 側）。
// 対話エンジンは s.engine、成果物生成は generator という変数名で保持している。
var aiCallBases = map[string]bool{"s.engine": true, "generator": true}

// funcCalls は「関数名 → その本文が呼んでいる名前の集合」。
// レシーバ経由の呼び出し（e.foo / a.bar）と aiCallBases 経由の呼び出しは素の名前で、
// それ以外のパッケージ・変数経由の呼び出しは "基底式.名前" で記録する
// （importer.Extract と対話エンジンの Extract のような同名衝突を避けるため）。
func funcCalls(t *testing.T, dir string) (calls map[string]map[string]bool, exportedMethods map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("%s を解析できない: %v", dir, err)
	}
	calls, exportedMethods = map[string]map[string]bool{}, map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				recv := ""
				if fn.Recv != nil && len(fn.Recv.List) > 0 && len(fn.Recv.List[0].Names) > 0 {
					recv = fn.Recv.List[0].Names[0].Name
				}
				if fn.Recv != nil && fn.Name.IsExported() {
					exportedMethods[fn.Name.Name] = true
				}
				body := map[string]bool{}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch f := call.Fun.(type) {
					case *ast.Ident:
						body[f.Name] = true
					case *ast.SelectorExpr:
						base := exprText(f.X)
						if (recv != "" && base == recv) || aiCallBases[base] {
							body[f.Sel.Name] = true
						} else {
							body[base+"."+f.Sel.Name] = true
						}
					}
					return true
				})
				calls[fn.Name.Name] = body
			}
		}
	}
	return calls, exportedMethods
}

func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	}
	return "?"
}

// reachesAny は start から targets のいずれかへ（同一パッケージ内の呼び出しをたどって）到達するか。
func reachesAny(calls map[string]map[string]bool, start string, targets map[string]bool) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		for callee := range calls[name] {
			if targets[callee] || walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(start)
}

// 前段（beginAICall）を通らない AI 実行口が存在しない。
func TestEveryAIEntryPointGoesThroughUsageGuard(t *testing.T) {
	// 1. AI 送信へ到達する dialogue / docgen の公開メソッド。
	streamEntry := map[string]bool{"aiprovider.StreamRetrying": true}
	aiMethods := map[string]bool{}
	for _, dir := range []string{"../dialogue", "../docgen"} {
		calls, exported := funcCalls(t, dir)
		for name := range calls {
			if exported[name] && reachesAny(calls, name, streamEntry) {
				aiMethods[name] = true
			}
		}
	}
	// 走査が空振りしていないこと（0 件で緑にしない）。
	if len(aiMethods) < 5 {
		t.Fatalf("AI 送信へ到達する公開メソッドの検出が不足している: %v", sortedKeys(aiMethods))
	}
	for _, want := range []string{"GenerateQuestion", "Extract", "AnalyzeImport", "AnalyzeAnswers", "Generate"} {
		if !aiMethods[want] {
			t.Errorf("AI を呼ぶはずの %s が検出されていない（走査が壊れている）", want)
		}
	}

	// 2. binding のうち AI 実行口へ到達する関数。
	calls, exported := funcCalls(t, ".")
	var entries []string
	for name := range calls {
		if reachesAny(calls, name, aiMethods) {
			entries = append(entries, name)
		}
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		t.Fatal("binding に AI 実行口が 1 つも見つからない（走査が壊れている）")
	}

	// 3. すべてが前段を通ること。
	guard := map[string]bool{"beginAICall": true}
	for _, name := range entries {
		if !reachesAny(calls, name, guard) {
			t.Errorf("AI 実行口 %s が上限判定の前段（beginAICall）を通っていない", name)
		}
	}

	// 4. 公開バインディングの AI 実行口が、設計が挙げる操作を網羅していること
	//    （対話・回答分析・成果物生成・取り込み分析・質問文生成）。
	for _, want := range []string{
		"AskNextQuestion", "SendAnswer", "AnalyzeImportedAnswers",
		"GenerateDocument", "AnalyzeImport", "GenerateQuestionDrafts",
	} {
		if !exported[want] {
			t.Errorf("公開バインディング %s が存在しない（名前が変わったら本テストも更新する）", want)
			continue
		}
		if !reachesAny(calls, want, aiMethods) {
			t.Errorf("%s が AI 実行口として検出されない（走査条件が実装とずれている）", want)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// **回答モードの AI 呼び出しはプロジェクトのトークン上限の対象外**である
// （本人の端末・本人の資格で行われ、プロジェクトの記録に残らないため）。
//
// 回答モードは抽象化層（aiprovider.StreamRetrying）を binding から直接呼ぶため、上の走査
// （dialogue / docgen 経由の AI 実行口を探す）には掛からない。**掛からないことを放置すると、
// 「網から黙って外れた」のか「対象外だと決めた」のかが後から区別できない**。
// そこで、直接呼びの経路を名前で列挙して固定し、それ以外が現れたら赤くする。
func TestRespondModeAIEntryPointsAreExplicitlyOutOfScope(t *testing.T) {
	calls, exported := funcCalls(t, ".")
	stream := map[string]bool{"aiprovider.StreamRetrying": true}

	var direct []string
	for name := range calls {
		if reachesAny(calls, name, stream) {
			direct = append(direct, name)
		}
	}
	sort.Strings(direct)
	if len(direct) == 0 {
		t.Fatal("binding から AI 送信の入口へ到達する関数が 1 つも見つからない（走査が壊れている）")
	}

	// 上限判定の前段を通さずに AI 送信へ到達してよいのは、ここに挙げた回答モードの経路だけ。
	// **増やすときは設計で対象外と定めた範囲かを確かめてから**足す。
	outOfScope := map[string]bool{
		"SendRespondAIMessage": true, // 公開バインディング（回答モードの画面の下部ペイン）
		"streamRespondAI":      true, // その内部（1 回のストリーミング）
	}
	for _, name := range direct {
		if !outOfScope[name] {
			t.Errorf("%s が上限判定の前段（beginAICall）を通らずに AI 送信へ到達している。"+
				"担当者モードの経路なら前段を通すこと、回答モードの経路なら本テストへ明示すること", name)
		}
	}
	for name := range outOfScope {
		if _, ok := calls[name]; !ok {
			t.Errorf("対象外として挙げた %s が実装に存在しない（名前が変わったら本テストも更新する）", name)
		}
	}
	if !exported["SendRespondAIMessage"] {
		t.Error("回答モードの AI 対話の公開バインディング SendRespondAIMessage が存在しない")
	}
	// 対象外である以上、前段を通ってはいけない（通ると回答モードがプロジェクトの上限に縛られる）。
	if reachesAny(calls, "SendRespondAIMessage", map[string]bool{"beginAICall": true}) {
		t.Error("回答モードの AI 呼び出しがプロジェクトの上限判定（beginAICall）を通っている" +
			"（回答モードは上限の対象外）")
	}
}
