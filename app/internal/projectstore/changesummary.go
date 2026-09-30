package projectstore

// 本ファイルは他の作業者による変更要約を担う。
//
// **要約の単位は「取り込み」**。取り込みで自分の作業コピーへ入った
// **他の作業者**の変更履歴レコードを区分ごとに集計・列挙する。AI 呼び出しを行わず、要約を保存しない
// （進捗レポートと同じ履歴データソースの機械組立て。二重管理をしない）。
//
// 日時で区切らない理由: 作業コピーは各自の端末にあり、相手が昨日書いた記録が今日の取り込みで入る。
// 「前回の日時以降」で絞ると、取り込みで入ったばかりの古い記録を落としてしまう。
// 入ってきたレコードの特定は同期モジュールが行い、本ファイルはその結果を受け取る。
//
// 各項目は対象 ID を持ち、該当レコードへの遷移は既存の追跡連鎖・逆引きで
// 解決する（本ファイルは遷移先の解決を持たない）。

import (
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
)

// 変更要約の区分。
const (
	SummaryDecisions     = "decisions"      // 決定事項
	SummaryOpenIssues    = "open-issues"    // 未決事項
	SummaryRequirements  = "requirements"   // 要件項目
	SummaryQuestionnaire = "questionnaires" // 質問票の状態変化
	SummaryConfirmation  = "confirmations"  // 確定・差し戻し
	SummaryMembers       = "members"        // メンバー変更
	SummaryReservations  = "reservations"   // 予約と作業状況
)

// ChangeSummaryItem は変更要約の 1 項目。
type ChangeSummaryItem struct {
	// Kind は区分（Summary* のいずれか）。
	Kind string `json:"kind"`
	// Target は対象 ID（DEC-nnn / ISS-nnn / FR-* / QS-nnn / 利用者 ID など）。遷移の起点。
	Target string `json:"target"`
	// Change は変更の種別（auditlog.Change*）。
	Change string `json:"change"`
	// Author は変更した作業者の利用者 ID。
	Author string    `json:"author"`
	At     time.Time `json:"at"`
	// Before / After は変更履歴に記録された 1 行要約（表示用）。
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// ChangeSummary は前回確認した取り込み以降に入った、他の作業者による変更。
type ChangeSummary struct {
	// Incorporation は今回集計した取り込みの位置（確認済みとして端末側の settings.json へ保存する値）。
	Incorporation string `json:"incorporation,omitempty"`
	// NoIncorporation は起点となる取り込みが無い状態か。
	//
	// 同期先が未設定（単独利用）・まだ一度も取り込んでいない・確認済みの位置が失われた場合に真。
	// 起点が無いため**全期間を対象にせず、対象なしとして返す**（推定で埋めない。
	// 初回オープンで過去の全変更を「前回以降の変更」として見せない）。
	NoIncorporation bool `json:"noIncorporation"`
	// Items は区分ごとの項目（日時の昇順）。
	Items []ChangeSummaryItem `json:"items,omitempty"`
}

// IsEmpty は提示する変更が 1 件も無いかを返す（画面は「変更なし」と示す）。
func (s ChangeSummary) IsEmpty() bool { return len(s.Items) == 0 }

// CountByKind は区分ごとの件数を返す。
func (s ChangeSummary) CountByKind() map[string]int {
	out := map[string]int{}
	for _, item := range s.Items {
		out[item.Kind]++
	}
	return out
}

// ChangeSummaryOfIncorporation は取り込みで入った変更履歴レコードから要約を組み立てる。
//
// incoming は同期モジュールが特定した「この取り込みで作業コピーへ入ったレコード」。
// **自分が行った変更は除く**（他の作業者による変更を把握するための要約のため）。
// incorporation は今回の取り込みの位置、noStartingPoint は起点が無かったか。
//
// 読み出しのみで、プロジェクトデータを変更しない（要約も保存しない）。
func (s *Store) ChangeSummaryOfIncorporation(incorporation string, noStartingPoint bool,
	incoming []auditlog.ChangeRecord) *ChangeSummary {

	out := &ChangeSummary{Incorporation: incorporation, NoIncorporation: noStartingPoint}
	if noStartingPoint {
		return out
	}
	self := s.author.AuthorID
	for _, c := range incoming {
		if c.Author == self {
			continue
		}
		kind, ok := summaryKindOf(c)
		if !ok {
			continue
		}
		out.Items = append(out.Items, ChangeSummaryItem{
			Kind: kind, Target: c.Target, Change: c.Change,
			Author: c.Author, At: c.At, Before: c.Before, After: c.After,
		})
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].At.Before(out.Items[j].At) })
	return out
}

// summaryKindOf は変更履歴レコードを要約の区分へ振り分ける（対象外は ok = false）。
//
// 対象外: プロジェクトの開閉・端末紐づけ・AI 送信（要約の区分に無いもの）。
func summaryKindOf(c auditlog.ChangeRecord) (string, bool) {
	switch c.Change {
	case auditlog.ChangeReservationSet, auditlog.ChangeReservationReleased:
		return SummaryReservations, true
	case auditlog.ChangeVersionRenumbered:
		// 確定版の版番号の付け替えは「確定・差し戻し」と同じ区分で提示する（対象は成果物の版）。
		return SummaryConfirmation, true
	case auditlog.ChangeMemberAdded, auditlog.ChangeMemberRemoved,
		auditlog.ChangeRoleChanged, auditlog.ChangeOwnerTakeover, auditlog.ChangeMemberIDCorrected:
		return SummaryMembers, true
	}
	switch {
	case strings.HasPrefix(c.Target, IDDecision.Prefix+"-"):
		return SummaryDecisions, true
	case strings.HasPrefix(c.Target, IDOpenIssue.Prefix+"-"):
		return SummaryOpenIssues, true
	case strings.HasPrefix(c.Target, IDQuestionnaire.Prefix+"-"):
		return SummaryQuestionnaire, true
	case isRequirementTarget(c.Target):
		// 要件項目の状態変化（合意・差し戻し）と確定操作は「確定・差し戻し」区分へ。
		if c.Change == auditlog.ChangeStatusChanged {
			return SummaryConfirmation, true
		}
		return SummaryRequirements, true
	case isDocumentTarget(c.Target):
		return SummaryConfirmation, true
	}
	return "", false
}

// isRequirementTarget は要件項目 ID（FR-<グループ>-nnn / NFR-<グループ>-nnn）かを返す。
func isRequirementTarget(target string) bool {
	return strings.HasPrefix(target, "FR-") || strings.HasPrefix(target, "NFR-")
}

// isDocumentTarget は成果物ドキュメントの確定の対象かを返す。
//
// 確定操作の変更履歴は `<kind>/v<版>`（例: requirements/v1）を対象に記録する。
// フェーズ移行（対象 `project`）は変更要約の区分に無いため含めない。
func isDocumentTarget(target string) bool {
	return strings.HasPrefix(target, DocKindRequirements+"/") ||
		strings.HasPrefix(target, DocKindBasicDesign+"/")
}
