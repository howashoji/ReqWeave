//go:build !windows

package applog

import (
	"errors"
	"os"
	"syscall"
)

// processAlive は pid のプロセスがまだ動いているかを返す。
//
// シグナル 0 は「送らずに送信可否だけ確かめる」ため、対象へ影響を与えない。
// EPERM（権限が無い）は**相手が存在する**ことを意味するため生存として扱う
// （標準ユーザーで動かす本システムでは通常起きないが、判定を安全側へ倒す）。
//
// 既知の限界: pid は再利用されうるため、終了済みの pid を別のプロセスが取っていると
// 「生存」と判定して異常終了を見逃す。**誤検知（正常なのに異常と言う）よりも
// 見逃しを選ぶ**方針（起動時の復元は別経路で働く）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
