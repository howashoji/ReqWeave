//go:build integration

// 結合テスト（予約と作業状況のバインディング）。

package binding

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 同期先を設定していない（＝到達できない）状態でも、排他・並行のいずれでも
// 作業を開始でき、予約がその時点では他メンバーへ効いていない旨が示される。
func TestStartWorkWithoutSyncRemote(t *testing.T) {
	a, root := newMemberAPI(t)
	if sync := storeOf(t, a).Project().Sync; sync != nil {
		t.Fatalf("この検証は同期先が未設定であることを前提にする: %+v", sync)
	}

	for _, mode := range []string{projectstore.ReservationExclusive, projectstore.ReservationConcurrent} {
		target := projectstore.ReservationTerms
		if mode == projectstore.ReservationConcurrent {
			target = projectstore.ReservationRoster
		}
		set, err := a.StartWork(target, mode, false)
		if err != nil {
			t.Fatalf("同期先が無い状態で %s の作業を開始できない: %v", mode, err)
		}
		if set.Mode != mode || !set.Self || set.StartedAt == "" {
			t.Errorf("記録内容が違う: %+v", set)
		}
	}

	list, err := a.Reservations()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("作業状況が 2 件にならない: %+v", list.Items)
	}
	if !strings.Contains(list.Notice, "反映して初めて") || !strings.Contains(list.Notice, "最後に取り込んだ時点") {
		t.Errorf("予約が同期先へ反映して初めて効く旨が示されない: %q", list.Notice)
	}
	// 設定は変更履歴に残る（作業者名・日時つき）。
	rec := changeOf(t, root, projectstore.ReservationTerms, auditlog.ChangeReservationSet)
	if rec == nil {
		t.Fatal("予約の設定が変更履歴に無い")
	}
	if rec.Author != "k.sato@example.co.jp" || !strings.Contains(rec.After, "佐藤") || rec.At.IsZero() {
		t.Errorf("記録内容が違う: %+v", *rec)
	}
}

// 予約できない対象・進め方は理由つきで拒否する（画面へ内部コード値を出さない）。
func TestStartWorkRejectsUnknownTargetAndMode(t *testing.T) {
	a, _ := newMemberAPI(t)

	if _, err := a.StartWork("ids", projectstore.ReservationExclusive, false); err == nil {
		t.Error("対象一覧に無い対象が受理された")
	}
	if _, err := a.StartWork("requirement:FR-1", projectstore.ReservationExclusive, false); err == nil {
		t.Error("形式が不正な成果物 ID が受理された")
	}
	_, err := a.StartWork(projectstore.ReservationTerms, "shared", false)
	if err == nil {
		t.Error("進め方が不正な値で受理された")
	} else if !strings.Contains(err.Error(), "排他") || !strings.Contains(err.Error(), "並行") {
		t.Errorf("選べる進め方が示されない: %v", err)
	}
	if _, err := a.CheckReservation("sessions"); err == nil {
		t.Error("対象一覧に無い対象の確認が受理された")
	}
}

// 閲覧権限は作業を開始できず予約も解除できないが、作業状況は参照できる。
func TestReservationViewerScope(t *testing.T) {
	a, root := newMemberAPI(t)
	if _, err := a.StartWork(projectstore.ReservationTerms, projectstore.ReservationExclusive, false); err != nil {
		t.Fatal(err)
	}
	demoteToViewer(t, root, "k.sato@example.co.jp")

	if _, err := a.StartWork(projectstore.ReservationRoster, projectstore.ReservationExclusive, false); err == nil {
		t.Error("閲覧権限で作業を開始できた")
	} else if !strings.Contains(err.Error(), "編集権限が必要です") {
		t.Errorf("理由が示されていない: %v", err)
	}
	if err := a.ReleaseReservation(projectstore.ReservationTerms, "y.suzuki@example.co.jp", true); err == nil {
		t.Error("閲覧権限で他メンバーの予約を解除できた")
	}
	if err := a.ReleaseWindowLock(projectstore.LockTerms, true); err == nil {
		t.Error("閲覧権限でロックを解除できた")
	}
	list, err := a.Reservations()
	if err != nil {
		t.Fatalf("閲覧権限で作業状況を参照できない: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("閲覧権限で作業状況が見えない: %+v", list)
	}
	if _, err := a.WindowLocks(); err != nil {
		t.Errorf("閲覧権限でウィンドウの処理状況を参照できない: %v", err)
	}
}
