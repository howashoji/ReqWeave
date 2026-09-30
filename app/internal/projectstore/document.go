package projectstore

// 本ファイルは「YAML フロントマター + Markdown 本文」形式のレコード（要件項目・決定事項・未決事項）の
// 共通の組み立て・解釈を担う。人が読める形（本文は Markdown のまま）を保つ。

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// marshalDocument はフロントマター（YAML）と本文（Markdown）を 1 つのファイル内容にする。
func marshalDocument(front any, body string) ([]byte, error) {
	header, err := yaml.Marshal(front)
	if err != nil {
		return nil, fmt.Errorf("フロントマターを組み立てられません: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(header)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// parseDocument はフロントマターを front へ読み込み、本文を返す。
func parseDocument(data []byte, front any) (string, error) {
	header, body, err := splitFrontMatter(data)
	if err != nil {
		return "", err
	}
	if err := yaml.Unmarshal(header, front); err != nil {
		return "", fmt.Errorf("フロントマターを解釈できません: %w", err)
	}
	return strings.Trim(string(body), "\n"), nil
}
