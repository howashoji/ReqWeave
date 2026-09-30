//go:build !windows

package codex

// 一時領域の排他（実装時に決めた条件は「プロセスの生死で判定でき、異常終了で残った排他を
// 次の起動で解けること」）。OS のファイルロックは**プロセスの終了で必ず解放される**ため、
// 残留ロックを手当てする仕組みが要らない。

import (
	"fmt"
	"os"
	"syscall"
)

// lockExclusive はロックファイルへ排他ロックを取る（待たない）。
// 既に他のプロセスが握っていれば errWorkspaceBusy を返す。
func lockExclusive(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("一時領域の排他を取れません: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, errWorkspaceBusy
		}
		return nil, fmt.Errorf("一時領域の排他を取れません: %w", err)
	}
	return file, nil
}

// unlockAndClose はロックを解いて閉じる。
func unlockAndClose(file *os.File) error {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return file.Close()
}
