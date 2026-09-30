package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/*
 * hdiutil の失敗を診断できる形で残すこと・マウントを残さないこと。
 *
 * 2026-09-08 の検証ゲートの偽赤は「配布物を切り離せません（…）: 」で終端しており、
 * hdiutil が何と言って落ちたのかが残っていなかった（原因: detach の `-quiet`）。
 * ここでは **失敗メッセージに hdiutil の実出力が入ること** と
 * **残骸を検出・切り離しできること** を固定する。
 */

// 失敗した hdiutil の error には、実行した引数と hdiutil の実出力が入る。
// （`-quiet` 付きの実装ではここが空になり、原因を追えなかった）。
func TestHdiutilRunKeepsFailureDetail(t *testing.T) {
	// マウントされていないディレクトリを切り離そうとして必ず失敗させる。
	notMounted := t.TempDir()
	_, err := hdiutilRun("detach", notMounted)
	if err == nil {
		t.Fatalf("マウントされていない %q の切り離しが成功した", notMounted)
	}
	msg := err.Error()
	if !strings.Contains(msg, "hdiutil detach "+notMounted) {
		t.Errorf("実行した引数が残っていない: %q", msg)
	}
	// 「: exit status N: <理由>」の理由部分が空でないこと。
	i := strings.LastIndex(msg, ": ")
	if i < 0 || strings.TrimSpace(msg[i+2:]) == "" {
		t.Fatalf("失敗の理由が空: %q", msg)
	}
	if !strings.Contains(msg, "hdiutil: detach failed") {
		t.Errorf("hdiutil の実出力が入っていない: %q", msg)
	}
}

// attachTestDMG はテスト用の dmg を作ってマウントし、dmg のパスとマウント先を返す。
// 後始末はテスト側で行う（残したままにしない）。
func attachTestDMG(t *testing.T) (dmgPath, mountPoint string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dmgPath = filepath.Join(t.TempDir(), "stale.dmg")
	// **`-quiet` を付けない**: 失敗の理由まで抑止されるため。
	if o, err := exec.Command("hdiutil", "create", "-volname", "ReqWeaveStale", "-srcfolder", root,
		"-ov", "-format", "UDZO", dmgPath).CombinedOutput(); err != nil {
		t.Fatalf("テスト用の dmg を作れません: %v\n%s", err, o)
	}
	mountPoint = t.TempDir()
	if err := attachRetry(dmgPath, mountPoint); err != nil {
		t.Fatalf("テスト用の dmg をマウントできません: %v", err)
	}
	t.Cleanup(func() {
		// 取りこぼしがあっても他のテストへ持ち越さない。
		if left, err := mountsOfImage(dmgPath); err == nil {
			for _, m := range left {
				_ = detachDevices(m)
			}
		}
	})
	return dmgPath, mountPoint
}

// マウント中の dmg は mountsOfImage で device つきで見つかり、
// 切り離した後は見つからない（残骸の検出がこれに乗る）。
func TestMountsOfImageFindsAndLosesMount(t *testing.T) {
	dmgPath, mountPoint := attachTestDMG(t)

	found, err := mountsOfImage(dmgPath)
	if err != nil {
		t.Fatalf("マウント一覧を読めません: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("マウント中の dmg が 1 件で見つからない: %d 件 %+v", len(found), found)
	}
	if len(found[0].Devices) == 0 {
		t.Errorf("device が取れていない: %+v", found[0])
	}
	// /var は /private/var への symlink のため、実体へ解決してから突き合わせる。
	resolved, rerr := filepath.EvalSymlinks(mountPoint)
	if rerr != nil {
		t.Fatalf("マウント先を解決できません: %v", rerr)
	}
	if !containsString(found[0].MountPoints, resolved) {
		t.Errorf("マウント先が取れていない: %+v（期待: %s）", found[0], resolved)
	}

	if err := detachRetry(mountPoint, dmgPath); err != nil {
		t.Fatalf("切り離せません: %v", err)
	}
	// **detach の成功と hdiutil info からの消滅は同期しない**。
	// 上限つきで排出を待つ。上限を過ぎて残っていれば取り残しとして失敗する。
	after, err := waitMountsDrained(dmgPath)
	if err != nil {
		t.Fatalf("マウント一覧を読めません: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("切り離した後もマウントが残っている: %+v", after)
	}
}

// 排出待ちは**本当の取り残しを緑にしない**。
// マウントしたままなら上限まで待って「残っている」を返す。
func TestWaitMountsDrainedDoesNotHideLiveMount(t *testing.T) {
	dmgPath, mountPoint := attachTestDMG(t)

	// 上限を短くして測る（既定の 10 秒を待たない。判定の向きは変えない）。
	restore := mountDrainTimeout
	mountDrainTimeout = 600 * time.Millisecond
	t.Cleanup(func() { mountDrainTimeout = restore })

	started := time.Now()
	left, err := waitMountsDrained(dmgPath)
	if err != nil {
		t.Fatalf("マウント一覧を読めません: %v", err)
	}
	if len(left) == 0 {
		t.Fatalf("マウント中なのに「残っていない」と返った（取り残しを緑にしている）")
	}
	if elapsed := time.Since(started); elapsed < mountDrainTimeout {
		t.Errorf("上限まで待っていない: %v < %v", elapsed, mountDrainTimeout)
	}

	if err := detachRetry(mountPoint, dmgPath); err != nil {
		t.Fatalf("切り離せません: %v", err)
	}
}

// 前回の残骸（同じ dmg がマウントされたまま）は、実行前に切り離され、
// **何を切り離したかが記録される**（黙って消さない）。
func TestSweepStaleMountsDetachesAndRecords(t *testing.T) {
	dmgPath, _ := attachTestDMG(t)

	var logged []string
	sweepStaleMounts(dmgPath, func(msg string) { logged = append(logged, msg) })

	left, err := waitMountsDrained(dmgPath)
	if err != nil {
		t.Fatalf("マウント一覧を読めません: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("残骸が切り離されていない: %+v", left)
	}
	if len(logged) != 1 {
		t.Fatalf("切り離しの記録が 1 件でない: %d 件 %v", len(logged), logged)
	}
	if !strings.Contains(logged[0], "切り離しました") || !strings.Contains(logged[0], "/dev/disk") {
		t.Errorf("何を切り離したかが記録されていない: %q", logged[0])
	}
}

// 残骸が無いときは何も記録しない（無音）。
func TestSweepStaleMountsQuietWhenClean(t *testing.T) {
	dmgPath := filepath.Join(t.TempDir(), "not-mounted.dmg")
	if err := os.WriteFile(dmgPath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged []string
	sweepStaleMounts(dmgPath, func(msg string) { logged = append(logged, msg) })
	if len(logged) != 0 {
		t.Errorf("残骸が無いのに記録が出た: %v", logged)
	}
}

// マウント先からの切り離しが失敗し続けても、**マウントを残さない**。
//
// マウント先を取り違えた状況（そこには何もマウントされていない）を作り、
// `hdiutil detach <マウント先>` を必ず失敗させる。それでも device 指定の後始末が働き、
// dmg のマウントは残らない。失敗自体は隠さず、後始末の結果を添えて報告する。
func TestDetachRetryFallsBackToDeviceAndLeavesNoMount(t *testing.T) {
	dmgPath, _ := attachTestDMG(t)
	wrong := t.TempDir() // 何もマウントされていない場所

	saveAttempts, saveInterval := detachAttempts, detachInterval
	detachAttempts, detachInterval = 1, 10*time.Millisecond
	defer func() { detachAttempts, detachInterval = saveAttempts, saveInterval }()

	err := detachRetry(wrong, dmgPath)
	if err == nil {
		t.Fatal("マウントされていない場所の切り離しが成功扱いになった")
	}
	if !strings.Contains(err.Error(), "hdiutil: detach failed") {
		t.Errorf("失敗の理由が残っていない: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "device 指定で切り離しました") {
		t.Errorf("後始末の結果が報告されていない: %q", err.Error())
	}

	left, ierr := mountsOfImage(dmgPath)
	if ierr != nil {
		t.Fatalf("マウント一覧を読めません: %v", ierr)
	}
	if len(left) != 0 {
		t.Errorf("切り離しに失敗した後にマウントが残っている: %+v", left)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
