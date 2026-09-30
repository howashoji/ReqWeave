package codex

// 本ファイルは ChatGPT のアカウントでのサインインとプランの残量。
//
// 本アダプタは抽象化層の任意のインタフェース（aiprovider.AccountAdapter / aiprovider.PlanUsageReporter）を
// 実装する。**認可の URL は返すだけで、既定のブラウザで開くのはバインディング層**（外部のプログラムを起動する層を分ける）。
// 残量は**このパッケージのメモリにだけ**置き、本システムのファイルへ書かない。
//
// JSON の形は同梱版（bundle.json の版）の生成スキーマで確認した実際の形に合わせている
// （LoginAccountResponse / AccountLoginCompletedNotification / GetAccountResponse /
//  GetAccountRateLimitsResponse / RateLimitSnapshot）。

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

// サインインの待ち受けの上限。
//
// Codex 自身が 15 分で `account/login/completed`（`success: false`）を返す（実測）。
// 本システム側にも同じ時間の上限を置き、通知が届かないまま待ち続ける状態を作らない。
// **パッケージ内のテストのみが短く差し替える**（待ち時間そのものではなく、時間が来たときの振る舞いを確かめるため）。
var signInWait = 15 * time.Minute

// 動作ログのイベント名（記録するのはサインインの開始・結果に限り、
// 認可の URL・メールアドレス・トークンは記録しない）。
const (
	eventSignInStarted   = "ai.codex_signin_started"
	eventSignInCompleted = "ai.codex_signin_completed"
	eventSignedOut       = "ai.codex_signed_out"
)

// 本ファイル固有の Code（画面の文言はエラーカタログが持つ）。
const (
	// codeSignInTimedOut はサインインが時間内に完了しなかった状態（設定は変えない）。
	codeSignInTimedOut = "signin_timed_out"
	// codeSignInUnavailable はサインインの待ち受けを始められない状態。
	codeSignInUnavailable = "signin_unavailable"
	// codeUnauthorized は未サインイン・サインインの失効（errors.go の unauthorized と同じ Code）。
	codeUnauthorized = "unauthorized"
)

// ---------------------------------------------------------------------------
// サインインの待ち受け（`account/login/completed` の受け口）
// ---------------------------------------------------------------------------

// loginResult はサインインの結末。
type loginResult struct {
	success bool
	// message は Codex が返した失敗の理由（画面の文言には使わない）。
	message string
	// err は本システム側で起きたこと（子プロセスの終了）。
	err error
}

// loginRegistry は進行中のサインインの待ち合わせ。
//
// バインディングは「開始」と「待ち受け」を別の呼び出しで行い、そのたびにアダプタの実体が
// 作り直される（公開バインディング層は呼び出しごとにアダプタを生成する）ため、
// 待ち合わせはアダプタの構造体ではなく**パッケージのメモリ**に置く。
type loginRegistry struct {
	mu      sync.Mutex
	waiters map[string]*loginWaiter
	// early は待ち合わせ口が作られる前に届いた完了（通知が先着する競合への備え）。
	early map[string]loginResult
}

// loginWaiter は 1 件のサインインの待ち合わせ口。
//
// **結果を渡した後も口を残す**。渡した時点で消すと、開始（StartSignIn）と
// 待ち受け（WaitSignIn）の間に完了が届いたときに、WaitSignIn が空の口を作り直して
// 結果を取りこぼす（15 分の上限まで止まる）。口を捨てるのは forget（待ち受けの終了・取り消し）。
type loginWaiter struct {
	ch chan loginResult
	// delivered は結果を渡したか（二重に送るとバッファ 1 の口で詰まる）。
	delivered bool
}

var loginTracker = &loginRegistry{
	waiters: map[string]*loginWaiter{},
	early:   map[string]loginResult{},
}

// register は待ち合わせ口を作る（既に完了が届いていれば、それを持った口を返す）。
//
// 開始（StartSignIn）と待ち受け（WaitSignIn）は別の呼び出しであり、どちらからも呼ばれる。
// **既に口があるときは同じ口を返す**（作り直すと、開始から待ち受けまでの間に届いた完了を落とす）。
func (r *loginRegistry) register(loginID string) <-chan loginResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.waiters[loginID]; ok {
		return w.ch
	}
	w := &loginWaiter{ch: make(chan loginResult, 1)}
	if res, ok := r.early[loginID]; ok {
		delete(r.early, loginID)
		w.ch <- res
		w.delivered = true
	}
	r.waiters[loginID] = w
	return w.ch
}

// complete は完了を待ち手へ渡す（待ち手がまだ無ければ保持する）。
//
// loginID が空の通知（スキーマ上 null を取りうる）は、待っている 1 件へ渡す。
func (r *loginRegistry) complete(loginID string, res loginResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if loginID == "" {
		for _, w := range r.waiters {
			if w.delivered {
				continue
			}
			w.ch <- res
			w.delivered = true
			return
		}
		return
	}
	if w, ok := r.waiters[loginID]; ok {
		if w.delivered {
			return
		}
		w.ch <- res
		w.delivered = true
		return
	}
	r.early[loginID] = res
}

// forget は待ち合わせ口を捨てる（取り消し・待ちの打ち切り）。
func (r *loginRegistry) forget(loginID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.waiters, loginID)
	delete(r.early, loginID)
}

// failAll は待っているすべてを失敗させる（子プロセスが終わったとき）。
func (r *loginRegistry) failAll(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.waiters {
		if w.delivered {
			continue
		}
		w.ch <- loginResult{err: err}
		w.delivered = true
	}
}

// handleLoginCompleted は `account/login/completed` の通知を待ち手へ渡す。
func handleLoginCompleted(params json.RawMessage) {
	var body struct {
		LoginID *string `json:"loginId"`
		Success bool    `json:"success"`
		Error   *string `json:"error"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return
	}
	res := loginResult{success: body.Success}
	if body.Error != nil {
		res.message = *body.Error
	}
	loginID := ""
	if body.LoginID != nil {
		loginID = *body.LoginID
	}
	loginTracker.complete(loginID, res)
}

// ---------------------------------------------------------------------------
// プランの残量（メモリのみ）
// ---------------------------------------------------------------------------

// usageStore は残量の最新値。**本システムのファイルへ書かない**。
type usageStore struct {
	mu    sync.Mutex
	usage aiprovider.PlanUsage
	ok    bool
}

var planUsageStore = &usageStore{}

func (s *usageStore) set(u aiprovider.PlanUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage, s.ok = u, true
}

func (s *usageStore) get() (aiprovider.PlanUsage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage, s.ok
}

// clear はサインアウト時に捨てる（前のアカウントの残量を出し続けない）。
func (s *usageStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage, s.ok = aiprovider.PlanUsage{}, false
}

// nowFn は受け取った時刻（パッケージ内のテストのみが差し替える）。
var nowFn = func() time.Time { return time.Now().UTC() }

// rateLimitWindow は Codex の `RateLimitWindow`。
type rateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

// rateLimitSnapshot は Codex の `RateLimitSnapshot`（1 つの枠）。
type rateLimitSnapshot struct {
	LimitID     *string          `json:"limitId"`
	LimitName   *string          `json:"limitName"`
	PlanType    *string          `json:"planType"`
	Primary     *rateLimitWindow `json:"primary"`
	Secondary   *rateLimitWindow `json:"secondary"`
	ReachedType *string          `json:"rateLimitReachedType"`
}

// rateLimitsPayload は `account/rateLimits/read` の応答と `account/rateLimits/updated` の通知に共通の形。
//
// 通知は `rateLimits` だけを持ち、応答は複数の枠（`rateLimitsByLimitId`）も持つ。
type rateLimitsPayload struct {
	RateLimits rateLimitSnapshot            `json:"rateLimits"`
	ByLimitID  map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
}

// parsePlanUsage は残量を読む。枠が 1 つも無ければ ok = false
// （シークレットキー方式では `limitId` 以外がすべて null で届く。実測）。
func parsePlanUsage(raw json.RawMessage) (aiprovider.PlanUsage, bool) {
	var payload rateLimitsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return aiprovider.PlanUsage{}, false
	}
	snapshots := []rateLimitSnapshot{payload.RateLimits}
	if len(payload.ByLimitID) > 0 {
		// 複数の枠があるときはそちらを使う（枠の本数はプランで変わる）。
		keys := make([]string, 0, len(payload.ByLimitID))
		for k := range payload.ByLimitID {
			keys = append(keys, k)
		}
		sort.Strings(keys) // 表示の順を毎回同じにする
		snapshots = snapshots[:0]
		for _, k := range keys {
			snap := payload.ByLimitID[k]
			if snap.LimitID == nil {
				id := k
				snap.LimitID = &id
			}
			snapshots = append(snapshots, snap)
		}
	}

	usage := aiprovider.PlanUsage{ReceivedAt: nowFn()}
	for _, snap := range snapshots {
		if snap.PlanType != nil && usage.PlanType == "" {
			usage.PlanType = *snap.PlanType
		}
		if snap.ReachedType != nil && usage.ReachedType == "" {
			usage.ReachedType = *snap.ReachedType
		}
		usage.Windows = append(usage.Windows,
			planWindows(snap, "primary", snap.Primary)...)
		usage.Windows = append(usage.Windows,
			planWindows(snap, "secondary", snap.Secondary)...)
	}
	if len(usage.Windows) == 0 {
		return aiprovider.PlanUsage{}, false
	}
	return usage, true
}

func planWindows(snap rateLimitSnapshot, scope string, w *rateLimitWindow) []aiprovider.PlanUsageWindow {
	if w == nil {
		return nil
	}
	out := aiprovider.PlanUsageWindow{Scope: scope, UsedPercent: w.UsedPercent}
	if snap.LimitID != nil {
		out.LimitID = *snap.LimitID
	}
	if snap.LimitName != nil {
		out.LimitName = *snap.LimitName
	}
	if w.WindowDurationMins != nil {
		out.WindowDurationMins = int(*w.WindowDurationMins)
	}
	if w.ResetsAt != nil {
		out.ResetsAt = time.Unix(*w.ResetsAt, 0).UTC()
	}
	return []aiprovider.PlanUsageWindow{out}
}

// handleRateLimitsUpdated は `account/rateLimits/updated` の通知を保持する（上位へのイベントにはしない）。
func handleRateLimitsUpdated(params json.RawMessage) {
	if usage, ok := parsePlanUsage(params); ok {
		planUsageStore.set(usage)
	}
}

// PlanUsage は保持している残量を返す（aiprovider.PlanUsageReporter）。
//
// **取得のための通信を起こさない**（閲覧の操作では取得しない）。
// シークレットキー方式では値が届かないため常に未取得を返す（同 受け入れ条件6）。
func (a *Adapter) PlanUsage() (aiprovider.PlanUsage, bool) {
	if a.auth != authChatGPTSignin {
		return aiprovider.PlanUsage{}, false
	}
	return planUsageStore.get()
}

// ---------------------------------------------------------------------------
// サインイン・サインアウト・アカウントの情報
// ---------------------------------------------------------------------------

// StartSignIn はサインインを開始し、認可の URL を返す（aiprovider.AccountAdapter）。
//
// **ブラウザは開かない**（開くのはバインディング層）。
// 子プロセスの起動と起動後の検査に合格してから `account/login/start` を送る。
func (a *Adapter) StartSignIn(ctx context.Context) (aiprovider.SignIn, error) {
	if a.auth != authChatGPTSignin {
		return aiprovider.SignIn{}, permanentError(codeInvalidRequest,
			"この認証方式ではサインインを使いません")
	}
	connectCtx, cancel := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancel()

	proc, release, err := procManager.begin(connectCtx, a.processKey(), a.keys, a.rec)
	if err != nil {
		return aiprovider.SignIn{}, err
	}
	defer release()

	raw, err := proc.rpc.call(connectCtx, "account/login/start", map[string]any{"type": "chatgpt"})
	if err != nil {
		return aiprovider.SignIn{}, signInUnavailable(proc.callError(err))
	}
	var result struct {
		Type    string `json:"type"`
		LoginID string `json:"loginId"`
		AuthURL string `json:"authUrl"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.LoginID == "" || result.AuthURL == "" {
		return aiprovider.SignIn{}, configError(codeSignInUnavailable,
			"Codex がサインインの認可 URL を返しませんでした")
	}
	// 待ち合わせ口は**要求を送った直後**に作る（完了の通知が先着しても取りこぼさない）。
	loginTracker.register(result.LoginID)
	// 記録するのは開始した事実だけ（認可の URL は記録しない）。
	a.rec.Info(eventSignInStarted, "ChatGPT のアカウントでのサインインを開始しました", nil)
	return aiprovider.SignIn{LoginID: result.LoginID, AuthorizationURL: result.AuthURL}, nil
}

// signInUnavailable は待ち受けを始められない状態へ倒す（エラーカタログの様式に対応する Code）。
func signInUnavailable(err error) *aiprovider.ProviderError {
	pErr := toProviderError(err)
	return configError(codeSignInUnavailable, pErr.Message)
}

// WaitSignIn は `account/login/completed` を待ち、成功時はアカウントの情報と残量まで取得する。
//
//   - 時間切れ（Codex は 15 分で失敗を返す）と失敗は設定起因のエラーにする。
//     **設定は変えない**（変えないのは呼び出し側 = バインディングの責務）。
//   - 成功時に `account/rateLimits/read` を**1 回だけ**呼ぶ
//     （以後は `account/rateLimits/updated` でのみ更新する）。
func (a *Adapter) WaitSignIn(ctx context.Context, loginID string) (aiprovider.AccountInfo, error) {
	if a.auth != authChatGPTSignin {
		return aiprovider.AccountInfo{}, permanentError(codeInvalidRequest,
			"この認証方式ではサインインを使いません")
	}
	if loginID == "" {
		return aiprovider.AccountInfo{}, configError(codeSignInUnavailable,
			"サインインの待ち受けが始まっていません")
	}
	wait := loginTracker.register(loginID)
	defer loginTracker.forget(loginID)

	timer := time.NewTimer(signInWait)
	defer timer.Stop()

	select {
	case res := <-wait:
		switch {
		case res.err != nil:
			a.recordSignInResult(false)
			return aiprovider.AccountInfo{}, transientError(codeProcessExited,
				"サインインの途中で Codex App Server が終了しました")
		case !res.success:
			a.recordSignInResult(false)
			return aiprovider.AccountInfo{}, configError(codeSignInTimedOut, res.message)
		}
	case <-timer.C:
		// 通知が届かないまま上限を超えた（Codex 側の待ち受けも終わっている）。
		a.cancelQuietly(loginID)
		a.recordSignInResult(false)
		return aiprovider.AccountInfo{}, configError(codeSignInTimedOut,
			"サインインの完了を待てる時間を過ぎました")
	case <-ctx.Done():
		// 利用者の取り消し（エラーとして表示しない）。
		a.cancelQuietly(loginID)
		return aiprovider.AccountInfo{}, ctx.Err()
	}

	account, err := a.Account(ctx)
	if err != nil {
		a.recordSignInResult(false)
		return aiprovider.AccountInfo{}, err
	}
	if !account.SignedIn {
		a.recordSignInResult(false)
		return aiprovider.AccountInfo{}, configError(codeUnauthorized,
			"サインインの完了後もアカウントを確認できませんでした")
	}
	// **1 回だけ**の残量取得（失敗しても疎通確認の成否は変えない）。
	a.readPlanUsageOnce(ctx)
	a.recordSignInResult(true)
	return account, nil
}

// recordSignInResult は成否だけを動作ログへ残す（メールアドレス・プラン種別は残さない）。
func (a *Adapter) recordSignInResult(ok bool) {
	result := "failed"
	if ok {
		result = "succeeded"
	}
	a.rec.Info(eventSignInCompleted, "ChatGPT のアカウントでのサインインが終わりました",
		map[string]string{"result": result})
}

// readPlanUsageOnce はサインインの完了時の 1 回だけの残量取得。
func (a *Adapter) readPlanUsageOnce(ctx context.Context) {
	proc := procManager.current()
	if proc == nil || proc.isDead() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancel()
	raw, err := proc.rpc.call(callCtx, "account/rateLimits/read", map[string]any{})
	if err != nil {
		return // 残量は参考値であり、取得できなくてもサインインは成立している
	}
	if usage, ok := parsePlanUsage(raw); ok {
		planUsageStore.set(usage)
	}
}

// cancelQuietly は待ち受けの取り消しを送る（結果は見ない）。
func (a *Adapter) cancelQuietly(loginID string) {
	ctx, cancel := context.WithTimeout(context.Background(), interruptGrace)
	defer cancel()
	_ = a.CancelSignIn(ctx, loginID)
}

// CancelSignIn は待ち受けを取り消す（aiprovider.AccountAdapter）。
func (a *Adapter) CancelSignIn(ctx context.Context, loginID string) error {
	loginTracker.forget(loginID)
	proc := procManager.current()
	if proc == nil || proc.isDead() {
		return nil // 待ち受けている子プロセスがもういない = 取り消し済みと同じ
	}
	if _, err := proc.rpc.call(ctx, "account/login/cancel", map[string]any{"loginId": loginID}); err != nil {
		return toProviderError(proc.callError(err))
	}
	return nil
}

// SignOut はサインアウトする（aiprovider.AccountAdapter）。
//
// Codex の項目が OS セキュアストレージから消える（実測）。本システムは
// 認証情報に触れないため、ここで消すものは持たない（保持している残量だけを捨てる）。
func (a *Adapter) SignOut(ctx context.Context) error {
	if a.auth != authChatGPTSignin {
		return permanentError(codeInvalidRequest, "この認証方式ではサインアウトを使いません")
	}
	connectCtx, cancel := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancel()

	proc, release, err := procManager.begin(connectCtx, a.processKey(), a.keys, a.rec)
	if err != nil {
		return err
	}
	defer release()

	if _, err := proc.rpc.call(connectCtx, "account/logout", map[string]any{}); err != nil {
		return toProviderError(proc.callError(err))
	}
	planUsageStore.clear()
	a.rec.Info(eventSignedOut, "ChatGPT のアカウントからサインアウトしました", nil)
	return nil
}

// Account は現在のアカウントの情報を返す（aiprovider.AccountAdapter）。
func (a *Adapter) Account(ctx context.Context) (aiprovider.AccountInfo, error) {
	connectCtx, cancel := context.WithTimeout(ctx, a.timeouts.Connect)
	defer cancel()

	proc, release, err := procManager.begin(connectCtx, a.processKey(), a.keys, a.rec)
	if err != nil {
		return aiprovider.AccountInfo{}, err
	}
	defer release()

	return proc.readAccount(connectCtx)
}

// readAccount は `account/read` を呼んでアカウントの情報を返す。
func (p *process) readAccount(ctx context.Context) (aiprovider.AccountInfo, error) {
	raw, err := p.rpc.call(ctx, "account/read", map[string]any{})
	if err != nil {
		return aiprovider.AccountInfo{}, toProviderError(p.callError(err))
	}
	var body struct {
		Account *struct {
			Type     string `json:"type"`
			PlanType string `json:"planType"`
			Email    string `json:"email"`
		} `json:"account"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return aiprovider.AccountInfo{}, permanentError(codeUnexpected, "Codex の応答を解釈できません")
	}
	if body.Account == nil {
		return aiprovider.AccountInfo{SignedIn: false}, nil
	}
	return aiprovider.AccountInfo{
		SignedIn: true,
		Type:     body.Account.Type,
		PlanType: body.Account.PlanType,
		Email:    body.Account.Email,
	}, nil
}

// アダプタが任意のインタフェースを実装していることを、コンパイル時に固定する。
var (
	_ aiprovider.AccountAdapter    = (*Adapter)(nil)
	_ aiprovider.PlanUsageReporter = (*Adapter)(nil)
)
