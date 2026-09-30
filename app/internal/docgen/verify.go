package docgen

// 本ファイルは整合性検証（V1〜V6）と、用語集の自動維持の検出側を担う。
//
// 生成時の自己検証とエクスポート前検証は本検証器を共用する。
// AI を使わない機械検証。

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 検証項目。
const (
	CheckIDReference        = "V1" // ID 参照切れ
	CheckMissingEvidence    = "V2" // 参照欠落
	CheckBlockingOpenIssue  = "V3" // ブロックする未決事項の残存
	CheckTermMismatch       = "V4" // 用語不一致
	CheckAmbiguousWord      = "V5" // 曖昧語
	CheckAcceptanceCriteria = "V6" // 受け入れ条件の欠落
)

// 違反の区分。
const (
	// SeverityError は警告付きエクスポートの選択を要する違反。
	SeverityError = "error"
	// SeverityWarning は一覧提示のみ（エクスポートの選択肢は同じ）。
	SeverityWarning = "warning"
)

// Violation は検証違反の 1 件。
type Violation struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	// File は対象の章ファイル名（レコード起因の違反では空）。
	File string `json:"file,omitempty"`
	// Line は対象の行番号（1 起算。行を特定できない場合は 0）。
	Line int `json:"line,omitempty"`
	// Target は対象のレコード ID・用語など。
	Target string `json:"target,omitempty"`
	// Message は利用者向けの説明（原因と対象がわかる 1 文）。
	Message string `json:"message"`
}

// VerifyResult は検証結果。
type VerifyResult struct {
	Violations []Violation `json:"violations"`
}

// Errors はエラー区分の違反数を返す。
func (r VerifyResult) Errors() int { return r.count(SeverityError) }

// Warnings は警告区分の違反数を返す。
func (r VerifyResult) Warnings() int { return r.count(SeverityWarning) }

func (r VerifyResult) count(severity string) int {
	n := 0
	for _, v := range r.Violations {
		if v.Severity == severity {
			n++
		}
	}
	return n
}

// Passed は違反ゼロ（検証合格）かを返す。
func (r VerifyResult) Passed() bool { return len(r.Violations) == 0 }

// idPatterns は本文中の ID 参照を走査するパターン（成果物の ID 形式）。
var idPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bFR-[A-Z]{2,4}-[0-9]{3}\b`),
	regexp.MustCompile(`\bNFR-[A-Z]{2,4}-[0-9]{3}\b`),
	regexp.MustCompile(`\bUC-[0-9]{2}\b`),
	regexp.MustCompile(`\bDEC-[0-9]{3}\b`),
	regexp.MustCompile(`\bISS-[0-9]{3}\b`),
	regexp.MustCompile(`\bBD-[A-Z]{2,4}-[0-9]{3}\b`),
}

// DefaultAmbiguousWords は曖昧語の初期リスト（V5。アプリ組み込み）。
var DefaultAmbiguousWords = []string{
	"速い", "遅い", "使いやすい", "わかりやすい", "柔軟に", "適切に", "十分に", "可能な限り", "随時",
}

// AmbiguousWordsFor は初期リストへプロジェクトの調整を適用した語リストを返す。
//
// 対象システムの業務用語として正式な語（例:「随時」）は除外でき、逆に固有の曖昧語を足せる。
func AmbiguousWordsFor(p *projectstore.Project) []string {
	excluded := map[string]bool{}
	var added []string
	if p != nil && p.AmbiguousTerms != nil {
		for _, w := range p.AmbiguousTerms.Excluded {
			excluded[w] = true
		}
		added = p.AmbiguousTerms.Added
	}
	out := make([]string, 0, len(DefaultAmbiguousWords)+len(added))
	for _, w := range DefaultAmbiguousWords {
		if !excluded[w] {
			out = append(out, w)
		}
	}
	for _, w := range added {
		if w != "" && !excluded[w] {
			out = append(out, w)
		}
	}
	return out
}

// VerifyInput は検証の入力。
type VerifyInput struct {
	// Chapters は検証対象の章ファイル（生成直後のドラフト、または確定版）。
	Chapters []projectstore.DocumentChapter
	Records  Records
	// AmbiguousWords は曖昧語リスト（空なら DefaultAmbiguousWords。通常は AmbiguousWordsFor の結果を渡す）。
	AmbiguousWords []string
	// KnownIDs は本文中に現れてよい追加の ID（対象システム側で定義済みの設計要素など）。
	KnownIDs []string
}

// Verify は V1〜V6 を走査する。
func Verify(in VerifyInput) VerifyResult {
	var out VerifyResult
	out.Violations = append(out.Violations, checkIDReferences(in)...)
	out.Violations = append(out.Violations, checkMissingEvidence(in)...)
	out.Violations = append(out.Violations, checkBlockingOpenIssues(in)...)
	out.Violations = append(out.Violations, checkTermMismatch(in)...)
	out.Violations = append(out.Violations, checkAmbiguousWords(in)...)
	out.Violations = append(out.Violations, checkAcceptanceCriteria(in)...)
	sort.SliceStable(out.Violations, func(i, j int) bool {
		if out.Violations[i].Check != out.Violations[j].Check {
			return out.Violations[i].Check < out.Violations[j].Check
		}
		if out.Violations[i].File != out.Violations[j].File {
			return out.Violations[i].File < out.Violations[j].File
		}
		return out.Violations[i].Line < out.Violations[j].Line
	})
	return out
}

// checkIDReferences は V1（ID 参照切れ）。本文中の ID を全走査し、存在しない参照先を列挙する。
func checkIDReferences(in VerifyInput) []Violation {
	known := map[string]bool{}
	for _, r := range in.Records.Requirements {
		known[r.ID] = true
	}
	for _, d := range in.Records.Decisions {
		known[d.ID] = true
	}
	for _, i := range in.Records.OpenIssues {
		known[i.ID] = true
	}
	for _, id := range in.KnownIDs {
		known[id] = true
	}

	var out []Violation
	for _, c := range in.Chapters {
		for line, text := range strings.Split(c.Body, "\n") {
			for _, re := range idPatterns {
				for _, id := range re.FindAllString(text, -1) {
					if known[id] {
						continue
					}
					out = append(out, Violation{
						Check: CheckIDReference, Severity: SeverityError,
						File: c.FileName, Line: line + 1, Target: id,
						Message: fmt.Sprintf("%s は存在しない参照先です。", id),
					})
				}
			}
		}
	}
	return out
}

// checkMissingEvidence は V2（参照欠落）。根拠を持たない要件項目と、
// 要件項目 ID を参照しない設計要素（基本設計の章）を列挙する。
func checkMissingEvidence(in VerifyInput) []Violation {
	var out []Violation
	for _, r := range in.Records.Requirements {
		if r.HasEvidence() {
			continue
		}
		out = append(out, Violation{
			Check: CheckMissingEvidence, Severity: SeverityError, Target: r.ID,
			Message: fmt.Sprintf("%s は根拠となる決定事項・発話への参照を持ちません。", r.ID),
		})
	}
	// 基本設計の章は根拠となる要件項目 ID を参照していること（設計から要件へ辿れるように）。
	for _, c := range in.Chapters {
		if c.DocKind != projectstore.DocKindBasicDesign {
			continue
		}
		if hasRequirementReference(c.Body) {
			continue
		}
		out = append(out, Violation{
			Check: CheckMissingEvidence, Severity: SeverityError, File: c.FileName, Target: c.Chapter,
			Message: fmt.Sprintf("%s は根拠となる要件項目 ID を参照していません。", c.FileName),
		})
	}
	return out
}

func hasRequirementReference(body string) bool {
	return idPatterns[0].MatchString(body) || idPatterns[1].MatchString(body)
}

// checkBlockingOpenIssues は V3（ブロックする未決事項の残存）。
// 確定可否判定と同一のデータ（要件項目の blocked_by と未決の状態）を使う。
func checkBlockingOpenIssues(in VerifyInput) []Violation {
	blocking := map[string][]string{}
	for _, r := range in.Records.Requirements {
		for _, id := range r.BlockedBy {
			blocking[id] = append(blocking[id], r.ID)
		}
	}
	var out []Violation
	for _, i := range in.Records.OpenIssues {
		if i.Status != projectstore.OpenIssueOpen {
			continue
		}
		targets := blocking[i.ID]
		if len(targets) == 0 {
			continue
		}
		sort.Strings(targets)
		out = append(out, Violation{
			Check: CheckBlockingOpenIssue, Severity: SeverityError, Target: i.ID,
			Message: fmt.Sprintf("%s（%s）が %s をブロックしたままです。",
				i.ID, firstLine(i.Body), strings.Join(targets, ", ")),
		})
	}
	return out
}

// checkTermMismatch は V4（用語不一致）。禁止同義語の使用と英語識別子の不一致を検出する。
func checkTermMismatch(in VerifyInput) []Violation {
	var out []Violation
	for _, c := range in.Chapters {
		lines := strings.Split(c.Body, "\n")
		for i, line := range lines {
			for _, t := range in.Records.Terms {
				for _, forbidden := range t.Forbidden {
					if forbidden == "" || !strings.Contains(line, forbidden) {
						continue
					}
					out = append(out, Violation{
						Check: CheckTermMismatch, Severity: SeverityError,
						File: c.FileName, Line: i + 1, Target: forbidden,
						Message: fmt.Sprintf("「%s」は使わない表記です。「%s」を使ってください。", forbidden, t.Name),
					})
				}
				// 英語識別子の併記が用語集と食い違う場合。
				if mismatched := mismatchedEnglish(line, t); mismatched != "" {
					out = append(out, Violation{
						Check: CheckTermMismatch, Severity: SeverityError,
						File: c.FileName, Line: i + 1, Target: t.Name,
						Message: fmt.Sprintf("「%s」の英語識別子が用語集と異なります（%s ではなく %s）。",
							t.Name, mismatched, t.NameEn),
					})
				}
			}
		}
	}
	return out
}

// englishPairRe は「用語（English Identifier）」の併記を取り出す。
var englishPairRe = regexp.MustCompile(`（([A-Za-z][A-Za-z0-9 _-]*)）`)

// mismatchedEnglish は行内で当該用語に併記された英語識別子が用語集と違う場合にその値を返す。
func mismatchedEnglish(line string, t projectstore.Term) string {
	idx := strings.Index(line, t.Name+"（")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(t.Name):]
	m := englishPairRe.FindStringSubmatch(rest)
	if m == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(m[1]), strings.TrimSpace(t.NameEn)) {
		return ""
	}
	return m[1]
}

// checkAmbiguousWords は V5（曖昧語。警告区分）。
func checkAmbiguousWords(in VerifyInput) []Violation {
	words := in.AmbiguousWords
	if len(words) == 0 {
		words = DefaultAmbiguousWords
	}
	var out []Violation
	for _, c := range in.Chapters {
		for i, line := range strings.Split(c.Body, "\n") {
			for _, w := range words {
				if w == "" || !strings.Contains(line, w) {
					continue
				}
				out = append(out, Violation{
					Check: CheckAmbiguousWord, Severity: SeverityWarning,
					File: c.FileName, Line: i + 1, Target: w,
					Message: fmt.Sprintf("「%s」は曖昧語です。測定可能な条件へ書き換えてください。", w),
				})
			}
		}
	}
	return out
}

// checkAcceptanceCriteria は V6（受け入れ条件の欠落。警告区分）。
// 判定元は要件項目の acceptance_criteria（本文の記述では判定しない。AI の書きぶりで結果が揺れないように）。
func checkAcceptanceCriteria(in VerifyInput) []Violation {
	var out []Violation
	for _, r := range in.Records.Requirements {
		if r.HasAcceptanceCriteria() {
			continue
		}
		out = append(out, Violation{
			Check: CheckAcceptanceCriteria, Severity: SeverityWarning, Target: r.ID,
			Message: fmt.Sprintf("%s に受け入れ条件がありません。", r.ID),
		})
	}
	return out
}
