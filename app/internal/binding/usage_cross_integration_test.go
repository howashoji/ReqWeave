//go:build integration

// 結合テスト（プロジェクト横断の利用量集計）。
// 実ファイル（project.yaml / members.yaml / ai-log）を読み、プロジェクトを開かずに集計する。

package binding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

var crossAuthor = projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"}

// makeProject は集計対象のプロジェクトを作る。tokens > 0 なら ai-log へ実績つきの送信を 1 件書く。
func makeProject(t *testing.T, name string, author projectstore.Author, tokens int, at time.Time) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: name, Author: author,
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	if tokens > 0 {
		rec := auditlog.AISendRecord{ID: "s-" + name, At: at, Author: author.AuthorID,
			Provider: "anthropic", Model: "claude-opus-5", Session: "S-0001", Prompt: "[user]\n質問"}
		rec.SetTokens(tokens, 0, 0)
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendFile(auditlog.FileName(auditlog.DirAILog, at, author.AuthorID),
			append(line, '\n')); err != nil {
			t.Fatalf("ai-log へ追記できない: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("プロジェクトを閉じられない: %v", err)
	}
	return root
}

func rowOf(t *testing.T, rows []ProjectUsageRow, path string) ProjectUsageRow {
	t.Helper()
	for _, r := range rows {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("行が見つからない: %s\n%+v", path, rows)
	return ProjectUsageRow{}
}

// 開けるプロジェクトだけを集計し、各行に合計・直近利用日時・上限と消費率を持つ。
func TestCrossProjectUsageListsOnlyMemberProjects(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	mine := makeProject(t, "自分のプロジェクト", crossAuthor, 300, at)
	other := makeProject(t, "他人のプロジェクト",
		projectstore.Author{AuthorID: "t.suzuki@example.co.jp", DisplayName: "鈴木"}, 900, at)

	// 上限を設定して消費率が出ることを見る。
	store, err := projectstore.Open(mine, crossAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SetUsageLimit(1000, nil); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	rows := crossProjectUsage([]string{mine, other}, crossAuthor, from, to)

	if len(rows) != 1 {
		t.Fatalf("メンバーでないプロジェクトが一覧に含まれている: %+v", rows)
	}
	got := rows[0]
	if got.Path != mine || got.TargetSystemName != "自分のプロジェクト" || got.ProjectID == "" {
		t.Errorf("遷移用の識別子・表示名が違う: %+v", got)
	}
	if !got.Aggregated || got.Tokens != 300 {
		t.Errorf("期間内の合計が違う: %+v", got)
	}
	if got.LastUsedAt != at.Format(time.RFC3339) {
		t.Errorf("直近の利用日時が違う: %q", got.LastUsedAt)
	}
	if got.LimitTokens == nil || *got.LimitTokens != 1000 {
		t.Errorf("上限値が違う: %+v", got.LimitTokens)
	}
	if got.ConsumptionRatio == nil || *got.ConsumptionRatio != 0.3 {
		t.Errorf("消費率が違う: %+v", got.ConsumptionRatio)
	}
}

// 上限未設定のプロジェクトは上限・消費率を持たない（0 と未設定を混同しない）。
func TestCrossProjectUsageWithoutLimit(t *testing.T) {
	root := makeProject(t, "上限なし", crossAuthor, 10, time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC))
	rows := crossProjectUsage([]string{root}, crossAuthor,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC))
	got := rowOf(t, rows, root)
	if got.LimitTokens != nil || got.ConsumptionRatio != nil {
		t.Errorf("上限未設定なのに上限・消費率が入っている: %+v", got)
	}
}

// 到達不能なプロジェクトは一覧から消さず「集計不能」として理由つきで示す。
func TestCrossProjectUsageKeepsUnreachableProjects(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	ok := makeProject(t, "到達できる", crossAuthor, 100, at)
	gone := filepath.Join(t.TempDir(), "共有フォルダ未接続")

	rows := crossProjectUsage([]string{gone, ok}, crossAuthor,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC))
	if len(rows) != 2 {
		t.Fatalf("到達不能なプロジェクトが一覧から消えた: %+v", rows)
	}
	bad := rowOf(t, rows, gone)
	if bad.Aggregated {
		t.Errorf("到達不能なのに集計できた扱いになっている: %+v", bad)
	}
	if bad.Notice == "" {
		t.Error("集計できない理由が示されていない")
	}
	if good := rowOf(t, rows, ok); !good.Aggregated || good.Tokens != 100 {
		t.Errorf("他の行の集計が完了していない: %+v", good)
	}
}

// 1 件の集計が時間内に終わらなくても全体の表示を止めない。
func TestCrossProjectUsageTimesOutOneProject(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	slow := makeProject(t, "遅いプロジェクト", crossAuthor, 100, at)
	fast := makeProject(t, "速いプロジェクト", crossAuthor, 200, at)

	origTimeout, origAggregate := crossUsageTimeout, aggregateProjectUsage
	t.Cleanup(func() { crossUsageTimeout, aggregateProjectUsage = origTimeout, origAggregate })
	crossUsageTimeout = 100 * time.Millisecond
	aggregateProjectUsage = func(root string, from, to time.Time) (auditlog.UsageSummary, error) {
		if root == slow {
			time.Sleep(2 * time.Second)
		}
		return origAggregate(root, from, to)
	}

	start := time.Now()
	rows := crossProjectUsage([]string{slow, fast}, crossAuthor,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC))
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("遅い 1 件が全体を止めている: %v", elapsed)
	}
	if len(rows) != 2 {
		t.Fatalf("行数が違う: %+v", rows)
	}
	if bad := rowOf(t, rows, slow); bad.Aggregated || bad.Notice == "" {
		t.Errorf("時間内に終わらない行が集計不能になっていない: %+v", bad)
	}
	if good := rowOf(t, rows, fast); !good.Aggregated || good.Tokens != 200 {
		t.Errorf("他の行の集計が完了していない: %+v", good)
	}
}

// 集計のためにプロジェクトを開かない（ロックを取らない）。
func TestCrossProjectUsageDoesNotOpenProjects(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	root := makeProject(t, "開かない", crossAuthor, 100, at)

	before := lockEntries(t, root)
	rows := crossProjectUsage([]string{root}, crossAuthor,
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC))
	if len(rows) != 1 || !rows[0].Aggregated {
		t.Fatalf("集計できていない: %+v", rows)
	}
	after := lockEntries(t, root)
	if len(after) != len(before) {
		t.Errorf("集計でロックの痕跡が残った: %v → %v", before, after)
	}
}

func lockEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "locks"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
