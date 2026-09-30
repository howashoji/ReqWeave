//go:build cloudfs

package projectstore

// クラウド同期領域（Box / Dropbox / OneDrive 等）に置いたプロジェクトデータの
// 書き込みが安全かを**実測**する。
//
// 通常の検証ゲートからは外す（同期クライアントの導入が要り、実行時間も読めないため）。
// 実行:
//
//	REQWEAVE_CLOUDFS_DIR=/path/to/cloud/folder go test -tags cloudfs -count=1 ./internal/projectstore/ -run TestCloudFS -v
//
// 判定の観点は原子的書き込みと保存処理の原子性:
// **半端な内容が読めてはいけない**。壊れるとしたらここが最初に壊れる。

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// cloudDir は検証対象の場所。未指定ならスキップせず**失敗**させる
// （前提が揃わないときに黙って緑にしない）。
func cloudDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("REQWEAVE_CLOUDFS_DIR")
	if dir == "" {
		t.Fatal("REQWEAVE_CLOUDFS_DIR に検証対象のフォルダを指定してください（クラウド同期領域）")
	}
	work := filepath.Join(dir, fmt.Sprintf("__cloudfs-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(work, dataDirMode); err != nil {
		t.Fatalf("検証用フォルダを作れません（%s）: %v", work, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	return work
}

// TestCloudFSAtomicWriteNeverExposesPartialContent は、書き換え中に**半端な内容が読めない**ことを確かめる。
//
// クラウド同期のファイルシステムでは rename が copy+delete に落ちることがあり、
// そのとき読み手は「途中まで書かれたファイル」を観測しうる。ここが本イシューの核心。
func TestCloudFSAtomicWriteNeverExposesPartialContent(t *testing.T) {
	dir := cloudDir(t)
	target := filepath.Join(dir, "records.yaml")

	// 内容は「同じ 1 文字の繰り返し」にし、混ざったら即座に分かる形にする。
	const size = 64 * 1024
	patterns := [][]byte{
		bytes.Repeat([]byte("A"), size),
		bytes.Repeat([]byte("B"), size),
	}
	if err := WriteFileAtomic(target, patterns[0]); err != nil {
		t.Fatalf("初回の書き込みに失敗: %v", err)
	}

	var (
		wg      sync.WaitGroup
		stop    = make(chan struct{})
		reads   int
		bad     int
		badSeen []byte
		mu      sync.Mutex
	)

	// 読み手: 書き換えの最中に読み続ける。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(target)
			if err != nil {
				// 置き換えの瞬間に消えて見えるのも異常（rename なら起きない）。
				mu.Lock()
				bad++
				badSeen = []byte(err.Error())
				mu.Unlock()
				continue
			}
			mu.Lock()
			reads++
			if !isUniform(data, size) {
				bad++
				badSeen = summarize(data)
			}
			mu.Unlock()
		}
	}()

	// 書き手: A と B を交互に置き換える。
	for i := 0; i < 40; i++ {
		if err := WriteFileAtomic(target, patterns[i%2]); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("%d 回目の書き込みに失敗: %v", i+1, err)
		}
	}
	close(stop)
	wg.Wait()

	if reads == 0 {
		t.Fatal("一度も読めていない（測れていない）")
	}
	if bad != 0 {
		t.Fatalf("半端な内容が %d 回観測された（%d 回中）: %s", bad, reads, badSeen)
	}
	t.Logf("読み取り %d 回、すべて完全な内容だった", reads)
}

// isUniform は「同じ 1 文字が size バイト」という完全な内容かを返す。
func isUniform(data []byte, size int) bool {
	if len(data) != size {
		return false
	}
	first := data[0]
	if first != 'A' && first != 'B' {
		return false
	}
	for _, b := range data {
		if b != first {
			return false
		}
	}
	return true
}

// summarize は失敗時の手掛かり（長さと先頭・末尾の文字）。
func summarize(data []byte) []byte {
	if len(data) == 0 {
		return []byte("長さ 0")
	}
	return []byte(fmt.Sprintf("長さ %d・先頭 %q・末尾 %q", len(data), data[0], data[len(data)-1]))
}

// TestCloudFSAtomicWriteSurvivesConcurrentWriters は、同一端末の複数の書き手が
// 直列化されずにぶつかっても、**内容が混ざらない**ことを確かめる。
//
// 本システムは保存キューで直列化するが、
// ファイルシステム側の性質を見るためここでは直接ぶつける。
func TestCloudFSAtomicWriteSurvivesConcurrentWriters(t *testing.T) {
	dir := cloudDir(t)
	target := filepath.Join(dir, "concurrent.yaml")

	const size = 16 * 1024
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('A' + n)}, size)
			for j := 0; j < 10; j++ {
				if err := WriteFileAtomic(target, payload); err != nil {
					errs <- fmt.Errorf("書き手 %d: %w", n, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("並行書き込みに失敗: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("読み出せない: %v", err)
	}
	if len(data) != size {
		t.Fatalf("長さが違う（内容が混ざった疑い）: %d", len(data))
	}
	for _, b := range data {
		if b != data[0] {
			t.Fatalf("内容が混ざっている: %s", summarize(data))
		}
	}
}

// TestCloudFSProjectLifecycle は、作成 → 記録 → 退避 → 復元の一連が通ることを確かめる。
func TestCloudFSProjectLifecycle(t *testing.T) {
	dir := cloudDir(t)
	root := filepath.Join(dir, "在庫管理システム"+ProjectFolderExt)

	store, err := CreateProject(root, CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           Author{AuthorID: "tester@example.co.jp", DisplayName: "検証"},
	})
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}

	if _, err := store.CreateDecision(Decision{
		TopicKey: "background/scope", Body: "クラウド同期領域での検証",
		Evidence: []string{"S-0001#utt-00001"},
	}); err != nil {
		t.Fatalf("決定事項を書けない: %v", err)
	}

	paths := AppPaths{Base: filepath.Join(t.TempDir(), AppID)}
	backup, err := store.CloseAndBackup(paths)
	if err != nil {
		t.Fatalf("退避に失敗: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("退避ファイルが無い: %v", err)
	}

	restored := filepath.Join(dir, "復元先")
	if _, err := RestoreZip(backup, restored); err != nil {
		t.Fatalf("復元に失敗: %v", err)
	}
	if !IsProjectFolder(restored) {
		t.Fatal("復元先がプロジェクトとして読めない")
	}
}
