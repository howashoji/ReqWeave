package exchange

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// passcodeAlphabet はパスコードの文字種。
//
// 英大小数字から、電話・チャットでの読み上げ・転記で取り違えやすい文字
// （0 / O / o・1 / l / I）を除外した 56 文字。
const passcodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ" + "abcdefghijkmnpqrstuvwxyz" + "23456789"

const (
	// PasscodeLength は生成するパスコードの長さ（許容範囲 10〜16 文字）。
	// 56 文字種 × 12 文字 = 約 69.7 bit（要求は 56 bit 以上）。
	PasscodeLength = 12

	// passcodeMinLength / passcodeMaxLength は許容範囲。
	passcodeMinLength = 10
	passcodeMaxLength = 16

	// passcodeMinEntropyBits は要求されるエントロピー下限。
	passcodeMinEntropyBits = 56
)

// NewPasscode は質問票 1 通ぶんのパスコードを生成する。
//
// 生成値は呼び出し側が担当者へ 1 回だけ提示するためだけに使い、
// プロジェクトデータ・アプリ設定・OS セキュアストレージ・監査ログのいずれにも
// 保存しない（漏えいの経路を作らない）。ログ・エラーメッセージへ出力してはならない。
func NewPasscode() (string, error) {
	var sb strings.Builder
	sb.Grow(PasscodeLength)
	max := big.NewInt(int64(len(passcodeAlphabet)))
	for range PasscodeLength {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("パスコードを生成できません: %w", err)
		}
		sb.WriteByte(passcodeAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

// PasscodeEntropyBits は与えた長さのパスコードのエントロピー（bit）を返す。
func PasscodeEntropyBits(length int) float64 {
	if length <= 0 {
		return 0
	}
	return float64(length) * math.Log2(float64(len(passcodeAlphabet)))
}

// ValidatePasscodeFormat は入力されたパスコードが生成規則に沿うかを返す。
//
// 回答モードの入力欄で、復号を試みる前に明らかな入力誤り（長さ・文字種）を
// 弾くために使う。判定結果に入力値そのものを含めない。
func ValidatePasscodeFormat(passcode string) error {
	n := len([]rune(passcode))
	if n < passcodeMinLength || n > passcodeMaxLength {
		return fmt.Errorf("パスコードは %d〜%d 文字です。入力し直してください", passcodeMinLength, passcodeMaxLength)
	}
	for _, r := range passcode {
		if !strings.ContainsRune(passcodeAlphabet, r) {
			return fmt.Errorf("パスコードに使えない文字が含まれています。英字と数字だけで入力し直してください")
		}
	}
	return nil
}
