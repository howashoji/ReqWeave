//go:build integration

// 結合テスト（エクスポート × 実ファイル）。

package docgen

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// generatedGenerator は要件定義のドラフトを生成済みの生成器を返す。
func generatedGenerator(t *testing.T) (*Generator, *projectstore.Store) {
	t.Helper()
	stub := &stubAdapter{}
	g, store := newTestGenerator(t, stub)
	ch, err := g.Generate(context.Background(), projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	return g, store
}

// 対象と出力先を指定すると成果物の全ファイルと共通文書が出力される。
func TestExportWritesAllFiles(t *testing.T) {
	g, _ := generatedGenerator(t)
	dest := filepath.Join(t.TempDir(), "export")

	got, err := g.Export(ExportRequest{Destination: dest, AcceptWarnings: true})
	if err != nil {
		t.Fatalf("エクスポートに失敗: %v", err)
	}
	if !got.Exported {
		t.Fatalf("出力されていない: %+v", got)
	}

	for _, want := range []string{
		"CLAUDE.md", "feedback-template.md", "export-report.md",
		"00-project/glossary.md", "00-project/decisions.md", "00-project/issues.md",
		"10-requirements/00-index.md", "10-requirements/01-business-context.md",
		"10-requirements/12-risks-assumptions.md",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s が出力されていない: %v", want, err)
		}
	}
	// 13 文書 + 共通 3 + 導入・様式・レポート 3 = 19 ファイル。
	if len(got.Files) != 19 {
		t.Errorf("出力ファイル数が違う: %d %+v", len(got.Files), got.Files)
	}
}

// 導入ファイルが同梱ファイルへの相対リンクを持ち、リンク先が出力フォルダ内で解決できる。
func TestExportIntroLinksResolve(t *testing.T) {
	g, _ := generatedGenerator(t)
	dest := filepath.Join(t.TempDir(), "export")
	if _, err := g.Export(ExportRequest{Destination: dest, AcceptWarnings: true}); err != nil {
		t.Fatal(err)
	}

	intro, err := os.ReadFile(filepath.Join(dest, IntroFileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(intro)
	// 導入ファイルの 8 章。
	for i, want := range []string{"1. 対象システム概要", "2. 読み順", "3. ID 規約", "4. 用語集の位置と遵守",
		"5. 未決事項の扱い", "6. 実装時の遵守事項", "7. フィードバックの返し方", "8. 未解決事項"} {
		if !strings.Contains(text, want) {
			t.Errorf("導入ファイルに第 %d 章（%s）が無い", i+1, want)
		}
	}
	// 相対リンクがすべて出力フォルダ内で解決できる。
	links := regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(text, -1)
	if len(links) == 0 {
		t.Fatal("導入ファイルにリンクが無い")
	}
	for _, m := range links {
		target := m[1]
		if strings.HasPrefix(target, "/") || strings.Contains(target, "://") {
			t.Errorf("絶対パス・外部リンクが含まれる: %s", target)
			continue
		}
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(target))); err != nil {
			t.Errorf("リンク先が解決できない: %s", target)
		}
	}
	// フィードバック様式への参照。
	if !strings.Contains(text, FeedbackTemplateFileName) {
		t.Error("フィードバック様式への参照が無い")
	}
}

// フィードバック様式が記入項目 4 種と 1 ブロック 1 論点の構造を持つ。
func TestExportFeedbackTemplate(t *testing.T) {
	g, _ := generatedGenerator(t)
	dest := filepath.Join(t.TempDir(), "export")
	if _, err := g.Export(ExportRequest{Destination: dest, AcceptWarnings: true}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, FeedbackTemplateFileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"- 種別:", "- 関連 ID:", "- 内容:", "- 実装への影響:", "1 ブロック 1 論点"} {
		if !strings.Contains(text, want) {
			t.Errorf("フィードバック様式に %q が無い:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "記入例") {
		t.Error("記入例が無い")
	}
}

// 違反があるとき既定では出力せず、検証結果を返す。
func TestExportBlockedByViolations(t *testing.T) {
	g, _ := generatedGenerator(t)
	dest := filepath.Join(t.TempDir(), "export")

	got, err := g.Export(ExportRequest{Destination: dest})
	if err != nil {
		t.Fatalf("エクスポートの呼び出しに失敗: %v", err)
	}
	// テストデータにはブロックする未決事項があるため V3 のエラーが出る。
	if got.Exported {
		t.Fatal("違反があるのに出力された")
	}
	if got.Verification.Errors() == 0 {
		t.Fatalf("違反が返らない: %+v", got.Verification)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("出力先が作られてしまった")
	}

	// 警告付きを選ぶと出力し、違反一覧が導入ファイルとレポートに載る。
	got, err = g.Export(ExportRequest{Destination: dest, AcceptWarnings: true})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exported {
		t.Fatal("警告付きでも出力されない")
	}
	intro, _ := os.ReadFile(filepath.Join(dest, IntroFileName))
	if !strings.Contains(string(intro), "警告付きで出力") {
		t.Errorf("導入ファイルに未解決事項が明記されない:\n%s", intro)
	}
	report, _ := os.ReadFile(filepath.Join(dest, ExportReportFileName))
	if !strings.Contains(string(report), "V3") {
		t.Errorf("検証レポートに違反が載らない:\n%s", report)
	}
}

// 違反ゼロなら検証合格を返し、導入ファイルにも記す。
func TestExportPassesVerification(t *testing.T) {
	g, store := generatedGenerator(t)
	// ブロックする未決事項を決着させ、受け入れ条件を満たす状態にする。
	d, err := store.CreateDecision(projectstore.Decision{TopicKey: "functional-requirements/list",
		Body: "ロット単位で引き当てる。", Evidence: []string{"S-0001#utt-00003"}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CurrentBaseline("ISS-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveOpenIssueGuarded(base, "ISS-001", d.ID); err != nil {
		t.Fatal(err)
	}
	// 生成し直して本文を最新にする。
	ch, err := g.Generate(context.Background(), projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)

	dest := filepath.Join(t.TempDir(), "export")
	got, err := g.Export(ExportRequest{Destination: dest})
	if err != nil {
		t.Fatal(err)
	}
	if got.Verification.Errors() != 0 {
		t.Fatalf("エラーが残っている: %+v", got.Verification.Violations)
	}
	if !got.Exported {
		t.Fatal("違反ゼロなのに出力されない")
	}
	intro, _ := os.ReadFile(filepath.Join(dest, IntroFileName))
	if !strings.Contains(string(intro), "検証合格") {
		t.Errorf("検証合格が記されない:\n%s", intro)
	}
}

// 出力先の既存ファイルを壊さず、キーを含めない。
func TestExportSafety(t *testing.T) {
	g, _ := generatedGenerator(t)
	dest := filepath.Join(t.TempDir(), "export")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(existing, []byte("既存の内容"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := g.Export(ExportRequest{Destination: dest, AcceptWarnings: true}); err == nil {
		t.Fatal("既存フォルダへ上書き出力できた")
	}
	body, err := os.ReadFile(existing)
	if err != nil || string(body) != "既存の内容" {
		t.Errorf("既存ファイルが壊れた: %q %v", body, err)
	}

	// 出力内容にキー・キー参照名が含まれない。
	dest2 := filepath.Join(t.TempDir(), "export2")
	if _, err := g.Export(ExportRequest{Destination: dest2, AcceptWarnings: true}); err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(dest2, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range []string{"key_ref", "sk-ant-", "reqweave/anthropic"} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s に %q が含まれる", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 出力先を指定しないときはエラー。
	if _, err := g.Export(ExportRequest{}); err == nil {
		t.Error("出力先なしで実行できた")
	}
}
