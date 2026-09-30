package binding

// 本ファイルは権限判定のバインディング前段の共通実装。
//
// 権限はオーナー / 編集 / 閲覧の 3 段階。個別画面・個別機能に判定を分散させない
// （判定が散らばると、画面ごとに許可の範囲がずれるため）。閲覧権限では編集操作を拒否し、
// 理由を日本語 1 文で返す（画面の「無効化＋理由表示」に対応）。

import (
	"fmt"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// roleRank は権限の強さ。値はオーナー / 編集 / 閲覧の 3 つに限る。
func roleRank(role string) int {
	switch role {
	case projectstore.RoleOwner:
		return 3
	case projectstore.RoleEditor:
		return 2
	case projectstore.RoleViewer:
		return 1
	default:
		return 0
	}
}

// authorRole は開いているプロジェクトでの作業者の権限を返す（members.yaml から読む）。
func authorRole(store *projectstore.Store) (string, error) {
	members, err := loadMembers(store.Root())
	if err != nil {
		return "", fmt.Errorf("メンバー一覧を読み込めません。プロジェクトを開き直してください。")
	}
	member, ok := members.Find(store.Author().AuthorID)
	if !ok {
		return "", fmt.Errorf("このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。")
	}
	return member.Role, nil
}

// requireRole は operation に必要な権限を満たさない場合に理由つきで拒否し、**動作ログへ残す**。
// operation は利用者向けの操作名（「名簿の登録」等）。内部 ID・コード値を含めない。
//
// 記録するのは共同作業系の「対象・失敗種別」（= 操作名と拒否の種別）だけで、
// **利用者 ID・メンバー一覧・プロジェクトのパスは残さない**。
// **押せるかどうかの問い合わせ（CurrentPermission）では記録しない**（画面を描くたびに
// 拒否が積もると、実際に試した操作が埋もれる）。そちらは roleDenial を使う。
func (a *API) requireRole(store *projectstore.Store, min string, operation string) error {
	kind, err := roleDenial(store, min, operation)
	if err == nil {
		return nil
	}
	a.recordCollabDenied(operation, kind, "")
	return err
}

// roleDenial は拒否の種別と理由を返す（**記録しない**）。満たしているときは種別が空で理由が nil。
func roleDenial(store *projectstore.Store, min string, operation string) (string, error) {
	role, err := authorRole(store)
	if err != nil {
		return collabKindNotMember, err
	}
	if roleRank(role) >= roleRank(min) {
		return "", nil
	}
	return collabKindRole, fmt.Errorf(
		"%sは%s権限が必要です（現在は%s）。オーナーに権限の変更を依頼してください。",
		operation, roleLabel(min), roleLabel(role))
}

// PermissionView は開いているプロジェクトでの操作可否（画面の「無効化＋理由表示」用）。
//
// 画面はこの値を表示・無効化の根拠に使い、権限の判定そのものは行わない
// （判定は本ファイルの requireRole に一本化する）。
type PermissionView struct {
	Role      string `json:"role"`
	RoleLabel string `json:"roleLabel"`
	// CanEdit は編集権限以上か（取り込み・分析・承認・観点編集の可否）。
	CanEdit bool `json:"canEdit"`
	// Reason は編集操作ができない理由（できる場合は空）。原因＋次の行動の 1 文。
	Reason string `json:"reason,omitempty"`
	// CanManageMembers はオーナー権限か（メンバー管理・プロジェクト削除の可否）。
	CanManageMembers bool `json:"canManageMembers"`
	// ManageReason はメンバー管理ができない理由（できる場合は空）。
	ManageReason string `json:"manageReason,omitempty"`
	// CanManageUsageLimit は AI 利用量上限の設定・変更・解除ができるか（オーナーのみ）。
	CanManageUsageLimit bool `json:"canManageUsageLimit"`
	// UsageLimitReason は上限設定ができない理由（できる場合は空）。
	UsageLimitReason string `json:"usageLimitReason,omitempty"`
}

// CurrentPermission は開いているプロジェクトでの操作可否を返す。
func (a *API) CurrentPermission() (PermissionView, error) {
	s, err := a.current()
	if err != nil {
		return PermissionView{}, err
	}
	role, err := authorRole(s.store)
	if err != nil {
		return PermissionView{}, err
	}
	// **押せるかどうかの問い合わせでは記録しない**（実際に試した操作だけを残す）。
	out := PermissionView{Role: role, RoleLabel: roleLabel(role)}
	if _, err := roleDenial(s.store, projectstore.RoleOwner, "メンバー管理"); err != nil {
		out.ManageReason = err.Error()
	} else {
		out.CanManageMembers = true
	}
	if _, err := roleDenial(s.store, projectstore.RoleOwner, "AI 利用量上限の設定"); err != nil {
		out.UsageLimitReason = err.Error()
	} else {
		out.CanManageUsageLimit = true
	}
	if _, err := roleDenial(s.store, projectstore.RoleEditor, "この操作"); err != nil {
		out.Reason = err.Error()
		return out, nil
	}
	out.CanEdit = true
	return out, nil
}
