package projectstore

// 単体テスト（検証規則）。実行: make -C app test-unit

import (
	"strings"
	"testing"
	"time"
)

func testPerspective(id string) Perspective {
	return Perspective{
		ID: id, Name: "棚卸の差異処理", Summary: "差異の確定者と締め時刻を確認する",
		Origin:    PerspectiveOriginManual,
		CreatedAt: time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC), Author: "k.sato@example.co.jp",
	}
}

// 必須項目・値集合・ID の一意性を書き込み前に検証する。
func TestPerspectivesValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(p *Perspective)
		wantErr string
	}{
		{name: "手動登録は根拠なしで通る"},
		{name: "ID の形式違反", mutate: func(p *Perspective) { p.ID = "PRS-1" }, wantErr: "PRS-nnn の形式"},
		{name: "観点名なし", mutate: func(p *Perspective) { p.Name = "  " }, wantErr: "観点名がありません"},
		{name: "要旨なし", mutate: func(p *Perspective) { p.Summary = "" }, wantErr: "要旨がありません"},
		{name: "由来が値集合の外", mutate: func(p *Perspective) { p.Origin = "auto" }, wantErr: "由来が不正です"},
		{
			name:    "取り込み由来で根拠なし",
			mutate:  func(p *Perspective) { p.Origin = PerspectiveOriginImport },
			wantErr: "該当箇所（IMP-nnn#Lm-Ln）がありません",
		},
		{
			name: "取り込み由来で根拠あり",
			mutate: func(p *Perspective) {
				p.Origin = PerspectiveOriginImport
				p.Evidence = "IMP-002#L18-L40"
			},
		},
		{
			name:    "根拠の形式違反",
			mutate:  func(p *Perspective) { p.Origin = PerspectiveOriginImport; p.Evidence = "IMP-002" },
			wantErr: "該当箇所の形式が不正です",
		},
		{name: "登録日時なし", mutate: func(p *Perspective) { p.CreatedAt = time.Time{} }, wantErr: "登録日時がありません"},
		{name: "登録者なし", mutate: func(p *Perspective) { p.Author = "" }, wantErr: "作業者がありません"},
		{
			name:    "削除日時だけ立っている",
			mutate:  func(p *Perspective) { p.DeletedAt = time.Now().UTC() },
			wantErr: "削除記録が揃っていません",
		},
		{
			name:    "削除者だけ立っている",
			mutate:  func(p *Perspective) { p.DeletedBy = "k.sato@example.co.jp" },
			wantErr: "削除記録が揃っていません",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := testPerspective("PRS-001")
			if tt.mutate != nil {
				tt.mutate(&v)
			}
			err := (&Perspectives{Perspectives: []Perspective{v}}).Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("検証で落ちた: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("検証を通ってしまった（期待するエラー: %s）", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("エラー内容が違う: got %q, want contains %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestPerspectivesValidateRejectsDuplicateID(t *testing.T) {
	p := &Perspectives{Perspectives: []Perspective{testPerspective("PRS-001"), testPerspective("PRS-001")}}
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), "重複しています") {
		t.Fatalf("ID 重複が検出されない: %v", err)
	}
}

// 論理削除された観点は利用側（Active）から外れる。実体は残る。
func TestPerspectivesActiveExcludesSoftDeleted(t *testing.T) {
	deleted := testPerspective("PRS-002")
	deleted.DeletedAt = time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	deleted.DeletedBy = "k.sato@example.co.jp"
	p := &Perspectives{Perspectives: []Perspective{testPerspective("PRS-001"), deleted, testPerspective("PRS-003")}}

	if err := p.Validate(); err != nil {
		t.Fatalf("論理削除済みを含む状態が検証で落ちた: %v", err)
	}
	active := p.Active()
	if len(active) != 2 || active[0].ID != "PRS-001" || active[1].ID != "PRS-003" {
		t.Fatalf("論理削除された観点が残っている: %+v", active)
	}
	if len(p.Perspectives) != 3 {
		t.Fatalf("実体が消えている: %+v", p.Perspectives)
	}
	if got, ok := p.Find("PRS-002"); !ok || !got.Deleted() {
		t.Fatalf("削除済みエントリを引けない: %+v, ok=%v", got, ok)
	}
}

// 論点キーは custom/PRS-nnn。
func TestPerspectiveTopicKey(t *testing.T) {
	if got := testPerspective("PRS-007").TopicKey(); got != "custom/PRS-007" {
		t.Fatalf("論点キーが違う: %q", got)
	}
}
