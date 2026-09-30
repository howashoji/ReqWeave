package binding

// 本ファイルは確定版の版番号の再採番の記録（変更履歴の `version-renumbered`）。
//
// 再採番そのものは projectstore が行い、同期モジュールが取り込みの途中で呼ぶ。
// **変更履歴への記録はバインディング層の責務**（projectstore は監査記録を書かない分担）。
// 記録には旧番号・新番号・対象種別を含める（後から番号の付け替えを辿れるようにする）。

import (
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// docKindLabels は成果物の種別の日本語表示（値は要件定義書・基本設計書の 2 つで閉じている）。
var docKindLabels = map[string]string{
	projectstore.DocKindRequirements: "要件定義書",
	projectstore.DocKindBasicDesign:  "基本設計書",
}

func docKindLabel(kind string) string {
	if label, ok := docKindLabels[kind]; ok {
		return label
	}
	return "成果物"
}

// versionResolver は同期モジュール（sync.VersionResolver）へ渡す実装を組み立てる。
//
// 取り込みで自分の確定版の版番号が付け替えられたとき、変更履歴へ `version-renumbered` を記録する。
// 記録に失敗しても取り込みは止めない（監査記録の失敗で作業を止めない = recordChange の方針）。
func (a *API) versionResolver(s *dialogueSession) projectstore.VersionRenumberResolver {
	return projectstore.VersionRenumberResolver{
		Store: s.store,
		OnRenumber: func(r projectstore.VersionRenumber) {
			a.recordChange(s, auditlog.ChangeRecord{
				At: time.Now().UTC(), Author: s.store.Author().AuthorID,
				Target: fmt.Sprintf("%s/v%d", r.Kind, r.To),
				Change: auditlog.ChangeVersionRenumbered,
				Before: fmt.Sprintf("%s v%d", docKindLabel(r.Kind), r.From),
				After:  fmt.Sprintf("%s v%d", docKindLabel(r.Kind), r.To),
			})
		},
	}
}
