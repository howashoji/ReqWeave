//go:build darwin

package appenv

import "syscall"

// osVersion は macOS の製品版（例 "26.6.2"）を返す。
// カーネル版（kern.osrelease）ではなく利用者が知る製品版を用いる。
func osVersion() string {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return v
}
