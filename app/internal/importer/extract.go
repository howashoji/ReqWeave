package importer

// 本ファイルは取り込み資料からの本文テキスト抽出を担う。
//
// 抽出はローカル処理のみで完結し、外部通信を行わない（depcheck が機械検知する）。
// 抽出結果は無加工で extracted.md へ保存し以後変更しない（IMP-nnn#Lm-Ln の行参照を安定させるため。
// やり直しは新しい IMP としての再取り込み）。

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrNotExtractable は本文テキストを取り出せなかったことを表す（extraction_status: failed）。
//
// メッセージは取り込みのエラーの様式に従う（原因 → 次に取る行動 → 原本が保持される旨）。
// 内部用語・生のコード値を含めない。原本は取り込み済みのまま保持され、
// extracted.md は作られない。
var ErrNotExtractable = errors.New(
	"この資料からテキストを取り出せませんでした。" +
		"対応形式（テキスト / Markdown / Word / Excel / PowerPoint / PDF）か確認してください。" +
		"原本は取り込み済みのまま保持されます。")

// Extract は原本のバイト列から本文テキストを取り出す。
//
// 取り出せない場合は ErrNotExtractable を返す（呼び出し側は extraction_status: failed とする）。
// 本関数は状態を持たず、ファイル I/O も外部通信も行わない。
func Extract(format Format, content []byte) (string, error) {
	var (
		text string
		err  error
	)
	switch format {
	case FormatTxt, FormatMD, FormatClipboard:
		text, err = extractPlainText(content)
	case FormatDocx:
		text, err = extractDocx(content)
	case FormatXlsx:
		text, err = extractXlsx(content)
	case FormatPptx:
		text, err = extractPptx(content)
	case FormatPDF:
		text, err = extractPDF(content)
	default:
		return "", fmt.Errorf("対応していない形式です: %q", format)
	}
	if err != nil {
		return "", err
	}
	if !looksExtractable(text) {
		return "", ErrNotExtractable
	}
	return text, nil
}

// extractPlainText はテキスト・Markdown・クリップボード貼り付けを扱う。
//
// 内容をそのまま本文とする（無加工）。ただし UTF-8 として読めないものは
// 抽出不能として扱う（文字化けを extracted.md に残さない）。
func extractPlainText(content []byte) (string, error) {
	if !utf8.Valid(content) {
		return "", ErrNotExtractable
	}
	return string(content), nil
}

// looksExtractable は抽出結果が本文として使える状態かを判定する（品質ゲート）。
//
// **なぜ必要か**: PDF はフォントのエンコーディング次第で、ライブラリがエラーを返さないまま
// 文字化けした文字列を返すことがある（実測: Identity-H で ToUnicode を持たない PDF では
// 埋め込みフォントのグリフ ID がそのまま出るため、原理的に本文へ復元できない）。
// 文字化けを extracted.md に保存すると、以後の取り込み分析に汚れた入力を渡し、
// 根拠参照（IMP-nnn#Lm-Ln）も無意味な行を指す。**抽出できないことは failed として扱うほうが良い**
// （原本は保持され、利用者には取り込みのエラーの文言で通知される）。
func looksExtractable(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	var total, replacement, control int
	for _, r := range text {
		total++
		switch {
		case r == utf8.RuneError:
			replacement++
		case r == '\t' || r == '\n' || r == '\r':
			// 本文に現れてよい制御文字。
		case unicode.IsControl(r):
			control++
		}
	}
	if total == 0 {
		return false
	}
	// 文字化けした PDF は置換文字・制御文字が大量に混ざる一方、
	// 正常な日本語文書ではいずれもほぼ 0 になる（実測にもとづく）。
	if replacement*100/total > replacementRatioLimit {
		return false
	}
	if control*100/total > controlRatioLimit {
		return false
	}
	return true
}

// 品質ゲートの閾値（％）。
const (
	replacementRatioLimit = 10 // U+FFFD の混入率
	controlRatioLimit     = 5  // タブ・改行を除く制御文字の混入率
)
