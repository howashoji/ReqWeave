//go:build windows

package applog

import "os"

// processAlive は pid のプロセスがまだ動いているかを返す。
//
// Windows の os.FindProcess は OpenProcess を呼び、**開けなければエラーを返す**
// （Unix と違い、存在しない pid では失敗する）。標準ライブラリだけで判定するため
// これを生存判定に使う。
//
// 既知の限界: pid の再利用（Unix と同じ。見逃し側へ倒れる）に加え、終了直後で
// ハンドルが残っている間は「生存」と判定しうる。**実機での確認は Windows の実機検証で行う**。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
