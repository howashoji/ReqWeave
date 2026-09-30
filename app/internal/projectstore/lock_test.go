package projectstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// heartbeat_at が閾値より古ければ残留とみなす（自動解除はしない）。
func TestLockInfoIsStale(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		heartbeat time.Time
		want      bool
	}{
		{"直近の heartbeat", now.Add(-30 * time.Second), false},
		{"閾値の直前", now.Add(-4*time.Minute - 59*time.Second), false},
		{"閾値ちょうど", now.Add(-5 * time.Minute), true},
		{"閾値を超過", now.Add(-30 * time.Minute), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := LockInfo{HeartbeatAt: c.heartbeat}
			if got := info.IsStale(now, defaultStaleAfter); got != c.want {
				t.Errorf("IsStale: got %v, want %v", got, c.want)
			}
		})
	}
}

// 残留判定の閾値 > ハートビート間隔 ×3 を満たすこと。
func TestLockPolicyDefaultsAreWithinAllowedRange(t *testing.T) {
	p := defaultLockPolicy()
	if p.heartbeatInterval < 30*time.Second || p.heartbeatInterval > 120*time.Second {
		t.Errorf("ハートビート間隔が許容範囲（30〜120 秒）外: %v", p.heartbeatInterval)
	}
	if p.staleAfter < 3*time.Minute || p.staleAfter > 10*time.Minute {
		t.Errorf("残留判定の閾値が許容範囲（3〜10 分）外: %v", p.staleAfter)
	}
	if p.staleAfter <= p.heartbeatInterval*3 {
		t.Errorf("閾値 %v が間隔 %v の 3 倍以下", p.staleAfter, p.heartbeatInterval)
	}
	if p.shortTimeout < 10*time.Second || p.shortTimeout > 30*time.Second {
		t.Errorf("短時間ロックのタイムアウトが許容範囲（10〜30 秒）外: %v", p.shortTimeout)
	}
	if p.retryMin < 100*time.Millisecond || p.retryMax > time.Second || p.retryMin > p.retryMax {
		t.Errorf("リトライ間隔が許容範囲（100ms〜1s）外: %v〜%v", p.retryMin, p.retryMax)
	}
}

// ハートビートは**削除済みのロックファイルを再作成しない**。
//
// 以前は Stat で存在を確かめてから原子的書き込みをしていたため、確認と書き込みの間に
// 外から消されると再作成された（フォルダを消した直後に再作成され、後片づけが失敗した）。
func TestLockHeartbeatDoesNotRecreateRemovedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "documents-requirements.lock")
	// 中身は「まだ書かれていない」と分かる文字列にしておき、上書きされたことで
	// ハートビートが実際に走ったことを確かめる（空振りで緑にならないように）。
	if err := os.WriteFile(path, []byte("not-yet"), dataFileMode); err != nil {
		t.Fatal(err)
	}

	info := LockInfo{
		AppInstanceID: "test-instance",
		AcquiredAt:    time.Now().UTC().Truncate(time.Second),
		HeartbeatAt:   time.Now().UTC().Truncate(time.Second),
	}
	l := &Lock{
		path:   path,
		target: "documents-requirements",
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go l.heartbeat(info, time.Millisecond)
	defer func() {
		close(l.stop)
		<-l.done
	}()

	// 1. ハートビートが実際に書いていること
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ロックファイルを読めない: %v", err)
		}
		if string(b) != "not-yet" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ハートビートがロックファイルを更新しない（検証が成立しない）")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// 2. 消したあとは再作成しないこと
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // 周期（1ms）を十分に跨ぐ
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("削除済みのロックファイルが再作成された: %v", err)
	}
}
