package applog

import "sync/atomic"

// 既定のロガー。
//
// 動作ログの書き手は本パッケージ 1 つ（depcheck 規則 logs-writer）だが、
// ロガーの実体を引数で持ち回せない場所がある。**自前で起こしたゴルーチン**がそれで、
// そこでのパニックは recover しなければプロセスごと落ち、記録が一切残らない。
// 引数を層をまたいで通すより、起動時に 1 度だけ据える既定のロガーを置くほうが害が小さい。
//
// 据えるのは main の起動時 1 回だけ。据える前・据えない場合は「記録しない」ロガーとして
// 振る舞う（nil で安全 = Logger と同じ方針）。

var defaultLogger atomic.Pointer[Logger]

// SetDefault は既定のロガーを据える（main の起動時に 1 度だけ呼ぶ）。
func SetDefault(l *Logger) { defaultLogger.Store(l) }

// Default は既定のロガーを返す（未設定なら nil。nil でも記録操作は安全）。
func Default() *Logger { return defaultLogger.Load() }

// RecoverPanic は既定のロガーで (*Logger).RecoverPanic を行う。
//
// ロガーを引数で受け取れないゴルーチンの先頭で使う:
//
//	go func() {
//		defer applog.RecoverPanic(applog.EventAppPanic)
//		…
//	}()
//
// 記録したうえで再送出するため、パニックを握り潰さない（挙動は変えない）。
func RecoverPanic(event string) {
	r := recover()
	if r == nil {
		return
	}
	Default().recordPanic(event, r)
	panic(r)
}
