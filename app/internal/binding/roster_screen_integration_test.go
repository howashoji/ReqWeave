//go:build integration

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newRosterAPI は名簿操作用にプロジェクトを開いた API を返す（AI 呼び出しは行わない）。
func newRosterAPI(t *testing.T) (*API, string) {
	t.Helper()
	a, root := newDialogueAPI(t, &streamingStub{})
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	return a, root
}

// 名簿の登録・変更と、その変更履歴への記録。
func TestRosterBindingAddUpdateAndHistory(t *testing.T) {
	a, root := newRosterAPI(t)

	list, err := a.Roster()
	if err != nil {
		t.Fatalf("一覧を取得できない: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("初期状態が空ではない: %+v", list)
	}

	added, err := a.AddStakeholder(StakeholderRequest{Name: "佐藤", Org: "営業部"})
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if added.ID != "STK-001" || added.Label != "佐藤（営業部）" {
		t.Fatalf("登録結果が違う: %+v", added)
	}

	updated, err := a.UpdateStakeholder(StakeholderRequest{ID: added.ID, Name: "佐藤 一郎", Org: "営業第一部"})
	if err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	if updated.Label != "佐藤 一郎（営業第一部）" {
		t.Fatalf("変更結果が違う: %+v", updated)
	}

	list, err = a.Roster()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "佐藤 一郎" {
		t.Fatalf("一覧に変更が反映されていない: %+v", list)
	}

	// 変更履歴に作業者名つきで記録されている（target: STK-nnn）。
	changes, err := auditlog.ReadChanges(root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var created, updatedRec *auditlog.ChangeRecord
	for i := range changes {
		if changes[i].Target != added.ID {
			continue
		}
		switch changes[i].Change {
		case auditlog.ChangeCreated:
			created = &changes[i]
		case auditlog.ChangeUpdated:
			updatedRec = &changes[i]
		}
	}
	if created == nil {
		t.Fatalf("登録が変更履歴にない: %+v", changes)
	}
	if created.Author != "k.sato@example.co.jp" || created.After != "佐藤（営業部）" {
		t.Fatalf("登録の記録内容が違う: %+v", *created)
	}
	if updatedRec == nil {
		t.Fatalf("変更が変更履歴にない: %+v", changes)
	}
	if updatedRec.Before != "佐藤（営業部）" || updatedRec.After != "佐藤 一郎（営業第一部）" {
		t.Fatalf("変更の記録内容が違う: %+v", *updatedRec)
	}
}

func TestRosterBindingRejectsEmptyFields(t *testing.T) {
	a, _ := newRosterAPI(t)
	if _, err := a.AddStakeholder(StakeholderRequest{Name: "", Org: "営業部"}); err == nil {
		t.Fatalf("氏名なしが受理された")
	}
	if _, err := a.AddStakeholder(StakeholderRequest{Name: "佐藤", Org: ""}); err == nil {
		t.Fatalf("所属なしが受理された")
	}
}

// 閲覧権限では名簿を変更できず、理由が示される。
func TestRosterBindingDeniesViewer(t *testing.T) {
	a, root := newRosterAPI(t)
	added, err := a.AddStakeholder(StakeholderRequest{Name: "佐藤", Org: "営業部"})
	if err != nil {
		t.Fatalf("前提の登録に失敗: %v", err)
	}

	// 自分を閲覧権限へ落とす（オーナーは別のメンバーが持つ）。
	demoteToViewer(t, root, "k.sato@example.co.jp")

	if _, err := a.AddStakeholder(StakeholderRequest{Name: "鈴木", Org: "物流部"}); err == nil {
		t.Fatalf("閲覧権限で登録できてしまった")
	} else if !strings.Contains(err.Error(), "編集権限が必要です") {
		t.Fatalf("理由が示されていない: %v", err)
	}
	if _, err := a.UpdateStakeholder(StakeholderRequest{ID: added.ID, Name: "佐藤 一郎", Org: "営業第一部"}); err == nil {
		t.Fatalf("閲覧権限で変更できてしまった")
	}

	// 参照は閲覧権限でもできる。
	list, err := a.Roster()
	if err != nil {
		t.Fatalf("閲覧権限で一覧を取得できない: %v", err)
	}
	if len(list) != 1 || list[0].Name != "佐藤" {
		t.Fatalf("一覧の内容が違う: %+v", list)
	}
}

// demoteToViewer は members.yaml を書き換えて対象の作業者を閲覧権限にする（オーナーは別途用意する）。
func demoteToViewer(t *testing.T, root, authorID string) {
	t.Helper()
	path := filepath.Join(root, projectstore.FileMembers)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	members, err := projectstore.UnmarshalMembers(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range members.Members {
		if members.Members[i].AuthorID == authorID {
			members.Members[i].Role = projectstore.RoleViewer
		}
	}
	members.Members = append(members.Members, projectstore.Member{
		AuthorID: "owner@example.co.jp", DisplayName: "オーナー",
		Role: projectstore.RoleOwner, AddedAt: time.Now().UTC(), AddedBy: authorID,
	})
	out, err := members.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
