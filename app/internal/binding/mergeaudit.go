package binding

// 本ファイルは三面マージで解決した競合の記録（変更履歴の `merge-applied`）。
//
// 突き合わせと承認の適用は同期モジュール（internal/sync）が行い、**変更履歴への記録はバインディング層の
// 責務**（同期モジュールは監査記録を書かない分担。版番号の再採番 = versionrenumber.go と同じ）。
// 記録には競合対象・選んだ解決・相手の作業者名を含める。

import (
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	"github.com/howashoji/ReqWeave/app/internal/sync"
)

// mergeAuditor は同期モジュール（sync.MergeAuditor）へ渡す実装を組み立てる。
//
// 記録に失敗しても取り込みは止めない（監査記録の失敗で作業を止めない = recordChange の方針）。
func (a *API) mergeAuditor(s *dialogueSession) sync.MergeAuditor {
	return conflictAuditor{api: a, session: s}
}

type conflictAuditor struct {
	api     *API
	session *dialogueSession
}

// RecordMergeApplied は解決 1 件を変更履歴へ書く。
//
// Before に相手の作業者名（表示名）、After に選んだ解決を入れる。
// 「未決事項として起票する」を選んだ場合は起票した未決事項 ID を evidence に残す。
func (r conflictAuditor) RecordMergeApplied(root string, c sync.Conflict, res sync.Resolution) {
	if r.session == nil || r.session.store == nil || r.session.store.Root() != root {
		return // 別の作業コピーの取り込み（記録先が特定できない）
	}
	r.api.recordChange(r.session, auditlog.ChangeRecord{
		At:       time.Now().UTC(),
		Author:   r.session.store.Author().AuthorID,
		Target:   c.Target(),
		Change:   auditlog.ChangeMergeApplied,
		Before:   conflictOpponent(r.session.store, c),
		After:    mergeChoiceLabel(res.Choice),
		Evidence: res.OpenIssueID,
	})
}

// conflictOpponent は相手の作業者の表示名（メンバー一覧に居なければ利用者 ID のまま）。
func conflictOpponent(store *projectstore.Store, c sync.Conflict) string {
	members, err := store.LoadMembers()
	if err != nil {
		return c.TheirsAuthor
	}
	return authorDisplayName(members, c.TheirsAuthor)
}

// mergeChoiceLabel は選んだ解決の表示名（生のコード値を残さない）。
func mergeChoiceLabel(choice sync.Choice) string {
	if label, ok := sync.ChoiceLabel[choice]; ok {
		return label
	}
	return "解決不明"
}
