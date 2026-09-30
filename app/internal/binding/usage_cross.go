package binding

// 本ファイルはプロジェクト横断の AI 利用量集計。
//
// 範囲はアプリ設定の既知プロジェクト一覧（アプリ設定の recent_projects。プロジェクト一覧の画面と同じ）。
// 各プロジェクトについてメンバー照合を行い、**開ける（メンバー登録がある）ものだけ**を
// 集計する。到達不能なプロジェクトは一覧から消さず「集計不能」として理由つきで示す（黙って消さない）。
//
// 集計のためにプロジェクトを**開かない**（ロックを取らない・locks/ に痕跡を残さない）。
// 一覧はファイルを直接読むだけで組み立てる（派生インデックスの遅延読込と同じ方針）。
// サーバー側の集計基盤は持たない（データを端末の外で預からない方針のため。組織全体の合算は各担当者の Markdown 出力を突き合わせる運用）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// crossUsageTimeout は 1 プロジェクトの集計を待つ上限（到達不能なプロジェクトで全体の表示を止めない）。テストのみが差し替える。
var crossUsageTimeout = 10 * time.Second

// aggregateProjectUsage は 1 プロジェクトの期間集計。テストのみが差し替える（遅延の再現）。
var aggregateProjectUsage = auditlog.AggregateUsage

// ProjectUsageRow はプロジェクト横断一覧の 1 行。
type ProjectUsageRow struct {
	// Path は単体表示へ遷移するための識別子。
	Path             string `json:"path"`
	ProjectID        string `json:"projectId"`
	TargetSystemName string `json:"targetSystemName"`

	// Aggregated は集計できたか。false のときは Notice に理由が入る（行は消さない）。
	Aggregated bool   `json:"aggregated"`
	Notice     string `json:"notice,omitempty"`

	// Tokens は期間内のトークン消費合計。MissingRecords は実績を持たない送信の件数。
	Tokens         int `json:"tokens"`
	MissingRecords int `json:"missingRecords"`
	// LastUsedAt は期間内で最後に AI を呼び出した日時（RFC3339。無ければ空）。
	LastUsedAt string `json:"lastUsedAt,omitempty"`

	// LimitTokens / ConsumptionRatio は上限設定時のみ（未設定なら nil）。
	LimitTokens      *int     `json:"limitTokens"`
	ConsumptionRatio *float64 `json:"consumptionRatio"`
}

// crossProjectUsage は既知プロジェクトの横断一覧を組み立てる。
//
// 集計は並行に行い、1 件が遅くても全体を止めない（遅い行は「集計不能」として返す）。
func crossProjectUsage(paths []string, author projectstore.Author, from, to time.Time) []ProjectUsageRow {
	type slot struct {
		row     ProjectUsageRow
		include bool
	}
	slots := make([]slot, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			row, include := projectUsageRow(path, author, from, to)
			slots[i] = slot{row, include}
		}(i, path)
	}
	wg.Wait()

	out := make([]ProjectUsageRow, 0, len(paths))
	for _, s := range slots {
		if s.include {
			out = append(out, s.row)
		}
	}
	return out
}

// projectUsageRow は 1 プロジェクトの行を組み立てる（プロジェクトを開かない）。
// 2 つ目の戻り値が false のとき、その行は一覧に含めない（メンバー登録が無い = 開けないプロジェクト）。
func projectUsageRow(path string, author projectstore.Author, from, to time.Time) (ProjectUsageRow, bool) {
	row := ProjectUsageRow{Path: path}

	data, err := os.ReadFile(filepath.Join(path, projectstore.FileProject))
	if err != nil {
		row.Notice = "このプロジェクトを読み込めないため集計できません。フォルダの場所と共有フォルダの接続を確認してください。"
		return row, true
	}
	project, err := projectstore.UnmarshalProject(data)
	if err != nil {
		row.Notice = "プロジェクトデータが読み込めないため集計できません。自動退避からの復元を実行してください。"
		return row, true
	}
	row.ProjectID = project.ProjectID
	row.TargetSystemName = project.TargetSystemName

	// メンバー照合。開けないプロジェクトは集計対象にしない。
	members, err := loadMembers(path)
	if err != nil {
		row.Notice = "メンバー一覧を読み込めないため集計できません。共有フォルダの接続を確認してください。"
		return row, true
	}
	if _, ok := members.Find(author.AuthorID); !ok {
		return ProjectUsageRow{}, false
	}

	summary, err := aggregateWithTimeout(path, from, to)
	if err != nil {
		row.Notice = err.Error()
		return row, true
	}
	row.Aggregated = true
	row.Tokens = summary.Tokens.Total
	row.MissingRecords = summary.Missing
	if !summary.LastUsedAt.IsZero() {
		row.LastUsedAt = summary.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if limit := project.UsageLimit; limit != nil {
		max := limit.TokensMax
		row.LimitTokens = &max
		ratio := float64(row.Tokens) / float64(max)
		row.ConsumptionRatio = &ratio
	}
	return row, true
}

// aggregateWithTimeout は 1 プロジェクトの集計を待つ。時間内に終わらなければ集計不能として扱う
// （読み出し自体は中断できないが、待つのをやめて全体の表示を進める）。
func aggregateWithTimeout(path string, from, to time.Time) (auditlog.UsageSummary, error) {
	type result struct {
		summary auditlog.UsageSummary
		err     error
	}
	done := make(chan result, 1)
	go func() {
		// ロガーを引数で受け取らない位置のため既定のロガーを使う（applog.SetDefault = main）。
		// 記録なしでプロセスが落ちるのを避けるためで、挙動は変えない（記録後に再送出する）。
		defer applog.RecoverPanic(applog.EventAppPanic)
		s, err := aggregateProjectUsage(path, from, to)
		done <- result{s, err}
	}()
	timer := time.NewTimer(crossUsageTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			return auditlog.UsageSummary{}, fmt.Errorf("利用量の記録を読み込めないため集計できません。共有フォルダの接続を確認してください。")
		}
		return r.summary, nil
	case <-timer.C:
		return auditlog.UsageSummary{}, fmt.Errorf("時間内に集計できませんでした。時間をおいて再表示してください。")
	}
}
