//go:build integration

// 結合テスト（取り込み分析 × 実ファイル）。AI プロバイダはスタブへ差し替える（外部 API を呼ばない）。
// 対象: 分析・承認・反映・一時状態の保全と削除、根拠参照、議事録の随時取り込み。

package dialogue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

const materialText = `## 在庫管理の現行業務

受注が確定した時点で在庫を引き当てる。欠品時はバックオーダーを起票する。

## 棚卸

棚卸は月次で実施し、差異は倉庫長が承認する。
`

// 応答の候補（抽出スキーマ + perspective_candidates）。根拠は当該資料の実在行を指す。
const materialResponse = `{
  "decisions": [{"topic_key": "business-flow/main-flow", "body": "在庫は受注確定時に引き当てる。",
    "rationale": "現行業務の記述", "evidence_refs": ["IMP-001#L3-L3"], "duplicate_of": null}],
  "open_issues": [{"topic": "棚卸差異の承認者を確定する", "owner": "倉庫長",
    "evidence_refs": ["IMP-001#L7-L7"]}],
  "requirement_updates": [{"operation": "create", "chapter": "functional-requirements",
    "title": "在庫引当", "body_after": "受注確定時に在庫を引き当てること。",
    "acceptance_criteria": ["受注確定から 3 秒以内に引当結果を返すこと"],
    "evidence_refs": ["IMP-001#L3-L3"]}],
  "term_candidates": [{"term": "バックオーダー", "english": "back-order",
    "definition": "欠品時に起票する未出荷の受注", "evidence_refs": ["IMP-001#L3-L3"]}],
  "contradictions": [],
  "perspective_candidates": [{"name": "棚卸の差異処理", "summary": "差異の承認経路と記録方法",
    "evidence_refs": ["IMP-001#L7-L7"]}]
}`

func newMaterial(t *testing.T, store *projectstore.Store, kind importer.Kind) *importer.Meta {
	t.Helper()
	im := importer.New(store)
	meta, err := im.Import(importer.Input{
		Kind: kind, SourceName: "現行業務.md", SourceFormat: importer.FormatMD,
		Content:          []byte(materialText),
		ExtractionStatus: importer.StatusExtracted, ExtractedText: materialText,
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}
	return meta
}

// snapshotRecords はレコード類のファイル内容を採取する（承認前に変化しないことの確認用）。
//
// imports/ と audit/ は分析そのもので変わるため除く（前者は一時状態、後者は送信記録）。
func snapshotRecords(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			if strings.HasPrefix(rel, "imports") || strings.HasPrefix(rel, "audit") ||
				strings.HasPrefix(rel, "locks") {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("プロジェクトフォルダを走査できない: %v", err)
	}
	return out
}

func diffSnapshots(before, after map[string]string) []string {
	var changed []string
	for path, sum := range after {
		if before[path] != sum {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			changed = append(changed, path+"（消滅）")
		}
	}
	return changed
}

// 対話セッションの途中でも実行でき、対話状態とレコードを変えない。
// 注入文脈と出力（候補・観点候補・根拠）。
func TestAnalyzeMaterialProducesCandidatesWithoutChangingRecords(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	// 対話セッションを進行中にしておく（取り込み分析が状態を変えないことの確認用）。
	sess, err := e.StartSession(projectstore.PhaseRequirements)
	if err != nil {
		t.Fatalf("セッションを開始できない: %v", err)
	}
	stateBefore, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatalf("対話状態を読めない: %v", err)
	}
	before := snapshotRecords(t, store.Root())

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}

	if got.Fallback || len(got.FailedChunks) != 0 {
		t.Fatalf("正常応答なのに縮退している: %+v", got)
	}
	if got.ChunkCount != 1 {
		t.Errorf("コンテキスト長に収まる資料が分割された: %d 件", got.ChunkCount)
	}
	ex := got.Extraction
	if len(ex.Decisions) != 1 || len(ex.OpenIssues) != 1 || len(ex.RequirementUpdates) != 1 ||
		len(ex.TermCandidates) != 1 {
		t.Fatalf("候補が揃っていない: %+v", ex)
	}
	if len(ex.PerspectiveCandidates) != 1 || ex.PerspectiveCandidates[0].Name != "棚卸の差異処理" {
		t.Errorf("観点候補が取れていない: %+v", ex.PerspectiveCandidates)
	}
	if len(ex.Decisions[0].EvidenceRefs) != 1 || ex.Decisions[0].EvidenceRefs[0] != "IMP-001#L3-L3" {
		t.Errorf("根拠が取り込み元の該当箇所になっていない: %+v", ex.Decisions[0].EvidenceRefs)
	}

	// 承認前にレコードは 1 件も作られない。
	if changed := diffSnapshots(before, snapshotRecords(t, store.Root())); len(changed) != 0 {
		t.Errorf("承認前にレコードファイルが変化した: %v", changed)
	}
	decisions, err := store.ListDecisions()
	if err != nil || len(decisions) != 0 {
		t.Errorf("承認前に決定事項が記録された: %d 件 %v", len(decisions), err)
	}

	// 対話セッションの状態は変わらない。
	stateAfter, err := e.LoadState(sess.ID)
	if err != nil {
		t.Fatalf("対話状態を読めない: %v", err)
	}
	if stateAfter.DialogueState != stateBefore.DialogueState ||
		stateAfter.PendingCandidates != nil {
		t.Errorf("取り込み分析が対話状態を変えた: %+v → %+v", stateBefore, stateAfter)
	}

	// 送信内容: 注入文脈は決定・未決・要件項目・用語のみ。対話履歴を含めない。
	if len(stub.reqs) != 1 {
		t.Fatalf("送信回数が違う: %d", len(stub.reqs))
	}
	content := stub.reqs[0].Messages[0].Content
	if !strings.Contains(content, materialText) {
		t.Error("抽出テキスト全文が送られていない")
	}
	if strings.Contains(content, "これまでの対話") {
		t.Error("対話履歴が注入されている")
	}
	if !strings.Contains(stub.reqs[0].System, "IMP-nnn#Lm-Ln") {
		t.Error("出力契約に取り込み分析の根拠形式が無い")
	}
	if len(stub.reqs[0].ImportRefs) != 1 || !stub.reqs[0].ConsentGiven {
		t.Errorf("同意・資料識別が送信に付いていない: %+v", stub.reqs[0].ImportRefs)
	}

	// 未承認候補が analysis.meta.yaml へ保全される。
	saved, err := importer.New(store).LoadAnalysis(meta.ID)
	if err != nil || saved == nil {
		t.Fatalf("一時状態が保存されていない: %v", err)
	}
	if saved.AnalysisState != importer.AnalysisAwaitingApproval {
		t.Errorf("一時状態が承認待ちでない: %q", saved.AnalysisState)
	}
	pending, err := e.PendingMaterialAnalysis(meta.ID)
	if err != nil || pending == nil || len(pending.Decisions) != 1 {
		t.Fatalf("保全した候補を復元できない: %v %+v", err, pending)
	}
	if len(pending.PerspectiveCandidates) != 1 {
		t.Errorf("観点候補が保全されていない: %+v", pending.PerspectiveCandidates)
	}
}

// 実在しない資料 ID・行範囲の根拠は除去する。
func TestAnalyzeMaterialRemovesInvalidEvidence(t *testing.T) {
	const response = `{
	  "decisions": [{"topic_key": "business-flow/main-flow", "body": "実在しない行だけを根拠にした候補",
	    "rationale": "", "evidence_refs": ["IMP-001#L900-L950", "IMP-002#L1-L2"]}],
	  "open_issues": [{"topic": "一部だけ実在する根拠", "evidence_refs": ["IMP-001#L3-L3", "IMP-001#L999-L999"]}],
	  "requirement_updates": [], "term_candidates": [], "contradictions": [], "perspective_candidates": []
	}`
	stub := &stubAdapter{scripts: []string{response}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	d := got.Extraction.Decisions[0]
	if len(d.EvidenceRefs) != 0 || !d.MissingEvidence {
		t.Errorf("実在しない根拠が残っている（参照欠落として立たない）: %+v", d)
	}
	o := got.Extraction.OpenIssues[0]
	if len(o.EvidenceRefs) != 1 || o.EvidenceRefs[0] != "IMP-001#L3-L3" || o.MissingEvidence {
		t.Errorf("実在する根拠まで落ちている: %+v", o)
	}
}

// 既存レコードとの重複は duplicate_of、矛盾は contradictions で提示する。
func TestAnalyzeMaterialSurfacesDuplicatesAndContradictions(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)

	dec, err := store.CreateDecision(projectstore.Decision{TopicKey: "business-flow/main-flow",
		Body: "在庫は出荷時に引き当てる。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("既存の決定を作れない: %v", err)
	}
	req, err := store.CreateRequirement("INV", projectstore.Requirement{
		Title: "在庫引当", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
		Status: projectstore.RequirementDraft, Body: "受注確定時に在庫を引き当てること。"})
	if err != nil {
		t.Fatalf("既存の要件項目を作れない: %v", err)
	}
	stub.scripts = []string{`{
	  "decisions": [], "open_issues": [],
	  "requirement_updates": [{"operation": "create", "chapter": "functional-requirements",
	    "title": "在庫引当", "body_after": "受注確定時に在庫を引き当てること。",
	    "evidence_refs": ["IMP-001#L3-L3"], "duplicate_of": "` + req.ID + `"}],
	  "term_candidates": [],
	  "contradictions": [
	    {"with_decision_id": "` + dec.ID + `", "description": "資料は受注確定時、既存決定は出荷時",
	      "evidence_refs": ["IMP-001#L3-L3"]},
	    {"with_decision_id": "DEC-999", "description": "実在しない決定との矛盾"}],
	  "perspective_candidates": []
	}`}
	meta := newMaterial(t, store, importer.KindMaterial)

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	if got.Extraction.RequirementUpdates[0].DuplicateOf != req.ID {
		t.Errorf("重複指摘が保持されていない: %+v", got.Extraction.RequirementUpdates[0])
	}
	if len(got.Extraction.Contradictions) != 1 ||
		got.Extraction.Contradictions[0].WithDecisionID != dec.ID {
		t.Errorf("矛盾指摘が正しくない: %+v", got.Extraction.Contradictions)
	}
	if !strings.Contains(got.Notice, "矛盾指摘 1 件") {
		t.Errorf("実在しない指摘先を外したことが案内に出ていない: %q", got.Notice)
	}
	// 既存レコードは注入文脈に載る（重複・矛盾の判断材料）。
	content := stub.reqs[0].Messages[0].Content
	if !strings.Contains(content, dec.ID) || !strings.Contains(content, req.ID) {
		t.Errorf("既存レコードが注入されていない:\n%s", content)
	}
}

// 議事録では既存未決事項の決着に相当する内容を決着案として提示する。
func TestAnalyzeMinutesProposesIssueResolution(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	issue, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "佐藤",
		Status: projectstore.OpenIssueOpen, Body: "棚卸差異の承認者",
		Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatalf("未決事項を作れない: %v", err)
	}
	stub.scripts = []string{`{
	  "decisions": [{"topic_key": "business-flow/main-flow", "body": "棚卸差異は倉庫長が承認する。",
	    "rationale": "議事録で合意", "evidence_refs": ["IMP-001#L7-L7"]}],
	  "open_issues": [], "requirement_updates": [], "term_candidates": [],
	  "contradictions": [], "perspective_candidates": []
	}`}
	meta := newMaterial(t, store, importer.KindMinutes)

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("議事録の分析に失敗: %v", err)
	}
	if len(got.Extraction.Decisions) != 1 {
		t.Fatalf("決着案が提示されていない: %+v", got.Extraction)
	}
	if got.Kind != string(importer.KindMinutes) {
		t.Errorf("資料種別が結果に反映されていない: %q", got.Kind)
	}
	content := stub.reqs[0].Messages[0].Content
	if !strings.Contains(content, "議事録") {
		t.Error("資料が議事録であることを送っていない")
	}
	if !strings.Contains(content, issue.ID) || !strings.Contains(content, "棚卸差異の承認者") {
		t.Errorf("既存の未決事項が注入されていない（決着案の判断材料）:\n%s", content)
	}
	if !strings.Contains(stub.reqs[0].System, "決着案") {
		t.Error("議事録の決着案生成の指示が出力契約に無い")
	}

	// 決着は承認操作で行う（分析だけでは未決事項の状態を変えない）。
	after, err := store.LoadOpenIssue(issue.ID)
	if err != nil {
		t.Fatalf("未決事項を読めない: %v", err)
	}
	if after.Status != projectstore.OpenIssueOpen {
		t.Errorf("分析だけで未決事項が決着した: %q", after.Status)
	}
}

// 観点候補が登録できないときは、記録の候補も含めて何も書かない。
//
// 観点の登録は記録の反映より後に行うため、以前は決定事項だけが記録されてから観点で失敗し、
// 分析結果（候補）が残ったまま押し直すと決定事項が重複記録された。
func TestApplyMaterialApprovalValidatesPerspectivesBeforeWriting(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	ex := analysis.Extraction

	_, err = e.ApplyMaterialApproval(meta.ID, MaterialApproval{
		ApprovalRequest: ApprovalRequest{Decisions: []DecisionApproval{{Candidate: ex.Decisions[0]}}},
		Perspectives:    []PerspectiveCandidate{{Name: "棚卸の差異処理", Summary: "差異の承認経路"}},
	})
	if err == nil {
		t.Fatal("該当箇所の無い観点候補を登録できてしまった")
	}
	if msg := err.Error(); !strings.Contains(msg, "質問観点の候補「棚卸の差異処理」は、資料の該当箇所を特定できないため登録できません") {
		t.Errorf("直すべき候補が利用者の言葉で示されない: %v", err)
	}
	if ds, _ := store.ListDecisions(); len(ds) != 0 {
		t.Fatalf("失敗したのに決定事項が記録された: %d 件", len(ds))
	}
	// 分析結果（候補）は残り、観点を外して押し直せば 1 件だけ記録される。
	if saved, err := importer.New(store).LoadAnalysis(meta.ID); err != nil || saved == nil {
		t.Fatalf("失敗後に候補が消えた: %v", err)
	}
	if _, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{
		ApprovalRequest: ApprovalRequest{Decisions: []DecisionApproval{{Candidate: ex.Decisions[0]}}},
	}); err != nil {
		t.Fatalf("押し直しで反映できない: %v", err)
	}
	if ds, _ := store.ListDecisions(); len(ds) != 1 {
		t.Errorf("決定事項が重複または欠落した: %d 件", len(ds))
	}
}

// 承認・反映の完了で完成度を再算出し、一時状態を削除する。
func TestApplyMaterialApprovalAppliesAndClearsState(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	ex := analysis.Extraction

	got, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{ApprovalRequest: ApprovalRequest{
		Decisions:          []DecisionApproval{{Candidate: ex.Decisions[0]}},
		OpenIssues:         ex.OpenIssues,
		RequirementUpdates: []RequirementApproval{{Candidate: ex.RequirementUpdates[0], Group: "INV"}},
		TermCandidates:     ex.TermCandidates,
	}})
	if err != nil {
		t.Fatalf("承認の反映に失敗: %v", err)
	}
	if len(got.DecisionIDs) != 1 || len(got.OpenIssueIDs) != 1 || len(got.RequirementIDs) != 1 ||
		len(got.TermNames) != 1 {
		t.Fatalf("反映結果が揃っていない: %+v", got.ApprovalResult)
	}
	if len(got.Completeness) == 0 {
		t.Fatal("完成度が再算出されていない")
	}
	// 反映後のレコードを実ファイルから読み直して確認する。
	reqs, err := store.ListRequirements()
	if err != nil || len(reqs) != 1 {
		t.Fatalf("要件項目が反映されていない: %d 件 %v", len(reqs), err)
	}
	if len(reqs[0].Evidence) != 1 || reqs[0].Evidence[0] != "IMP-001#L3-L3" {
		t.Errorf("取り込み元の該当箇所が evidence に保存されていない: %+v", reqs[0].Evidence)
	}
	// 完成度は反映後のレコードから算出されている（章観点が 0% のままではない）。
	var functional ChapterCompleteness
	for _, c := range got.Completeness {
		if c.ChapterID == "functional-requirements" {
			functional = c
		}
	}
	if functional.Percent == 0 {
		t.Errorf("反映後の完成度が更新されていない: %+v", functional)
	}
	// 一時状態は削除される。
	saved, err := importer.New(store).LoadAnalysis(meta.ID)
	if err != nil {
		t.Fatalf("一時状態の確認に失敗: %v", err)
	}
	if saved != nil {
		t.Errorf("承認完了後も analysis.meta.yaml が残っている: %+v", saved)
	}
	if _, err := os.Stat(filepath.Join(store.Root(),
		filepath.FromSlash(importer.AnalysisPath(meta.ID)))); !os.IsNotExist(err) {
		t.Errorf("一時状態ファイルが実ファイルとして残っている: %v", err)
	}
	// 原本・抽出テキストは残る。
	if _, err := importer.New(store).ReadExtracted(meta.ID); err != nil {
		t.Errorf("抽出テキストが読めなくなった: %v", err)
	}
}

// 全候補を破棄した場合はレコードを作らず、一時状態だけを削除する（対話の承認と同じ原則）。
func TestApplyMaterialApprovalWithAllDiscarded(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	if _, err := e.AnalyzeMaterial(context.Background(), meta.ID, true); err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	before := snapshotRecords(t, store.Root())

	got, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{})
	if err != nil {
		t.Fatalf("全破棄に失敗: %v", err)
	}
	if len(got.DecisionIDs) != 0 || len(got.RequirementIDs) != 0 {
		t.Errorf("破棄した候補が記録された: %+v", got.ApprovalResult)
	}
	if changed := diffSnapshots(before, snapshotRecords(t, store.Root())); len(changed) != 0 {
		t.Errorf("全破棄でレコードファイルが変化した: %v", changed)
	}
	saved, err := importer.New(store).LoadAnalysis(meta.ID)
	if err != nil || saved != nil {
		t.Errorf("全破棄後も一時状態が残っている: %+v %v", saved, err)
	}
	// 分析していない資料は承認できない（一時状態が無い）。
	if _, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{}); err == nil {
		t.Error("分析結果が無いのに承認できてしまった")
	}
}

// AI 障害中も原本・抽出テキストは参照でき、
// 分析だけを後から再実行できる。
func TestAnalyzeMaterialRecoversAfterAPIFailure(t *testing.T) {
	stub := &stubAdapter{err: &aiprovider.ProviderError{
		Class: aiprovider.ErrClassConfig, Provider: aiprovider.ProviderAnthropic,
		HTTPStatus: 401, Message: "invalid api key"}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("障害時にエラーで止まった（縮退して返るはず）: %v", err)
	}
	if !got.Fallback || len(got.FailedChunks) == 0 {
		t.Fatalf("障害が縮退として返っていない: %+v", got)
	}
	if !strings.Contains(got.Notice, "分析だけをやり直せます") {
		t.Errorf("次の行動が案内されていない: %q", got.Notice)
	}
	// 原本・抽出テキストは参照できる。
	im := importer.New(store)
	if _, err := im.ReadSource(meta.ID); err != nil {
		t.Errorf("障害中に原本を読めない: %v", err)
	}
	if _, err := im.ReadExtracted(meta.ID); err != nil {
		t.Errorf("障害中に抽出テキストを読めない: %v", err)
	}
	saved, err := im.LoadAnalysis(meta.ID)
	if err != nil || saved == nil || saved.AnalysisState != importer.AnalysisAnalyzing {
		t.Fatalf("障害時の一時状態が保全されていない: %+v %v", saved, err)
	}

	// 回復後、分析だけを再実行できる。
	stub.err = nil
	stub.scripts = []string{materialResponse}
	retried, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("再実行に失敗: %v", err)
	}
	if retried.Fallback || len(retried.Extraction.Decisions) != 1 {
		t.Fatalf("再実行で候補が得られていない: %+v", retried)
	}
	saved, err = im.LoadAnalysis(meta.ID)
	if err != nil || saved == nil || saved.AnalysisState != importer.AnalysisAwaitingApproval {
		t.Errorf("再実行後の一時状態が承認待ちになっていない: %+v %v", saved, err)
	}
}

// 同意していない資料は送信されない。
func TestAnalyzeMaterialWithoutConsentDoesNotSend(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	got, err := e.AnalyzeMaterial(context.Background(), meta.ID, false)
	if err != nil {
		t.Fatalf("同意なしでエラー終了した（縮退して返るはず）: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("同意なしで送信された: %d 回", stub.calls)
	}
	if !got.Fallback {
		t.Errorf("同意なしで候補が生成された: %+v", got.Extraction)
	}
	saved, err := importer.New(store).LoadAnalysis(meta.ID)
	if err != nil || saved == nil || saved.AnalysisState != importer.AnalysisAnalyzing {
		t.Errorf("一時状態が承認待ちになっている（候補は無いはず）: %+v %v", saved, err)
	}
}

// 抽出できなかった資料（extraction_status: failed）は分析対象にしない（中身の無いテキストを AI へ送らない）。
func TestAnalyzeMaterialRejectsUnextractedSource(t *testing.T) {
	stub := &stubAdapter{}
	e, store := newTestEngine(t, stub)
	im := importer.New(store)
	meta, err := im.Import(importer.Input{
		Kind: importer.KindMaterial, SourceName: "画像PDF.pdf", SourceFormat: importer.FormatPDF,
		Content: []byte("%PDF-1.7\n"), ExtractionStatus: importer.StatusFailed,
	})
	if err != nil {
		t.Fatalf("資料を取り込めない: %v", err)
	}

	if _, err := e.AnalyzeMaterial(context.Background(), meta.ID, true); err == nil {
		t.Fatal("抽出できていない資料が分析された")
	}
	if stub.calls != 0 {
		t.Errorf("抽出できていない資料が送信された: %d 回", stub.calls)
	}
	// 原本は保持されたまま。
	if _, err := im.ReadSource(meta.ID); err != nil {
		t.Errorf("原本が保持されていない: %v", err)
	}
}

// 承認された記録の evidence に IMP-nnn#Lm-Ln が保存され、
// そこから取り込み元へ遡及できる。逆方向の参照は資料側に保持しない。
func TestApprovedRecordsKeepImportEvidence(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	ex := analysis.Extraction
	if _, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{ApprovalRequest: ApprovalRequest{
		Decisions:          []DecisionApproval{{Candidate: ex.Decisions[0]}},
		OpenIssues:         ex.OpenIssues,
		RequirementUpdates: []RequirementApproval{{Candidate: ex.RequirementUpdates[0], Group: "INV"}},
	}}); err != nil {
		t.Fatalf("承認の反映に失敗: %v", err)
	}

	// 決定事項・未決事項・要件項目のいずれにも取り込み元の該当箇所が残る。
	decisions, err := store.ListDecisions()
	if err != nil || len(decisions) != 1 {
		t.Fatalf("決定事項が反映されていない: %v", err)
	}
	if !contains(decisions[0].Evidence, "IMP-001#L3-L3") {
		t.Errorf("決定事項の evidence に取り込み元が無い: %+v", decisions[0].Evidence)
	}
	issues, err := store.ListOpenIssues()
	if err != nil || len(issues) != 1 {
		t.Fatalf("未決事項が反映されていない: %v", err)
	}
	if !contains(issues[0].Evidence, "IMP-001#L7-L7") {
		t.Errorf("未決事項の evidence に取り込み元が無い: %+v", issues[0].Evidence)
	}
	reqs, err := store.ListRequirements()
	if err != nil || len(reqs) != 1 {
		t.Fatalf("要件項目が反映されていない: %v", err)
	}
	if !contains(reqs[0].Evidence, "IMP-001#L3-L3") {
		t.Errorf("要件項目の evidence に取り込み元が無い: %+v", reqs[0].Evidence)
	}

	// 根拠から原本と該当箇所へ遡及できる。
	im := importer.New(store)
	loc, err := im.ResolveRef(decisions[0].Evidence[0])
	if err != nil {
		t.Fatalf("承認済みレコードから取り込み元へ遡及できない: %v", err)
	}
	if loc.ImportID != meta.ID || len(loc.Excerpt) != 1 ||
		!strings.Contains(loc.Excerpt[0], "受注が確定した時点") {
		t.Errorf("該当箇所が違う: %+v", loc)
	}

	// 逆方向（資料 → 承認済みレコード）は派生で解決する（参照は記録側の片方向だけに持つ）。
	citations, err := store.CitingRecords(meta.ID)
	if err != nil {
		t.Fatalf("逆引きに失敗: %v", err)
	}
	got := map[string]bool{}
	for _, c := range citations {
		got[c.RecordID] = true
	}
	if !got[decisions[0].ID] || !got[issues[0].ID] || !got[reqs[0].ID] {
		t.Errorf("逆引きで承認済みレコードを拾えていない: %+v", citations)
	}

	// 資料側（import.yaml・analysis.meta.yaml）には承認レコードを書き戻さない。
	raw, err := os.ReadFile(filepath.Join(store.Root(), filepath.FromSlash(importer.MetaPath(meta.ID))))
	if err != nil {
		t.Fatalf("import.yaml を読めない: %v", err)
	}
	for _, id := range []string{decisions[0].ID, issues[0].ID, reqs[0].ID} {
		if strings.Contains(string(raw), id) {
			t.Errorf("資料側に承認レコード %s が書き戻されている（参照は記録側の片方向だけに持つ）:\n%s", id, raw)
		}
	}
}

// 根拠参照のない候補は参照欠落として一覧に現れ、記録側でも同じ一覧で拾える。
func TestMissingEvidenceSurfacesForCandidatesAndRecords(t *testing.T) {
	const response = `{
	  "decisions": [{"topic_key": "business-flow/main-flow", "body": "根拠のない決定",
	    "rationale": "", "evidence_refs": ["IMP-001#L900-L950"]}],
	  "open_issues": [], "term_candidates": [], "contradictions": [],
	  "requirement_updates": [{"operation": "create", "chapter": "functional-requirements",
	    "title": "根拠のない要件", "body_after": "受注確定時に在庫を引き当てること。",
	    "evidence_refs": ["IMP-002#L1-L2"]}],
	  "perspective_candidates": []
	}`
	stub := &stubAdapter{scripts: []string{response}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	analysis, err := e.AnalyzeMaterial(context.Background(), meta.ID, true)
	if err != nil {
		t.Fatalf("取り込み分析に失敗: %v", err)
	}
	missing := analysis.MissingEvidenceCandidates()
	if len(missing) != 2 {
		t.Fatalf("参照欠落の候補が一覧に現れない: %+v", missing)
	}
	if missing[0].Kind != projectstore.RecordKindDecision || missing[0].Title != "根拠のない決定" ||
		missing[1].Kind != projectstore.RecordKindRequirement || missing[1].Title != "根拠のない要件" {
		t.Errorf("参照欠落の一覧の内容が違う: %+v", missing)
	}

	// 承認前は記録が無いので記録側の一覧も空。
	before, err := store.MissingEvidenceRecords()
	if err != nil || len(before) != 0 {
		t.Fatalf("承認前に参照欠落レコードがある: %+v %v", before, err)
	}

	// 決定事項は根拠なしでは記録できない（記録層の根拠の検証が働く）。
	if _, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{ApprovalRequest: ApprovalRequest{
		Decisions: []DecisionApproval{{Candidate: analysis.Extraction.Decisions[0]}},
	}}); err == nil {
		t.Fatal("根拠の無い決定事項が記録できてしまった")
	}

	// 要件項目は根拠なしでも記録でき、記録側の参照欠落一覧に現れる。
	if _, err := e.ApplyMaterialApproval(meta.ID, MaterialApproval{ApprovalRequest: ApprovalRequest{
		RequirementUpdates: []RequirementApproval{
			{Candidate: analysis.Extraction.RequirementUpdates[0], Group: "INV"}},
	}}); err != nil {
		t.Fatalf("要件項目の承認に失敗: %v", err)
	}
	after, err := store.MissingEvidenceRecords()
	if err != nil {
		t.Fatalf("参照欠落の一覧に失敗: %v", err)
	}
	if len(after) != 1 || after[0].RecordKind != projectstore.RecordKindRequirement ||
		after[0].Title != "根拠のない要件" {
		t.Fatalf("参照欠落の記録が一覧に現れない: %+v", after)
	}

	// 根拠を持つ記録は一覧に出ない。
	if _, err := store.CreateDecision(projectstore.Decision{TopicKey: "scope/in-scope",
		Body: "根拠のある決定", Evidence: []string{"IMP-001#L3-L3"}}); err != nil {
		t.Fatalf("決定を作れない: %v", err)
	}
	after, err = store.MissingEvidenceRecords()
	if err != nil || len(after) != 1 {
		t.Errorf("根拠のある記録まで参照欠落に含まれている: %+v %v", after, err)
	}
}
