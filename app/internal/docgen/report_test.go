package docgen

// 単体テスト（進捗レポートの組立て）。実行: make -C app test-unit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// jst は表示・フィクスチャに使う日本標準時（境界値の固定用）。
var jst = time.FixedZone("JST", 9*60*60)

// at は JST の時刻を UTC の time.Time として返す（保存値は UTC）。
func at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, jst).UTC()
}

// stubCompleteness は章観点 2 件ぶんの充足率を返す（判定条件の正本は対話エンジン側）。
//
// レコードの件数だけで単調に増える単純な算出にしてあり、期首（復元後）と期末で
// 値が変わることをテストから確認できる。
func stubCompleteness(_ string, r Records) ([]ChapterProgress, error) {
	decided := 0
	for _, d := range r.Decisions {
		if d.SupersededBy == "" {
			decided++
		}
	}
	return []ChapterProgress{
		{ChapterID: "background", Name: "背景・目的", Percent: 50 * min(decided, 2), Satisfied: min(decided, 2), Total: 2},
		{ChapterID: "functional-requirements", Name: "機能要件",
			Percent: 100 * min(len(r.Requirements), 1), Satisfied: min(len(r.Requirements), 1), Total: 1},
	}, nil
}

// reportFixture は 8 月を対象期間とする 1 か月ぶんの動きを持つ入力を作る。
func reportFixture() ProgressReportInput {
	return ProgressReportInput{
		ProjectName:      "在庫管理システム",
		Phase:            projectstore.PhaseRequirements,
		From:             at(2026, 8, 1, 0, 0),
		To:               at(2026, 8, 31, 23, 59),
		Now:              at(2026, 8, 31, 23, 59),
		Location:         jst,
		ProjectCreatedAt: at(2026, 7, 1, 10, 0),
		Completeness:     stubCompleteness,
		Records: Records{
			Decisions: []projectstore.Decision{
				{ID: "DEC-001", TopicKey: "background/purpose", Body: "現行の在庫管理を刷新する。",
					DecidedAt: at(2026, 7, 10, 10, 0)},
				{ID: "DEC-002", TopicKey: "background/scope", Body: "対象は国内倉庫のみとする。",
					DecidedAt: at(2026, 8, 5, 14, 0)},
				{ID: "DEC-003", TopicKey: "business-flow/main-flow", Body: "受注確定時に引き当てる。",
					DecidedAt: at(2026, 8, 20, 9, 0)},
			},
			OpenIssues: []projectstore.OpenIssue{
				{ID: "ISS-001", Owner: "倉庫長", Due: "2026-09-30",
					Status: projectstore.OpenIssueOpen, Body: "棚卸差異の承認者を決める"},
				{ID: "ISS-002", Owner: "情報システム部", Status: projectstore.OpenIssueResolved,
					Body: "既存システムとの連携範囲を決める", ResolvedBy: "DEC-003"},
			},
			Requirements: []projectstore.Requirement{
				{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
					Status: projectstore.RequirementDraft, BlockedBy: []string{"ISS-001"}},
			},
			Terms: []projectstore.Term{{Name: "引当", NameEn: "allocation", Definition: "受注に在庫を割り当てること"}},
		},
		Changes: []auditlog.ChangeRecord{
			{At: at(2026, 7, 10, 10, 0), Target: "DEC-001", Change: auditlog.ChangeCreated, After: "現行の在庫管理を刷新する。"},
			{At: at(2026, 7, 12, 10, 0), Target: "引当", Change: auditlog.ChangeUpdated, After: "受注に在庫を割り当てること"},
			{At: at(2026, 7, 15, 9, 0), Target: "ISS-002", Change: auditlog.ChangeCreated, After: "既存システムとの連携範囲を決める"},
			{At: at(2026, 7, 16, 9, 0), Target: "FR-INV-001", Change: auditlog.ChangeCreated, After: "在庫引当"},
			{At: at(2026, 8, 5, 14, 0), Target: "DEC-002", Change: auditlog.ChangeCreated, After: "対象は国内倉庫のみとする。"},
			{At: at(2026, 8, 10, 11, 0), Target: "ISS-001", Change: auditlog.ChangeCreated, After: "棚卸差異の承認者を決める"},
			{At: at(2026, 8, 10, 11, 0), Target: "FR-INV-001", Change: auditlog.ChangeUpdated, After: "blocked_by: ISS-001"},
			{At: at(2026, 8, 20, 9, 0), Target: "DEC-003", Change: auditlog.ChangeCreated, After: "受注確定時に引き当てる。"},
			{At: at(2026, 8, 20, 9, 0), Target: "ISS-002", Change: auditlog.ChangeStatusChanged,
				Before: projectstore.OpenIssueOpen, After: projectstore.OpenIssueResolved, Evidence: "DEC-003"},
		},
		Questionnaires: []projectstore.Questionnaire{
			{ID: "QS-001", Addressee: "佐藤（営業部）", IssuedAt: at(2026, 8, 21, 10, 0),
				Status: projectstore.QuestionnaireIssued},
			{ID: "QS-002", Addressee: "鈴木（物流部）", IssuedAt: at(2026, 8, 25, 10, 0),
				Status: projectstore.QuestionnaireImported},
		},
	}
}

// 期間を指定すると 7 章（該当時）が組み立つ。
func TestProgressReportBuildsChapters(t *testing.T) {
	in := reportFixture()
	in.Feedback = &importer.FeedbackSummary{Total: 2,
		Counts: []importer.ClassificationCount{{Label: "要件の欠落", Count: 2}}}

	got, err := ProgressReport(in)
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	for _, want := range []string{
		"# 進捗レポート: 在庫管理システム",
		"## 1. 要約", "## 2. 決定事項", "## 3. 未決事項の動き", "## 4. 完成度",
		"## 5. 回答待ち質問票", "## 6. 確定をブロックしている要因",
		"## 7. " + FeedbackChapterTitle,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("章が組み立っていない: %q\n%s", want, got)
		}
	}
	// 要約は各章の集計値を 1 段落で述べる（上長への報告にそのまま使えるように）。
	for _, want := range []string{"決定事項は 2 件", "未決事項は 1 件を新規に起票し、1 件が決着",
		"確定をブロックしている未決事項は 1 件"} {
		if !strings.Contains(got, want) {
			t.Errorf("要約に %q がない:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "対象期間: 2026-08-01 〜 2026-08-31") {
		t.Errorf("対象期間が表示されていない:\n%s", got)
	}
	if !strings.Contains(got, "フェーズ: 要件定義") {
		t.Errorf("フェーズが表示されていない:\n%s", got)
	}
}

// フィードバックが 0 件の期間は章7 を出さない。
func TestProgressReportOmitsEmptyFeedbackChapter(t *testing.T) {
	in := reportFixture()
	got, err := ProgressReport(in) // Feedback は nil
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, FeedbackChapterTitle) || strings.Contains(got, "## 7.") {
		t.Errorf("フィードバック 0 件の期間で章7 が出力された:\n%s", got)
	}
	in.Feedback = &importer.FeedbackSummary{}
	got, err = ProgressReport(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "## 7.") {
		t.Errorf("集計 0 件で章7 が出力された:\n%s", got)
	}
}

// 決定事項は decided_at で期間抽出する（期間外の決定を含めない）。
func TestProgressReportFiltersDecisionsByPeriod(t *testing.T) {
	got, err := ProgressReport(reportFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "| DEC-002 |") || !strings.Contains(got, "| DEC-003 |") {
		t.Errorf("期間内の決定事項が出ていない:\n%s", got)
	}
	if strings.Contains(got, "| DEC-001 |") {
		t.Errorf("期間外（7 月）の決定事項が含まれた:\n%s", got)
	}
	if !strings.Contains(got, "| DEC-002 | 対象は国内倉庫のみとする。 | 2026-08-05 |") {
		t.Errorf("決定日・本文 1 行が出ていない:\n%s", got)
	}
}

// 期間判定が TZ 依存でないこと（JST 0:00 / 8:59 / 9:00 の境界値で固定）。
//
// 保存値は UTC。同じ瞬間を UTC で表しても JST で表しても判定は変わらない。
func TestProgressReportPeriodBoundariesAreTimeZoneIndependent(t *testing.T) {
	// 期間は JST の 8/1 0:00 〜 8/31 23:59（= UTC の 7/31 15:00 〜 8/31 14:59）。
	base := reportFixture()
	boundary := []struct {
		id   string
		at   time.Time
		want bool
	}{
		{"DEC-101", at(2026, 7, 31, 23, 59), false}, // 期間開始の 1 分前（JST）
		{"DEC-102", at(2026, 8, 1, 0, 0), true},     // 期間開始ちょうど（JST 0:00）
		{"DEC-103", at(2026, 8, 1, 8, 59), true},    // JST 8:59（UTC では前日 23:59）
		{"DEC-104", at(2026, 8, 1, 9, 0), true},     // JST 9:00（UTC では同日 0:00）
		{"DEC-105", at(2026, 8, 31, 23, 59), true},  // 期間終了ちょうど
		{"DEC-106", at(2026, 9, 1, 0, 0), false},    // 期間終了の 1 分後
	}
	var decisions []projectstore.Decision
	for _, b := range boundary {
		decisions = append(decisions, projectstore.Decision{ID: b.id, TopicKey: "background/purpose",
			Body: "境界値の決定", DecidedAt: b.at})
	}

	// 表示タイムゾーンを変えても抽出結果は同じ（判定は絶対時刻で行う）。
	for _, loc := range []*time.Location{jst, time.UTC} {
		in := base
		in.Location = loc
		in.Records.Decisions = decisions
		got, err := ProgressReport(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range boundary {
			if strings.Contains(got, "| "+b.id+" |") != b.want {
				t.Errorf("%s（%s / 表示 %s）の期間判定が違う（期待 %v）",
					b.id, b.at.In(jst).Format(time.RFC3339), loc, b.want)
			}
		}
	}
}

// 未決事項の動きは変更履歴（起票・決着）から判定する。
func TestProgressReportOpenIssueMovement(t *testing.T) {
	got, err := ProgressReport(reportFixture())
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, got, "## 3. 未決事項の動き")
	if !strings.Contains(section, "新規に起票: 1 件 ／ 決着: 1 件") {
		t.Errorf("件数が違う:\n%s", section)
	}
	if !strings.Contains(section, "| ISS-001 | 棚卸差異の承認者を決める | 倉庫長 | 2026-09-30 | 未決 |") {
		t.Errorf("新規起票の行が違う:\n%s", section)
	}
	if !strings.Contains(section, "| ISS-002 | 既存システムとの連携範囲を決める | 情報システム部 | 未設定 | 決着済み |") {
		t.Errorf("決着の行が違う:\n%s", section)
	}
}

// 完成度の期首値は変更履歴から機械復元する（期首＝期間開始時点の状態）。
func TestProgressReportRestoresStartCompleteness(t *testing.T) {
	got, err := ProgressReport(reportFixture())
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, got, "## 4. 完成度")
	// 期首（8/1 時点）は DEC-001 のみ = 50%、期末は DEC-002/003 が加わり 100%。
	if !strings.Contains(section, "| 背景・目的 | 50% | 100% |") {
		t.Errorf("期首値が復元されていない:\n%s", section)
	}
	if !strings.Contains(section, "| 機能要件 | 100% | 100% |") {
		t.Errorf("期首に存在した要件項目が落ちている:\n%s", section)
	}
	if strings.Contains(section, UnknownLabel) {
		t.Errorf("復元できるのに不明と出力された:\n%s", section)
	}
	if !strings.Contains(got, "章観点の充足率（全体）は 66% → 100% です") {
		t.Errorf("要約の期首→期末が違う:\n%s", got)
	}
}

// 履歴が欠損して復元できない期間は「不明」と出力し、推定値で埋めない。
func TestProgressReportMarksUnrestorableStartAsUnknown(t *testing.T) {
	in := reportFixture()
	// DEC-001 の作成記録だけが失われた履歴（他は残っている）。
	var changes []auditlog.ChangeRecord
	for _, c := range in.Changes {
		if c.Target == "DEC-001" {
			continue
		}
		changes = append(changes, c)
	}
	in.Changes = changes

	got, err := ProgressReport(in)
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, got, "## 4. 完成度")
	if !strings.Contains(section, "| 背景・目的 | 不明 | 100% |") {
		t.Errorf("復元不能な期首値が不明になっていない:\n%s", section)
	}
	if !strings.Contains(section, "復元できませんでした") {
		t.Errorf("復元できなかった理由が示されていない:\n%s", section)
	}
	if !strings.Contains(got, "充足率（全体）は 不明 → 100% です") {
		t.Errorf("要約でも不明と示されていない:\n%s", got)
	}
	// 期首 0% 等の推定値で埋めない。
	if strings.Contains(section, "| 背景・目的 | 0% |") {
		t.Errorf("推定値で埋められた:\n%s", section)
	}
}

// 期間開始がプロジェクト作成以前なら、期首は「レコードなし」と確定できる。
func TestProgressReportStartBeforeProjectCreation(t *testing.T) {
	in := reportFixture()
	in.From = at(2026, 6, 1, 0, 0)
	in.ProjectCreatedAt = at(2026, 7, 1, 10, 0)

	got, err := ProgressReport(in)
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, got, "## 4. 完成度")
	if strings.Contains(section, UnknownLabel) {
		t.Errorf("作成前を起点にした期首が不明になった:\n%s", section)
	}
	if !strings.Contains(section, "| 背景・目的 | 0% | 100% |") {
		t.Errorf("期首がレコードなしとして算出されていない:\n%s", section)
	}
}

// 回答待ち質問票は issued のもののみ、宛先・発行日・経過日数つきで列挙する。
func TestProgressReportWaitingQuestionnaires(t *testing.T) {
	got, err := ProgressReport(reportFixture())
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, got, "## 5. 回答待ち質問票")
	if !strings.Contains(section, "| QS-001 | 佐藤（営業部） | 2026-08-21 | 10 日 |") {
		t.Errorf("回答待ちの行が違う:\n%s", section)
	}
	if strings.Contains(section, "QS-002") {
		t.Errorf("取込済みの質問票が回答待ちに含まれた:\n%s", section)
	}
}

// 確定をブロックしている要因は未決事項の状態と要件項目の blocked_by で判定する。
func TestProgressReportBlockingChapter(t *testing.T) {
	got, err := ProgressReport(reportFixture())
	if err != nil {
		t.Fatal(err)
	}
	section := chapterOf(t, got, "## 6. 確定をブロックしている要因")
	if !strings.Contains(section, "| ISS-001 | 棚卸差異の承認者を決める | 倉庫長 | 2026-09-30 | FR-INV-001 |") {
		t.Errorf("ブロック要因の行が違う:\n%s", section)
	}
	if strings.Contains(section, "ISS-002") {
		t.Errorf("決着済みの未決事項がブロック要因に含まれた:\n%s", section)
	}
}

// 成果物の形式: 改行 LF・見出しは 3 階層以内・表は Markdown。
func TestProgressReportFollowsFormatRules(t *testing.T) {
	in := reportFixture()
	in.Feedback = &importer.FeedbackSummary{Total: 1,
		Counts: []importer.ClassificationCount{{Label: "要件の欠落", Count: 1}}}
	got, err := ProgressReport(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "\r") {
		t.Error("CRLF が含まれている（成果物の改行は LF）")
	}
	heading := regexp.MustCompile(`(?m)^(#+) `)
	for _, m := range heading.FindAllStringSubmatch(got, -1) {
		if len(m[1]) > 3 {
			t.Errorf("見出しが 3 階層を超えた: %q", m[0])
		}
	}
	if !strings.HasPrefix(got, "# 進捗レポート") {
		t.Errorf("先頭が文書見出しでない: %q", firstLine(got))
	}
}

// 入力の検証（期間・対象システム名・算出関数）。
func TestProgressReportRejectsInvalidInput(t *testing.T) {
	base := reportFixture()

	noName := base
	noName.ProjectName = " "
	if _, err := ProgressReport(noName); err == nil {
		t.Error("対象システム名なしが受理された")
	}
	noPeriod := base
	noPeriod.From = time.Time{}
	if _, err := ProgressReport(noPeriod); err == nil {
		t.Error("期間なしが受理された")
	}
	reversed := base
	reversed.From, reversed.To = base.To, base.From
	if _, err := ProgressReport(reversed); err == nil {
		t.Error("開始より前に終わる期間が受理された")
	}
	noFunc := base
	noFunc.Completeness = nil
	if _, err := ProgressReport(noFunc); err == nil {
		t.Error("充足率の算出方法なしが受理された")
	}
}

// Markdown ファイルへ出力できる（UTF-8・LF）。
func TestSaveProgressReport(t *testing.T) {
	content, err := ProgressReport(reportFixture())
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out", "progress-report.md")
	if err := SaveProgressReport(dst, content); err != nil {
		t.Fatalf("出力に失敗: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("出力ファイルを読めない: %v", err)
	}
	if string(data) != content {
		t.Errorf("出力内容がレポートと一致しない")
	}
	if strings.Contains(string(data), "\r\n") {
		t.Error("CRLF で書き出された")
	}

	// 途中経過の一時ファイルを残さない。
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".progress-report-") {
			t.Errorf("一時ファイルが残っている: %s", e.Name())
		}
	}

	if err := SaveProgressReport("", content); err == nil {
		t.Error("出力先なしが受理された")
	}
	if err := SaveProgressReport(dst, " "); err == nil {
		t.Error("空のレポートが出力された")
	}
}

// chapterOf は章見出しから次の章見出しまでを切り出す。
func chapterOf(t *testing.T, report, heading string) string {
	t.Helper()
	i := strings.Index(report, heading)
	if i < 0 {
		t.Fatalf("章が見つからない: %q\n%s", heading, report)
	}
	rest := report[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return heading + rest[:j]
	}
	return heading + rest
}

// 期首状態の復元そのものの検証（章4）。
//
// 復元できるのは変更履歴が持つ「作成」と「状態変化」であり、期間内の動きを巻き戻す。
func TestRestoreRecordsAt(t *testing.T) {
	start := at(2026, 8, 1, 0, 0)
	created := at(2026, 7, 20, 10, 0)
	cur := Records{
		Decisions: []projectstore.Decision{
			{ID: "DEC-001", TopicKey: "background/purpose", Body: "古い決定", SupersededBy: "DEC-002"},
			{ID: "DEC-002", TopicKey: "background/purpose", Body: "新しい決定", Supersedes: "DEC-001"},
		},
		OpenIssues: []projectstore.OpenIssue{
			{ID: "ISS-001", Owner: "倉庫長", Status: projectstore.OpenIssueResolved, Body: "期間内に決着した論点"},
		},
		Requirements: []projectstore.Requirement{
			{ID: "FR-INV-001", Title: "在庫引当", Chapter: "functional-requirements",
				Status: projectstore.RequirementAgreed, BlockedBy: []string{"ISS-001"}},
		},
		Terms: []projectstore.Term{{Name: "引当", Definition: "在庫の確保"}},
	}
	changes := []auditlog.ChangeRecord{
		{At: created, Target: "DEC-001", Change: auditlog.ChangeCreated},
		{At: created, Target: "ISS-001", Change: auditlog.ChangeCreated},
		{At: created, Target: "FR-INV-001", Change: auditlog.ChangeCreated},
		{At: created, Target: "引当", Change: auditlog.ChangeUpdated},
		// 以下は期間内（8 月）の動き。期首では巻き戻す。
		{At: at(2026, 8, 12, 10, 0), Target: "DEC-002", Change: auditlog.ChangeCreated},
		{At: at(2026, 8, 12, 10, 0), Target: "DEC-001", Change: auditlog.ChangeUpdated, After: "superseded_by: DEC-002"},
		{At: at(2026, 8, 12, 10, 0), Target: "ISS-001", Change: auditlog.ChangeStatusChanged,
			Before: projectstore.OpenIssueOpen, After: projectstore.OpenIssueResolved},
		{At: at(2026, 8, 13, 10, 0), Target: "FR-INV-001", Change: auditlog.ChangeStatusChanged,
			Before: projectstore.RequirementDraft, After: projectstore.RequirementAgreed},
		{At: at(2026, 8, 13, 10, 0), Target: "FR-INV-001", Change: auditlog.ChangeUpdated, After: "blocked_by: ISS-001"},
	}

	got, ok := restoreRecordsAt(cur, changes, start, at(2026, 7, 1, 10, 0))
	if !ok {
		t.Fatal("復元できるはずの履歴で不明になった")
	}
	if len(got.Decisions) != 1 || got.Decisions[0].ID != "DEC-001" {
		t.Fatalf("期間内に作られた決定が期首に残った: %+v", got.Decisions)
	}
	if got.Decisions[0].SupersededBy != "" {
		t.Errorf("期間内の置き換えが期首へ巻き戻っていない: %+v", got.Decisions[0])
	}
	if len(got.OpenIssues) != 1 || got.OpenIssues[0].Status != projectstore.OpenIssueOpen {
		t.Errorf("期間内の決着が期首へ巻き戻っていない: %+v", got.OpenIssues)
	}
	if len(got.Requirements) != 1 || got.Requirements[0].Status != projectstore.RequirementDraft {
		t.Errorf("期間内の状態変化が期首へ巻き戻っていない: %+v", got.Requirements)
	}
	if len(got.Requirements[0].BlockedBy) != 0 {
		t.Errorf("期間内に付いたブロックが期首に残った: %+v", got.Requirements[0].BlockedBy)
	}
	if len(got.Terms) != 1 {
		t.Errorf("期首に存在した用語が落ちた: %+v", got.Terms)
	}
	// 現存レコードの記録が履歴に無ければ復元できない（推定しない）。
	if _, ok := restoreRecordsAt(cur, changes[1:], start, at(2026, 7, 1, 10, 0)); ok {
		t.Error("作成記録が欠けた履歴で復元できたことになっている")
	}
	// 変更前の値が無い状態変化からは復元できない。
	broken := append([]auditlog.ChangeRecord(nil), changes...)
	for i := range broken {
		if broken[i].Target == "ISS-001" && broken[i].Change == auditlog.ChangeStatusChanged {
			broken[i].Before = ""
		}
	}
	if _, ok := restoreRecordsAt(cur, broken, start, at(2026, 7, 1, 10, 0)); ok {
		t.Error("変更前の値が無い履歴で復元できたことになっている")
	}
}
