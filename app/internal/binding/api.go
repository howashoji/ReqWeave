package binding

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/appversion"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// AppName は画面表示・ウィンドウタイトルに用いるアプリ名。
const AppName = "ReqWeave"

// Version はアプリの版番号（自動更新の確認で使う「現行版」）。
// 正本は internal/appversion の VERSION ファイル 1 つだけで、埋め込みで解決する。
// ビルドフラグでの上書きはしない（正本と実行時値が食い違う経路を作らないため）。
var Version = appversion.Version()

// AppInfo はフロントエンドへ公開するアプリ情報。
// シークレットキーおよびその派生値を含めてはならない（画面側へキーを渡さない）。
type AppInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// API はフロントエンドへ公開する唯一の入口。
// 画面から呼べる操作はすべて本型のメソッドとして定義し、
// 権限判定とトークン上限判定は各メソッドの前段でここが行う（個々の画面に判定を分散させない）。
type API struct {
	ctx context.Context

	paths    projectstore.AppPaths
	pathsErr error
	keys     *keymanager.Manager
	// log は動作ログ。開けなかった端末では nil（記録しないだけで機能は止めない）。
	log *applog.Logger
	// syncKeys は同期先の認証情報の保管庫（AI のキーと同じ OS のセキュアストレージ・別サービス名）。
	// nil なら初回利用時に生成する（syncCredentials）。
	syncKeys *keymanager.SyncCredentials

	// background は背景で走る処理（成果物生成など）の待ち合わせ。
	//
	// プロジェクトを閉じる・アプリを終了するときは、**書き込みが終わってから**保存キューを閉じる。
	// 待たないと、ロックの解放やイベント送出が閉じた後に走り、
	// フォルダを消した直後に再作成される等の事故になる（結合テストで実測）。
	background sync.WaitGroup

	// mu は開いているプロジェクト（対話セッション）の排他。
	mu sync.Mutex
	// session は対話用に開いているプロジェクトと対話エンジン（同時に 1 件のみ）。
	session *dialogueSession

	// respond は回答モードで開いている質問票（担当者モードとは排他の経路）。
	respond respondState

	// openFile は OS の関連付け・ドラッグ・2 個目の起動から受け取ったファイル。
	openFile openFileState

	// update は自動更新の状態（全画面共通）。
	update updateState

	// codexSignIn は ChatGPT のアカウントでのサインインの状態。
	codexSignIn codexSignInState

	// newAdapter は AIプロバイダ抽象化層のアダプタ生成。既定はレジストリ。
	// テストのみが差し替える（公開 API・設定からの差し替え経路は無い）。
	newAdapter func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef, opts aiprovider.AdapterOptions) (aiprovider.Adapter, error)
}

// New は API を生成する。
func New() *API {
	paths, err := projectstore.DefaultAppPaths()
	api := &API{
		paths:      paths,
		pathsErr:   err,
		keys:       keymanager.New(),
		syncKeys:   keymanager.NewSyncCredentials(),
		newAdapter: aiprovider.NewAdapterWithOptions,
	}
	if err == nil {
		// 動作ログを開けないこと自体はアプリの利用を妨げない（動作ログは可用性の条件ではない）。
		// 開けなければ log は nil のままで、記録の呼び出しは無視される。
		if logger, logErr := applog.New(paths); logErr == nil {
			api.log = logger
		}
	}
	return api
}

// Log は動作ログの記録先を返す（nil でも呼び出せる = 記録しないロガー）。
func (a *API) Log() *applog.Logger { return a.log }

// onEffortDegrade は推論努力パラメータの縮退（パラメータを持たないモデルでは送信から省く）を動作ログへ残す。
//
// **利用者への通知はしない**（段階の意味はモデル選択・最大出力トークンの 2 写像で保たれるため）。
// 対話エンジン・ドキュメント生成の双方から同じ口を使う。
func (a *API) onEffortDegrade(model aiprovider.ModelInfo) {
	a.log.Info("ai.effort_degraded",
		"推論努力パラメータを持たないモデルのため、送信から省略しました",
		applog.F("model", model.ID), applog.F("tier", string(model.Tier)))
}

// onStreamPanic は AI ストリーミング用ゴルーチンのパニックを動作ログへ残す。
//
// 利用者へは抽象化層が恒久的エラー（aiprovider.CodeInternalPanic）として返すため、
// **画面へはパニックの内容を出さない**（内容とスタックが残るのは動作ログだけ）。
func (a *API) onStreamPanic(recovered any) {
	a.log.RecordPanic("ai.stream_panic", recovered)
}

// onAIEvent は子プロセス型アダプタ（外部の CLI を子プロセスとして起動するもの）の動作ログ記録口。
//
// 記録されるのは子プロセスの起動・終了・起動後の検査の結果・エラーの分類と Code に限られる
// （やり取りの本文＝発話本文は抽象化層が渡さない。動作ログに発話を残さないため）。
func (a *API) onAIEvent(level aiprovider.EventLevel, event, message string, fields map[string]string) {
	list := make([]applog.Field, 0, len(fields))
	for _, key := range sortedFieldKeys(fields) {
		list = append(list, applog.F(key, fields[key]))
	}
	if level == aiprovider.EventLevelWarn {
		a.log.Warn(event, message, list...)
		return
	}
	a.log.Info(event, message, list...)
}

// onAICallFailed は AI 呼び出しの失敗を動作ログへ記録する。
//
// 記録するのは AI 通信系のエラーの切り分けに要る項目だけ（分類・発生源・HTTPStatus・
// 正規化コード・再試行回数・所要時間）。**プロンプト・応答・発話・キーは記録しない**
// （aiprovider.FailureRecord がそれらを持たないことで構造的に担保している）。
//
// 画面には「原因＋次の行動」の 1 文しか出ず、回答モードでは
// 「システム担当者へ連絡してください」としか出ない（回答者は AI の設定に触れないため）。
// **本記録が失敗の切り分けの唯一の手掛かりになる**。
func (a *API) onAICallFailed(rec aiprovider.FailureRecord) {
	a.log.Warn(eventAICallFailed, "AI の呼び出しに失敗しました", aiFailureFields(rec)...)
}

// eventAICallFailed は AI 呼び出しの失敗の事象名（担当者モードと回答モードで同じ）。
const eventAICallFailed = "ai.call_failed"

// aiFailureFields は失敗の記録項目を組み立てる。
//
// **回答モードはこれに質問票 ID だけを足す**（回答モードで記録してよい項目を絞るため）。
// 項目の作り方を 1 か所に置き、モードごとに別の組み立てを持たせない。
func aiFailureFields(rec aiprovider.FailureRecord) []applog.Field {
	fields := []applog.Field{
		applog.F("provider", string(rec.Provider)),
		applog.F("class", rec.Class.String()),
		applog.F("attempts", strconv.Itoa(rec.Attempts)),
		applog.F("elapsed_ms", strconv.FormatInt(rec.Elapsed.Milliseconds(), 10)),
	}
	// 送信前に失敗すると呼び出し先からの応答が無く、状態もコードも無い（0 や空を残さない）。
	if rec.HTTPStatus != 0 {
		fields = append(fields, applog.F("http_status", strconv.Itoa(rec.HTTPStatus)))
	}
	if rec.Code != "" {
		fields = append(fields, applog.F("code", rec.Code))
	}
	return fields
}

// sortedFieldKeys は付随項目を毎回同じ順で記録するためのキー列。
func sortedFieldKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// aiSettingsChanged は AIプロバイダ・認証方式・キーの変更をアダプタへ伝える（子プロセスを止める時機の 1 つ）。
//
// 子プロセスを持つアダプタは、古い設定で動いている子プロセスをここで止める。
// 呼ぶ側はプロバイダを列挙しない（どのアダプタが子プロセスを持つかを知らない）。
func (a *API) aiSettingsChanged() {
	aiprovider.SettingsChanged(a.onAIEvent)
}

// context は Wails のランタイムコンテキストを返す（未起動時は Background）。
func (a *API) context() context.Context {
	if a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}

// settings は現在のアプリ設定を読む（未作成なら既定値）。
func (a *API) settings() (*projectstore.Settings, error) {
	if a.pathsErr != nil {
		return nil, fmt.Errorf("アプリ設定の保存先を特定できません: %w", a.pathsErr)
	}
	return projectstore.LoadSettings(a.paths)
}

// adapterOptions はアプリ設定のタイムアウト（ai_timeouts）を
// アダプタ生成オプションへ写す。未設定・範囲外の値は aiprovider 側で許容範囲へ丸まる。
func (a *API) adapterOptions() aiprovider.AdapterOptions {
	return a.adapterOptionsFor("")
}

// adapterOptionsFor は認証方式（アプリ設定の auth_method）を添えた生成オプションを返す。
//
// 認証方式を持つのは Codex App Server だけで、他のアダプタは無視する。
func (a *API) adapterOptionsFor(authMethod string) aiprovider.AdapterOptions {
	opts := aiprovider.AdapterOptions{
		OnPanic:    a.onStreamPanic,
		OnEvent:    a.onAIEvent,
		AuthMethod: aiprovider.NormalizeAuthMethod(aiprovider.AuthMethod(authMethod)),
	}
	settings, err := a.settings()
	if err != nil || settings.AITimeouts == nil {
		return opts
	}
	opts.Timeouts = aiprovider.Timeouts{
		Connect:  time.Duration(settings.AITimeouts.ConnectSeconds) * time.Second,
		Response: time.Duration(settings.AITimeouts.ResponseSeconds) * time.Second,
	}
	return opts
}

// ReadyMarker は「起動して画面（DOM）まで到達した」ことを外部プロセスから観測するための
// 標準出力マーカー。e2e の起動確認（app/e2e）がこの行の出現をもって起動成功と判定する。
// 出力内容はアプリ名と版番号のみで、利用者データ・キーに由来する値を含めない。
const ReadyMarker = "reqweave: ready"

// Startup は Wails の起動フックから呼ばれ、ランタイムコンテキストを保持する。
func (a *API) Startup(ctx context.Context) {
	a.ctx = ctx
	// 前回の異常終了（app.start に対する app.stop の欠落）は、動作ログを開いた時点で確定している。
	// Go の fatal error・cgo 側のシグナルは recover も終了フックも通らないため、
	// クラッシュはこの欠落でしか気づけない（実際にこの経路で落ちた例がある）。
	previousIncomplete := a.log.PreviousRunIncomplete()
	a.log.Info(applog.EventAppStart, "起動しました", applog.F("version", Version))
	if previousIncomplete {
		a.log.Warn(applog.EventPreviousRunIncomplete, "前回は正常に終了していません")
	}
	// 子プロセス型アダプタの残り物の後片づけ。異常終了で残った一時領域をここで消す。
	aiprovider.AppStarted(a.onAIEvent)
}

// Shutdown は Wails の終了フックから呼ばれ、動作ログを閉じる。
func (a *API) Shutdown(ctx context.Context) {
	// 開いているプロジェクトを閉じてから終わる。
	// Store.Close が保存キューを流し切る唯一の経路であり、
	// ここを通さないと終了時にキューへ残った書き込みが失われうる。
	_ = a.CloseDialogueProject()
	// サインインの待ち受け（最大 15 分）を畳む（背景の待ちを残さない）。
	a.cancelCodexSignIn()
	// 子プロセス型アダプタの停止と一時領域の削除。動作ログを閉じる前に行う
	// （停止の失敗を記録できるようにするため）。
	aiprovider.AppStopping(a.onAIEvent)
	a.log.Info(applog.EventAppStop, "終了します")
	a.log.Close()
}

// DomReady は Wails のフロントエンド読み込み完了フックから呼ばれ、起動完了を標準出力へ示す。
func (a *API) DomReady(ctx context.Context) {
	fmt.Printf("%s version=%s\n", ReadyMarker, Version)
}

// AppInfo はアプリ名と版番号を返す。
func (a *API) AppInfo() AppInfo {
	return AppInfo{Name: AppName, Version: Version}
}
