package binding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/updater"
)

// 更新の進行状態（自動更新と、画面の更新バナーの表示）。
//
// 画面はこの値だけで表示を決める（画面側で更新の可否を判定しない）。
const (
	// UpdateIdle は更新に関する表示が何も無い状態。
	UpdateIdle = "idle"
	// UpdateChecking は更新確認中。
	UpdateChecking = "checking"
	// UpdateAvailable は新版があり、利用者の同意を待っている状態。
	UpdateAvailable = "available"
	// UpdateDownloading は配布物を取得中。
	UpdateDownloading = "downloading"
	// UpdateVerifying は完全性を検証中。
	UpdateVerifying = "verifying"
	// UpdateReady は適用が終わり再起動を待っている状態。
	UpdateReady = "restart_required"
	// UpdateFailed は失敗して現行版のまま止まっている状態。
	UpdateFailed = "failed"
)

// UpdateState は更新の状態（画面へ渡す唯一の形）。
//
// **配布物 URL・ハッシュ・鍵素材・内部のエラーコードを含めない**
// （利用者に要るのは原因と次の行動だけで、秘密情報や内部の値は画面にもログにも出さない）。
type UpdateState struct {
	// Status は上の定数のいずれか。
	Status string `json:"status"`
	// CurrentVersion は現在動いている版。
	CurrentVersion string `json:"currentVersion"`
	// NewVersion は公開されている新版（Status が available 以降のときのみ）。
	NewVersion string `json:"newVersion,omitempty"`
	// Message は利用者向けの 1 文（原因＋次に取る行動）。失敗時のみ。
	Message string `json:"message,omitempty"`
}

// updateState はアプリ内で保持する更新の状態。
type updateState struct {
	mu sync.Mutex
	// state は画面へ返す現在の状態。
	state UpdateState
	// result は確認結果（同意後の取得に使う）。
	result updater.Result
	// deferredVersion は利用者が「後で」を選んだ版。
	// 同じ版の通知をこのアプリ実行中は再表示しない。
	deferredVersion string
	// cancel は取得中の中止。
	cancel context.CancelFunc
	// checkFn / newApplierFn / restartFn はテストのみが差し替える（公開の差し替え経路は無い）。
	checkFn      func(ctx context.Context) (updater.Result, error)
	newApplierFn func() (updateApplier, error)
	restartFn    func() error
}

// UpdateState は現在の更新状態を返す（画面の描画用）。
func (a *API) UpdateState() UpdateState {
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	return a.currentStateLocked()
}

// CheckForUpdate は更新を確認する（新版の有無を調べ、あれば通知する）。
//
// **回答モードでは何もしない**（更新通知・更新操作は担当者モードのみ）。
//
// **確認の失敗は画面へ出さない**。設計は
// 「**新版があれば**利用者へ通知」であり、確認の失敗の通知は求めていない。
// 本確認は起動時に自動で走る背景処理で、利用者は何も頼んでいない。加えて、
// 失敗の文言は「再実行してください」だが**再実行する導線が画面に無い**ため、
// 次に取る行動を示せない（エラーは「原因＋次の行動」で示すという様式を満たさない）。
// **利用者が「更新する」を押した後の取得・検証・適用の失敗は従来どおり表示する**
// （ApplyUpdate 側。頼んだ操作の結果は必ず返す）。
// 画面に出さないことと記録しないことは別であり、**失敗は動作ログに残す**。
func (a *API) CheckForUpdate() UpdateState {
	if a.StartupMode().Mode != ModeOwner {
		return UpdateState{Status: UpdateIdle, CurrentVersion: Version}
	}
	a.update.mu.Lock()
	if a.update.state.Status == UpdateChecking || a.update.state.Status == UpdateDownloading ||
		a.update.state.Status == UpdateVerifying || a.update.state.Status == UpdateReady {
		st := a.update.state
		a.update.mu.Unlock()
		st.CurrentVersion = Version
		return st
	}
	a.update.state = UpdateState{Status: UpdateChecking}
	check := a.update.checkFn
	a.update.mu.Unlock()

	if check == nil {
		check = defaultCheck
	}
	res, err := check(a.context())

	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	if err != nil {
		// 記録は残し、画面へは出さない（理由は CheckForUpdate の説明を参照）。
		a.log.Warn("update.check_failed", "更新の確認に失敗しました（画面へは出しません）",
			applog.F("reason", updateMessage(err)))
		a.update.state = UpdateState{Status: UpdateIdle}
		return a.currentStateLocked()
	}
	if !res.Available || res.Version == a.update.deferredVersion {
		// 新版が無い、または利用者が「後で」を選んだ版。通知しない。
		a.update.state = UpdateState{Status: UpdateIdle}
		return a.currentStateLocked()
	}
	a.update.result = res
	a.update.state = UpdateState{Status: UpdateAvailable, NewVersion: res.Version}
	return a.currentStateLocked()
}

// DeferUpdate は「後で」の選択。このアプリ実行中は同じ版の通知を再表示しない。
func (a *API) DeferUpdate() UpdateState {
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	if a.update.state.NewVersion != "" {
		a.update.deferredVersion = a.update.state.NewVersion
	}
	a.update.state = UpdateState{Status: UpdateIdle}
	return a.currentStateLocked()
}

// ApplyUpdate は利用者の同意を受けて取得・検証・適用する。
//
// **同意操作（本メソッドの呼び出し）の前に取得・適用は始まらない**。
// 成功しても再起動はしない（利用者の操作で再起動する = RestartApp）。
func (a *API) ApplyUpdate() UpdateState {
	a.update.mu.Lock()
	if a.update.state.Status != UpdateAvailable {
		// 同意できる状態でなければ何もしない（画面の二度押し・状態ずれの保護）。
		st := a.currentStateLocked()
		a.update.mu.Unlock()
		return st
	}
	res := a.update.result
	ctx, cancel := context.WithCancel(a.context())
	a.update.cancel = cancel
	a.update.state = UpdateState{Status: UpdateDownloading, NewVersion: res.Version}
	newApplier := a.update.newApplierFn
	a.update.mu.Unlock()
	defer cancel()

	if newApplier == nil {
		newApplier = defaultApplier
	}
	applied, err := a.runUpdate(ctx, res, newApplier)

	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	a.update.cancel = nil
	if err != nil {
		a.update.state = UpdateState{Status: UpdateFailed, NewVersion: res.Version, Message: updateMessage(err)}
		return a.currentStateLocked()
	}
	a.update.state = UpdateState{Status: UpdateReady, NewVersion: applied.Version}
	return a.currentStateLocked()
}

// runUpdate は取得 → 検証・差し替え の順に進め、その境目で進行状態を進める。
//
// 状態の遷移は本層が持つ（アップデータ側は進行状態を知らない）。
// 失敗したときは作業場所を片づけてから誤りを返す。
func (a *API) runUpdate(ctx context.Context, res updater.Result,
	newApplier func() (updateApplier, error)) (updater.Applied, error) {
	ap, err := newApplier()
	if err != nil {
		return updater.Applied{}, err
	}
	path, err := ap.Download(ctx, res.Asset)
	if err != nil {
		_ = ap.Cleanup()
		return updater.Applied{}, err
	}
	// 取得が終わり、完全性検証を経て差し替えに入る段。
	a.setUpdateStatus(UpdateVerifying)
	applied, err := ap.Apply(path, res.Version)
	if err != nil {
		_ = ap.Cleanup()
		return updater.Applied{}, err
	}
	return applied, nil
}

// setUpdateStatus は進行中の状態だけを進める（失敗・完了は呼び出し元が設定する）。
func (a *API) setUpdateStatus(status string) {
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	if a.update.state.Status == UpdateDownloading || a.update.state.Status == UpdateVerifying {
		a.update.state.Status = status
	}
}

// RestartApp は更新後の再起動を行う（利用者の操作でのみ呼ばれる）。
//
// 適用が終わっていない状態では何もしない（無断で終了しない）。
func (a *API) RestartApp() UpdateState {
	a.update.mu.Lock()
	if a.update.state.Status != UpdateReady {
		st := a.currentStateLocked()
		a.update.mu.Unlock()
		return st
	}
	restart := a.update.restartFn
	a.update.mu.Unlock()

	if restart == nil {
		restart = defaultRestart
	}
	if err := restart(); err != nil {
		a.update.mu.Lock()
		defer a.update.mu.Unlock()
		a.update.state.Message = "アプリを再起動できませんでした。手動で起動しなおしてください。"
		return a.currentStateLocked()
	}
	return a.UpdateState()
}

// CancelUpdate は取得中の中止。
func (a *API) CancelUpdate() UpdateState {
	a.update.mu.Lock()
	cancel := a.update.cancel
	a.update.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	if a.update.state.Status == UpdateDownloading || a.update.state.Status == UpdateVerifying {
		a.update.state = UpdateState{Status: UpdateIdle}
	}
	return a.currentStateLocked()
}

func (a *API) currentStateLocked() UpdateState {
	st := a.update.state
	if st.Status == "" {
		// ゼロ値を画面へ渡さない（画面のラベル対応表を網羅強制するため）。
		st.Status = UpdateIdle
	}
	st.CurrentVersion = Version
	return st
}

// updateMessage は更新の誤りを利用者向けの 1 文へ写す。
//
// `*updater.Error` は利用者向け文言と診断用詳細を分けて持つ。ここでは前者だけを取り出し、
// 想定外の誤りは既定の 1 文へ倒す（生のエラー文字列を画面へ出さない）。
func updateMessage(err error) string {
	if e, ok := err.(*updater.Error); ok {
		return e.Message() + "。"
	}
	return "更新に失敗しました。しばらく待って再実行してください。"
}

// defaultCheck は本番の更新確認。
func defaultCheck(ctx context.Context) (updater.Result, error) {
	c, err := updater.NewChecker()
	if err != nil {
		return updater.Result{}, err
	}
	return c.Check(ctx)
}

// updateApplier は取得・適用の担い手（本番は *updater.Applier）。
//
// バインディングが状態遷移を持つため、担い手側は「取得」と「適用」の 2 段だけを提供する。
type updateApplier interface {
	Download(ctx context.Context, asset updater.Asset) (string, error)
	Apply(archivePath, version string) (updater.Applied, error)
	Cleanup() error
}

// defaultApplier は本番の担い手を作る（置換対象は現在動いているアプリ本体）。
func defaultApplier() (updateApplier, error) {
	target, err := installedAppPath()
	if err != nil {
		return nil, err
	}
	return updater.NewApplier(target)
}

// installedAppPath は置換対象（macOS は .app バンドル、Windows は実行ファイル）を返す。
func installedAppPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		// <...>/ReqWeave.app/Contents/MacOS/ReqWeave → <...>/ReqWeave.app
		if idx := strings.Index(exe, ".app"+string(filepath.Separator)); idx >= 0 {
			return exe[:idx+len(".app")], nil
		}
	}
	return exe, nil
}

// defaultRestart は更新後の再起動。新しい版を起動してから現在のプロセスを終える。
func defaultRestart() error {
	target, err := installedAppPath()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("/usr/bin/open", "-n", target)
	} else {
		cmd = exec.Command(target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// 新しい版の起動を待たずに終える（待つと二重起動の判定に引っかかる環境がある）。
	go func() { _ = cmd.Process.Release() }()
	os.Exit(0)
	return nil
}
