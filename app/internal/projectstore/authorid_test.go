package projectstore

import (
	"strings"
	"testing"
)

// 利用者 ID は組織メール / UPN を小文字化・前後空白除去した値とする。
func TestNormalizeAuthorID(t *testing.T) {
	t.Run("小文字化と前後空白除去", func(t *testing.T) {
		got, err := NormalizeAuthorID("  K.Sato@Example.CO.JP\t")
		if err != nil {
			t.Fatalf("正規化に失敗: %v", err)
		}
		if want := "k.sato@example.co.jp"; got != want {
			t.Errorf("正規化結果が違う: got %q, want %q", got, want)
		}
	})

	invalid := map[string]string{
		"空":           "   ",
		"@ がない":       "ksato",
		"local が空":    "@example.co.jp",
		"domain が空":   "ksato@",
		"@ が複数":       "k@sato@example.co.jp",
		"ドメインにドットがない": "ksato@example",
		"途中に空白":       "k sato@example.co.jp",
	}
	for name, in := range invalid {
		t.Run("不正: "+name, func(t *testing.T) {
			if got, err := NormalizeAuthorID(in); err == nil {
				t.Errorf("%q は不正だが受理された: %q", in, got)
			}
		})
	}
}

// 利用者へ表示される文言は「メールアドレス」で示し、内部用語 UPN を出さない
// （利用者向けの文言の様式）。識別子の定義（組織メール / UPN）は変えない。
func TestNormalizeAuthorIDMessagesUseEmailWording(t *testing.T) {
	inputs := []string{"   ", "ksato", "@example.co.jp", "ksato@", "k@sato@example.co.jp", "ksato@example", "k sato@example.co.jp"}
	for _, in := range inputs {
		_, err := NormalizeAuthorID(in)
		if err == nil {
			t.Fatalf("%q は不正だが受理された", in)
		}
		msg := err.Error()
		if strings.Contains(msg, "UPN") {
			t.Errorf("画面へ出る文言に内部用語 UPN が含まれる: %q", msg)
		}
		if !strings.Contains(msg, "メールアドレス") {
			t.Errorf("何の欄かが伝わらない（「メールアドレス」を含まない）: %q", msg)
		}
	}
}
