//go:build darwin

package projectstore

// macOS でプロジェクトフォルダを「パッケージ」として扱わせる（Finder で 1 個のファイルとして見せる）。
//
// Finder は、拡張属性 `com.apple.FinderInfo` の Finder フラグに **kHasBundle（0x2000）** が
// 立っているディレクトリを 1 個のファイルとして表示する。**実体はディレクトリのまま**であり、
// 中身の読み書き・コピー・git・原子的書き込みは何も変わらない。
//
// アプリ側の UTI 宣言（`com.apple.package` 準拠）だけに頼らないのは、
// **アプリが未登録の端末ではただのフォルダに見える**ため。両方を行う。
//
// `SetFile` コマンドは Xcode のコマンドラインツール同梱で、利用者の端末にあるとは限らないため使わない。

import (
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// finderInfoSize は `com.apple.FinderInfo` の大きさ（固定 32 バイト）。
const finderInfoSize = 32

// finderFlagsOffset は Finder フラグ（big-endian の 2 バイト）の位置。
// ディレクトリの FolderInfo は windowBounds(8) の直後にフラグが来る。
const finderFlagsOffset = 8

// kHasBundle は「パッケージとして扱う」フラグ。
const kHasBundle = 0x2000

// markAsPackage はディレクトリへ kHasBundle を立てる。
//
// 既存の Finder 情報（色ラベル等）を壊さないよう、**読み出して該当ビットだけを立てる**。
func markAsPackage(path string) error {
	info := make([]byte, finderInfoSize)
	n, err := unix.Getxattr(path, "com.apple.FinderInfo", info)
	switch {
	case err == nil && n == finderInfoSize:
		// 既存の値を活かす。
	case errors.Is(err, unix.ENOATTR), errors.Is(err, unix.ENODATA), err == nil:
		// まだ付いていない。ゼロから作る。
		info = make([]byte, finderInfoSize)
	default:
		return err
	}

	flags := binary.BigEndian.Uint16(info[finderFlagsOffset:])
	if flags&kHasBundle != 0 {
		return nil
	}
	binary.BigEndian.PutUint16(info[finderFlagsOffset:], flags|kHasBundle)
	return unix.Setxattr(path, "com.apple.FinderInfo", info, 0)
}
