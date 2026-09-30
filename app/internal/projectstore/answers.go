package projectstore

// 本ファイルは回答（questionnaires/QS-nnn/answers.md）を担う。
//
// 回答モードが生成し返送ファイルに含める形式と同一（取込時に本フォルダへ保存）。
// AI 対話回答で得た回答も本形式で記録する（対話の生ログは回答としない）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Answers は回答ファイルのフロントマター。
type Answers struct {
	QuestionnaireID string `yaml:"questionnaire_id"`
	// Respondent は回答者名（発行時の宛先 = addressee_ref の氏名を初期値とする）。
	Respondent string    `yaml:"respondent"`
	AnsweredAt time.Time `yaml:"answered_at"`

	// Answers は本文の回答ブロック列（1 回答 = 1 ブロック）。
	Answers []Answer `yaml:"-"`

	unknown map[string]*yaml.Node
}

// knownAnswersFields は Answers が構造体として扱うフィールド名。
var knownAnswersFields = map[string]bool{
	"questionnaire_id": true, "respondent": true, "answered_at": true,
}

// Answer は 1 回答（回答ブロック）。
type Answer struct {
	// QuestionID は対応する質問の ID（q-nn）。
	QuestionID string
	// Kind は answered / unknown（「不明」）。
	Kind string
	// Selected は選択肢型の選択値（任意）。
	Selected []string
	// FreeText は自由記述（free / choice_with_free / 「その他」記入時。任意）。
	FreeText string
	At       time.Time
	// Body は補足の回答本文（kind: unknown の場合は理由・確認先）。
	Body string

	// extra は自版が知らないメタ行（原文のまま保持する）。
	extra []string
}

// Validate は必須項目を検証する。
func (a *Answers) Validate() error {
	if _, ok := IDQuestionnaire.Parse(a.QuestionnaireID); !ok {
		return fmt.Errorf("回答の質問票 ID が不正です: %q", a.QuestionnaireID)
	}
	if strings.TrimSpace(a.Respondent) == "" {
		return fmt.Errorf("回答者名がありません（%s）", a.QuestionnaireID)
	}
	if a.AnsweredAt.IsZero() {
		return fmt.Errorf("回答日時がありません（%s）", a.QuestionnaireID)
	}
	seen := map[string]bool{}
	for i := range a.Answers {
		if err := a.Answers[i].Validate(a.QuestionnaireID); err != nil {
			return err
		}
		if seen[a.Answers[i].QuestionID] {
			return fmt.Errorf("回答の質問 ID が重複しています（%s）: %s", a.QuestionnaireID, a.Answers[i].QuestionID)
		}
		seen[a.Answers[i].QuestionID] = true
	}
	return nil
}

// Validate は回答ブロックの必須項目を検証する。
func (a *Answer) Validate(questionnaireID string) error {
	if !isQuestionID(a.QuestionID) {
		return fmt.Errorf("回答の質問 ID が q-nn の形式ではありません（%s）: %q", questionnaireID, a.QuestionID)
	}
	switch a.Kind {
	case AnswerKindAnswered, AnswerKindUnknown:
	default:
		return fmt.Errorf("回答 %s の種別が %s / %s 以外です: %q",
			AnswerRef(questionnaireID, a.QuestionID), AnswerKindAnswered, AnswerKindUnknown, a.Kind)
	}
	if a.At.IsZero() {
		return fmt.Errorf("回答 %s の日時がありません", AnswerRef(questionnaireID, a.QuestionID))
	}
	return nil
}

// Find は質問 ID で回答を探す。
func (a *Answers) Find(questionID string) (Answer, bool) {
	for _, ans := range a.Answers {
		if ans.QuestionID == questionID {
			return ans, true
		}
	}
	return Answer{}, false
}

// Marshal は answers.md のバイト列を組み立てる。
func (a *Answers) Marshal() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	header, err := MarshalWithUnknownFields(a, a.unknown, "回答")
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(header)
	b.WriteString("---\n")
	for _, ans := range a.Answers {
		b.WriteString("\n")
		b.WriteString(ans.marshalBlock())
	}
	return []byte(b.String()), nil
}

func (a Answer) marshalBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", a.QuestionID)
	fmt.Fprintf(&b, "- kind: %s\n", a.Kind)
	if len(a.Selected) > 0 {
		fmt.Fprintf(&b, "- selected: %s\n", marshalMetaList(a.Selected))
	}
	if a.FreeText != "" {
		fmt.Fprintf(&b, "- free_text: %s\n", marshalMetaScalar(a.FreeText))
	}
	fmt.Fprintf(&b, "- at: %s\n", a.At.UTC().Format(time.RFC3339))
	for _, line := range a.extra {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if strings.TrimSpace(a.Body) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(a.Body, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// UnmarshalAnswers は answers.md のバイト列を解釈する（未知フィールドは保持する）。
func UnmarshalAnswers(data []byte) (*Answers, error) {
	header, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, err
	}
	var a Answers
	if err := yaml.Unmarshal(header, &a); err != nil {
		return nil, fmt.Errorf("回答のフロントマターを解釈できません: %w", err)
	}
	a.unknown, err = CollectUnknownFields(header, knownAnswersFields, "回答")
	if err != nil {
		return nil, err
	}
	a.Answers, err = parseAnswerBlocks(body)
	if err != nil {
		return nil, err
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return &a, nil
}

func parseAnswerBlocks(body []byte) ([]Answer, error) {
	var out []Answer
	var cur *Answer
	var bodyLines []string
	inMeta := false
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.Trim(strings.Join(bodyLines, "\n"), "\n")
		out = append(out, *cur)
		cur, bodyLines, inMeta = nil, nil, false
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(trimmed, "### "); ok {
			flush()
			cur = &Answer{QuestionID: strings.TrimSpace(id)}
			inMeta = true
			continue
		}
		if cur == nil {
			continue
		}
		if inMeta && strings.HasPrefix(trimmed, "- ") {
			if err := cur.applyMetaLine(trimmed); err != nil {
				return nil, err
			}
			continue
		}
		if inMeta && trimmed == "" {
			inMeta = false
			continue
		}
		inMeta = false
		bodyLines = append(bodyLines, line)
	}
	flush()
	return out, nil
}

func (a *Answer) applyMetaLine(line string) error {
	key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
	if !ok {
		return nil
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	switch key {
	case "kind":
		a.Kind = value
	case "selected":
		a.Selected = parseMetaList(value)
	case "free_text":
		a.FreeText = parseMetaScalar(value)
	case "at":
		at, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("回答の日時を解釈できません（%s）: %w", a.QuestionID, err)
		}
		a.At = at
	default:
		if isPasscodeKey(key) {
			return nil // パスコード相当の値は取り込まない（パスコードを記録に残さない）
		}
		a.extra = append(a.extra, line)
	}
	return nil
}

// SaveAnswers は取り込んだ回答を質問票フォルダへ保存する。
func (s *Store) SaveAnswers(a *Answers) error {
	data, err := a.Marshal()
	if err != nil {
		return err
	}
	return s.WriteFile(AnswersFile(a.QuestionnaireID), data)
}

// LoadAnswers は保存済みの回答を読む。未取込のときは (nil, nil)。
func (s *Store) LoadAnswers(questionnaireID string) (*Answers, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(AnswersFile(questionnaireID))))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("回答を読み込めません（%s）: %w", questionnaireID, err)
	}
	return UnmarshalAnswers(data)
}
