//go:build integration

// 結合テスト（実ファイル I/O）。要件項目・決定事項・未決事項・用語。

package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newDecision(topic string) Decision {
	return Decision{TopicKey: topic, Body: "受注確定時に在庫を引き当てる。",
		Evidence: []string{"S-0001#utt-00003"}}
}

// 決定事項は追記のみ。覆すときは新しい決定で置き換え、
// 旧決定には置き換え先への参照だけが付く（本文は書き換えない）。
func TestDecisionSupersede(t *testing.T) {
	s := createTestProject(t)
	old, err := s.CreateDecision(newDecision("scope/in-scope"))
	if err != nil {
		t.Fatalf("決定事項を作成できない: %v", err)
	}
	if old.ID != "DEC-001" {
		t.Fatalf("採番が違う: %s", old.ID)
	}
	oldBody := old.Body

	next, err := s.CreateDecision(Decision{TopicKey: "scope/in-scope", Supersedes: old.ID,
		Body: "出荷指示時の引当に変更する。", Evidence: []string{"S-0001#utt-00021"}})
	if err != nil {
		t.Fatalf("置き換えの決定事項を作成できない: %v", err)
	}
	if next.ID != "DEC-002" {
		t.Errorf("採番が違う: %s", next.ID)
	}

	got, err := s.LoadDecision(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SupersededBy != next.ID {
		t.Errorf("旧決定に置き換え先が付いていない: %+v", got)
	}
	if got.Body != oldBody {
		t.Errorf("旧決定の本文が書き換えられた: %q", got.Body)
	}

	// 存在しない決定は置き換え元にできない。
	if _, err := s.CreateDecision(Decision{TopicKey: "x/y", Supersedes: "DEC-099",
		Body: "本文", Evidence: []string{"S-0001#utt-00001"}}); err == nil {
		t.Error("存在しない決定を置き換え元にできた")
	}
}

// 決定事項には根拠が 1 件以上必要。
func TestDecisionRequiresEvidence(t *testing.T) {
	s := createTestProject(t)
	d := newDecision("scope/in-scope")
	d.Evidence = nil
	if _, err := s.CreateDecision(d); err == nil {
		t.Error("根拠なしの決定事項が受理された")
	}
	d = newDecision("")
	if _, err := s.CreateDecision(d); err == nil {
		t.Error("論点キーなしの決定事項が受理された")
	}
}

// 未決事項は決める人・期限・状態・根拠を持ち、期限超過を判定できる。
func TestOpenIssueLifecycle(t *testing.T) {
	s := createTestProject(t)
	issue, err := s.CreateOpenIssue(OpenIssue{
		Owner: "営業部 佐藤", Due: "2026-09-30", NeedsStakeholder: true,
		Evidence: []string{"S-0001#utt-00005"}, Body: "与信限度額の決裁者を誰にするか。",
	})
	if err != nil {
		t.Fatalf("未決事項を作成できない: %v", err)
	}
	if issue.ID != "ISS-001" || issue.Status != OpenIssueOpen {
		t.Fatalf("初期値が違う: %+v", issue)
	}

	// 期限超過の判定（期限当日は超過にしない）。
	onDue := time.Date(2026, 9, 30, 23, 0, 0, 0, time.Local)
	after := time.Date(2026, 10, 1, 0, 1, 0, 0, time.Local)
	if issue.IsOverdue(onDue) {
		t.Error("期限当日が超過と判定された")
	}
	if !issue.IsOverdue(after) {
		t.Error("期限翌日が超過と判定されない")
	}

	// 決着は決定事項の ID を伴う。
	if _, err := s.resolveOpenIssueUnguarded(issue.ID, "DEC-099"); err == nil {
		t.Error("存在しない決定事項で決着できた")
	}
	d, err := s.CreateDecision(newDecision("scope/credit"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.resolveOpenIssueUnguarded(issue.ID, d.ID)
	if err != nil {
		t.Fatalf("決着に失敗: %v", err)
	}
	if resolved.Status != OpenIssueResolved || resolved.ResolvedBy != d.ID {
		t.Errorf("決着が反映されていない: %+v", resolved)
	}
	if resolved.IsOverdue(after) {
		t.Error("決着済みが期限超過と判定された")
	}
	if _, err := s.resolveOpenIssueUnguarded(issue.ID, d.ID); err == nil {
		t.Error("二重に決着できた")
	}

	// 決める人・根拠は必須。
	if _, err := s.CreateOpenIssue(OpenIssue{Evidence: []string{"S-0001#utt-00005"}, Body: "論点"}); err == nil {
		t.Error("決める人なしの未決事項が受理された")
	}
	if _, err := s.CreateOpenIssue(OpenIssue{Owner: "佐藤", Body: "論点"}); err == nil {
		t.Error("根拠なしの未決事項が受理された")
	}
	if _, err := s.CreateOpenIssue(OpenIssue{Owner: "佐藤", Due: "2026/09/30",
		Evidence: []string{"S-0001#utt-00005"}, Body: "論点"}); err == nil {
		t.Error("期限の書式が不正な未決事項が受理された")
	}
}

// 要件項目 ID は FR-<グループ>-nnn。グループごとの連番で採番し再利用しない。
func TestRequirementIDAllocation(t *testing.T) {
	s := createTestProject(t)
	base := Requirement{Title: "在庫引当", Chapter: "functional-requirements",
		Kind: RequirementFunctional, Priority: PriorityMust, Body: "受注確定時に在庫を引き当てること。"}

	first, err := s.CreateRequirement("inv", base)
	if err != nil {
		t.Fatalf("要件項目を作成できない: %v", err)
	}
	if first.ID != "FR-INV-001" {
		t.Fatalf("採番が違う: %s", first.ID)
	}
	second, err := s.CreateRequirement("INV", base)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "FR-INV-002" {
		t.Errorf("同一グループの連番が違う: %s", second.ID)
	}
	other, err := s.CreateRequirement("SLS", base)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID != "FR-SLS-001" {
		t.Errorf("グループごとの連番になっていない: %s", other.ID)
	}
	nf := base
	nf.Kind = RequirementNonFunctional
	nfr, err := s.CreateRequirement("INV", nf)
	if err != nil {
		t.Fatal(err)
	}
	if nfr.ID != "NFR-INV-001" {
		t.Errorf("非機能要件の採番が違う: %s", nfr.ID)
	}

	if _, err := s.CreateRequirement("在庫", base); err == nil {
		t.Error("グループが英大文字・数字でない要件項目が受理された")
	}
}

// 差し戻し（agreed→draft）には理由が必要。
func TestRequirementRevertRequiresReason(t *testing.T) {
	s := createTestProject(t)
	r, err := s.CreateRequirement("INV", Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: RequirementFunctional,
		Priority: PriorityMust, Body: "受注確定時に在庫を引き当てること。"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.updateRequirement(r.ID, func(x *Requirement) error {
		x.Status = RequirementAgreed
		return nil
	}); err != nil {
		t.Fatalf("合意済みへの変更に失敗: %v", err)
	}
	if _, err := s.updateRequirement(r.ID, func(x *Requirement) error {
		x.Status = RequirementDraft
		return nil
	}); err == nil {
		t.Error("理由なしで差し戻せた")
	}
	got, err := s.updateRequirement(r.ID, func(x *Requirement) error {
		x.Status = RequirementDraft
		x.RevertedReason = "回答取込で前提が変わったため"
		return nil
	})
	if err != nil {
		t.Fatalf("差し戻しに失敗: %v", err)
	}
	if got.Status != RequirementDraft || got.RevertedReason == "" {
		t.Errorf("差し戻しが反映されていない: %+v", got)
	}
	if _, err := s.updateRequirement(r.ID, func(x *Requirement) error {
		x.ID = "FR-INV-999"
		return nil
	}); err == nil {
		t.Error("ID を変更できた")
	}
}

// 根拠へたどれない要件項目を識別できる（参照欠落の一覧表示用）。
func TestRequirementEvidenceLinkage(t *testing.T) {
	s := createTestProject(t)
	r, err := s.CreateRequirement("INV", Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: RequirementFunctional,
		Priority: PriorityMust, Body: "受注確定時に在庫を引き当てること。"})
	if err != nil {
		t.Fatal(err)
	}
	if r.HasEvidence() {
		t.Error("根拠が無いのに参照ありと判定された")
	}
	got, err := s.updateRequirement(r.ID, func(x *Requirement) error {
		x.Decisions = []string{"DEC-001"}
		x.BlockedBy = []string{"ISS-002"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasEvidence() {
		t.Error("決定事項に紐づいたのに参照なしと判定された")
	}
	list, err := s.ListRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].BlockedBy) != 1 {
		t.Errorf("一覧の内容が違う: %+v", list)
	}
}

// 用語集の読み書きと一意性。
func TestTerms(t *testing.T) {
	s := createTestProject(t)
	got, err := s.LoadTerms()
	if err != nil || len(got.Terms) != 0 {
		t.Fatalf("初期状態が空でない: %+v %v", got, err)
	}

	if _, err := s.upsertTerm(Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "受注に対して在庫を確保すること。", Forbidden: []string{"引当て", "在庫確保"}}); err != nil {
		t.Fatalf("用語を保存できない: %v", err)
	}
	if _, err := s.upsertTerm(Term{Name: "与信", NameEn: "Credit", Definition: "取引先ごとの取引上限。"}); err != nil {
		t.Fatal(err)
	}
	// 同じ表記は置き換える（重複させない）。
	updated, err := s.upsertTerm(Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "受注確定時に在庫を確保すること。"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Terms) != 2 {
		t.Fatalf("件数が違う: %+v", updated.Terms)
	}
	term, ok := updated.Find("在庫引当")
	if !ok || !strings.Contains(term.Definition, "受注確定時") {
		t.Errorf("用語が更新されていない: %+v", term)
	}

	// 英語識別子の重複は拒否する。
	if err := s.SaveTerms(&Terms{Terms: []Term{
		{Name: "A", NameEn: "Same", Definition: "定義"},
		{Name: "B", NameEn: "Same", Definition: "定義"},
	}}); err == nil {
		t.Error("英語識別子が重複する用語集が受理された")
	}
	if err := s.SaveTerms(&Terms{Terms: []Term{{Name: "A", Definition: "定義"}}}); err == nil {
		t.Error("英語識別子なしの用語が受理された")
	}
}

// レコードは「フロントマター + Markdown 本文」で人が読める形で保存する。
func TestRecordFilesAreHumanReadable(t *testing.T) {
	s := createTestProject(t)
	d, err := s.CreateDecision(newDecision("scope/in-scope"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Root(), filepath.FromSlash(DecisionFile(d.ID))))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") || !strings.Contains(text, "topic_key: scope/in-scope") {
		t.Errorf("フロントマターの形式が違う:\n%s", text)
	}
	if !strings.Contains(text, d.Body) {
		t.Errorf("本文が Markdown のまま保存されていない:\n%s", text)
	}
}

// 採番は並行呼び出しでも重複しない（ids ロックで直列化する）。
func TestConcurrentRecordIDAllocation(t *testing.T) {
	s := createTestProject(t)
	// 排他の正しさを見るテストのため、待ち間隔だけ短くする（既存の採番テストと同じ扱い）。
	s.lockPolicy.retryMin = 5 * time.Millisecond
	s.lockPolicy.retryMax = 20 * time.Millisecond
	const n = 6

	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	reqIDs := make([]string, n)
	reqErrs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := s.CreateDecision(newDecision("scope/in-scope"))
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = d.ID
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.CreateRequirement("INV", Requirement{Title: "在庫引当",
				Chapter: "functional-requirements", Kind: RequirementFunctional,
				Priority: PriorityMust, Body: "受注確定時に在庫を引き当てること。"})
			if err != nil {
				reqErrs[i] = err
				return
			}
			reqIDs[i] = r.ID
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, id := range ids {
		if errs[i] != nil {
			t.Fatalf("決定事項の採番に失敗: %v", errs[i])
		}
		if seen[id] {
			t.Fatalf("決定事項の ID が重複した: %s", id)
		}
		seen[id] = true
	}
	seenReq := map[string]bool{}
	for i, id := range reqIDs {
		if reqErrs[i] != nil {
			t.Fatalf("要件項目の採番に失敗: %v", reqErrs[i])
		}
		if seenReq[id] {
			t.Fatalf("要件項目の ID が重複した: %s", id)
		}
		seenReq[id] = true
	}
	if len(seen) != n || len(seenReq) != n {
		t.Errorf("採番数が違う: 決定 %d / 要件 %d", len(seen), len(seenReq))
	}
}
