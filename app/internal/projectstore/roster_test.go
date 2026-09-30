package projectstore

import "testing"

func TestRosterValidate(t *testing.T) {
	cases := map[string]struct {
		roster  Roster
		wantErr bool
	}{
		"正常": {Roster{Stakeholders: []Stakeholder{
			{ID: "STK-001", Name: "佐藤", Org: "営業部"},
			{ID: "STK-002", Name: "鈴木", Org: "物流部"},
		}}, false},
		"空の名簿":    {Roster{}, false},
		"ID 形式違い": {Roster{Stakeholders: []Stakeholder{{ID: "STK-1", Name: "佐藤", Org: "営業部"}}}, true},
		"ID なし":   {Roster{Stakeholders: []Stakeholder{{Name: "佐藤", Org: "営業部"}}}, true},
		"ID 重複":   {Roster{Stakeholders: []Stakeholder{{ID: "STK-001", Name: "佐藤", Org: "営業部"}, {ID: "STK-001", Name: "鈴木", Org: "物流部"}}}, true},
		"氏名なし":    {Roster{Stakeholders: []Stakeholder{{ID: "STK-001", Org: "営業部"}}}, true},
		"氏名が空白のみ": {Roster{Stakeholders: []Stakeholder{{ID: "STK-001", Name: "  ", Org: "営業部"}}}, true},
		"所属なし":    {Roster{Stakeholders: []Stakeholder{{ID: "STK-001", Name: "佐藤"}}}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.roster.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("エラーを期待しましたが nil でした")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("エラーを期待していません: %v", err)
			}
		})
	}
}

func TestRosterFind(t *testing.T) {
	r := &Roster{Stakeholders: []Stakeholder{
		{ID: "STK-001", Name: "佐藤", Org: "営業部"},
		{ID: "STK-002", Name: "鈴木", Org: "物流部"},
	}}
	got, ok := r.Find("STK-002")
	if !ok || got.Name != "鈴木" || got.Org != "物流部" {
		t.Fatalf("宛先を引けません: %+v (ok=%v)", got, ok)
	}
	if _, ok := r.Find("STK-003"); ok {
		t.Fatalf("存在しない宛先が引けました")
	}
}
