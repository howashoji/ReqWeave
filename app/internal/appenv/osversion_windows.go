//go:build windows

package appenv

import (
	"fmt"
	"syscall"
	"unsafe"
)

// osVersionInfoEx は RtlGetVersion が埋める構造体（OSVERSIONINFOEXW）。
type osVersionInfoEx struct {
	osVersionInfoSize uint32
	majorVersion      uint32
	minorVersion      uint32
	buildNumber       uint32
	platformID        uint32
	csdVersion        [128]uint16
	servicePackMajor  uint16
	servicePackMinor  uint16
	suiteMask         uint16
	productType       byte
	reserved          byte
}

// osVersion は Windows の版（例 "10.0.22631"）を返す。
//
// GetVersionEx は互換性マニフェストの影響を受けて古い値を返すため、
// マニフェストに左右されない ntdll の RtlGetVersion を用いる。
func osVersion() string {
	var info osVersionInfoEx
	info.osVersionInfoSize = uint32(unsafe.Sizeof(info))
	proc := syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion")
	if r, _, _ := proc.Call(uintptr(unsafe.Pointer(&info))); r != 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", info.majorVersion, info.minorVersion, info.buildNumber)
}
