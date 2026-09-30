package binding

// 本ファイルは同期の記録の保存と参照。
//
// 記録の実体は `audit/sync-log/YYYY-MM.<author>.ndjson`（ai-log / history と同一の追記方式）。
// **同期モジュールは監査記録を書かない**分担のため（版番号の再採番 = versionrenumber.go・
// 三面マージ = mergeaudit.go と同じ）、受け口 `sync.Recorder` の実装をバインディング層に置く。
//
// 記録の対象は取得・取り込み・反映の 3 操作。接続確認は記録しない（同期先の内容を変えない操作のため）。
// **認証情報を含めない**（同期先の所在は資格情報部を除去済み。auditlog 側でも再度除去する）。

import (
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// syncRecorder は sync.Recorder の実装。作業コピーごとに記録先が変わるため（取得では新しい作業コピー）、
// 記録のたびに対象の作業コピーを開いて追記する。
type syncRecorder struct {
	author projectstore.Author
}

// RecordSync は同期の記録を 1 件追記する。
//
// 記録できなくても同期の操作自体は止めない（呼び出し側が戻り値を捨てる = 監査記録の失敗で作業を止めない）。
func (r syncRecorder) RecordSync(root string, rec syncmod.Record) error {
	store, err := projectstore.Open(root, r.author)
	if err != nil {
		return err
	}
	defer store.Close()
	logger, err := auditlog.New(store, r.author.AuthorID)
	if err != nil {
		return err
	}
	return logger.RecordSync(auditlog.SyncRecord{
		At: rec.At, Author: rec.Author, Op: string(rec.Op),
		RemoteKind: rec.RemoteKind, RemoteLocation: rec.RemoteLocation,
		Summary: summaryCounts(rec.Summary), Conflicts: rec.Conflicts,
		Result: string(rec.Result), Failure: string(rec.Failure),
	})
}

// summaryCounts は区分ごとの件数（追加・変更・削除の合計）へ畳む。
// 記録には業務データの本文を含めない（区分と件数のみ）。
func summaryCounts(s syncmod.Summary) map[string]int {
	if len(s) == 0 {
		return nil
	}
	out := map[string]int{}
	for cat, counts := range s {
		if n := counts.Total(); n > 0 {
			out[cat] = n
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---- 参照（同期画面の「同期の記録の参照」） ----------------------

// SyncLogEntryView は同期の記録 1 件の表示。
//
// git の語・生のエラー文言を出さない（利用者に git を意識させない）。同期先の所在は資格情報部を除去済み。
type SyncLogEntryView struct {
	At string `json:"at"`
	// Author は実行した作業者（表示名が分かる場合は表示名、無ければ利用者 ID）。
	Author string `json:"author"`
	// Operation は操作の表示名（取得 / 取り込み / 反映）。
	Operation string `json:"operation"`
	// Remote は同期先の表示（種別の表示名と所在）。
	Remote string `json:"remote"`
	// Summary は区分ごとの件数の 1 行（「要件項目 3 / 決定事項 1」。変更が無ければ「変更なし」）。
	Summary string `json:"summary"`
	// Conflicts は三面マージで解決した件数（0 なら表示しない想定）。
	Conflicts int `json:"conflicts,omitempty"`
	// Result は結果の表示名（成功 / 失敗 / 中止）。
	Result string `json:"result"`
	// Failure は失敗の理由（利用者向けの 1 文。成功時は空）。
	Failure string `json:"failure,omitempty"`
}

// SyncLogView は同期の記録の一覧（新しい順）。
type SyncLogView struct {
	Entries []SyncLogEntryView `json:"entries,omitempty"`
	// LastIncorporation は最後に成功した取り込みの日時（空 = まだ取り込んでいない）。
	LastIncorporation string `json:"lastIncorporation,omitempty"`
	// Notice は記録が無いときの案内。
	Notice string `json:"notice,omitempty"`
}

// syncResultLabels は結果の表示名（生のコード値を画面へ出さない）。
var syncResultLabels = map[string]string{
	string(syncmod.ResultOK):       "成功",
	string(syncmod.ResultFailed):   "失敗",
	string(syncmod.ResultCanceled): "中止",
}

func syncResultLabel(result string) string {
	if label, ok := syncResultLabels[result]; ok {
		return label
	}
	return "結果不明"
}

// SyncLog は同期の記録を新しい順に返す（**閲覧権限でも参照できる**）。
//
// AI 呼び出しを伴わず、プロジェクトデータを変更しない。同期先へも接続しない（記録は作業コピー内にある）。
func (a *API) SyncLog() (SyncLogView, error) {
	s, err := a.current()
	if err != nil {
		return SyncLogView{}, err
	}
	records, err := auditlog.ReadSyncRecords(s.store.Root(), time.Time{}, time.Time{})
	if err != nil {
		return SyncLogView{}, fmt.Errorf("同期の記録を読み込めません: %w", err)
	}
	members, err := s.store.LoadMembers()
	if err != nil {
		return SyncLogView{}, err
	}
	out := SyncLogView{}
	for i := len(records) - 1; i >= 0; i-- { // 新しい順
		rec := records[i]
		out.Entries = append(out.Entries, SyncLogEntryView{
			At:        rec.At.Local().Format("2006-01-02 15:04"),
			Author:    authorDisplayName(members, rec.Author),
			Operation: syncmod.Operation(rec.Op).Label(),
			Remote:    syncRemoteLabel(rec),
			Summary:   syncSummaryLine(rec.Summary),
			Conflicts: rec.Conflicts,
			Result:    syncResultLabel(rec.Result),
			Failure:   syncmod.FailureKind(rec.Failure).Label(),
		})
	}
	if last, err := auditlog.LastIncorporation(s.store.Root()); err == nil && !last.IsZero() {
		out.LastIncorporation = last.Local().Format("2006-01-02 15:04")
	}
	if len(out.Entries) == 0 {
		out.Notice = "同期の記録はまだありません。"
	}
	return out, nil
}

// syncRemoteLabel は同期先の表示（種別の表示名 + 所在）。
func syncRemoteLabel(rec auditlog.SyncRecord) string {
	kind := projectstore.SyncKindLabel(rec.RemoteKind)
	if rec.RemoteLocation == "" {
		return kind
	}
	return kind + "（" + rec.RemoteLocation + "）"
}

// syncSummaryLine は区分ごとの件数を 1 行にする（sync.Summary.Describe と同じ様式）。
func syncSummaryLine(counts map[string]int) string {
	if len(counts) == 0 {
		return "変更なし"
	}
	s := syncmod.Summary{}
	for cat, n := range counts {
		s[cat] = syncmod.Counts{Modified: n}
	}
	return s.Describe()
}
