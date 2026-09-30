package docgen

// 本ファイルは機械組立て文書（目次・用語集・決定事項リスト・未決事項リスト）を担う。
//
// レコードから機械的に組み立てるだけで AI 呼び出しを行わない（記録した内容を AI に書き換えさせない）。
// 形式は UTF-8・LF・見出し 3 階層以内・冒頭メタは箇条書き。

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// AssembleInput は機械組立ての入力。
type AssembleInput struct {
	Document    *DocumentTemplate
	Records     Records
	TargetName  string
	GeneratedAt time.Time
	// IncludeBasicDesign は目次に基本設計書を含めるか（同梱時のみ）。
	IncludeBasicDesign bool
}

// AssembleIndex は目次・表記規約（00-index.md）を組み立てる。
func AssembleIndex(in AssembleInput) string {
	var b strings.Builder
	writeMeta(&b, "目次・表記規約", in)
	b.WriteString("\n## 1. 文書一覧\n\n")
	for _, c := range in.Document.Chapters {
		if c.Chapter == "index" {
			continue
		}
		fmt.Fprintf(&b, "- [%s](%s)\n", c.Title, c.File)
	}
	tmpl, err := LoadTemplate()
	if err == nil {
		b.WriteString("\n## 2. 共通文書\n\n")
		for _, c := range tmpl.Common {
			fmt.Fprintf(&b, "- [%s](../%s/%s)\n", c.Title, c.Dir, c.File)
		}
	}
	b.WriteString("\n## 3. 表記規約\n\n")
	b.WriteString("- 本文は日本語を正とし、章見出し・ID のグループ名・用語には英語識別子を併記する。\n")
	b.WriteString("- 文書間の参照は ID で行う（FR-/NFR-/UC-/DEC-/ISS-/BD- 形式）。\n")
	b.WriteString("- 図は Mermaid のフェンスブロックで記す（画像ファイルを使わない）。\n")
	b.WriteString("- 用語は用語集を正本とし、同義語を新造しない。\n")
	return b.String()
}

// AssembleGlossary は用語集を組み立てる（AI 生成による書き換えを行わない）。
func AssembleGlossary(in AssembleInput) string {
	var b strings.Builder
	writeMeta(&b, "用語集（glossary）", in)
	b.WriteString("\n| 用語 | 英語識別子 | 定義 | 使わない表記 |\n")
	b.WriteString("|---|---|---|---|\n")
	terms := append([]projectstore.Term(nil), in.Records.Terms...)
	sort.Slice(terms, func(i, j int) bool { return terms[i].Name < terms[j].Name })
	for _, t := range terms {
		forbidden := "—"
		if len(t.Forbidden) > 0 {
			forbidden = strings.Join(t.Forbidden, "、")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			cell(t.Name), cell(t.NameEn), cell(t.Definition), cell(forbidden))
	}
	if len(terms) == 0 {
		b.WriteString("| （未登録） | — | — | — |\n")
	}
	return b.String()
}

// AssembleDecisions は決定事項リストを組み立てる（追記のみの内容を反映する）。
func AssembleDecisions(in AssembleInput) string {
	var b strings.Builder
	writeMeta(&b, "決定事項リスト（decisions）", in)
	b.WriteString("\n| ID | 論点キー | 決定日 | 決定内容 | 根拠 | 置き換え |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	decisions := append([]projectstore.Decision(nil), in.Records.Decisions...)
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ID < decisions[j].ID })
	for _, d := range decisions {
		replaced := "—"
		switch {
		case d.SupersededBy != "":
			replaced = d.SupersededBy + " で置き換え"
		case d.Supersedes != "":
			replaced = d.Supersedes + " を置き換え"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			d.ID, cell(d.TopicKey), d.DecidedAt.Format("2006-01-02"),
			cell(firstLine(d.Body)), cell(strings.Join(d.Evidence, " ")), replaced)
	}
	if len(decisions) == 0 {
		b.WriteString("| （なし） | — | — | — | — | — |\n")
	}
	b.WriteString("\n決定事項は追記のみで記録する。覆す場合は新しい決定による置き換えとして記録し、旧決定の本文は書き換えない。\n")
	return b.String()
}

// AssembleIssues は未決事項リストを組み立てる（誰が・いつまでに・何を を含む）。
func AssembleIssues(in AssembleInput) string {
	var b strings.Builder
	writeMeta(&b, "未決事項リスト（issues）", in)
	b.WriteString("\n| ID | 論点（何を決めるか） | 決める人 | 期限 | 状態 | ブロック対象 | 決着 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")

	blocking := map[string][]string{}
	for _, r := range in.Records.Requirements {
		for _, id := range r.BlockedBy {
			blocking[id] = append(blocking[id], r.ID)
		}
	}
	issues := append([]projectstore.OpenIssue(nil), in.Records.OpenIssues...)
	sort.Slice(issues, func(i, j int) bool { return issues[i].ID < issues[j].ID })
	for _, i := range issues {
		due := i.Due
		if due == "" {
			due = "未定"
		}
		blocked := "—"
		if ids := blocking[i.ID]; len(ids) > 0 {
			sort.Strings(ids)
			blocked = strings.Join(ids, ", ")
		}
		resolved := "—"
		if i.ResolvedBy != "" {
			resolved = i.ResolvedBy
		}
		status := "未決"
		if i.Status == projectstore.OpenIssueResolved {
			status = "決着"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			i.ID, cell(firstLine(i.Body)), cell(i.Owner), due, status, blocked, resolved)
	}
	if len(issues) == 0 {
		b.WriteString("| （なし） | — | — | — | — | — | — |\n")
	}
	b.WriteString("\n未決事項は推測で補完しない。ブロックされた要件項目は決着後に着手する。\n")
	return b.String()
}

// writeMeta は各文書の冒頭メタ節を箇条書きで書く。
func writeMeta(b *strings.Builder, title string, in AssembleInput) {
	fmt.Fprintf(b, "# %s\n\n", title)
	fmt.Fprintf(b, "- 対象システム: %s\n", in.TargetName)
	fmt.Fprintf(b, "- 状態: 生成済み\n")
	fmt.Fprintf(b, "- 生成日時: %s\n", in.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(b, "- 入力: プロジェクトの決定事項・未決事項・要件項目・用語（レコードからの機械組立て）\n")
}

// cell は表のセルとして安全な 1 行文字列にする。
func cell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
