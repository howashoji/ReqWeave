package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/binding"
	"github.com/howashoji/ReqWeave/app/internal/fileassoc"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	api := binding.New()
	// クラッシュ（パニック）も動作ログへ残す。マスキングとローテーションは applog の出力層が行う。
	// 記録後は再送出して異常終了させる。
	// **これが捕まえるのは main ゴルーチンのパニックだけ**（実測で確かめた）。
	// 自前のゴルーチンは各所の applog.RecoverPanic が、フロントエンドからの呼び出しは
	// Wails のロガーの橋渡し（binding.NewWailsLogger）が受ける。
	applog.SetDefault(api.Log())
	defer api.Log().RecoverPanic(applog.EventAppPanic)

	// 受け渡しファイル（.rwvq / .rwva）の関連付け登録。
	// macOS はアプリ本体の Info.plist が持つため何もしない。Windows は利用者領域
	//（HKCU）へ登録する（管理者権限を要さない）。
	// 登録できなくてもアプリの利用は続けられるため、失敗しても起動を止めない。
	fileassoc.Register()

	err := wails.Run(&options.App{
		Title:            binding.AppName,
		Width:            1280,
		Height:           832,
		MinWidth:         1024,
		MinHeight:        720,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 0x16, G: 0x18, B: 0x1d, A: 1}, // 画面の背景色（配色トークンの bg。ダーク）
		// Wails 内部のログ（バインディング呼び出し中のパニックを含む）を動作ログへ橋渡しする。
		// 既定のロガーの標準出力は Finder 起動時に /dev/null になるため（実測）、
		// 橋渡しをしないと異常がどこにも残らない。
		Logger:     binding.NewWailsLogger(api.Log()),
		OnStartup:  api.Startup,
		OnDomReady: api.DomReady,
		OnShutdown: api.Shutdown,
		// macOS は Finder のダブルクリック・アプリへのドラッグを起動引数ではなく
		// open-file イベントで届ける。
		Mac:  &mac.Options{OnFileOpen: api.HandleOpenFile},
		Bind: []interface{}{api},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
