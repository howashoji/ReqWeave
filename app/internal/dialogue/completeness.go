package dialogue

// 本ファイルは章観点ごとの充足率と、確定可否の判定を担う。
//
// 両者は**同一のレコード**（要件項目・決定事項・未決事項・用語）から算出する。
// 完成度表示と確定前チェックが食い違わないことを構造で保証する。
// 成果物ドキュメントの本文は解析しない。

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 章観点の表示区分（3 状態）。
const (
	// StateUntouched は未着手（充足率 0%）。
	StateUntouched = "untouched"
	// StateOpenIssues は記載あり・未決あり（充足率 > 0% かつ当該章観点の未決が 1 件以上）。
	StateOpenIssues = "with-open-issues"
	// StateSettled は記載あり・未決なし。
	StateSettled = "settled"
)

// Records は算出の入力（充足率と確定可否のデータソース）。
type Records struct {
	Requirements []projectstore.Requirement
	Decisions    []projectstore.Decision
	OpenIssues   []projectstore.OpenIssue
	Terms        []projectstore.Term
}

// ChapterCompleteness は 1 章観点の充足状況。
type ChapterCompleteness struct {
	ChapterID string `json:"chapterId"`
	Name      string `json:"name"`
	// Percent は充足率（0〜100。小数点以下切り捨て）。
	Percent int `json:"percent"`
	// Satisfied / Total は充足済み必須項目数と必須項目総数。
	Satisfied int `json:"satisfied"`
	Total     int `json:"total"`
	// OpenIssues は当該章観点に紐づく未決状態の未決事項数（ブロック対象の要件項目経由）。
	OpenIssues int `json:"openIssues"`
	// State は表示区分（未着手 / 記載あり・未決あり / 記載あり・未決なし）。
	State string `json:"state"`
	// UnsatisfiedItems は未充足の必須項目（次に確認すべき論点の候補）。
	UnsatisfiedItems []UnsatisfiedItem `json:"unsatisfiedItems,omitempty"`
}

// UnsatisfiedItem は未充足の必須項目。
type UnsatisfiedItem struct {
	ItemID   string `json:"itemId"`
	Name     string `json:"name"`
	TopicKey string `json:"topicKey"`
}

// Completeness は章観点ごとの充足率を定義順で返す。
func Completeness(phase string, r Records) ([]ChapterCompleteness, error) {
	m, err := LoadMetaModel()
	if err != nil {
		return nil, err
	}
	p, err := m.Phase(phase)
	if err != nil {
		return nil, err
	}
	openByChapter := openIssueCountByChapter(r)

	out := make([]ChapterCompleteness, 0, len(p.Chapters))
	for _, c := range p.Chapters {
		cc := ChapterCompleteness{ChapterID: c.ID, Name: c.Name, Total: len(c.Items)}
		for _, item := range c.Items {
			if satisfied(c, item, r) {
				cc.Satisfied++
				continue
			}
			cc.UnsatisfiedItems = append(cc.UnsatisfiedItems, UnsatisfiedItem{
				ItemID: item.ID, Name: item.Name, TopicKey: c.TopicKey(item.ID),
			})
		}
		if cc.Total > 0 {
			cc.Percent = 100 * cc.Satisfied / cc.Total // 小数点以下切り捨て
		}
		cc.OpenIssues = openByChapter[c.ID]
		cc.State = chapterState(cc.Percent, cc.OpenIssues)
		out = append(out, cc)
	}
	return out, nil
}

// chapterState は表示区分を返す。
func chapterState(percent, openIssues int) string {
	switch {
	case percent == 0:
		return StateUntouched
	case openIssues > 0:
		return StateOpenIssues
	default:
		return StateSettled
	}
}

// satisfied は必須項目の判定条件を満たすレコードが存在するかを返す。
func satisfied(c Chapter, item Item, r Records) bool {
	cond := item.Condition
	switch cond.Kind {
	case CondDecision:
		return countDecisions(r, c.TopicKey(item.ID)) >= cond.Min
	case CondRequirement:
		return len(requirementsOf(r, c.ID, cond.RequirementKind)) >= cond.Min
	case CondRequirementCriteria:
		reqs := requirementsOf(r, c.ID, cond.RequirementKind)
		if len(reqs) < cond.Min {
			return false
		}
		for _, req := range reqs {
			if !req.HasAcceptanceCriteria() {
				return false
			}
		}
		return true
	case CondTerm:
		return len(r.Terms) >= cond.Min
	default:
		return false
	}
}

// countDecisions は論点キーが一致する決定事項の数を返す。
//
// 置き換えられた決定（superseded_by つき）は数えない（覆された内容で充足済みにしない）。
func countDecisions(r Records, topicKey string) int {
	n := 0
	for _, d := range r.Decisions {
		if d.TopicKey == topicKey && d.SupersededBy == "" {
			n++
		}
	}
	return n
}

// requirementsOf は章観点・種別で要件項目を絞る。
func requirementsOf(r Records, chapter, kind string) []projectstore.Requirement {
	var out []projectstore.Requirement
	for _, req := range r.Requirements {
		if req.Chapter == chapter && (kind == "" || req.Kind == kind) {
			out = append(out, req)
		}
	}
	return out
}

// openIssueCountByChapter は章観点ごとの未決状態の未決事項数を返す。
//
// 未決事項は章観点を持たないため、ブロック対象の要件項目（要件項目側の blocked_by を正とする）
// の章観点で数える。1 つの未決事項が同じ章観点を複数ブロックしても 1 件と数える。
func openIssueCountByChapter(r Records) map[string]int {
	chaptersByIssue := map[string]map[string]bool{}
	for _, req := range r.Requirements {
		for _, issueID := range req.BlockedBy {
			if chaptersByIssue[issueID] == nil {
				chaptersByIssue[issueID] = map[string]bool{}
			}
			chaptersByIssue[issueID][req.Chapter] = true
		}
	}
	counts := map[string]int{}
	for _, issue := range r.OpenIssues {
		if issue.Status != projectstore.OpenIssueOpen {
			continue
		}
		for chapter := range chaptersByIssue[issue.ID] {
			counts[chapter]++
		}
	}
	return counts
}

// Confirmation は確定可否の判定結果。
type Confirmation struct {
	// Confirmable は確定可か（要件項目が 1 件以上あり、全要件項目が合意済みで、
	// ブロックする未決状態の未決事項が 0 件）。
	Confirmable bool `json:"confirmable"`
	// NoRequirements は要件項目が 1 件も無いか（確定する対象が無い）。
	NoRequirements bool `json:"noRequirements,omitempty"`
	// DraftRequirements は未合意の要件項目 ID。
	DraftRequirements []string `json:"draftRequirements,omitempty"`
	// BlockingIssues はブロックしている未決状態の未決事項 ID。
	BlockingIssues []string `json:"blockingIssues,omitempty"`
}

// Reason は確定できない理由を利用者向けの 1 文で返す（確定可のときは空文字）。
func (c Confirmation) Reason() string {
	if c.Confirmable {
		return ""
	}
	var parts []string
	if c.NoRequirements {
		return "要件項目がまだ 1 件もありません。対話で要件を記録してから確定してください。"
	}
	if n := len(c.DraftRequirements); n > 0 {
		parts = append(parts, fmt.Sprintf("未合意の要件項目が %d 件あります", n))
	}
	if n := len(c.BlockingIssues); n > 0 {
		parts = append(parts, fmt.Sprintf("要件項目をブロックする未決事項が %d 件あります", n))
	}
	return strings.Join(parts, "。") + "。解消してから確定してください。"
}

// Confirmable は確定可否を判定する。
//
// 完成度表示（Completeness）と同一のレコードから算出する（表示と判定を食い違わせないため）。
func Confirmable(r Records) Confirmation {
	var result Confirmation
	for _, req := range r.Requirements {
		if req.Status != projectstore.RequirementAgreed {
			result.DraftRequirements = append(result.DraftRequirements, req.ID)
		}
	}
	blocking := map[string]bool{}
	for _, req := range r.Requirements {
		for _, issueID := range req.BlockedBy {
			blocking[issueID] = true
		}
	}
	for _, issue := range r.OpenIssues {
		if issue.Status == projectstore.OpenIssueOpen && blocking[issue.ID] {
			result.BlockingIssues = append(result.BlockingIssues, issue.ID)
		}
	}
	// 要件項目が 0 件のときは確定する対象が無いため確定不可（全件合意の条件が空虚に真になるのを防ぐ）。
	result.NoRequirements = len(r.Requirements) == 0
	result.Confirmable = !result.NoRequirements &&
		len(result.DraftRequirements) == 0 && len(result.BlockingIssues) == 0
	return result
}
