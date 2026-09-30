//go:build !darwin && !windows

package appenv

// osVersion は配布対象外の OS では版を返さない（欠測を偽の値で埋めない）。
// 配布対象は macOS / Windows のみ。開発用に Linux で動かした場合はここを通る。
func osVersion() string { return "" }
