package projectstore

import (
	"strings"
	"testing"
)

const validMembersYAML = `members:
  - author_id: k.sato@example.co.jp
    display_name: 佐藤
    role: owner
    added_at: 2026-08-27T05:00:00Z
    added_by: k.sato@example.co.jp
  - author_id: t.suzuki@example.co.jp
    display_name: 鈴木
    role: editor
    added_at: 2026-08-27T06:00:00Z
    added_by: k.sato@example.co.jp
`

func TestUnmarshalMembers(t *testing.T) {
	m, err := UnmarshalMembers([]byte(validMembersYAML))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if len(m.Members) != 2 {
		t.Fatalf("件数が違う: %d", len(m.Members))
	}
	owner, ok := m.Find("k.sato@example.co.jp")
	if !ok {
		t.Fatal("メンバー照合に失敗")
	}
	if owner.Role != RoleOwner || owner.DisplayName != "佐藤" {
		t.Errorf("メンバーの内容が違う: %+v", owner)
	}
	if _, ok := m.Find("unknown@example.co.jp"); ok {
		t.Error("メンバー外が照合された")
	}
}

// オーナーは常に 1 名以上。利用者 ID は重複しない。
func TestMembersValidateRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"メンバーが空":   "members: []\n",
		"オーナーがいない": strings.Replace(validMembersYAML, "role: owner", "role: editor", 1),
		"利用者 ID の重複": strings.Replace(validMembersYAML, "t.suzuki@example.co.jp\n    display_name: 鈴木",
			"k.sato@example.co.jp\n    display_name: 鈴木", 1),
		"権限が値集合外":    strings.Replace(validMembersYAML, "role: editor", "role: admin", 1),
		"表示名が無い":     strings.Replace(validMembersYAML, "display_name: 佐藤", `display_name: ""`, 1),
		"登録日時が無い":    strings.Replace(validMembersYAML, "    added_at: 2026-08-27T05:00:00Z\n", "", 1),
		"登録者が無い":     strings.Replace(validMembersYAML, "    added_by: k.sato@example.co.jp\n", "", 1),
		"利用者 ID が不正": strings.Replace(validMembersYAML, "author_id: k.sato@example.co.jp", "author_id: ksato", 1),
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if m, err := UnmarshalMembers([]byte(y)); err == nil {
				t.Errorf("不正な members.yaml が受理された: %+v", m)
			}
		})
	}
}

func TestMembersRoundTrip(t *testing.T) {
	m, err := UnmarshalMembers([]byte(validMembersYAML))
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	out, err := m.Marshal()
	if err != nil {
		t.Fatalf("組み立てに失敗: %v", err)
	}
	again, err := UnmarshalMembers(out)
	if err != nil {
		t.Fatalf("再解釈に失敗:\n%s\n%v", out, err)
	}
	if len(again.Members) != len(m.Members) {
		t.Errorf("往復で件数が変わった: %d → %d", len(m.Members), len(again.Members))
	}
	for i := range m.Members {
		if again.Members[i] != m.Members[i] {
			t.Errorf("往復で内容が変わった: %+v → %+v", m.Members[i], again.Members[i])
		}
	}
}
