package projectstore

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 権限（members.yaml の role。3 段階）。
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// Member は members.yaml の 1 メンバー。
type Member struct {
	AuthorID    string    `yaml:"author_id"`
	DisplayName string    `yaml:"display_name"`
	Role        string    `yaml:"role"`
	AddedAt     time.Time `yaml:"added_at"`
	AddedBy     string    `yaml:"added_by"`
}

// Members は members.yaml 全体。
type Members struct {
	Members []Member `yaml:"members"`
}

// UnmarshalMembers は members.yaml のバイト列を解釈する。
func UnmarshalMembers(data []byte) (*Members, error) {
	var m Members
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("members.yaml を解釈できません: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Marshal は members.yaml のバイト列を組み立てる。
func (m *Members) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("members.yaml を組み立てられません: %w", err)
	}
	return out, nil
}

// Validate は members.yaml の制約を検証する。
// オーナーは常に 1 名以上（最後のオーナーの削除・降格は書き込み前に拒否する。メンバーを管理できる人が居なくならないように）。
func (m *Members) Validate() error {
	if len(m.Members) == 0 {
		return fmt.Errorf("members.yaml にメンバーが 1 名もいません")
	}
	seen := map[string]bool{}
	owners := 0
	for i, mem := range m.Members {
		if _, err := NormalizeAuthorID(mem.AuthorID); err != nil {
			return fmt.Errorf("members.yaml の %d 件目: %w", i+1, err)
		}
		if seen[mem.AuthorID] {
			return fmt.Errorf("members.yaml に同じメールアドレスが重複しています: %q", mem.AuthorID)
		}
		seen[mem.AuthorID] = true
		if mem.DisplayName == "" {
			return fmt.Errorf("members.yaml の %q に表示名がありません", mem.AuthorID)
		}
		switch mem.Role {
		case RoleOwner:
			owners++
		case RoleEditor, RoleViewer:
		default:
			return fmt.Errorf("members.yaml の %q の権限が %q / %q / %q 以外です: %q",
				mem.AuthorID, RoleOwner, RoleEditor, RoleViewer, mem.Role)
		}
		if mem.AddedAt.IsZero() {
			return fmt.Errorf("members.yaml の %q に登録日時がありません", mem.AuthorID)
		}
		if mem.AddedBy == "" {
			return fmt.Errorf("members.yaml の %q に登録者がありません", mem.AuthorID)
		}
	}
	if owners == 0 {
		return fmt.Errorf("members.yaml にオーナーがいません（オーナーは常に 1 名以上必要です）")
	}
	return nil
}

// Find は利用者 ID に一致するメンバーを返す（メンバー照合）。
func (m *Members) Find(authorID string) (Member, bool) {
	for _, mem := range m.Members {
		if mem.AuthorID == authorID {
			return mem, true
		}
	}
	return Member{}, false
}

// ---- メンバー管理の書き込み ------------------------------------------------------
//
// いずれの操作も `locks/members`（同一端末の多重プロセス保護）の短時間ロック内で
// 読み直し → 検証 → 原子的書き込みを行う（同じ作業コピーを開いた別ウィンドウの同時変更を後勝ちで踏まない）。
// 他メンバーとの整合は取り込み時のエントリ単位の三面マージが担う（同期モジュール側）。
// **変更履歴への記録は呼び出し側**（バインディング層）が行う（roster.yaml と同じ分担）。
// 権限の判定も呼び出し側の前段共通実装に置く（ここでは不変条件だけを守る）。

// AddMember はメンバーを追加する（オーナーのみが呼ぶ）。
//
// 利用者 ID は正規化して保持し、既存メンバーとの重複を拒否する（端末に依存しない識別子として比べる）。
func (s *Store) AddMember(authorID, displayName, role string) (Member, error) {
	normalized, err := NormalizeAuthorID(authorID)
	if err != nil {
		return Member{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return Member{}, fmt.Errorf("メンバーの表示名を入力してください")
	}
	if !validRole(role) {
		return Member{}, fmt.Errorf("権限が不正です（%s / %s / %s のいずれか）: %q",
			RoleOwner, RoleEditor, RoleViewer, role)
	}

	var added Member
	err = s.WithShortLock(LockMembers, func() error {
		members, err := s.LoadMembers()
		if err != nil {
			return err
		}
		if _, exists := members.Find(normalized); exists {
			return fmt.Errorf("このメールアドレスは既にメンバーです（%s）", normalized)
		}
		added = Member{
			AuthorID: normalized, DisplayName: displayName, Role: role,
			AddedAt: time.Now().UTC().Truncate(time.Second), AddedBy: s.author.AuthorID,
		}
		members.Members = append(members.Members, added)
		return s.SaveMembers(members)
	})
	if err != nil {
		return Member{}, err
	}
	return added, nil
}

// RemoveMember はメンバーを削除する。
//
// 最後のオーナーは削除できない（オーナーは常に 1 名以上）。
func (s *Store) RemoveMember(authorID string) (Member, error) {
	var removed Member
	err := s.WithShortLock(LockMembers, func() error {
		members, err := s.LoadMembers()
		if err != nil {
			return err
		}
		idx, err := members.indexOf(authorID)
		if err != nil {
			return err
		}
		if members.Members[idx].Role == RoleOwner && members.countOwners() == 1 {
			return errLastOwner("削除")
		}
		removed = members.Members[idx]
		members.Members = append(members.Members[:idx], members.Members[idx+1:]...)
		return s.SaveMembers(members)
	})
	if err != nil {
		return Member{}, err
	}
	return removed, nil
}

// ChangeMemberRole はメンバーの権限を変更し、変更前後を返す。
//
// 最後のオーナーを降格できない。オーナーへの昇格（移譲）も本メソッドで行う。
func (s *Store) ChangeMemberRole(authorID, role string) (before, after Member, err error) {
	if !validRole(role) {
		return Member{}, Member{}, fmt.Errorf("権限が不正です（%s / %s / %s のいずれか）: %q",
			RoleOwner, RoleEditor, RoleViewer, role)
	}
	err = s.WithShortLock(LockMembers, func() error {
		members, loadErr := s.LoadMembers()
		if loadErr != nil {
			return loadErr
		}
		idx, findErr := members.indexOf(authorID)
		if findErr != nil {
			return findErr
		}
		before = members.Members[idx]
		if before.Role == role {
			after = before
			return nil // 変更なし（書き込みも履歴も生じない）
		}
		if before.Role == RoleOwner && role != RoleOwner && members.countOwners() == 1 {
			return errLastOwner("降格")
		}
		members.Members[idx].Role = role
		after = members.Members[idx]
		return s.SaveMembers(members)
	})
	if err != nil {
		return Member{}, Member{}, err
	}
	return before, after, nil
}

// TakeOverOwner はオーナー不在時に呼び出し元の作業者がオーナーを引き継ぐ。
//
// 確認操作（警告と明示同意）は呼び出し側の責務であり、ここでは呼び出し元をオーナーへ昇格させる。
// 既存のオーナーは降格しない（オーナーの複数在籍を許す = 最後のオーナーが不在の状況を作らない）。
func (s *Store) TakeOverOwner() (Member, error) {
	var taken Member
	err := s.WithShortLock(LockMembers, func() error {
		members, err := s.LoadMembers()
		if err != nil {
			return err
		}
		idx, err := members.indexOf(s.author.AuthorID)
		if err != nil {
			return err
		}
		if members.Members[idx].Role == RoleOwner {
			return fmt.Errorf("すでにオーナーです（引き継ぎの必要はありません）")
		}
		members.Members[idx].Role = RoleOwner
		taken = members.Members[idx]
		return s.SaveMembers(members)
	})
	if err != nil {
		return Member{}, err
	}
	return taken, nil
}

// CorrectAuthorID はメンバーの利用者 ID を訂正し、変更前後を返す（オーナーのみが呼ぶ）。
//
// 改姓等で組織メール/UPN が変わった場合の訂正であり、**権限・登録日時・登録者は変えない**
// （過去記録との対応を保つ）。訂正先が既存メンバーと重複する場合は拒否する。
func (s *Store) CorrectAuthorID(oldAuthorID, newAuthorID string) (before, after Member, err error) {
	normalized, err := NormalizeAuthorID(newAuthorID)
	if err != nil {
		return Member{}, Member{}, err
	}
	err = s.WithShortLock(LockMembers, func() error {
		members, loadErr := s.LoadMembers()
		if loadErr != nil {
			return loadErr
		}
		idx, findErr := members.indexOf(oldAuthorID)
		if findErr != nil {
			return findErr
		}
		if members.Members[idx].AuthorID == normalized {
			return fmt.Errorf("訂正前と同じメールアドレスです（%s）", normalized)
		}
		if _, exists := members.Find(normalized); exists {
			return fmt.Errorf("このメールアドレスは既に別のメンバーが使っています（%s）", normalized)
		}
		before = members.Members[idx]
		members.Members[idx].AuthorID = normalized
		after = members.Members[idx]
		return s.SaveMembers(members)
	})
	if err != nil {
		return Member{}, Member{}, err
	}
	return before, after, nil
}

// indexOf は利用者 ID の位置を返す（未登録は利用者向けの理由つきエラー）。
func (m *Members) indexOf(authorID string) (int, error) {
	for i := range m.Members {
		if m.Members[i].AuthorID == authorID {
			return i, nil
		}
	}
	return 0, fmt.Errorf("メンバーに登録されていないメールアドレスです（%s）。一覧から選び直してください", authorID)
}

// countOwners はオーナーの人数を返す。
func (m *Members) countOwners() int {
	n := 0
	for _, mem := range m.Members {
		if mem.Role == RoleOwner {
			n++
		}
	}
	return n
}

// errLastOwner は最後のオーナーを失う操作の拒否理由（原因＋次の行動の 1 文）。
func errLastOwner(operation string) error {
	return fmt.Errorf("最後のオーナーは%sできません。先に別のメンバーへオーナーを移譲してください。", operation)
}

// validRole は権限の値集合（3 値で閉じている）。
func validRole(role string) bool {
	switch role {
	case RoleOwner, RoleEditor, RoleViewer:
		return true
	default:
		return false
	}
}
