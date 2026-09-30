package projectstore

// 本ファイルは質問票（questionnaires/QS-nnn/questionnaire.md）と
// 回答（同 answers.md）、および質問票の状態遷移を担う。
//
// パスコード・その導出値は保持しない（受け渡しファイルの保護にだけ使い、記録に残さない方針）。構造体にフィールドを持たず、
// 未知フィールド保持の対象からも除外する（読み込んだファイルに紛れていても捨てる）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// dirQuestionnaires は質問票の配置。
const dirQuestionnaires = "questionnaires"

// 質問票の状態（発行済み → 回答済み → 取込済みの 3 状態）。
const (
	QuestionnaireIssued   = "issued"   // 発行済み
	QuestionnaireAnswered = "answered" // 回答済み（返送ファイルの取込開始）
	QuestionnaireImported = "imported" // 取込済み（反映差分の承認完了）
)

// 回答形式（questionnaire.md の answer_format。対話エンジンが生成する値・画面の表示形式と同一）。
const (
	AnswerFormatChoice         = "choice"
	AnswerFormatMultiChoice    = "multi_choice"
	AnswerFormatFree           = "free"
	AnswerFormatChoiceWithFree = "choice_with_free"
)

// 回答の種別（answers.md の kind）。
const (
	AnswerKindAnswered = "answered"
	AnswerKindUnknown  = "unknown" // 「不明」（回答者が分からないと答えたもの）
)

// answerFormatNeedsChoices は選択肢を必須とする回答形式。
func answerFormatNeedsChoices(format string) bool {
	switch format {
	case AnswerFormatChoice, AnswerFormatMultiChoice, AnswerFormatChoiceWithFree:
		return true
	default:
		return false
	}
}

// Questionnaire は質問票のフロントマター。
type Questionnaire struct {
	ID string `yaml:"id"`
	// AddresseeRef は宛先の名簿 ID（STK-nnn。表示は名簿から引く）。
	AddresseeRef string `yaml:"addressee_ref"`
	// Addressee は発行時点の宛先表示名の写し（名簿側の後日変更で発行済み票の表示が変わらないようにする）。
	Addressee string    `yaml:"addressee"`
	IssuedAt  time.Time `yaml:"issued_at"`
	// IssuedBy は発行した作業者の author_id。
	IssuedBy string `yaml:"issued_by"`
	Status   string `yaml:"status"`

	// Questions は本文の質問ブロック列（1 質問 = 1 ブロック）。
	Questions []Question `yaml:"-"`

	// unknown は自版が知らないフロントマターのフィールド（新しい版が足した項目を古い版が消さないように保持する）。
	unknown map[string]*yaml.Node
}

// knownQuestionnaireFields は Questionnaire が構造体として扱うフィールド名。
var knownQuestionnaireFields = map[string]bool{
	"id": true, "addressee_ref": true, "addressee": true,
	"issued_at": true, "issued_by": true, "status": true,
}

// Question は 1 質問（questionnaire.md の質問ブロック）。
type Question struct {
	ID string // q-nn（質問票内連番。参照は QS-nnn#q-nn）
	// SourceIssue は発行元未決事項（ISS-nnn。必須。どの未決事項を決めるための質問かを必ず辿れるように）。
	SourceIssue  string
	AnswerFormat string
	Choices      []string
	// Terms は回答に必要な用語（terms.yaml のキー。任意）。
	Terms []string
	// Text は質問本文（#### 質問）、Background は背景説明（#### 背景説明）。
	Text       string
	Background string

	// extra は自版が知らないメタ行（"- key: value" の原文）。書き戻しで失わないために保持する。
	extra []string
}

// QuestionnaireDir は質問票フォルダの相対パス。
func QuestionnaireDir(id string) string { return dirQuestionnaires + "/" + id }

// QuestionnaireFile は questionnaire.md の相対パス。
func QuestionnaireFile(id string) string { return QuestionnaireDir(id) + "/questionnaire.md" }

// AnswersFile は answers.md の相対パス。
func AnswersFile(id string) string { return QuestionnaireDir(id) + "/answers.md" }

// QuestionnaireExchangeDir は発行控え・返送原本の保存先。
func QuestionnaireExchangeDir(id string) string { return QuestionnaireDir(id) + "/exchange" }

// AnswerRef は回答への参照（QS-nnn#q-nn。決定事項の evidence に使う）。
func AnswerRef(questionnaireID, questionID string) string { return questionnaireID + "#" + questionID }

// ParseAnswerRef は回答参照（`QS-nnn#q-nn`）を分解する。
//
// **AnswerRef と対で本ファイルに置く**（参照形式を 1 か所に閉じる。二重管理を避ける）。
// 質問票 ID・質問 ID のどちらかが規約に合わなければ ok = false を返す
// （発話参照 `S-nnnn#utt-nnnnn` や取り込み資料参照を誤って拾わないため）。
func ParseAnswerRef(ref string) (questionnaireID, questionID string, ok bool) {
	qid, question, found := strings.Cut(ref, "#")
	if !found {
		return "", "", false
	}
	if _, valid := IDQuestionnaire.Parse(qid); !valid {
		return "", "", false
	}
	if !isQuestionID(question) {
		return "", "", false
	}
	return qid, question, true
}

// Validate は質問票の必須項目を検証する。
func (q *Questionnaire) Validate() error { return q.validate(false) }

// validate は forExchange のとき、受け渡し用の写しが持たない項目
// （発行者の利用者 ID・状態）を検証対象から外す。
func (q *Questionnaire) validate(forExchange bool) error {
	if _, ok := IDQuestionnaire.Parse(q.ID); !ok {
		return fmt.Errorf("質問票の ID が不正です: %q", q.ID)
	}
	if _, ok := IDStakeholder.Parse(q.AddresseeRef); !ok {
		return fmt.Errorf("質問票の宛先（名簿 ID）が不正です（%s）: %q", q.ID, q.AddresseeRef)
	}
	if strings.TrimSpace(q.Addressee) == "" {
		return fmt.Errorf("質問票の宛先表示名がありません（%s）", q.ID)
	}
	if q.IssuedAt.IsZero() {
		return fmt.Errorf("質問票の発行日時がありません（%s）", q.ID)
	}
	if !forExchange {
		if strings.TrimSpace(q.IssuedBy) == "" {
			return fmt.Errorf("質問票の発行者がありません（%s）", q.ID)
		}
		if !isQuestionnaireStatus(q.Status) {
			return fmt.Errorf("質問票の状態が %s / %s / %s 以外です（%s）: %q",
				QuestionnaireIssued, QuestionnaireAnswered, QuestionnaireImported, q.ID, q.Status)
		}
	}
	if len(q.Questions) == 0 {
		return fmt.Errorf("質問票に質問がありません（%s）", q.ID)
	}
	seen := map[string]bool{}
	for i := range q.Questions {
		if err := q.Questions[i].Validate(q.ID); err != nil {
			return err
		}
		if seen[q.Questions[i].ID] {
			return fmt.Errorf("質問の ID が重複しています（%s）: %s", q.ID, q.Questions[i].ID)
		}
		seen[q.Questions[i].ID] = true
	}
	return nil
}

func isQuestionnaireStatus(s string) bool {
	switch s {
	case QuestionnaireIssued, QuestionnaireAnswered, QuestionnaireImported:
		return true
	default:
		return false
	}
}

// Validate は質問ブロックの必須項目を検証する。
func (q *Question) Validate(questionnaireID string) error {
	if !isQuestionID(q.ID) {
		return fmt.Errorf("質問の ID が q-nn の形式ではありません（%s）: %q", questionnaireID, q.ID)
	}
	if _, ok := IDOpenIssue.Parse(q.SourceIssue); !ok {
		return fmt.Errorf("質問 %s の発行元未決事項がありません", AnswerRef(questionnaireID, q.ID))
	}
	switch q.AnswerFormat {
	case AnswerFormatChoice, AnswerFormatMultiChoice, AnswerFormatFree, AnswerFormatChoiceWithFree:
	default:
		return fmt.Errorf("質問 %s の回答形式が %s / %s / %s / %s 以外です: %q",
			AnswerRef(questionnaireID, q.ID), AnswerFormatChoice, AnswerFormatMultiChoice,
			AnswerFormatFree, AnswerFormatChoiceWithFree, q.AnswerFormat)
	}
	if answerFormatNeedsChoices(q.AnswerFormat) && len(q.Choices) == 0 {
		return fmt.Errorf("質問 %s の回答形式 %s には選択肢が必要です", AnswerRef(questionnaireID, q.ID), q.AnswerFormat)
	}
	if strings.TrimSpace(q.Text) == "" {
		return fmt.Errorf("質問 %s の本文がありません", AnswerRef(questionnaireID, q.ID))
	}
	if strings.TrimSpace(q.Background) == "" {
		return fmt.Errorf("質問 %s の背景説明がありません", AnswerRef(questionnaireID, q.ID))
	}
	return nil
}

// isQuestionID は q-nn（2 桁以上の連番）かを返す。
func isQuestionID(id string) bool {
	rest, ok := strings.CutPrefix(id, "q-")
	if !ok || len(rest) < 2 {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// FormatQuestionID は連番から質問 ID を作る（q-01）。
func FormatQuestionID(n int) string { return fmt.Sprintf("q-%02d", n) }

// FindQuestion は質問 ID で質問を探す。
func (q *Questionnaire) FindQuestion(id string) (Question, bool) {
	for _, qq := range q.Questions {
		if qq.ID == id {
			return qq, true
		}
	}
	return Question{}, false
}

// ---- 直列化 -------------------------------------------------------------

// Marshal は questionnaire.md のバイト列を組み立てる。
func (q *Questionnaire) Marshal() ([]byte, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	header, err := MarshalWithUnknownFields(q, q.unknown, "質問票")
	if err != nil {
		return nil, err
	}
	return q.assemble(header), nil
}

// exchangeQuestionnaireFront は受け渡し用の写しのフロントマター。
// 発行者の利用者 ID（issued_by）と質問票の状態（status）を持たない。
type exchangeQuestionnaireFront struct {
	ID           string    `yaml:"id"`
	AddresseeRef string    `yaml:"addressee_ref"`
	Addressee    string    `yaml:"addressee"`
	IssuedAt     time.Time `yaml:"issued_at"`
}

// MarshalForExchange は受け渡しファイルへ入れる写しを組み立てる。
//
// 回答に不要な発行者の利用者 ID と質問票の状態を含めない（宛先は社外を含み得るため）。
// 質問ブロックの様式は questionnaire.md と同一（様式を 1 か所で保つ）。
func (q *Questionnaire) MarshalForExchange() ([]byte, error) {
	// 写しが持たない項目（発行者・状態）は検証しない。写しを読み戻して書き直せるようにするため。
	if err := q.validate(true); err != nil {
		return nil, err
	}
	front := exchangeQuestionnaireFront{
		ID: q.ID, AddresseeRef: q.AddresseeRef, Addressee: q.Addressee, IssuedAt: q.IssuedAt,
	}
	header, err := MarshalWithUnknownFields(&front, q.unknown, "質問票")
	if err != nil {
		return nil, err
	}
	return q.assemble(header), nil
}

// assemble はフロントマターと質問ブロック列を 1 つの Markdown にする。
func (q *Questionnaire) assemble(header []byte) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(header)
	b.WriteString("---\n")
	for _, question := range q.Questions {
		b.WriteString("\n")
		b.WriteString(question.marshalBlock())
	}
	return []byte(b.String())
}

// marshalBlock は 1 質問ブロックを組み立てる。
func (q Question) marshalBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", q.ID)
	fmt.Fprintf(&b, "- source_issue: %s\n", q.SourceIssue)
	fmt.Fprintf(&b, "- answer_format: %s\n", q.AnswerFormat)
	if len(q.Choices) > 0 {
		fmt.Fprintf(&b, "- choices: %s\n", marshalMetaList(q.Choices))
	}
	if len(q.Terms) > 0 {
		fmt.Fprintf(&b, "- terms: %s\n", marshalMetaList(q.Terms))
	}
	for _, line := range q.extra {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n#### 質問\n")
	b.WriteString(strings.TrimRight(q.Text, "\n"))
	b.WriteString("\n#### 背景説明\n")
	b.WriteString(strings.TrimRight(q.Background, "\n"))
	b.WriteString("\n")
	return b.String()
}

// UnmarshalQuestionnaire は questionnaire.md のバイト列を解釈する。
// 未知のフロントマターは保持する。パスコード相当のキーは捨てる（パスコードを記録に残さない）。
func UnmarshalQuestionnaire(data []byte) (*Questionnaire, error) {
	return unmarshalQuestionnaire(data, false)
}

// UnmarshalExchangeQuestionnaire は受け渡しファイル内の写しを解釈する。
// 写しは発行者の利用者 ID と状態を持たないため、それらを必須としない。
func UnmarshalExchangeQuestionnaire(data []byte) (*Questionnaire, error) {
	return unmarshalQuestionnaire(data, true)
}

func unmarshalQuestionnaire(data []byte, forExchange bool) (*Questionnaire, error) {
	header, body, err := splitFrontMatter(data)
	if err != nil {
		return nil, err
	}
	var q Questionnaire
	if err := yaml.Unmarshal(header, &q); err != nil {
		return nil, fmt.Errorf("質問票のフロントマターを解釈できません: %w", err)
	}
	q.unknown, err = CollectUnknownFields(header, knownQuestionnaireFields, "質問票")
	if err != nil {
		return nil, err
	}
	q.Questions, err = parseQuestions(body)
	if err != nil {
		return nil, err
	}
	if err := q.validate(forExchange); err != nil {
		return nil, err
	}
	return &q, nil
}

// parseQuestions は本文の質問ブロック列を解釈する。
func parseQuestions(body []byte) ([]Question, error) {
	var out []Question
	var cur *Question
	section := ""
	var text, background []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.Text = strings.Trim(strings.Join(text, "\n"), "\n")
		cur.Background = strings.Trim(strings.Join(background, "\n"), "\n")
		out = append(out, *cur)
		cur, text, background, section = nil, nil, nil, ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(trimmed, "### "); ok {
			flush()
			cur = &Question{ID: strings.TrimSpace(id)}
			continue
		}
		if cur == nil {
			continue
		}
		switch trimmed {
		case "#### 質問":
			section = "text"
			continue
		case "#### 背景説明":
			section = "background"
			continue
		}
		if section == "" {
			if strings.HasPrefix(trimmed, "- ") {
				if err := cur.applyMetaLine(trimmed); err != nil {
					return nil, err
				}
			}
			continue
		}
		if section == "text" {
			text = append(text, line)
		} else {
			background = append(background, line)
		}
	}
	flush()
	return out, nil
}

// applyMetaLine は質問ブロックのメタ行（"- key: value"）を取り込む。
// 自版が知らないキーは原文のまま保持する（書き戻しで失わない）。
func (q *Question) applyMetaLine(line string) error {
	key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
	if !ok {
		return nil
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	switch key {
	case "source_issue":
		q.SourceIssue = value
	case "answer_format":
		q.AnswerFormat = value
	case "choices":
		q.Choices = parseMetaList(value)
	case "terms":
		q.Terms = parseMetaList(value)
	default:
		if isPasscodeKey(key) {
			return nil // パスコード相当の値は取り込まない（パスコードを記録に残さない）
		}
		q.extra = append(q.extra, line)
	}
	return nil
}

// isPasscodeKey はパスコード相当のキー名かを返す（読み込みの段階で捨て、構造的に保持しないため）。
func isPasscodeKey(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, banned := range []string{"passcode", "password", "passphrase", "derived_key", "kdf"} {
		if strings.Contains(k, banned) {
			return true
		}
	}
	return false
}

// CollectUnknownFields は既知フィールド以外の YAML フィールドを拾う（新しい版が足した項目を古い版が消さないように）。
//
// パスコード相当のキーは拾わずに捨てる（パスコードを構造的に保持しない）。label はエラー文の対象名。
// 受け渡しファイルの manifest も同じ規則で扱う。
func CollectUnknownFields(header []byte, known map[string]bool, label string) (map[string]*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(header, &doc); err != nil {
		return nil, fmt.Errorf("%sのフロントマターを解釈できません: %w", label, err)
	}
	var unknown map[string]*yaml.Node
	for k, v := range mappingPairs(&doc) {
		if known[k] || isPasscodeKey(k) {
			continue
		}
		if unknown == nil {
			unknown = map[string]*yaml.Node{}
		}
		unknown[k] = v
	}
	return unknown, nil
}

// MarshalWithUnknownFields は構造体と保持していた未知フィールドを 1 つの YAML にまとめる。
func MarshalWithUnknownFields(front any, unknown map[string]*yaml.Node, label string) ([]byte, error) {
	known, err := yaml.Marshal(front)
	if err != nil {
		return nil, fmt.Errorf("%sのフロントマターを組み立てられません: %w", label, err)
	}
	if len(unknown) == 0 {
		return known, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(known, &doc); err != nil {
		return nil, fmt.Errorf("%sのフロントマターを組み立てられません: %w", label, err)
	}
	mapping := doc.Content[0]
	for _, k := range sortedKeys(unknown) {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, unknown[k])
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("%sのフロントマターを組み立てられません: %w", label, err)
	}
	return out, nil
}

// ---- Store 操作 ---------------------------------------------------------

// CreateQuestionnaire は質問票を新規記録し、採番した ID を返す（QS-nnn の連番）。
// 発行操作の一部として呼ぶため、状態は issued で作る。
func (s *Store) CreateQuestionnaire(q Questionnaire) (*Questionnaire, error) {
	if q.IssuedAt.IsZero() {
		q.IssuedAt = time.Now().UTC().Truncate(time.Second)
	}
	q.Status = QuestionnaireIssued
	id, err := s.AllocateID(IDQuestionnaire, func(id string) error {
		q.ID = id
		data, err := q.Marshal()
		if err != nil {
			return err
		}
		if err := s.WriteFile(QuestionnaireFile(id), data); err != nil {
			return err
		}
		return s.mkdirAll(QuestionnaireExchangeDir(id))
	})
	if err != nil {
		return nil, err
	}
	return s.LoadQuestionnaire(id)
}

// LoadQuestionnaire は質問票を読む。
func (s *Store) LoadQuestionnaire(id string) (*Questionnaire, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(QuestionnaireFile(id))))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("質問票が見つかりません（%s）", id)
	}
	if err != nil {
		return nil, fmt.Errorf("質問票を読み込めません（%s）: %w", id, err)
	}
	return UnmarshalQuestionnaire(data)
}

// ListQuestionnaires は全質問票を ID 順で返す（質問票の一覧画面のデータ源）。
func (s *Store) ListQuestionnaires() ([]Questionnaire, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, dirQuestionnaires))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("質問票フォルダを走査できません: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, ok := IDQuestionnaire.Parse(e.Name()); ok {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	out := make([]Questionnaire, 0, len(ids))
	for _, id := range ids {
		q, err := s.LoadQuestionnaire(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *q)
	}
	return out, nil
}

// SaveQuestionnaire は質問票を原子的に書き戻す（再発行時の宛先・発行日時の更新に使う）。
// 状態の変更は SetQuestionnaireStatus / ReopenQuestionnaireForReimport を通す。
func (s *Store) SaveQuestionnaire(q *Questionnaire) error {
	current, err := s.LoadQuestionnaire(q.ID)
	if err != nil {
		return err
	}
	if q.Status != current.Status {
		return fmt.Errorf("質問票の状態はこの操作では変更できません（%s）", q.ID)
	}
	data, err := q.Marshal()
	if err != nil {
		return err
	}
	return s.WriteFile(QuestionnaireFile(q.ID), data)
}

// SetQuestionnaireStatus は質問票の状態を進める（定められた遷移のみ許す）。
//
// 許す遷移は issued → answered → imported と、同じ状態への据え置き（再発行・一時保存で
// 担当者側の状態が変わらない場合）。逆行・飛び越しは拒否する。
// 取込済みからの再取込は ReopenQuestionnaireForReimport を明示的に使う。
func (s *Store) SetQuestionnaireStatus(id, next string) error {
	if !isQuestionnaireStatus(next) {
		return fmt.Errorf("質問票の状態が不正です: %q", next)
	}
	q, err := s.LoadQuestionnaire(id)
	if err != nil {
		return err
	}
	if q.Status == next {
		return nil
	}
	if statusRank(next) != statusRank(q.Status)+1 {
		return fmt.Errorf("質問票の状態を %s から %s へ変えられません（%s）", q.Status, next, id)
	}
	q.Status = next
	data, err := q.Marshal()
	if err != nil {
		return err
	}
	return s.WriteFile(QuestionnaireFile(id), data)
}

// ReopenQuestionnaireForReimport は取込済みの質問票を回答済みへ戻す（回答の再取込）。
//
// 担当者が明示的に「再取込（回答の置き換え）」を選んだ場合のみ呼ぶ。
// 置き換えの記録（変更履歴）は呼び出し側が行う。
func (s *Store) ReopenQuestionnaireForReimport(id string) error {
	q, err := s.LoadQuestionnaire(id)
	if err != nil {
		return err
	}
	if q.Status != QuestionnaireImported {
		return fmt.Errorf("取込済みの質問票ではありません（%s）", id)
	}
	q.Status = QuestionnaireAnswered
	data, err := q.Marshal()
	if err != nil {
		return err
	}
	return s.WriteFile(QuestionnaireFile(id), data)
}

// statusRank は状態の進み具合（状態機械の順序）。
func statusRank(status string) int {
	switch status {
	case QuestionnaireIssued:
		return 0
	case QuestionnaireAnswered:
		return 1
	case QuestionnaireImported:
		return 2
	default:
		return -1
	}
}

// SaveExchangeArtifact は発行控え・受領した返送ファイルの原本を保存する。
// name はファイル名（パス区切りを含めない）。
func (s *Store) SaveExchangeArtifact(questionnaireID, name string, data []byte) error {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("受け渡しファイルの名前が不正です: %q", name)
	}
	return s.WriteFile(QuestionnaireExchangeDir(questionnaireID)+"/"+name, data)
}

// mkdirAll は保存キュー経由でプロジェクト内のフォルダを作る。
func (s *Store) mkdirAll(rel string) error {
	path := filepath.Join(s.root, filepath.FromSlash(rel))
	return s.write(func() error {
		if err := os.MkdirAll(path, dataDirMode); err != nil {
			return fmt.Errorf("フォルダを作成できません（%s）: %w", path, err)
		}
		return nil
	})
}

// ExchangeArtifactPath は受け渡しファイルの絶対パスを返す。
func (s *Store) ExchangeArtifactPath(questionnaireID, name string) string {
	return filepath.Join(s.root, filepath.FromSlash(QuestionnaireExchangeDir(questionnaireID)), name)
}
