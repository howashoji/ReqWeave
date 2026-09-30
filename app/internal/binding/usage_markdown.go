package binding

// 本ファイルは AI 利用量ダッシュボードの表示内容を Markdown へ書き出す（ファイル出力とクリップボードコピー）。
//
// **保存とコピーは同じ本文を使う**（出力経路で内容が変わらないよう、生成はここ 1 か所）。
// 次プロジェクトの見積材料として読むため、グラフではなく数値の表で書く
// （ダッシュボードで数値を必ず併記するのと同じ方針）。

import (
	"fmt"
	"strings"
	"time"
)

// usageMarkdown は横断一覧（＋あれば単体詳細）を Markdown で組み立てる。
func usageMarkdown(view UsageDashboardView, from, to time.Time) string {
	var b strings.Builder
	b.WriteString("# AI 利用量\n\n")
	fmt.Fprintf(&b, "- 対象期間: %s 〜 %s\n", view.From, view.To)
	fmt.Fprintf(&b, "- 出力日時: %s\n\n", time.Now().In(time.Local).Format("2006-01-02 15:04"))

	b.WriteString("## 1. プロジェクト横断\n\n")
	if len(view.Projects) == 0 {
		b.WriteString("（集計対象のプロジェクトがありません）\n")
	} else {
		b.WriteString("| プロジェクト | トークン消費 | 直近の利用 | 上限 | 消費率 | 実績なしの送信 |\n")
		b.WriteString("|---|---:|---|---:|---:|---:|\n")
		for _, p := range view.Projects {
			if !p.Aggregated {
				fmt.Fprintf(&b, "| %s | 集計不能 | — | — | — | — |\n", usageCell(projectLabel(p)))
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %d |\n",
				usageCell(projectLabel(p)), p.Tokens, localTime(p.LastUsedAt),
				intOrDash(p.LimitTokens), percentOrDash(p.ConsumptionRatio), p.MissingRecords)
		}
		b.WriteString("\n")
		for _, p := range view.Projects {
			if !p.Aggregated && p.Notice != "" {
				fmt.Fprintf(&b, "- %s: %s\n", projectLabel(p), p.Notice)
			}
		}
	}

	if view.Detail == nil {
		return b.String()
	}
	d := view.Detail
	fmt.Fprintf(&b, "\n## 2. %s の内訳\n\n", d.TargetSystemName)
	fmt.Fprintf(&b, "- トークン消費合計: %d（入力 %d / 出力 %d / 推論 %d）\n",
		d.Tokens, d.TokensIn, d.TokensOut, d.TokensReason)
	fmt.Fprintf(&b, "- 送信件数: %d 件（うちトークン実績なし: %d 件）\n", d.Sends, d.MissingRecords)
	fmt.Fprintf(&b, "- 直近の利用: %s\n", localTime(d.LastUsedAt))
	if d.LimitTokens != nil {
		fmt.Fprintf(&b, "- 上限: %d（消費率 %s）\n", *d.LimitTokens, percentOrDash(d.ConsumptionRatio))
	} else {
		b.WriteString("- 上限: 未設定\n")
	}
	b.WriteString("\n")
	writeBuckets(&b, "プロバイダ別", d.ByProvider)
	writeBuckets(&b, "対話セッション別", d.BySession)
	writeBuckets(&b, "作業者別", d.ByAuthor)

	s := d.Sessions
	b.WriteString("\n## 3. セッション統計\n\n")
	fmt.Fprintf(&b, "- 対話セッション数: %d 件", s.SessionsTotal)
	if len(s.SessionsByPhase) > 0 {
		var parts []string
		for _, p := range s.SessionsByPhase {
			parts = append(parts, fmt.Sprintf("%s %d 件", phaseLabel(p.Phase), p.Count))
		}
		fmt.Fprintf(&b, "（%s）", strings.Join(parts, " / "))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- 質問票: 発行 %d 件 ／ 取込 %d 件", s.QuestionnairesIssued, s.QuestionnairesImported)
	if s.QuestionnairesImported > 0 {
		fmt.Fprintf(&b, "（発行から取込まで 平均 %.1f 日）", s.ElapsedDaysAverage)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- 承認された決定事項: %d 件\n", s.DecisionsApproved)
	fmt.Fprintf(&b, "- 決着した未決事項: %d 件\n", s.OpenIssuesResolved)
	fmt.Fprintf(&b, "- 確定した要件項目: %d 件\n", s.RequirementsConfirmed)
	if len(s.ImportedFlows) > 0 {
		b.WriteString("\n| 質問票 | 宛先 | 発行 | 取込 | 経過日数 |\n|---|---|---|---|---:|\n")
		for _, f := range s.ImportedFlows {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %d |\n", f.ID, usageCell(f.Addressee),
				f.IssuedAt.In(time.Local).Format("2006-01-02"),
				f.ImportedAt.In(time.Local).Format("2006-01-02"), f.ElapsedDays)
		}
	}
	return b.String()
}

func writeBuckets(b *strings.Builder, title string, buckets []UsageBucketView) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(buckets) == 0 {
		b.WriteString("（この期間の送信はありません）\n\n")
		return
	}
	b.WriteString("| 区分 | トークン消費 | 送信件数 | 実績なし |\n|---|---:|---:|---:|\n")
	for _, x := range buckets {
		fmt.Fprintf(b, "| %s | %d | %d | %d |\n", usageCell(bucketLabel(x.Key)), x.Tokens, x.Sends, x.Missing)
	}
	b.WriteString("\n")
}

// bucketLabel は内訳のキーを利用者向けの表記にする（空文字 = セッション文脈を持たない送信）。
func bucketLabel(key string) string {
	if strings.TrimSpace(key) == "" {
		return "対話セッション外（取り込み分析など）"
	}
	return key
}

func projectLabel(p ProjectUsageRow) string {
	if strings.TrimSpace(p.TargetSystemName) != "" {
		return p.TargetSystemName
	}
	return p.Path
}

// localTime は RFC3339 の日時をローカルの表示形式にする（空なら「—」）。
func localTime(value string) string {
	if value == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "—"
	}
	return t.In(time.Local).Format("2006-01-02 15:04")
}

func intOrDash(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v)
}

func percentOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", *v*100)
}

// usageCell は表のセルで縦棒・改行が崩れないようにする。
func usageCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
