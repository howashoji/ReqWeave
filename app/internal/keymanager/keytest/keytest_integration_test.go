//go:build integration

// keytest 自身の結合テスト。実際の OS セキュアストレージを使う。

package keytest

import (
	"os/exec"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
)

// exitedPID は実際に起動して終了させたプロセスの識別子を返す。
// 「前回の中断で残った項目」= もう動いていない実行が書いた項目、を作るために使う。
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("go", "version")
	if err := cmd.Start(); err != nil {
		t.Fatalf("確認用のプロセスを起動できない: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("確認用のプロセスが失敗した: %v", err)
	}
	return pid
}

// dummySecret は実キーではないテスト用の値（テストで実キーを使わない）。
const dummySecret = "reqweave-dummy-secret-do-not-use-4c7e1a09"

// 中断で t.Cleanup が走らなかった項目を、次回の開始時に片づけること。
func TestSweepStaleRemovesLeftoverFromInterruptedRun(t *testing.T) {
	m := keymanager.New()
	ref := keymanager.Ref{ProviderID: "anthropic", Label: Marker + uuid.NewString()}

	// 中断された実行の再現: 記録は残したまま、削除の予約をしない。
	// **既に終了したプロセスが書いた**ことにする（生きている実行の項目は消さないため）。
	add(t, entry{Kind: "key", Ref: ref.String()})
	overwritePIDForTest(t, ref.String(), exitedPID(t))
	if err := m.Register(ref, dummySecret); err != nil {
		t.Fatalf("OS セキュアストレージへ登録できません: %v", err)
	}
	// 掃除できなかった場合に利用者のキーチェーンへ残さない。
	t.Cleanup(func() { _ = m.Delete(ref) })

	if ok, err := m.Exists(ref); err != nil || !ok {
		t.Fatalf("前提が成立していない（登録できていない）: %v %v", ok, err)
	}

	// **件数では判定しない**。ここで置いた項目は「終了したプロセスの残骸」であり、
	// **どの実行からも掃除の対象**になる（そういう設計）。通しのゲートでは他のパッケージが
	// 並行して走り、そちらが先に片づけると戻り値が 0 になって落ちる＝偽赤。
	// 確かめたいのは「終了したプロセスの残骸が片づくこと」であり、誰が片づけたかではない。
	SweepStale()

	ok, err := m.Exists(ref)
	if err != nil {
		t.Fatalf("存在確認に失敗: %v", err)
	}
	if ok {
		t.Error("中断で残った項目が片づけられていない")
	}
}

// **利用者の実エントリには触れないこと**（片づけの対象は印の付いたものだけ）。
func TestSweepStaleLeavesNonTestEntriesAlone(t *testing.T) {
	m := keymanager.New()
	// 利用者が自分で付けそうな参照名（"test-" で始まるが印は無い）。
	userRef := keymanager.Ref{ProviderID: "anthropic", Label: "test-" + uuid.NewString()}
	if err := m.Register(userRef, dummySecret); err != nil {
		t.Fatalf("OS セキュアストレージへ登録できません: %v", err)
	}
	t.Cleanup(func() { _ = m.Delete(userRef) })

	// 記録が壊れて実エントリが載ってしまった状況を作る。
	add(t, entry{Kind: "key", Ref: userRef.String()})
	overwritePIDForTest(t, userRef.String(), exitedPID(t))
	SweepStale()

	ok, err := m.Exists(userRef)
	if err != nil {
		t.Fatalf("存在確認に失敗: %v", err)
	}
	if !ok {
		t.Error("印の無い項目（利用者の実エントリ相当）を消してしまった")
	}
}

// 印の無い参照名を Track しようとしたら止めること（片づけの対象を限定する要）。
func TestTrackKeyRejectsUnmarkedRef(t *testing.T) {
	if isTestRef(entry{Kind: "key", Ref: "anthropic/既定"}) {
		t.Error("利用者の実エントリを片づけの対象と判定した")
	}
	if isTestRef(entry{Kind: "key", Ref: "anthropic/test-1234"}) {
		t.Error("印の無い参照名を片づけの対象と判定した")
	}
	if !isTestRef(entry{Kind: "key", Ref: "anthropic/" + Marker + "1234"}) {
		t.Error("印の付いた参照名を片づけの対象から外した")
	}
	if !isTestRef(entry{Kind: "sync", Ref: "proj-abc/token", Generated: true}) {
		t.Error("採番された参照名（記録が根拠）を片づけの対象から外した")
	}
}

// overwritePIDForTest は記録済みの項目の PID を差し替える（add は自プロセスの PID を打つため）。
func overwritePIDForTest(t *testing.T, ref string, pid int) {
	t.Helper()
	p := registryPath()
	if p == "" {
		t.Fatal("記録の置き場所を決められない")
	}
	withRegistryLock(p, func() {
		entries := read(p)
		for i := range entries {
			if entries[i].Ref == ref {
				entries[i].PID = pid
			}
		}
		writeAll(p, entries)
	})
}

// 並行して動いている別のテストバイナリの項目を消さないこと。
//
// go test ./... は複数のテストバイナリを並行に走らせる。相手が今まさに使っている項目を
// 消すと相手のテストが落ちる（2026-09-04 の検証ゲートで実際に発生）。
func TestSweepStaleKeepsEntriesOfRunningProcesses(t *testing.T) {
	m := keymanager.New()
	ref := keymanager.Ref{ProviderID: "anthropic", Label: Marker + uuid.NewString()}

	// 「並行して動いている別のテストバイナリ」= 生きているプロセスの記録。
	// ここでは自プロセスの PID（確実に生きている）を使う。
	add(t, entry{Kind: "key", Ref: ref.String()})
	if err := m.Register(ref, dummySecret); err != nil {
		t.Fatalf("OS セキュアストレージへ登録できません: %v", err)
	}
	t.Cleanup(func() { _ = m.Delete(ref) })

	SweepStale()

	ok, err := m.Exists(ref)
	if err != nil {
		t.Fatalf("存在確認に失敗: %v", err)
	}
	if !ok {
		t.Error("まだ動いている実行の項目を消してしまった（並行実行で相手のテストが落ちる）")
	}
	// 記録も残っていること（消してしまうと相手が後始末できない）。
	found := false
	for _, e := range read(registryPath()) {
		if e.Ref == ref.String() {
			found = true
		}
	}
	if !found {
		t.Error("まだ動いている実行の記録を消してしまった")
	}
}
