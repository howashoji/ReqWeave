//go:build windows

package codex

// Windows 版では Codex App Server を提供しない（Windows 実機での検証が済むまで）。
// 一時領域の場所と排他の方式もその検証で定めるため、ここでは取得できないことを明示して止める
// （黙って排他なしで動かさない = fail-closed）。

import (
	"errors"
	"os"
)

func lockExclusive(path string) (*os.File, error) {
	return nil, errors.New("Windows 版では Codex App Server を使えません")
}

func unlockAndClose(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}
