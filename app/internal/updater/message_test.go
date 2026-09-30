package updater

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// actionPhrases は「利用者が次に取る行動」を示す語尾（「原因＋次に取る行動」の様式）。
var actionPhrases = []string{"ください"}

// forbiddenInMessages は利用者向け文言に出してはならない内部情報の手掛かり。
// 具体値（URL・ハッシュ・鍵）は診断用の cause 側にだけ置く。
var forbiddenInMessages = []string{
	"http", "sha256", "SHA-256", "ed25519", "base64", "json", "JSON",
	"status", "manifest", "%v", "%w", "%q", "%d", "%s",
}

// 本パッケージが返す利用者向け文言のすべてが、
// 「原因＋利用者が次に取る行動」の様式で、内部情報を含まないこと。
//
// 個別のテストで代表例だけを見ると、あとから足した文言が素通りする。
// ソースを走査して `msg:` に与えた文字列リテラルを**全件**検査する。
func TestAllUserFacingMessagesFollowCatalogStyle(t *testing.T) {
	messages := collectMessages(t)
	if len(messages) < 10 {
		t.Fatalf("検査した文言が %d 件しかない（走査が効いていない可能性）", len(messages))
	}
	for _, m := range messages {
		if strings.TrimSpace(m.text) == "" {
			t.Errorf("%s: 空の文言", m.pos)
			continue
		}
		hasAction := false
		for _, p := range actionPhrases {
			if strings.Contains(m.text, p) {
				hasAction = true
				break
			}
		}
		if !hasAction {
			t.Errorf("%s: 次に取る行動が書かれていない: %q", m.pos, m.text)
		}
		for _, bad := range forbiddenInMessages {
			if strings.Contains(m.text, bad) {
				t.Errorf("%s: 内部情報 %q が文言に含まれる: %q", m.pos, bad, m.text)
			}
		}
		// 句点で終える（呼び出し側が「。」を付ける前提のため、文言自体は付けない）。
		if strings.HasSuffix(m.text, "。") {
			t.Errorf("%s: 文言が句点で終わっている（付与は表示側の責務）: %q", m.pos, m.text)
		}
	}
}

type message struct {
	text string
	pos  string
}

// collectMessages は本パッケージの Go ソースから `msg:` に与えた文字列リテラルを集める。
func collectMessages(t *testing.T) []message {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []message
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("%s の解析に失敗: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			ident, ok := kv.Key.(*ast.Ident)
			if !ok || ident.Name != "msg" {
				return true
			}
			text, ok := literalText(kv.Value)
			if !ok {
				// 定数・変数を渡している場合は、その定義側で別途検査される。
				return true
			}
			out = append(out, message{text: text, pos: fset.Position(kv.Pos()).String()})
			return true
		})
	}
	// 定数として定義した文言も対象に含める。
	out = append(out, message{text: malformedManifestMessage, pos: "manifest.go: malformedManifestMessage"})
	return out
}

// literalText は式が単純な文字列リテラルならその中身を返す。
func literalText(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// NewErrorForTest が返す文言も同じ様式に従うこと
// （バインディング側のテストがこの関数を通して文言を見るため）。
func TestNewErrorForTestMessagesFollowCatalogStyle(t *testing.T) {
	kinds := []Kind{KindMalformed, KindUnsupportedSchema, KindUntrusted, KindNoTrustedKeys,
		KindHashMismatch, KindSizeMismatch, KindNoAsset, KindNetwork, KindNotUserArea}
	for _, k := range kinds {
		msg := NewErrorForTest(k).Message()
		if !strings.Contains(msg, "ください") {
			t.Errorf("%s: 次に取る行動が書かれていない: %q", k, msg)
		}
		if strings.HasSuffix(msg, "。") {
			t.Errorf("%s: 文言が句点で終わっている: %q", k, msg)
		}
	}
	// 未知の種別でも既定の文言に倒れること（生のコード値を画面に出さない）。
	fallback := NewErrorForTest(Kind("unknown-kind")).Message()
	if strings.Contains(fallback, "unknown-kind") {
		t.Errorf("既定の文言に種別の生値が出ている: %q", fallback)
	}
}
