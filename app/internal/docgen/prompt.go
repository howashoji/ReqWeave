package docgen

// 本ファイルは章生成のプロンプト構成を担う。
//
// 送信するのは当該章のソースレコード全文・用語集・章仕様・形式規約のみ
//（AI へ送る範囲を必要最小限に限る。他章のレコード・アプリ設定・端末情報を載せる経路を持たない）。

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 送信文脈の内訳ラベル（AI 呼び出しの記録に「何を送ったか」として残す）。
const (
	LabelChapterSpec  = "chapter-spec"
	LabelRequirements = "requirements"
	LabelDecisions    = "decisions"
	LabelOpenIssues   = "open-issues"
	LabelTerms        = "terms"
)

// buildSystemPrompt は章生成のシステムプロンプトを組み立てる。
func buildSystemPrompt(doc *DocumentTemplate, chapter ChapterTemplate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "あなたは%sの「%s（%s）」章を書く担当者です。応答は日本語で書きます。\n\n",
		doc.Name, chapter.Title, chapter.Chapter)

	b.WriteString("## 書き方の規律\n\n")
	b.WriteString("- 与えられたレコード（要件項目・決定事項・未決事項・用語）に書かれている内容だけで書きます。\n")
	b.WriteString("- **推測で補完しません**。決まっていない事柄は、該当する未決事項の ID と論点を本文に明示します。\n")
	b.WriteString("- 要件項目・決定事項・未決事項への参照は必ず ID で書きます（FR-/NFR-/UC-/DEC-/ISS-/BD- 形式）。\n")
	b.WriteString("- 用語は与えられた用語集の表記を使い、同義語を新造しません。\n")
	b.WriteString("- 「速い」「使いやすい」などの曖昧語を使わず、測定可能な条件で書きます。\n\n")

	b.WriteString("## 形式\n\n")
	b.WriteString("- Markdown。見出しは `##` と `###` の 2 階層まで（章題の `#` は出力しない）。\n")
	b.WriteString("- 図が必要なときは Mermaid のフェンスブロックで書きます（画像は使いません）。\n")
	b.WriteString("  概念モデル = classDiagram / 状態遷移 = stateDiagram-v2 / 業務フロー・画面遷移 = flowchart / 時系列 = sequenceDiagram\n")
	b.WriteString("- 本文だけを出力します（前置き・後書き・コードフェンスでの全体囲みをしません）。\n")
	return b.String()
}

// buildUserPrompt は当該章のソースレコードを載せた指示を組み立て、文脈の内訳ラベルを返す。
func buildUserPrompt(src ChapterSources) (string, []string) {
	var b strings.Builder
	labels := []string{LabelChapterSpec}

	fmt.Fprintf(&b, "## 章の仕様\n\n- 章: %s（%s）\n- ファイル: %s\n\n",
		src.Chapter.Title, src.Chapter.Chapter, src.Chapter.File)

	if len(src.Requirements) > 0 {
		labels = append(labels, LabelRequirements)
		b.WriteString("## この章に収載する要件項目\n\n")
		for _, r := range src.Requirements {
			fmt.Fprintf(&b, "### %s %s\n- 種別: %s / 優先度: %s / 状態: %s\n\n%s\n",
				r.ID, r.Title, r.Kind, r.Priority, r.Status, strings.TrimSpace(r.Body))
			if len(r.AcceptanceCriteria) > 0 {
				b.WriteString("\n受け入れ条件:\n")
				for _, c := range r.AcceptanceCriteria {
					fmt.Fprintf(&b, "- %s\n", c)
				}
			}
			if len(r.BlockedBy) > 0 {
				fmt.Fprintf(&b, "\nブロックする未決事項: %s\n", strings.Join(r.BlockedBy, ", "))
			}
			b.WriteString("\n")
		}
	}

	if len(src.Decisions) > 0 {
		labels = append(labels, LabelDecisions)
		b.WriteString("## この章に関係する決定事項\n\n")
		for _, d := range src.Decisions {
			if d.SupersededBy != "" {
				fmt.Fprintf(&b, "- %s（%s で置き換え済み。本文には使わない）\n", d.ID, d.SupersededBy)
				continue
			}
			fmt.Fprintf(&b, "### %s\n- 論点キー: %s / 決定日: %s\n\n%s\n\n",
				d.ID, d.TopicKey, d.DecidedAt.Format("2006-01-02"), strings.TrimSpace(d.Body))
		}
	}

	if len(src.OpenIssues) > 0 {
		labels = append(labels, LabelOpenIssues)
		b.WriteString("## この章をブロックしている未決事項（推測で埋めず、ID と論点を本文に明示すること）\n\n")
		for _, i := range src.OpenIssues {
			due := i.Due
			if due == "" {
				due = "未定"
			}
			fmt.Fprintf(&b, "- %s: %s（決める人: %s / 期限: %s）\n",
				i.ID, firstLine(i.Body), i.Owner, due)
		}
		b.WriteString("\n")
	}

	if len(src.Terms) > 0 {
		labels = append(labels, LabelTerms)
		b.WriteString("## 用語集（この表記を使う）\n\n")
		for _, t := range src.Terms {
			fmt.Fprintf(&b, "- %s（%s）: %s\n", t.Name, t.NameEn, firstLine(t.Definition))
		}
		b.WriteString("\n")
	}

	if src.IsEmpty() {
		b.WriteString("## 収載するレコード\n\nこの章に割当てられたレコードはまだありません。" +
			"内容が無いことを 1 文で述べ、推測で補完しないでください。\n\n")
	}

	fmt.Fprintf(&b, "以上のレコードだけを使って「%s」章の本文を書いてください。\n", src.Chapter.Title)
	return b.String(), labels
}

// chapterHeader は章ファイルの冒頭メタ節を返す（箇条書き形式）。
func chapterHeader(chapter ChapterTemplate, targetName string, generatedAt string, covers []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s（%s）\n\n", chapter.Title, chapter.Chapter)
	fmt.Fprintf(&b, "- 対象システム: %s\n", targetName)
	fmt.Fprintf(&b, "- 状態: 生成済み\n")
	fmt.Fprintf(&b, "- 生成日時: %s\n", generatedAt)
	if len(covers) > 0 {
		fmt.Fprintf(&b, "- 収載レコード: %s\n", strings.Join(covers, ", "))
	} else {
		b.WriteString("- 収載レコード: なし\n")
	}
	b.WriteString("\n")
	return b.String()
}

// requirementIDs は収載レコードのうち要件項目の ID を返す（章の covers に記録する）。
func requirementIDs(reqs []projectstore.Requirement) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.ID)
	}
	return out
}
