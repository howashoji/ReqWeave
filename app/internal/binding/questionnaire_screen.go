package binding

// 本ファイルは質問票管理と回答取込のバインディング。
//
// 権限判定は前段の共通実装（requireRole）に置き、個別 API へ分散させない。
// 画面へ返す値に内部の生データ（受け渡しファイルのバイト列・パスコード・鍵）を載せない
// （パスコードは発行時に 1 回だけ示し、保存しない）。エラーは「原因＋次の行動」の日本語 1 文。

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// QuestionDraftView は AI が生成した質問文の草案（担当者が編集して確定する）。
type QuestionDraftView struct {
	SourceIssue  string   `json:"sourceIssue"`
	Text         string   `json:"text"`
	Background   string   `json:"background"`
	AnswerFormat string   `json:"answerFormat"`
	Choices      []string `json:"choices,omitempty"`
}

// QuestionDraftsView は質問文生成の結果（AI 障害時は Fallback + Notice で手入力へ縮退）。
type QuestionDraftsView struct {
	Drafts   []QuestionDraftView `json:"drafts"`
	Fallback bool                `json:"fallback"`
	Notice   string              `json:"notice,omitempty"`
}

// GenerateQuestionDrafts は未決事項から質問文を生成する。
//
// AI 呼び出しに失敗しても失敗として返さず、論点を転記したテンプレートを返す（AI が使えなくても手入力で作業を続けられるように）。
func (a *API) GenerateQuestionDrafts(openIssueIDs []string) (QuestionDraftsView, error) {
	s, err := a.current()
	if err != nil {
		return QuestionDraftsView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "質問文の生成"); err != nil {
		return QuestionDraftsView{}, err
	}
	if _, err := a.beginAICall(s, "質問文の生成"); err != nil {
		return QuestionDraftsView{}, err
	}
	result, err := s.engine.GenerateQuestionnaire(a.context(), openIssueIDs)
	if err != nil {
		return QuestionDraftsView{}, err
	}
	out := QuestionDraftsView{Fallback: result.Fallback, Notice: result.Notice}
	for _, d := range result.Drafts {
		out.Drafts = append(out.Drafts, QuestionDraftView{
			SourceIssue: d.SourceOpenIssueID, Text: d.QuestionText, Background: d.Background,
			AnswerFormat: d.AnswerFormat, Choices: d.Choices,
		})
	}
	return out, nil
}

// QuestionInput は担当者が確定した質問 1 件（画面からの入力）。
type QuestionInput struct {
	SourceIssue  string   `json:"sourceIssue"`
	Text         string   `json:"text"`
	Background   string   `json:"background"`
	AnswerFormat string   `json:"answerFormat"`
	Choices      []string `json:"choices,omitempty"`
	// Terms は回答に必要な用語（用語集のキー。質問票に同梱する用語の起点）。
	Terms []string `json:"terms,omitempty"`
}

// IssueDraftRequest は発行する質問票の内容（宛先と質問）。
type IssueDraftRequest struct {
	AddresseeRef string          `json:"addresseeRef"`
	Questions    []QuestionInput `json:"questions"`
}

// IssuePreviewView は発行前プレビュー。
//
// 実際にコンテナへ入る内容そのものから作る（表示と実内容の乖離を作らない）。
type IssuePreviewView struct {
	// QuestionnaireID は発行時に付く ID（この時点では未確定の予定値）。
	QuestionnaireID string `json:"questionnaireId"`
	Addressee       string `json:"addressee"`
	QuestionCount   int    `json:"questionCount"`
	// SourceIssues は発行元未決事項の ID。
	SourceIssues []string `json:"sourceIssues"`
	// QuestionnaireMarkdown は questionnaire.md の全文（プレビュー2）。
	QuestionnaireMarkdown string `json:"questionnaireMarkdown"`
	// Terms は同梱する用語（プレビュー3）。
	Terms []TermView `json:"terms,omitempty"`
	// Excluded は「含まれないもの」の定型表示（プレビュー4）。
	Excluded []string `json:"excluded"`
}

// TermView は同梱する用語 1 件。
type TermView struct {
	Name       string `json:"name"`
	NameEn     string `json:"nameEn"`
	Definition string `json:"definition"`
}

// IssueResultView は発行の結果（出力完了時に画面へ示す内容）。
type IssueResultView struct {
	QuestionnaireID string    `json:"questionnaireId"`
	Path            string    `json:"path"`
	IssuedAt        time.Time `json:"issuedAt"`
	// Passcode は 1 回だけ提示する（保存も再表示もしない）。
	Passcode string `json:"passcode"`
	// PasscodeNotice は別経路での伝達と再表示不可の案内。
	PasscodeNotice string `json:"passcodeNotice"`
	// Reissued は再発行か（旧ファイルの回答途中データが引き継がれない旨を画面が併記する）。
	Reissued bool `json:"reissued"`
}

// passcodeNotice は発行時に必ず併記する案内（パスコードをファイルと同じ経路で送られないようにする）。
const passcodeNotice = "パスコードは質問票ファイルとは別の経路（電話・チャット等）で宛先へお伝えください。" +
	"本システムはパスコードを保存せず、この画面を閉じると再表示できません（忘れた場合は再発行になります）。"

// reissueNotice は再発行時に併記する案内。
const reissueNotice = "再発行したため、前のファイルで宛先が入力していた回答途中のデータは引き継がれません。"

// PreviewQuestionnaireIssue は発行前プレビューを作る（ファイルもレコードも書かない）。
//
// 確認操作の後に IssueQuestionnaire を同じ内容で呼ぶ。
func (a *API) PreviewQuestionnaireIssue(req IssueDraftRequest) (IssuePreviewView, error) {
	s, err := a.current()
	if err != nil {
		return IssuePreviewView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "質問票の発行"); err != nil {
		return IssuePreviewView{}, err
	}
	draft, err := a.buildIssueDraft(s, req)
	if err != nil {
		return IssuePreviewView{}, err
	}
	content, err := buildIssueContent(s, draft)
	if err != nil {
		return IssuePreviewView{}, err
	}
	return issuePreviewView(content), nil
}

// buildIssueDraft は画面の入力から質問票レコードの下書きを作る（ID はこの時点では予定値）。
func (a *API) buildIssueDraft(s *dialogueSession, req IssueDraftRequest) (*projectstore.Questionnaire, error) {
	if len(req.Questions) == 0 {
		return nil, errors.New("質問がありません。質問を 1 件以上入力してから発行してください。")
	}
	roster, err := s.store.LoadRoster()
	if err != nil {
		return nil, err
	}
	addressee, ok := roster.Find(req.AddresseeRef)
	if !ok {
		return nil, errors.New("宛先が名簿にありません。名簿へ登録してから発行してください。")
	}
	nextID, err := s.store.NextID(projectstore.IDQuestionnaire)
	if err != nil {
		return nil, err
	}
	q := &projectstore.Questionnaire{
		ID:           nextID,
		AddresseeRef: addressee.ID,
		Addressee:    stakeholderLabel(addressee),
		IssuedAt:     time.Now().UTC().Truncate(time.Second),
		IssuedBy:     s.store.Author().AuthorID,
		Status:       projectstore.QuestionnaireIssued,
	}
	for i, in := range req.Questions {
		q.Questions = append(q.Questions, projectstore.Question{
			ID:           fmt.Sprintf("q-%02d", i+1),
			SourceIssue:  in.SourceIssue,
			AnswerFormat: in.AnswerFormat,
			Choices:      in.Choices,
			Terms:        in.Terms,
			Text:         in.Text,
			Background:   in.Background,
		})
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	return q, nil
}

// buildIssueContent は発行内容一式を組み立てる（プレビューと出力で同じ経路を通る）。
func buildIssueContent(s *dialogueSession, q *projectstore.Questionnaire) (*exchange.IssueContent, error) {
	kp, err := exchange.EnsureProjectKeyPair(s.store)
	if err != nil {
		return nil, err
	}
	terms, err := s.store.LoadTerms()
	if err != nil {
		return nil, err
	}
	return exchange.BuildIssueContent(q, terms, kp.Public)
}

func issuePreviewView(c *exchange.IssueContent) IssuePreviewView {
	view := IssuePreviewView{
		QuestionnaireID:       c.Questionnaire.ID,
		Addressee:             c.Questionnaire.Addressee,
		QuestionCount:         len(c.Questionnaire.Questions),
		SourceIssues:          c.SourceIssues(),
		QuestionnaireMarkdown: string(c.QuestionnaireMarkdown),
		Excluded:              exchange.ExcludedFromIssue,
	}
	for _, t := range c.Terms {
		view.Terms = append(view.Terms, TermView{Name: t.Name, NameEn: t.NameEn, Definition: t.Definition})
	}
	return view
}

// IssueQuestionnaire は質問票を記録して発行用ファイルを出力する。
//
// プレビューと同じ req を渡す。ID の採番はここで確定するため、プレビュー時の予定値と
// 食い違った場合（他の作業者が先に採番した場合）は出力せずにやり直しを促す
// （プレビューと実出力の乖離を作らない）。
func (a *API) IssueQuestionnaire(req IssueDraftRequest, dst string) (IssueResultView, error) {
	s, err := a.current()
	if err != nil {
		return IssueResultView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "質問票の発行"); err != nil {
		return IssueResultView{}, err
	}
	draft, err := a.buildIssueDraft(s, req)
	if err != nil {
		return IssueResultView{}, err
	}
	preview, err := buildIssueContent(s, draft)
	if err != nil {
		return IssueResultView{}, err
	}

	created, err := s.store.CreateQuestionnaire(*draft)
	if err != nil {
		return IssueResultView{}, err
	}
	content, err := buildIssueContent(s, created)
	if err != nil {
		return IssueResultView{}, err
	}
	if !bytes.Equal(preview.QuestionnaireMarkdown, content.QuestionnaireMarkdown) {
		return IssueResultView{}, errors.New("発行の途中で内容が変わりました。もう一度内容を確認してから発行してください。")
	}

	result, err := exchange.WriteIssue(s.store, content, dst)
	if err != nil {
		return IssueResultView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: created.ID, Change: auditlog.ChangeCreated,
		After: created.Addressee, Evidence: joinIDs(content.SourceIssues()),
	})
	return IssueResultView{
		QuestionnaireID: created.ID, Path: result.Path, IssuedAt: result.IssuedAt,
		Passcode: result.Passcode, PasscodeNotice: passcodeNotice,
	}, nil
}

// ReissueQuestionnaire は同一質問票を新しいパスコードで再出力する。
//
// パスコード忘失時の唯一の回復経路。発行控えと突合基準を更新し、変更履歴へ記録する。
func (a *API) ReissueQuestionnaire(questionnaireID, dst string) (IssueResultView, error) {
	s, err := a.current()
	if err != nil {
		return IssueResultView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "質問票の再発行"); err != nil {
		return IssueResultView{}, err
	}
	q, err := s.store.LoadQuestionnaire(questionnaireID)
	if err != nil {
		return IssueResultView{}, err
	}
	if q.Status != projectstore.QuestionnaireIssued {
		return IssueResultView{}, errors.New("回答が届いている質問票は再発行できません。新しい質問票を発行してください。")
	}
	content, err := buildIssueContent(s, q)
	if err != nil {
		return IssueResultView{}, err
	}
	result, err := exchange.WriteIssue(s.store, content, dst)
	if err != nil {
		return IssueResultView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: q.ID, Change: auditlog.ChangeUpdated, After: "再発行",
	})
	return IssueResultView{
		QuestionnaireID: q.ID, Path: result.Path, IssuedAt: result.IssuedAt,
		Passcode: result.Passcode, PasscodeNotice: passcodeNotice + reissueNotice, Reissued: true,
	}, nil
}

// ChooseIssueDestination は発行用ファイルの保存先を選ぶ（OS の保存ダイアログ）。
//
// 既定のファイル名は質問票 ID とする（宛先氏名をファイル名に出さない = 誤送付時の情報露出を避ける）。
func (a *API) ChooseIssueDestination(questionnaireID string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("保存先の選択を開けません。アプリを再起動してください。")
	}
	name := questionnaireID
	if name == "" {
		name = "questionnaire"
	}
	return wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "質問票ファイルの保存先を選ぶ",
		DefaultFilename: name + exchange.ExtIssue,
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "質問票ファイル (*" + exchange.ExtIssue + ")", Pattern: "*" + exchange.ExtIssue},
		},
	})
}

// ChooseReturnFile は取り込む返送用ファイルを選ぶ（OS の選択ダイアログ）。
func (a *API) ChooseReturnFile() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("ファイルの選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "返送ファイルを選ぶ",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "返送ファイル (*" + exchange.ExtReturn + ")", Pattern: "*" + exchange.ExtReturn},
		},
	})
}

// QuestionnaireView は質問票一覧の 1 行。
type QuestionnaireView struct {
	ID        string    `json:"id"`
	Addressee string    `json:"addressee"`
	IssuedAt  time.Time `json:"issuedAt"`
	Status    string    `json:"status"`
	// StatusLabel は利用者向けの状態表示（生のコード値を画面に出さない）。
	StatusLabel string `json:"statusLabel"`
	// ElapsedDays は発行日からの経過日数（暦日。ローカル時刻の日付差）。
	ElapsedDays int `json:"elapsedDays"`
	// SourceIssues は発行元未決事項（未決事項一覧からの逆引き表示に使う）。
	SourceIssues []string `json:"sourceIssues,omitempty"`
}

// questionnaireStatusLabels は状態の表示名。型で網羅を強制する。
var questionnaireStatusLabels = map[string]string{
	projectstore.QuestionnaireIssued:   "発行済み",
	projectstore.QuestionnaireAnswered: "回答済み",
	projectstore.QuestionnaireImported: "取込済み",
}

// questionnaireStatusLabel は状態の表示名を返す（未知の値は生のコード値を出さない）。
func questionnaireStatusLabel(status string) string {
	if label, ok := questionnaireStatusLabels[status]; ok {
		return label
	}
	return "状態不明"
}

// Questionnaires は質問票一覧を返す（閲覧権限でも参照できる）。
func (a *API) Questionnaires() ([]QuestionnaireView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	list, err := s.store.ListQuestionnaires()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]QuestionnaireView, 0, len(list))
	for _, q := range list {
		out = append(out, QuestionnaireView{
			ID: q.ID, Addressee: q.Addressee, IssuedAt: q.IssuedAt, Status: q.Status,
			StatusLabel:  questionnaireStatusLabel(q.Status),
			ElapsedDays:  elapsedDays(q.IssuedAt, now),
			SourceIssues: sourceIssuesOf(q),
		})
	}
	return out, nil
}

// elapsedDays は発行日からの経過日数を暦日で返す。
//
// 時刻の差ではなくローカル時刻の日付の差で数える（発行が 23:00、参照が翌 1:00 なら 1 日）。
func elapsedDays(issuedAt, now time.Time) int {
	from := time.Date(issuedAt.In(now.Location()).Year(), issuedAt.In(now.Location()).Month(),
		issuedAt.In(now.Location()).Day(), 0, 0, 0, 0, now.Location())
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(to.Sub(from).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// sourceIssuesOf は質問票の発行元未決事項を重複なく返す。
func sourceIssuesOf(q projectstore.Questionnaire) []string {
	seen := map[string]bool{}
	var out []string
	for _, question := range q.Questions {
		if question.SourceIssue == "" || seen[question.SourceIssue] {
			continue
		}
		seen[question.SourceIssue] = true
		out = append(out, question.SourceIssue)
	}
	return out
}

// ---- 回答取込 ------------------------------------------------------------

// ImportWarningView は確認操作を要する検証結果（再取込・内容の改変・宛先の食い違い）。
type ImportWarningView struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

// AnswerMatchView は質問・回答・発行元未決事項の対応表示。
type AnswerMatchView struct {
	QuestionID   string `json:"questionId"`
	QuestionText string `json:"questionText"`
	SourceIssue  string `json:"sourceIssue"`
	// Kind は answered / unknown / 未回答のときは空。
	Kind     string   `json:"kind,omitempty"`
	Selected []string `json:"selected,omitempty"`
	FreeText string   `json:"freeText,omitempty"`
	Body     string   `json:"body,omitempty"`
	Answered bool     `json:"answered"`
}

// ImportReviewView は返送ファイルの検証結果（取込前の確認材料）。
//
// 受け渡しファイルのバイト列は画面へ返さない（API 側で保持する）。
type ImportReviewView struct {
	QuestionnaireID string            `json:"questionnaireId"`
	Addressee       string            `json:"addressee"`
	Respondent      string            `json:"respondent"`
	AnsweredAt      time.Time         `json:"answeredAt"`
	Matches         []AnswerMatchView `json:"matches"`
	// MissingQuestionIDs は未回答、UnknownQuestionIDs は質問票に無い回答（無視して記録に残す）。
	MissingQuestionIDs []string            `json:"missingQuestionIds,omitempty"`
	UnknownQuestionIDs []string            `json:"unknownQuestionIds,omitempty"`
	Warnings           []ImportWarningView `json:"warnings,omitempty"`
	// SessionCount は返送に含まれるステークホルダーセッションの数（再採番して保存する）。
	SessionCount int `json:"sessionCount"`
}

// ValidateReturnFile は返送ファイルを検証して突合結果を返す。
//
// プロジェクトデータを一切変更しない。検証結果は API 側で保持し、ImportReturnFile で反映する。
func (a *API) ValidateReturnFile(src string) (ImportReviewView, error) {
	s, err := a.current()
	if err != nil {
		return ImportReviewView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "回答の取り込み"); err != nil {
		return ImportReviewView{}, err
	}
	review, err := exchange.ValidateReturn(s.store, src)
	if err != nil {
		// 動作ログ（受け渡し系の失敗 = ファイル形式版・失敗種別。質問票 ID を添える）。
		a.recordExchangeFailure(exchangeOpValidateReturn, src, err)
		return ImportReviewView{}, err
	}
	a.setPendingImport(review)
	return importReviewView(review), nil
}

func importReviewView(r *exchange.ImportReview) ImportReviewView {
	view := ImportReviewView{
		QuestionnaireID:    r.Questionnaire.ID,
		Addressee:          r.Questionnaire.Addressee,
		Respondent:         r.Answers.Respondent,
		AnsweredAt:         r.Answers.AnsweredAt,
		MissingQuestionIDs: r.MissingQuestionIDs,
		UnknownQuestionIDs: r.UnknownQuestionIDs,
		SessionCount:       len(r.Sessions),
	}
	for _, m := range r.Matches {
		row := AnswerMatchView{
			QuestionID: m.Question.ID, QuestionText: m.Question.Text, SourceIssue: m.SourceIssue,
		}
		if m.Answer != nil {
			row.Answered = true
			row.Kind = m.Answer.Kind
			row.Selected = m.Answer.Selected
			row.FreeText = m.Answer.FreeText
			row.Body = m.Answer.Body
		}
		view.Matches = append(view.Matches, row)
	}
	for _, w := range r.Warnings {
		view.Warnings = append(view.Warnings, ImportWarningView{
			Kind: w.Kind, Message: w.Message, Before: w.Before, After: w.After})
	}
	return view
}

// ImportResultView は取込の結果（発行済み → 回答済み）。
type ImportResultView struct {
	QuestionnaireID string `json:"questionnaireId"`
	Status          string `json:"status"`
	StatusLabel     string `json:"statusLabel"`
	// SessionIDs は再採番して保存したステークホルダーセッション。
	SessionIDs []string `json:"sessionIds,omitempty"`
}

// ImportReturnFile は検証済みの返送ファイルを取り込む。
//
// 直前の ValidateReturnFile の結果に対して行う。確認を要する警告が確認されていない場合は
// 何も書き込まずに理由を返す。
func (a *API) ImportReturnFile(confirm exchange.ImportConfirmation) (ImportResultView, error) {
	s, err := a.current()
	if err != nil {
		return ImportResultView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "回答の取り込み"); err != nil {
		return ImportResultView{}, err
	}
	review := a.pendingImport()
	if review == nil {
		return ImportResultView{}, errors.New("取り込む返送ファイルが選ばれていません。返送ファイルを読み込んでください。")
	}
	result, err := exchange.ApplyReturn(s.store, review, confirm)
	if err != nil {
		return ImportResultView{}, err
	}
	a.setPendingImport(nil)

	view := ImportResultView{
		QuestionnaireID: result.QuestionnaireID, Status: result.Status,
		StatusLabel: questionnaireStatusLabel(result.Status),
	}
	for _, imported := range result.Sessions {
		view.SessionIDs = append(view.SessionIDs, imported.SessionID)
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: result.QuestionnaireID, Change: auditlog.ChangeStatusChanged,
		Before: projectstore.QuestionnaireIssued, After: result.Status,
		Evidence: review.SourceName,
	})
	return view, nil
}

// AnalyzeImportedAnswers は取り込んだ回答から反映差分を生成する。
//
// AI 呼び出しに失敗しても失敗として返さず、突き合わせ表示の材料を Fallback として返す。
func (a *API) AnalyzeImportedAnswers(questionnaireID string) (*dialogue.ImportAnalysis, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "回答の分析"); err != nil {
		return nil, err
	}
	if _, err := a.beginAICall(s, "回答の分析"); err != nil {
		return nil, err
	}
	return s.engine.AnalyzeAnswers(a.context(), questionnaireID)
}

// ApproveImportDiff は承認された反映差分を記録へ反映する。
//
// 承認していない差分は渡らないため反映されない。反映完了で質問票が取込済みになる。
func (a *API) ApproveImportDiff(questionnaireID string, req dialogue.ImportApproval) (ApprovalOutcome, error) {
	s, err := a.current()
	if err != nil {
		return ApprovalOutcome{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "反映差分の承認"); err != nil {
		return ApprovalOutcome{}, err
	}
	result, err := s.engine.ApplyImportApproval(questionnaireID, req)
	if err != nil {
		if outcome, ok := conflictOutcome(err); ok {
			return outcome, nil
		}
		return ApprovalOutcome{}, err
	}
	return ApprovalOutcome{Applied: appliedFrom(result.ApprovalResult, nil, result)}, nil
}

// joinIDs は ID 列を変更履歴の evidence 用に 1 行へまとめる。
func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += id
	}
	return out
}

// setPendingImport は検証済みの返送ファイルを保持する（画面へバイト列を返さないため）。
func (a *API) setPendingImport(review *exchange.ImportReview) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.session.pendingImport = review
	}
}

// pendingImport は直前に検証した返送ファイルを返す（無ければ nil）。
func (a *API) pendingImport() *exchange.ImportReview {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return nil
	}
	return a.session.pendingImport
}
