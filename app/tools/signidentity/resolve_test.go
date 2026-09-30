package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// 試験の入力はすべて架空の値（実在の証明書の SHA-1 や名義を使わない）。
var (
	shaA = strings.Repeat("A", 40)
	shaB = strings.Repeat("B", 40)
	shaC = strings.Repeat("C", 40)
	shaD = strings.Repeat("D", 40)
)

const (
	appName       = "Developer ID Application: Example Corp (EXAMPLE123)"
	otherAppName  = "Developer ID Application: Example Labs (EXAMPLE456)"
	devName       = "Apple Development: dev@example.com (EXAMPLE789)"
	installerName = "Developer ID Installer: Example Corp (EXAMPLE123)"
)

// listing は find-identity -v -p codesigning と同じ形の出力を作る。
func listing(entries ...[2]string) string {
	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "  %d) %s %q\n", i+1, e[0], e[1])
	}
	fmt.Fprintf(&b, "     %d valid identities found\n", len(entries))
	return b.String()
}

func mustResolve(t *testing.T, in, explicit string) identity {
	t.Helper()
	id, err := resolve(in, explicit)
	if err != nil {
		t.Fatalf("決まるはずが止まった: %v\n入力:\n%s", err, in)
	}
	return id
}

func mustStop(t *testing.T, in, explicit string) string {
	t.Helper()
	id, err := resolve(in, explicit)
	if err == nil {
		t.Fatalf("止まるはずが %s に決まった\n入力:\n%s", id.SHA1, in)
	}
	return err.Error()
}

func TestResolvePicksTheOnlyDeveloperID(t *testing.T) {
	id := mustResolve(t, listing([2]string{shaA, appName}), "")
	if id.SHA1 != shaA || id.Name != appName {
		t.Fatalf("got %+v", id)
	}
}

// 同じ SHA-1 が 2 行に出ても、証明書は 1 枚なので決まる。
func TestResolveCountsDuplicateSHA1Once(t *testing.T) {
	id := mustResolve(t, listing([2]string{shaA, appName}, [2]string{shaA, appName}), "")
	if id.SHA1 != shaA {
		t.Fatalf("got %+v", id)
	}
}

// 同じ名前の別々の証明書が 2 枚あると、黙って 1 枚目を選ばずに止まる。
// 対照: 同じ入力でも SHA-1 を指定すれば決まる（止まる理由が「入力が読めない」ではないことを示す）。
func TestResolveStopsOnTwoDistinctCertificates(t *testing.T) {
	// 実際の不具合と同じ形: 別々の SHA-1 が 2 つ、そのうち 1 つは 2 行に出る（3 行で 2 枚）。
	in := listing([2]string{shaA, appName}, [2]string{shaB, appName}, [2]string{shaB, appName})
	msg := mustStop(t, in, "")
	for _, want := range []string{shaA, shaB, appName, "MAC_SIGN_IDENTITY=<SHA-1>", "2 枚"} {
		if !strings.Contains(msg, want) {
			t.Errorf("止まったときの案内に %q が無い:\n%s", want, msg)
		}
	}
	// 名前の違う Developer ID が 2 枚でも同じ（別の製品の証明書で署名しない）。
	mustStop(t, listing([2]string{shaA, appName}, [2]string{shaC, otherAppName}), "")

	// 対照
	if id := mustResolve(t, in, shaB); id.SHA1 != shaB {
		t.Fatalf("SHA-1 を指定したのに %s に決まった", id.SHA1)
	}
}

// 0 枚なら止まる。対照: 1 枚足せば決まる。
func TestResolveStopsWhenNoDeveloperID(t *testing.T) {
	msg := mustStop(t, "     0 valid identities found\n", "")
	if !strings.Contains(msg, "キーチェーンにありません") {
		t.Errorf("0 枚のときの案内が違う:\n%s", msg)
	}
	mustStop(t, "", "")
	mustResolve(t, listing([2]string{shaA, appName}), "")
}

// Developer ID Application 以外の証明書（開発用・インストーラ用）は数えない。
func TestResolveIgnoresOtherCertificateKinds(t *testing.T) {
	id := mustResolve(t, listing([2]string{shaC, devName}, [2]string{shaA, appName}, [2]string{shaD, installerName}), "")
	if id.SHA1 != shaA {
		t.Fatalf("Developer ID Application ではない証明書に決まった: %+v", id)
	}
	// 他の種類だけなら、候補は 0 枚なので止まる。
	mustStop(t, listing([2]string{shaC, devName}, [2]string{shaD, installerName}), "")
}

// MAC_SIGN_IDENTITY に SHA-1 を渡したときは、その証明書に決まる（大文字小文字を問わない）。
func TestResolveExplicitSHA1(t *testing.T) {
	in := listing([2]string{shaA, appName}, [2]string{shaB, appName})
	if id := mustResolve(t, in, strings.ToLower(shaA)); id.SHA1 != shaA {
		t.Fatalf("got %+v", id)
	}
	// 種類を問わず、指定された証明書を使う（利用者が明示したものを選び直さない）。
	if id := mustResolve(t, listing([2]string{shaC, devName}), shaC); id.SHA1 != shaC {
		t.Fatalf("got %+v", id)
	}
	// キーチェーンに無い SHA-1 は codesign まで進ませずに止める。
	msg := mustStop(t, in, shaD)
	if !strings.Contains(msg, shaD) || !strings.Contains(msg, shaA) {
		t.Errorf("無い SHA-1 を指定したときの案内に、指定値と候補が無い:\n%s", msg)
	}
}

// MAC_SIGN_IDENTITY に名前を渡したときは、その名前の証明書が 1 枚だけなら決まり、2 枚なら止まる。
func TestResolveExplicitName(t *testing.T) {
	if id := mustResolve(t, listing([2]string{shaA, appName}, [2]string{shaC, otherAppName}), otherAppName); id.SHA1 != shaC {
		t.Fatalf("got %+v", id)
	}
	msg := mustStop(t, listing([2]string{shaA, appName}, [2]string{shaB, appName}), appName)
	if !strings.Contains(msg, shaA) || !strings.Contains(msg, shaB) {
		t.Errorf("同じ名前が 2 枚のときの案内に候補の SHA-1 が無い:\n%s", msg)
	}
	mustStop(t, listing([2]string{shaA, appName}), otherAppName)
}

// 実行の形: 決まれば標準出力は SHA-1 の 1 行だけ（Makefile がそのまま codesign へ渡す）。
// 決まらなければ標準出力は空（Makefile は空を見て止まる）。
func TestRunOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, strings.NewReader(listing([2]string{shaA, appName}, [2]string{shaA, appName})), &out, &errOut); code != 0 {
		t.Fatalf("終了コード %d: %s", code, errOut.String())
	}
	if out.String() != shaA+"\n" {
		t.Fatalf("標準出力が SHA-1 の 1 行ではない: %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	in := listing([2]string{shaA, appName}, [2]string{shaB, appName})
	if code := run(nil, strings.NewReader(in), &out, &errOut); code != 1 {
		t.Fatalf("2 枚のときの終了コード %d（期待 1）", code)
	}
	if out.Len() != 0 {
		t.Fatalf("止まったのに標準出力がある: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "MAC_SIGN_IDENTITY=<SHA-1>") {
		t.Fatalf("標準エラーに指定の仕方が無い: %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"-identity", shaB}, strings.NewReader(in), &out, &errOut); code != 0 || out.String() != shaB+"\n" {
		t.Fatalf("-identity で SHA-1 を渡したのに決まらない: code=%d out=%q err=%s", code, out.String(), errOut.String())
	}
}

// Makefile で codesign に渡す署名 ID が、すべてこの道具で決めた値を通ること。
// 名前をそのまま渡す行や、一覧の 1 件目を黙って取る行が戻ると、同じ名前の証明書が 2 枚ある Mac で止まる。
func TestMakefileSignsWithResolvedIdentity(t *testing.T) {
	b, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	signLine := regexp.MustCompile(`codesign\b.*--sign\b`)
	var signs int
	for i, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		// ad-hoc 署名（--sign -）は証明書を使わないので対象外。
		if signLine.MatchString(line) && !strings.Contains(line, "--sign -") {
			signs++
			if !strings.Contains(line, `--sign "$(MAC_SIGN_SHA1)"`) {
				t.Errorf("Makefile %d 行目: codesign に決めた SHA-1 以外を渡している: %s", i+1, strings.TrimSpace(line))
			}
		}
		if strings.Contains(line, "find-identity") && strings.Contains(line, "head -1") {
			t.Errorf("Makefile %d 行目: 署名 ID の一覧の 1 件目を黙って取っている: %s", i+1, strings.TrimSpace(line))
		}
	}
	// 前提の確認: 署名の行が 1 つも見つからなければ、この検査は何も確かめていない。
	if signs == 0 {
		t.Fatal("前提が崩れています: Makefile に codesign --sign の行が見つかりません")
	}
}
