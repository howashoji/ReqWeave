//go:build integration

// 結合テスト（根拠の逆引き・参照欠落 × 実ファイル）。

package projectstore

import "testing"

// 逆引きは接頭辞で絞り込み、レコード ID 順に返す。
func TestCitingRecordsFiltersByPrefix(t *testing.T) {
	s := createTestProject(t)

	dec, err := s.CreateDecision(Decision{TopicKey: "scope/in-scope",
		Body: "受注チャネルは EDI と Web。", Evidence: []string{"IMP-001#L3-L3", "S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("決定を作れない: %v", err)
	}
	issue, err := s.CreateOpenIssue(OpenIssue{Owner: "佐藤", Body: "与信限度額",
		Status: OpenIssueOpen, Evidence: []string{"IMP-002#L1-L2"}})
	if err != nil {
		t.Fatalf("未決事項を作れない: %v", err)
	}
	req, err := s.CreateRequirement("INV", Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: RequirementFunctional,
		Priority: PriorityMust, Status: RequirementDraft,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"IMP-001#L3-L3"}})
	if err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}

	got, err := s.CitingRecords("IMP-001")
	if err != nil {
		t.Fatalf("逆引きに失敗: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("IMP-001 を引くレコードの件数が違う: %+v", got)
	}
	if got[0].RecordID != dec.ID || got[0].RecordKind != RecordKindDecision {
		t.Errorf("1 件目が違う（レコード ID 順のはず）: %+v", got[0])
	}
	if got[1].RecordID != req.ID || got[1].RecordKind != RecordKindRequirement {
		t.Errorf("2 件目が違う: %+v", got[1])
	}

	other, err := s.CitingRecords("IMP-002")
	if err != nil {
		t.Fatalf("逆引きに失敗: %v", err)
	}
	if len(other) != 1 || other[0].RecordID != issue.ID {
		t.Errorf("別資料の逆引きが違う: %+v", other)
	}
	// 接頭辞が空なら全根拠（発話参照も含む）。
	all, err := s.CitingRecords("")
	if err != nil {
		t.Fatalf("逆引きに失敗: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("全件の逆引きが違う: %+v", all)
	}
	// 存在しない資料は 0 件（エラーにしない）。
	none, err := s.CitingRecords("IMP-099")
	if err != nil || len(none) != 0 {
		t.Errorf("存在しない資料の逆引き: %+v %v", none, err)
	}
}

// 根拠へたどれない記録だけが参照欠落の一覧に出る。
func TestMissingEvidenceRecords(t *testing.T) {
	s := createTestProject(t)

	dec, err := s.CreateDecision(Decision{TopicKey: "scope/in-scope",
		Body: "受注チャネルは EDI と Web。", Evidence: []string{"IMP-001#L3-L3"}})
	if err != nil {
		t.Fatalf("決定を作れない: %v", err)
	}
	withEvidence, err := s.CreateRequirement("INV", Requirement{Title: "根拠あり",
		Chapter: "functional-requirements", Kind: RequirementFunctional,
		Priority: PriorityMust, Status: RequirementDraft,
		Body: "本文。", Evidence: []string{"IMP-001#L3-L3"}})
	if err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}
	viaDecision, err := s.CreateRequirement("INV", Requirement{Title: "決定経由",
		Chapter: "functional-requirements", Kind: RequirementFunctional,
		Priority: PriorityMust, Status: RequirementDraft,
		Body: "本文。", Decisions: []string{dec.ID}})
	if err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}
	orphan, err := s.CreateRequirement("INV", Requirement{Title: "根拠なし",
		Chapter: "functional-requirements", Kind: RequirementFunctional,
		Priority: PriorityMust, Status: RequirementDraft, Body: "本文。"})
	if err != nil {
		t.Fatalf("要件項目を作れない: %v", err)
	}

	got, err := s.MissingEvidenceRecords()
	if err != nil {
		t.Fatalf("参照欠落の一覧に失敗: %v", err)
	}
	if len(got) != 1 || got[0].RecordID != orphan.ID {
		t.Fatalf("参照欠落の一覧が違う: %+v", got)
	}
	for _, m := range got {
		if m.RecordID == withEvidence.ID || m.RecordID == viaDecision.ID || m.RecordID == dec.ID {
			t.Errorf("根拠のある記録が参照欠落に含まれている: %+v", m)
		}
	}
}
