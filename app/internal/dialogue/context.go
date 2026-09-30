package dialogue

// 本ファイルは AI へ送る文脈の内容と範囲を担う。
//
// 注入する文脈は ContextInput の 7 種に限定する。
// シークレットキー・アプリ設定・他プロジェクトのデータ・端末情報を載せる経路を持たない
// （外部へ送ってはならない情報を、型の上で受け取れないことで構造的に担保する）。

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 送信文脈の内訳ラベル（aiprovider.RecordContext の Included。監査記録に残す文脈種別）。
const (
	LabelCompleteness = "completeness"
	LabelDecisions    = "decisions"
	LabelOpenIssues   = "open-issues"
	LabelRequirements = "requirements"
	LabelTerms        = "terms"
	LabelDocument     = "document-draft"
	// LabelPerspectives は既存の質問観点（プリセット・プロジェクト観点）。
	LabelPerspectives = "perspectives"
	LabelHistory      = "dialogue-history"
)

// ContextInput は文脈注入の入力（注入する文脈の種類と 1 対 1）。
type ContextInput struct {
	// Phase は現在フェーズ（基本設計では根拠要件の載せ方が変わる）。
	Phase string
	// Records は決定事項・未決事項・要件項目・用語（データソース）。
	Records Records
	// Completeness は章観点ごとの充足サマリ（完成度の算出結果）。
	Completeness []ChapterCompleteness
	// CurrentChapter は現在論点の章観点 ID（要件項目・成果物ドラフトの絞り込みに使う）。
	CurrentChapter string
	// CurrentTopicKey は現在論点の論点キー（該当する決定事項のみ全文を載せる）。
	CurrentTopicKey string
	// ChapterDraft は現在論点の章の最新ドラフト本文（未生成なら空）。
	ChapterDraft string
	// History は対話履歴（圧縮した結果）。
	History CompressedHistory
}

// BuildContext は注入する文脈テキストと、その内訳ラベルを返す。
func BuildContext(in ContextInput) (string, []string) {
	var b strings.Builder
	var labels []string

	if len(in.Completeness) > 0 {
		b.WriteString("## 章観点ごとの充足状況\n\n")
		for _, c := range in.Completeness {
			fmt.Fprintf(&b, "- %s（%s）: %d%%（%d/%d）未決 %d 件\n",
				c.Name, c.ChapterID, c.Percent, c.Satisfied, c.Total, c.OpenIssues)
		}
		b.WriteString("\n")
		labels = append(labels, LabelCompleteness)
	}

	if len(in.Records.Decisions) > 0 {
		b.WriteString("## 決定事項（既決の論点。これらを再質問しない）\n\n")
		for _, d := range in.Records.Decisions {
			if d.SupersededBy != "" {
				fmt.Fprintf(&b, "- %s [%s]（%s で置き換え済み）: %s\n",
					d.ID, d.TopicKey, d.SupersededBy, firstLine(d.Body))
				continue
			}
			// 当該論点の継続時のみ全文、それ以外は結論 1 行の要約形（送る量を抑えつつ、既決の論点を AI に知らせる）。
			if in.CurrentTopicKey != "" && d.TopicKey == in.CurrentTopicKey {
				fmt.Fprintf(&b, "- %s [%s]:\n%s\n", d.ID, d.TopicKey, indent(d.Body))
				continue
			}
			fmt.Fprintf(&b, "- %s [%s]: %s\n", d.ID, d.TopicKey, firstLine(d.Body))
		}
		b.WriteString("\n")
		labels = append(labels, LabelDecisions)
	}

	if len(in.Records.OpenIssues) > 0 {
		b.WriteString("## 未決事項\n\n")
		blockedBy := blockedRequirementsByIssue(in.Records.Requirements)
		for _, i := range in.Records.OpenIssues {
			due := i.Due
			if due == "" {
				due = "未定"
			}
			fmt.Fprintf(&b, "- %s: %s ／ 決める人: %s ／ 期限: %s ／ 状態: %s",
				i.ID, firstLine(i.Body), i.Owner, due, i.Status)
			if ids := blockedBy[i.ID]; len(ids) > 0 {
				fmt.Fprintf(&b, " ／ ブロック対象: %s", strings.Join(ids, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		labels = append(labels, LabelOpenIssues)
	}

	if reqs := contextRequirements(in); len(reqs) > 0 {
		b.WriteString("## 要件項目（現在の章観点と、そこから参照される項目）\n\n")
		for _, r := range reqs {
			fmt.Fprintf(&b, "### %s %s（%s / %s）\n%s\n", r.ID, r.Title, r.Status, r.Priority, r.Body)
			if len(r.AcceptanceCriteria) > 0 {
				b.WriteString("受け入れ条件:\n")
				for _, ac := range r.AcceptanceCriteria {
					fmt.Fprintf(&b, "- %s\n", ac)
				}
			}
			b.WriteString("\n")
		}
		labels = append(labels, LabelRequirements)
	} else if summary := designSourceRequirements(in); summary != "" {
		// 基本設計フェーズでは章観点が要件定義側と異なるため、上の絞り込みでは 1 件も載らない。
		// 設計の根拠となる要件項目 ID を質問へ添えられるように要約形で載せる。
		b.WriteString("## 根拠となる要件項目（合意済み）\n\n")
		b.WriteString(summary)
		b.WriteString("\n")
		labels = append(labels, LabelRequirements)
	}

	if len(in.Records.Terms) > 0 {
		b.WriteString("## 用語集（この表記を使う。同義語を新造しない）\n\n")
		for _, t := range in.Records.Terms {
			fmt.Fprintf(&b, "- %s（%s）: %s\n", t.Name, t.NameEn, firstLine(t.Definition))
		}
		b.WriteString("\n")
		labels = append(labels, LabelTerms)
	}

	if strings.TrimSpace(in.ChapterDraft) != "" {
		b.WriteString("## 現在の章の最新ドラフト\n\n")
		b.WriteString(strings.TrimRight(in.ChapterDraft, "\n"))
		b.WriteString("\n\n")
		labels = append(labels, LabelDocument)
	}

	if text := in.History.Text(); text != "" {
		b.WriteString("## これまでの対話\n\n")
		b.WriteString(text)
		b.WriteString("\n")
		labels = append(labels, LabelHistory)
	}

	return strings.TrimRight(b.String(), "\n"), labels
}

// contextRequirements は現在の章観点の要件項目と、それらが本文で ID 参照する要件項目を返す。
func contextRequirements(in ContextInput) []projectstore.Requirement {
	if in.CurrentChapter == "" {
		return nil
	}
	byID := map[string]projectstore.Requirement{}
	for _, r := range in.Records.Requirements {
		byID[r.ID] = r
	}
	var out []projectstore.Requirement
	included := map[string]bool{}
	for _, r := range in.Records.Requirements {
		if r.Chapter != in.CurrentChapter || included[r.ID] {
			continue
		}
		included[r.ID] = true
		out = append(out, r)
	}
	// 参照先（本文中の要件項目 ID）を 1 段だけ追加する。
	for _, r := range append([]projectstore.Requirement(nil), out...) {
		for id := range byID {
			if id == r.ID || included[id] {
				continue
			}
			if strings.Contains(r.Body, id) {
				included[id] = true
				out = append(out, byID[id])
			}
		}
	}
	return out
}

// designSourceRequirements は基本設計フェーズで注入する要件項目の要約を返す（設計の質問に根拠の要件 ID を添えるため）。
//
// 合意済みの要件項目を ID・要件名・受け入れ条件の 1 行で載せる（全文は載せない）。
func designSourceRequirements(in ContextInput) string {
	if in.Phase != PhaseBasicDesign {
		return ""
	}
	var b strings.Builder
	for _, r := range in.Records.Requirements {
		if r.Status != projectstore.RequirementAgreed {
			continue
		}
		fmt.Fprintf(&b, "- %s %s: %s", r.ID, r.Title, firstLine(r.Body))
		if len(r.AcceptanceCriteria) > 0 {
			fmt.Fprintf(&b, "（受け入れ条件: %s）", strings.Join(r.AcceptanceCriteria, " / "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// blockedRequirementsByIssue は未決事項 ID → ブロックする要件項目 ID の対応を返す
// （要件項目側の blocked_by を正とする）。
func blockedRequirementsByIssue(reqs []projectstore.Requirement) map[string][]string {
	out := map[string][]string{}
	for _, r := range reqs {
		for _, issueID := range r.BlockedBy {
			out[issueID] = append(out[issueID], r.ID)
		}
	}
	return out
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
