package main

import (
	"os"
	"path/filepath"
	"testing"
)

// テストで使うライセンス表示の原本の中身（配布物の見本にも同じものを入れる）。
const (
	testLicense = "MIT License\n\nCopyright (c) test\n"
	testNotice  = "第三者のソフトウェアのライセンス文（テスト）\n"
)

// testLegalDir はライセンス表示の原本（LICENSE・NOTICE）を置いたディレクトリを返す。
func testLegalDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"LICENSE": testLicense, "NOTICE": testNotice} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 受け入れ条件: 配布物に LICENSE・NOTICE が入っていて、原本と一致すること。
// 置き場所は macOS がアプリ本体の中（Contents/Resources）、Windows が zip の直下。
func TestCheckArchiveRequiresLegalFiles(t *testing.T) {
	cases := []struct {
		name    string
		goos    string
		entries map[string]string
		want    string // 空なら問題なし
	}{
		{"macOS: アプリ本体の中にあれば通る", "darwin", goodMacEntries(), ""},
		{"Windows: zip の直下にあれば通る", "windows", goodWinEntries(), ""},
		{"macOS: NOTICE が無い", "darwin", without(goodMacEntries(), "ReqWeave.app/Contents/Resources/NOTICE"),
			`ライセンス表示 "NOTICE" が配布物にありません`},
		{"Windows: LICENSE が無い", "windows", without(goodWinEntries(), "LICENSE"),
			`ライセンス表示 "LICENSE" が配布物にありません`},
		{"macOS: 最上位に置いてもアプリ本体の中に無ければ落ちる", "darwin",
			with(without(goodMacEntries(), "ReqWeave.app/Contents/Resources/LICENSE"), "LICENSE", testLicense),
			`ライセンス表示 "LICENSE" が配布物にありません`},
		{"Windows: 古い NOTICE（原本と違う）", "windows", with(goodWinEntries(), "NOTICE", "古いライセンス文\n"),
			"配布物の NOTICE がリポジトリ直上の NOTICE と違います"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems, _, err := checkArchive(writeZip(t, buildZip(t, tc.entries)), tc.goos, testLegalDir(t))
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("想定どおりの配布物で問題を報告した: %v", problems)
				}
				return
			}
			if !containsSubstring(problems, tc.want) {
				t.Fatalf("%q を報告しない: %v", tc.want, problems)
			}
		})
	}
}

// 原本が読めないときは、黙って通さずに問題として挙げる。
func TestCheckLegalReportsMissingOriginal(t *testing.T) {
	problems := checkLegal(func(string) ([]byte, error) { return []byte("x"), nil }, "windows", "ReqWeave.exe", t.TempDir())
	if len(problems) != len(legalFiles) || !containsSubstring(problems, "原本を読めません") {
		t.Fatalf("原本の欠落を報告しない: %v", problems)
	}
}

// 実物の原本（リポジトリ直上の LICENSE・NOTICE）が、make の既定の -legal-dir（app/ から見た ..）にあること。
func TestLegalOriginalsExistAtDefaultDir(t *testing.T) {
	for _, name := range legalFiles {
		p := filepath.Join("..", "..", "..", name) // tools/distcheck から見たリポジトリ直上
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			t.Errorf("リポジトリ直上の %s がありません・空です（%s）: %v", name, p, err)
		}
	}
}

func without(m map[string]string, key string) map[string]string {
	delete(m, key)
	return m
}

func with(m map[string]string, key, value string) map[string]string {
	m[key] = value
	return m
}
