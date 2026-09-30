package exchange

// 本ファイルは返送ファイルの検証と突合を担う。
//
// 検証のいずれかで停止した場合、既存プロジェクトデータは一切変更しない。
// 検証（ValidateReturn）は読み取り専用で、書き込みは ApplyReturn だけが行う。

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 確認操作を要する警告の種別（二重取込・改変・宛先の不一致）。
const (
	WarnReimport          = "reimport"           // 二重取込（取込済みの質問票への再取込）
	WarnContentModified   = "content-modified"   // 質問部の改変検出
	WarnAddresseeMismatch = "addressee-mismatch" // 宛先が発行時と一致しない
)

// ImportWarning は担当者の確認操作を要する検証結果。
type ImportWarning struct {
	Kind string
	// Message は原因と次の行動を示す 1 文。
	Message string
	// Before / After は改変検出の差分表示に使う（Kind が content-modified / addressee-mismatch のとき）。
	Before string
	After  string
}

// AnswerMatch は質問・回答・発行元未決事項の対応（取込画面の対応表示）。
type AnswerMatch struct {
	Question projectstore.Question
	// Answer は対応する回答。未回答のときは nil（回答の欠落）。
	Answer *projectstore.Answer
	// SourceIssue は発行元未決事項の ID（質問の source_issue）。
	SourceIssue string
}

// ImportReview は返送ファイルの検証結果（取込前の確認材料）。
//
// この値を作る時点でプロジェクトデータは変更していない。反映は ApplyReturn で行う。
type ImportReview struct {
	Questionnaire *projectstore.Questionnaire
	Answers       *projectstore.Answers
	// Matches は質問順の対応表（回答が無い質問も含む）。
	Matches []AnswerMatch
	// MissingQuestionIDs は回答が無い質問（未回答として取込を続行できる）。
	MissingQuestionIDs []string
	// UnknownQuestionIDs は質問票に無い回答（無視するが記録に残す）。
	UnknownQuestionIDs []string
	// Sessions は返送に含まれるステークホルダーセッション（コンテナ内パス → 内容）。
	Sessions map[string][]byte
	// Warnings は確認操作を要する事項（空なら確認なしで取り込める）。
	Warnings []ImportWarning
	// Raw は受領した返送ファイルの原本（exchange/ へ無変更で保存する）。
	Raw []byte
	// SourceName は受領ファイルの名前（控えの保存名に使う）。
	SourceName string
}

// NeedsConfirmation は確認操作を要する警告があるかを返す。
func (r *ImportReview) NeedsConfirmation() bool { return len(r.Warnings) > 0 }

// HasWarning は指定種別の警告があるかを返す。
func (r *ImportReview) HasWarning(kind string) bool {
	for _, w := range r.Warnings {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// ImportConfirmation は担当者が確認した警告の種別（警告は確認操作の後でのみ続行する）。
type ImportConfirmation struct {
	// AllowReimport は取込済みの質問票への再取込（回答の置き換え）を許す（手順5）。
	AllowReimport bool
	// AcceptModifiedContent は質問部の改変を承知のうえ続行する（手順6）。
	AcceptModifiedContent bool
	// AcceptAddresseeMismatch は宛先の不一致を承知のうえ続行する（手順8）。
	AcceptAddresseeMismatch bool
}

// ValidateReturn は返送ファイルを検証して突合する（本関数内の手順0〜8 = 復号・版照合・種別・宛先のプロジェクト・
// 質問票の有無・二重取込・改変検出・回答の突き合わせ・宛先対応）。
//
// プロジェクトデータを一切変更しない。手順0〜4 の不合格は error を返し、
// 手順5・6・8 の不合格は Warnings に載せて返す（担当者の確認操作後に ApplyReturn で続行する）。
func ValidateReturn(store *projectstore.Store, src string) (*ImportReview, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("返送ファイルを読み込めません（%s）: %w", src, err)
	}
	// 手順1（版照合）と manifest の解釈。
	container, err := ParseContainer(raw)
	if err != nil {
		return nil, err
	}
	// 手順2: kind: return であること。
	if container.Manifest.Kind != KindReturn {
		return nil, fmt.Errorf("発行用ファイルです。ステークホルダーから返送されたファイルを選んでください")
	}
	// 手順3: 宛先違い検出（開いているプロジェクトと一致すること）。
	if container.Manifest.ProjectID != store.Project().ProjectID {
		return nil, fmt.Errorf("別のプロジェクト宛のファイルです。対象のプロジェクトを開いてから取り込んでください")
	}
	// 手順0: ペイロードの復号（プロジェクトの交換鍵秘密鍵。パスコード入力は不要）。
	kp, err := EnsureProjectKeyPair(store)
	if err != nil {
		return nil, err
	}
	payload, err := container.OpenReturnPayload(kp)
	if err != nil {
		return nil, err
	}
	// 手順4: questionnaire_id が questionnaires/ に存在すること。
	questionnaire, err := store.LoadQuestionnaire(container.Manifest.QuestionnaireID)
	if err != nil {
		return nil, fmt.Errorf("発行元の質問票が見つかりません（%s）。別のプロジェクトのファイルでないか確認してください",
			container.Manifest.QuestionnaireID)
	}

	answers, err := parseReturnedAnswers(payload, questionnaire.ID)
	if err != nil {
		return nil, err
	}

	review := &ImportReview{
		Questionnaire: questionnaire,
		Answers:       answers,
		Sessions:      collectSessions(payload),
		Raw:           raw,
		SourceName:    path.Base(strings.ReplaceAll(src, `\`, "/")),
	}
	// 手順5: 二重取込検出。
	if questionnaire.Status == projectstore.QuestionnaireImported {
		review.Warnings = append(review.Warnings, ImportWarning{
			Kind:    WarnReimport,
			Message: "この質問票は取込済みです。再取込すると前回の回答を置き換えます。",
		})
	}
	// 手順6: 改変検出（返送内の questionnaire.md が発行時のものと一致するか）。
	modified, err := checkContentHash(store, payload, questionnaire)
	if err != nil {
		return nil, err
	}
	if modified != nil {
		review.Warnings = append(review.Warnings, *modified)
	}
	// 手順8: 宛先対応検証。
	issued, err := LoadIssueRecord(store, questionnaire.ID)
	if err != nil {
		return nil, err
	}
	if mismatch := checkAddressee(payload, questionnaire, issued); mismatch != nil {
		review.Warnings = append(review.Warnings, *mismatch)
	}
	// 手順7: 回答 ID の突合（欠落は未回答として続行、未知 ID は無視して記録に残す）。
	review.Matches, review.MissingQuestionIDs, review.UnknownQuestionIDs = matchAnswers(questionnaire, answers)

	return review, nil
}

// parseReturnedAnswers は返送内の answers.md を読む。
func parseReturnedAnswers(payload Payload, questionnaireID string) (*projectstore.Answers, error) {
	body, ok := payload[EntryAnswers]
	if !ok {
		return nil, fmt.Errorf("返送ファイルに回答が入っていません。ステークホルダーへ確定操作を依頼してください")
	}
	answers, err := projectstore.UnmarshalAnswers(body)
	if err != nil {
		return nil, err
	}
	if answers.QuestionnaireID != questionnaireID {
		return nil, fmt.Errorf("返送ファイルの回答が別の質問票のものです（%s）", answers.QuestionnaireID)
	}
	return answers, nil
}

// collectSessions は返送内のステークホルダーセッションを集める（返送に含まれるのは任意）。
func collectSessions(payload Payload) map[string][]byte {
	var out map[string][]byte
	for _, name := range payload.Names() {
		if !strings.HasPrefix(name, EntrySessionsDir) {
			continue
		}
		if out == nil {
			out = map[string][]byte{}
		}
		out[name] = payload[name]
	}
	return out
}

// checkContentHash は質問部の改変を検出する。
//
// 返送内の questionnaire.md のハッシュを、返送内 content.yaml の content_hash と
// 発行控え（= プロジェクト側の questionnaire.md から都度計算する値）の双方と照合する。
func checkContentHash(store *projectstore.Store, payload Payload, questionnaire *projectstore.Questionnaire) (*ImportWarning, error) {
	returned, ok := payload[EntryQuestionnaire]
	if !ok {
		return nil, fmt.Errorf("返送ファイルに質問票が入っていません（ファイルが壊れています）")
	}
	issued, err := IssuedContentHash(store, questionnaire)
	if err != nil {
		return nil, err
	}
	returnedHash := ContentHash(returned)
	declared := ""
	if body, ok := payload[EntryContent]; ok {
		var meta ContentMeta
		if err := yaml.Unmarshal(body, &meta); err != nil {
			return nil, fmt.Errorf("返送ファイルの内容情報を解釈できません: %w", err)
		}
		declared = meta.ContentHash
	}
	if returnedHash == issued && (declared == "" || declared == issued) {
		return nil, nil
	}

	current, err := questionnaire.MarshalForExchange()
	if err != nil {
		return nil, err
	}
	return &ImportWarning{
		Kind:    WarnContentModified,
		Message: "返送ファイルの質問部が発行時と違います。内容を確認してから取り込んでください。",
		Before:  string(current),
		After:   string(returned),
	}, nil
}

// checkAddressee は宛先対応を検証する。
func checkAddressee(payload Payload, questionnaire *projectstore.Questionnaire, issued *IssueRecord) *ImportWarning {
	returned, ok := payload[EntryQuestionnaire]
	if !ok {
		return nil
	}
	parsed, err := projectstore.UnmarshalExchangeQuestionnaire(returned)
	if err != nil {
		// 質問部が読めない場合は改変検出（手順6）が拾う。
		return nil
	}
	// 基準は発行時点の宛先（発行控えメタデータ）。無い場合は現在の質問票の値を使う。
	wantRef, wantLabel := questionnaire.AddresseeRef, questionnaire.Addressee
	if issued != nil && issued.AddresseeRef != "" {
		wantRef, wantLabel = issued.AddresseeRef, issued.Addressee
	}
	if parsed.AddresseeRef == wantRef {
		return nil
	}
	return &ImportWarning{
		Kind:    WarnAddresseeMismatch,
		Message: "返送ファイルの宛先が発行時と一致しません。宛先を確認してから取り込んでください。",
		Before:  wantLabel,
		After:   parsed.Addressee,
	}
}

// matchAnswers は質問と回答を突き合わせる。
func matchAnswers(q *projectstore.Questionnaire, a *projectstore.Answers) (matches []AnswerMatch, missing, unknown []string) {
	known := map[string]bool{}
	for _, question := range q.Questions {
		known[question.ID] = true
		match := AnswerMatch{Question: question, SourceIssue: question.SourceIssue}
		if answer, ok := a.Find(question.ID); ok {
			copied := answer
			match.Answer = &copied
		} else {
			missing = append(missing, question.ID)
		}
		matches = append(matches, match)
	}
	for _, answer := range a.Answers {
		if !known[answer.QuestionID] {
			unknown = append(unknown, answer.QuestionID)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	return matches, missing, unknown
}

// ErrConfirmationRequired は確認操作を経ずに取込を続行しようとしたことを表す。
var ErrConfirmationRequired = errors.New("確認が必要な内容があります。内容を確認してから取り込んでください")

// ImportedSession は再採番して保存したステークホルダーセッション。
type ImportedSession struct {
	// Path は返送ファイル内のコンテナ内パス（sessions/S-0001.md）。
	Path string
	// OriginalID は返送ファイル内の ID（ステークホルダー側の採番）。
	OriginalID string
	// SessionID はプロジェクト内で採番し直した ID（S-nnnn）。
	SessionID string
}

// ApplyResult は取込の反映結果。
type ApplyResult struct {
	QuestionnaireID string
	// Status は反映後の質問票の状態（回答済み。取込済みへの遷移は反映差分の承認後）。
	Status string
	// Sessions は再採番して保存したステークホルダーセッション（無ければ空）。
	Sessions []ImportedSession
}

// ApplyReturn は検証済みの返送ファイルを取り込む（質問票の状態を発行済み → 回答済みへ）。
//
// 確認操作を要する警告（二重取込・改変検出・宛先不一致）が確認されていない場合は
// 何も書き込まずに ErrConfirmationRequired を返す。
// 反映差分の生成・承認は本関数の後段で行う（取込済みへの遷移もそちら）。
func ApplyReturn(store *projectstore.Store, review *ImportReview, confirm ImportConfirmation) (*ApplyResult, error) {
	if review == nil {
		return nil, fmt.Errorf("取り込む内容がありません")
	}
	if err := checkConfirmations(review, confirm); err != nil {
		return nil, err
	}

	// 再取込のときは取込済みから回答済みへ戻す（置き換えの変更履歴への記録は呼び出し側）。
	if review.HasWarning(WarnReimport) {
		if err := store.ReopenQuestionnaireForReimport(review.Questionnaire.ID); err != nil {
			return nil, err
		}
	}
	// 受領原本を無変更で保存する。
	if err := store.SaveExchangeArtifact(review.Questionnaire.ID, returnArchiveName(review), review.Raw); err != nil {
		return nil, err
	}
	// 回答を保存する（未知の質問 ID の回答は取り込まない = 手順7）。
	filtered := filterKnownAnswers(review)
	if err := store.SaveAnswers(filtered); err != nil {
		return nil, err
	}
	// ステークホルダーセッションをプロジェクト内 ID で再採番して保存する。
	sessions, err := importSessions(store, review)
	if err != nil {
		return nil, err
	}
	// 発行済み → 回答済み。
	if err := store.SetQuestionnaireStatus(review.Questionnaire.ID, projectstore.QuestionnaireAnswered); err != nil {
		return nil, err
	}
	return &ApplyResult{
		QuestionnaireID: review.Questionnaire.ID,
		Status:          projectstore.QuestionnaireAnswered,
		Sessions:        sessions,
	}, nil
}

// importSessions は返送内のステークホルダーセッションを再採番して保存する。
//
// 受領原本は exchange/ に無変更で残るため、ここで書くのは写しである。
func importSessions(store *projectstore.Store, review *ImportReview) ([]ImportedSession, error) {
	if len(review.Sessions) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(review.Sessions))
	for name := range review.Sessions {
		names = append(names, name)
	}
	// 返送内の順序（S-0001, S-0002, …）で採番するため名前順に処理する。
	sort.Strings(names)

	out := make([]ImportedSession, 0, len(names))
	for _, name := range names {
		sess, err := store.ImportStakeholderSession(review.Sessions[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, ImportedSession{
			Path:       name,
			OriginalID: strings.TrimSuffix(path.Base(name), ".md"),
			SessionID:  sess.ID,
		})
	}
	return out, nil
}

// checkConfirmations は警告ごとに確認済みかを検査する。
func checkConfirmations(review *ImportReview, confirm ImportConfirmation) error {
	for _, w := range review.Warnings {
		switch w.Kind {
		case WarnReimport:
			if !confirm.AllowReimport {
				return fmt.Errorf("%w（%s）", ErrConfirmationRequired, w.Message)
			}
		case WarnContentModified:
			if !confirm.AcceptModifiedContent {
				return fmt.Errorf("%w（%s）", ErrConfirmationRequired, w.Message)
			}
		case WarnAddresseeMismatch:
			if !confirm.AcceptAddresseeMismatch {
				return fmt.Errorf("%w（%s）", ErrConfirmationRequired, w.Message)
			}
		default:
			return fmt.Errorf("%w（%s）", ErrConfirmationRequired, w.Message)
		}
	}
	return nil
}

// filterKnownAnswers は質問票に無い質問 ID の回答を落とす（手順7。無視するが UnknownQuestionIDs に残る）。
func filterKnownAnswers(review *ImportReview) *projectstore.Answers {
	known := map[string]bool{}
	for _, q := range review.Questionnaire.Questions {
		known[q.ID] = true
	}
	out := *review.Answers
	out.Answers = nil
	for _, a := range review.Answers.Answers {
		if known[a.QuestionID] {
			out.Answers = append(out.Answers, a)
		}
	}
	return &out
}

// returnArchiveName は受領原本の保存名（同じ質問票への複数回の受領を上書きしない）。
func returnArchiveName(review *ImportReview) string {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	return fmt.Sprintf("%s-return-%s%s", review.Questionnaire.ID, stamp, ExtReturn)
}
