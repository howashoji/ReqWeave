package binding

// 本ファイルは対話画面向けの公開バインディング。
//
// フロントエンドはここを通してのみ対話エンジン・プロジェクトストアへ到達する。
// AI 呼び出しを伴う操作は beginAICall を前段に置く（AI の疎通確認と
// トークン上限判定の共通前段。usage_guard.go が正本。個々の機能へ判定を分散させない）。

import (
	"context"
	"errors"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// EventDialogue は対話イベントの Wails イベント名。
//
// 1 つのイベント名で dialogue.Event を送り、画面側が kind で分岐する。
const EventDialogue = "dialogue:event"

// dialogueSession は開いているプロジェクトと対話エンジン。
type dialogueSession struct {
	projectPath string
	store       *projectstore.Store
	engine      *dialogue.Engine
	// cancel は進行中のストリーミングを中断する。
	cancel context.CancelFunc
	// pendingImport は検証済みで未反映の返送ファイル（検証の後、取込を始めるまで保持する）。
	pendingImport *exchange.ImportReview
}

// DialogueOpenResult はプロジェクトを対話用に開いた結果。
type DialogueOpenResult struct {
	ProjectPath string `json:"projectPath"`
	// ProjectID は同期先の認証情報など、プロジェクト単位の設定の指定に使う。
	ProjectID  string `json:"projectId"`
	Phase      string `json:"phase"`
	TargetName string `json:"targetName"`
	// Sessions は既存の対話セッション（再開の候補）。
	Sessions []DialogueSessionView `json:"sessions"`
}

// DialogueSessionView は対話セッションの一覧表示。
type DialogueSessionView struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Phase     string `json:"phase"`
	StartedAt string `json:"startedAt"`
	Author    string `json:"author,omitempty"`
}

// UtteranceView は発話の表示。
type UtteranceView struct {
	ID      string `json:"id"`
	Speaker string `json:"speaker"`
	At      string `json:"at"`
	Status  string `json:"status"`
	Body    string `json:"body"`
}

// OpenDialogueProject は対話用にプロジェクトを開く（プロジェクト一覧 → 対話画面）。
//
// AI キーの未設定・疎通未確認のときは開かずに理由を返す。
func (a *API) OpenDialogueProject(path string) (DialogueOpenResult, error) {
	if err := a.requireAIReady(); err != nil {
		return DialogueOpenResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		a.closeSessionLocked()
	}
	store, err := a.openProject(path)
	if err != nil {
		return DialogueOpenResult{}, err
	}
	engine, err := a.newEngine(store)
	if err != nil {
		_ = store.Close()
		return DialogueOpenResult{}, err
	}
	a.session = &dialogueSession{projectPath: path, store: store, engine: engine}
	// 開いた事実と、端末と利用者 ID の紐づけ（初回のみ）を記録する。
	a.recordProjectOpened(a.session)
	// 一覧（`recent_projects`）へ載せる。
	// **一覧を経由しない入口**（パッケージのダブルクリック）から開いたときも
	// 「最近開いたもの」に入るようにする。一覧から開いた場合は先頭へ移るだけ。
	a.rememberOpenedProject(path)

	sessions, err := store.ListSessions()
	if err != nil {
		return DialogueOpenResult{}, err
	}
	project := store.Project()
	out := DialogueOpenResult{ProjectPath: path, ProjectID: project.ProjectID,
		Phase: project.Phase, TargetName: project.TargetSystemName}
	for _, s := range sessions {
		out.Sessions = append(out.Sessions, toSessionView(s))
	}
	return out, nil
}

// CloseDialogueProject は開いているプロジェクトを閉じる（進行中のストリーミングは中断する）。
func (a *API) CloseDialogueProject() error {
	// **背景処理の完了を待ってから閉じる**。
	// 先に中断を伝え、排他を手放した状態で待つ（背景側が API を呼んでも詰まらないため）。
	// 待たずに閉じると、生成中のロック解放やイベント送出が閉じた後に走る。
	a.mu.Lock()
	if a.session != nil && a.session.cancel != nil {
		a.session.cancel()
	}
	a.mu.Unlock()
	a.background.Wait()

	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeSessionLocked()
	return nil
}

func (a *API) closeSessionLocked() {
	if a.session == nil {
		return
	}
	if a.session.cancel != nil {
		a.session.cancel()
	}
	// 閉じた事実を記録する（変更要約の基準点になる）。ストアを閉じる前に行う。
	a.recordProjectClosed(a.session)
	// Close は保存キューを流し切る唯一の経路。ここで失敗すると**書き込みが失われる**ため、
	// 黙って捨てずに動作ログへ「保存失敗」として残す。
	if err := a.session.store.Close(); err != nil {
		a.recordProjectDataFailure("", dataKindSaveFailed, "")
	}
	a.session = nil
}

// newEngine は現在の設定（プロバイダ・モデル・エフォート）で対話エンジンを組み立てる。
func (a *API) newEngine(store *projectstore.Store) (*dialogue.Engine, error) {
	settings, err := a.settings()
	if err != nil {
		return nil, err
	}
	provider, ok := settings.DefaultProviderSetting()
	if !ok {
		return nil, errors.New("AI プロバイダが設定されていません。設定画面で登録してください。")
	}
	adapter, model, err := a.adapterFor(provider)
	if err != nil {
		return nil, err
	}

	logger, err := auditlog.New(store, store.Author().AuthorID)
	if err != nil {
		return nil, err
	}
	onRecordError := func(err error) {
		// 監査記録の失敗は対話を止めない。画面へは通知イベントとして知らせ、
		// 動作ログへも残す（事後の調査で「記録が欠けた瞬間」を追えるようにする）。
		a.log.Error("audit.record_failed", "監査記録の保存に失敗しました",
			applog.F("source", "dialogue"), applog.F("error", err))
		a.emitDialogue(dialogue.Event{Kind: dialogue.EventError,
			ErrorClass: aiprovider.ErrClassPermanent.String(),
			Message:    "記録の保存に失敗しました: " + err.Error()})
	}
	return dialogue.New(dialogue.Config{
		Store:         store,
		Adapter:       adapter,
		Model:         model,
		Effort:        aiEffort(provider.Effort),
		Timeouts:      a.adapterOptions().Timeouts,
		Recorder:      auditlog.NewSendRecorder(logger, onRecordError),
		Logger:        logger,
		OnRecordError: onRecordError,
		OnDegrade:     a.onEffortDegrade,
		OnPanic:       a.onStreamPanic,
		OnCallFailed:  a.onAICallFailed,
	})
}

// adapterFor は設定からアダプタと選択中モデルの情報を組み立てる（対話・生成で共用する）。
//
// **サインイン方式の設定はキーへの参照名を持たない**（認証情報は AI プロバイダ側が保管する）。その場合は参照名を空のままアダプタへ渡す。
func (a *API) adapterFor(provider projectstore.ProviderSetting) (aiprovider.Adapter, aiprovider.ModelInfo, error) {
	keyRefValue := ""
	if !usesSignIn(provider.AuthMethodOrDefault()) {
		ref, err := keymanager.ParseRef(provider.KeyRef)
		if err != nil {
			return nil, aiprovider.ModelInfo{}, err
		}
		keyRefValue = ref.String()
	}
	adapter, err := a.newAdapter(aiprovider.ProviderID(provider.Provider), keyProvider{a.keys},
		aiprovider.KeyRef(keyRefValue), a.adapterOptionsFor(provider.AuthMethodOrDefault()))
	if err != nil {
		return nil, aiprovider.ModelInfo{}, err
	}
	model, err := knownModel(aiprovider.ProviderID(provider.Provider), provider.Model)
	if err != nil {
		return nil, aiprovider.ModelInfo{}, err
	}
	return adapter, model, nil
}

// aiEffort は設定値をエフォート段階へ写す（未設定は既定の標準）。
func aiEffort(effort string) aiprovider.Effort {
	switch aiprovider.Effort(effort) {
	case aiprovider.EffortLow:
		return aiprovider.EffortLow
	case aiprovider.EffortHigh:
		return aiprovider.EffortHigh
	default:
		return aiprovider.EffortStandard
	}
}

// requireAIReady は AI 呼び出しを伴う操作の共通前段（AI の疎通確認。権限判定と同じ位置に置く）。
func (a *API) requireAIReady() error {
	settings, err := a.settings()
	if err != nil {
		return err
	}
	if ready, reason := a.aiReadiness(settings); !ready {
		return errors.New(reason)
	}
	return nil
}

// current は開いている対話セッションを返す。
func (a *API) current() (*dialogueSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return nil, errors.New("プロジェクトが開かれていません。プロジェクト一覧から開いてください。")
	}
	return a.session, nil
}

// StartDialogueSession は新しい対話セッションを開始する。
func (a *API) StartDialogueSession() (DialogueSessionView, error) {
	s, err := a.current()
	if err != nil {
		return DialogueSessionView{}, err
	}
	sess, err := s.engine.StartSession(s.store.Project().Phase)
	if err != nil {
		return DialogueSessionView{}, err
	}
	return toSessionView(*sess), nil
}

// ResumeDialogueSession は中断したセッションの文脈を復元して返す。
func (a *API) ResumeDialogueSession(sessionID string) (*dialogue.ResumeContext, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	return s.engine.Resume(sessionID)
}

// AskNextQuestion は次の質問を生成し、イベントで逐次通知する（最初の文字を早く見せるため）。
//
// 戻り値は受理の可否のみで、本文はイベントで届く（非同期に受け付けて画面を止めない）。
// 次の論点が無い（全章観点が充足）場合は false を返す。
func (a *API) AskNextQuestion(sessionID string) (bool, error) {
	s, err := a.current()
	if err != nil {
		return false, err
	}
	// AI 実行口の共通前段（疎通確認 + トークン上限判定）。
	if _, err := a.beginAICall(s, "次の質問の生成"); err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancel(a.context())
	a.setCancel(cancel)

	ch, err := s.engine.GenerateQuestion(ctx, sessionID)
	if err != nil {
		cancel()
		return false, err
	}
	if ch == nil {
		cancel()
		return false, nil // 充足済み（次の質問は無い）
	}
	go a.pump(ch, cancel)
	return true, nil
}

// SendAnswer は回答を保存し、抽出を開始する（対話の状態は抽出中へ移る）。
//
// 発話の保存は同期で行い（保存前に AI を呼ばない）、抽出の結果はイベントで届く。
func (a *API) SendAnswer(sessionID, text string) (string, error) {
	s, err := a.current()
	if err != nil {
		return "", err
	}
	utteranceID, err := s.engine.SubmitAnswer(sessionID, text)
	if err != nil {
		return "", err
	}
	// 発話の保存後に前段を通す（上限到達でも回答は保存済み = AI なしの手動編集で続けられる）。
	if _, err := a.beginAICall(s, "回答からの抽出"); err != nil {
		return utteranceID, err
	}
	ctx, cancel := context.WithCancel(a.context())
	a.setCancel(cancel)
	ch, err := s.engine.Extract(ctx, sessionID)
	if err != nil {
		cancel()
		return utteranceID, err
	}
	go a.pump(ch, cancel)
	return utteranceID, nil
}

// InterruptDialogue は進行中のストリーミングを中断する。
func (a *API) InterruptDialogue() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil || a.session.cancel == nil {
		return nil
	}
	a.session.cancel()
	a.session.cancel = nil
	return nil
}

// SuspendDialogue は対話を中断状態として保存する（画面を離れる・アプリ終了時）。
func (a *API) SuspendDialogue(sessionID string) error {
	s, err := a.current()
	if err != nil {
		return err
	}
	return s.engine.Suspend(sessionID)
}

// ApproveDialogueCandidates は抽出候補の承認・編集して承認・破棄を反映する。
func (a *API) ApproveDialogueCandidates(sessionID string, req dialogue.ApprovalRequest) (ApprovalOutcome, error) {
	s, err := a.current()
	if err != nil {
		return ApprovalOutcome{}, err
	}
	result, err := s.engine.ApproveCandidates(sessionID, req)
	if err != nil {
		// 競合はエラーではなく結果として返す（画面が三面を出して本人が承認する）。
		if outcome, ok := conflictOutcome(err); ok {
			return outcome, nil
		}
		return ApprovalOutcome{}, err
	}
	return ApprovalOutcome{Applied: appliedOf(result, nil)}, nil
}

// PendingCandidates は保全されている未承認候補を返す（再開時の復元）。
func (a *API) PendingCandidates(sessionID string) (*dialogue.Extraction, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	return s.engine.PendingExtraction(sessionID)
}

// DialogueCompletenessView は完成度表示と確定可否。
type DialogueCompletenessView struct {
	Chapters     []dialogue.ChapterCompleteness `json:"chapters"`
	Confirmation dialogue.Confirmation          `json:"confirmation"`
}

// DialogueCompleteness は章観点ごとの充足率と確定可否を返す（UI 側で再計算しない。判定を 1 か所に置くため）。
func (a *API) DialogueCompleteness() (DialogueCompletenessView, error) {
	s, err := a.current()
	if err != nil {
		return DialogueCompletenessView{}, err
	}
	records, err := s.engine.Records()
	if err != nil {
		return DialogueCompletenessView{}, err
	}
	chapters, err := dialogue.Completeness(s.store.Project().Phase, records)
	if err != nil {
		return DialogueCompletenessView{}, err
	}
	return DialogueCompletenessView{Chapters: chapters, Confirmation: dialogue.Confirmable(records)}, nil
}

// DialogueSessions は対話セッション一覧を返す（読み出しのみ。対話履歴は監査記録のため書き換えない）。
func (a *API) DialogueSessions() ([]DialogueSessionView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.ListSessions()
	if err != nil {
		return nil, err
	}
	out := make([]DialogueSessionView, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, toSessionView(sess))
	}
	return out, nil
}

// DialogueUtterances は発話履歴を返す（読み出しのみ。対話履歴は監査記録のため書き換え API は設けない）。
func (a *API) DialogueUtterances(sessionID string) ([]UtteranceView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	_, utterances, err := s.store.LoadSession(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]UtteranceView, 0, len(utterances))
	for _, u := range utterances {
		out = append(out, UtteranceView{ID: u.ID, Speaker: u.Speaker,
			At: u.At.UTC().Format(time.RFC3339), Status: u.Status, Body: u.Body})
	}
	return out, nil
}

// UtteranceHitView は対話履歴の全文検索の該当発話 1 件（結果一覧の 1 行）。
type UtteranceHitView struct {
	SessionID string `json:"sessionId"`
	Phase     string `json:"phase"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	Speaker   string `json:"speaker"`
	At        string `json:"at"`
	Status    string `json:"status"`
	// Excerpt は該当箇所を含む抜粋（前後の文脈つき）。
	Excerpt string `json:"excerpt"`
}

// UtteranceSearchView は対話履歴の全文検索の結果。
type UtteranceSearchView struct {
	Hits []UtteranceHitView `json:"hits"`
	// Total は該当した発話の総数（Limit で切る前）。0 件も正常な結果として返す。
	Total int `json:"total"`
	// Truncated は一覧が Limit で切られたことを表す（切ったことを画面で必ず示す）。
	Truncated bool `json:"truncated"`
	Limit     int  `json:"limit"`
}

// SearchDialogueUtterances は全対話セッションの発話本文を語句で検索する。
//
// 読み出しのみで AI 呼び出しを伴わない（オフライン・API 障害中も使える）。
// phase / kind が "all" または空のときは、その軸で絞り込まない（履歴一覧の絞り込みと併用する）。
func (a *API) SearchDialogueUtterances(text, phase, kind string) (UtteranceSearchView, error) {
	s, err := a.current()
	if err != nil {
		return UtteranceSearchView{}, err
	}
	result, err := s.store.SearchUtterances(projectstore.UtteranceSearchQuery{
		Text:  text,
		Phase: filterValue(phase),
		Type:  filterValue(kind),
	})
	if err != nil {
		return UtteranceSearchView{}, err
	}
	hits := make([]UtteranceHitView, 0, len(result.Hits))
	for _, h := range result.Hits {
		hits = append(hits, UtteranceHitView{
			SessionID: h.SessionID, Phase: h.Phase, Type: h.Type, ID: h.UtteranceID,
			Speaker: h.Speaker, At: h.At.UTC().Format(time.RFC3339), Status: h.Status,
			Excerpt: h.Excerpt,
		})
	}
	return UtteranceSearchView{Hits: hits, Total: result.Total, Truncated: result.Truncated,
		Limit: projectstore.DefaultUtteranceSearchLimit}, nil
}

// filterValue は画面の「すべて」（all）を「絞り込まない」（空文字）へ直す。
func filterValue(v string) string {
	if v == "all" {
		return ""
	}
	return v
}

// setCancel は進行中の中断関数を差し替える（前の呼び出しが残っていれば中断する）。
func (a *API) setCancel(cancel context.CancelFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		cancel()
		return
	}
	if a.session.cancel != nil {
		a.session.cancel()
	}
	a.session.cancel = cancel
}

// pump は対話イベントを Wails イベントとして逐次発行する（本層で集約しない。抽象化層のストリーミングと同じ方針）。
func (a *API) pump(ch <-chan dialogue.Event, cancel context.CancelFunc) {
	defer cancel()
	for ev := range ch {
		a.emitDialogue(ev)
	}
}

// emitDialogue は 1 イベントを画面へ送る。
//
// エラーには利用者向けの「原因＋次の行動」の 1 文を添える（エラーカタログの正本は本層。
// **未知の Code は分類ごとの既定へ倒す**ため、画面に生のコード値は出ない）。
func (a *API) emitDialogue(ev dialogue.Event) {
	if a.ctx == nil {
		return // Wails 未起動（テスト実行時）は何もしない
	}
	if ev.Kind == dialogue.EventError && ev.UserMessage == "" {
		ev.UserMessage = a.aiUserMessage(ev.ErrorClass, ev.ErrorCode)
	}
	wailsruntime.EventsEmit(a.ctx, EventDialogue, ev)
}

func toSessionView(s projectstore.Session) DialogueSessionView {
	return DialogueSessionView{ID: s.ID, Type: s.Type, Phase: s.Phase,
		StartedAt: s.StartedAt.UTC().Format(time.RFC3339), Author: s.Author}
}

// knownModel は既知一覧から選択中モデルの情報を引く（コンテキスト長・最大出力の判定に使う）。
//
// 既知一覧に無いモデル（利用者が API 取得の一覧から選んだ新しいモデル）は
// コンテキスト長不明として扱い、履歴の予算制限を掛けない。
func knownModel(provider aiprovider.ProviderID, modelID string) (aiprovider.ModelInfo, error) {
	if modelID == "" {
		return aiprovider.ModelInfo{}, errors.New("モデルが選択されていません。設定画面で選んでください。")
	}
	models, err := aiprovider.KnownModels(provider)
	if err != nil {
		return aiprovider.ModelInfo{}, err
	}
	for _, m := range models {
		if m.ID == modelID {
			return m, nil
		}
	}
	return aiprovider.ModelInfo{ID: modelID, Tier: aiprovider.TierOther}, nil
}
