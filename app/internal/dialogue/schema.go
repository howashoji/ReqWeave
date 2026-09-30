package dialogue

import "encoding/json"

// 本ファイルは抽出出力スキーマの**契約**を持つ。
//
// スキーマは `aiprovider.ChatRequest.ResponseSchema` として各プロバイダの構造化出力機能へ
// **JSON Schema としてそのまま**渡る（Anthropic の
// output_config.format / OpenAI の text.format.json_schema / Google の responseJsonSchema）。
// そのため本ファイルの定義は妥当な JSON Schema（draft 2020-12 相当）でなければならない
// （出力例の形をした JSON では 3 社とも受理しない）。
//
// 各項目の意味は description に書いてある（AI への説明を兼ねる）。

// ExtractionSchemaJSON は抽出結果の JSON Schema。
const ExtractionSchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["decisions", "open_issues", "requirement_updates", "term_candidates", "contradictions"],
  "properties": {
    "decisions": {
      "type": "array",
      "description": "決定事項の候補。該当が無ければ空配列",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["topic_key", "body", "rationale", "evidence_refs"],
        "properties": {
          "topic_key": {"type": "string", "description": "章観点ID/必須項目ID（論点キー）"},
          "body": {"type": "string", "description": "決定内容の本文（決定事項レコードの本文となる）"},
          "rationale": {"type": "string", "description": "決定理由の要約"},
          "evidence_refs": {
            "type": "array", "minItems": 1, "items": {"type": "string"},
            "description": "根拠の参照ID。1件以上必須"
          },
          "supersedes_decision_id": {
            "type": ["string", "null"], "description": "置き換える既存決定のID。なければ null"
          },
          "duplicate_of": {
            "type": ["string", "null"], "description": "重複する既存レコードのID。なければ null"
          }
        }
      }
    },
    "open_issues": {
      "type": "array",
      "description": "未決事項の候補。該当が無ければ空配列",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["topic", "evidence_refs"],
        "properties": {
          "topic": {"type": "string", "description": "論点"},
          "owner": {"type": ["string", "null"], "description": "決める人。特定できなければ null"},
          "due": {"type": ["string", "null"], "description": "期限 YYYY-MM-DD。特定できなければ null"},
          "needs_stakeholder": {"type": "boolean", "description": "質問票発行の候補か"},
          "blocks_requirement_ids": {
            "type": "array", "items": {"type": "string"},
            "description": "ブロックする要件項目ID"
          },
          "evidence_refs": {
            "type": "array", "minItems": 1, "items": {"type": "string"},
            "description": "根拠の参照ID。1件以上必須"
          },
          "duplicate_of": {
            "type": ["string", "null"], "description": "重複する既存レコードのID。なければ null"
          }
        }
      }
    },
    "requirement_updates": {
      "type": "array",
      "description": "要件項目への反映案。該当が無ければ空配列",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["operation", "chapter", "title", "body_after", "evidence_refs"],
        "properties": {
          "operation": {"enum": ["create", "update"], "description": "新規作成か既存項目の更新か"},
          "target_id": {
            "type": ["string", "null"],
            "description": "update 時は既存の FR-*/NFR-* ID。create 時は null（IDはアプリが採番）"
          },
          "chapter": {"type": "string", "description": "章観点ID"},
          "id_group": {
            "type": ["string", "null"],
            "description": "create 時の ID グループ（FR-<グループ>-nnn の <グループ>。英大文字と数字 2〜8 文字。既に使っているグループがあれば流用する。分からなければ null）"
          },
          "title": {"type": "string", "description": "要件名"},
          "body_after": {"type": "string", "description": "反映後の要件文の全文"},
          "acceptance_criteria": {
            "type": "array", "items": {"type": "string"},
            "description": "測定可能な受け入れ条件"
          },
          "evidence_refs": {
            "type": "array", "minItems": 1, "items": {"type": "string"},
            "description": "根拠の参照ID。1件以上必須"
          },
          "duplicate_of": {
            "type": ["string", "null"], "description": "重複する既存レコードのID。なければ null"
          }
        }
      }
    },
    "term_candidates": {
      "type": "array",
      "description": "用語集への追加候補。該当が無ければ空配列",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["term", "english", "definition"],
        "properties": {
          "term": {"type": "string", "description": "用語の表記"},
          "english": {"type": "string", "description": "英語識別子"},
          "definition": {"type": "string", "description": "定義"},
          "evidence_refs": {"type": "array", "items": {"type": "string"}, "description": "根拠の参照ID"}
        }
      }
    },
    "contradictions": {
      "type": "array",
      "description": "既存決定との矛盾。該当が無ければ空配列",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["with_decision_id", "description"],
        "properties": {
          "with_decision_id": {"type": "string", "description": "矛盾する既存決定のID"},
          "description": {"type": "string", "description": "矛盾内容の説明"},
          "evidence_refs": {"type": "array", "items": {"type": "string"}, "description": "根拠の参照ID"}
        }
      }
    }
  }
}`

// perspectiveCandidatesSchemaJSON は取り込み分析だけに加える観点候補。
const perspectiveCandidatesSchemaJSON = `{
  "type": "array",
  "description": "プロジェクト固有の質問観点の候補。該当が無ければ空配列",
  "items": {
    "type": "object",
    "additionalProperties": false,
    "required": ["name", "summary"],
    "properties": {
      "name": {"type": "string", "description": "観点の名称"},
      "summary": {"type": "string", "description": "観点の要旨（何を確認する観点か）"},
      "evidence_refs": {"type": "array", "items": {"type": "string"}, "description": "根拠の参照ID"},
      "duplicate_of": {
        "type": ["string", "null"],
        "description": "重複する既存観点のID（PRS-nnn）またはプリセット観点キー。なければ null"
      }
    }
  }
}`

// MaterialAnalysisSchemaJSON は取り込み分析の出力スキーマ。
//
// 抽出結果と同一スキーマに perspective_candidates を加えたもの。
// 加算は組み立てで行い、共通部分を二重に書かない（片方だけ更新される事故を防ぐ）。
var MaterialAnalysisSchemaJSON = buildMaterialAnalysisSchema()

// buildMaterialAnalysisSchema は ExtractionSchemaJSON へ観点候補を差し込む。
//
// 入力はいずれもソース内の定数であり、失敗はビルド時に混入した記述ミスでしかない。
// 黙って壊れたスキーマを送らないよう panic で落とす（テストで即座に検出される）。
func buildMaterialAnalysisSchema() string {
	var schema map[string]any
	if err := json.Unmarshal([]byte(ExtractionSchemaJSON), &schema); err != nil {
		panic("抽出スキーマが JSON として不正: " + err.Error())
	}
	var perspectives map[string]any
	if err := json.Unmarshal([]byte(perspectiveCandidatesSchemaJSON), &perspectives); err != nil {
		panic("観点候補スキーマが JSON として不正: " + err.Error())
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		panic("抽出スキーマに properties がない")
	}
	props["perspective_candidates"] = perspectives
	required, ok := schema["required"].([]any)
	if !ok {
		panic("抽出スキーマに required がない")
	}
	schema["required"] = append(required, "perspective_candidates")
	out, err := json.Marshal(schema)
	if err != nil {
		panic("取り込み分析スキーマを組み立てられない: " + err.Error())
	}
	return string(out)
}

// relatedIDsSchemaJSON はフィードバック論点化だけに加える紐づけ候補。
const relatedIDsSchemaJSON = `{
  "type": "array",
  "items": {"type": "string"},
  "description": "関連する要件項目・設計要素の ID（紐づけ候補）。無ければ空配列"
}`

// FeedbackSchemaJSON はフィードバック論点化の出力スキーマ。
//
// 取り込み分析と同一スキーマに、各候補の related_ids を加えたもの。
var FeedbackSchemaJSON = buildFeedbackSchema()

// buildFeedbackSchema は取り込み分析スキーマの各候補へ related_ids を差し込む。
func buildFeedbackSchema() string {
	var schema map[string]any
	if err := json.Unmarshal([]byte(MaterialAnalysisSchemaJSON), &schema); err != nil {
		panic("取り込み分析スキーマが JSON として不正: " + err.Error())
	}
	var related map[string]any
	if err := json.Unmarshal([]byte(relatedIDsSchemaJSON), &related); err != nil {
		panic("紐づけ候補スキーマが JSON として不正: " + err.Error())
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		panic("取り込み分析スキーマに properties がない")
	}
	// 候補 3 種（決定・未決事項・要件項目の反映案）に付ける。
	for _, key := range []string{"decisions", "open_issues", "requirement_updates"} {
		array, ok := props[key].(map[string]any)
		if !ok {
			panic("スキーマに候補配列がない: " + key)
		}
		items, ok := array["items"].(map[string]any)
		if !ok {
			panic("候補配列に items がない: " + key)
		}
		itemProps, ok := items["properties"].(map[string]any)
		if !ok {
			panic("候補に properties がない: " + key)
		}
		itemProps["related_ids"] = related
	}
	out, err := json.Marshal(schema)
	if err != nil {
		panic("フィードバック論点化スキーマを組み立てられない: " + err.Error())
	}
	return string(out)
}

// SchemaForMode は当該モードで要求する構造化出力の JSON Schema を返す。
//
// 構造化出力を使わないモード（ModeQuestion = 質問生成。出力は JSON ではなく定型テキスト）では
// nil を返し、ChatRequest.ResponseSchema を空のままにする（非構造化）。
//
// スキーマ本体はプロンプトへ埋め込まない。埋め込みとプロバイダ指定の二重管理になり、
// 片方だけ更新したときに食い違うため。
func SchemaForMode(mode string) json.RawMessage {
	switch mode {
	case ModeExtraction, ModeImportAnalysis:
		return json.RawMessage(ExtractionSchemaJSON)
	case ModeMaterialAnalysis:
		return json.RawMessage(MaterialAnalysisSchemaJSON)
	case ModeFeedbackAnalysis:
		return json.RawMessage(FeedbackSchemaJSON)
	case ModeQuestionnaire:
		return json.RawMessage(QuestionnaireSchemaJSON)
	default:
		return nil
	}
}
