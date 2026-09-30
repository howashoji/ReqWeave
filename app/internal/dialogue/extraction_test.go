package dialogue

import (
	"strings"
	"testing"
)

const validExtraction = `{
  "decisions": [{
    "topic_key": "background/current-state",
    "body": "現状は Excel 台帳で在庫を管理している。",
    "rationale": "回答で明示された",
    "evidence_refs": ["S-0001#utt-00002"],
    "supersedes_decision_id": null
  }],
  "open_issues": [{
    "topic": "棚卸の頻度",
    "owner": null,
    "due": null,
    "needs_stakeholder": true,
    "blocks_requirement_ids": [],
    "evidence_refs": ["S-0001#utt-00002"]
  }],
  "requirement_updates": [{
    "operation": "create",
    "target_id": null,
    "chapter": "functional-requirements",
    "title": "在庫引当",
    "body_after": "受注確定時に在庫を引き当てること。",
    "acceptance_criteria": ["受注確定から 3 秒以内に引当が完了すること"],
    "evidence_refs": ["S-0001#utt-00002"]
  }],
  "term_candidates": [{
    "term": "在庫引当", "english": "Stock Allocation",
    "definition": "受注に対して在庫を確保すること。", "evidence_refs": ["S-0001#utt-00002"]
  }],
  "contradictions": []
}`

// 抽出結果は 5 区分のスキーマで解釈する。
func TestParseExtraction(t *testing.T) {
	got, err := ParseExtraction(validExtraction)
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].TopicKey != "background/current-state" {
		t.Errorf("決定事項候補が違う: %+v", got.Decisions)
	}
	if len(got.OpenIssues) != 1 || !got.OpenIssues[0].NeedsStakeholder {
		t.Errorf("未決事項候補が違う: %+v", got.OpenIssues)
	}
	if len(got.RequirementUpdates) != 1 || got.RequirementUpdates[0].Operation != OperationCreate ||
		len(got.RequirementUpdates[0].AcceptanceCriteria) != 1 {
		t.Errorf("要件項目の反映案が違う: %+v", got.RequirementUpdates)
	}
	if len(got.TermCandidates) != 1 {
		t.Errorf("用語候補が違う: %+v", got.TermCandidates)
	}
	if got.IsEmpty() {
		t.Error("候補があるのに空と判定された")
	}

	// コードフェンス・前後の説明文が付いていても解釈する。
	fenced := "分析しました。\n```json\n" + validExtraction + "\n```\n以上です。"
	if _, err := ParseExtraction(fenced); err != nil {
		t.Errorf("コードフェンス付きが解釈できない: %v", err)
	}
}

// スキーマに合わない出力は検証で弾く（再要求の対象になる）。
func TestParseExtractionRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"JSON でない":         "候補はありません。",
		"論点キーが無い":          `{"decisions":[{"body":"本文","evidence_refs":["S-0001#utt-00002"]}]}`,
		"本文が無い":            `{"decisions":[{"topic_key":"a/b","evidence_refs":["S-0001#utt-00002"]}]}`,
		"操作が不正":            `{"requirement_updates":[{"operation":"delete","chapter":"c","title":"t","body_after":"b"}]}`,
		"update に対象 ID 無し": `{"requirement_updates":[{"operation":"update","chapter":"c","title":"t","body_after":"b"}]}`,
		"create に対象 ID":    `{"requirement_updates":[{"operation":"create","target_id":"FR-A-001","chapter":"c","title":"t","body_after":"b"}]}`,
		"用語の定義が無い":         `{"term_candidates":[{"term":"用語","english":"Term"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseExtraction(body); err == nil {
				t.Errorf("不正な抽出結果が受理された: %+v", got)
			}
		})
	}
}

// 実在しない根拠は除去し、空になった候補は参照欠落として識別する。
func TestVerifyEvidence(t *testing.T) {
	ex, err := ParseExtraction(validExtraction)
	if err != nil {
		t.Fatal(err)
	}
	// 実在する発話が 1 件だけの場合。
	ex.VerifyEvidence(map[string]bool{"S-0001#utt-00002": true})
	if len(ex.Decisions[0].EvidenceRefs) != 1 || ex.Decisions[0].MissingEvidence {
		t.Errorf("実在する根拠が除去された: %+v", ex.Decisions[0])
	}

	// 実在しない場合は除去され、参照欠落になる。
	ex2, _ := ParseExtraction(validExtraction)
	ex2.VerifyEvidence(map[string]bool{"S-0001#utt-00099": true})
	if len(ex2.Decisions[0].EvidenceRefs) != 0 || !ex2.Decisions[0].MissingEvidence {
		t.Errorf("実在しない根拠が残った: %+v", ex2.Decisions[0])
	}
	if !ex2.RequirementUpdates[0].MissingEvidence || !ex2.OpenIssues[0].MissingEvidence {
		t.Errorf("参照欠落が識別されない: %+v %+v", ex2.RequirementUpdates[0], ex2.OpenIssues[0])
	}

	// 形式が不正な参照も除去する（発話・回答・資料の参照形式に合わないもの）。
	ex3 := &Extraction{Decisions: []DecisionCandidate{{TopicKey: "a/b", Body: "本文",
		EvidenceRefs: []string{"utt-00002", "S-1#utt-2", "S-0001#utt-00002"}}}}
	ex3.VerifyEvidence(map[string]bool{"S-0001#utt-00002": true})
	if len(ex3.Decisions[0].EvidenceRefs) != 1 || ex3.Decisions[0].EvidenceRefs[0] != "S-0001#utt-00002" {
		t.Errorf("形式不正の参照が残った: %+v", ex3.Decisions[0].EvidenceRefs)
	}
}

// 根拠が空になった候補へ抽出対象の発話参照を補う。
func TestFillMissingEvidence(t *testing.T) {
	ex, err := ParseExtraction(validExtraction)
	if err != nil {
		t.Fatal(err)
	}
	// AI が書いた ID が実在しない → 3 区分とも根拠が空になる。
	ex.VerifyEvidence(map[string]bool{"S-0001#utt-00099": true})
	ex.FillMissingEvidence([]string{"S-0001#utt-00004"})
	for _, got := range [][]string{ex.Decisions[0].EvidenceRefs, ex.OpenIssues[0].EvidenceRefs,
		ex.RequirementUpdates[0].EvidenceRefs} {
		if len(got) != 1 || got[0] != "S-0001#utt-00004" {
			t.Errorf("抽出対象の発話参照が補われない: %v", got)
		}
	}
	if ex.Decisions[0].MissingEvidence || ex.OpenIssues[0].MissingEvidence || ex.RequirementUpdates[0].MissingEvidence {
		t.Errorf("補ったのに参照欠落のまま: %+v", ex)
	}

	// AI が正しく書いた根拠は置き換えない。
	ex2, _ := ParseExtraction(validExtraction)
	ex2.VerifyEvidence(map[string]bool{"S-0001#utt-00002": true})
	ex2.FillMissingEvidence([]string{"S-0001#utt-00004"})
	if got := ex2.Decisions[0].EvidenceRefs; len(got) != 1 || got[0] != "S-0001#utt-00002" {
		t.Errorf("正しい根拠が置き換えられた: %v", got)
	}

	// 抽出対象を特定できないときは参照欠落のまま示す（黙って空の根拠で通さない）。
	ex3, _ := ParseExtraction(validExtraction)
	ex3.VerifyEvidence(map[string]bool{})
	ex3.FillMissingEvidence(nil)
	if !ex3.Decisions[0].MissingEvidence || len(ex3.Decisions[0].EvidenceRefs) != 0 {
		t.Errorf("補う参照が無いのに参照欠落が消えた: %+v", ex3.Decisions[0])
	}
}

// 未承認候補は併置メタデータへ保存でき、読み直して同じ内容に戻る。
func TestExtractionRoundTripThroughMeta(t *testing.T) {
	ex, err := ParseExtraction(validExtraction)
	if err != nil {
		t.Fatal(err)
	}
	encoded := encodeExtraction(ex)
	if _, ok := encoded.(map[string]any); !ok {
		t.Fatalf("保存形式が map でない: %T", encoded)
	}
	got, err := decodeExtraction(encoded)
	if err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].Body != ex.Decisions[0].Body {
		t.Errorf("決定事項候補が復元されない: %+v", got.Decisions)
	}
	if len(got.RequirementUpdates) != 1 || got.RequirementUpdates[0].Title != "在庫引当" {
		t.Errorf("要件項目の反映案が復元されない: %+v", got.RequirementUpdates)
	}
	// YAML 由来の map[any]any でも復元できる。
	yamlLike := map[any]any{"decisions": []any{map[any]any{
		"topic_key": "a/b", "body": "本文", "evidence_refs": []any{"S-0001#utt-00002"}}}}
	got2, err := decodeExtraction(yamlLike)
	if err != nil {
		t.Fatalf("YAML 由来の構造を復元できない: %v", err)
	}
	if len(got2.Decisions) != 1 || got2.Decisions[0].TopicKey != "a/b" {
		t.Errorf("YAML 由来の復元が違う: %+v", got2.Decisions)
	}
}

// 空の抽出結果（候補なし）も有効な応答として扱う。
func TestEmptyExtraction(t *testing.T) {
	got, err := ParseExtraction(`{"decisions":[],"open_issues":[],"requirement_updates":[],"term_candidates":[],"contradictions":[]}`)
	if err != nil {
		t.Fatalf("空の抽出結果が拒否された: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("空と判定されない: %+v", got)
	}
	if strings.Contains(ExtractionSchemaJSON, "acceptance_criteria") == false {
		t.Error("出力契約に受け入れ条件が含まれていない")
	}
}
