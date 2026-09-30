package codex

import (
	"encoding/json"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
)

// collectOptional は「変換で required へ加えたプロパティ」を path つきで集める。
func collectOptional(shape *schemaShape, prefix string, out map[string]bool) {
	if shape == nil {
		return
	}
	for name := range shape.added {
		out[prefix+name] = true
	}
	for name, child := range shape.props {
		collectOptional(child, prefix+name+".", out)
	}
	collectOptional(shape.items, prefix+"[].", out)
}

// 任意のプロパティを required へ加え、null を許す。
func TestConvertSchemaMakesOptionalPropertiesNullable(t *testing.T) {
	raw := json.RawMessage(`{
	  "type": "object", "additionalProperties": false,
	  "required": ["decisions"],
	  "properties": {
	    "decisions": {
	      "type": "array",
	      "items": {
	        "type": "object", "additionalProperties": false,
	        "required": ["body"],
	        "properties": {
	          "body": {"type": "string"},
	          "duplicate_of": {"type": ["string", "null"]},
	          "needs_review": {"type": "boolean"},
	          "tags": {"type": "array", "items": {"type": "string"}},
	          "kind": {"enum": ["a", "b"]}
	        }
	      }
	    },
	    "note": {"type": "string"}
	  }
	}`)
	schema, shape, err := convertSchema(raw)
	if err != nil {
		t.Fatalf("変換できない: %v", err)
	}

	body, _ := json.Marshal(schema)
	var converted map[string]any
	if err := json.Unmarshal(body, &converted); err != nil {
		t.Fatalf("変換結果を解釈できない: %v", err)
	}
	// 上位のオブジェクト: note が required へ入り、null を許す
	required := toStringSet(converted["required"])
	if !required["decisions"] || !required["note"] {
		t.Errorf("required に全プロパティが入っていない: %v", converted["required"])
	}
	note := converted["properties"].(map[string]any)["note"].(map[string]any)
	if !hasNullType(note["type"]) {
		t.Errorf("任意のプロパティに null を許していない: %v", note["type"])
	}
	// 配列の要素のオブジェクトも同じ扱い
	item := converted["properties"].(map[string]any)["decisions"].(map[string]any)["items"].(map[string]any)
	itemRequired := toStringSet(item["required"])
	for _, name := range []string{"body", "duplicate_of", "needs_review", "tags", "kind"} {
		if !itemRequired[name] {
			t.Errorf("配列の要素の required に %q が無い: %v", name, item["required"])
		}
	}
	itemProps := item["properties"].(map[string]any)
	if !hasNullType(itemProps["needs_review"].(map[string]any)["type"]) {
		t.Error("真偽値の任意プロパティに null を許していない")
	}
	if !hasNullType(itemProps["tags"].(map[string]any)["type"]) {
		t.Error("配列の任意プロパティに null を許していない")
	}
	if values, ok := itemProps["kind"].(map[string]any)["enum"].([]any); !ok || !containsNil(values) {
		t.Errorf("列挙の任意プロパティに null を許していない: %v", itemProps["kind"])
	}
	// 元から required のものは「加えた」に数えない（応答から消してはいけない）
	added := map[string]bool{}
	collectOptional(shape, "", added)
	if added["decisions"] || added["[].body"] {
		t.Errorf("必須のプロパティを加えたものとして扱っている: %v", added)
	}
}

func toStringSet(v any) map[string]bool {
	out := map[string]bool{}
	list, ok := v.([]any)
	if !ok {
		return out
	}
	for _, item := range list {
		if s, ok := item.(string); ok {
			out[s] = true
		}
	}
	return out
}

func hasNullType(v any) bool {
	list, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if item == "null" {
			return true
		}
	}
	return false
}

func containsNil(values []any) bool {
	for _, v := range values {
		if v == nil {
			return true
		}
	}
	return false
}

// 応答から「変換で加えたプロパティの null」だけを取り除く。
func TestStripAddedNullsRemovesOnlyConvertedProperties(t *testing.T) {
	raw := json.RawMessage(`{
	  "type": "object", "additionalProperties": false,
	  "required": ["items", "kept"],
	  "properties": {
	    "kept": {"type": ["string", "null"]},
	    "note": {"type": "string"},
	    "items": {
	      "type": "array",
	      "items": {
	        "type": "object", "additionalProperties": false,
	        "required": ["body"],
	        "properties": {"body": {"type": "string"}, "owner": {"type": ["string", "null"]}}
	      }
	    }
	  }
	}`)
	_, shape, err := convertSchema(raw)
	if err != nil {
		t.Fatalf("変換できない: %v", err)
	}
	response := `{"kept":null,"note":null,"items":[{"body":"本文","owner":null},{"body":"本文2","owner":"佐藤"}]}`
	cleaned, err := stripAddedNulls([]byte(response), shape)
	if err != nil {
		t.Fatalf("取り除けない: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(cleaned, &got); err != nil {
		t.Fatalf("結果を解釈できない: %v", err)
	}
	if _, ok := got["note"]; ok {
		t.Error("変換で加えたプロパティの null が残っている")
	}
	if v, ok := got["kept"]; !ok || v != nil {
		t.Error("元から必須（null 可）のプロパティを消している")
	}
	items := got["items"].([]any)
	first := items[0].(map[string]any)
	if _, ok := first["owner"]; ok {
		t.Error("配列の要素で null が残っている")
	}
	second := items[1].(map[string]any)
	if second["owner"] != "佐藤" {
		t.Errorf("null でない値まで消している: %+v", second)
	}
}

// 意味を変えずに変換できないスキーマは送らない。
func TestConvertSchemaRejectsUnconvertible(t *testing.T) {
	cases := map[string]string{
		"追加プロパティを許すオブジェクト": `{"type":"object","properties":{"a":{"type":"string"}}}`,
		"入れ子で許しているもの": `{"type":"object","additionalProperties":false,"required":["a"],
		  "properties":{"a":{"type":"object","properties":{"b":{"type":"string"}}}}}`,
		"型の分からない任意プロパティ": `{"type":"object","additionalProperties":false,"required":[],
		  "properties":{"a":{"description":"型が無い"}}}`,
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := convertSchema(json.RawMessage(schema)); err == nil {
				t.Error("変換できないスキーマが受理された")
			}
		})
	}
}

// 上位（対話エンジン）が実際に渡すスキーマが変換できること。
// 上位のスキーマが変わったときに、ここで気づけるようにする。
func TestConvertSchemaAcceptsDialogueSchemas(t *testing.T) {
	totalAdded := 0
	for _, mode := range []string{
		dialogue.ModeExtraction, dialogue.ModeMaterialAnalysis,
		dialogue.ModeFeedbackAnalysis, dialogue.ModeQuestionnaire,
	} {
		t.Run(mode, func(t *testing.T) {
			schema := dialogue.SchemaForMode(mode)
			if len(schema) == 0 {
				t.Fatalf("%s のスキーマが空", mode)
			}
			converted, shape, err := convertSchema(schema)
			if err != nil {
				t.Fatalf("上位のスキーマを変換できない: %v", err)
			}
			if converted == nil || shape == nil {
				t.Fatal("変換結果が空")
			}
			added := map[string]bool{}
			collectOptional(shape, "", added)
			totalAdded += len(added)
			// 変換後は strict の条件（全プロパティが required）を満たす
			assertAllRequired(t, converted, "")
		})
	}
	// 少なくとも 1 つのスキーマには任意のプロパティがある（変換が空振りしていないことの確認）。
	if totalAdded == 0 {
		t.Error("上位のスキーマに任意のプロパティが 1 つも無い（本変換の前提が崩れている）")
	}
}

// assertAllRequired は各オブジェクトの properties がすべて required に入っていることを確かめる。
func assertAllRequired(t *testing.T, node map[string]any, path string) {
	t.Helper()
	if props, ok := node["properties"].(map[string]any); ok {
		required := toStringSet(node["required"])
		for name, child := range props {
			if !required[name] {
				t.Errorf("%s%s が required に入っていない", path, name)
			}
			if childNode, ok := child.(map[string]any); ok {
				assertAllRequired(t, childNode, path+name+".")
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		assertAllRequired(t, items, path+"[].")
	}
}
