//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// makeImportTree は一括取り込みの検証用フォルダを作る。
//
//	root/
//	  現行業務.md          … 対象
//	  メモ.txt             … 対象
//	  図.png               … 対象外の形式
//	  .DS_Store            … 隠しファイル（理由を出さずに除外）
//	  sub/仕様.md          … 対象（再帰）
//	  .git/config          … 隠しディレクトリ（丸ごと除外）
func makeImportTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("現行業務.md", materialBody)
	write("メモ.txt", "棚卸は月次で行う。\n")
	write("図.png", "x")
	write(".DS_Store", "x")
	write("sub/仕様.md", "# 仕様\n\n受注は当日締め。\n")
	write(".git/config", "[core]\n")
	return root
}

// ディレクトリを指定すると、サブディレクトリを含む対象の一覧と件数が提示される。
func TestScanImportDirectoryListsTargetsAndCount(t *testing.T) {
	a, _ := openForImports(t)
	root := makeImportTree(t)

	view, err := a.ScanImportDirectory(root)
	if err != nil {
		t.Fatalf("フォルダを調べられない: %v", err)
	}
	if view.Count != 3 {
		t.Fatalf("対象の件数が違う（再帰していない可能性）: %d / %+v", view.Count, view.Files)
	}
	got := make([]string, 0, len(view.Files))
	for _, f := range view.Files {
		got = append(got, f.Name)
		if f.Size <= 0 {
			t.Errorf("大きさが取れていない: %+v", f)
		}
	}
	want := []string{"sub/仕様.md", "メモ.txt", "現行業務.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("対象の一覧が違う: got %v, want %v", got, want)
	}
	if view.TotalBytes <= 0 {
		t.Errorf("合計の大きさが取れていない: %+v", view)
	}

	// 対象外は理由つきで示す。隠しファイル・隠しディレクトリは理由を出さずに除く。
	if view.SkippedCount != 1 {
		t.Fatalf("対象外の件数が違う: %d / %+v", view.SkippedCount, view.Skipped)
	}
	if view.Skipped[0].Name != "図.png" || !strings.Contains(view.Skipped[0].Reason, "対象の形式ではありません") {
		t.Errorf("対象外の理由が違う: %+v", view.Skipped[0])
	}
	if view.Blocked != "" {
		t.Errorf("実行できない理由が付いた: %q", view.Blocked)
	}

	// この時点では 1 件も取り込まれていない（確認操作の前は取り込まない）。
	list, err := a.Imports()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("確認前に取り込まれた: %+v", list)
	}
}

// 確認後に一括で取り込め、種別は 1 つが一括適用され、AI 分析は走らない。
func TestImportDirectoryImportsAllWithSingleKind(t *testing.T) {
	a, stub := openForImports(t)
	root := makeImportTree(t)

	res, err := a.ImportDirectory(root, string(importer.KindMinutes))
	if err != nil {
		t.Fatalf("一括取り込みに失敗: %v", err)
	}
	if res.ImportedCount != 3 || res.FailedCount != 0 || res.SkippedCount != 1 {
		t.Fatalf("結果の件数が違う: %+v", res)
	}
	for _, v := range res.Imported {
		if v.KindLabel != "議事録" {
			t.Errorf("種別が一括適用されていない: %+v", v)
		}
		// 元ファイル名はベース名のみ（端末固有のパスを残さない）。
		if strings.ContainsAny(v.SourceName, `/\`) {
			t.Errorf("元ファイル名にパスが含まれる: %q", v.SourceName)
		}
	}
	if !strings.Contains(res.Notice, "3 件を取り込みました") || !strings.Contains(res.Notice, "分析は行っていません") {
		t.Errorf("結果の案内が要件を満たさない: %q", res.Notice)
	}

	list, err := a.Imports()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("一覧に反映されていない: %+v", list)
	}

	// **AI へ 1 回も送っていない**（一括では分析しない。件数ぶん AI を呼ぶと利用量が一度に膨らむため）。
	if stub.calls != 0 {
		t.Errorf("一括取り込みで AI を呼んだ: %d 回", stub.calls)
	}
}

// 読めなかったファイルがあっても全体を止めず、取り込めたものだけが登録される。
func TestImportDirectoryContinuesAfterUnreadableFile(t *testing.T) {
	// root では 0o000 のファイルも読めてしまい、この検証が成立しない（飛ばさず落とす）。
	if os.Geteuid() == 0 {
		t.Fatal("root で実行しないこと（権限で読めないファイルを作れないため検証が成立しない）")
	}
	a, _ := openForImports(t)
	root := t.TempDir()
	ok := filepath.Join(root, "読める.md")
	if err := os.WriteFile(ok, []byte("# 読める\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ng := filepath.Join(root, "読めない.md")
	if err := os.WriteFile(ng, []byte("# 読めない\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	res, err := a.ImportDirectory(root, string(importer.KindMaterial))
	if err != nil {
		t.Fatalf("一括取り込みが中止された: %v", err)
	}
	if res.ImportedCount != 1 || res.FailedCount != 1 {
		t.Fatalf("失敗 1 件で全体が止まった／数が合わない: %+v", res)
	}
	if res.Imported[0].SourceName != "読める.md" {
		t.Errorf("取り込めたものが違う: %+v", res.Imported[0])
	}
	if res.Failed[0].Name != "読めない.md" || !strings.Contains(res.Failed[0].Reason, "読み込めませんでした") {
		t.Errorf("失敗の理由が示されていない: %+v", res.Failed[0])
	}
	if !strings.Contains(res.Notice, "1 件は取り込めませんでした") {
		t.Errorf("失敗件数が案内に無い: %q", res.Notice)
	}
}

// 取り込むと原本の総量の上限を超える場合は実行しない。
func TestImportDirectoryStopsWhenScaleLimitWouldBeExceeded(t *testing.T) {
	a, _ := openForImports(t)
	root := t.TempDir()
	// 上限（500MB）を 1 ファイルで超える大きさの原本を作る（実体は疎ファイル）。
	big := filepath.Join(root, "大きい資料.txt")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(projectstore.ScaleLimitBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	view, err := a.ScanImportDirectory(root)
	if err != nil {
		t.Fatalf("フォルダを調べられない: %v", err)
	}
	if view.Blocked == "" {
		t.Fatalf("上限を超えるのに実行できてしまう: %+v", view)
	}
	if !strings.Contains(view.Blocked, "上限の目安") {
		t.Errorf("止めた理由が伝わらない: %q", view.Blocked)
	}
	if _, err := a.ImportDirectory(root, string(importer.KindMaterial)); err == nil {
		t.Error("上限を超える一括取り込みが実行された")
	}
	list, err := a.Imports()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("止めたのに取り込まれた: %+v", list)
	}
}

// 閲覧権限では一括取り込みを実行できない（理由を示す）。
func TestImportDirectoryDeniesViewer(t *testing.T) {
	a, _ := openForImports(t)
	root := makeImportTree(t)
	demoteToViewer(t, projectRootOf(t, a), "k.sato@example.co.jp")

	if _, err := a.ScanImportDirectory(root); err == nil {
		t.Error("閲覧権限でフォルダを調べられた")
	}
	if _, err := a.ImportDirectory(root, string(importer.KindMaterial)); err == nil {
		t.Error("閲覧権限で一括取り込みができた")
	}
}
