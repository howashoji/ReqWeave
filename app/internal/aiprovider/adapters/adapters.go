// Package adapters は 3社アダプタをレジストリへ登録するための取り込み口。
//
// プロバイダを追加するときは、アダプタパッケージを作り、本ファイルへ空白 import を 1 行足す
// （上位モジュール・既存アダプタ・エラーカタログの分類体系は変更しない）。
package adapters

import (
	_ "github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/anthropic"
	_ "github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/codex"
	_ "github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/google"
	_ "github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/openai"
)
