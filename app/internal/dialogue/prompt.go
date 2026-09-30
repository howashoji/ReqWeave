package dialogue

// 本ファイルはシステムプロンプトの構成を担う。
//
// 5 節を固定順で構成する。追加の質問観点（プリセット観点・プロジェクト観点）は節 2 の末尾へ差し込む。

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// プロンプトの用途（節 5 の出力契約を切り替える）。
const (
	// ModeQuestion は質問生成。
	ModeQuestion = "question"
	// ModeExtraction は回答からの構造化抽出。
	ModeExtraction = "extraction"
)

// SystemPromptInput はシステムプロンプトの組み立て入力。
type SystemPromptInput struct {
	Phase  string
	Effort aiprovider.Effort
	Mode   string
	// ExtraPerspectives は追加の質問観点（プリセット・プロジェクト観点。無ければ空）。
	ExtraPerspectives []string
}

// BuildSystemPrompt はシステムプロンプトの 5 節を固定順で組み立てる。
func BuildSystemPrompt(in SystemPromptInput) (string, error) {
	m, err := LoadMetaModel()
	if err != nil {
		return "", err
	}
	phase, err := m.Phase(in.Phase)
	if err != nil {
		return "", err
	}
	switch in.Mode {
	case ModeQuestion, ModeExtraction, ModeQuestionnaire, ModeImportAnalysis,
		ModeMaterialAnalysis, ModeFeedbackAnalysis:
	default:
		return "", fmt.Errorf("プロンプトの用途が不正です: %q", in.Mode)
	}

	var b strings.Builder
	b.WriteString("## 1. 役割\n\n")
	b.WriteString(roleSection(in.Phase))

	b.WriteString("\n\n## 2. メタモデル定義（章観点と必須項目）\n\n")
	b.WriteString(metaModelSection(phase, in.ExtraPerspectives))

	b.WriteString("\n\n## 3. 対話規律\n\n")
	b.WriteString(disciplineSection(in.Phase))

	b.WriteString("\n\n## 4. エフォート制御値\n\n")
	b.WriteString(effortSection(ControlsFor(in.Effort)))

	b.WriteString("\n\n## 5. 出力契約\n\n")
	b.WriteString(outputContractSection(in.Mode))
	b.WriteString("\n")
	return b.String(), nil
}

// roleSection は 1. 役割定義（現在フェーズ・応答は日本語）。
func roleSection(phase string) string {
	target := "要件定義"
	if phase == PhaseBasicDesign {
		target = "基本設計"
	}
	return fmt.Sprintf(
		"あなたは対象システムの%sを対話で進める AIエージェントです。"+
			"システム担当者との一問一答で論点を詰め、後工程がそのまま使える精度の記録を作ることが目的です。\n"+
			"応答は日本語で書きます。", target)
}

// metaModelSection は 2. メタモデル定義（現在フェーズの章観点と必須項目リスト）。
func metaModelSection(phase *Phase, extra []string) string {
	var b strings.Builder
	b.WriteString("次の章観点と必須項目を埋めることが対話の目的です（並び順が確認の優先順です）。\n")
	for _, c := range phase.Chapters {
		fmt.Fprintf(&b, "\n- %s（%s）\n", c.Name, c.ID)
		for _, item := range c.Items {
			fmt.Fprintf(&b, "  - %s（論点キー: %s）\n", item.Name, c.TopicKey(item.ID))
		}
	}
	if len(extra) > 0 {
		b.WriteString("\n追加の質問観点:\n")
		for _, e := range extra {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// disciplineSection は 3. 対話規律（一問一答・背景説明・曖昧語の変換・既決論点の再質問禁止）。
func disciplineSection(phase string) string {
	lines := []string{
		"- 質問は 1 件ずつ出します。複数の論点をまとめて聞きません。",
		"- 各質問に背景説明（なぜ聞くか・何が決まるか）を必ず添えます。",
		"- 「速い」「使いやすい」などの曖昧語は、測定可能な受け入れ条件へ変換した反映案を作ります。",
		"- 不明な事実を推測で補完しません。分からないことは未決事項の候補として扱います。",
		"- 既に決定済みの論点は再質問しません（決定済み内容の変更を目的とした再確認を除く）。",
		"- 質問には対象の論点キーを必ず併記します。",
	}
	if phase == PhaseBasicDesign {
		lines = append(lines,
			"- 質問には根拠となる要件項目 ID と、選択肢（それぞれのトレードオフつき）を必ず添えます。")
	}
	return strings.Join(lines, "\n")
}

// effortSection は 4. エフォート制御値（当該段階の実値）。
func effortSection(c EffortControls) string {
	return strings.Join([]string{
		fmt.Sprintf("- 1 論点あたりの追問は最大 %d 往復までです。上限に達したら次の論点へ移ります。", c.FollowUpLimit),
		"- " + c.CriteriaRule + "。",
		"- " + c.OpenIssueRule + "。",
		"- 要件項目への反映案は「" + c.ReflectionScope + "」の範囲で作ります。",
	}, "\n")
}

// outputContractSection は 5. 出力契約（質問提示形式 と 構造化出力時の補足規則）。
//
// スキーマ本体はプロンプトへ書かない（出力の形式はプロバイダの構造化出力機能で保証するため）。構造化出力を要求するモードでは
// ChatRequest.ResponseSchema でプロバイダの構造化出力機能へ渡す（SchemaForMode）。
// ここに残すのは、スキーマだけでは表せない**埋め方の規則**（どの ID を入れるか等）である。
func outputContractSection(mode string) string {
	if mode == ModeQuestionnaire {
		return "ステークホルダーへ渡す質問文を、指定された JSON スキーマに従って出力してください（前後に説明文を書かない）。\n\n" +
			"- 未決事項 1 件につき 1 件以上の質問を作り、source_open_issue_id にその未決事項の ID を入れます。\n" +
			"- 質問文・背景説明には本システムの内部 ID（FR-/NFR-/ISS-/DEC- 等）と要件定義の専門用語を使わず、業務側の言葉で書きます。\n" +
			"- 回答に必要な用語は背景説明の中で説明します（別の用語集を参照させない）。\n" +
			"- 「不明」の選択肢は作りません（回答画面が常に用意します）。\n" +
			"- 選択肢を作らない質問の answer_format は free とし、choices は空配列にします。"
	}
	if mode == ModeFeedbackAnalysis {
		return "開発AIから返ってきたフィードバックの分析結果を、指定された JSON スキーマに従って出力してください（前後に説明文を書かない）。\n\n" +
			"- 各候補の evidence_refs には、根拠となるフィードバックの該当箇所（IMP-nnn#Lm-Ln の形式）を 1 件以上入れます。\n" +
			"- 各候補の related_ids には、フィードバックが指している既存の要件項目・設計要素の ID を入れます（不明なら空配列）。\n" +
			"- 種別が「質問」の論点は open_issues（未決事項候補）に出します。\n" +
			"- 種別が「指摘」「修正依頼」の論点は requirement_updates に出し、既存の要件項目を直す場合は\n" +
			"  operation を update、対象の要件項目 ID を target_id に入れます（要件変更 = 差し戻しの候補になります）。\n" +
			"- 成果物で回答できる確認は、要件の変更ではなく open_issues として出します。\n" +
			"- フィードバックに書かれていないことを推測で補いません。該当する候補が無い配列は空配列にします（省略しない）。"
	}
	if mode == ModeMaterialAnalysis {
		return "取り込んだ資料の分析結果を、指定された JSON スキーマに従って出力してください（前後に説明文を書かない）。\n\n" +
			"- 各候補の evidence_refs には、根拠となる資料の該当箇所（IMP-nnn#Lm-Ln の形式。L は資料本文の行番号）を 1 件以上入れます。\n" +
			"- 資料の見出し行に添えた行範囲を基準に、実際に根拠がある行だけを指してください（範囲外の行番号は使わない）。\n" +
			"- 既存の決定事項・未決事項・要件項目と同じ内容の候補には duplicate_of へその既存レコードの ID を入れます。\n" +
			"- 既存の決定と食い違う内容は contradictions に出し、with_decision_id へその決定の ID を入れます。\n" +
			"- 資料から読み取れる「このプロジェクトで今後確認すべき観点」は perspective_candidates に出します（質問文ではなく観点の名称と要旨）。\n" +
			"- 議事録では、既存の未決事項の決着に相当する内容を decisions（決着案）として出します。\n" +
			"- 資料に書かれていないことを推測で補いません。該当する候補が無い配列は空配列にします（省略しない）。"
	}
	if mode == ModeImportAnalysis {
		return "ステークホルダーの回答の分析結果を、指定された JSON スキーマに従って出力してください（前後に説明文を書かない）。\n\n" +
			"- 各候補の evidence_refs には、根拠となる回答 ID（QS-nnn#q-nn）を 1 件以上入れます（発話 ID は使いません）。\n" +
			"- 「不明」と回答された質問からは決着案（decisions）を作りません。決められない理由が新しい論点なら open_issues に出します。\n" +
			"- 既存の決定と食い違う回答は contradictions に出し、with_decision_id へその決定の ID を入れます。\n" +
			"- 該当する候補が無い配列は空配列にします（省略しない）。"
	}
	if mode == ModeExtraction {
		return "回答の分析結果を、指定された JSON スキーマに従って出力してください（前後に説明文を書かない）。\n\n" +
			"- 各候補の evidence_refs には、根拠となる発話 ID（S-nnnn#utt-nnnnn）を 1 件以上入れます。\n" +
			"- 新規の要件項目（operation: create）には id_group を付けます（英大文字と数字 2〜8 文字の略号。" +
			"同じまとまりの要件には同じ略号を使い、既に使っているグループがあれば流用します。分からなければ null）。\n" +
			"- 該当する候補が無い配列は空配列にします（省略しない）。"
	}
	return strings.Join([]string{
		"次の形式で 1 件だけ質問を出力してください。",
		"",
		"```",
		"論点キー: <章観点ID/必須項目ID>",
		"質問: <質問文（1 件）>",
		"背景: <なぜ聞くか・何が決まるか>",
		"```",
	}, "\n")
}
