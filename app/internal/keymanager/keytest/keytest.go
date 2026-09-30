//go:build integration

// Package keytest は、結合テストが OS セキュアストレージへ作った項目を確実に片づけるための道具。
//
// # なぜ要るか
//
// `keymanager` と `binding` の初期設定系の結合テストは、**利用者の login キーチェーンを実際に使う**
// （OS セキュアストレージへの直結をモックで置き換えず、実物で確かめるため）。
// 各テストは `t.Cleanup` で自分の項目を消しているが、**実行が中断されると Cleanup は走らない**
// （ゲートの打ち切り・パニック・端末のスリープ）。2026-09-03 の中断で
// `anthropic/test-fac4a2cd-…` が利用者のキーチェーンへ残った。
//
// # やり方
//
// 作った参照名を**利用者のキャッシュ領域のファイルへ記録**し、
//   - テストの終了時に消す（従来どおり）
//   - **次回のテスト開始時に、記録に残っているものを消す**（中断で消し損ねた分）
//
// の 2 段で片づける。片づけの対象は **testLabelPrefix で始まる参照名だけ**であり、
// 利用者の実エントリには触れない。
//
// 本パッケージは `integration` タグつきのため、アプリの配布物には入らない。
package keytest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
)

// Marker は結合テストが作る参照名に必ず含める印。**片づけの対象はこれを含むものだけ**。
//
// 利用者が自分で付けうる名前（"test-…" など）と紛れない綴りにしてある。
// 紛れる綴りを使うと、片づけが利用者の実エントリを消しうる。
const Marker = "reqweave-inttest-"

// registryName は片づけ待ちの記録の置き場所（利用者のキャッシュ領域配下）。
const registryDir = "reqweave-keytest"
const registryName = "pending.jsonl"

// entry は片づけ待ちの 1 件。Kind で対象のサービスを分ける。
type entry struct {
	// Kind は "key"（シークレットキー）か "sync"（同期先の認証情報）。
	Kind string `json:"kind"`
	// Ref は保存されているアカウント名（`<プロバイダ>/<参照名>` / `<プロジェクト>/<種別>`）。
	Ref string `json:"ref"`
	// Generated は「参照名に Marker を入れられない」もの（アプリが採番したプロジェクト ID など）。
	// この記録だけが片づけの根拠になるため、**認証情報を作る前に記録する**。
	Generated bool `json:"generated,omitempty"`
	// PID は記録を書いたテストバイナリのプロセス識別子。
	// `go test ./...` は複数のテストバイナリを**並行**に走らせるため、
	// **まだ動いているプロセスの項目を消してはいけない**（消すと相手のテストが落ちる）。
	PID int `json:"pid,omitempty"`
}

// mu は同じプロセス内の直列化。**プロセスをまたぐ直列化は withRegistryLock が行う**。
var mu sync.Mutex

// 記録ファイルの読み書きをプロセスをまたいで直列化するための設定。
const (
	// lockWait はロックを待つ上限。超えたらロック無しで進む（テストを止めない）。
	lockWait = 10 * time.Second
	// lockStale はロックを残したまま落ちたプロセスがあると見なすまでの時間。
	lockStale = 30 * time.Second
	// lockPoll は待ち直す間隔。
	lockPoll = 2 * time.Millisecond
)

// withRegistryLock は記録ファイルの「読んで書き戻す」更新をプロセスをまたいで直列化する。
//
// `go test -tags integration ./...` は keymanager / binding / sync… のテストバイナリを**並行**に走らせ、
// いずれも本パッケージを使う。プロセス内の mutex だけでは、後から書き戻した側が相手の追記を
// 消してしまい（lost update）、**片づけ待ちの記録が失われて中断の残骸が二度と片づかない**。
//
// ロックは O_EXCL のファイルで取る（外部依存を増やさず、どの OS でも同じ動きになる）。
// 保持したまま落ちた場合に備え、古いロックは一定時間で解く。
func withRegistryLock(path string, fn func()) {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		fn()
		return
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			fn()
			_ = os.Remove(lock)
			return
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(lock) // 保持していたプロセスが落ちている
			continue
		}
		if time.Now().After(deadline) {
			fn() // 取れなくても作業は続ける（記録を残さないより良い）
			return
		}
		time.Sleep(lockPoll)
	}
}

// registryPath は記録ファイルの場所を返す。取れない場合は空（記録しない）。
func registryPath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, registryDir, registryName)
}

// TrackKey は作ったシークレットキーの参照名を記録し、テスト終了時の削除を予約する。
//
// 参照名に Marker が含まれない場合は失敗させる（利用者の実エントリを
// 片づけの対象にしないため）。プロバイダ ID 側・参照名側のどちらに付けてもよい。
func TrackKey(t *testing.T, ref keymanager.Ref) {
	t.Helper()
	if !strings.Contains(ref.ProviderID, Marker) && !strings.Contains(ref.Label, Marker) {
		t.Fatalf("テストが作るキー参照名には %q を含めること（片づけの対象を限定するため）: %q", Marker, ref.String())
	}
	add(t, entry{Kind: "key", Ref: ref.String()})
	t.Cleanup(func() {
		_ = keymanager.New().Delete(ref)
		remove(entry{Kind: "key", Ref: ref.String()})
	})
}

// TrackSync は作った同期先の認証情報の参照名を記録し、テスト終了時の削除を予約する。
func TrackSync(t *testing.T, ref keymanager.SyncRef) {
	t.Helper()
	if !strings.Contains(ref.ProjectID, Marker) {
		t.Fatalf("テストが作る同期の参照名には %q を含めること（片づけの対象を限定するため）: %q", Marker, ref.String())
	}
	add(t, entry{Kind: "sync", Ref: ref.String()})
	t.Cleanup(func() {
		_ = keymanager.NewSyncCredentials().Delete(ref)
		remove(entry{Kind: "sync", Ref: ref.String()})
	})
}

// TrackGeneratedSync は、参照名に Marker を入れられない同期の認証情報を記録する。
//
// プロジェクト ID はアプリが採番するためテスト側で綴りを決められない。
// **認証情報を登録する前に呼ぶこと**（中断が登録の直後に起きても記録が残るように）。
// 対象は一時フォルダ上のプロジェクトであり、採番は毎回新しい値になるため、
// 利用者の実プロジェクトの参照名と一致することはない。
func TrackGeneratedSync(t *testing.T, ref keymanager.SyncRef) {
	t.Helper()
	if strings.TrimSpace(ref.ProjectID) == "" {
		t.Fatal("同期の参照名のプロジェクト ID が空（記録できない）")
	}
	add(t, entry{Kind: "sync", Ref: ref.String(), Generated: true})
	t.Cleanup(func() {
		_ = keymanager.NewSyncCredentials().Delete(ref)
		remove(entry{Kind: "sync", Ref: ref.String(), Generated: true})
	})
}

// SweepStale は前回までの中断で消し損ねた項目を削除する。
// TestMain の冒頭から呼ぶ。消せた件数を返す（0 なら残骸が無かった）。
func SweepStale() int {
	mu.Lock()
	defer mu.Unlock()
	p := registryPath()
	if p == "" {
		return 0
	}
	swept := 0
	withRegistryLock(p, func() { swept = sweepLocked(p) })
	return swept
}

// sweepLocked は記録の読み直しから書き戻しまで（呼び出し側がロックを保持していること）。
func sweepLocked(p string) int {
	entries := read(p)
	if len(entries) == 0 {
		return 0
	}
	swept := 0
	var kept []entry
	for _, e := range entries {
		if !isTestRef(e) {
			// 記録が壊れている・印が違うものには触れない（利用者の実エントリを守る）。
			kept = append(kept, e)
			continue
		}
		// **まだ動いている実行の項目は消さない**。
		// go test ./... は複数のテストバイナリを並行に走らせるため、
		// 相手が今まさに使っている項目を消すと相手のテストが落ちる。
		if e.PID != 0 && processAlive(e.PID) {
			kept = append(kept, e)
			continue
		}
		switch e.Kind {
		case "key":
			ref, err := keymanager.ParseRef(e.Ref)
			if err != nil {
				kept = append(kept, e)
				continue
			}
			_ = keymanager.New().Delete(ref)
		case "sync":
			ref, err := keymanager.ParseSyncRef(e.Ref)
			if err != nil {
				kept = append(kept, e)
				continue
			}
			_ = keymanager.NewSyncCredentials().Delete(ref)
		default:
			kept = append(kept, e)
			continue
		}
		swept++
	}
	writeAll(p, kept)
	return swept
}

// isTestRef は記録がテストの作った参照名かを判定する（片づけの対象を限定する要）。
//
// **記録ファイルに書いてあるからではなく、名前に Marker が入っていることで判断する**
// （記録が壊れていても、利用者の実エントリを消さない）。
func isTestRef(e entry) bool {
	if !strings.Contains(e.Ref, "/") {
		return false
	}
	switch e.Kind {
	case "key", "sync":
		return strings.Contains(e.Ref, Marker) || e.Generated
	}
	return false
}

// add は 1 件を記録へ追記する（記録できなくてもテストは止めない）。
func add(t *testing.T, e entry) {
	t.Helper()
	addEntry(e)
}

// addEntry は記録への追記そのもの（テストの外＝補助プロセスからも使う）。
func addEntry(e entry) {
	mu.Lock()
	defer mu.Unlock()
	p := registryPath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	e.PID = os.Getpid()
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	// 追記そのものは 1 回の Write だが、**読んで書き戻す側（remove / SweepStale）と
	// 競合すると消える**ため、同じロックの中で行う。
	withRegistryLock(p, func() {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.Write(append(b, '\n'))
	})
}

// remove は片づけ済みの 1 件を記録から落とす。
func remove(target entry) {
	mu.Lock()
	defer mu.Unlock()
	p := registryPath()
	if p == "" {
		return
	}
	target.PID = os.Getpid()
	withRegistryLock(p, func() {
		var kept []entry
		for _, e := range read(p) {
			if e == target {
				continue
			}
			kept = append(kept, e)
		}
		writeAll(p, kept)
	})
}

// writeAll は記録を書き戻す（空になったらファイルごと消す）。
func writeAll(p string, entries []entry) {
	if len(entries) == 0 {
		_ = os.Remove(p)
		return
	}
	var buf strings.Builder
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	_ = os.WriteFile(p, []byte(buf.String()), 0o600)
}

// read は記録を読む（壊れた行は飛ばす）。
func read(p string) []entry {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var entries []entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}
