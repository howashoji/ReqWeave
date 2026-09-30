package binding

// 本ファイルは決定・未決一覧・要件項目一覧/詳細・対話履歴一覧向けの公開バインディング。
//
// 決定事項の本文を書き換える経路は設けない（覆すときは新しい決定で置き換え、元の記録を残す）。
// 要件項目は手動編集・差し戻しのみ更新でき、差し戻しには理由が要る。

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 未決事項を発行元に持つ質問票が 1 つも無いときの表示。
const questionnaireNotIssued = "未発行"

// questionnaireProgress は質問票の状態の進み具合（小さいほど手前）。
// 1 つの未決事項が複数の質問票から問われている場合は、最も手前の状態を未決事項の行に出す
// （回答を待っている質問票が 1 つでも残っていれば「発行済み」と見える）。
var questionnaireProgress = map[string]int{
	projectstore.QuestionnaireIssued:   0,
	projectstore.QuestionnaireAnswered: 1,
	projectstore.QuestionnaireImported: 2,
}

// questionnaireStatusByIssue は未決事項 ID ごとの質問票の発行状態（表示名）を返す。
// 未決事項から質問票へは、質問ごとの発行元未決事項（source_issue）で逆引きする。
func questionnaireStatusByIssue(store *projectstore.Store) (map[string]string, error) {
	list, err := store.ListQuestionnaires()
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, q := range list {
		rank, ok := questionnaireProgress[q.Status]
		if !ok {
			continue
		}
		for _, question := range q.Questions {
			current, seen := status[question.SourceIssue]
			if !seen || rank < questionnaireProgress[current] {
				status[question.SourceIssue] = q.Status
			}
		}
	}
	labels := make(map[string]string, len(status))
	for issue, st := range status {
		labels[issue] = questionnaireStatusLabel(st)
	}
	return labels, nil
}

// DecisionView は決定事項一覧の 1 行。
type DecisionView struct {
	ID        string   `json:"id"`
	TopicKey  string   `json:"topicKey"`
	DecidedAt string   `json:"decidedAt"`
	Body      string   `json:"body"`
	Evidence  []string `json:"evidence"`
	// SupersededBy は置き換え先の決定事項 ID（覆された決定）。
	SupersededBy string `json:"supersededBy,omitempty"`
	Supersedes   string `json:"supersedes,omitempty"`
}

// OpenIssueView は未決事項一覧の 1 行。
type OpenIssueView struct {
	ID       string   `json:"id"`
	Topic    string   `json:"topic"`
	Owner    string   `json:"owner"`
	Due      string   `json:"due,omitempty"`
	Status   string   `json:"status"`
	Overdue  bool     `json:"overdue"`
	Blocking []string `json:"blocking,omitempty"`
	// QuestionnaireStatus は質問票の発行状態（未発行 / 発行済み / 回答済み / 取込済み）。
	QuestionnaireStatus string   `json:"questionnaireStatus"`
	ResolvedBy          string   `json:"resolvedBy,omitempty"`
	Evidence            []string `json:"evidence"`
}

// RequirementView は要件項目一覧の 1 行。
type RequirementView struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Chapter            string   `json:"chapter"`
	Kind               string   `json:"kind"`
	Priority           string   `json:"priority"`
	Status             string   `json:"status"`
	Body               string   `json:"body"`
	AcceptanceCriteria []string `json:"acceptanceCriteria,omitempty"`
	Decisions          []string `json:"decisions,omitempty"`
	Evidence           []string `json:"evidence,omitempty"`
	BlockedBy          []string `json:"blockedBy,omitempty"`
	RevertedReason     string   `json:"revertedReason,omitempty"`
	// MissingEvidence は根拠へたどれない項目（参照欠落）。
	MissingEvidence bool `json:"missingEvidence"`
}

// EvidenceView は根拠の遡及表示。
type EvidenceView struct {
	Ref string `json:"ref"`
	// Found は参照先が見つかったか（見つからない場合は参照欠落として表示する）。
	Found bool `json:"found"`
	// Context は根拠発話の前後文脈（前 1 件・当該・後 1 件）。
	Context []UtteranceView `json:"context,omitempty"`
	// Import は取り込み資料が根拠のとき、原本の所在と該当箇所。
	Import *importer.RefLocation `json:"import,omitempty"`
	// Answer は質問票の回答が根拠のとき、質問と回答の内容。
	Answer *AnswerEvidenceView `json:"answer,omitempty"`
}

// AnswerEvidenceView は回答（`QS-nnn#q-nn`）を根拠に持つレコードの遡及先。
//
// 回答本文だけでは「何を聞かれた回答か」が分からないため、**質問側も併せて返す**
// （決定の根拠を確かめるには問いと答えの対が要る）。
type AnswerEvidenceView struct {
	QuestionnaireID string `json:"questionnaireId"`
	QuestionID      string `json:"questionId"`
	// QuestionText / Background は質問側。
	QuestionText string `json:"questionText"`
	Background   string `json:"background,omitempty"`
	// SourceIssue は発行元の未決事項（ISS-nnn）。
	SourceIssue string `json:"sourceIssue,omitempty"`
	// Respondent / AnsweredAt は回答者と回答日時。
	Respondent string `json:"respondent"`
	AnsweredAt string `json:"answeredAt"`
	// Kind は answered / unknown（unknown は回答者が「不明」と答えたもの）。
	Kind string `json:"kind"`
	// Selected / FreeText / Body は回答の中身。
	Selected []string `json:"selected,omitempty"`
	FreeText string   `json:"freeText,omitempty"`
	Body     string   `json:"body,omitempty"`
}

// Decisions は決定事項一覧を返す（本文を書き換える API は設けない）。
func (a *API) Decisions() ([]DecisionView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	decisions, err := s.store.ListDecisions()
	if err != nil {
		return nil, err
	}
	out := make([]DecisionView, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, DecisionView{
			ID: d.ID, TopicKey: d.TopicKey, DecidedAt: d.DecidedAt.UTC().Format(time.RFC3339),
			Body: d.Body, Evidence: d.Evidence, SupersededBy: d.SupersededBy, Supersedes: d.Supersedes,
		})
	}
	return out, nil
}

// OpenIssues は未決事項一覧を返す（ブロック対象・期限超過・質問票状態つき）。
func (a *API) OpenIssues() ([]OpenIssueView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	issues, err := s.store.ListOpenIssues()
	if err != nil {
		return nil, err
	}
	reqs, err := s.store.ListRequirements()
	if err != nil {
		return nil, err
	}
	blocking := map[string][]string{}
	for _, r := range reqs {
		for _, id := range r.BlockedBy {
			blocking[id] = append(blocking[id], r.ID)
		}
	}
	questionnaires, err := questionnaireStatusByIssue(s.store)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]OpenIssueView, 0, len(issues))
	for _, i := range issues {
		qs, ok := questionnaires[i.ID]
		if !ok {
			qs = questionnaireNotIssued
		}
		out = append(out, OpenIssueView{
			ID: i.ID, Topic: i.Body, Owner: i.Owner, Due: i.Due, Status: i.Status,
			Overdue: i.IsOverdue(now), Blocking: blocking[i.ID],
			QuestionnaireStatus: qs,
			ResolvedBy:          i.ResolvedBy, Evidence: i.Evidence,
		})
	}
	return out, nil
}

// Requirements は要件項目一覧を返す。
func (a *API) Requirements() ([]RequirementView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	reqs, err := s.store.ListRequirements()
	if err != nil {
		return nil, err
	}
	out := make([]RequirementView, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, toRequirementView(r))
	}
	return out, nil
}

func toRequirementView(r projectstore.Requirement) RequirementView {
	return RequirementView{
		ID: r.ID, Title: r.Title, Chapter: r.Chapter, Kind: r.Kind, Priority: r.Priority,
		Status: r.Status, Body: r.Body, AcceptanceCriteria: r.AcceptanceCriteria,
		Decisions: r.Decisions, Evidence: r.Evidence, BlockedBy: r.BlockedBy,
		RevertedReason: r.RevertedReason, MissingEvidence: !r.HasEvidence(),
	}
}

// EditRequirementRequest は要件項目の手動編集（AI 障害中も可）。
type EditRequirementRequest struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	AcceptanceCriteria []string `json:"acceptanceCriteria,omitempty"`
	Priority           string   `json:"priority,omitempty"`
}

// EditRequirement は要件項目を手動編集する（状態は変えない）。
func (a *API) EditRequirement(req EditRequirementRequest) (RequirementView, error) {
	s, err := a.current()
	if err != nil {
		return RequirementView{}, err
	}
	if strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Body) == "" {
		return RequirementView{}, errors.New("要件名と要件文を入力してください。")
	}
	// 共有レコードの書き換えは基準版を検証してから行う（他の人の更新を上書きしないため）。
	base, err := s.store.CurrentBaseline(req.ID)
	if err != nil {
		return RequirementView{}, err
	}
	updated, err := s.store.UpdateRequirementGuarded(base, req.ID, func(r *projectstore.Requirement) error {
		r.Title = req.Title
		r.Body = req.Body
		r.AcceptanceCriteria = req.AcceptanceCriteria
		if req.Priority != "" {
			r.Priority = req.Priority
		}
		return nil
	})
	if err != nil {
		return RequirementView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{Target: updated.ID, Change: auditlog.ChangeUpdated,
		After: updated.Title})
	return toRequirementView(*updated), nil
}

// AgreeRequirement は要件項目を合意済みにする（確定前の状態遷移）。
func (a *API) AgreeRequirement(id string) (RequirementView, error) {
	s, err := a.current()
	if err != nil {
		return RequirementView{}, err
	}
	// 共有レコードの書き換えは基準版を検証してから行う（他の人の更新を上書きしないため）。
	base, err := s.store.CurrentBaseline(id)
	if err != nil {
		return RequirementView{}, err
	}
	updated, err := s.store.UpdateRequirementGuarded(base, id, func(r *projectstore.Requirement) error {
		r.Status = projectstore.RequirementAgreed
		r.RevertedReason = ""
		return nil
	})
	if err != nil {
		return RequirementView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{Target: id, Change: auditlog.ChangeStatusChanged,
		Before: projectstore.RequirementDraft, After: projectstore.RequirementAgreed})
	return toRequirementView(*updated), nil
}

// RevertRequirement は要件項目を差し戻す（agreed → draft）。理由が必須。
//
// 差し戻しは成果物の内容を変える操作のため、現在フェーズの成果物種別の予約を確認・記録し、
// 完了時に解除する。work は着手時の進め方の選択。
func (a *API) RevertRequirement(id, reason string, work WorkStart) (RequirementView, error) {
	s, err := a.current()
	if err != nil {
		return RequirementView{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return RequirementView{}, errors.New("差し戻しの理由を入力してください。")
	}
	kind, err := documentKindOfPhase(s.store.Project().Phase)
	if err != nil {
		return RequirementView{}, err
	}
	release, err := a.beginDocumentWork(s, kind, work)
	if err != nil {
		return RequirementView{}, err
	}
	defer release()
	// 共有レコードの書き換えは基準版を検証してから行う（他の人の更新を上書きしないため）。
	base, err := s.store.CurrentBaseline(id)
	if err != nil {
		return RequirementView{}, err
	}
	updated, err := s.store.UpdateRequirementGuarded(base, id, func(r *projectstore.Requirement) error {
		r.Status = projectstore.RequirementDraft
		r.RevertedReason = reason
		return nil
	})
	if err != nil {
		return RequirementView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{Target: id, Change: auditlog.ChangeStatusChanged,
		Before: projectstore.RequirementAgreed, After: projectstore.RequirementDraft, Evidence: reason})
	return toRequirementView(*updated), nil
}

// Evidence は根拠参照から発話の前後文脈を返す（根拠の遡及表示）。
func (a *API) Evidence(ref string) (EvidenceView, error) {
	s, err := a.current()
	if err != nil {
		return EvidenceView{}, err
	}
	out := EvidenceView{Ref: ref}
	// 取り込み資料の根拠は原本の所在と抽出テキストの該当箇所を返す。
	if _, _, _, ok := importer.ParseRef(ref); ok {
		loc, err := importer.New(s.store).ResolveRef(ref)
		if err != nil {
			return out, nil // 資料・行範囲が無い = 参照欠落として表示する
		}
		out.Found = true
		out.Import = loc
		return out, nil
	}
	// 質問票の回答の根拠は、問いと答えの対を返す。
	if questionnaireID, questionID, ok := projectstore.ParseAnswerRef(ref); ok {
		answer, found := resolveAnswerEvidence(s.store, questionnaireID, questionID)
		if !found {
			return out, nil // 質問票・設問・回答のいずれかが無い = 参照欠落として表示する
		}
		out.Found = true
		out.Answer = answer
		return out, nil
	}
	sessionID, utteranceID, ok := strings.Cut(ref, "#")
	if !ok {
		return out, nil // 参照形式として解釈できない = 参照欠落として表示する
	}
	_, utterances, err := s.store.LoadSession(sessionID)
	if err != nil {
		return out, nil // 参照先が見つからない = 参照欠落として表示する
	}
	for i, u := range utterances {
		if u.ID != utteranceID {
			continue
		}
		out.Found = true
		from, to := i-1, i+1
		if from < 0 {
			from = 0
		}
		if to >= len(utterances) {
			to = len(utterances) - 1
		}
		for _, ctx := range utterances[from : to+1] {
			out.Context = append(out.Context, UtteranceView{ID: ctx.ID, Speaker: ctx.Speaker,
				At: ctx.At.UTC().Format(time.RFC3339), Status: ctx.Status, Body: ctx.Body})
		}
		break
	}
	return out, nil
}

// resolveAnswerEvidence は `QS-nnn#q-nn` を質問票・回答から解決する。
//
// 質問が無い・回答ファイルが無い・当該設問への回答が無いときは見つからない扱いにする
// （「質問票は発行したがまだ回答が返っていない」状態を、回答があったかのように見せない）。
func resolveAnswerEvidence(store *projectstore.Store, questionnaireID, questionID string) (*AnswerEvidenceView, bool) {
	q, err := store.LoadQuestionnaire(questionnaireID)
	if err != nil || q == nil {
		return nil, false
	}
	var question *projectstore.Question
	for i := range q.Questions {
		if q.Questions[i].ID == questionID {
			question = &q.Questions[i]
			break
		}
	}
	if question == nil {
		return nil, false
	}
	answers, err := store.LoadAnswers(questionnaireID)
	if err != nil || answers == nil {
		return nil, false
	}
	for _, a := range answers.Answers {
		if a.QuestionID != questionID {
			continue
		}
		at := answers.AnsweredAt
		if !a.At.IsZero() {
			at = a.At
		}
		return &AnswerEvidenceView{
			QuestionnaireID: questionnaireID,
			QuestionID:      questionID,
			QuestionText:    question.Text,
			Background:      question.Background,
			SourceIssue:     question.SourceIssue,
			Respondent:      answers.Respondent,
			AnsweredAt:      at.UTC().Format(time.RFC3339),
			Kind:            a.Kind,
			Selected:        a.Selected,
			FreeText:        a.FreeText,
			Body:            a.Body,
		}, true
	}
	return nil, false
}

// EvidenceCitations は指定の根拠接頭辞を引いているレコードを返す（根拠参照の逆方向の解決）。
//
// 取り込み資料 ID（`IMP-001`）を渡すと、その資料を根拠に持つ承認済みレコードが得られる
// （資料からの遡及の逆向き）。逆方向の参照は資料側に保持しておらず、
// レコードの evidence から毎回導出する（参照はレコード側の片方向だけに持つ）。
func (a *API) EvidenceCitations(prefix string) ([]projectstore.EvidenceCitation, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	return s.store.CitingRecords(prefix)
}

// MissingEvidenceRecords は根拠へたどれないレコードの一覧を返す。
func (a *API) MissingEvidenceRecords() ([]projectstore.MissingEvidenceRecord, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	return s.store.MissingEvidenceRecords()
}

// ChangeHistoryView は変更履歴の 1 行（決定・未決一覧の変更履歴表示）。
type ChangeHistoryView struct {
	At     string `json:"at"`
	Author string `json:"author"`
	Target string `json:"target"`
	Change string `json:"change"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// ChangeHistory は変更履歴を新しい順で返す（読み出しのみ）。
func (a *API) ChangeHistory() ([]ChangeHistoryView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	changes, err := auditlog.ReadChanges(s.store.Root(), time.Time{}, time.Time{})
	if err != nil {
		return nil, err
	}
	out := make([]ChangeHistoryView, 0, len(changes))
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		out = append(out, ChangeHistoryView{At: c.At.UTC().Format(time.RFC3339), Author: c.Author,
			Target: c.Target, Change: c.Change, Before: c.Before, After: c.After})
	}
	return out, nil
}

// recordChange は変更履歴を追記する（失敗しても操作は成立させ、画面へ通知する）。
func (a *API) recordChange(s *dialogueSession, rec auditlog.ChangeRecord) {
	logger, err := auditlog.New(s.store, s.store.Author().AuthorID)
	if err != nil {
		return
	}
	if err := logger.RecordChange(rec); err != nil {
		a.emitDialogue(dialogue.Event{Kind: dialogue.EventError,
			Message: fmt.Sprintf("記録の保存に失敗しました: %v", err)})
	}
}

// ImpactView は変更影響表示。
//
// データは追跡連鎖と同一ソース: 章割当の依存マップ・要件項目の decisions / blocked_by。
type ImpactView struct {
	// Target は起点のレコード ID。
	Target string `json:"target"`
	// Chapters は当該レコードを収載する成果物の章。
	Chapters []ImpactChapterView `json:"chapters,omitempty"`
	// Requirements は当該レコードを根拠とする要件項目 ID（決定事項を起点にしたとき）。
	Requirements []string `json:"requirements,omitempty"`
	// OpenIssues は当該要件項目をブロックする / していた未決事項 ID。
	OpenIssues []string `json:"openIssues,omitempty"`
	// DesignChapters は当該要件項目 ID を参照する基本設計の章。
	DesignChapters []ImpactChapterView `json:"designChapters,omitempty"`
}

// ImpactChapterView は影響のある章（文書箇所への遷移に使う）。
type ImpactChapterView struct {
	Kind     string `json:"kind"`
	FileName string `json:"fileName"`
	Title    string `json:"title"`
}

// IsEmpty は影響が 1 件も無いかを返す。
func (v ImpactView) IsEmpty() bool {
	return len(v.Chapters) == 0 && len(v.Requirements) == 0 &&
		len(v.OpenIssues) == 0 && len(v.DesignChapters) == 0
}

// Impact は要件項目・決定事項を起点に影響一覧を返す。
func (a *API) Impact(target string) (ImpactView, error) {
	s, err := a.current()
	if err != nil {
		return ImpactView{}, err
	}
	if strings.TrimSpace(target) == "" {
		return ImpactView{}, errors.New("影響を調べる対象を指定してください。")
	}
	out := ImpactView{Target: target}

	reqs, err := s.store.ListRequirements()
	if err != nil {
		return ImpactView{}, err
	}
	issues, err := s.store.ListOpenIssues()
	if err != nil {
		return ImpactView{}, err
	}

	// 決定事項を起点にしたとき: それを根拠とする要件項目。
	for _, r := range reqs {
		for _, id := range r.Decisions {
			if id == target && !contains(out.Requirements, r.ID) {
				out.Requirements = append(out.Requirements, r.ID)
			}
		}
	}
	// 要件項目を起点にしたとき: ブロックする / していた未決事項。
	for _, r := range reqs {
		if r.ID != target {
			continue
		}
		for _, id := range r.BlockedBy {
			if !contains(out.OpenIssues, id) {
				out.OpenIssues = append(out.OpenIssues, id)
			}
		}
	}
	// 決着済みも含めて、当該要件項目を対象にしていた未決事項を拾う。
	for _, i := range issues {
		if i.ResolvedBy == target && !contains(out.OpenIssues, i.ID) {
			out.OpenIssues = append(out.OpenIssues, i.ID)
		}
	}

	// 収載する成果物の章（依存マップと同一の割当ロジックを使う）。
	chapters, design, err := a.impactChapters(s, target)
	if err != nil {
		return ImpactView{}, err
	}
	out.Chapters = chapters
	out.DesignChapters = design
	sort.Strings(out.Requirements)
	sort.Strings(out.OpenIssues)
	return out, nil
}

// impactChapters は当該レコードを収載する章と、ID を参照する基本設計の章を返す。
func (a *API) impactChapters(s *dialogueSession, target string) ([]ImpactChapterView, []ImpactChapterView, error) {
	records, err := docgenRecords(s)
	if err != nil {
		return nil, nil, err
	}
	tmpl, err := docgen.LoadTemplate()
	if err != nil {
		return nil, nil, err
	}

	var covering, design []ImpactChapterView
	for _, kind := range []string{projectstore.DocKindRequirements, projectstore.DocKindBasicDesign} {
		doc, err := tmpl.Document(kind)
		if err != nil {
			return nil, nil, err
		}
		titles, err := chapterTitles(kind)
		if err != nil {
			return nil, nil, err
		}
		// 章割当の依存マップから逆引きする。
		for file, ids := range docgen.Assign(doc, records).DependencyMap() {
			if contains(ids, target) {
				covering = append(covering, ImpactChapterView{
					Kind: kind, FileName: file, Title: titles[file]})
			}
		}
		if kind != projectstore.DocKindBasicDesign {
			continue
		}
		// 基本設計の章が当該要件項目 ID を本文で参照している場合（設計要素からの参照）。
		chapters, err := s.store.LoadDraft(kind)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range chapters {
			if strings.Contains(c.Body, target) {
				design = append(design, ImpactChapterView{
					Kind: kind, FileName: c.FileName, Title: titles[c.FileName]})
			}
		}
	}
	sort.Slice(covering, func(i, j int) bool { return covering[i].FileName < covering[j].FileName })
	sort.Slice(design, func(i, j int) bool { return design[i].FileName < design[j].FileName })
	return covering, design, nil
}

// docgenRecords はドキュメント生成側のレコード型へ詰め替える。
func docgenRecords(s *dialogueSession) (docgen.Records, error) {
	var r docgen.Records
	reqs, err := s.store.ListRequirements()
	if err != nil {
		return r, err
	}
	decisions, err := s.store.ListDecisions()
	if err != nil {
		return r, err
	}
	issues, err := s.store.ListOpenIssues()
	if err != nil {
		return r, err
	}
	terms, err := s.store.LoadTerms()
	if err != nil {
		return r, err
	}
	r.Requirements, r.Decisions, r.OpenIssues, r.Terms = reqs, decisions, issues, terms.Terms
	return r, nil
}

// contains は文字列の集合に値が含まれるかを返す。
func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
