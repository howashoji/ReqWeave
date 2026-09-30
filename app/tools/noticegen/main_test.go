package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLicenseName(t *testing.T) {
	for _, name := range []string{"LICENSE", "LICENCE", "license.md", "LICENSE.txt", "LICENSE-MIT", "LICENSE.MIT", "COPYING", "NOTICE", "notice.md"} {
		if !isLicenseFile(name) {
			t.Errorf("%s をライセンス文とみなしていない", name)
		}
	}
	for _, name := range []string{"README.md", "package.json", "licenses.go", "LICENSE.go", "license-update.mjs", "license.d.ts", "notice.json", "index.js"} {
		if isLicenseFile(name) {
			t.Errorf("%s をライセンス文とみなしている", name)
		}
	}
}

func TestRepoPath(t *testing.T) {
	cases := map[string]string{
		"github.com/wailsapp/wails/v2":  "github.com/wailsapp/wails",
		"github.com/zalando/go-keyring": "github.com/zalando/go-keyring",
		"golang.org/x/net":              "golang.org/x/net",
		"example.com/foo/vendor":        "example.com/foo/vendor",
	}
	for in, want := range cases {
		if got := repoPath(in); got != want {
			t.Errorf("repoPath(%q) = %q、期待 %q", in, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	got := normalize("a  \r\nb\t\rc\n")
	if got != "a\nb\nc\n" {
		t.Errorf("normalize の結果が %q", got)
	}
}

func TestAuthorName(t *testing.T) {
	cases := map[string]string{
		`"Wilson Page <wilsonpage@me.com>"`:           "Wilson Page",
		`{"name":"Jane Doe","email":"j@example.com"}`: "Jane Doe",
		`"Solo"`: "Solo",
		`null`:   "",
	}
	for in, want := range cases {
		if got := authorName(json.RawMessage(in)); got != want {
			t.Errorf("authorName(%s) = %q、期待 %q", in, got, want)
		}
	}
}

func TestNpmSource(t *testing.T) {
	cases := []struct {
		repo, homepage, want string
	}{
		{`"git+https://github.com/a/b.git"`, "", "https://github.com/a/b"},
		{`{"type":"git","url":"git://github.com/a/b.git"}`, "", "https://github.com/a/b"},
		{`"github:a/b"`, "", "https://github.com/a/b"},
		{`"a/b"`, "", "https://github.com/a/b"},
		{`null`, "https://example.com/", "https://example.com/"},
		{`null`, "", "https://www.npmjs.com/package/pkg"},
	}
	for _, c := range cases {
		pj := packageJSON{Name: "pkg", Repository: json.RawMessage(c.repo), Homepage: c.homepage}
		if got := npmSource(pj); got != c.want {
			t.Errorf("npmSource(%s, %q) = %q、期待 %q", c.repo, c.homepage, got, c.want)
		}
	}
}

// writeFrontend は npmSections が読む形（package-lock.json と node_modules）を一時ディレクトリに作る。
func writeFrontend(t *testing.T, lock string, pkgs map[string]map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	for pkg, files := range pkgs {
		pdir := filepath.Join(dir, "node_modules", pkg)
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func TestNpmSectionsSkipsDevAndSorts(t *testing.T) {
	lock := `{"packages":{
		"": {"version":"0.0.0"},
		"node_modules/zeta": {"version":"1.0.0","license":"MIT"},
		"node_modules/alpha": {"version":"2.0.0","license":"ISC"},
		"node_modules/devonly": {"version":"3.0.0","license":"MIT","dev":true}
	}}`
	dir := writeFrontend(t, lock, map[string]map[string]string{
		"zeta":  {"package.json": `{"name":"zeta","version":"1.0.0"}`, "LICENSE": "zeta license"},
		"alpha": {"package.json": `{"name":"alpha","version":"2.0.0"}`, "LICENSE.md": "alpha license"},
		// devonly は node_modules に置かない（dev を読みに行けば失敗する）
	})
	secs, err := npmSections(dir)
	if err != nil {
		t.Fatalf("npmSections: %v", err)
	}
	if len(secs) != 2 {
		t.Fatalf("節の数が %d、期待 2（dev の依存を除く）", len(secs))
	}
	if secs[0].title != "alpha 2.0.0" || secs[1].title != "zeta 1.0.0" {
		t.Errorf("並びが名前順でない: %q, %q", secs[0].title, secs[1].title)
	}
	if secs[0].licenses[0].name != "LICENSE.md" || secs[0].licenses[0].text != "alpha license" {
		t.Errorf("alpha のライセンス文が読めていない: %+v", secs[0].licenses)
	}
}

func TestNpmSectionsFailsWithoutLicense(t *testing.T) {
	lock := `{"packages":{"node_modules/nolicense":{"version":"1.0.0","license":"MIT"}}}`
	dir := writeFrontend(t, lock, map[string]map[string]string{
		"nolicense": {"package.json": `{"name":"nolicense","version":"1.0.0"}`},
	})
	_, err := npmSections(dir)
	if err == nil || !strings.Contains(err.Error(), "nolicense") {
		t.Fatalf("ライセンス文の無いパッケージで失敗していない: %v", err)
	}
}

func TestNpmSectionsKnownWithoutFile(t *testing.T) {
	lock := `{"packages":{"node_modules/fastdom":{"version":"1.0.12","license":"MIT"}}}`
	dir := writeFrontend(t, lock, map[string]map[string]string{
		"fastdom": {"package.json": `{"name":"fastdom","version":"1.0.12","author":"Wilson Page <wilsonpage@me.com>"}`},
	})
	secs, err := npmSections(dir)
	if err != nil {
		t.Fatalf("npmSections: %v", err)
	}
	l := secs[0].licenses
	if len(l) != 1 || !strings.HasPrefix(l[0].text, "Copyright (c) Wilson Page\n") || !strings.Contains(l[0].text, "Permission is hereby granted") {
		t.Errorf("定型文の MIT が著作者つきで入っていない: %+v", l)
	}
	if secs[0].note == "" {
		t.Error("定型文で記した旨の補足が無い")
	}
}

func TestNpmSectionsKnownWithoutFileLicenseMismatch(t *testing.T) {
	// 例外に挙げた名前でも、宣言が MIT から変わっていたら通さない
	lock := `{"packages":{"node_modules/fastdom":{"version":"2.0.0","license":"GPL-3.0"}}}`
	dir := writeFrontend(t, lock, map[string]map[string]string{
		"fastdom": {"package.json": `{"name":"fastdom","version":"2.0.0","author":"Wilson Page"}`},
	})
	if _, err := npmSections(dir); err == nil {
		t.Fatal("宣言が変わった例外のパッケージで失敗していない")
	}
}

func TestRenderIsStable(t *testing.T) {
	std := section{title: "Go", source: "https://go.dev/", licenses: []licenseFile{{"LICENSE", "go  \r\n"}}}
	goSecs := []section{{title: "m v1", source: "https://m", licenses: []licenseFile{{"LICENSE", "m"}}}}
	npmSecs := []section{{title: "p 1", note: "補足", licenses: []licenseFile{{"LICENSE", "p"}}}}
	a := render(std, goSecs, npmSecs)
	b := render(std, goSecs, npmSecs)
	if string(a) != string(b) {
		t.Fatal("同じ入力で出力が変わる")
	}
	s := string(a)
	for _, want := range []string{"収録: Go のモジュール 1・フロントエンドのパッケージ 1", "\ngo\n", "入手先: https://m", "補足\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("出力に %q が無い", want)
		}
	}
	if strings.Contains(s, "\r") || strings.Contains(s, "go  ") {
		t.Error("改行・行末の空白がそろっていない")
	}
}
