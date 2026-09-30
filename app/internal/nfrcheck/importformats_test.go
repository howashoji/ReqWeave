package nfrcheck

// 取り込み対象形式の列挙が、実装と文書のすべてで揃っていることを確認する（PowerPoint を加えた後の漏れを捕まえる）。
//
// **なぜ構造で縛るか**: 対応形式は実装（拡張子判定・ファイル選択のフィルタ・エラー文言）と
// 文書（要件・設計・利用手引き）の**十数か所に散らばって列挙されている**。形式を 1 つ足すたびに
// どこか 1 か所を書き漏らし、利用者向けの文言だけが古いまま残る（実際 pptx の追加で 8 か所を直した）。
// 「1 つでも古い列挙が残っていたら落ちる」検査を置き、書き漏らしを機械で捕まえる。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSourceFormatEnumerationsIncludePptx は、実装コードの中で
// docx・xlsx・pdf を並べて列挙している行が pptx も含むことを確認する。
func TestSourceFormatEnumerationsIncludePptx(t *testing.T) {
	root := appRoot(t)

	scanned, found := 0, 0
	walkSource(t, root, func(rel string, lineNo int, line string) {
		if !strings.Contains(line, "docx") || !strings.Contains(line, "xlsx") || !strings.Contains(line, "pdf") {
			return
		}
		found++
		if !strings.Contains(line, "pptx") {
			t.Errorf("%s:%d 対応形式の列挙に pptx が無い: %s", rel, lineNo, strings.TrimSpace(line))
		}
	}, &scanned)

	if scanned == 0 {
		t.Fatal("走査が空振りしている: app/ のソースを 1 件も読めていない")
	}
	if found == 0 {
		t.Fatal("走査が空振りしている: 対応形式を列挙した行が 1 件も見つからない（書式が変わった可能性）")
	}
	t.Logf("走査したファイル: %d 件 / 対応形式を列挙した行: %d 件", scanned, found)
}

// TestDocumentFormatEnumerationsIncludePowerPoint は、成果物文書の中で
// Word・Excel・PDF を並べて列挙している行が PowerPoint も含むことを確認する。
//
// **開発側の決定記録（decisions.md）は対象外**とする。決定記録は追記のみで既存の行を書き換えない運用であり、
// 過去の決定にある「Word / Excel / PDF を初版必須」は**当時そう決めたという事実**として残す
// （覆す決定は新しい決定として追記する）。
func TestDocumentFormatEnumerationsIncludePowerPoint(t *testing.T) {
	root := repoRootFromApp(t)

	scanned, found := 0, 0
	err := filepath.Walk(filepath.Join(root, "docs"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == filepath.Join("docs", "00-project", "decisions.md") {
			return nil // 追記のみの記録（上のコメント）
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		scanned++
		for i, line := range strings.Split(string(b), "\n") {
			// 日本語表記の列挙（Word / Excel / PDF）。
			if strings.Contains(line, "Word") && strings.Contains(line, "Excel") && strings.Contains(line, "PDF") {
				found++
				if !strings.Contains(line, "PowerPoint") {
					t.Errorf("%s:%d 対応形式の列挙に PowerPoint が無い: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
			// 拡張子・保存値の列挙（docx / xlsx / pdf）。設計書のスキーマ定義がこの形。
			if strings.Contains(line, "docx") && strings.Contains(line, "xlsx") && strings.Contains(line, "pdf") {
				found++
				if !strings.Contains(line, "pptx") {
					t.Errorf("%s:%d 対応形式の列挙に pptx が無い: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("docs/ の走査に失敗: %v", err)
	}
	if scanned == 0 {
		t.Fatal("走査が空振りしている: docs/ の Markdown を 1 件も読めていない")
	}
	if found == 0 {
		t.Fatal("走査が空振りしている: 対応形式を列挙した行が 1 件も見つからない（書式が変わった可能性）")
	}
	t.Logf("走査した文書: %d 件 / 対応形式を列挙した行: %d 件", scanned, found)
}

// walkSource は app/ 配下の Go・TypeScript を 1 行ずつ渡す（生成物・依存は除く）。
func walkSource(t *testing.T, root string, fn func(rel string, lineNo int, line string), scanned *int) {
	t.Helper()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", "dist", "build", ".gates":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".go", ".ts", ".tsx":
		default:
			return nil
		}
		if filepath.Base(p) == "importformats_test.go" {
			return nil // 本検査そのもの（判定式に形式名が並ぶため自己ヒットする）
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		*scanned++
		for i, line := range strings.Split(string(b), "\n") {
			fn(rel, i+1, line)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("app/ の走査に失敗: %v", err)
	}
}

// appRoot は app/ を返す。
func appRoot(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p, "go.mod")); err != nil {
		t.Fatalf("app/ を特定できない（%s に go.mod が無い）: %v", p, err)
	}
	return p
}

// repoRootFromApp はリポジトリのルート（docs/ を持つ階層）を返す。
func repoRootFromApp(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p, "docs")); err != nil {
		t.Fatalf("リポジトリのルートを特定できない（%s に docs が無い）: %v", p, err)
	}
	return p
}
