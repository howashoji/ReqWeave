package nfrcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// 利用者向けの画面が日本語であることの機械検査。
//
// 測定可能な基準は「全画面・全ダイアログを走査し、利用者向け文言に日本語以外の文
// （未翻訳の英文メッセージ等）が 0 件であること」。画面を目で見る代わりに、
// **画面のソースに現れる利用者向け文字列**を走査して固定する。
//
// フロントエンドのテストではなく Go 側に置いてある。ファイル走査に Node の API が要り、
// フロントエンドの型定義（ブラウザ向け）には無いため、ビルドを通すには依存を増やすことになる。
// 配布物に不要な依存を足さない方を採った。
//
// 対象は JSX のテキストノードと、画面に出る属性（aria-label / placeholder / title / alt）。
// className・import・データのキー名などは利用者に見えないため対象外。
var (
	jsxTextNode    = regexp.MustCompile(`>([^<>{}\n]+)<`)
	userFacingAttr = regexp.MustCompile(`(?:aria-label|placeholder|title|alt)="([^"]+)"`)
	// 英字だけで構成された「文」（2 語以上）。単語 1 つ（OK・ID など）は許す。
	englishSentence = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:[ ,.;:'-][A-Za-z0-9]+){1,}[.!?]?$`)
	// 書式そのものを示す表記（日付・数値のパターン）。文ではなく記法であり、
	// 日本語へ置き換えると入力形式が伝わらなくなるため対象外。
	formatNotation = regexp.MustCompile(`^[YMDHhms]{1,4}([-/:.][YMDHhms]{1,4})+$`)
)

// allowedEnglish は固有名詞・製品名として画面に出てよいもの。
// OS や AI プロバイダ由来の固有名詞は、日本語でなくてよい。
var allowedEnglish = map[string]bool{
	"ReqWeave":           true,
	"Anthropic (Claude)": true,
	"OpenAI":             true,
	"Google Gemini":      true,
	"Apple Silicon":      true,
	"Program Files":      true,
	"GitHub Releases":    true,
}

func hasJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) || r == 'ー' {
			return true
		}
	}
	return false
}

// screenSources は画面のソース（.tsx。テストを除く）を渡す。
func screenSources(t *testing.T, fn func(rel, content string)) int {
	t.Helper()
	root := filepath.Join(repoAppRoot(t), "frontend", "src")
	count := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".test.tsx") {
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
		count++
		fn(rel, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestScreenTextIsJapanese(t *testing.T) {
	inspected := 0
	var offenders []string
	files := screenSources(t, func(rel, content string) {
		var texts []string
		for _, m := range jsxTextNode.FindAllStringSubmatch(content, -1) {
			texts = append(texts, strings.TrimSpace(m[1]))
		}
		for _, m := range userFacingAttr.FindAllStringSubmatch(content, -1) {
			texts = append(texts, strings.TrimSpace(m[1]))
		}
		for _, text := range texts {
			if text == "" {
				continue
			}
			inspected++
			if hasJapanese(text) || allowedEnglish[text] || formatNotation.MatchString(text) {
				continue
			}
			if englishSentence.MatchString(text) {
				offenders = append(offenders, rel+": "+text)
			}
		}
	})
	if files < 10 {
		t.Fatalf("走査した画面ソースが %d 件（走査が効いていない）", files)
	}
	if inspected < 100 {
		t.Fatalf("走査した文言が %d 件（走査が効いていない）", inspected)
	}
	for _, o := range offenders {
		t.Errorf("利用者向け文言に未翻訳の英文がある: %s", o)
	}
	t.Logf("走査した画面 %d ファイル / 文言 %d 件", files, inspected)
}

// 走査が空振りしていないこと（日本語 UI なので大半の画面ソースに日本語がある）。
func TestScreenSourcesActuallyContainJapanese(t *testing.T) {
	withJapanese := 0
	files := screenSources(t, func(rel, content string) {
		if hasJapanese(content) {
			withJapanese++
		}
	})
	if withJapanese*2 <= files {
		t.Fatalf("日本語を含む画面ソースが %d / %d 件しかない（走査対象が違う可能性）", withJapanese, files)
	}
}
