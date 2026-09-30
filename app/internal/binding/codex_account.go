package binding

// 本ファイルは ChatGPT のアカウントでのサインインとプランの残量のバインディング
// （画面はサインイン・初期設定・設定・利用量の各画面）。
//
// 役割分担:
//
//   - **認可の URL を既定のブラウザで開くのは本層**。アダプタは URL を返すだけで外部のプログラムを
//     起動しない（依存規則の機械検査 = tools/depcheck の browser-open）。
//   - サインイン・サインアウト・アカウントの情報・残量は、抽象化層の**任意のインタフェース**
//     （aiprovider.AccountAdapter / aiprovider.PlanUsageReporter）を型の判定で使う。
//     プロバイダ名で分岐しない（未対応のプロバイダは「対応していません」で断る）。
//   - **メールアドレス・プラン種別・認可の URL を保存・記録しない**（動作ログにも残さない）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// providerCodexID は Codex App Server のプロバイダ ID。
const providerCodexID = "codex"

// EventCodexSignIn はサインインの結果を画面へ知らせるイベント名（待っている間も他の画面を使えるため）。
const EventCodexSignIn = "codex:signin"

// サインインの状態（画面の 4 状態）。
const (
	SignInStateSignedOut = "signed_out" // 1. 説明（未サインイン）
	SignInStateWaiting   = "waiting"    // 2. 待機（ブラウザでの認可待ち）
	SignInStateSignedIn  = "signed_in"  // 3. 成功
	SignInStateFailed    = "failed"     // 4. 失敗
)

// AccountView はアカウントの情報の表示内容（画面の 3. 成功）。
//
// **生のコード値を画面へ出さない**（ラベルは型で網羅強制し、未知は「不明」）。
type AccountView struct {
	// Email は表示のみ。保存しない。
	Email string `json:"email,omitempty"`
	// PlanLabel はプラン種別のラベル（未知・未取得は「不明」）。
	PlanLabel string `json:"planLabel"`
	// AccountLabel はアカウントの種類のラベル（未知は「不明」）。
	AccountLabel string `json:"accountLabel"`
	// TrainingNotice は個人向けプラン（取得できない場合を含む）での学習利用の注意喚起と停止方法。
	// 空なら表示しない。
	TrainingNotice string `json:"trainingNotice,omitempty"`
}

// SignInView はサインイン画面の状態。
type SignInView struct {
	// Available はこの環境・この設定でサインインを使えるか（使えないときは画面に出さない）。
	Available bool `json:"available"`
	// State は signed_out / waiting / signed_in / failed。
	State string `json:"state"`
	// Label は対象のプロバイダ設定の表示名。
	Label string `json:"label,omitempty"`
	// Message は失敗のときの利用者向け 1 文（原因＋次の行動）。
	Message string `json:"message,omitempty"`
	// WaitMinutes は待ち受けの上限（分）。画面の「最大 ◯ 分待ちます」に使う。
	WaitMinutes int `json:"waitMinutes,omitempty"`
	// Account は成功のときのアカウントの情報。
	Account *AccountView `json:"account,omitempty"`
}

// PlanUsageWindowView は 1 つの枠の残量。
type PlanUsageWindowView struct {
	// Label は枠の名前（Codex が名前を返さないときは区分から作る）。
	Label string `json:"label"`
	// UsedPercent は使用率（0〜100）。
	UsedPercent int `json:"usedPercent"`
	// WindowDurationMins は枠の長さ（分。0 = 不明）。**特定の長さを前提にしない**（プランで異なる）。
	WindowDurationMins int `json:"windowDurationMins"`
	// WindowLabel は枠の長さの表示用ラベル（不明のときは空）。
	WindowLabel string `json:"windowLabel,omitempty"`
	// ResetsAt は次の回復時刻（UTC の ISO 8601。空 = 不明。表示時にローカルへ変換する）。
	ResetsAt string `json:"resetsAt,omitempty"`
}

// PlanUsageView はプランの残量の表示内容。
type PlanUsageView struct {
	// Available は残量を表示する設定か（シークレットキー方式では false = 表示しない）。
	Available bool `json:"available"`
	// Fetched は値を受け取っているか（false = 未取得である旨を表示する）。
	Fetched bool `json:"fetched"`
	// PlanLabel はプラン種別のラベル（未知は「不明」）。
	PlanLabel string `json:"planLabel,omitempty"`
	// ReceivedAt は値を受け取った時刻（UTC の ISO 8601。残量と併せて受け取った時刻を表示する）。
	ReceivedAt string                `json:"receivedAt,omitempty"`
	Windows    []PlanUsageWindowView `json:"windows,omitempty"`
	// Notice は未取得のときの説明（黙って空欄にしない）。
	Notice string `json:"notice,omitempty"`
}

// planUsageUnfetchedNotice は未取得のときの説明（閲覧の操作では取りに行かない）。
const planUsageUnfetchedNotice = "まだ取得していません（AI を使うと表示されます）"

// codexSignInState は進行中のサインイン（同時に 1 件）。
type codexSignInState struct {
	mu      sync.Mutex
	state   string
	label   string
	loginID string
	authURL string
	message string
	account *AccountView
	cancel  context.CancelFunc
	// signedIn は分かっている範囲の「サインイン済みか」（nil = 不明）。
	//
	// アプリを起動し直すと不明に戻る。**不明のときは使える前提で扱い**、実際の失効は
	// AI 呼び出しのエラー（unauthorized）で気づく（エラーの文言で再サインインへ誘導）。
	// サインアウトの直後だけは確実に「未サインイン」であり、AI 機能をブロックする。
	signedIn *bool
}

// openURLFn は認可の URL を既定のブラウザで開く（パッケージ内のテストのみが差し替える）。
var openURLFn = func(ctx context.Context, url string) { wailsruntime.BrowserOpenURL(ctx, url) }

// CodexSignInState は現在のサインインの状態を返す。
//
// **通信を起こさない**（保持している状態だけを返す）。
func (a *API) CodexSignInState() SignInView {
	a.codexSignIn.mu.Lock()
	defer a.codexSignIn.mu.Unlock()
	return a.signInViewLocked()
}

// signInViewLocked は保持している状態から表示内容を組み立てる（呼び出し側がロックを持つこと）。
func (a *API) signInViewLocked() SignInView {
	state := a.codexSignIn.state
	if state == "" {
		state = SignInStateSignedOut
	}
	return SignInView{
		Available:   supportsAuthMethod(providerCodexID, string(aiprovider.AuthChatGPTSignin)),
		State:       state,
		Label:       a.codexSignIn.label,
		Message:     a.codexSignIn.message,
		WaitMinutes: signInWaitMinutes,
		Account:     a.codexSignIn.account,
	}
}

// signInWaitMinutes は画面へ出す待ち受けの上限（分）。値の正本はアダプタで、
// 画面は本値を表示に使う（15 分で Codex が失敗を返すことを実機で確かめた）。
const signInWaitMinutes = 15

// StartCodexSignIn はサインインを開始し、認可の URL を既定のブラウザで開く。
//
// 待っている間も他の画面を使えるよう、完了の待ち受けは背景で行い、結果はイベントで知らせる。
func (a *API) StartCodexSignIn(label string) (SignInView, error) {
	adapter, err := a.accountAdapter(label)
	if err != nil {
		return SignInView{}, err
	}
	signIn, err := adapter.StartSignIn(a.context())
	if err != nil {
		return a.failSignIn(label, err), nil
	}
	if !strings.HasPrefix(signIn.AuthorizationURL, "https://") {
		// 子プロセスが返した値をそのまま OS へ渡さない（https 以外を開かない）。
		return a.failSignIn(label, configSignInError("認可の URL を確認できませんでした")), nil
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	a.codexSignIn.mu.Lock()
	if a.codexSignIn.cancel != nil {
		a.codexSignIn.cancel() // 前の待ち受けが残っていれば畳む（同時に 1 件）
	}
	a.codexSignIn.state = SignInStateWaiting
	a.codexSignIn.label = label
	a.codexSignIn.loginID = signIn.LoginID
	a.codexSignIn.authURL = signIn.AuthorizationURL
	a.codexSignIn.message = ""
	a.codexSignIn.account = nil
	a.codexSignIn.cancel = cancel
	view := a.signInViewLocked()
	a.codexSignIn.mu.Unlock()

	openURLFn(a.context(), signIn.AuthorizationURL)
	go a.waitCodexSignIn(waitCtx, adapter, label, signIn.LoginID)
	return view, nil
}

// waitCodexSignIn は完了を待って状態を更新し、画面へイベントで知らせる。
func (a *API) waitCodexSignIn(ctx context.Context, adapter aiprovider.AccountAdapter,
	label, loginID string) {

	account, err := adapter.WaitSignIn(ctx, loginID)
	switch {
	case errors.Is(err, context.Canceled):
		// 利用者の取り消し。**エラーとして表示しない**。設定は変えない。
		a.setSignedOut(label)
	case err != nil:
		a.failSignIn(label, err)
	default:
		a.succeedSignIn(label, account)
	}
	a.emitSignIn()
}

// succeedSignIn はサインインの成功を記録する（設定の保存は利用者の確定操作で行う）。
func (a *API) succeedSignIn(label string, account aiprovider.AccountInfo) {
	view := accountView(account)
	signedIn := true
	a.codexSignIn.mu.Lock()
	defer a.codexSignIn.mu.Unlock()
	a.codexSignIn.state = SignInStateSignedIn
	a.codexSignIn.label = label
	a.codexSignIn.message = ""
	a.codexSignIn.account = &view
	a.codexSignIn.signedIn = &signedIn
	a.codexSignIn.cancel = nil
}

// failSignIn は失敗を記録し、利用者向けの 1 文を持つ表示内容を返す。**設定は変えない**。
//
// 失敗した時点では**サインインできていない**ため、AI 呼び出しの可否も「未サインイン」に倒す
// （画面の 4. 失敗 → もう一度サインインする導線）。
func (a *API) failSignIn(label string, err error) SignInView {
	message := a.signInErrorMessage(err)
	signedOut := false
	a.codexSignIn.mu.Lock()
	defer a.codexSignIn.mu.Unlock()
	a.codexSignIn.state = SignInStateFailed
	a.codexSignIn.label = label
	a.codexSignIn.message = message
	a.codexSignIn.account = nil
	a.codexSignIn.signedIn = &signedOut
	a.codexSignIn.cancel = nil
	return a.signInViewLocked()
}

// setSignedOut は未サインインへ戻す（取り消し・サインアウト）。
func (a *API) setSignedOut(label string) {
	signedOut := false
	a.codexSignIn.mu.Lock()
	defer a.codexSignIn.mu.Unlock()
	a.codexSignIn.state = SignInStateSignedOut
	a.codexSignIn.label = label
	a.codexSignIn.message = ""
	a.codexSignIn.account = nil
	a.codexSignIn.signedIn = &signedOut
	a.codexSignIn.cancel = nil
}

// signInErrorMessage はサインインの失敗を利用者向けの 1 文にする（エラーカタログの文言）。
func (a *API) signInErrorMessage(err error) string {
	var pErr *aiprovider.ProviderError
	if !errors.As(err, &pErr) {
		return msgSignInUnavailable
	}
	// サインインの操作中のエラーであることが分かっているため、`unauthorized` は
	// 「サインインが切れた」の意味で扱う（signedIn = true）。
	return aiErrorMessage(pErr.Class.String(), pErr.Code, true, time.Time{})
}

// configSignInError はサインインを始められない状態の正規化エラー（エラーカタログの様式に対応）。
func configSignInError(message string) error {
	return &aiprovider.ProviderError{
		Class: aiprovider.ErrClassConfig, Provider: aiprovider.ProviderID(providerCodexID),
		Code: codeSignInUnavailable, Message: message,
	}
}

// emitSignIn は現在の状態を画面へ送る（待っている間に他の画面を見ていても結果が届く）。
func (a *API) emitSignIn() {
	if a.ctx == nil {
		return // Wails 未起動（テスト実行時）は何もしない
	}
	wailsruntime.EventsEmit(a.ctx, EventCodexSignIn, a.CodexSignInState())
}

// ReopenCodexSignInPage は認可のページをもう一度開く（画面の 2. 待機）。
func (a *API) ReopenCodexSignInPage() error {
	a.codexSignIn.mu.Lock()
	url := a.codexSignIn.authURL
	waiting := a.codexSignIn.state == SignInStateWaiting
	a.codexSignIn.mu.Unlock()
	if !waiting || url == "" {
		return errors.New("サインインの待ち受けが終わっています。もう一度サインインしてください。")
	}
	openURLFn(a.context(), url)
	return nil
}

// CancelCodexSignIn は待ち受けを取り消す（画面の 2. 待機。設定は変えない）。
func (a *API) CancelCodexSignIn() error {
	a.codexSignIn.mu.Lock()
	label, loginID, cancel := a.codexSignIn.label, a.codexSignIn.loginID, a.codexSignIn.cancel
	waiting := a.codexSignIn.state == SignInStateWaiting
	a.codexSignIn.mu.Unlock()
	if !waiting {
		return nil // 既に終わっている（取り消しは何度押しても失敗にしない）
	}
	if cancel != nil {
		cancel()
	}
	if adapter, err := a.accountAdapter(label); err == nil {
		_ = adapter.CancelSignIn(a.context(), loginID)
	}
	a.setSignedOut(label)
	return nil
}

// cancelCodexSignIn は本システムの終了時に待ち受けを畳む（背景の待ちを残さない）。
func (a *API) cancelCodexSignIn() {
	a.codexSignIn.mu.Lock()
	cancel := a.codexSignIn.cancel
	a.codexSignIn.cancel = nil
	a.codexSignIn.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SignOutCodex はサインアウトする（Codex の項目が OS セキュアストレージから消える）。
//
// サインアウト後は「未サインイン」となり、AI 呼び出しを伴う操作は「キー未登録」と同じ扱いで
// ブロックされる。
func (a *API) SignOutCodex(label string) error {
	adapter, err := a.accountAdapter(label)
	if err != nil {
		return err
	}
	if err := adapter.SignOut(a.context()); err != nil {
		return errors.New(a.signInErrorMessage(err))
	}
	a.setSignedOut(label)
	a.aiSettingsChanged() // 子プロセスを止める（認証の設定が変わったため）
	return nil
}

// CodexAccount は現在のアカウントの情報を返す（設定画面の状態表示）。
//
// **サインインの状態を確かめるための呼び出し**であり、残量の取得は行わない。
func (a *API) CodexAccount(label string) (SignInView, error) {
	adapter, err := a.accountAdapter(label)
	if err != nil {
		return SignInView{}, err
	}
	account, err := adapter.Account(a.context())
	if err != nil {
		return a.failSignIn(label, err), nil
	}
	if !account.SignedIn {
		a.setSignedOut(label)
		return a.CodexSignInState(), nil
	}
	a.succeedSignIn(label, account)
	return a.CodexSignInState(), nil
}

// CodexPlanUsage は保持しているプランの残量を返す。
//
// **取得のための通信を起こさない**（閲覧の操作で外部への通信を発生させない）。
// シークレットキー方式では表示しない。
func (a *API) CodexPlanUsage(label string) (PlanUsageView, error) {
	if !usesSignIn(a.storedAuthMethod(providerCodexID, label)) {
		return PlanUsageView{Available: false}, nil
	}
	usage, ok := a.planUsage(label)
	if !ok {
		return PlanUsageView{Available: true, Fetched: false, Notice: planUsageUnfetchedNotice}, nil
	}
	view := PlanUsageView{
		Available:  true,
		Fetched:    true,
		PlanLabel:  planLabel(usage.PlanType),
		ReceivedAt: usage.ReceivedAt.UTC().Format(time.RFC3339),
	}
	for _, w := range usage.Windows {
		item := PlanUsageWindowView{
			Label:              windowLabel(w),
			UsedPercent:        w.UsedPercent,
			WindowDurationMins: w.WindowDurationMins,
			WindowLabel:        windowDurationLabel(w.WindowDurationMins),
		}
		if !w.ResetsAt.IsZero() {
			item.ResetsAt = w.ResetsAt.UTC().Format(time.RFC3339)
		}
		view.Windows = append(view.Windows, item)
	}
	return view, nil
}

// planUsage は保持している残量を返す（通信を起こさない）。
func (a *API) planUsage(label string) (aiprovider.PlanUsage, bool) {
	adapter, err := a.providerAdapter(providerCodexID, label, string(aiprovider.AuthChatGPTSignin))
	if err != nil {
		return aiprovider.PlanUsage{}, false
	}
	reporter, ok := adapter.(aiprovider.PlanUsageReporter)
	if !ok {
		return aiprovider.PlanUsage{}, false
	}
	return reporter.PlanUsage()
}

// accountAdapter はサインインを持つアダプタを取り出す（型の判定。プロバイダ名で分岐しない）。
func (a *API) accountAdapter(label string) (aiprovider.AccountAdapter, error) {
	adapter, err := a.providerAdapter(providerCodexID, label, string(aiprovider.AuthChatGPTSignin))
	if err != nil {
		return nil, err
	}
	account, ok := adapter.(aiprovider.AccountAdapter)
	if !ok {
		return nil, errors.New("この AI プロバイダはアカウントでのサインインに対応していません。")
	}
	return account, nil
}

// providerAdapter は認証方式を指定してアダプタを生成する。
func (a *API) providerAdapter(providerID, label, authMethod string) (aiprovider.Adapter, error) {
	if !supportsAuthMethod(providerID, authMethod) {
		return nil, fmt.Errorf("この AI プロバイダでは選べない認証方式です。一覧から選んでください。")
	}
	// サインイン方式はキーへの参照名を持たない。
	ref := aiprovider.KeyRef("")
	if !usesSignIn(authMethod) {
		parsed, err := keyRef(providerID, label)
		if err != nil {
			return nil, err
		}
		ref = aiprovider.KeyRef(parsed.String())
	}
	return a.newAdapter(aiprovider.ProviderID(providerID), keyProvider{a.keys}, ref,
		a.adapterOptionsFor(authMethod))
}

// storedAuthMethod はアプリ設定に保存されている認証方式を返す（未保存はシークレットキー方式）。
func (a *API) storedAuthMethod(providerID, label string) string {
	settings, err := a.settings()
	if err != nil {
		return string(aiprovider.AuthSecretKey)
	}
	for _, p := range settings.Providers {
		if p.Provider == providerID && (label == "" || p.Label == label) {
			return p.AuthMethodOrDefault()
		}
	}
	return string(aiprovider.AuthSecretKey)
}

// requireSignedIn はサインイン方式での設定の確定・利用の前段。
//
// **分かっている範囲で「サインアウト済み」と言えるときだけ拒否する**（起動直後は不明であり、
// その場合は使える前提で進めて、失効は AI 呼び出しのエラーで気づく）。
func (a *API) requireSignedIn() error {
	a.codexSignIn.mu.Lock()
	defer a.codexSignIn.mu.Unlock()
	if a.codexSignIn.signedIn != nil && !*a.codexSignIn.signedIn {
		return errors.New("ChatGPT のアカウントにサインインしていません。設定でサインインしてください。")
	}
	return nil
}

// signedOutKnown は「サインアウト済みと分かっている」かを返す（AI 機能の可否の判定に使う）。
func (a *API) signedOutKnown() bool {
	a.codexSignIn.mu.Lock()
	defer a.codexSignIn.mu.Unlock()
	return a.codexSignIn.signedIn != nil && !*a.codexSignIn.signedIn
}

// ---------------------------------------------------------------------------
// ラベル（**生のコード値を画面へ出さない**。未知は「不明」）
// ---------------------------------------------------------------------------

// planType はプラン種別（値の一覧は同梱版の生成スキーマ `PlanType` の実測）。
type planType string

const (
	planFree                        planType = "free"
	planGo                          planType = "go"
	planPlus                        planType = "plus"
	planPro                         planType = "pro"
	planProLite                     planType = "prolite"
	planTeam                        planType = "team"
	planSelfServeBusinessUsageBased planType = "self_serve_business_usage_based"
	planBusiness                    planType = "business"
	planEnterpriseCBPUsageBased     planType = "enterprise_cbp_usage_based"
	planEnterprise                  planType = "enterprise"
	planEdu                         planType = "edu"
	planUnknown                     planType = "unknown"
)

// allPlanTypes は網羅を強制するための一覧（新しい値を足したら planLabels / personalPlans も足す。
// 漏れは単体テストが機械検知する）。
var allPlanTypes = []planType{
	planFree, planGo, planPlus, planPro, planProLite, planTeam,
	planSelfServeBusinessUsageBased, planBusiness, planEnterpriseCBPUsageBased,
	planEnterprise, planEdu, planUnknown,
}

// labelUnknown は取得できない・未知の値の表示（生のコード値を出さない）。
const labelUnknown = "不明"

var planLabels = map[planType]string{
	planFree:                        "Free",
	planGo:                          "Go",
	planPlus:                        "Plus",
	planPro:                         "Pro",
	planProLite:                     "Pro Lite",
	planTeam:                        "Team",
	planSelfServeBusinessUsageBased: "Business（従量課金）",
	planBusiness:                    "Business",
	planEnterpriseCBPUsageBased:     "Enterprise（従量課金）",
	planEnterprise:                  "Enterprise",
	planEdu:                         "Edu",
	planUnknown:                     labelUnknown,
}

// personalPlans は個人向けプラン（学習利用の注意喚起の対象）。
//
// **取得できない場合・未知の値も対象に含める**（安全側）。
var personalPlans = map[planType]bool{
	planFree: true, planGo: true, planPlus: true, planPro: true, planProLite: true,
}

// trainingNotice は個人向けプランでの学習利用の注意喚起と停止方法。
const trainingNotice = "このプランでは、送信内容が OpenAI のモデルの学習に使われることがあります。" +
	"ChatGPT の設定「データコントロール」の「Improve the model for everyone」をオフにするか、" +
	"プライバシーポータルで「Do not train on my content」を選ぶと停止できます（どちらか一方で足ります）。"

// planLabel はプラン種別のラベルを返す（未知・未取得は「不明」）。
func planLabel(code string) string {
	if label, ok := planLabels[planType(code)]; ok {
		return label
	}
	return labelUnknown
}

// needsTrainingNotice は学習利用の注意喚起を出すかを返す（未知・未取得も出す）。
func needsTrainingNotice(code string) bool {
	if _, known := planLabels[planType(code)]; !known {
		return true // 取得できなかった・知らない値は個人向けとみなす（安全側）
	}
	if planType(code) == planUnknown {
		return true
	}
	return personalPlans[planType(code)]
}

// accountType は認証の種類（値の一覧は生成スキーマ `Account` の実測）。
type accountType string

const (
	accountAPIKey        accountType = "apiKey"
	accountChatGPT       accountType = "chatgpt"
	accountAmazonBedrock accountType = "amazonBedrock"
)

// allAccountTypes は網羅を強制するための一覧（漏れは単体テストが機械検知する）。
var allAccountTypes = []accountType{accountAPIKey, accountChatGPT, accountAmazonBedrock}

var accountLabels = map[accountType]string{
	accountAPIKey:        "シークレットキー",
	accountChatGPT:       "ChatGPT のアカウント",
	accountAmazonBedrock: "Amazon Bedrock",
}

// accountLabel はアカウントの種類のラベルを返す（未知は「不明」）。
func accountLabel(code string) string {
	if label, ok := accountLabels[accountType(code)]; ok {
		return label
	}
	return labelUnknown
}

// accountView はアカウントの情報を表示内容へ写す（生のコード値を載せない）。
func accountView(account aiprovider.AccountInfo) AccountView {
	view := AccountView{
		Email:        account.Email,
		PlanLabel:    planLabel(account.PlanType),
		AccountLabel: accountLabel(account.Type),
	}
	if needsTrainingNotice(account.PlanType) {
		view.TrainingNotice = trainingNotice
	}
	return view
}

// windowLabel は枠の名前を返す（Codex が名前を返さないときは区分から作る）。
func windowLabel(w aiprovider.PlanUsageWindow) string {
	if w.LimitName != "" {
		return w.LimitName
	}
	if w.Scope == "secondary" {
		return "追加の利用枠"
	}
	return "利用枠"
}

// windowDurationLabel は枠の長さの表示用ラベル（0 = 不明は空）。
//
// **枠の長さはプランで異なる**ため、特定の長さを前提にした表記をしない。
func windowDurationLabel(mins int) string {
	switch {
	case mins <= 0:
		return ""
	case mins%(60*24) == 0:
		return fmt.Sprintf("%d 日", mins/(60*24))
	case mins%60 == 0:
		return fmt.Sprintf("%d 時間", mins/60)
	default:
		return fmt.Sprintf("%d 分", mins)
	}
}
