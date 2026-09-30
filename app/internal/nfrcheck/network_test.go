package nfrcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoAppRoot は app/ の絶対パスを返す。
func repoAppRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("app/ を特定できない（%s）: %v", root, err)
	}
	return root
}

// walkGoSources は app/ 配下の Go ソース（テストを除く）を渡す。
func walkGoSources(t *testing.T, fn func(rel string, content string)) {
	t.Helper()
	root := repoAppRoot(t)
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", "dist", "build", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		fn(rel, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// urlPattern はソース中の URL リテラルを拾う。
var urlPattern = regexp.MustCompile(`"(https?|ws|ftp)://[^"]*"`)

// 通信の暗号化:
// 「通信キャプチャで平文 HTTP の接続が 0 件であること」を、**接続先を持つコードの側**で固定する。
// 実装が持つ URL リテラルはすべて https でなければならない。
func TestAllOutboundURLsAreHTTPS(t *testing.T) {
	// 例外: 通信しない用途で http のリテラルを持つもの（XML 名前空間・スキーマ URI）。
	// 名前空間は識別子であって接続先ではない（Office Open XML の解析で使う）。
	allowedNonHTTPS := map[string]bool{
		`"http://schemas.openxmlformats.org/spreadsheetml/2006/main"`:           true,
		`"http://schemas.openxmlformats.org/wordprocessingml/2006/main"`:        true,
		`"http://schemas.openxmlformats.org/officeDocument/2006/relationships"`: true,
		`"http://schemas.openxmlformats.org/package/2006/relationships"`:        true,
		`"http://schemas.microsoft.com/office/word/2010/wordml"`:                true,
		`"http://purl.oclc.org/ooxml/spreadsheetml/main"`:                       true,
		`"http://purl.oclc.org/ooxml/wordprocessingml/main"`:                    true,
	}
	found := 0
	walkGoSources(t, func(rel, content string) {
		for _, lit := range urlPattern.FindAllString(content, -1) {
			found++
			if strings.HasPrefix(lit, `"https://`) {
				continue
			}
			if allowedNonHTTPS[lit] {
				continue
			}
			t.Errorf("%s: https でない URL リテラルがある: %s", rel, lit)
		}
	})
	if found == 0 {
		t.Fatal("URL リテラルが 1 件も見つからない（走査が効いていない）")
	}
	t.Logf("走査した URL リテラル: %d 件", found)
}

// 通信の暗号化: TLS 検証を無効化する経路が存在しないこと。
// depcheck も同じ規則を持つが、こちらは「テストコードを含めて 0 件」まで見る
// （テストのためだけに無効化する抜け道を作らせない）。
func TestNoTLSVerificationBypassAnywhere(t *testing.T) {
	root := repoAppRoot(t)
	// 語そのものを書くとこのファイル自身が引っかかるため、分割して組み立てる。
	banned := []string{"Insecure" + "SkipVerify", "TLS" + "ClientConfig"}
	hits := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", "dist", "build", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		// 禁止語を持つのが仕事のファイルは対象外（depcheck の規則定義と本検査自身）。
		if strings.HasPrefix(rel, filepath.Join("tools", "depcheck")) ||
			strings.HasPrefix(rel, filepath.Join("internal", "nfrcheck")) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, word := range banned {
			if strings.Contains(string(b), word) {
				hits++
				t.Errorf("%s: TLS 検証を触る記述がある: %s", rel, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits > 0 {
		t.Log("TLS の検証は既定のまま用いる（証明書の検証を外すと、通信の暗号化が成り立たない）")
	}
}

// 通信先の限定: 外向き通信を行う層が 2 つ（AI プロバイダ抽象化層・アップデータ）に
// 限られていること。depcheck の import 規則と同じ内容を、許可リストの側から固定する。
func TestOutboundNetworkLayersAreLimited(t *testing.T) {
	allowed := []string{
		filepath.Join("internal", "aiprovider"),
		filepath.Join("internal", "updater"),
	}
	var offenders []string
	walkGoSources(t, func(rel, content string) {
		if !strings.Contains(content, `"net/http"`) {
			return
		}
		for _, dir := range allowed {
			if strings.HasPrefix(rel, dir+string(filepath.Separator)) {
				return
			}
		}
		// リリース用の道具は配布物を作るだけで、アプリの通信経路ではない。
		if strings.HasPrefix(rel, "tools"+string(filepath.Separator)) {
			return
		}
		offenders = append(offenders, rel)
	})
	if len(offenders) > 0 {
		t.Errorf("外向き通信の層が 2 つ（AI プロバイダ抽象化層・アップデータ）を超えている: %v", offenders)
	}
}
