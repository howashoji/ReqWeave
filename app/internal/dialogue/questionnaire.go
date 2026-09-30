package dialogue

// 本ファイルは翻訳質問文（未決事項をステークホルダー向けの言葉に直した質問票の質問）の生成を担う。
//
// 対話ループとは独立の 1 回呼び出しとして実行する。
// AI API 障害時は論点を転記した空テンプレートを返し、手入力のみで発行を完了できる
//（AI が使えなくても質問票の発行を止めないため）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ModeQuestionnaire は質問票の質問文生成。
const ModeQuestionnaire = "questionnaire"

// QuestionnaireSchemaJSON は質問文生成の出力スキーマ。
//
// ExtractionSchemaJSON と同じく、プロバイダの構造化出力機能へ JSON Schema として
// そのまま渡るため妥当な JSON Schema で書く。
const QuestionnaireSchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["questions"],
  "properties": {
    "questions": {
      "type": "array",
      "description": "ステークホルダーへ渡す質問。未決事項1件につき1件以上",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["source_open_issue_id", "question_text", "background", "answer_format", "choices"],
        "properties": {
          "source_open_issue_id": {"type": "string", "description": "発行元の未決事項ID（ISS-nnn）"},
          "question_text": {"type": "string", "description": "業務側の言葉で書いた質問文"},
          "background": {
            "type": "string",
            "description": "なぜ聞くか・何が決まるか（必要な用語はここで定義する）"
          },
          "answer_format": {
            "enum": ["choice", "multi_choice", "free", "choice_with_free"],
            "description": "回答形式"
          },
          "choices": {
            "type": "array", "items": {"type": "string"},
            "description": "選択肢。answer_format が free のときは空配列"
          }
        }
      }
    }
  }
}`

// QuestionDraft は生成された質問文の草案（担当者が編集・確定してから発行する）。
type QuestionDraft struct {
	SourceOpenIssueID string   `json:"source_open_issue_id"`
	QuestionText      string   `json:"question_text"`
	Background        string   `json:"background"`
	AnswerFormat      string   `json:"answer_format"`
	Choices           []string `json:"choices,omitempty"`
}

// QuestionDrafts は生成結果（出力スキーマに対応する）。
type QuestionDrafts struct {
	Questions []QuestionDraft `json:"questions"`
}

// QuestionDraftResult は質問文生成の結果。
type QuestionDraftResult struct {
	Drafts []QuestionDraft
	// Fallback は AI 呼び出しに失敗し、論点を転記したテンプレートを返したか。
	Fallback bool
	// Notice は縮退したときに画面へ出す 1 文（原因＋次の行動）。
	Notice string
}

// unknownChoiceLabels は選択肢に生成してはならないラベル（回答モード UI が常設する）。
var unknownChoiceLabels = []string{"不明", "わからない", "分からない", "不明（理由・確認先を記入）"}

// GenerateQuestionnaire は未決事項から翻訳質問文を生成する。
//
// AI 呼び出しに失敗した場合もエラーを返さず、論点を転記したテンプレートを Fallback として返す
// （手入力のみで発行を完了できる）。
func (e *Engine) GenerateQuestionnaire(ctx context.Context, openIssueIDs []string) (*QuestionDraftResult, error) {
	issues, err := e.loadOpenIssues(openIssueIDs)
	if err != nil {
		return nil, err
	}
	req, labels, err := e.buildQuestionnaireRequest(issues)
	if err != nil {
		return nil, err
	}

	body, res := e.streamSilently(ctx, "", req, labels)
	if res.err != nil || strings.TrimSpace(body) == "" {
		return fallbackResult(issues, res.cause()), nil
	}
	drafts, err := ParseQuestionDrafts(body)
	if err != nil {
		return fallbackResult(issues, err), nil
	}
	drafts.normalize(issues)
	if len(drafts.Questions) == 0 {
		return fallbackResult(issues, fmt.Errorf("質問文を組み立てられませんでした")), nil
	}
	// 質問が 1 件も付かなかった未決事項にはテンプレートを補う（1 未決事項につき 1 件以上の質問を付ける）。
	drafts.Questions = append(drafts.Questions, templatesForMissing(issues, drafts.Questions)...)
	return &QuestionDraftResult{Drafts: drafts.Questions}, nil
}

// loadOpenIssues は指定された未決事項を読む（未決のものだけを対象にする）。
func (e *Engine) loadOpenIssues(ids []string) ([]projectstore.OpenIssue, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("質問票にする未決事項を選んでください")
	}
	out := make([]projectstore.OpenIssue, 0, len(ids))
	for _, id := range ids {
		issue, err := e.cfg.Store.LoadOpenIssue(id)
		if err != nil {
			return nil, err
		}
		if issue.Status != projectstore.OpenIssueOpen {
			return nil, fmt.Errorf("決着済みの未決事項は質問票にできません（%s）", id)
		}
		out = append(out, *issue)
	}
	return out, nil
}

// buildQuestionnaireRequest は質問文生成のリクエストを組み立てる。
func (e *Engine) buildQuestionnaireRequest(issues []projectstore.OpenIssue) (aiprovider.ChatRequest, []string, error) {
	system, err := BuildSystemPrompt(SystemPromptInput{
		Phase: e.cfg.Store.Project().Phase, Effort: e.cfg.Effort, Mode: ModeQuestionnaire})
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	effort := e.effortParams()

	terms, err := e.cfg.Store.LoadTerms()
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	decisions, err := e.cfg.Store.ListDecisions()
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}

	var b strings.Builder
	labels := make([]string, 0, len(issues)+2)
	b.WriteString("### 未決事項（この論点をステークホルダーへ聞きます）\n\n")
	for _, issue := range issues {
		fmt.Fprintf(&b, "- %s（決める人: %s）: %s\n", issue.ID, issue.Owner, oneLine(issue.Body))
		labels = append(labels, issue.ID)
	}
	if len(terms.Terms) > 0 {
		b.WriteString("\n### 回答の解釈に必要な用語の定義\n\n")
		for _, t := range terms.Terms {
			fmt.Fprintf(&b, "- %s: %s\n", t.Name, oneLine(t.Definition))
		}
		labels = append(labels, projectstore.FileTerms)
	}
	if len(decisions) > 0 {
		b.WriteString("\n### 関連する決定（各 1 行の要約）\n\n")
		for _, d := range decisions {
			fmt.Fprintf(&b, "- %s: %s\n", d.ID, oneLine(d.Body))
		}
		labels = append(labels, "decisions")
	}
	b.WriteString("\n上の未決事項ごとに、業務側の言葉で答えられる質問を作ってください。")

	return aiprovider.ChatRequest{
		Model:    e.cfg.Model.ID,
		System:   system,
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: b.String()}},
		Effort:   effort,
		// 構造化出力。スキーマはプロンプトではなくここで渡す。
		ResponseSchema: SchemaForMode(ModeQuestionnaire),
	}, labels, nil
}

// oneLine は本文を 1 行に畳む（プロンプトの箇条書きに載せるため）。
func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// ParseQuestionDrafts は生成結果の JSON を読む（前後の説明文があっても本体を取り出す）。
func ParseQuestionDrafts(body string) (*QuestionDrafts, error) {
	raw, err := extractJSONObject(body)
	if err != nil {
		return nil, err
	}
	var d QuestionDrafts
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("質問文の形式が不正です: %w", err)
	}
	return &d, nil
}

// normalize は生成規律に反する内容を落とす。
//
//   - 指定していない未決事項に紐づく質問を捨てる（source_open_issue_id の実在確認）
//   - 質問文・背景説明が空の質問を捨てる
//   - 回答形式が列挙外のものは自由記述に倒す（選択肢が無い場合も同じ）
//   - 「不明」の選択肢を取り除く（回答モード UI が常設するため）
func (d *QuestionDrafts) normalize(issues []projectstore.OpenIssue) {
	known := map[string]bool{}
	for _, issue := range issues {
		known[issue.ID] = true
	}
	kept := make([]QuestionDraft, 0, len(d.Questions))
	for _, q := range d.Questions {
		q.SourceOpenIssueID = strings.TrimSpace(q.SourceOpenIssueID)
		q.QuestionText = strings.TrimSpace(q.QuestionText)
		q.Background = strings.TrimSpace(q.Background)
		if !known[q.SourceOpenIssueID] || q.QuestionText == "" || q.Background == "" {
			continue
		}
		q.Choices = dropUnknownChoices(q.Choices)
		switch q.AnswerFormat {
		case projectstore.AnswerFormatChoice, projectstore.AnswerFormatMultiChoice, projectstore.AnswerFormatChoiceWithFree:
			if len(q.Choices) == 0 {
				q.AnswerFormat = projectstore.AnswerFormatFree
			}
		case projectstore.AnswerFormatFree:
			q.Choices = nil
		default:
			q.AnswerFormat = projectstore.AnswerFormatFree
			q.Choices = nil
		}
		kept = append(kept, q)
	}
	d.Questions = kept
}

// dropUnknownChoices は「不明」に相当する選択肢を取り除く（回答モード UI の常設の選択肢と重複させない）。
func dropUnknownChoices(choices []string) []string {
	var out []string
	for _, c := range choices {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		skip := false
		for _, banned := range unknownChoiceLabels {
			if trimmed == banned {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, trimmed)
		}
	}
	return out
}

// templatesForMissing は質問が 1 件も付かなかった未決事項のテンプレートを返す。
func templatesForMissing(issues []projectstore.OpenIssue, drafts []QuestionDraft) []QuestionDraft {
	covered := map[string]bool{}
	for _, d := range drafts {
		covered[d.SourceOpenIssueID] = true
	}
	var out []QuestionDraft
	for _, issue := range issues {
		if !covered[issue.ID] {
			out = append(out, templateDraft(issue))
		}
	}
	return out
}

// templateDraft は論点を転記した空テンプレート（AI 障害時の手入力の下敷き）。
func templateDraft(issue projectstore.OpenIssue) QuestionDraft {
	return QuestionDraft{
		SourceOpenIssueID: issue.ID,
		QuestionText:      oneLine(issue.Body),
		Background:        "",
		AnswerFormat:      projectstore.AnswerFormatFree,
	}
}

// fallbackResult は AI 障害時の縮退結果（論点を転記したテンプレートと縮退の理由）。
func fallbackResult(issues []projectstore.OpenIssue, cause error) *QuestionDraftResult {
	drafts := make([]QuestionDraft, 0, len(issues))
	for _, issue := range issues {
		drafts = append(drafts, templateDraft(issue))
	}
	notice := "AI で質問文を作れませんでした。論点を転記しましたので、質問文と背景説明を入力して発行してください。"
	if cause != nil {
		notice = describeQuestionnaireFailure(cause) + "論点を転記しましたので、質問文と背景説明を入力して発行してください。"
	}
	return &QuestionDraftResult{Drafts: drafts, Fallback: true, Notice: notice}
}

// describeQuestionnaireFailure は縮退の原因を利用者向けの 1 文にする（内部用語・コード値を出さない）。
func describeQuestionnaireFailure(cause error) string {
	var perr *aiprovider.ProviderError
	if errors.As(cause, &perr) && perr != nil {
		switch perr.Class {
		case aiprovider.ErrClassConfig:
			return "AI の設定を確認できませんでした。"
		case aiprovider.ErrClassTransient:
			return "AI が一時的に応答しませんでした。"
		}
		return "AI との通信に失敗しました。"
	}
	return "AI の応答を読み取れませんでした。"
}
