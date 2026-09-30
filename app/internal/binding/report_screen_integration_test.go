//go:build integration

package binding

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// today は当日の日付（ローカル暦日。期間指定と同じ解釈）。
func today() string { return time.Now().Local().Format("2006-01-02") }

// 期間を指定して 7 章（該当時）のレポートを生成し、
// Markdown ファイルへ出力できる。クリップボードコピーは同一の本文を返す。
func TestProgressReportBinding(t *testing.T) {
	a, _, _ := openWithRecords(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	root := s.store.Root()
	before := hashProjectTree(t, root)

	req := ProgressReportRequest{From: today(), To: today()}
	got, err := a.ProgressReport(req)
	if err != nil {
		t.Fatalf("生成に失敗: %v", err)
	}
	for _, want := range []string{
		"# 進捗レポート: 在庫管理システム",
		"## 1. 要約", "## 2. 決定事項", "## 3. 未決事項の動き",
		"## 4. 完成度", "## 5. 回答待ち質問票", "## 6. 確定をブロックしている要因",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("章が組み立っていない: %q\n%s", want, got.Markdown)
		}
	}
	// フィードバックが 0 件のプロジェクトでは章7 を出さない。
	if strings.Contains(got.Markdown, "## 7.") {
		t.Errorf("フィードバック 0 件なのに章7 が出た:\n%s", got.Markdown)
	}
	// 当日に起票した未決事項が「新規に起票」へ現れる（変更履歴からの判定）。
	if !strings.Contains(got.Markdown, "ISS-001") {
		t.Errorf("当日の動きが反映されていない:\n%s", got.Markdown)
	}

	// ファイル出力（プロジェクトフォルダの外）。
	dst := filepath.Join(t.TempDir(), "progress-report.md")
	saved, err := a.SaveProgressReport(req, dst)
	if err != nil {
		t.Fatalf("保存に失敗: %v", err)
	}
	if saved != dst {
		t.Errorf("保存先が違う: %q", saved)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("保存したファイルを読めない: %v", err)
	}
	if string(data) != got.Markdown {
		t.Error("保存内容が生成結果と一致しない（出力経路で本文が変わっている）")
	}

	// 事後条件: 生成・出力でプロジェクトデータは変化しない。
	if diff := diffTrees(before, hashProjectTree(t, root)); len(diff) != 0 {
		t.Errorf("プロジェクトデータが変化した: %v", diff)
	}
}

// 期間指定の不備は日本語で拒否する（内部用語・コード値を出さない）。
func TestProgressReportBindingRejectsInvalidPeriod(t *testing.T) {
	a, _, _ := openWithRecords(t)

	if _, err := a.ProgressReport(ProgressReportRequest{From: "", To: today()}); err == nil {
		t.Error("開始日なしが受理された")
	}
	if _, err := a.ProgressReport(ProgressReportRequest{From: "2026/08/01", To: today()}); err == nil {
		t.Error("形式違いが受理された")
	} else if !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("形式の案内が無い: %v", err)
	}
	if _, err := a.ProgressReport(ProgressReportRequest{From: today(), To: "2000-01-01"}); err == nil {
		t.Error("開始より前に終わる期間が受理された")
	}
	if _, err := a.SaveProgressReport(ProgressReportRequest{From: today(), To: today()}, " "); err == nil {
		t.Error("保存先なしが受理された")
	}
}

// 閲覧権限でも進捗レポートは生成できる（読み出しのみの操作）。
func TestProgressReportBindingAllowsViewer(t *testing.T) {
	a, _, _ := openWithRecords(t)
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	demoteToViewer(t, s.store.Root(), "k.sato@example.co.jp")

	got, err := a.ProgressReport(ProgressReportRequest{From: today(), To: today()})
	if err != nil {
		t.Fatalf("閲覧権限で生成できない: %v", err)
	}
	if !strings.Contains(got.Markdown, "# 進捗レポート") {
		t.Errorf("レポートが組み立っていない:\n%s", got.Markdown)
	}
}

func hashProjectTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("プロジェクトフォルダを走査できない: %v", err)
	}
	return out
}

func diffTrees(before, after map[string]string) []string {
	var diff []string
	for path, sum := range after {
		if before[path] != sum {
			diff = append(diff, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			diff = append(diff, path+"（消滅）")
		}
	}
	return diff
}
