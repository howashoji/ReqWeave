//go:build integration

package binding

import (
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newMemberAPI はメンバー管理用にプロジェクトを開いた API を返す（作成者 = オーナー）。
func newMemberAPI(t *testing.T) (*API, string) {
	t.Helper()
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	return a, root
}

// changeOf は対象・種別に一致する変更履歴を返す。
func changeOf(t *testing.T, root, target, kind string) *auditlog.ChangeRecord {
	t.Helper()
	changes, err := auditlog.ReadChanges(root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	for i := range changes {
		if changes[i].Target == target && changes[i].Change == kind {
			return &changes[i]
		}
	}
	return nil
}

// 追加・権限変更・削除がバインディング経由で行え、変更履歴へ記録される。
func TestMemberBindingLifecycleAndHistory(t *testing.T) {
	a, root := newMemberAPI(t)

	list, err := a.Members()
	if err != nil {
		t.Fatalf("一覧を取得できない: %v", err)
	}
	if len(list) != 1 || list[0].RoleLabel != "オーナー" || !list[0].Self {
		t.Fatalf("作成者がオーナーとして在籍していない: %+v", list)
	}

	added, err := a.AddMember(MemberRequest{
		AuthorID: "Y.Suzuki@Example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor})
	if err != nil {
		t.Fatalf("追加に失敗: %v", err)
	}
	if added.AuthorID != "y.suzuki@example.co.jp" || added.RoleLabel != "編集" || added.Self {
		t.Fatalf("追加結果が違う: %+v", added)
	}
	if rec := changeOf(t, root, added.AuthorID, auditlog.ChangeMemberAdded); rec == nil {
		t.Error("追加が変更履歴にない")
	} else if !strings.Contains(rec.After, "鈴木") || rec.Author != "k.sato@example.co.jp" {
		t.Errorf("追加の記録内容が違う: %+v", *rec)
	}

	changed, err := a.ChangeMemberRole(MemberRequest{
		AuthorID: added.AuthorID, Role: projectstore.RoleViewer})
	if err != nil {
		t.Fatalf("権限変更に失敗: %v", err)
	}
	if changed.RoleLabel != "閲覧" {
		t.Fatalf("権限変更の結果が違う: %+v", changed)
	}
	if rec := changeOf(t, root, added.AuthorID, auditlog.ChangeRoleChanged); rec == nil {
		t.Error("権限変更が変更履歴にない")
	} else if rec.Before != "編集" || rec.After != "閲覧" {
		t.Errorf("権限変更の記録内容が違う: %+v", *rec)
	}

	if err := a.RemoveMember(added.AuthorID); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if rec := changeOf(t, root, added.AuthorID, auditlog.ChangeMemberRemoved); rec == nil {
		t.Error("削除が変更履歴にない")
	} else if !strings.Contains(rec.Before, "鈴木") {
		t.Errorf("削除の記録内容が違う: %+v", *rec)
	}

	// 最後のオーナーは削除・降格できない（理由が示される）。
	if err := a.RemoveMember("k.sato@example.co.jp"); err == nil {
		t.Error("最後のオーナーが削除できた")
	} else if !strings.Contains(err.Error(), "移譲") {
		t.Errorf("次の行動が案内されていない: %v", err)
	}
}

// 利用者 ID の訂正（オーナーのみ）と変更履歴への旧値・新値の記録。
func TestMemberBindingCorrectAuthorID(t *testing.T) {
	a, root := newMemberAPI(t)
	added, err := a.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor})
	if err != nil {
		t.Fatal(err)
	}

	corrected, err := a.CorrectMemberAuthorID(added.AuthorID, "y.tanaka@example.co.jp")
	if err != nil {
		t.Fatalf("訂正に失敗: %v", err)
	}
	if corrected.AuthorID != "y.tanaka@example.co.jp" || corrected.RoleLabel != "編集" {
		t.Fatalf("訂正結果が違う: %+v", corrected)
	}
	rec := changeOf(t, root, corrected.AuthorID, auditlog.ChangeMemberIDCorrected)
	if rec == nil {
		t.Fatal("訂正が変更履歴にない")
	}
	if rec.Before != "y.suzuki@example.co.jp" || rec.After != "y.tanaka@example.co.jp" {
		t.Errorf("訂正の前後が記録されていない: %+v", *rec)
	}
}

// オーナー不在時の引き継ぎは確認操作を経てのみ実行できる。
func TestMemberBindingTakeOverOwnerRequiresConfirmation(t *testing.T) {
	a, root := newMemberAPI(t)
	// 自分を編集権限へ落とす（オーナーは連絡のつかない別メンバー）。
	demoteToViewer(t, root, "k.sato@example.co.jp")
	if _, _, err := storeOf(t, a).ChangeMemberRole("k.sato@example.co.jp", projectstore.RoleEditor); err != nil {
		t.Fatal(err)
	}

	if _, err := a.TakeOverOwner(false); err == nil {
		t.Fatal("確認なしで引き継ぎが実行された")
	} else if !strings.Contains(err.Error(), "確認") {
		t.Errorf("確認が必要である旨が示されない: %v", err)
	}
	if changeOf(t, root, "k.sato@example.co.jp", auditlog.ChangeOwnerTakeover) != nil {
		t.Fatal("確認なしの呼び出しで履歴が記録された")
	}

	taken, err := a.TakeOverOwner(true)
	if err != nil {
		t.Fatalf("引き継ぎに失敗: %v", err)
	}
	if taken.RoleLabel != "オーナー" {
		t.Fatalf("引き継ぎ結果が違う: %+v", taken)
	}
	if rec := changeOf(t, root, taken.AuthorID, auditlog.ChangeOwnerTakeover); rec == nil {
		t.Error("引き継ぎが変更履歴にない")
	} else if rec.After != "オーナー" {
		t.Errorf("引き継ぎの記録内容が違う: %+v", *rec)
	}
}

// メンバー管理はオーナーのみ。閲覧・編集は拒否され、参照はできる。
func TestMemberBindingDeniesNonOwner(t *testing.T) {
	a, root := newMemberAPI(t)
	if _, err := a.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	demoteToViewer(t, root, "k.sato@example.co.jp")

	if _, err := a.AddMember(MemberRequest{
		AuthorID: "y.tanaka@example.co.jp", DisplayName: "田中", Role: projectstore.RoleViewer}); err == nil {
		t.Error("閲覧権限でメンバーを追加できた")
	} else if !strings.Contains(err.Error(), "オーナー権限が必要です") {
		t.Errorf("理由が示されていない: %v", err)
	}
	if err := a.RemoveMember("y.suzuki@example.co.jp"); err == nil {
		t.Error("閲覧権限でメンバーを削除できた")
	}
	if _, err := a.ChangeMemberRole(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", Role: projectstore.RoleOwner}); err == nil {
		t.Error("閲覧権限で権限を変更できた")
	}
	if _, err := a.CorrectMemberAuthorID("y.suzuki@example.co.jp", "y.tanaka@example.co.jp"); err == nil {
		t.Error("閲覧権限で利用者 ID を訂正できた")
	}
	// 引き継ぎは編集権限以上（閲覧では拒否）。
	if _, err := a.TakeOverOwner(true); err == nil {
		t.Error("閲覧権限でオーナーを引き継げた")
	}

	// 参照は閲覧権限でもできる。
	list, err := a.Members()
	if err != nil {
		t.Fatalf("閲覧権限で一覧を取得できない: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("一覧の件数が違う: %+v", list)
	}
}

// storeOf は開いているプロジェクトの Store を返す（前提づくり用）。
func storeOf(t *testing.T, a *API) *projectstore.Store {
	t.Helper()
	s, err := a.current()
	if err != nil {
		t.Fatal(err)
	}
	return s.store
}
