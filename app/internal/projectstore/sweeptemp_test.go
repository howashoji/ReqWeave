package projectstore

// 単体テスト（一時ファイルの残骸の判定と掃除）。実行: make -C app test-unit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 掃除の対象は「WriteFileAtomic が作る名前」に限る。
// 利用者が置いた `.tmp` で終わるファイルや、原本を巻き添えにしない。
func TestIsAtomicTempName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{".project.json.tmp123456", true},
		{".S-0001.md.tmp1", true},
		{"project.json", false},
		{".project.json", false},
		{"作業中.tmp", false},              // 利用者が置いたもの（先頭が `.` でない）
		{".メモ.tmp", false},              // 末尾の数字が無い
		{".メモ.tmpabc", false},           // 末尾が数字でない
		{".project.json.tmp12a", false}, // 数字の後ろに文字がある
	} {
		if got := isAtomicTempName(tc.name); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 古い残骸は消し、**新しいもの（別プロセスが書き込み中かもしれないもの）は消さない**。
//
// アプリは単一インスタンス化しないため、同じプロジェクトを別プロセスが
// 開いていることがある。無条件に消すと相手の書き込みを壊す。
func TestSweepStaleTempFilesKeepsRecentOnes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), dataDirMode); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(root, ".project.json.tmp111")
	staleNested := filepath.Join(root, "sessions", ".S-0001.md.tmp222")
	fresh := filepath.Join(root, ".project.json.tmp333")
	keepUserFile := filepath.Join(root, "作業中.tmp")
	keepData := filepath.Join(root, "project.json")
	for _, p := range []string{stale, staleNested, fresh, keepUserFile, keepData} {
		if err := os.WriteFile(p, []byte("x"), dataFileMode); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{stale, staleNested} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if got := sweepStaleTempFiles(root); got != 2 {
		t.Errorf("掃除した件数 = %d、期待 2", got)
	}
	for _, p := range []string{stale, staleNested} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("古い残骸が消えていない: %s", p)
		}
	}
	for _, p := range []string{fresh, keepUserFile, keepData} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("消してはいけないものを消した: %s（%v）", p, err)
		}
	}
}

// `.git/` の中は触らない（git の内部ファイルを掃除の巻き添えにしない）。
func TestSweepStaleTempFilesSkipsGitDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, gitDirName, "objects"), dataDirMode); err != nil {
		t.Fatal(err)
	}
	inGit := filepath.Join(root, gitDirName, "objects", ".pack.tmp999")
	if err := os.WriteFile(inGit, []byte("x"), dataFileMode); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(inGit, old, old); err != nil {
		t.Fatal(err)
	}

	if got := sweepStaleTempFiles(root); got != 0 {
		t.Errorf("掃除した件数 = %d、期待 0（.git の中は触らない）", got)
	}
	if _, err := os.Stat(inGit); err != nil {
		t.Errorf(".git の中のファイルを消した: %v", err)
	}
}
