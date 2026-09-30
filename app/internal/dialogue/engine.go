package dialogue

// 本ファイルは対話ループ本体を担う。
//
// 状態遷移ごとの永続化は保存キュー経由で行い、
// 利用者の発話は AI 送信前に保存する（AI の障害で回答を失わないため）。
// 中断・障害時の状態（提示中の質問・未承認の候補）は併置メタデータへ保存し、再開時に復元する。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 対話イベントの種別（バインディング層が Wails イベントへ変換する）。
const (
	// EventText は本文差分（ストリーミング表示）。
	EventText = "text"
	// EventReplaced は既決論点だったため質問を作り直したこと（それまでの本文を破棄する）。
	EventReplaced = "replaced"
	// EventDone は完了（中断を含む）。
	EventDone = "done"
	// EventError は障害停止（再試行の上限超過）。
	EventError = "error"
)

// Event は対話エンジンが画面へ流すイベント。
type Event struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// UtteranceID は EventDone のとき保存した発話 ID。
	UtteranceID string `json:"utteranceId,omitempty"`
	// State は EventDone / EventError 後の対話状態。
	State string `json:"state,omitempty"`
	// Interrupted は利用者の中断で終わったか。
	Interrupted bool `json:"interrupted,omitempty"`
	// TopicKey は EventDone のとき提示した論点キー。
	TopicKey string `json:"topicKey,omitempty"`
	// Reconfirm は既決論点への再確認として提示したか。
	Reconfirm bool `json:"reconfirm,omitempty"`
	// ErrorClass は EventError のときのエラー区分（transient / config / permanent）。
	ErrorClass string `json:"errorClass,omitempty"`
	// ErrorCode は EventError のときのプロバイダ固有コード（aiprovider.ProviderError.Code）。
	// 画面の文言は分類 × Code から作る（**未知の Code は分類ごとの既定へ倒す**）。
	ErrorCode string `json:"errorCode,omitempty"`
	// UserMessage は利用者向けの 1 文（原因＋次の行動）。
	// **公開バインディング層が組み立てて詰める**（エラーの文言の一覧を 1 か所に保つため）。
	UserMessage string `json:"userMessage,omitempty"`
	// Message は EventError / EventFallback のときの原因（利用者向け文言は上位が作る）。
	Message string `json:"message,omitempty"`
	// Extraction は EventCandidates のときの抽出候補。
	Extraction *Extraction `json:"extraction,omitempty"`
}

// Config は対話エンジンの構成。
type Config struct {
	Store   *projectstore.Store
	Adapter aiprovider.Adapter
	// Model は選択中モデル（コンテキスト長・最大出力の判定に使う）。
	Model    aiprovider.ModelInfo
	Effort   aiprovider.Effort
	Timeouts aiprovider.Timeouts
	// Recorder は AI 送信記録の発行先。nil でも動くが監査記録は残らない。
	Recorder aiprovider.SendRecorder
	// Logger は変更履歴の記録先。nil でも動くが履歴は残らない。
	Logger *auditlog.Logger
	// OnRecordError は変更履歴の記録に失敗したときの通知先（nil なら通知しない）。
	OnRecordError func(error)
	// OnDegrade は推論努力パラメータの縮退（モデルがそのパラメータを持たない）が起きたときの通知先。
	// 利用者へは通知せず動作ログへ記録するための口（nil なら記録しない）。
	OnDegrade func(model aiprovider.ModelInfo)
	// OnPanic はストリーミング用ゴルーチンのパニックの記録先。
	// 記録先を知るのはバインディング層だけ（本層は動作ログのパッケージへ依存しない）。
	OnPanic aiprovider.PanicRecorder
	// OnCallFailed は AI 呼び出しの失敗の記録先（障害の原因を後から調べられるように、動作ログへ残す）。
	// OnPanic と同じ委譲の形（本層は動作ログのパッケージへ依存しない）。nil なら記録しない。
	OnCallFailed aiprovider.FailureRecorder
}

// effortParams は推論努力の写像・モデル上限での丸め・縮退をまとめて行う。
//
// 縮退が起きたことは OnDegrade で通知する（動作ログへの記録先はバインディング層が渡す）。
// 送信前に効かせる箇所が複数あるため、**縮退の判定と通知はここ 1 か所に閉じる**
// （呼び出し側が degraded を取りこぼすと記録が欠ける）。
func (e *Engine) effortParams() aiprovider.EffortParams {
	effort := aiprovider.MapEffort(e.cfg.Adapter.ID(), e.cfg.Effort).ClampToModel(e.cfg.Model)
	effort, degraded := effort.Degrade(e.cfg.Model)
	if degraded && e.cfg.OnDegrade != nil {
		e.cfg.OnDegrade(e.cfg.Model)
	}
	return effort
}

// Engine は 1 プロジェクトぶんの対話エンジン。
type Engine struct {
	cfg Config
}

// New は対話エンジンを生成する。
func New(cfg Config) (*Engine, error) {
	if cfg.Store == nil {
		return nil, errors.New("プロジェクトが開かれていません")
	}
	if cfg.Adapter == nil {
		return nil, errors.New("AI プロバイダが設定されていません")
	}
	if cfg.Model.ID == "" {
		return nil, errors.New("モデルが選択されていません")
	}
	return &Engine{cfg: cfg}, nil
}

// StartSession は新しい担当者セッションを開始する（初期状態 → 質問生成中）。
func (e *Engine) StartSession(phase string) (*projectstore.Session, error) {
	sess, err := e.cfg.Store.CreateSession(projectstore.SessionOwner, phase)
	if err != nil {
		return nil, err
	}
	if err := e.saveMeta(sess.ID, &SessionState{DialogueState: StateQuestioning}); err != nil {
		return nil, err
	}
	return sess, nil
}

// SessionState は併置メタデータ（セッションファイル）の対話エンジン側の表現。
type SessionState struct {
	DialogueState     string
	Summaries         []projectstore.SummaryBlock
	PresentedQuestion *PresentedQuestion
	// PendingCandidates は未承認の抽出候補（中断しても失わないよう保全する）。構造は Extraction。
	PendingCandidates any
	// Baselines は候補が書き換える共有レコードの基準版。
	// 候補と同じ寿命で保全し、承認・反映の直前の競合検知に使う。
	Baselines map[string]projectstore.RecordBaseline
}

// LoadState は保存済みの対話状態を読む（未保存・未知の状態は中断として扱う）。
func (e *Engine) LoadState(sessionID string) (*SessionState, error) {
	meta, err := e.cfg.Store.LoadSessionMeta(sessionID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return &SessionState{DialogueState: StateSuspended}, nil
	}
	state := &SessionState{
		DialogueState:     meta.DialogueState,
		Summaries:         meta.SummaryBlocks,
		PendingCandidates: meta.PendingCandidates,
		Baselines:         meta.Baselines,
	}
	if !IsKnownState(state.DialogueState) {
		state.DialogueState = StateSuspended
	}
	if meta.PresentedQuestion != nil {
		q, err := decodePresentedQuestion(meta.PresentedQuestion)
		if err != nil {
			return nil, err
		}
		state.PresentedQuestion = q
	}
	return state, nil
}

// saveMeta は対話状態を併置メタデータへ保存する。
func (e *Engine) saveMeta(sessionID string, s *SessionState) error {
	meta := &projectstore.SessionMeta{
		DialogueState:     s.DialogueState,
		SummaryBlocks:     s.Summaries,
		PendingCandidates: encodeExtraction(s.PendingCandidates),
		Baselines:         s.Baselines,
	}
	if s.PresentedQuestion != nil {
		meta.PresentedQuestion = encodePresentedQuestion(s.PresentedQuestion)
	}
	return e.cfg.Store.SaveSessionMeta(sessionID, meta)
}

// setState は状態機械を検証してから状態を移す。
func (e *Engine) setState(sessionID string, s *SessionState, to string) error {
	if err := ValidateTransition(s.DialogueState, to); err != nil {
		return err
	}
	s.DialogueState = to
	return e.saveMeta(sessionID, s)
}

// Records はレコード類を読み出す（文脈注入・充足率算出のデータソース）。
func (e *Engine) Records() (Records, error) {
	var r Records
	reqs, err := e.cfg.Store.ListRequirements()
	if err != nil {
		return r, err
	}
	decisions, err := e.cfg.Store.ListDecisions()
	if err != nil {
		return r, err
	}
	issues, err := e.cfg.Store.ListOpenIssues()
	if err != nil {
		return r, err
	}
	terms, err := e.cfg.Store.LoadTerms()
	if err != nil {
		return r, err
	}
	r.Requirements, r.Decisions, r.OpenIssues, r.Terms = reqs, decisions, issues, terms.Terms
	return r, nil
}

// GenerateQuestion は次の質問を生成してストリーミングする（状態は質問生成中）。
//
// 返すチャネルは EventDone または EventError で必ず閉じる（呼び出し側は読み切ること）。
// ctx のキャンセルは中断であり、受信済み本文を中断発話として保存する。
// 全章観点が充足済みで次の論点が無い場合は (nil, nil) を返す。
func (e *Engine) GenerateQuestion(ctx context.Context, sessionID string) (<-chan Event, error) {
	sess, _, err := e.cfg.Store.LoadSession(sessionID)
	if err != nil {
		return nil, err
	}
	state, err := e.LoadState(sessionID)
	if err != nil {
		return nil, err
	}
	records, err := e.Records()
	if err != nil {
		return nil, err
	}
	completeness, err := Completeness(sess.Phase, records)
	if err != nil {
		return nil, err
	}

	topic, err := e.selectTopic(sess.Phase, records, completeness, state)
	if err != nil {
		return nil, err
	}
	if topic == nil {
		return nil, nil // 充足済み（次の質問が無い）
	}
	if err := e.setState(sessionID, state, StateQuestioning); err != nil {
		return nil, err
	}

	req, labels, err := e.buildQuestionRequest(ctx, sess, records, completeness, topic)
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 16)
	go e.runQuestion(ctx, sessionID, sess, state, records, topic, req, labels, out)
	return out, nil
}

// selectTopic は次の論点を選ぶ。追問上限を超えている場合は継続をやめて次の論点へ移す。
func (e *Engine) selectTopic(phase string, records Records, completeness []ChapterCompleteness, state *SessionState) (*TopicSelection, error) {
	last := state.PresentedQuestion
	limit := ControlsFor(e.cfg.Effort).FollowUpLimit
	if last != nil && ExceedsFollowUpLimit(last.FollowUpIndex+1, limit) {
		// 上限に達した論点は継続しない（AI の遵守に依存せずアプリ側で移行を強制する）。
		last = nil
	}
	perspectives, err := e.perspectives()
	if err != nil {
		return nil, err
	}
	return SelectTopic(phase, records, completeness, last, perspectives)
}

// perspectives は追加の質問観点を返す（プリセット観点・プロジェクト観点）。
//
// プリセットの選択は project.yaml の domain_presets、プロジェクト観点は
// perspectives.yaml。いずれも毎回読み直すため、選択・登録の変更は以後の
// 質問生成から効く。
// 並び順はプリセット → プロジェクト観点（プロンプトへの注入順）。
func (e *Engine) perspectives() ([]SelectedPerspective, error) {
	preset, err := SelectedPerspectives(e.cfg.Store.Project().DomainPresets)
	if err != nil {
		return nil, err
	}
	project, err := ProjectPerspectives(e.cfg.Store)
	if err != nil {
		return nil, err
	}
	return append(preset, project...), nil
}

// buildQuestionRequest は質問生成のリクエストと送信文脈の内訳ラベルを組み立てる。
func (e *Engine) buildQuestionRequest(ctx context.Context, sess *projectstore.Session, records Records,
	completeness []ChapterCompleteness, topic *TopicSelection) (aiprovider.ChatRequest, []string, error) {

	perspectives, err := e.perspectives()
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	system, err := BuildSystemPrompt(SystemPromptInput{
		Phase: sess.Phase, Effort: e.cfg.Effort, Mode: ModeQuestion,
		ExtraPerspectives: PerspectiveLines(perspectives)})
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}

	_, utterances, err := e.cfg.Store.LoadSession(sess.ID)
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	state, err := e.LoadState(sess.ID)
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	effort := e.effortParams()

	fixed := aiprovider.EstimateTokens(system)
	history, _ := CompressHistory(utterances, state.Summaries, HistoryBudget{
		ContextWindow:   e.cfg.Model.ContextWindow,
		FixedTokens:     fixed,
		MaxOutputTokens: effort.MaxOutputTokens,
	})

	contextText, labels := BuildContext(ContextInput{
		Phase: sess.Phase, Records: records, Completeness: completeness,
		CurrentChapter: topic.ChapterID, CurrentTopicKey: topic.TopicKey,
		History: history,
	})
	instruction := fmt.Sprintf(
		"次の論点について質問を 1 件だけ作ってください。\n論点キー: %s（%s）\n選定理由: %s\nこの論点での質問は %d 回目です。",
		topic.TopicKey, topic.ItemName, topic.Reason, topic.FollowUpIndex)

	return aiprovider.ChatRequest{
		Model:    e.cfg.Model.ID,
		System:   system,
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: contextText + "\n\n" + instruction}},
		Effort:   effort,
	}, labels, nil
}

// runQuestion はストリーミングを中継し、完了時に発話保存と状態遷移を行う。
func (e *Engine) runQuestion(ctx context.Context, sessionID string, sess *projectstore.Session,
	state *SessionState, records Records, topic *TopicSelection,
	req aiprovider.ChatRequest, labels []string, out chan<- Event) {

	defer close(out)

	for attempt := 0; ; attempt++ {
		body, res := e.streamOnce(ctx, sessionID, req, labels, out)

		switch {
		case res.err != nil:
			// 再試行の上限超過 → 障害停止 → 中断。
			e.saveInterruptedUtterance(sessionID, body)
			_ = e.setState(sessionID, state, StateFailed)
			_ = e.setState(sessionID, state, StateSuspended)
			out <- Event{Kind: EventError, State: state.DialogueState,
				ErrorClass: res.err.Class.String(), ErrorCode: res.err.Code, Message: res.err.Message}
			return
		case res.interrupted:
			// 中断: 受信済み本文を中断フラグ付きで保存する。
			id := e.saveInterruptedUtterance(sessionID, body)
			_ = e.setState(sessionID, state, StateSuspended)
			out <- Event{Kind: EventDone, UtteranceID: id, State: state.DialogueState, Interrupted: true}
			return
		}

		parsed := ParseQuestion(body)
		reconfirm := false
		if parsed.TopicKey != "" && IsDecided(records, parsed.TopicKey) {
			if attempt == 0 {
				// 既決論点への質問は 1 回だけ作り直しを要求する。
				out <- Event{Kind: EventReplaced}
				req.Messages = append(req.Messages,
					aiprovider.Message{Role: aiprovider.RoleAssistant, Content: body},
					aiprovider.Message{Role: aiprovider.RoleUser, Content: fmt.Sprintf(
						"論点 %s は決定済みです。まだ決まっていない論点について質問を作り直してください。", parsed.TopicKey)})
				continue
			}
			// 作り直してもなお既決 → 変更を目的とした再確認として明示表示する。
			reconfirm = true
		}
		if parsed.TopicKey == "" {
			parsed.TopicKey = topic.TopicKey
		}

		id, err := e.cfg.Store.AppendUtterance(sessionID, projectstore.Utterance{
			Speaker: projectstore.SpeakerAgent,
			Status:  projectstore.UtteranceCompleted,
			Body:    QuestionUtteranceBody(parsed, reconfirm),
		})
		if err != nil {
			out <- Event{Kind: EventError, ErrorClass: aiprovider.ErrClassPermanent.String(), Message: err.Error()}
			return
		}
		state.PresentedQuestion = &PresentedQuestion{
			TopicKey: parsed.TopicKey, Text: parsed.Question,
			FollowUpIndex: topic.FollowUpIndex, UtteranceID: id, Reconfirm: reconfirm,
		}
		if err := e.setState(sessionID, state, StateAwaitingAnswer); err != nil {
			out <- Event{Kind: EventError, ErrorClass: aiprovider.ErrClassPermanent.String(), Message: err.Error()}
			return
		}
		out <- Event{Kind: EventDone, UtteranceID: id, State: state.DialogueState,
			TopicKey: parsed.TopicKey, Reconfirm: reconfirm}
		return
	}
}

// streamResult は 1 回のストリーミングの結果。
type streamResult struct {
	interrupted bool
	err         *aiprovider.ProviderError
}

// cause は縮退処理へ渡す原因を error として返す。
//
// err は *aiprovider.ProviderError のため、そのまま error 引数へ渡すと値が nil でも
// インタフェースは非 nil（型付き nil）になり、受け側の nil 判定をすり抜ける。
// エラーイベントが届かないまま本文が空で終わる経路（context 期限切れ等）のために必要。
func (r streamResult) cause() error {
	if r.err == nil {
		return nil
	}
	return r.err
}

// streamOnce は 1 回ストリーミングし、本文を組み立てながらイベントを中継する。
func (e *Engine) streamOnce(ctx context.Context, sessionID string, req aiprovider.ChatRequest,
	labels []string, out chan<- Event) (string, streamResult) {

	var body strings.Builder
	ch, err := aiprovider.StreamRetrying(ctx, e.cfg.Adapter, req, aiprovider.StreamOptions{
		Timeouts:  e.cfg.Timeouts,
		Recorder:  e.cfg.Recorder,
		OnPanic:   e.cfg.OnPanic,
		OnFailure: e.cfg.OnCallFailed,
		Context:   aiprovider.RecordContext{Session: sessionID, Included: labels},
	})
	if err != nil {
		return "", streamResult{err: &aiprovider.ProviderError{
			Class: aiprovider.ErrClassConfig, Provider: e.cfg.Adapter.ID(), Message: err.Error()}}
	}
	var res streamResult
	for ev := range ch {
		switch ev.Kind {
		case aiprovider.EventTextDelta:
			body.WriteString(ev.Text)
			out <- Event{Kind: EventText, Text: ev.Text}
		case aiprovider.EventDone:
			res.interrupted = ev.Interrupted
		case aiprovider.EventError:
			res.err = ev.Err
		}
	}
	return body.String(), res
}

// saveInterruptedUtterance は受信済み本文を中断発話として保存する。
// 本文が空の場合は保存しない（中身の無い発話を残さない）。
func (e *Engine) saveInterruptedUtterance(sessionID, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	id, err := e.cfg.Store.AppendUtterance(sessionID, projectstore.Utterance{
		Speaker: projectstore.SpeakerAgent,
		Status:  projectstore.UtteranceInterrupted,
		Body:    body,
	})
	if err != nil {
		return ""
	}
	return id
}

// SubmitAnswer は担当者の回答を保存して抽出へ進める（回答待ち → 抽出中）。
//
// 発話は AI 送信前に保存する（AI の障害で回答を失わないため）。抽出そのものは Extract が行う。
func (e *Engine) SubmitAnswer(sessionID, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("送信する内容がありません")
	}
	state, err := e.LoadState(sessionID)
	if err != nil {
		return "", err
	}
	if state.DialogueState == StateSuspended {
		// 中断からの再開（中断 → 質問生成中 → 回答待ち）。
		if err := e.setState(sessionID, state, StateQuestioning); err != nil {
			return "", err
		}
		if err := e.setState(sessionID, state, StateAwaitingAnswer); err != nil {
			return "", err
		}
	}
	id, err := e.cfg.Store.AppendUtterance(sessionID, projectstore.Utterance{
		Speaker: projectstore.SpeakerUser,
		Status:  projectstore.UtteranceCompleted,
		Body:    text,
	})
	if err != nil {
		return "", err
	}
	if state.PresentedQuestion != nil {
		state.PresentedQuestion.Answered = true
	}
	if err := e.setState(sessionID, state, StateExtracting); err != nil {
		return "", err
	}
	return id, nil
}

// Suspend は明示的な中断（画面を離れる・アプリ終了）を記録する（→ 中断）。
func (e *Engine) Suspend(sessionID string) error {
	state, err := e.LoadState(sessionID)
	if err != nil {
		return err
	}
	if state.DialogueState == StateSuspended {
		return nil
	}
	if !CanTransition(state.DialogueState, StateSuspended) {
		// 反映中など中断を許さない状態では現状のまま保存する（保全を優先し、状態を壊さない）。
		return e.saveMeta(sessionID, state)
	}
	return e.setState(sessionID, state, StateSuspended)
}

// ResumeContext は再開時に提示する文脈（再開画面に出す）。
type ResumeContext struct {
	State             string             `json:"state"`
	PresentedQuestion *PresentedQuestion `json:"presentedQuestion,omitempty"`
	// RecentSummary は直近の対話要約（要約ブロックの最後）。
	RecentSummary string `json:"recentSummary,omitempty"`
	// OpenIssues は残っている未決事項（再開時に提示する）。
	OpenIssues []projectstore.OpenIssue `json:"openIssues,omitempty"`
	// HasPendingCandidates は未承認の抽出候補が保全されているか。
	HasPendingCandidates bool `json:"hasPendingCandidates"`
}

// Resume は中断時点の文脈を復元して返す（抽出済み内容を失わない）。
func (e *Engine) Resume(sessionID string) (*ResumeContext, error) {
	state, err := e.LoadState(sessionID)
	if err != nil {
		return nil, err
	}
	records, err := e.Records()
	if err != nil {
		return nil, err
	}
	out := &ResumeContext{
		State:                state.DialogueState,
		PresentedQuestion:    state.PresentedQuestion,
		HasPendingCandidates: state.PendingCandidates != nil,
	}
	for _, i := range records.OpenIssues {
		if i.Status == projectstore.OpenIssueOpen {
			out.OpenIssues = append(out.OpenIssues, i)
		}
	}
	if n := len(state.Summaries); n > 0 {
		out.RecentSummary = state.Summaries[n-1].Body
	}
	return out, nil
}

// 抽出のイベント種別。
const (
	// EventCandidates は抽出候補の提示（承認待ちへ遷移した）。
	EventCandidates = "candidates"
	// EventFallback はスキーマ検証に失敗し、手動起票へ縮退したこと。
	EventFallback = "fallback"
)

// schemaRetryLimit はスキーマ検証失敗時の再要求回数（調整してよい範囲は 1〜2 回）。
const schemaRetryLimit = 1

// Extract は直近の回答を分析して構造化候補を提示する（抽出中 → 承認待ち）。
//
// 返すチャネルは EventCandidates / EventFallback / EventError / EventDone(中断) のいずれかで閉じる。
func (e *Engine) Extract(ctx context.Context, sessionID string) (<-chan Event, error) {
	sess, utterances, err := e.cfg.Store.LoadSession(sessionID)
	if err != nil {
		return nil, err
	}
	state, err := e.LoadState(sessionID)
	if err != nil {
		return nil, err
	}
	if state.DialogueState != StateExtracting {
		return nil, fmt.Errorf("抽出できる状態ではありません（現在: %s）", state.DialogueState)
	}
	records, err := e.Records()
	if err != nil {
		return nil, err
	}
	completeness, err := Completeness(sess.Phase, records)
	if err != nil {
		return nil, err
	}
	req, labels, err := e.buildExtractionRequest(sess, records, completeness, state, utterances)
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 8)
	go e.runExtraction(ctx, sessionID, state, sess, utterances, req, labels, out)
	return out, nil
}

// buildExtractionRequest は抽出のリクエストを組み立てる（直近の質問と回答を対象にする）。
func (e *Engine) buildExtractionRequest(sess *projectstore.Session, records Records,
	completeness []ChapterCompleteness, state *SessionState,
	utterances []projectstore.Utterance) (aiprovider.ChatRequest, []string, error) {

	system, err := BuildSystemPrompt(SystemPromptInput{
		Phase: sess.Phase, Effort: e.cfg.Effort, Mode: ModeExtraction})
	if err != nil {
		return aiprovider.ChatRequest{}, nil, err
	}
	effort := e.effortParams()

	chapter := ""
	topicKey := ""
	if state.PresentedQuestion != nil {
		topicKey = state.PresentedQuestion.TopicKey
		chapter, _, _ = strings.Cut(topicKey, "/")
	}
	history, _ := CompressHistory(utterances, state.Summaries, HistoryBudget{
		ContextWindow:   e.cfg.Model.ContextWindow,
		FixedTokens:     aiprovider.EstimateTokens(system),
		MaxOutputTokens: effort.MaxOutputTokens,
	})
	contextText, labels := BuildContext(ContextInput{
		Phase: sess.Phase, Records: records, Completeness: completeness,
		CurrentChapter: chapter, CurrentTopicKey: topicKey, History: history,
	})

	target := lastExchangeText(sess.ID, utterances)
	instruction := "直前のやり取りから、決定事項・未決事項・要件項目への反映案・用語候補・矛盾を抽出してください。\n" +
		"各発話の先頭の角括弧内にある ID（S-nnnn#utt-nnnnn）が、evidence_refs に入れる発話 ID です。\n\n" +
		"### 直前のやり取り\n" + target
	if topicKey != "" {
		instruction += "\n\n（この論点は " + topicKey + " です。決定事項候補の topic_key に用いてください。）"
	}

	return aiprovider.ChatRequest{
		Model:    e.cfg.Model.ID,
		System:   system,
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: contextText + "\n\n" + instruction}},
		Effort:   effort,
		// 構造化出力。スキーマはプロンプトではなくここで渡す。
		ResponseSchema: SchemaForMode(ModeExtraction),
	}, labels, nil
}

// lastExchangeText は直近 1 往復の本文を、発話ごとの参照 ID つきで返す
// （中断発話は含めない）。
//
// 抽出指示は evidence_refs に発話 ID を求める。**本文に ID が無いと AI は ID を知り得ず**、
// 推測した ID は VerifyEvidence で除去されて根拠が必ず空になる。
func lastExchangeText(sessionID string, utterances []projectstore.Utterance) string {
	exchanges := SplitExchanges(utterances)
	if len(exchanges) == 0 {
		return ""
	}
	var b strings.Builder
	for _, u := range exchanges[len(exchanges)-1].Utterances {
		fmt.Fprintf(&b, "[%s %s] %s\n", speakerLabel(u.Speaker),
			projectstore.UtteranceRef(sessionID, u.ID), strings.TrimSpace(u.Body))
	}
	return strings.TrimRight(b.String(), "\n")
}

// lastExchangeRefs は抽出対象（直近 1 往復）のうち担当者の回答の発話参照を返す。
//
// 候補の根拠を AI が書き損じたときに補う参照（FillMissingEvidence）。
// 決定・未決の根拠は「回答」であり、質問だけを根拠にしない。回答が無ければ往復全体を使う。
func lastExchangeRefs(sessionID string, utterances []projectstore.Utterance) []string {
	exchanges := SplitExchanges(utterances)
	if len(exchanges) == 0 {
		return nil
	}
	var answers, all []string
	for _, u := range exchanges[len(exchanges)-1].Utterances {
		ref := projectstore.UtteranceRef(sessionID, u.ID)
		all = append(all, ref)
		if u.Speaker != projectstore.SpeakerAgent {
			answers = append(answers, ref)
		}
	}
	if len(answers) > 0 {
		return answers
	}
	return all
}

// runExtraction は抽出を実行し、候補の提示または縮退を通知する。
func (e *Engine) runExtraction(ctx context.Context, sessionID string, state *SessionState,
	sess *projectstore.Session, utterances []projectstore.Utterance,
	req aiprovider.ChatRequest, labels []string, out chan<- Event) {

	defer close(out)

	var lastBody string
	for attempt := 0; ; attempt++ {
		body, res := e.streamSilently(ctx, sessionID, req, labels)
		lastBody = body

		switch {
		case res.err != nil:
			_ = e.setState(sessionID, state, StateFailed)
			_ = e.setState(sessionID, state, StateSuspended)
			out <- Event{Kind: EventError, State: state.DialogueState,
				ErrorClass: res.err.Class.String(), ErrorCode: res.err.Code, Message: res.err.Message}
			return
		case res.interrupted:
			// 抽出の中断では候補が生成されない。発話も残さない。
			_ = e.setState(sessionID, state, StateSuspended)
			out <- Event{Kind: EventDone, State: state.DialogueState, Interrupted: true}
			return
		}

		extraction, err := ParseExtraction(body)
		if err != nil {
			if attempt < schemaRetryLimit {
				req.Messages = append(req.Messages,
					aiprovider.Message{Role: aiprovider.RoleAssistant, Content: body},
					aiprovider.Message{Role: aiprovider.RoleUser,
						Content: "出力が指定の JSON スキーマに合っていません（" + err.Error() +
							"）。説明文を付けず、スキーマどおりの JSON だけを出力してください。"})
				continue
			}
			// 再要求してもなお失敗 → 応答原文を提示して手動起票へ縮退する（AI が使えなくても記録を続けられるように）。
			_ = e.setState(sessionID, state, StateAwaitingApproval)
			out <- Event{Kind: EventFallback, State: state.DialogueState,
				Text: lastBody, Message: err.Error()}
			return
		}

		extraction.VerifyEvidence(utteranceRefs(sess.ID, utterances))
		// 根拠を書き損じた候補には抽出対象の回答を根拠として付ける（根拠の無い候補を承認画面に出さないため）。
		extraction.FillMissingEvidence(lastExchangeRefs(sess.ID, utterances))
		// 新規の要件項目の ID グループ・種別をアプリが確定する（利用者に入力させない）。
		extraction.ResolveRequirementIDs(e.requirementGroupsByChapter())
		// 観点候補は取り込み分析だけの出力。対話抽出では
		// スキーマにも含めていないが、混入しても承認画面へは流さない。
		extraction.DropPerspectiveCandidates()
		state.PendingCandidates = extraction
		// 候補が書き換える共有レコードの基準版を採る（承認・反映の直前に、他の変更と競合していないかを確かめるため）。
		baselines, err := e.captureBaselines(extraction)
		if err != nil {
			out <- Event{Kind: EventError, ErrorClass: aiprovider.ErrClassPermanent.String(), Message: err.Error()}
			return
		}
		state.Baselines = baselines
		if err := e.setState(sessionID, state, StateAwaitingApproval); err != nil {
			out <- Event{Kind: EventError, ErrorClass: aiprovider.ErrClassPermanent.String(), Message: err.Error()}
			return
		}
		out <- Event{Kind: EventCandidates, State: state.DialogueState, Extraction: extraction}
		return
	}
}

// streamSilently はストリーミングを本文へ組み立てるだけで中継しない（抽出の JSON は画面へ流さない）。
func (e *Engine) streamSilently(ctx context.Context, sessionID string,
	req aiprovider.ChatRequest, labels []string) (string, streamResult) {

	var body strings.Builder
	ch, err := aiprovider.StreamRetrying(ctx, e.cfg.Adapter, req, aiprovider.StreamOptions{
		Timeouts:  e.cfg.Timeouts,
		Recorder:  e.cfg.Recorder,
		OnPanic:   e.cfg.OnPanic,
		OnFailure: e.cfg.OnCallFailed,
		Context:   aiprovider.RecordContext{Session: sessionID, Included: labels},
	})
	if err != nil {
		return "", streamResult{err: &aiprovider.ProviderError{
			Class: aiprovider.ErrClassConfig, Provider: e.cfg.Adapter.ID(), Message: err.Error()}}
	}
	var res streamResult
	for ev := range ch {
		switch ev.Kind {
		case aiprovider.EventTextDelta:
			body.WriteString(ev.Text)
		case aiprovider.EventDone:
			res.interrupted = ev.Interrupted
		case aiprovider.EventError:
			res.err = ev.Err
		}
	}
	return body.String(), res
}

// requirementGroupsByChapter は既存の要件項目から「章観点 → 使っているグループ」を返す。
//
// 読み出せないときは空を返す（章観点の既定へ倒る。候補の提示を止めない）。
func (e *Engine) requirementGroupsByChapter() map[string]string {
	reqs, err := e.cfg.Store.ListRequirements()
	if err != nil {
		return nil
	}
	return RequirementGroupsByChapter(reqs)
}

// utteranceRefs は当該セッションの発話参照（S-nnnn#utt-nnnnn）の集合を返す。
//
// 根拠は当該対話の発話に限る（抽出は直近の回答に対する分析であるため）。
// 中断発話は抽出の入力に含めないため、根拠としても認めない。
func utteranceRefs(sessionID string, utterances []projectstore.Utterance) map[string]bool {
	out := map[string]bool{}
	for _, u := range utterances {
		if u.IsInterrupted() {
			continue
		}
		out[projectstore.UtteranceRef(sessionID, u.ID)] = true
	}
	return out
}

// PendingExtraction は保全されている未承認候補を返す（中断からの復元）。
func (e *Engine) PendingExtraction(sessionID string) (*Extraction, error) {
	state, err := e.LoadState(sessionID)
	if err != nil {
		return nil, err
	}
	if state.PendingCandidates == nil {
		return nil, nil
	}
	ex, ok := state.PendingCandidates.(*Extraction)
	if !ok {
		// 併置メタデータから読み直した場合は YAML の汎用構造で入っている。
		if ex, err = decodeExtraction(state.PendingCandidates); err != nil {
			return nil, err
		}
	}
	if err := e.fillSavedEvidence(sessionID, state, ex); err != nil {
		return nil, err
	}
	// 旧版で保存された候補は ID グループ・種別を持たない。表示と承認が同じ ID を指すよう、
	// 読み直すときにも確定する（確定済みの値は変えない）。
	ex.ResolveRequirementIDs(e.requirementGroupsByChapter())
	return ex, nil
}

// fillSavedEvidence は、抽出の入力に発話 ID を載せていなかった旧版で保存された「根拠が空の未承認候補」へ、
// 抽出対象の回答の発話参照を補う（利用者のプロジェクトに実際に残っている）。
//
// 補ってよいのは、候補が**いまの最後の往復から抽出されたと確定できる**ときだけ:
// 承認待ち・反映中（新しい質問を出せない状態）で、最後の往復が担当者の回答で終わっている。
// それ以外（中断を挟んで次の質問へ進んだ等）は、別の往復を根拠にしてしまうため補わない。
func (e *Engine) fillSavedEvidence(sessionID string, state *SessionState, ex *Extraction) error {
	if state.DialogueState != StateAwaitingApproval && state.DialogueState != StateApplying {
		return nil
	}
	_, utterances, err := e.cfg.Store.LoadSession(sessionID)
	if err != nil {
		return err
	}
	exchanges := SplitExchanges(utterances)
	if len(exchanges) == 0 {
		return nil
	}
	last := exchanges[len(exchanges)-1].Utterances
	if len(last) == 0 || last[len(last)-1].Speaker == projectstore.SpeakerAgent {
		return nil
	}
	ex.FillMissingEvidence(lastExchangeRefs(sessionID, utterances))
	return nil
}
