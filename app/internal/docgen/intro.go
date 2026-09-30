package docgen

// 本ファイルは開発AI向け導入ファイルとフィードバック記入用定型ファイル、
// エクスポートの検証レポートを担う。

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// buildIntro は導入ファイル（CLAUDE.md 相当。8 章）を組み立てる。
func buildIntro(c exportContent, files []string) string {
	var b strings.Builder
	b.WriteString("# 開発AI向け導入ファイル\n\n")

	b.WriteString("## 1. 対象システム概要\n\n")
	fmt.Fprintf(&b, "- 対象システム: %s\n", c.Project.TargetSystemName)
	if c.Project.Summary != "" {
		fmt.Fprintf(&b, "- 概要: %s\n", c.Project.Summary)
	}
	fmt.Fprintf(&b, "- 生成元: ReqWeave（%s 出力）\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- 収載: 要件定義書%s%s\n", versionLabel(c.RequirementsAt), basicDesignLabel(c))
	if background := firstLine(chapterBody(c.Requirements, "background")); background != "" {
		fmt.Fprintf(&b, "- 業務背景（先頭 1 行）: %s\n", background)
	}

	b.WriteString("\n## 2. 読み順\n\n")
	b.WriteString("1. 用語集（00-project/glossary.md）\n")
	b.WriteString("2. 要件定義書（10-requirements/00-index.md から順に）\n")
	b.WriteString("3. 決定事項リスト（00-project/decisions.md）・未決事項リスト（00-project/issues.md）\n")
	if len(c.BasicDesign) > 0 {
		b.WriteString("4. 基本設計書（20-basic-design/01 から順に）\n")
	}
	b.WriteString("\n同梱ファイル:\n\n")
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, f := range sorted {
		fmt.Fprintf(&b, "- [%s](%s)\n", f, f)
	}

	b.WriteString("\n## 3. ID 規約\n\n")
	b.WriteString("- 要件項目: `FR-<グループ>-nnn`（機能）/ `NFR-<グループ>-nnn`（非機能）\n")
	b.WriteString("- ユースケース: `UC-nn` ／ 決定事項: `DEC-nnn` ／ 未決事項: `ISS-nnn`\n")
	b.WriteString("- 設計要素: `BD-<グループ>-nnn`\n")
	b.WriteString("- 採番はプロジェクト内の連番。一度発番した ID は変更・再利用しない。\n")
	b.WriteString("- **文書間・コードとの対応づけは必ず ID で行う**（本文の言い回しで参照しない）。\n")

	b.WriteString("\n## 4. 用語集の位置と遵守\n\n")
	b.WriteString("- `00-project/glossary.md` が用語の正本。同義語を新造しない。\n")
	b.WriteString("- 「使わない表記」列の語をコード・コメント・ドキュメントで使わない。\n")
	b.WriteString("- 命名には用語集の英語識別子を使う。\n")

	b.WriteString("\n## 5. 未決事項の扱い\n\n")
	b.WriteString("- `00-project/issues.md` の未決事項を推測で補完実装しない。\n")
	b.WriteString("- 未決事項がブロックする要件項目は実装を保留し、決着後に着手する。\n")
	b.WriteString("- 判断に迷う箇所は実装せず、フィードバックとして返す（第 7 章）。\n")

	b.WriteString("\n## 6. 実装時の遵守事項\n\n")
	b.WriteString("- 各要件項目の受け入れ条件を満たすことをテストで検証する。\n")
	b.WriteString("- 要件 ID とコード変更の対応を記録する（追跡連鎖を実装まで延長する）。\n")
	b.WriteString("- スコープ外の機能を追加しない（スコープは 10-requirements/02-scope.md）。\n")

	b.WriteString("\n## 7. フィードバックの返し方\n\n")
	fmt.Fprintf(&b, "- 質問・指摘・修正依頼は [%s](%s) に記入して返してください。\n",
		FeedbackTemplateFileName, FeedbackTemplateFileName)
	b.WriteString("- 1 ブロック 1 論点で書き、関連 ID は正確に転記してください（推測の混入は避けてください）。\n")

	b.WriteString("\n## 8. 未解決事項\n\n")
	if !c.WithWarnings {
		b.WriteString("検証合格（整合性検証 V1〜V6 の違反はありません）。\n")
	} else {
		fmt.Fprintf(&b, "警告付きで出力しています（エラー %d 件 / 警告 %d 件）。詳細は [%s](%s) を参照してください。\n\n",
			c.Verification.Errors(), c.Verification.Warnings(), ExportReportFileName, ExportReportFileName)
		for _, v := range c.Verification.Violations {
			fmt.Fprintf(&b, "- %s（%s）: %s\n", v.Check, v.Severity, v.Message)
		}
	}
	return b.String()
}

func versionLabel(version int) string {
	if version > 0 {
		return fmt.Sprintf(" v%d", version)
	}
	return "（ドラフト）"
}

func basicDesignLabel(c exportContent) string {
	if len(c.BasicDesign) == 0 {
		return ""
	}
	return " / 基本設計書" + versionLabel(c.BasicDesignAt)
}

// chapterBody は章観点 ID から本文を引く。
func chapterBody(chapters []projectstore.DocumentChapter, chapter string) string {
	for _, c := range chapters {
		if c.Chapter == chapter {
			// 冒頭メタ節（見出しと箇条書き）を飛ばして本文の先頭を返す。
			for _, line := range strings.Split(c.Body, "\n") {
				t := strings.TrimSpace(line)
				if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") {
					continue
				}
				return t
			}
		}
	}
	return ""
}

// FeedbackTemplate はフィードバック記入用の定型ファイルの本文を返す。
//
// 見出し構造は取り込み側（フィードバックの取り込み）のパースの正本であり、変更はアプリの版更新で両者同時に行う。
// 取り込み側が同一形式を保っていることを検証できるよう公開している。
func FeedbackTemplate() string { return buildFeedbackTemplate() }

// buildFeedbackTemplate はフィードバック記入用の定型ファイルを組み立てる。
func buildFeedbackTemplate() string {
	var b strings.Builder
	b.WriteString("# フィードバック記入用テンプレート\n\n")
	b.WriteString("- 1 ブロック 1 論点で記入してください（ブロックは複数記入できます）。\n")
	b.WriteString("- 関連 ID は成果物からそのまま転記してください（推測で書かない）。\n")
	b.WriteString("- 記入後、このファイルを担当者へ返してください。\n\n")

	b.WriteString("## 記入例\n\n")
	b.WriteString("### フィードバック 1\n\n")
	b.WriteString("- 種別: 質問\n")
	b.WriteString("- 関連 ID: FR-INV-001\n")
	b.WriteString("- 内容: 在庫引当の対象に予約在庫を含めるかが読み取れませんでした。\n")
	b.WriteString("- 実装への影響: 該当箇所の実装を保留しています。\n\n")

	b.WriteString("## 記入欄\n\n")
	b.WriteString("### フィードバック 2\n\n")
	b.WriteString("- 種別: （質問 / 指摘 / 修正依頼 のいずれか）\n")
	b.WriteString("- 関連 ID: （FR-/NFR-/UC-/DEC-/ISS-/BD- 形式。不明なら空欄）\n")
	b.WriteString("- 内容: \n")
	b.WriteString("- 実装への影響: （任意）\n")
	return b.String()
}

// buildExportReport は検証結果のレポートを組み立てる。
func buildExportReport(c exportContent) string {
	var b strings.Builder
	b.WriteString("# エクスポート検証レポート\n\n")
	fmt.Fprintf(&b, "- 対象システム: %s\n", c.Project.TargetSystemName)
	fmt.Fprintf(&b, "- 対象: 要件定義書%s%s\n", versionLabel(c.RequirementsAt), basicDesignLabel(c))
	fmt.Fprintf(&b, "- 実行日時: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- 結果: エラー %d 件 / 警告 %d 件\n\n",
		c.Verification.Errors(), c.Verification.Warnings())

	if c.Verification.Passed() {
		b.WriteString("整合性検証 V1〜V6 の違反はありません（検証合格）。\n")
		return b.String()
	}
	b.WriteString("| 項目 | 区分 | 対象文書 | 行 | 対象 | 内容 |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, v := range c.Verification.Violations {
		line := "—"
		if v.Line > 0 {
			line = fmt.Sprintf("%d", v.Line)
		}
		file := v.File
		if file == "" {
			file = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			v.Check, severityLabel(v.Severity), file, line, cell(v.Target), cell(v.Message))
	}
	return b.String()
}

func severityLabel(severity string) string {
	if severity == SeverityError {
		return "エラー"
	}
	return "警告"
}
