package docgen

import (
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func chapter(name, body string) projectstore.DocumentChapter {
	return projectstore.DocumentChapter{
		DocKind: projectstore.DocKindRequirements, Chapter: strings.TrimSuffix(name, ".md"),
		FileName: name, Status: projectstore.DocStatusGenerated,
		GeneratedAt: time.Now(), Body: body,
	}
}

func verifyRecords() Records {
	return Records{
		Requirements: []projectstore.Requirement{
			{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
				Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
				Status: projectstore.RequirementDraft, Body: "受注確定時に引き当てる。",
				AcceptanceCriteria: []string{"3 秒以内"}, Decisions: []string{"DEC-001"}},
		},
		Decisions: []projectstore.Decision{
			{ID: "DEC-001", TopicKey: "functional-requirements/list", Body: "受注確定時に引き当てる。",
				DecidedAt: time.Now(), Evidence: []string{"S-0001#utt-00001"}},
		},
		Terms: []projectstore.Term{
			{Name: "在庫引当", NameEn: "Stock Allocation", Definition: "受注に在庫を確保すること。",
				Forbidden: []string{"引当て"}},
		},
	}
}

// V1: 本文中の ID を走査し、存在しない参照先を列挙する。存在する ID は検出しない。
func TestVerifyIDReferences(t *testing.T) {
	in := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("06-functional-requirements.md", "FR-INV-001 は DEC-001 に基づく。\nFR-INV-999 も参照する。"),
	}}
	got := Verify(in)

	var v1 []Violation
	for _, v := range got.Violations {
		if v.Check == CheckIDReference {
			v1 = append(v1, v)
		}
	}
	if len(v1) != 1 {
		t.Fatalf("V1 の検出数が違う: %+v", v1)
	}
	if v1[0].Target != "FR-INV-999" || v1[0].Line != 2 || v1[0].Severity != SeverityError {
		t.Errorf("V1 の内容が違う: %+v", v1[0])
	}

	// 参照切れが無ければ検出しない。
	clean := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("06-functional-requirements.md", "FR-INV-001 は DEC-001 に基づく。"),
	}}
	for _, v := range Verify(clean).Violations {
		if v.Check == CheckIDReference {
			t.Errorf("参照切れが無いのに検出された: %+v", v)
		}
	}
}

// V2: 根拠を持たない要件項目と、要件項目 ID を参照しない設計要素を検出する。
func TestVerifyMissingEvidence(t *testing.T) {
	records := verifyRecords()
	records.Requirements = append(records.Requirements, projectstore.Requirement{
		ID: "FR-INV-002", Title: "欠品時の扱い", Chapter: "functional-requirements",
		Kind: projectstore.RequirementFunctional, Priority: projectstore.PriorityMust,
		Status: projectstore.RequirementDraft, Body: "欠品時はバックオーダー。",
		AcceptanceCriteria: []string{"翌営業日までに起票"},
	})
	got := Verify(VerifyInput{Records: records})

	var targets []string
	for _, v := range got.Violations {
		if v.Check == CheckMissingEvidence {
			targets = append(targets, v.Target)
		}
	}
	if len(targets) != 1 || targets[0] != "FR-INV-002" {
		t.Fatalf("V2 の検出が違う: %+v", targets)
	}

	// 基本設計の章が要件項目 ID を参照しない場合も V2。
	design := chapter("01-architecture.md", "## 全体構成\n\n三層構成とする。")
	design.DocKind = projectstore.DocKindBasicDesign
	got = Verify(VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{design}})
	found := false
	for _, v := range got.Violations {
		if v.Check == CheckMissingEvidence && v.File == "01-architecture.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("要件 ID を参照しない設計要素が検出されない: %+v", got.Violations)
	}

	// 要件 ID を参照していれば検出しない。
	design.Body = "## 全体構成\n\nFR-INV-001 を満たすため三層構成とする。"
	got = Verify(VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{design}})
	for _, v := range got.Violations {
		if v.Check == CheckMissingEvidence && v.File == "01-architecture.md" {
			t.Errorf("要件 ID を参照しているのに検出された: %+v", v)
		}
	}
}

// V3: ブロックする未決事項の残存を検出する（確定可否判定と同一データ）。
func TestVerifyBlockingOpenIssues(t *testing.T) {
	records := verifyRecords()
	records.Requirements[0].BlockedBy = []string{"ISS-001"}
	records.OpenIssues = []projectstore.OpenIssue{
		{ID: "ISS-001", Owner: "佐藤", Status: projectstore.OpenIssueOpen, Body: "引当の単位",
			Evidence: []string{"S-0001#utt-00001"}},
		{ID: "ISS-002", Owner: "佐藤", Status: projectstore.OpenIssueOpen, Body: "ブロックしない論点",
			Evidence: []string{"S-0001#utt-00002"}},
	}
	got := Verify(VerifyInput{Records: records})

	var v3 []Violation
	for _, v := range got.Violations {
		if v.Check == CheckBlockingOpenIssue {
			v3 = append(v3, v)
		}
	}
	if len(v3) != 1 || v3[0].Target != "ISS-001" {
		t.Fatalf("V3 の検出が違う: %+v", v3)
	}
	if !strings.Contains(v3[0].Message, "FR-INV-001") {
		t.Errorf("ブロック対象が示されない: %+v", v3[0])
	}

	// 決着すれば検出しない。
	records.OpenIssues[0].Status = projectstore.OpenIssueResolved
	for _, v := range Verify(VerifyInput{Records: records}).Violations {
		if v.Check == CheckBlockingOpenIssue {
			t.Errorf("決着済みなのに検出された: %+v", v)
		}
	}
}

// V4: 禁止同義語と英語識別子の不一致を検出する。
func TestVerifyTermMismatch(t *testing.T) {
	in := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("06-functional-requirements.md",
			"在庫引当（Stock Allocation）を行う。\n引当てのタイミングは受注確定時。\n在庫引当（Inventory Reserve）とも書く。"),
	}}
	got := Verify(in)

	var forbidden, english *Violation
	for i, v := range got.Violations {
		if v.Check != CheckTermMismatch {
			continue
		}
		switch v.Target {
		case "引当て":
			forbidden = &got.Violations[i]
		case "在庫引当":
			english = &got.Violations[i]
		}
	}
	if forbidden == nil || forbidden.Line != 2 {
		t.Errorf("禁止同義語が検出されない: %+v", got.Violations)
	}
	if english == nil || english.Line != 3 {
		t.Errorf("英語識別子の不一致が検出されない: %+v", got.Violations)
	}
	if english != nil && !strings.Contains(english.Message, "Stock Allocation") {
		t.Errorf("正しい英語識別子が示されない: %+v", english)
	}

	// 用語集どおりの表記だけなら検出しない。
	clean := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("06.md", "在庫引当（Stock Allocation）を行う。"),
	}}
	for _, v := range Verify(clean).Violations {
		if v.Check == CheckTermMismatch {
			t.Errorf("正しい表記が検出された: %+v", v)
		}
	}
}

// V5: 曖昧語を警告として検出する。リストは差し替えられる。
func TestVerifyAmbiguousWords(t *testing.T) {
	in := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("07-non-functional-requirements.md", "処理は速いこと。\n画面は使いやすいこと。"),
	}}
	got := Verify(in)

	var v5 []Violation
	for _, v := range got.Violations {
		if v.Check == CheckAmbiguousWord {
			v5 = append(v5, v)
		}
	}
	if len(v5) != 2 {
		t.Fatalf("V5 の検出数が違う: %+v", v5)
	}
	for _, v := range v5 {
		if v.Severity != SeverityWarning {
			t.Errorf("V5 が警告区分でない: %+v", v)
		}
	}

	// リストを差し替えると検出対象が変わる（利用者による追加・除外の受け口）。
	custom := in
	custom.AmbiguousWords = []string{"使いやすい"}
	v5 = nil
	for _, v := range Verify(custom).Violations {
		if v.Check == CheckAmbiguousWord {
			v5 = append(v5, v)
		}
	}
	if len(v5) != 1 || v5[0].Target != "使いやすい" {
		t.Errorf("曖昧語リストの差し替えが効かない: %+v", v5)
	}
}

// V6: 受け入れ条件が 0 件の要件項目を警告として検出する（判定元は acceptance_criteria）。
func TestVerifyAcceptanceCriteria(t *testing.T) {
	records := verifyRecords()
	records.Requirements = append(records.Requirements, projectstore.Requirement{
		ID: "NFR-PF-001", Title: "応答性能", Chapter: "non-functional-requirements",
		Kind: projectstore.RequirementNonFunctional, Priority: projectstore.PriorityMust,
		Status: projectstore.RequirementDraft, Decisions: []string{"DEC-001"},
		// 本文には受け入れ条件らしき記述があるが、判定には使わない。
		Body: "受け入れ条件: 1 秒以内に応答すること。",
	})
	got := Verify(VerifyInput{Records: records})

	var v6 []Violation
	for _, v := range got.Violations {
		if v.Check == CheckAcceptanceCriteria {
			v6 = append(v6, v)
		}
	}
	if len(v6) != 1 || v6[0].Target != "NFR-PF-001" || v6[0].Severity != SeverityWarning {
		t.Fatalf("V6 の検出が違う: %+v", v6)
	}
}

// エラー区分（V1〜V4）と警告区分（V5・V6）を区別して返す。違反ゼロなら合格。
func TestVerifySeverityAndPass(t *testing.T) {
	clean := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("06-functional-requirements.md", "FR-INV-001 は DEC-001 に基づく。在庫引当（Stock Allocation）を行う。"),
	}}
	got := Verify(clean)
	if !got.Passed() || got.Errors() != 0 || got.Warnings() != 0 {
		t.Fatalf("違反が無いのに合格しない: %+v", got.Violations)
	}

	dirty := VerifyInput{Records: verifyRecords(), Chapters: []projectstore.DocumentChapter{
		chapter("06-functional-requirements.md", "FR-INV-999 は速い。"),
	}}
	got = Verify(dirty)
	if got.Passed() {
		t.Fatal("違反があるのに合格した")
	}
	if got.Errors() != 1 || got.Warnings() != 1 {
		t.Errorf("区分ごとの件数が違う: エラー %d / 警告 %d（%+v）", got.Errors(), got.Warnings(), got.Violations)
	}
}

// 曖昧語リストは初期リストへプロジェクトの追加・除外を適用したものになる。
func TestAmbiguousWordsFor(t *testing.T) {
	base := AmbiguousWordsFor(nil)
	if len(base) != len(DefaultAmbiguousWords) {
		t.Fatalf("調整なしで初期リストと一致しない: %+v", base)
	}

	p := &projectstore.Project{AmbiguousTerms: &projectstore.AmbiguousTerms{
		Added:    []string{"なるべく"},
		Excluded: []string{"随時"},
	}}
	got := AmbiguousWordsFor(p)
	has := func(w string) bool {
		for _, v := range got {
			if v == w {
				return true
			}
		}
		return false
	}
	if has("随時") {
		t.Errorf("除外した語が残っている: %+v", got)
	}
	if !has("なるべく") {
		t.Errorf("追加した語が入っていない: %+v", got)
	}
	if !has("速い") {
		t.Errorf("初期リストの語が失われた: %+v", got)
	}

	// 「随時」を除外したプロジェクトでは V5 が検出しない。
	in := VerifyInput{Records: verifyRecords(), AmbiguousWords: got,
		Chapters: []projectstore.DocumentChapter{chapter("07.md", "随時に確認する。なるべく早く。")}}
	var targets []string
	for _, v := range Verify(in).Violations {
		if v.Check == CheckAmbiguousWord {
			targets = append(targets, v.Target)
		}
	}
	if len(targets) != 1 || targets[0] != "なるべく" {
		t.Errorf("調整が検証に反映されない: %+v", targets)
	}
}
