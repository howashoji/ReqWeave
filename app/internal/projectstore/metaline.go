package projectstore

// 本ファイルは Markdown レコードのメタ行（"- key: value"）の値の直列化を担う。
//
// 値は YAML スカラー／フローシーケンスとして書き、同じ規則で読み戻す
// （区切り文字・改行を含む業務データで往復が壊れないようにする。質問票・回答の様式を保つ）。

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// marshalMetaScalar は 1 行に収まる YAML スカラー表記を返す。
// 改行・前後空白・YAML の特別扱いが必要な値は二重引用符で囲む。
func marshalMetaScalar(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, "\n\r") {
		return quoteYAML(v)
	}
	// 素の値として書いて読み戻せるかを確かめ、崩れる場合だけ引用する。
	var probe string
	if err := yaml.Unmarshal([]byte(v), &probe); err != nil || probe != v {
		return quoteYAML(v)
	}
	return v
}

func quoteYAML(v string) string {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}
	out, err := yaml.Marshal(node)
	if err != nil {
		// 到達しない（スカラーの直列化は失敗しない）。安全側として改行を落とす。
		return `"` + strings.NewReplacer("\n", `\n`, "\r", "", `"`, `\"`).Replace(v) + `"`
	}
	return strings.TrimRight(string(out), "\n")
}

// parseMetaScalar はメタ行の値を読む（marshalMetaScalar の逆）。
func parseMetaScalar(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	var out string
	if err := yaml.Unmarshal([]byte(v), &out); err == nil {
		return out
	}
	return v
}

// marshalMetaList は "[a, b]" のフローシーケンス表記を返す。
func marshalMetaList(v []string) string {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, item := range v {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item})
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		return "[]"
	}
	return strings.TrimRight(string(out), "\n")
}

// parseMetaList はメタ行の列挙を読む（marshalMetaList の逆）。
func parseMetaList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []string
	if err := yaml.Unmarshal([]byte(v), &out); err == nil {
		return out
	}
	// フロー表記として読めない場合はカンマ区切りとして扱う（手書きの取り込み）。
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var fallback []string
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			fallback = append(fallback, s)
		}
	}
	return fallback
}
