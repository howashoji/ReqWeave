package docgen

// 単体テスト（セッション統計）。実行: make -C app test-unit
//
// 進捗レポートと同一のフィクスチャ（reportFixture）を使い、同一期間の件数が一致することまで検証する。

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// statsFixture は進捗レポートのフィクスチャへ、統計側だけが使う動き
// （対話セッション・質問票の取込・要件の確定）を足したもの。データソースは共有する。
func statsFixture() (SessionStatsInput, ProgressReportInput) {
	r := reportFixture()
	r.Questionnaires = append(r.Questionnaires, projectstore.Questionnaire{
		ID: "QS-003", Addressee: "田中（購買部）", IssuedAt: at(2026, 7, 25, 10, 0),
		Status: projectstore.QuestionnaireImported,
	})
	r.Changes = append(r.Changes,
		// 期間内に取込済みへ移行（QS-002: 8/25 発行 → 8/28 取込 = 3 日）
		auditlog.ChangeRecord{At: at(2026, 8, 28, 10, 0), Target: "QS-002",
			Change: auditlog.ChangeStatusChanged,
			Before: projectstore.QuestionnaireAnswered, After: projectstore.QuestionnaireImported},
		// 発行は期間前・取込は期間内（QS-003: 7/25 発行 → 8/2 取込 = 8 日）
		auditlog.ChangeRecord{At: at(2026, 8, 2, 10, 0), Target: "QS-003",
			Change: auditlog.ChangeStatusChanged,
			Before: projectstore.QuestionnaireAnswered, After: projectstore.QuestionnaireImported},
		// 要件の確定（同一 ID が 2 回 = 差し戻し後の再確定。1 件として数える）
		auditlog.ChangeRecord{At: at(2026, 8, 22, 9, 0), Target: "FR-INV-001",
			Change: auditlog.ChangeStatusChanged,
			Before: projectstore.RequirementDraft, After: projectstore.RequirementAgreed},
		auditlog.ChangeRecord{At: at(2026, 8, 29, 9, 0), Target: "FR-INV-001",
			Change: auditlog.ChangeStatusChanged,
			Before: projectstore.RequirementDraft, After: projectstore.RequirementAgreed},
		// 期間外の確定（7/30）は数えない
		auditlog.ChangeRecord{At: at(2026, 7, 30, 9, 0), Target: "FR-INV-002",
			Change: auditlog.ChangeStatusChanged,
			Before: projectstore.RequirementDraft, After: projectstore.RequirementAgreed},
	)
	sessions := []projectstore.Session{
		{ID: "S-0001", Type: projectstore.SessionOwner, Phase: projectstore.PhaseRequirements,
			StartedAt: at(2026, 8, 2, 10, 0), Author: "k.sato@example.co.jp"},
		{ID: "S-0002", Type: projectstore.SessionOwner, Phase: projectstore.PhaseRequirements,
			StartedAt: at(2026, 8, 15, 10, 0), Author: "k.sato@example.co.jp"},
		{ID: "S-0003", Type: projectstore.SessionOwner, Phase: projectstore.PhaseBasicDesign,
			StartedAt: at(2026, 8, 20, 10, 0), Author: "t.suzuki@example.co.jp"},
		// 期間外（7/20）
		{ID: "S-0004", Type: projectstore.SessionOwner, Phase: projectstore.PhaseRequirements,
			StartedAt: at(2026, 7, 20, 10, 0), Author: "k.sato@example.co.jp"},
	}
	return SessionStatsInput{
		From: r.From, To: r.To,
		Sessions:       sessions,
		Questionnaires: r.Questionnaires,
		Records:        r.Records,
		Changes:        r.Changes,
	}, r
}

// 期間内の各項目が数えられる（期待値はフィクスチャから手で導出したもの）。
func TestSessionStatisticsCountsAllAxes(t *testing.T) {
	in, _ := statsFixture()
	got, err := SessionStatistics(in)
	if err != nil {
		t.Fatalf("統計の組立てに失敗: %v", err)
	}

	if got.SessionsTotal != 3 {
		t.Errorf("対話セッション数が違う: got %d, want 3（8/2・8/15・8/20。7/20 は期間外）", got.SessionsTotal)
	}
	wantPhase := []PhaseSessionCount{
		{Phase: projectstore.PhaseRequirements, Count: 2},
		{Phase: projectstore.PhaseBasicDesign, Count: 1},
	}
	if len(got.SessionsByPhase) != len(wantPhase) {
		t.Fatalf("フェーズ別の行数が違う: %+v", got.SessionsByPhase)
	}
	for i := range wantPhase {
		if got.SessionsByPhase[i] != wantPhase[i] {
			t.Errorf("フェーズ別 %d 行目が違う: got %+v, want %+v", i+1, got.SessionsByPhase[i], wantPhase[i])
		}
	}

	if got.QuestionnairesIssued != 2 {
		t.Errorf("質問票の発行数が違う: got %d, want 2（QS-001・QS-002。QS-003 は 7/25 発行）", got.QuestionnairesIssued)
	}
	if got.QuestionnairesImported != 2 {
		t.Errorf("質問票の取込数が違う: got %d, want 2（QS-003 = 8/2・QS-002 = 8/28）", got.QuestionnairesImported)
	}
	// 取込日時の昇順: QS-003（8/2・8 日）→ QS-002（8/28・3 日）
	if len(got.ImportedFlows) != 2 ||
		got.ImportedFlows[0].ID != "QS-003" || got.ImportedFlows[0].ElapsedDays != 8 ||
		got.ImportedFlows[1].ID != "QS-002" || got.ImportedFlows[1].ElapsedDays != 3 {
		t.Errorf("発行→取込の経過が違う: %+v", got.ImportedFlows)
	}
	if got.ElapsedDaysAverage != 5.5 {
		t.Errorf("平均経過日数が違う: got %v, want 5.5（(8+3)/2）", got.ElapsedDaysAverage)
	}

	if got.DecisionsApproved != 2 {
		t.Errorf("承認された決定事項数が違う: got %d, want 2（DEC-002・DEC-003）", got.DecisionsApproved)
	}
	if got.OpenIssuesResolved != 1 {
		t.Errorf("決着した未決事項数が違う: got %d, want 1（ISS-002）", got.OpenIssuesResolved)
	}
	if got.RequirementsConfirmed != 1 {
		t.Errorf("確定した要件項目数が違う: got %d, want 1（FR-INV-001 の 2 回を 1 件として数える）",
			got.RequirementsConfirmed)
	}
}

// 同一期間の集計値が進捗レポートの件数と一致すること
// （同一の計数関数を両者が呼ぶ。計数規則を変えると本テストと進捗レポートのテストが同時に落ちる）。
func TestSessionStatisticsMatchesProgressReportCounts(t *testing.T) {
	in, reportIn := statsFixture()
	stats, err := SessionStatistics(in)
	if err != nil {
		t.Fatalf("統計の組立てに失敗: %v", err)
	}
	report, err := ProgressReport(reportIn)
	if err != nil {
		t.Fatalf("進捗レポートの組立てに失敗: %v", err)
	}

	if got := countIn(t, report, `期間内に承認した決定事項: (\d+) 件`); got != stats.DecisionsApproved {
		t.Errorf("決定事項数が進捗レポートと一致しない: レポート %d / 統計 %d", got, stats.DecisionsApproved)
	}
	if got := countIn(t, report, `新規に起票: \d+ 件 ／ 決着: (\d+) 件`); got != stats.OpenIssuesResolved {
		t.Errorf("決着した未決事項数が進捗レポートと一致しない: レポート %d / 統計 %d", got, stats.OpenIssuesResolved)
	}
}

// countIn はレポート本文から件数を 1 つ取り出す。
func countIn(t *testing.T, report, pattern string) int {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("レポートに %q が見つからない", pattern)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("件数を解釈できない: %v", err)
	}
	return n
}

// 期間の境界（JST）を含み、境界外は含まない。
// 保存は UTC・指定はローカル暦日のため、9 時間ずれる書き方をしていないことを固定する。
func TestSessionStatisticsPeriodBoundaryIsJSTCalendarDay(t *testing.T) {
	from := at(2026, 8, 1, 0, 0)  // JST 8/1 0:00
	to := at(2026, 8, 31, 23, 59) // JST 8/31 23:59
	sessions := []projectstore.Session{
		{ID: "S-0001", Phase: projectstore.PhaseRequirements, StartedAt: from.Add(-time.Minute)},
		{ID: "S-0002", Phase: projectstore.PhaseRequirements, StartedAt: from},
		// JST 8/1 8:59 / 9:00（UTC 換算で日付が変わる時刻。どちらも期間内）
		{ID: "S-0003", Phase: projectstore.PhaseRequirements, StartedAt: at(2026, 8, 1, 8, 59)},
		{ID: "S-0004", Phase: projectstore.PhaseRequirements, StartedAt: at(2026, 8, 1, 9, 0)},
		{ID: "S-0005", Phase: projectstore.PhaseRequirements, StartedAt: to},
		{ID: "S-0006", Phase: projectstore.PhaseRequirements, StartedAt: to.Add(time.Minute)},
	}
	got, err := SessionStatistics(SessionStatsInput{From: from, To: to, Sessions: sessions})
	if err != nil {
		t.Fatalf("統計の組立てに失敗: %v", err)
	}
	if got.SessionsTotal != 4 {
		t.Errorf("境界の扱いが違う: got %d, want 4（S-0002〜S-0005 のみ）", got.SessionsTotal)
	}
}

// 期間の指定が無い・逆転しているときは、理由つきで拒否する（進捗レポートと同じ検証）。
func TestSessionStatisticsRejectsInvalidPeriod(t *testing.T) {
	in, _ := statsFixture()
	for name, mutate := range map[string]func(*SessionStatsInput){
		"開始日なし": func(i *SessionStatsInput) { i.From = time.Time{} },
		"終了日なし": func(i *SessionStatsInput) { i.To = time.Time{} },
		"期間が逆転": func(i *SessionStatsInput) { i.From, i.To = i.To, i.From },
	} {
		bad := in
		mutate(&bad)
		if _, err := SessionStatistics(bad); err == nil {
			t.Errorf("%s: 拒否されなかった", name)
		}
	}
}

// 取込 0 件のとき平均は 0（0 除算しない）。
func TestSessionStatisticsWithoutImportedQuestionnaires(t *testing.T) {
	got, err := SessionStatistics(SessionStatsInput{
		From: at(2026, 8, 1, 0, 0), To: at(2026, 8, 31, 23, 59),
	})
	if err != nil {
		t.Fatalf("統計の組立てに失敗: %v", err)
	}
	if got.QuestionnairesImported != 0 || got.ElapsedDaysAverage != 0 || len(got.ImportedFlows) != 0 {
		t.Errorf("取込 0 件の扱いが違う: %+v", got)
	}
}
