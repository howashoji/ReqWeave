// Package appenv は実行環境の識別（診断情報に添える OS の種別と版）。
//
// 利用者が問い合わせ用に書き出す診断情報には「アプリの版番号・OS の種別と版」を添える。
// 版番号の正本は internal/appversion、OS の識別は本パッケージが担う。
//
// **端末を特定できる値を返さない**（ホスト名・OS ユーザー名・シリアル番号等は扱わない。
// ログや診断情報に個人・端末を特定する情報を残さないため）。返すのは配布対象 OS の種別と版だけである。
package appenv

import "runtime"

// OSName は OS の種別を利用者向けの表記で返す（配布対象は macOS / Windows）。
func OSName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

// OSVersion は OS の版を返す。取得できない環境では空文字を返す（欠測を偽の値で埋めない）。
func OSVersion() string { return osVersion() }

// Arch は CPU アーキテクチャを返す（universal binary のどちらで動いているかの手がかり）。
func Arch() string { return runtime.GOARCH }
