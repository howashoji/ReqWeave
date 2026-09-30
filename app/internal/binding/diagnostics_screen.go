package binding

// 本ファイルは診断情報の書き出しの公開バインディング。
//
// 設定画面から、書き出す内容を確認 → 保存先を選んで単一の書庫を保存する。
// **外部へは送らない**（提供者のサーバへ送る仕組みは持たない。本機能は
// ネットワーク通信を行わない）。
//
// 動作ログの読み出しはログ出力層（internal/applog）の口を通す（保存先を本層が知らない状態を
// 保つ = depcheck 規則 logs-writer）。

import (
	"fmt"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/appenv"
	"github.com/howashoji/ReqWeave/app/internal/applog"
)

// diagnosticEnvironment は診断情報に添える実行環境。
// 端末を特定できる値は入れない（ホスト名・OS ユーザー名等）。
func diagnosticEnvironment() applog.DiagnosticEnvironment {
	return applog.DiagnosticEnvironment{
		AppName:    AppName,
		AppVersion: Version,
		OS:         appenv.OSName(),
		OSVersion:  appenv.OSVersion(),
		Arch:       appenv.Arch(),
	}
}

// DiagnosticsPreview は書き出す内容を返す（保存前に利用者が中身を確かめるため）。
func (a *API) DiagnosticsPreview() (applog.Diagnostic, error) {
	d, err := a.log.Diagnostics(diagnosticEnvironment())
	if err != nil {
		return applog.Diagnostic{}, fmt.Errorf("診断情報を組み立てられません。時間をおいてもう一度お試しください。")
	}
	return d, nil
}

// ExportDiagnostics は診断情報を単一の書庫として保存する。
// 保存先は OS のファイル保存ダイアログで選ぶ。取り消した場合は空文字を返す。
func (a *API) ExportDiagnostics() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("保存先を選べません。アプリを再起動してください。")
	}
	name := fmt.Sprintf("%s-diagnostics-%s.zip", AppName, time.Now().UTC().Format("20060102T150405Z"))
	dst, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "診断情報の保存先",
		DefaultFilename: name,
	})
	if err != nil {
		return "", fmt.Errorf("保存先を選べませんでした。もう一度お試しください。")
	}
	if dst == "" {
		return "", nil
	}
	if err := a.log.WriteDiagnostics(dst, diagnosticEnvironment()); err != nil {
		return "", err
	}
	return dst, nil
}

// PreviousRunIncomplete は前回の起動が正常に終了していないかを返す（クラッシュの検知）。
// 画面はこの結果をもとに起動時の案内と書き出しへの導線を出す。
func (a *API) PreviousRunIncomplete() bool {
	return a.log.PreviousRunIncomplete()
}
