package binding

// 本ファイルはメンバー管理画面のバインディング。
//
// メンバー管理はオーナーのみ。判定は前段の共通実装（requireRole）で行い、
// 画面側の判定に依存しない。唯一の例外はオーナー不在時の引き継ぎで、編集権限のメンバーが
// **確認操作を経たこと**（明示同意の引数）を示した場合にのみ実行できる（オーナーがいないと誰もメンバーを管理できなくなるため）。
//
// 変更は変更履歴へ記録する（member-added / member-removed / role-changed /
// owner-takeover / member-id-corrected）。AI 呼び出しは伴わない。

import (
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// MemberView はメンバー一覧の 1 行。
type MemberView struct {
	AuthorID    string `json:"authorId"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	// RoleLabel は画面表示用の権限（内部の値をそのまま出さない）。
	RoleLabel string `json:"roleLabel"`
	AddedAt   string `json:"addedAt"`
	AddedBy   string `json:"addedBy"`
	// Self は自分自身の行か（自分の権限の表示に使う）。
	Self bool `json:"self"`
}

// MemberRequest はメンバーの追加・変更の入力。
type MemberRequest struct {
	AuthorID    string `json:"authorId"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

func memberView(m projectstore.Member, self string) MemberView {
	return MemberView{
		AuthorID: m.AuthorID, DisplayName: m.DisplayName,
		Role: m.Role, RoleLabel: roleLabel(m.Role),
		AddedAt: m.AddedAt.Local().Format("2006-01-02 15:04"), AddedBy: m.AddedBy,
		Self: m.AuthorID == self,
	}
}

// memberLabel は変更履歴へ残す表示（誰が・どの権限で在籍したか）。
func memberLabel(m projectstore.Member) string {
	return fmt.Sprintf("%s（%s / %s）", m.DisplayName, m.AuthorID, roleLabel(m.Role))
}

// Members はメンバー一覧を返す（閲覧権限でも参照できる）。
func (a *API) Members() ([]MemberView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	members, err := s.store.LoadMembers()
	if err != nil {
		return nil, err
	}
	self := s.store.Author().AuthorID
	out := make([]MemberView, 0, len(members.Members))
	for _, m := range members.Members {
		out = append(out, memberView(m, self))
	}
	return out, nil
}

// AddMember はメンバーを追加する（オーナーのみ）。
func (a *API) AddMember(req MemberRequest) (MemberView, error) {
	s, err := a.current()
	if err != nil {
		return MemberView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "メンバーの追加"); err != nil {
		return MemberView{}, err
	}
	added, err := s.store.AddMember(req.AuthorID, req.DisplayName, req.Role)
	if err != nil {
		return MemberView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: added.AuthorID, Change: auditlog.ChangeMemberAdded, After: memberLabel(added),
	})
	return memberView(added, s.store.Author().AuthorID), nil
}

// RemoveMember はメンバーを削除する（オーナーのみ。最後のオーナーは削除できない）。
func (a *API) RemoveMember(authorID string) error {
	s, err := a.current()
	if err != nil {
		return err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "メンバーの削除"); err != nil {
		return err
	}
	removed, err := s.store.RemoveMember(authorID)
	if err != nil {
		return err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: removed.AuthorID, Change: auditlog.ChangeMemberRemoved, Before: memberLabel(removed),
	})
	return nil
}

// ChangeMemberRole はメンバーの権限を変更する（オーナー移譲を含む。オーナーのみ）。
func (a *API) ChangeMemberRole(req MemberRequest) (MemberView, error) {
	s, err := a.current()
	if err != nil {
		return MemberView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "権限の変更"); err != nil {
		return MemberView{}, err
	}
	before, after, err := s.store.ChangeMemberRole(req.AuthorID, req.Role)
	if err != nil {
		return MemberView{}, err
	}
	if before.Role != after.Role {
		a.recordChange(s, auditlog.ChangeRecord{
			At: time.Now().UTC(), Author: s.store.Author().AuthorID,
			Target: after.AuthorID, Change: auditlog.ChangeRoleChanged,
			Before: roleLabel(before.Role), After: roleLabel(after.Role),
		})
	}
	return memberView(after, s.store.Author().AuthorID), nil
}

// TakeOverOwner はオーナー不在時に編集権限のメンバーがオーナーを引き継ぐ。
//
// confirmed は画面での確認操作（警告表示と明示の同意）を経たことを示す。
// false のときは実行しない（確認操作なしに実行される経路を持たない）。
func (a *API) TakeOverOwner(confirmed bool) (MemberView, error) {
	s, err := a.current()
	if err != nil {
		return MemberView{}, err
	}
	if !confirmed {
		return MemberView{}, fmt.Errorf(
			"オーナーの引き継ぎには確認が必要です。警告の内容を確認してから実行してください。")
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "オーナーの引き継ぎ"); err != nil {
		return MemberView{}, err
	}
	taken, err := s.store.TakeOverOwner()
	if err != nil {
		return MemberView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: taken.AuthorID, Change: auditlog.ChangeOwnerTakeover,
		Before: roleLabel(projectstore.RoleEditor), After: roleLabel(projectstore.RoleOwner),
	})
	return memberView(taken, s.store.Author().AuthorID), nil
}

// CorrectMemberAuthorID はメンバーの利用者 ID を訂正する（オーナーのみ）。
//
// 改姓等で組織メール/UPN が変わった場合の訂正であり、権限・登録日時・登録者は変わらない。
func (a *API) CorrectMemberAuthorID(oldAuthorID, newAuthorID string) (MemberView, error) {
	s, err := a.current()
	if err != nil {
		return MemberView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "メールアドレスの訂正"); err != nil {
		return MemberView{}, err
	}
	before, after, err := s.store.CorrectAuthorID(oldAuthorID, newAuthorID)
	if err != nil {
		return MemberView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: s.store.Author().AuthorID,
		Target: after.AuthorID, Change: auditlog.ChangeMemberIDCorrected,
		Before: before.AuthorID, After: after.AuthorID,
	})
	return memberView(after, s.store.Author().AuthorID), nil
}
