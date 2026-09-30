package binding

// 本ファイルは Wails 内部のログを動作ログへ橋渡しする。
//
// # なぜ要るか
//
// フロントエンドからのバインディング呼び出しは Wails が**別ゴルーチン**で実行し、そこでの
// パニックは Wails 自身が recover して**自分のロガー**へ出す。既定のロガーの出力先は標準出力だが、
// Finder / open から起動したアプリの標準出力は macOS で `/dev/null` になる（実機で確かめた）。
// 橋渡しをしないと、バインディング呼び出し中の異常が**どこにも残らない**。
//
// # 何を渡さないか
//
// Wails の Dispatcher はパニック時に「呼び出しメッセージそのもの」を添えて出力する
// （`process message error: <呼び出しメッセージ> -> <エラー>`）。呼び出しメッセージには
// **引数＝発話本文などの業務データ**が入るため、動作ログへ渡さない
// （発話本文は動作ログに記録しない情報に当たる）。
// 同じエラーは各プラットフォームのフロントエンドが呼び出しメッセージ抜きで別途出力するため
// （darwin/windows とも `f.logger.Error("%s", err.Error())`）、落としても内容は失われない。
//
// 記録の水準は警告以上に限る。Wails は情報以下の水準で呼び出し結果の JSON（業務データ）を
// 出すことがあるため、水準で構造的に締める。

import (
	"strings"

	"github.com/wailsapp/wails/v2/pkg/logger"

	"github.com/howashoji/ReqWeave/app/internal/applog"
)

// 動作ログへ記録するときのイベント名。
const (
	eventWailsWarn  = "wails.warn"
	eventWailsError = "wails.error"
	eventWailsFatal = "wails.fatal"
)

// wailsLogger は Wails のロガー（logger.Logger）の実装。
// 既定のロガーへそのまま流したうえで、警告以上を動作ログへ複写する。
type wailsLogger struct {
	base logger.Logger
	log  *applog.Logger
}

// NewWailsLogger は Wails へ渡すロガーを返す（options.App の Logger）。
//
// 既定のロガー（標準出力）の挙動は変えない。`wails dev` での端末出力が消えないようにするためで、
// 動作ログへの記録はその複写である。
func NewWailsLogger(l *applog.Logger) logger.Logger {
	return newWailsLogger(logger.NewDefaultLogger(), l)
}

// newWailsLogger は委譲先を差し替えられる形の生成（テストが委譲を確認するために使う）。
func newWailsLogger(base logger.Logger, l *applog.Logger) *wailsLogger {
	return &wailsLogger{base: base, log: l}
}

func (w *wailsLogger) Print(message string) { w.base.Print(message) }
func (w *wailsLogger) Trace(message string) { w.base.Trace(message) }
func (w *wailsLogger) Debug(message string) { w.base.Debug(message) }
func (w *wailsLogger) Info(message string)  { w.base.Info(message) }

func (w *wailsLogger) Warning(message string) {
	w.base.Warning(message)
	w.record(eventWailsWarn, message)
}

func (w *wailsLogger) Error(message string) {
	w.base.Error(message)
	w.record(eventWailsError, message)
}

func (w *wailsLogger) Fatal(message string) {
	w.record(eventWailsFatal, message)
	// Fatal は既定のロガーがプロセスを落とすため、記録を先に済ませる。
	w.base.Fatal(message)
}

// record は業務データを含みうる行を落としたうえで動作ログへ書く。
func (w *wailsLogger) record(event, message string) {
	if carriesCallPayload(message) {
		return
	}
	switch event {
	case eventWailsWarn:
		w.log.Warn(event, message)
	default:
		w.log.Error(event, message)
	}
}

// carriesCallPayload はフロントエンドからの呼び出しメッセージ（引数を含む）を抱えた行かを返す。
//
// 判定は 2 通りを併用する。文言（Dispatcher の書式）だけに頼ると、Wails 側の文言変更で
// 業務データが黙って流れ出すため、呼び出しメッセージの構造（Wails 内部のフィールド名）でも見る。
func carriesCallPayload(message string) bool {
	return strings.HasPrefix(message, "process message error:") ||
		strings.Contains(message, `"callbackID"`)
}
