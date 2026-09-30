package docgen

// 本ファイルは章割当と依存マップを担う。
//
// 割当のデータソースはレコード（要件項目・決定事項・未決事項・用語）のみ。
// 章ごとの収載ソース ID 集合を依存マップとして残し、差分再生成と
// 変更影響表示の入力にする。

import (
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// Records は割当・組立ての入力（対話の充足判定と同じデータソース）。
type Records struct {
	Requirements []projectstore.Requirement
	Decisions    []projectstore.Decision
	OpenIssues   []projectstore.OpenIssue
	Terms        []projectstore.Term
}

// ChapterSources は 1 章に収載するレコード。
type ChapterSources struct {
	Chapter      ChapterTemplate
	Requirements []projectstore.Requirement
	Decisions    []projectstore.Decision
	OpenIssues   []projectstore.OpenIssue
	// Terms は本章の本文で使う用語（全用語を渡す。用語集は正本として常に注入する）。
	Terms []projectstore.Term
}

// SourceIDs は収載したレコードの ID 集合を昇順で返す（依存マップの値）。
func (s ChapterSources) SourceIDs() []string {
	var ids []string
	for _, r := range s.Requirements {
		ids = append(ids, r.ID)
	}
	for _, d := range s.Decisions {
		ids = append(ids, d.ID)
	}
	for _, i := range s.OpenIssues {
		ids = append(ids, i.ID)
	}
	sort.Strings(ids)
	return ids
}

// IsEmpty は収載するレコードが無いかを返す。
func (s ChapterSources) IsEmpty() bool {
	return len(s.Requirements) == 0 && len(s.Decisions) == 0 && len(s.OpenIssues) == 0
}

// Assignment は章割当の結果（章の定義順）。
type Assignment struct {
	Sources []ChapterSources
}

// DependencyMap は章ファイル名 → 収載ソース ID 集合を返す（版履歴へ記録する）。
func (a Assignment) DependencyMap() map[string][]string {
	out := map[string][]string{}
	for _, s := range a.Sources {
		out[s.Chapter.File] = s.SourceIDs()
	}
	return out
}

// AllSourceIDs は全章の収載ソース ID を重複なく昇順で返す。
func (a Assignment) AllSourceIDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range a.Sources {
		for _, id := range s.SourceIDs() {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Assign はレコードをテンプレートの章へ割当てる。
//
//   - 要件項目: chapter タグの章へ
//   - 決定事項: 論点キーの章観点部分（<章観点ID>/<必須項目ID>）の章へ
//   - 未決事項: ブロックする要件項目の章へ（複数章に載りうる。ブロック対象が無いものは載らない）
func Assign(doc *DocumentTemplate, records Records) Assignment {
	byChapter := map[string]*ChapterSources{}
	var order []string
	for _, c := range doc.Chapters {
		if !c.IsGenerated() {
			continue // 機械組立て文書は割当の対象外
		}
		byChapter[c.Chapter] = &ChapterSources{Chapter: c, Terms: records.Terms}
		order = append(order, c.Chapter)
	}

	requirementChapter := map[string]string{}
	for _, r := range records.Requirements {
		requirementChapter[r.ID] = r.Chapter
		if s, ok := byChapter[r.Chapter]; ok {
			s.Requirements = append(s.Requirements, r)
		}
	}
	for _, d := range records.Decisions {
		chapter, _, _ := strings.Cut(d.TopicKey, "/")
		if s, ok := byChapter[chapter]; ok {
			s.Decisions = append(s.Decisions, d)
		}
	}
	// 未決事項はブロック対象の要件項目の章へ載せる（ブロックの関係は要件項目側の blocked_by を正とする）。
	blockedChapters := map[string]map[string]bool{}
	for _, r := range records.Requirements {
		for _, issueID := range r.BlockedBy {
			if blockedChapters[issueID] == nil {
				blockedChapters[issueID] = map[string]bool{}
			}
			blockedChapters[issueID][r.Chapter] = true
		}
	}
	for _, i := range records.OpenIssues {
		for chapter := range blockedChapters[i.ID] {
			if s, ok := byChapter[chapter]; ok {
				s.OpenIssues = append(s.OpenIssues, i)
			}
		}
	}

	out := Assignment{}
	for _, chapter := range order {
		out.Sources = append(out.Sources, *byChapter[chapter])
	}
	return out
}
