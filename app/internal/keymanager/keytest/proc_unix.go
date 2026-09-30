//go:build integration && !windows

package keytest

import (
	"errors"
	"os"
	"syscall"
)

// processAlive は pid のプロセスがまだ動いているかを返す。
//
// 並行して走っている別のテストバイナリの項目を消さないための判定。
// 実装は internal/applog の同名関数と同じ考え方（シグナル 0 で存在だけ確かめ、
// EPERM は「居るが権限が無い」ため生存扱い）。**本パッケージはテスト専用**のため、
// 製品コード側へ公開 API を増やさず、ここに小さく持つ。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	} else {
		return errors.Is(err, syscall.EPERM)
	}
}
