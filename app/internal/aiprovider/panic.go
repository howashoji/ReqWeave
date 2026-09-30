package aiprovider

// 本ファイルはストリーミング用ゴルーチンのパニックの扱い。
//
// アダプタと再試行はストリーミングを**自前のゴルーチン**で回す。そこでのパニックは
// main の recover に届かず、記録なしでプロセスごと落ちる（実測: 終了コード 2・
// 標準エラーは Finder 起動時 /dev/null・OS のクラッシュレポートも出ない）。
// 利用者から見ると「対話中に突然アプリが消える」になり、AI API 障害時のデータ保全の
// 「入力済みの内容を失わない」も成り立たない。
//
// そこでパニックを**利用者向けのエラーへ倒す**。倒し先の文言にはパニックの内容を載せない
//（何が入るか制御できないため。画面・送信記録・受け渡しデータへ出さない）。
// 内容とスタックの記録は本層では行わず、**呼び出し側が渡す記録先**（PanicRecorder）へ委ねる。
// 本層は動作ログのパッケージへ依存しない（動作ログ → プロジェクトストア → 監査記録 → 本層の
// 依存があり、逆向きの import は循環になる。エフォートの縮退の記録（OnDegrade）と同じ委譲の形をとる）。

const (
	// CodeInternalPanic はストリーム処理の想定外の状態を表す内部コード。
	// プロバイダのエラーコードと衝突しないよう本システム側の接頭辞をつける。
	CodeInternalPanic = "reqweave_internal_panic"
	// messageInternalPanic は利用者へ渡す文言（エラーの詳細表示に出る）。
	messageInternalPanic = "応答の処理中に想定外の状態が発生したため中断しました"
)

// PanicRecorder は recover したパニックの記録先（nil なら記録しない）。
// 実体は動作ログへの記録で、配線は公開バインディング層が行う。
type PanicRecorder func(recovered any)

// RecoverStreamPanic はストリーミング用ゴルーチンの先頭で defer して使う。
//
//	go func() {
//		defer aiprovider.RecoverStreamPanic(sink, a.ID(), a.onPanic)
//		…
//	}()
//
// パニックを記録先へ渡したうえで、恒久的エラー（再試行しない）として
// ストリームを閉じる。**再送出しない**（プロセスを落とさないことが目的）。
// 既に Done / Fail 済みのストリームでは Fail が何もしないため、二重に閉じることはない。
func RecoverStreamPanic(sink *StreamSink, provider ProviderID, record PanicRecorder) {
	r := recover()
	if r == nil {
		return
	}
	if record != nil {
		record(r)
	}
	if sink == nil {
		return
	}
	sink.Fail(&ProviderError{
		Class:    ErrClassPermanent,
		Provider: provider,
		Code:     CodeInternalPanic,
		Message:  messageInternalPanic,
	})
}
