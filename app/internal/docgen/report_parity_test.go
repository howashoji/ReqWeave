//go:build integration

// 結合テスト（進捗レポート × 対話エンジンの算出）。
//
// 外部テストパッケージ（docgen_test）にしてあるのは、対話エンジンが docgen を import するため
// （内部テストから dialogue を import すると循環になる）。レポートの充足率・ブロック要因が
// 対話エンジンの完成度・確定可否判定と同一であることを、両者の実装で突き合わせて固定する。

package docgen_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// completenessFromDialogue は対話エンジンの充足率算出をレポートへ渡す形にする
// （呼び出し側 = バインディング層が行う適合と同じ）。
func completenessFromDialogue(phase string, r docgen.Records) ([]docgen.ChapterProgress, error) {
	got, err := dialogue.Completeness(phase, dialogue.Records{
		Requirements: r.Requirements, Decisions: r.Decisions,
		OpenIssues: r.OpenIssues, Terms: r.Terms})
	if err != nil {
		return nil, err
	}
	out := make([]docgen.ChapterProgress, 0, len(got))
	for _, c := range got {
		out = append(out, docgen.ChapterProgress{ChapterID: c.ChapterID, Name: c.Name,
			Percent: c.Percent, Satisfied: c.Satisfied, Total: c.Total})
	}
	return out, nil
}

func parityRecords() docgen.Records {
	return docgen.Records{
		Decisions: []projectstore.Decision{
			{ID: "DEC-001", TopicKey: "background/current-state", Body: "現状は Excel 台帳。"},
			{ID: "DEC-002", TopicKey: "background/purpose", Body: "在庫管理を刷新する。"},
		},
		OpenIssues: []projectstore.OpenIssue{
			{ID: "ISS-001", Owner: "倉庫長", Status: projectstore.OpenIssueOpen, Body: "棚卸差異の承認者"},
			{ID: "ISS-002", Owner: "情報システム部", Status: projectstore.OpenIssueResolved, Body: "連携範囲"},
			{ID: "ISS-003", Owner: "営業部", Status: projectstore.OpenIssueOpen, Body: "誰もブロックしていない論点"},
		},
		Requirements: []projectstore.Requirement{
			{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
				Kind: projectstore.RequirementFunctional, Status: projectstore.RequirementDraft,
				AcceptanceCriteria: []string{"3 秒以内"}, BlockedBy: []string{"ISS-001", "ISS-002"}},
		},
		Terms: []projectstore.Term{{Name: "在庫引当", NameEn: "Stock Allocation", Definition: "在庫の確保"}},
	}
}

// 章4: レポートが出す期末の充足率は対話エンジンの完成度の算出値そのものである
// （レポート側で丸め直し・再計算をしない）。
func TestProgressReportCompletenessMatchesDialogue(t *testing.T) {
	records := parityRecords()
	want, err := dialogue.Completeness(projectstore.PhaseRequirements, dialogue.Records{
		Requirements: records.Requirements, Decisions: records.Decisions,
		OpenIssues: records.OpenIssues, Terms: records.Terms})
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("前提が崩れている: 章観点が 1 件も無い")
	}

	report, err := docgen.ProgressReport(docgen.ProgressReportInput{
		ProjectName: "在庫管理システム",
		Phase:       projectstore.PhaseRequirements,
		From:        parityFrom(), To: parityTo(), Now: parityTo(),
		ProjectCreatedAt: parityFrom(),
		Records:          records,
		Completeness:     completenessFromDialogue,
	})
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}

	// 章4 の各行は「| 章観点名 | 期首 | 期末 |」。期末値が対話エンジンの算出値と一致すること。
	satisfied, total := 0, 0
	for _, c := range want {
		row := fmt.Sprintf("| %s | 0%% | %d%% |", c.Name, c.Percent)
		if !strings.Contains(report, row) {
			t.Errorf("章観点 %s の充足率が対話エンジンと違う（期待行 %q）:\n%s", c.ChapterID, row, report)
		}
		satisfied += c.Satisfied
		total += c.Total
	}
	// 要約の全体値も同じデータから算出する。
	overall := fmt.Sprintf("0%% → %d%%", 100*satisfied/total)
	if !strings.Contains(report, overall) {
		t.Errorf("要約の全体充足率が違う（期待 %q）:\n%s", overall, report)
	}
}

// 章6: 確定をブロックしている未決事項は対話エンジンの確定可否判定と同一の判定である。
func TestProgressReportBlockingMatchesDialogue(t *testing.T) {
	records := parityRecords()
	want := dialogue.Confirmable(dialogue.Records{
		Requirements: records.Requirements, Decisions: records.Decisions,
		OpenIssues: records.OpenIssues, Terms: records.Terms}).BlockingIssues
	if len(want) == 0 {
		t.Fatal("前提が崩れている: ブロック中の未決事項がフィクスチャに無い")
	}

	report, err := docgen.ProgressReport(docgen.ProgressReportInput{
		ProjectName: "在庫管理システム",
		Phase:       projectstore.PhaseRequirements,
		From:        parityFrom(), To: parityTo(), Now: parityTo(),
		ProjectCreatedAt: parityFrom(),
		Records:          records,
		Completeness:     completenessFromDialogue,
	})
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}

	for _, id := range want {
		if !containsRow(report, id) {
			t.Errorf("確定可否判定がブロック要因とする %s がレポートに出ていない:\n%s", id, report)
		}
	}
	// 対話エンジンがブロック要因としない未決事項は出さない。
	for _, issue := range records.OpenIssues {
		if contains(want, issue.ID) {
			continue
		}
		if blockingSection(t, report) != "" && containsRow(blockingSection(t, report), issue.ID) {
			t.Errorf("確定可否判定がブロック要因としない %s がレポートに出た:\n%s", issue.ID, report)
		}
	}
}

func parityFrom() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }
func parityTo() time.Time   { return time.Date(2026, 8, 31, 23, 59, 0, 0, time.UTC) }

// containsRow は Markdown 表の行に ID があるかを返す。
func containsRow(report, id string) bool { return strings.Contains(report, "| "+id+" |") }

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// blockingSection は章6 を切り出す。
func blockingSection(t *testing.T, report string) string {
	t.Helper()
	const heading = "## 6. 確定をブロックしている要因"
	i := strings.Index(report, heading)
	if i < 0 {
		t.Fatalf("章6 が見つからない:\n%s", report)
	}
	rest := report[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}
