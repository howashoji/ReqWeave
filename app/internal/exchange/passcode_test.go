package exchange

import (
	"strings"
	"testing"
)

// 紛らわしい文字（0/O・1/l/I 等）を除外した英大小数字で 10〜16 文字。
func TestNewPasscodeCharsetAndLength(t *testing.T) {
	const confusing = "0Oo1lI"
	if PasscodeLength < passcodeMinLength || PasscodeLength > passcodeMaxLength {
		t.Fatalf("PasscodeLength が許容範囲 %d〜%d の外です: %d", passcodeMinLength, passcodeMaxLength, PasscodeLength)
	}
	for i := range 200 {
		pc, err := NewPasscode()
		if err != nil {
			t.Fatalf("%d 回目の生成に失敗: %v", i, err)
		}
		if got := len([]rune(pc)); got != PasscodeLength {
			t.Fatalf("長さが %d です（期待 %d）", got, PasscodeLength)
		}
		for _, r := range pc {
			if strings.ContainsRune(confusing, r) {
				t.Fatalf("紛らわしい文字 %q が含まれています", string(r))
			}
			if !isASCIIAlnum(r) {
				t.Fatalf("英大小数字以外の文字 %q が含まれています", string(r))
			}
		}
	}
}

// 56bit 以上のエントロピー。
func TestPasscodeEntropyBits(t *testing.T) {
	if got := PasscodeEntropyBits(PasscodeLength); got < passcodeMinEntropyBits {
		t.Fatalf("生成長のエントロピーが %.1f bit です（要求 %d bit 以上）", got, passcodeMinEntropyBits)
	}
	if got := PasscodeEntropyBits(passcodeMinLength); got < passcodeMinEntropyBits {
		t.Fatalf("最小長 %d 文字のエントロピーが %.1f bit です（要求 %d bit 以上）",
			passcodeMinLength, got, passcodeMinEntropyBits)
	}
}

func TestNewPasscodeIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		pc, err := NewPasscode()
		if err != nil {
			t.Fatalf("生成に失敗: %v", err)
		}
		if seen[pc] {
			t.Fatalf("同じパスコードが 2 回生成されました")
		}
		seen[pc] = true
	}
}

func TestValidatePasscodeFormat(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"規則どおり", "AbCdEfGh23456", false},
		{"下限ちょうど", "AbCdEfGh23", false},
		{"下限未満", "AbCdEfGh2", true},
		{"上限超過", "AbCdEfGhJkLmNpQr2", true},
		{"紛らわしい文字を含む", "AbCdEfGh0123", true},
		{"記号を含む", "AbCdEfGh-234", true},
		{"空", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePasscodeFormat(c.input)
			if c.wantErr && err == nil {
				t.Fatalf("エラーを期待しましたが nil でした")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("エラーを期待していません: %v", err)
			}
			// 判定結果に入力値そのものを含めない。
			if err != nil && c.input != "" && strings.Contains(err.Error(), c.input) {
				t.Fatalf("エラー文にパスコードが含まれています: %q", err.Error())
			}
		})
	}
}

func isASCIIAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}
