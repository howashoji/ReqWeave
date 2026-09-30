package projectstore

// 単体テスト（メンバー一覧の不変条件）。実行: make -C app test-unit

import (
	"strings"
	"testing"
	"time"
)

func testMembers(roles ...string) *Members {
	m := &Members{}
	for i, role := range roles {
		m.Members = append(m.Members, Member{
			AuthorID:    string(rune('a'+i)) + ".sato@example.co.jp",
			DisplayName: "作業者" + string(rune('A'+i)),
			Role:        role,
			AddedAt:     time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
			AddedBy:     "a.sato@example.co.jp",
		})
	}
	return m
}

// オーナーは常に 1 名以上（オーナー 0 名の状態を保存できない）。
func TestMembersValidateRequiresOwner(t *testing.T) {
	if err := testMembers(RoleOwner, RoleEditor).Validate(); err != nil {
		t.Fatalf("正常な一覧が検証で落ちた: %v", err)
	}
	err := testMembers(RoleEditor, RoleViewer).Validate()
	if err == nil || !strings.Contains(err.Error(), "オーナー") {
		t.Fatalf("オーナー不在が検出されない: %v", err)
	}
}

func TestMembersCountOwnersAndIndexOf(t *testing.T) {
	m := testMembers(RoleOwner, RoleOwner, RoleViewer)
	if got := m.countOwners(); got != 2 {
		t.Errorf("オーナー数が違う: %d", got)
	}
	if _, err := m.indexOf("c.sato@example.co.jp"); err != nil {
		t.Errorf("在籍メンバーを引けない: %v", err)
	}
	if _, err := m.indexOf("z.sato@example.co.jp"); err == nil {
		t.Error("未登録の利用者 ID が引けてしまった")
	}
}

func TestValidRole(t *testing.T) {
	for _, role := range []string{RoleOwner, RoleEditor, RoleViewer} {
		if !validRole(role) {
			t.Errorf("正しい権限が拒否された: %q", role)
		}
	}
	for _, role := range []string{"", "admin", "OWNER"} {
		if validRole(role) {
			t.Errorf("値集合外の権限が受理された: %q", role)
		}
	}
}
