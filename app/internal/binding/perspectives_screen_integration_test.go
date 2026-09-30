//go:build integration

package binding

import (
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newPerspectiveAPI は観点操作用にプロジェクトを開いた API を返す（AI 呼び出しは行わない）。
func newPerspectiveAPI(t *testing.T) (*API, string, *streamingStub) {
	t.Helper()
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	return a, root, stub
}

// ヒアリング観点は手動の追加・編集・削除ができ、AI 呼び出しを伴わず、変更履歴へ記録される。
func TestPerspectiveBindingManualLifecycle(t *testing.T) {
	a, root, stub := newPerspectiveAPI(t)

	list, err := a.Perspectives()
	if err != nil {
		t.Fatalf("一覧を取得できない: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("初期状態が空ではない: %+v", list)
	}

	added, err := a.AddPerspective(PerspectiveRequest{
		Name: "棚卸の差異処理", Summary: "差異の承認経路と記録方法を確認する"})
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if added.ID != "PRS-001" || added.TopicKey != "custom/PRS-001" {
		t.Fatalf("登録結果が違う: %+v", added)
	}
	if added.Origin != projectstore.PerspectiveOriginManual || added.OriginLabel != "手動で登録" {
		t.Errorf("由来が手動登録になっていない: %+v", added)
	}
	if added.Evidence != "" {
		t.Errorf("手動登録に根拠が入っている: %+v", added)
	}

	updated, err := a.UpdatePerspective(PerspectiveRequest{
		ID: added.ID, Name: "棚卸の差異処理と承認", Summary: "差異の確定者と締め時刻を確認する"})
	if err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	if updated.Name != "棚卸の差異処理と承認" || updated.TopicKey != added.TopicKey {
		t.Fatalf("変更結果が違う: %+v", updated)
	}

	second, err := a.AddPerspective(PerspectiveRequest{Name: "入出庫の権限", Summary: "誰が確定できるか"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RemovePerspective(second.ID); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}

	list, err = a.Perspectives()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != added.ID {
		t.Fatalf("削除済みが一覧に残っている・変更が反映されていない: %+v", list)
	}

	// AI プロバイダは 1 度も呼ばれない（AI API 障害中でも操作できる）。
	if stub.calls != 0 {
		t.Errorf("手動操作で AI プロバイダが呼ばれた: %d 回", stub.calls)
	}

	// 変更履歴（target: PRS-nnn / change: created・updated・removed）。
	changes, err := auditlog.ReadChanges(root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	got := map[string]auditlog.ChangeRecord{}
	for _, c := range changes {
		if strings.HasPrefix(c.Target, "PRS-") {
			got[c.Target+"/"+c.Change] = c
		}
	}
	created, ok := got[added.ID+"/"+auditlog.ChangeCreated]
	if !ok || created.After != "棚卸の差異処理" || created.Author != "k.sato@example.co.jp" {
		t.Errorf("登録の記録が違う: %+v", created)
	}
	updatedRec, ok := got[added.ID+"/"+auditlog.ChangeUpdated]
	if !ok || updatedRec.Before != "棚卸の差異処理" || updatedRec.After != "棚卸の差異処理と承認" {
		t.Errorf("変更の記録が違う: %+v", updatedRec)
	}
	removedRec, ok := got[second.ID+"/"+auditlog.ChangeRemoved]
	if !ok || removedRec.Before != "入出庫の権限" {
		t.Errorf("削除の記録が違う: %+v", removedRec)
	}
}

// 閲覧権限では追加・編集・削除が拒否され、参照はできる。
func TestPerspectiveBindingDeniesViewer(t *testing.T) {
	a, root, _ := newPerspectiveAPI(t)
	added, err := a.AddPerspective(PerspectiveRequest{Name: "棚卸の差異処理", Summary: "要旨"})
	if err != nil {
		t.Fatalf("前提の登録に失敗: %v", err)
	}

	demoteToViewer(t, root, "k.sato@example.co.jp")

	if _, err := a.AddPerspective(PerspectiveRequest{Name: "入出庫の権限", Summary: "要旨"}); err == nil {
		t.Fatalf("閲覧権限で登録できてしまった")
	} else if !strings.Contains(err.Error(), "編集権限が必要です") {
		t.Fatalf("理由が示されていない: %v", err)
	}
	if _, err := a.UpdatePerspective(PerspectiveRequest{ID: added.ID, Name: "別名", Summary: "要旨"}); err == nil {
		t.Fatalf("閲覧権限で変更できてしまった")
	}
	if err := a.RemovePerspective(added.ID); err == nil {
		t.Fatalf("閲覧権限で削除できてしまった")
	}

	list, err := a.Perspectives()
	if err != nil {
		t.Fatalf("閲覧権限で一覧を取得できない: %v", err)
	}
	if len(list) != 1 || list[0].Name != "棚卸の差異処理" {
		t.Fatalf("一覧の内容が違う: %+v", list)
	}
}

// ヒアリング観点の必須項目は画面からの入力でも書き込み前に拒否する。
func TestPerspectiveBindingRejectsEmptyFields(t *testing.T) {
	a, _, _ := newPerspectiveAPI(t)
	if _, err := a.AddPerspective(PerspectiveRequest{Name: "", Summary: "要旨"}); err == nil {
		t.Fatalf("観点名なしが受理された")
	}
	if _, err := a.AddPerspective(PerspectiveRequest{Name: "観点", Summary: " "}); err == nil {
		t.Fatalf("要旨なしが受理された")
	}
	if err := a.RemovePerspective("PRS-999"); err == nil {
		t.Fatalf("未登録の観点が削除できた")
	}
}
