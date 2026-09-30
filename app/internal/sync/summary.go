package sync

import (
	"sort"
	"strings"
)

// 変更の要約（区分ごとの件数）。コミットメッセージ・反映の事前提示・
// 同期の記録（summary）で共通に使う。**業務データの本文を含めない**。

// Counts は区分ごとの件数。
type Counts struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

// Total は件数の合計。
func (c Counts) Total() int { return c.Added + c.Modified + c.Removed }

// Summary は区分（categoryOf の値）→ 件数。
type Summary map[string]Counts

type changeKind int

const (
	changeAdded changeKind = iota
	changeModified
	changeRemoved
)

func (s Summary) add(path string, kind changeKind) {
	cat := categoryOf(path)
	c := s[cat]
	switch kind {
	case changeAdded:
		c.Added++
	case changeRemoved:
		c.Removed++
	default:
		c.Modified++
	}
	s[cat] = c
}

// Total は全区分の件数の合計。
func (s Summary) Total() int {
	n := 0
	for _, c := range s {
		n += c.Total()
	}
	return n
}

// Categories は件数のある区分を表示順（categoryOrder）で返す。
func (s Summary) Categories() []string {
	var cats []string
	for cat, c := range s {
		if c.Total() > 0 {
			cats = append(cats, cat)
		}
	}
	sort.Slice(cats, func(i, j int) bool { return categoryRank(cats[i]) < categoryRank(cats[j]) })
	return cats
}

// Describe は「要件項目 3 / 決定事項 1」の形式（コミットメッセージ・記録用）。変更が無ければ「変更なし」。
func (s Summary) Describe() string {
	cats := s.Categories()
	if len(cats) == 0 {
		return "変更なし"
	}
	parts := make([]string, 0, len(cats))
	for _, cat := range cats {
		parts = append(parts, CategoryLabel(cat)+" "+itoa(s[cat].Total()))
	}
	return strings.Join(parts, " / ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// 区分（プロジェクトフォルダの構成に対応）。
const (
	CatProject        = "project"
	CatMembers        = "members"
	CatRoster         = "roster"
	CatReservations   = "reservations"
	CatIDRanges       = "id-ranges"
	CatExchangeKeys   = "exchange-keys"
	CatTerms          = "terms"
	CatPerspectives   = "perspectives"
	CatSessions       = "sessions"
	CatRequirements   = "requirements"
	CatDecisions      = "decisions"
	CatOpenIssues     = "open-issues"
	CatQuestionnaires = "questionnaires"
	CatImports        = "imports"
	CatDocuments      = "documents"
	CatAudit          = "audit"
	CatOther          = "other"
)

var categoryOrder = []string{
	CatProject, CatMembers, CatRoster, CatReservations, CatIDRanges, CatExchangeKeys, CatTerms, CatPerspectives,
	CatSessions, CatRequirements, CatDecisions, CatOpenIssues, CatQuestionnaires, CatImports, CatDocuments, CatAudit, CatOther,
}

// categoryLabels は区分の表示名（生のコード値を画面へ出さない）。
var categoryLabels = map[string]string{
	CatProject:        "プロジェクト設定",
	CatMembers:        "メンバー",
	CatRoster:         "名簿",
	CatReservations:   "予約",
	CatIDRanges:       "番号帯",
	CatExchangeKeys:   "交換鍵",
	CatTerms:          "用語",
	CatPerspectives:   "観点",
	CatSessions:       "対話セッション",
	CatRequirements:   "要件項目",
	CatDecisions:      "決定事項",
	CatOpenIssues:     "未決事項",
	CatQuestionnaires: "質問票",
	CatImports:        "取り込み資料",
	CatDocuments:      "成果物ドキュメント",
	CatAudit:          "監査記録",
	CatOther:          "その他",
}

// CategoryLabel は区分の表示名を返す。
func CategoryLabel(cat string) string {
	if l, ok := categoryLabels[cat]; ok {
		return l
	}
	return categoryLabels[CatOther]
}

func categoryRank(cat string) int {
	for i, c := range categoryOrder {
		if c == cat {
			return i
		}
	}
	return len(categoryOrder)
}

// syncLogDir は同期の記録の置き場所（sync-log）。
const syncLogDir = "audit/sync-log/"

// IsSyncBookkeeping は「同期そのものが書いた記録」かを返す。
//
// 同期の記録は操作の**完了後**に作業コピーへ追記されるため（結果が確定してからでないと書けない）、
// 取得・取り込み・反映のたびに未反映の変更が 1 件残る。これを「未反映の変更の有無」の指標
// （上部の同期の表示・同期の画面）に数えると**指標が常時点灯して意味を失う**ため、
// 指標の集計からだけ外す（同期の対象からは外さない = 同期の対象範囲は変えない。
// 記録は次回の同期で同期先へ載る）。
func IsSyncBookkeeping(path string) bool {
	return strings.HasPrefix(strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./"), syncLogDir)
}

// categoryOf はプロジェクトフォルダからの相対パスを区分へ写す。
func categoryOf(path string) string {
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
	top, _, _ := strings.Cut(path, "/")
	switch top {
	case "project.yaml":
		return CatProject
	case "members.yaml":
		return CatMembers
	case "roster.yaml":
		return CatRoster
	case "reservations.yaml":
		return CatReservations
	case "id-ranges.yaml":
		return CatIDRanges
	case "exchange-keys.yaml":
		return CatExchangeKeys
	case "terms.yaml":
		return CatTerms
	case "perspectives.yaml":
		return CatPerspectives
	case "sessions", "requirements", "decisions", "open-issues", "questionnaires", "imports", "documents", "audit":
		return top
	default:
		return CatOther
	}
}
