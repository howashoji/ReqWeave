package codex

// 本ファイルは構造化出力の変換。
//
// Codex は `outputSchema` を `strict: true` で送る（実測）。strict モードはすべての
// プロパティの `required` と `additionalProperties: false` を求めるが、上位のスキーマ
// （対話エンジンが渡すもの）には任意のプロパティがある。そこで**上位のスキーマの意味を変えない**次の変換で吸収する。
//
//  1. 各オブジェクトの任意のプロパティを `required` へ加え、その型に `null` を許す。
//  2. 応答の JSON から、1 で加えたプロパティのうち値が `null` のものを取り除いてから上位へ渡す。
//  3. 意味を変えずに変換できないスキーマは送らず、恒久的（invalid_response_schema）とする。

import (
	"encoding/json"
	"fmt"
	"sort"
)

// schemaShape は変換した箇所の対応（応答から null を取り除くために使う）。
type schemaShape struct {
	// props はオブジェクトのプロパティごとの対応（オブジェクト以外では nil）。
	props map[string]*schemaShape
	// added は 1 の変換で `required` へ加えたプロパティ（応答で null なら取り除く）。
	added map[string]bool
	// items は配列の要素の対応（配列以外では nil）。
	items *schemaShape
}

// convertSchema は上位のスキーマを strict へ適合させ、変換箇所の対応を返す。
func convertSchema(raw json.RawMessage) (map[string]any, *schemaShape, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, fmt.Errorf("構造化出力のスキーマを JSON として解釈できません")
	}
	shape, err := convertNode(schema)
	if err != nil {
		return nil, nil, err
	}
	return schema, shape, nil
}

// convertNode は 1 つのスキーマ節点を変換する（オブジェクト・配列は再帰する）。
func convertNode(node map[string]any) (*schemaShape, error) {
	if props, ok := node["properties"].(map[string]any); ok {
		return convertObject(node, props)
	}
	if items, ok := node["items"].(map[string]any); ok {
		child, err := convertNode(items)
		if err != nil {
			return nil, err
		}
		return &schemaShape{items: child}, nil
	}
	return nil, nil
}

func convertObject(node map[string]any, props map[string]any) (*schemaShape, error) {
	// 追加のプロパティを許すオブジェクトは strict に適合させられない（意味が変わる）。
	if allow, ok := node["additionalProperties"].(bool); !ok || allow {
		return nil, fmt.Errorf("構造化出力のスキーマに additionalProperties: false でないオブジェクトがあります")
	}
	required := map[string]bool{}
	var order []any
	if list, ok := node["required"].([]any); ok {
		order = list
		for _, name := range list {
			if s, ok := name.(string); ok {
				required[s] = true
			}
		}
	}
	shape := &schemaShape{props: map[string]*schemaShape{}, added: map[string]bool{}}
	// 並びを決めて処理する（同じ入力から同じ出力を作るため）。
	for _, name := range sortedNames(props) {
		child, ok := props[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("構造化出力のスキーマのプロパティ %q を解釈できません", name)
		}
		if !required[name] {
			if err := allowNull(child); err != nil {
				return nil, err
			}
			order = append(order, name)
			shape.added[name] = true
		}
		grandchild, err := convertNode(child)
		if err != nil {
			return nil, err
		}
		if grandchild != nil {
			shape.props[name] = grandchild
		}
	}
	node["required"] = order
	return shape, nil
}

// allowNull は任意のプロパティに `null` を許す（型情報が無いものは変換できない）。
func allowNull(schema map[string]any) error {
	switch t := schema["type"].(type) {
	case string:
		if t != "null" {
			schema["type"] = []any{t, "null"}
		}
		return nil
	case []any:
		for _, v := range t {
			if s, ok := v.(string); ok && s == "null" {
				return nil
			}
		}
		schema["type"] = append(t, "null")
		return nil
	}
	if values, ok := schema["enum"].([]any); ok {
		for _, v := range values {
			if v == nil {
				return nil
			}
		}
		schema["enum"] = append(values, nil)
		return nil
	}
	return fmt.Errorf("構造化出力のスキーマに、型の分からない任意のプロパティがあります")
}

func sortedNames(props map[string]any) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	// 並びは JSON Schema の意味に影響しないが、出力を毎回同じにするため整える。
	sort.Strings(names)
	return names
}

// stripAddedNulls は変換で加えたプロパティのうち値が null のものを応答から取り除く。
func stripAddedNulls(body []byte, shape *schemaShape) ([]byte, error) {
	if shape == nil {
		return body, nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		// 応答がスキーマに適合するかの検証は対話エンジンの責務。
		// ここでは JSON として読めないものをそのまま上位へ渡す（判断させる）。
		return body, nil
	}
	cleaned := stripValue(value, shape)
	out, err := json.Marshal(cleaned)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func stripValue(value any, shape *schemaShape) any {
	if shape == nil {
		return value
	}
	switch v := value.(type) {
	case map[string]any:
		for name := range shape.added {
			if current, ok := v[name]; ok && current == nil {
				delete(v, name)
			}
		}
		for name, child := range shape.props {
			if current, ok := v[name]; ok {
				v[name] = stripValue(current, child)
			}
		}
		return v
	case []any:
		if shape.items == nil {
			return v
		}
		for i := range v {
			v[i] = stripValue(v[i], shape.items)
		}
		return v
	default:
		return value
	}
}
