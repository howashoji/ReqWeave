package importer

// 本ファイルは PDF からのテキスト抽出。
//
// **抽出できる範囲には原理的な限界がある**（実測）:
//   - 標準エンコーディングの英数字 PDF … 抽出できる
//   - 日本語 PDF … 埋め込みフォントのエンコーディング次第。ToUnicode CMap を持たない
//     Identity-H の PDF（macOS の印刷経由など）は、本文がグリフ ID として格納されているため
//     **どのライブラリでも復元できない**
//
// ライブラリはエラーを返さないまま文字化けした文字列を返すことがあるため、
// 呼び出し元（Extract）の品質ゲート looksExtractable で failed へ倒す。
// 文字化けを extracted.md に残さないことが目的（取り込み分析の入力を汚さない）。

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/dslipak/pdf"
)

// extractPDF は PDF の本文テキストを取り出す。
//
// 採用ライブラリ github.com/dslipak/pdf は標準ライブラリのみに依存し、外部通信を行わない
// （取り込みは外部通信をしない。go list -deps で net 系の依存が無いことを確認済み）。
//
// ライブラリがファイルパスしか受け付けないため一時ファイルへ書き出す。原本はプロジェクト内に
// 保存済みだが、抽出は取り込み前にも呼べる必要があるため（保存より先に抽出結果を確定する）
// ここでは内容から直接扱う。
func extractPDF(content []byte) (string, error) {
	f, err := os.CreateTemp("", "reqweave-extract-*.pdf")
	if err != nil {
		return "", ErrNotExtractable
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(content); err != nil {
		f.Close()
		return "", ErrNotExtractable
	}
	if err := f.Close(); err != nil {
		return "", ErrNotExtractable
	}

	text, err := readPDFText(f.Name())
	if err != nil {
		return "", ErrNotExtractable
	}
	return normalizePDFText(text), nil
}

// readPDFText はライブラリの panic も抽出不能として扱う。
//
// 壊れた PDF・想定外の構造で panic することがあり、取り込み操作全体を落とさないため。
func readPDFText(path string) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text, err = "", ErrNotExtractable
		}
	}()

	r, err := pdf.Open(path)
	if err != nil {
		return "", ErrNotExtractable
	}
	rc, err := r.GetPlainText()
	if err != nil {
		return "", ErrNotExtractable
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return "", ErrNotExtractable
	}
	return buf.String(), nil
}

// normalizePDFText は行末の余分な空白を落とす。
//
// PDF は文字位置で組まれているため、抽出結果には桁揃えの空白が大量に混ざる。
// 本文の内容は変えず、行末の空白のみ取り除く（抽出テキストの「無加工」は
// 本文の改変を禁じるものであり、レイアウト由来の余白の除去まで禁じてはいない）。
func normalizePDFText(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}
