//go:build integration

// 結合テスト（進捗レポート × 実ファイル）。

package docgen

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newReportStore は決定・未決・要件・用語と変更履歴を持つプロジェクトを作る。
//
// 変更履歴は実運用と同じ auditlog.Logger 経由で記録する（期首値の復元はこの記録から行う）。
func newReportStore(t *testing.T) (*projectstore.Store, []auditlog.ChangeRecord) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger, err := auditlog.New(store, store.Author().AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	record := func(rec auditlog.ChangeRecord) {
		t.Helper()
		if err := logger.RecordChange(rec); err != nil {
			t.Fatal(err)
		}
	}

	// 期首（7 月）までに作ったレコード。
	early, err := store.CreateDecision(projectstore.Decision{TopicKey: "background/current-state",
		Body: "現状は Excel 台帳。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatal(err)
	}
	record(auditlog.ChangeRecord{At: at(2026, 7, 10, 10, 0), Target: early.ID,
		Change: auditlog.ChangeCreated, After: firstLine(early.Body)})

	issue, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "倉庫長", Due: "2026-09-30",
		Body: "棚卸差異の承認者を決める", Evidence: []string{"S-0001#utt-00002"}})
	if err != nil {
		t.Fatal(err)
	}
	record(auditlog.ChangeRecord{At: at(2026, 8, 10, 11, 0), Target: issue.ID,
		Change: auditlog.ChangeCreated, After: firstLine(issue.Body)})

	req, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Body: "受注確定時に在庫を引き当てること。",
		AcceptanceCriteria: []string{"受注確定から 3 秒以内に引当結果を返すこと"},
		Evidence:           []string{"S-0001#utt-00003"}, BlockedBy: []string{issue.ID}})
	if err != nil {
		t.Fatal(err)
	}
	record(auditlog.ChangeRecord{At: at(2026, 7, 16, 9, 0), Target: req.ID,
		Change: auditlog.ChangeCreated, After: req.Title})
	record(auditlog.ChangeRecord{At: at(2026, 8, 10, 11, 0), Target: req.ID,
		Change: auditlog.ChangeUpdated, After: "blocked_by: " + issue.ID})

	if _, err := store.UpsertTermGuarded(projectstore.RecordBaseline{}, projectstore.Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "受注に対して在庫を確保すること。"}); err != nil {
		t.Fatal(err)
	}
	record(auditlog.ChangeRecord{At: at(2026, 7, 12, 10, 0), Target: "在庫引当",
		Change: auditlog.ChangeUpdated, After: "受注に対して在庫を確保すること。"})

	// 期間内（8 月）の決定。
	inPeriod, err := store.CreateDecision(projectstore.Decision{TopicKey: "background/purpose",
		Body: "在庫管理を刷新する。", Evidence: []string{"S-0001#utt-00004"}})
	if err != nil {
		t.Fatal(err)
	}
	record(auditlog.ChangeRecord{At: at(2026, 8, 5, 14, 0), Target: inPeriod.ID,
		Change: auditlog.ChangeCreated, After: firstLine(inPeriod.Body)})

	changes, err := auditlog.ReadChanges(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	return store, changes
}

// reportInputFrom はプロジェクトの実データからレポート入力を組み立てる（呼び出し側の手順と同じ）。
func reportInputFrom(t *testing.T, store *projectstore.Store, changes []auditlog.ChangeRecord) ProgressReportInput {
	t.Helper()
	reqs, err := store.ListRequirements()
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := store.ListDecisions()
	if err != nil {
		t.Fatal(err)
	}
	issues, err := store.ListOpenIssues()
	if err != nil {
		t.Fatal(err)
	}
	terms, err := store.LoadTerms()
	if err != nil {
		t.Fatal(err)
	}
	project := store.Project()
	return ProgressReportInput{
		ProjectName: project.TargetSystemName,
		Phase:       project.Phase,
		From:        at(2026, 8, 1, 0, 0),
		To:          at(2026, 8, 31, 23, 59),
		Now:         at(2026, 8, 31, 23, 59),
		Location:    jst,
		// 実プロジェクトの作成日時はテスト実行時刻になるため、フィクスチャの履歴
		//（7 月から動きがある）と整合する作成日時を与える。
		ProjectCreatedAt: at(2026, 7, 1, 10, 0),
		Records: Records{Requirements: reqs, Decisions: decisions,
			OpenIssues: issues, Terms: terms.Terms},
		Changes:      changes,
		Completeness: stubCompleteness,
	}
}

// 生成の前後でプロジェクトデータが変化しない（保存しない使い切りの出力）。
// 生成中に AI プロバイダへの送信が発生しない。
func TestProgressReportDoesNotTouchProjectData(t *testing.T) {
	store, changes := newReportStore(t)
	in := reportInputFrom(t, store, changes)

	before := hashTree(t, store.Root())
	sendsBefore, err := auditlog.ReadAISends(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	report, err := ProgressReport(in)
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	if !strings.Contains(report, "# 進捗レポート: 在庫管理システム") {
		t.Fatalf("レポートが組み立っていない:\n%s", report)
	}

	// 出力先はプロジェクトフォルダの外。
	dst := filepath.Join(t.TempDir(), "progress-report.md")
	if err := SaveProgressReport(dst, report); err != nil {
		t.Fatalf("出力に失敗: %v", err)
	}
	saved, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != report {
		t.Error("出力ファイルの内容がレポートと一致しない")
	}

	after := hashTree(t, store.Root())
	if diff := treeDiff(before, after); len(diff) != 0 {
		t.Errorf("プロジェクトデータが変化した: %v", diff)
	}
	sendsAfter, err := auditlog.ReadAISends(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sendsAfter) != len(sendsBefore) {
		t.Errorf("AI 送信記録が増えた（プロバイダを呼んでいる）: %d → %d", len(sendsBefore), len(sendsAfter))
	}
}

// 実データ・実履歴からの期首復元（章4）。
func TestProgressReportRestoresStartFromRealHistory(t *testing.T) {
	store, changes := newReportStore(t)
	in := reportInputFrom(t, store, changes)

	report, err := ProgressReport(in)
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, report, "## 4. 完成度")
	if strings.Contains(section, UnknownLabel) {
		t.Errorf("実履歴から復元できていない:\n%s", section)
	}
	// 期首（8/1）は DEC-001 のみ = 50%、期末は 8 月の決定が加わり 100%。
	if !strings.Contains(section, "| 背景・目的 | 50% | 100% |") {
		t.Errorf("期首値が違う:\n%s", section)
	}
	// 章6: 8/10 に付いたブロックは期末では有効（期首では未ブロック）。
	blocking := chapterOf(t, report, "## 6. 確定をブロックしている要因")
	if !strings.Contains(blocking, "ISS-001") || !strings.Contains(blocking, "FR-INV-001") {
		t.Errorf("ブロック要因が出ていない:\n%s", blocking)
	}
}

// 履歴ファイルを失った状態では期首値を「不明」と出力する（推定値で埋めない）。
func TestProgressReportUnknownWhenHistoryLost(t *testing.T) {
	store, changes := newReportStore(t)
	in := reportInputFrom(t, store, changes)
	in.Changes = nil // 履歴ファイルが失われた状態

	report, err := ProgressReport(in)
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, report, "## 4. 完成度")
	if !strings.Contains(section, UnknownLabel) {
		t.Errorf("履歴欠損なのに期首値が出た:\n%s", section)
	}
	if strings.Contains(section, "| 背景・目的 | 0% |") {
		t.Errorf("推定値で埋められた:\n%s", section)
	}
}

// hashTree はフォルダ内の全ファイルの内容ハッシュを返す。
func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("プロジェクトフォルダを走査できない: %v", err)
	}
	return out
}

func treeDiff(before, after map[string]string) []string {
	var diff []string
	for path, sum := range after {
		if before[path] != sum {
			diff = append(diff, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			diff = append(diff, path+"（消滅）")
		}
	}
	return diff
}
