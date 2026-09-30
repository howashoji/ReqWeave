package aiprovider

// 本ファイルは、**子プロセスを持つアダプタ**（Codex App Server など）が本システムの起動・終了・設定変更に
// 合わせて後片づけを行うための口である。
//
// 子プロセス型のアダプタは、HTTP の API を呼ぶだけのアダプタと違い「呼び出しの外側」に状態を持つ
// （動いている子プロセス・端末上の一時領域）。本システムの終了時に子プロセスを止め、一時領域を
// 消すには、アダプタが自分で気づけない出来事（アプリの終了・設定の変更）を外から伝える必要がある
// （アプリの終了時と設定の変更時に子プロセスを止め、起動時に前回の残りを消す）。
//
// 本ファイルは**プロバイダを列挙しない**。アダプタが自分で登録し、公開バインディング層が
// 起動・終了・設定変更の時点で一括して呼ぶ。以後の子プロセス型プロバイダの追加でも本ファイルは
// 変更しない（プロバイダの追加で触るファイルを増やさないため）。

import (
	"sort"
	"sync"
)

// EventLevel は動作ログへ記録する重大度。
type EventLevel int

const (
	EventLevelInfo EventLevel = iota
	EventLevelWarn
)

// EventRecorder はアダプタの動作ログ記録口（実体は動作ログ。配線は公開バインディング層）。
//
// 記録してよいのは、**子プロセスの起動・終了・起動後の検査の結果・エラーの分類と Code** に限る
// （Codex とのやり取りの本文＝発話本文は記録しない。動作ログに利用者の発話を残さないため）。
// 本層は動作ログのパッケージへ依存しない（OnDegrade・OnPanic と同じ委譲の形）。
type EventRecorder func(level EventLevel, event, message string, fields map[string]string)

// Info は情報レベルで記録する（nil のときは何もしない）。
func (r EventRecorder) Info(event, message string, fields map[string]string) {
	r.record(EventLevelInfo, event, message, fields)
}

// Warn は警告レベルで記録する（nil のときは何もしない）。
func (r EventRecorder) Warn(event, message string, fields map[string]string) {
	r.record(EventLevelWarn, event, message, fields)
}

func (r EventRecorder) record(level EventLevel, event, message string, fields map[string]string) {
	if r == nil {
		return
	}
	r(level, event, message, fields)
}

// LifecycleHooks は子プロセス型アダプタの後片づけ。未設定のフックは呼ばれない。
type LifecycleHooks struct {
	// OnAppStart は本システムの起動時。異常終了で残った一時領域の削除に使う。
	OnAppStart func() error
	// OnAppStop は本システムの終了時。子プロセスの停止と一時領域の削除に使う。
	OnAppStop func() error
	// OnSettingsChanged は AIプロバイダ・認証方式・キーの変更時。古い設定で動いている子プロセスの停止に使う。
	OnSettingsChanged func() error
}

var (
	lifecycleMu sync.RWMutex
	lifecycles  = map[ProviderID]LifecycleHooks{}
)

// RegisterLifecycle は後片づけのフックを登録する（アダプタパッケージの init から呼ぶ）。
func RegisterLifecycle(id ProviderID, h LifecycleHooks) {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	if _, dup := lifecycles[id]; dup {
		panic("aiprovider: 後片づけのフックが二重登録されています: " + string(id))
	}
	lifecycles[id] = h
}

// AppStarted は本システムの起動時に公開バインディング層から呼ぶ。
func AppStarted(rec EventRecorder) {
	runHooks(rec, "ai.provider_start_cleanup_failed", func(h LifecycleHooks) func() error { return h.OnAppStart })
}

// AppStopping は本システムの終了時に公開バインディング層から呼ぶ。
func AppStopping(rec EventRecorder) {
	runHooks(rec, "ai.provider_stop_failed", func(h LifecycleHooks) func() error { return h.OnAppStop })
}

// SettingsChanged は AIプロバイダ・認証方式・キーの変更後に公開バインディング層から呼ぶ。
func SettingsChanged(rec EventRecorder) {
	runHooks(rec, "ai.provider_settings_change_failed", func(h LifecycleHooks) func() error { return h.OnSettingsChanged })
}

// runHooks は登録順に依存しないよう ID 順で実行し、失敗しても残りを実行する
// （1 つのアダプタの後片づけの失敗で、他のアダプタの後片づけを飛ばさない）。
func runHooks(rec EventRecorder, event string, pick func(LifecycleHooks) func() error) {
	lifecycleMu.RLock()
	ids := make([]ProviderID, 0, len(lifecycles))
	for id := range lifecycles {
		ids = append(ids, id)
	}
	hooks := make(map[ProviderID]LifecycleHooks, len(lifecycles))
	for id, h := range lifecycles {
		hooks[id] = h
	}
	lifecycleMu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		fn := pick(hooks[id])
		if fn == nil {
			continue
		}
		if err := fn(); err != nil {
			rec.Warn(event, "AIプロバイダの後片づけに失敗しました",
				map[string]string{"provider": string(id), "error": err.Error()})
		}
	}
}
