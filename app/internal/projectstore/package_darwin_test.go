//go:build darwin

package projectstore

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

/*
 * macOS でプロジェクトフォルダをパッケージとして扱わせる。
 *
 * 期待値は「Finder が 1 個のファイルとして表示する条件」= kHasBundle が立っていること。
 * **実体はディレクトリのまま**であることも併せて確かめる（ここが崩れると同期も退避も壊れる）。
 */

func finderFlags(t *testing.T, path string) uint16 {
	t.Helper()
	buf := make([]byte, finderInfoSize)
	n, err := unix.Getxattr(path, "com.apple.FinderInfo", buf)
	if err != nil {
		t.Fatalf("Finder 情報を読めない: %v", err)
	}
	if n != finderInfoSize {
		t.Fatalf("Finder 情報の大きさが違う: %d", n)
	}
	return binary.BigEndian.Uint16(buf[finderFlagsOffset:])
}

func TestMarkAsPackageSetsBundleFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "在庫管理システム"+ProjectFolderExt)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	if err := markAsPackage(dir); err != nil {
		t.Fatalf("パッケージにできない: %v", err)
	}
	if flags := finderFlags(t, dir); flags&kHasBundle == 0 {
		t.Fatalf("パッケージの印が立っていない: flags=0x%04x", flags)
	}

	// 実体はディレクトリのまま（中身の読み書きが従来どおりできる）。
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("ディレクトリでなくなっている: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileProject), []byte("x"), 0o644); err != nil {
		t.Fatalf("中へ書けない: %v", err)
	}
}

func TestMarkAsPackageKeepsExistingFinderInfo(t *testing.T) {
	// 色ラベル等、既に付いている Finder 情報を壊さない。
	dir := filepath.Join(t.TempDir(), "既存"+ProjectFolderExt)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	const otherFlag = 0x0004 // 既存の別のフラグ（何であれ保たれること）
	existing := make([]byte, finderInfoSize)
	binary.BigEndian.PutUint16(existing[finderFlagsOffset:], otherFlag)
	if err := unix.Setxattr(dir, "com.apple.FinderInfo", existing, 0); err != nil {
		t.Fatalf("Finder 情報を付けられない: %v", err)
	}

	if err := markAsPackage(dir); err != nil {
		t.Fatalf("パッケージにできない: %v", err)
	}
	flags := finderFlags(t, dir)
	if flags&kHasBundle == 0 {
		t.Fatalf("パッケージの印が立っていない: 0x%04x", flags)
	}
	if flags&otherFlag == 0 {
		t.Fatalf("既存の Finder 情報を消している: 0x%04x", flags)
	}
}

func TestMarkAsPackageIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "二度"+ProjectFolderExt)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := markAsPackage(dir); err != nil {
			t.Fatalf("%d 回目で失敗: %v", i+1, err)
		}
	}
	if flags := finderFlags(t, dir); flags&kHasBundle == 0 {
		t.Fatal("2 回目で印が消えている")
	}
}
