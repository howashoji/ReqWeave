package binding

// 回答モードの AI 対話回答のバインディング。
//
// 担当者モードの対話（dialogue_screen.go）とは**別経路**である。回答モードにはプロジェクトも
// メンバーも監査ログも無いため、`dialogue.Engine`（`*projectstore.Store` を必須とする）は持ち込めない。
//
// 本経路が担当者モードと違う点（設計の根拠つき）:
//   - **トークン上限の判定を通らない**（本人の端末・本人の資格で動き、
//     プロジェクトの記録に残らない）。これは網から漏れたのではなく**対象外である**ことを
//     usage_guard_test.go に明示している。
//   - **AI 送信記録（監査ログ）を作らない**（受け渡しのファイルに監査データを含めないため）。
//     そのぶん、送る内容は有効化の操作で**事前に**示す。
//   - **プランの残量を出さない**。**モデル・エフォートを選ばせない**。

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// respondAILabel は回答モードのプロバイダ設定の表示名（キー参照名・サインインの対象の識別）。
//
// 回答モードにはアプリ設定（プロバイダ設定の一覧）が無い。キーの保管と
// サインインは担当者モードと**同じ実装**を使う（キー登録の画面は両モードで共通の実装）ため、
// 参照名だけを固定値で分ける（担当者モードの登録と混ざらないようにする）。
//
// **結合テストのみが差し替える**（利用者のセキュアストレージへ固定名の項目を作らないため。
// 公開 API・設定からの差し替え経路は無い = openURLFn / newAdapter と同じ扱い）。
var respondAILabel = "回答モード"

// EventRespondAI は回答モードの AI 対話の進行を画面へ知らせるイベント名（回答画面の下部ペイン）。
const EventRespondAI = "respondai:event"

// 回答モードの AI 対話イベントの種類（生のコード値は画面に出さず、分岐にだけ使う）。
const (
	RespondAIEventText  = "text"  // 応答の本文が届いた（差分）
	RespondAIEventDone  = "done"  // 応答が終わった（中断を含む）
	RespondAIEventError = "error" // 失敗した（Message が利用者向けの 1 文）
)

// RespondAIStreamEvent は対話ペインへ流す 1 件のイベント。
type RespondAIStreamEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// Message は失敗のときの利用者向け 1 文（原因＋次の行動。回答モードの様式）。
	Message string `json:"message,omitempty"`
	// Interrupted は利用者の中断で終わったか。
	Interrupted bool `json:"interrupted,omitempty"`
}

// RespondAIScopeView は AI 対話回答を有効にする操作で示す送信範囲。
//
// **文言の正本はバックエンド**（画面に一覧を作らない）。
// 回答モードには送信記録が無いため、この表示が「何を送るか」を知る唯一の機会になる。
type RespondAIScopeView struct {
	// Sent は送るもの、NotSent は送らないもの（いずれも利用者の言葉で書く）。
	Sent    []string `json:"sent"`
	NotSent []string `json:"notSent"`
	// Notice はプロバイダ固有の注意喚起（担当者モードと同じ内容）。
	Notice string `json:"notice,omitempty"`
	// PolicyURL はデータ利用ポリシーの参照先。
	PolicyURL string `json:"policyUrl,omitempty"`
	// NoRecordNotice は「送信記録が残らないこと」の説明。
	NoRecordNotice string `json:"noRecordNotice"`
}

// respondAIScopeSent / respondAIScopeNotSent は、回答モードで AI へ送るもの・送らないものの区分を利用者の言葉で写したもの。
//
// **表の区分と 1 対 1 に対応させる**（設計を変えずに表示だけ変えると、説明と実際がずれる）。
var (
	respondAIScopeSent = []string{
		"この質問票に入っている全ての質問（質問文・背景説明・回答形式・選択肢）",
		"質問に出てくる用語の説明",
		"あなたがこれまでに入力した回答（「わからない」を選んだものを含む）",
		"この画面での AI とのやり取り",
	}
	respondAIScopeNotSent = []string{
		"あなたの氏名・所属",
		"質問票や質問の管理番号",
		"受け取ったファイルの保護に使っている情報（合言葉を含む）",
		"AI を使うためのキーやサインインの情報",
		"ファイルの置き場所・ファイル名・お使いの端末の情報",
		"担当者側のプロジェクトの資料（そもそも受け取ったファイルに入っていません）",
	}
)

// respondAINoRecordNotice は送信記録が残らないことの説明。
const respondAINoRecordNotice = "この画面では、送った内容の記録は残りません。" +
	"上の一覧が、何を送るかを確かめられる唯一の機会です。"

// RespondAIScope は AI 対話回答を有効にする前に示す送信範囲を返す。
//
// 引数のプロバイダに応じて注意喚起を添える（担当者モードと同じ内容）。
func (a *API) RespondAIScope(providerID string) RespondAIScopeView {
	view := RespondAIScopeView{
		Sent:           append([]string(nil), respondAIScopeSent...),
		NotSent:        append([]string(nil), respondAIScopeNotSent...),
		Notice:         providerNotice(providerID),
		NoRecordNotice: respondAINoRecordNotice,
	}
	for _, p := range providerOptions {
		if p.ID == providerID {
			view.PolicyURL = p.PolicyURL
			break
		}
	}
	return view
}

// RespondAIProviders は回答モードで選べる AIプロバイダの一覧（AI 対話回答を有効にする前の画面で出す）。
//
// 担当者モードの初期設定と**同じ定義**を返す（一覧を回答モード用に作らない = 二重管理の禁止）。
// Codex App Server が macOS 版でだけ出るのも同じ仕組みで決まる。
// **モデル・エフォートの選択肢は返さない**（回答モードでは選ばせない）。
func (a *API) RespondAIProviders() []ProviderOption {
	return availableProviderOptions(runtime.GOOS)
}

// RespondAIDialogueView は AI 対話ペインの表示内容（回答画面の下部ペイン）。
type RespondAIDialogueView struct {
	// Enabled は有効化済みか（未有効なら画面はプロバイダ選択と注意喚起を出す）。
	Enabled bool `json:"enabled"`
	// ProviderID / ProviderLabel は選んだ AIプロバイダ（未有効なら空）。
	ProviderID    string `json:"providerId,omitempty"`
	ProviderLabel string `json:"providerLabel,omitempty"`
	// AuthMethod / AuthMethodLabel は認証方式。
	AuthMethod      string `json:"authMethod,omitempty"`
	AuthMethodLabel string `json:"authMethodLabel,omitempty"`
	// CanSignOut はサインアウトの導線を出すか（サインイン方式で有効にしたときだけ）。
	// **画面に認証方式のコード値を比較させない**（判定はここで済ませる）。
	CanSignOut bool `json:"canSignOut"`
	// Running は応答の受信中か（送信操作の無効化・中断操作の表示に使う）。
	Running bool `json:"running"`
	// Utterances は対話の発話列（古い順）。
	Utterances []RespondUtteranceView `json:"utterances"`
}

// RespondUtteranceView は 1 発話の表示内容。
//
// 内部の話者コード（agent / user）を画面へ出さない。
type RespondUtteranceView struct {
	ID           string `json:"id"`
	SpeakerLabel string `json:"speakerLabel"`
	// IsAgent は表示の左右・配色の出し分けに使う（コード値の代わり）。
	IsAgent bool   `json:"isAgent"`
	At      string `json:"at"`
	Body    string `json:"body"`
	// Interrupted は中断した応答か（途中までの内容である旨を画面に示す）。
	Interrupted bool `json:"interrupted,omitempty"`
	// CanApply は回答欄へ移す操作を出せるか（AI の応答だけ）。
	CanApply bool `json:"canApply"`
}

// respondSpeakerLabel は話者の表示文言（生のコード値を画面へ出さない）。
func respondSpeakerLabel(speaker string) string {
	switch speaker {
	case projectstore.SpeakerAgent:
		return "AI"
	case projectstore.SpeakerUser:
		return "あなた"
	default:
		return "不明"
	}
}

// RespondAIDialogue は AI 対話ペインの現在の表示内容を返す。
func (a *API) RespondAIDialogue() (RespondAIDialogueView, error) {
	sess, err := a.respondSession()
	if err != nil {
		return RespondAIDialogueView{}, err
	}
	a.respond.mu.Lock()
	defer a.respond.mu.Unlock()

	view := RespondAIDialogueView{
		Enabled:       a.respond.aiEnabled,
		ProviderID:    a.respond.aiProvider,
		AuthMethod:    a.respond.aiAuthMethod,
		ProviderLabel: providerDisplayName(a.respond.aiProvider),
		Utterances:    make([]RespondUtteranceView, 0, len(sess.Utterances)),
	}
	if view.Enabled {
		view.AuthMethodLabel = authMethodLabel(a.respond.aiProvider, a.respond.aiAuthMethod)
		view.CanSignOut = usesSignIn(a.respond.aiAuthMethod)
	}
	view.Running = a.respond.aiRunning
	for _, u := range sess.Utterances {
		agent := u.Speaker == projectstore.SpeakerAgent
		view.Utterances = append(view.Utterances, RespondUtteranceView{
			ID:           u.ID,
			SpeakerLabel: respondSpeakerLabel(u.Speaker),
			IsAgent:      agent,
			At:           u.At.Local().Format("2006-01-02 15:04"),
			Body:         u.Body,
			Interrupted:  u.IsInterrupted(),
			CanApply:     agent,
		})
	}
	return view, nil
}

// EnableRespondAI は AI 対話回答を有効にする。
//
// **送信範囲の事前表示を確認したあとにだけ呼ばれる**ことを前提にし、確認の有無を引数で受け取る
// （画面が確認を飛ばして呼んだ場合はここで止める = 確認前に 1 回も AI を呼ばせない）。
func (a *API) EnableRespondAI(providerID, authMethod string, scopeConfirmed bool) (RespondAIDialogueView, error) {
	if !scopeConfirmed {
		return RespondAIDialogueView{}, errRespondScopeUnconfirmed
	}
	sess, err := a.respondSession()
	if err != nil {
		return RespondAIDialogueView{}, err
	}
	if !supportsAuthMethod(providerID, authMethod) {
		return RespondAIDialogueView{}, fmt.Errorf("選んだ組み合わせでは利用できません。システム担当者へ連絡してください")
	}

	a.respond.mu.Lock()
	a.respond.aiEnabled = true
	a.respond.aiProvider = providerID
	a.respond.aiAuthMethod = authMethod
	a.respond.aiModel = aiprovider.ModelInfo{} // プロバイダを変えたら解決し直す
	a.respond.mu.Unlock()

	// 動作ログ（回答モードの allowlist に載っている事象）。
	// 記録するのは事実だけで、質問文・回答本文・発話は残さない。
	a.log.Info("respond.ai_enabled", "回答モードの AI 対話回答を有効にしました",
		applog.F("provider", providerID), applog.F("auth_method", authMethod),
		applog.F("questionnaire_id", sess.Questionnaire.ID))
	return a.RespondAIDialogue()
}

// DisableRespondAI は AI 対話回答を無効にする（対話の記録は消さない）。
func (a *API) DisableRespondAI() (RespondAIDialogueView, error) {
	sess, err := a.respondSession()
	if err != nil {
		return RespondAIDialogueView{}, err
	}
	a.respond.mu.Lock()
	provider, authMethod := a.respond.aiProvider, a.respond.aiAuthMethod
	a.respond.aiEnabled = false
	a.respond.mu.Unlock()

	a.log.Info("respond.ai_disabled", "回答モードの AI 対話回答を無効にしました",
		applog.F("provider", provider), applog.F("auth_method", authMethod),
		applog.F("questionnaire_id", sess.Questionnaire.ID))
	return a.RespondAIDialogue()
}

// errRespondScopeUnconfirmed は事前表示の確認を経ずに呼ばれたときの拒否。
//
// **確認操作を完了するまで AI プロバイダへの呼び出しが 1 回も発生しない**ことを、
// 画面の作りではなくバックエンドで担保する（キー登録の疎通確認も AI 呼び出しである）。
var errRespondScopeUnconfirmed = errors.New(
	"送信される内容の確認が必要です。表示された内容を確認してから有効にしてください")

// RegisterRespondAIKey は回答モードのシークレットキーを登録する（キー登録は担当者モードと共通の実装）。
//
// 中身は担当者モードの RegisterKey（疎通確認に成功したときだけ保管する）と同一で、
// 違いは**事前表示の確認を要求すること**と、参照名を回答モード用に固定することだけ。
func (a *API) RegisterRespondAIKey(providerID, key string, scopeConfirmed bool) (VerifyResult, error) {
	if !scopeConfirmed {
		return VerifyResult{}, errRespondScopeUnconfirmed
	}
	if _, err := a.respondSession(); err != nil {
		return VerifyResult{}, err
	}
	if !supportsAuthMethod(providerID, string(aiprovider.AuthSecretKey)) {
		return VerifyResult{}, errors.New("選んだ組み合わせでは利用できません。システム担当者へ連絡してください")
	}
	return a.RegisterKey(providerID, respondAILabel, key)
}

// StartRespondAISignIn は ChatGPT のアカウントでのサインインを始める（画面は担当者モードのサインインと共用）。
func (a *API) StartRespondAISignIn(scopeConfirmed bool) (SignInView, error) {
	if !scopeConfirmed {
		return SignInView{}, errRespondScopeUnconfirmed
	}
	if _, err := a.respondSession(); err != nil {
		return SignInView{}, err
	}
	return a.StartCodexSignIn(respondAILabel)
}

// SignOutRespondAI は回答モードのサインアウト。
//
// 認証情報を消すのは Codex 側（OS セキュアストレージ）であり、本システムは読み書きしない。
// 出力済みの返送ファイルは変わらない（サインアウトは返送の作業に影響しない）。
func (a *API) SignOutRespondAI() (RespondAIDialogueView, error) {
	if _, err := a.respondSession(); err != nil {
		return RespondAIDialogueView{}, err
	}
	a.respond.mu.Lock()
	signIn := usesSignIn(a.respond.aiAuthMethod) && a.respond.aiEnabled
	a.respond.mu.Unlock()
	if !signIn {
		return RespondAIDialogueView{}, errors.New(
			"サインインしていないため、サインアウトする必要はありません。")
	}
	if err := a.SignOutCodex(respondAILabel); err != nil {
		return RespondAIDialogueView{}, err
	}
	// 無効化と同じ事実（動作ログの allowlist の「無効にした」）として記録する。
	// サインアウト専用の事象を足さない（allowlist に無い事象を作らない）。
	return a.DisableRespondAI()
}

// SendRespondAIMessage は 1 往復の対話を行う（AI への送信は共通の唯一の入口を通る）。
//
// **担当者モードの前段（beginAICall）を通らない**。回答モードの AI 呼び出しは本人の端末・本人の
// 資格で行われ、プロジェクトのトークン上限の対象外である。
// これは網から漏れたのではなく対象外であることを、usage_guard_test.go が名前で固定している。
//
// 送信記録（監査ログ）も作らない（回答モードにはプロジェクトが無い）。
// そのぶん失敗は動作ログへ残す（回答モードの allowlist の事象）。
func (a *API) SendRespondAIMessage(body string) (RespondAIDialogueView, error) {
	sess, err := a.respondSession()
	if err != nil {
		return RespondAIDialogueView{}, err
	}
	if strings.TrimSpace(body) == "" {
		return RespondAIDialogueView{}, errors.New("相談したい内容を入力してください。")
	}

	a.respond.mu.Lock()
	if !a.respond.aiEnabled {
		a.respond.mu.Unlock()
		return RespondAIDialogueView{}, errors.New(
			"AI との相談がまだ使える状態になっていません。相談の欄から有効にしてください。")
	}
	if a.respond.aiRunning {
		a.respond.mu.Unlock()
		return RespondAIDialogueView{}, errors.New(
			"前の応答がまだ終わっていません。終わるまで待つか、中断してください。")
	}
	provider, authMethod := a.respond.aiProvider, a.respond.aiAuthMethod
	a.respond.aiRunning = true
	a.respond.mu.Unlock()
	// 早い戻り（アダプタの生成失敗など）でも必ず外す。正常系では応答の受信を終えた時点で
	// 明示的に外す（**表示を組み立てる前に外さないと、返した内容が「受信中」のままになる**）。
	defer a.finishRespondAICall()

	adapter, err := a.providerAdapter(provider, respondAILabel, authMethod)
	if err != nil {
		return RespondAIDialogueView{}, err
	}
	model, err := a.respondAIModel(adapter)
	if err != nil {
		return RespondAIDialogueView{}, err
	}

	// 発話は AI 送信の前に保存する（担当者モードの対話と同じ規律。送信できてもできなくても記録が残る）。
	if err := sess.AppendUtterance(projectstore.SpeakerUser, body); err != nil {
		return RespondAIDialogueView{}, err
	}

	ctx, cancel := context.WithCancel(a.context())
	a.respond.mu.Lock()
	a.respond.aiCancel = cancel
	a.respond.mu.Unlock()
	defer cancel()

	text, interrupted, perr := a.streamRespondAI(ctx, sess, adapter, model, authMethod)
	a.finishRespondAICall()
	if interrupted {
		// 受信済みの本文を中断発話として残す（担当者モードの中断と同じ扱い。空なら残さない）。
		if err := sess.AppendInterruptedUtterance(text); err != nil {
			return RespondAIDialogueView{}, err
		}
		a.emitRespondAI(RespondAIStreamEvent{Kind: RespondAIEventDone, Interrupted: true})
		return a.RespondAIDialogue()
	}
	if perr != nil {
		// 途中まで届いていた本文は捨てずに残す（中断と同じ扱い。何を送ったかの手がかりになる）。
		if err := sess.AppendInterruptedUtterance(text); err != nil {
			return RespondAIDialogueView{}, err
		}
		message := respondAIErrorMessage(perr, usesSignIn(authMethod))
		a.emitRespondAI(RespondAIStreamEvent{Kind: RespondAIEventError, Message: message})
		return RespondAIDialogueView{}, errors.New(message)
	}
	if strings.TrimSpace(text) == "" {
		message := respondAIErrorMessage(nil, usesSignIn(authMethod))
		a.emitRespondAI(RespondAIStreamEvent{Kind: RespondAIEventError, Message: message})
		return RespondAIDialogueView{}, errors.New(message)
	}
	if err := sess.AppendUtterance(projectstore.SpeakerAgent, text); err != nil {
		return RespondAIDialogueView{}, err
	}
	a.emitRespondAI(RespondAIStreamEvent{Kind: RespondAIEventDone})
	return a.RespondAIDialogue()
}

// finishRespondAICall は「受信中」を解く（何度呼んでもよい）。
func (a *API) finishRespondAICall() {
	a.respond.mu.Lock()
	a.respond.aiRunning = false
	a.respond.aiCancel = nil
	a.respond.mu.Unlock()
}

// streamRespondAI は 1 回のストリーミングを行い、本文・中断の有無・失敗を返す。
//
// 送るのは **RespondAIScope で示した送信範囲だけ**（システムプロンプト = 指示文 + 質問票の文脈は
// exchange 側の 1 か所で組み立てる。メッセージは本画面の発話履歴だけ）。
func (a *API) streamRespondAI(ctx context.Context, sess *exchange.RespondSession,
	adapter aiprovider.Adapter, model aiprovider.ModelInfo, authMethod string,
) (string, bool, *aiprovider.ProviderError) {

	req := aiprovider.ChatRequest{
		Model:    model.ID,
		System:   sess.AISystemPrompt(),
		Messages: respondAIMessages(sess),
		// エフォートは「標準」固定（回答モードでは選ばせず、表示もしない）。
		Effort: aiprovider.MapEffort(adapter.ID(), aiprovider.EffortStandard).ClampToModel(model),
	}
	ch, err := aiprovider.StreamRetrying(ctx, adapter, req, aiprovider.StreamOptions{
		Timeouts: a.adapterOptionsFor(authMethod).Timeouts,
		// Recorder は渡さない（回答モードには AI 送信記録が無い）。
		OnPanic:   a.onStreamPanic,
		OnFailure: a.onRespondAICallFailed(sess.Questionnaire.ID),
	})
	if err != nil {
		return "", false, &aiprovider.ProviderError{
			Class: aiprovider.ErrClassConfig, Provider: adapter.ID(), Message: err.Error()}
	}

	var text strings.Builder
	var interrupted bool
	var perr *aiprovider.ProviderError
	for ev := range ch {
		switch ev.Kind {
		case aiprovider.EventTextDelta:
			text.WriteString(ev.Text)
			a.emitRespondAI(RespondAIStreamEvent{Kind: RespondAIEventText, Text: ev.Text})
		case aiprovider.EventDone:
			interrupted = ev.Interrupted
		case aiprovider.EventError:
			perr = ev.Err
		}
	}
	return text.String(), interrupted, perr
}

// respondAIMessages は発話履歴をメッセージ列へ写す。
//
// 中断発話も含める（会話の流れを保つため）。質問票の文脈は System 側にあり、ここでは足さない。
func respondAIMessages(sess *exchange.RespondSession) []aiprovider.Message {
	out := make([]aiprovider.Message, 0, len(sess.Utterances))
	for _, u := range sess.Utterances {
		role := aiprovider.RoleUser
		if u.Speaker == projectstore.SpeakerAgent {
			role = aiprovider.RoleAssistant
		}
		out = append(out, aiprovider.Message{Role: role, Content: u.Body})
	}
	return out
}

// respondAIModel は用いるモデルを決める。
//
// **プロバイダの推奨**を使い、取得できないときは既知一覧の既定へ倒す。利用者には選ばせず、
// 画面にも出さない。解決は 1 回だけ行い、以後は保持した値を使う（呼び出しのたびに一覧を取りに行かない）。
func (a *API) respondAIModel(adapter aiprovider.Adapter) (aiprovider.ModelInfo, error) {
	a.respond.mu.Lock()
	cached := a.respond.aiModel
	a.respond.mu.Unlock()
	if cached.ID != "" {
		return cached, nil
	}

	var model aiprovider.ModelInfo
	var ok bool
	if models, err := adapter.ListModels(a.context()); err == nil {
		model, ok = recommendedModel(models)
	}
	if !ok {
		known, err := aiprovider.KnownModels(adapter.ID())
		if err != nil {
			return aiprovider.ModelInfo{}, err
		}
		if model, ok = recommendedModel(known); !ok {
			return aiprovider.ModelInfo{}, errors.New(
				"この AI プロバイダで使えるモデルが分かりませんでした。システム担当者へ連絡してください。")
		}
	}
	a.respond.mu.Lock()
	a.respond.aiModel = model
	a.respond.mu.Unlock()
	return model, nil
}

// recommendedModel は一覧から推奨モデルを選ぶ（主系統の既定）。
//
// 推奨が無い一覧では主系統の先頭、それも無ければ先頭を使う（黙って空を返さない）。
func recommendedModel(models []aiprovider.ModelInfo) (aiprovider.ModelInfo, bool) {
	var primary aiprovider.ModelInfo
	var hasPrimary bool
	for _, m := range models {
		if m.Tier != aiprovider.TierPrimary {
			continue
		}
		if m.DefaultForTier {
			return m, true
		}
		if !hasPrimary {
			primary, hasPrimary = m, true
		}
	}
	if hasPrimary {
		return primary, true
	}
	if len(models) > 0 {
		return models[0], true
	}
	return aiprovider.ModelInfo{}, false
}

// CancelRespondAIMessage は受信中の応答を中断する（担当者モードの中断と同方式）。
//
// 受信済みの本文は中断発話として残る（送信側で保存する）。
func (a *API) CancelRespondAIMessage() error {
	a.respond.mu.Lock()
	cancel := a.respond.aiCancel
	a.respond.mu.Unlock()
	if cancel == nil {
		return errors.New("中断できる応答がありません。")
	}
	cancel()
	return nil
}

// onRespondAICallFailed は回答モードの AI 呼び出しの失敗の記録先（動作ログの allowlist の事象）。
//
// 担当者モードと**同一項目**に質問票 ID だけを足す（どの配布分の作業中かが分からないと、
// 担当者が連絡を受けても切り分けられないため）。**質問 ID・本文は足さない**。
func (a *API) onRespondAICallFailed(questionnaireID string) aiprovider.FailureRecorder {
	return func(rec aiprovider.FailureRecord) {
		fields := append(aiFailureFields(rec), applog.F("questionnaire_id", questionnaireID))
		a.log.Warn(eventAICallFailed, "AI の呼び出しに失敗しました", fields...)
	}
}

// msgRespondAIUnavailable は回答モードの既定の文言（ステークホルダーは設定を直せないため、担当者への連絡を促す）。
const msgRespondAIUnavailable = "この操作は現在使えません。システム担当者へ連絡してください。"

// msgRespondSignInExpired はサインインの失効（回答モードには設定画面が無いため、
// 担当者モードの「設定画面で」に代えて AI 対話回答の導線を示す）。
const msgRespondSignInExpired = "ChatGPT のアカウントのサインインが切れました。" +
	"「AI と相談する」からサインインし直してください。"

// respondAIErrorMessage は回答モードの利用者向けの 1 文を作る。
//
// 既定は「システム担当者へ連絡してください」へ倒す（ステークホルダーは設定を直せない）。
// **ただしサインインの操作に関するものだけ**は本人が対処できるため、担当者モードと同じ趣旨の
// 文言を出す。生のコード値・分類名は画面へ出さない。
func respondAIErrorMessage(perr *aiprovider.ProviderError, signedIn bool) string {
	if perr == nil {
		return msgRespondAIUnavailable
	}
	switch perr.Code {
	case codeSignInTimedOut:
		return msgSignInTimedOut
	case codeSignInUnavailable:
		return msgSignInUnavailable
	case codeUnauthorized:
		if signedIn {
			return msgRespondSignInExpired
		}
	}
	return msgRespondAIUnavailable
}

// emitRespondAI は対話ペインへイベントを流す（Wails 未起動のテストでは何もしない）。
func (a *API) emitRespondAI(ev RespondAIStreamEvent) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, EventRespondAI, ev)
}

// ---------------------------------------------------------------------------
// AI の応答を構造化回答欄へ移す（下書きとして入れる。回答にするのは本人）
// ---------------------------------------------------------------------------

// 回答欄へ入れるときの入れ方（画面の 2 択「置き換える / 末尾へ足す」）。
const (
	RespondDraftReplace = "replace"
	RespondDraftAppend  = "append"
)

// respondDraftNotice は下書きである旨（そのままでは回答にならない）。
const respondDraftNotice = "回答欄へ下書きとして入れました。" +
	"内容を確かめて、必要なら書き直してから次へ進んでください。"

// RespondAnswerDraftView は回答欄へ入れる下書き。
//
// **保存はしない**（回答欄で本人が確認・編集して初めて回答になり、通常の回答と同じ経路で
// 自動保存される）。アプリが勝手に回答を書き込まないことを型で表す。
type RespondAnswerDraftView struct {
	QuestionID string `json:"questionId"`
	// FreeText は自由記述欄へ入れる文面（置き換え・追記を適用済み）。
	FreeText string `json:"freeText"`
	Notice   string `json:"notice"`
}

// RespondAIAnswerDraft は AI の応答 1 件を、いま表示している質問の自由記述欄の下書きにする。
//
// **押すのは本人**（アプリが自動で書き込まない）。移す先はこの 1 問だけで、他の質問へは書かない。
// 選択肢形式（`choice` / `multi_choice`）では選択肢を自動で選ばない。
func (a *API) RespondAIAnswerDraft(questionID, utteranceID, mode string) (RespondAnswerDraftView, error) {
	sess, err := a.respondSession()
	if err != nil {
		return RespondAnswerDraftView{}, err
	}
	question, ok := respondQuestionByID(sess, questionID)
	if !ok {
		return RespondAnswerDraftView{}, errors.New("この質問が見つかりません。画面を開き直してください。")
	}
	if !respondAcceptsFreeText(question.AnswerFormat) {
		return RespondAnswerDraftView{}, errors.New(
			"この質問は選択肢から選ぶ形式です。AI の内容は参考にして、選ぶのはご自身で行ってください。")
	}
	body, ok := respondAgentUtteranceBody(sess, utteranceID)
	if !ok {
		return RespondAnswerDraftView{}, errors.New("移す内容が見つかりません。もう一度相談してください。")
	}

	current := respondCurrentFreeText(sess, questionID)
	var draft string
	switch mode {
	case RespondDraftReplace:
		draft = body
	case RespondDraftAppend:
		draft = body
		if strings.TrimSpace(current) != "" {
			draft = current + "\n\n" + body
		}
	default:
		// 黙って上書きしない（入れ方は必ず本人が選ぶ）。
		return RespondAnswerDraftView{}, errors.New(
			"入れ方を選んでください（いまの内容を置き換える / 末尾へ足す）。")
	}
	return RespondAnswerDraftView{QuestionID: questionID, FreeText: draft, Notice: respondDraftNotice}, nil
}

// respondAcceptsFreeText は自由記述欄を持つ回答形式か（質問の answer_format で判定する）。
func respondAcceptsFreeText(format string) bool {
	return format == projectstore.AnswerFormatFree || format == projectstore.AnswerFormatChoiceWithFree
}

func respondQuestionByID(s *exchange.RespondSession, id string) (projectstore.Question, bool) {
	for _, q := range s.Questionnaire.Questions {
		if q.ID == id {
			return q, true
		}
	}
	return projectstore.Question{}, false
}

// respondAgentUtteranceBody は AI の発話の本文を返す（本人の発話は移す対象にしない）。
func respondAgentUtteranceBody(s *exchange.RespondSession, id string) (string, bool) {
	for _, u := range s.Utterances {
		if u.ID == id && u.Speaker == projectstore.SpeakerAgent {
			return u.Body, true
		}
	}
	return "", false
}

func respondCurrentFreeText(s *exchange.RespondSession, questionID string) string {
	if s.Answers == nil {
		return ""
	}
	for _, ans := range s.Answers.Answers {
		if ans.QuestionID == questionID {
			return ans.FreeText
		}
	}
	return ""
}
