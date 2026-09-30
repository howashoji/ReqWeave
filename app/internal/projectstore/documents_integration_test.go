//go:build integration

// 結合テスト（実ファイル I/O）。成果物ドキュメントの保存構造と版管理。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testChapters(bodies map[string]string) []DocumentChapter {
	var out []DocumentChapter
	for name, body := range bodies {
		out = append(out, DocumentChapter{
			Chapter: strings.TrimSuffix(name, ".md"), FileName: name,
			GeneratedAt: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC),
			Covers:      []string{"FR-INV-001"}, Body: body,
		})
	}
	return out
}

// 確定のたびに版番号が 1 増え、過去の確定版は上書き・削除されない。
func TestConfirmDraftCreatesVersions(t *testing.T) {
	s := createTestProject(t)
	if err := s.ReplaceDraft(DocKindRequirements, testChapters(map[string]string{
		"01-background.md":              "# 業務背景\n\n現状は Excel 台帳。",
		"06-functional-requirements.md": "# 機能要件\n\nFR-INV-001 在庫引当。",
	})); err != nil {
		t.Fatalf("ドラフトを保存できない: %v", err)
	}

	v1, err := s.ConfirmDraft(DocKindRequirements, []string{"FR-INV-001", "DEC-001"},
		map[string][]string{"01-background.md": {"DEC-001"}})
	if err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	if v1.Version != 1 {
		t.Fatalf("版番号が違う: %d", v1.Version)
	}

	// ドラフトを差し替えても確定版は変わらない。
	if err := s.ReplaceDraft(DocKindRequirements, testChapters(map[string]string{
		"01-background.md": "# 業務背景\n\n書き換えたドラフト。",
	})); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.LoadVersion(DocKindRequirements, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed) != 2 || !strings.Contains(confirmed[0].Body, "現状は Excel 台帳") {
		t.Fatalf("確定版が変化した: %+v", confirmed)
	}
	for _, c := range confirmed {
		if c.Status != DocStatusConfirmed || c.Version != 1 {
			t.Errorf("確定版のメタが違う: %+v", c)
		}
	}

	// 2 回目の確定で版番号が増え、v1 は残る。
	v2, err := s.ConfirmDraft(DocKindRequirements, []string{"FR-INV-001"}, nil)
	if err != nil {
		t.Fatalf("2 回目の確定に失敗: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("版番号が増えていない: %d", v2.Version)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(VersionDir(DocKindRequirements, 1)))); err != nil {
		t.Errorf("v1 が失われた: %v", err)
	}
	versions, err := s.Versions(DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 1 || len(versions[0].SourceIDs) != 2 {
		t.Errorf("版履歴が違う: %+v", versions)
	}
	if versions[0].Chapters["01-background.md"][0] != "DEC-001" {
		t.Errorf("依存マップが記録されていない: %+v", versions[0].Chapters)
	}
	latest, _ := s.LatestVersion(DocKindRequirements)
	if latest != 2 {
		t.Errorf("最新版が違う: %d", latest)
	}
}

// ドラフトの置換は原子的で、途中失敗しても旧内容が残る。
func TestReplaceDraftIsAtomic(t *testing.T) {
	s := createTestProject(t)
	if err := s.ReplaceDraft(DocKindRequirements, testChapters(map[string]string{
		"01-background.md": "# 業務背景\n\n最初の内容。",
	})); err != nil {
		t.Fatal(err)
	}

	// 不正な章（ファイル名なし）を含む置換は失敗し、旧ドラフトが残る。
	bad := testChapters(map[string]string{"01-background.md": "# 差し替え"})
	bad = append(bad, DocumentChapter{Chapter: "broken", GeneratedAt: time.Now(), Body: "x"})
	if err := s.ReplaceDraft(DocKindRequirements, bad); err == nil {
		t.Fatal("不正な章を含む置換が成功した")
	}
	got, err := s.LoadDraft(DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Body, "最初の内容") {
		t.Fatalf("旧ドラフトが失われた: %+v", got)
	}
	// 一時フォルダが残っていないこと。
	dir := filepath.Join(s.Root(), dirDocuments, DocKindRequirements)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".draft-") {
			t.Errorf("一時フォルダが残っている: %s", e.Name())
		}
	}
}

// 確定版を編集・削除する API を持たない（複製のみ）。
func TestNoConfirmedVersionMutationAPI(t *testing.T) {
	s := createTestProject(t)
	if err := s.ReplaceDraft(DocKindRequirements, testChapters(map[string]string{
		"01-background.md": "# 業務背景",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmDraft(DocKindRequirements, nil, nil); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadVersion(DocKindRequirements, 1)
	if err != nil {
		t.Fatal(err)
	}
	// ドラフトの置換・再確定を繰り返しても v1 は変わらない。
	if err := s.ReplaceDraft(DocKindRequirements, testChapters(map[string]string{
		"01-background.md": "# 別の内容",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmDraft(DocKindRequirements, nil, nil); err != nil {
		t.Fatal(err)
	}
	after, err := s.LoadVersion(DocKindRequirements, 1)
	if err != nil {
		t.Fatal(err)
	}
	if before[0].Body != after[0].Body {
		t.Errorf("確定版が書き換わった: %q → %q", before[0].Body, after[0].Body)
	}
}

// 種別・状態・章識別子の値集合を検証する。
func TestDocumentValidation(t *testing.T) {
	s := createTestProject(t)
	if err := s.ReplaceDraft("design", testChapters(map[string]string{"01.md": "x"})); err == nil {
		t.Error("種別が値集合外のドラフトが保存された")
	}
	if err := s.ReplaceDraft(DocKindRequirements, nil); err == nil {
		t.Error("章が空のドラフトが保存された")
	}
	if err := s.ReplaceDraft(DocKindRequirements, []DocumentChapter{{
		Chapter: "background", FileName: "../escape.md", GeneratedAt: time.Now(), Body: "x",
	}}); err == nil {
		t.Error("パス区切りを含む章ファイル名が受理された")
	}
	if _, err := s.ConfirmDraft(DocKindRequirements, nil, nil); err == nil {
		t.Error("ドラフトが無いのに確定できた")
	}
}
