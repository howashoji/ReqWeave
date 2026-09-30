//go:build integration && windows

package keytest

import "os"

// processAlive は pid のプロセスがまだ動いているかを返す。
//
// Windows の os.FindProcess は OpenProcess を呼び、存在しない pid では失敗する。
// 実装は internal/applog の同名関数と同じ考え方。
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
