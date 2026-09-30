//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package projectstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newRosterStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// STK-nnn は既存最大値 + 1 で採番し、欠番を再利用しない。
func TestAddStakeholderAllocatesSequentialIDs(t *testing.T) {
	s := newRosterStore(t)

	first, err := s.AddStakeholder("佐藤", "営業部")
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if first.ID != "STK-001" {
		t.Fatalf("最初の ID が %q です（期待 STK-001）", first.ID)
	}
	second, err := s.AddStakeholder("鈴木", "物流部")
	if err != nil {
		t.Fatalf("2 件目の登録に失敗: %v", err)
	}
	if second.ID != "STK-002" {
		t.Fatalf("2 件目の ID が %q です（期待 STK-002）", second.ID)
	}

	// 途中の宛先を取り除いた状態からの登録でも欠番を再利用しない。
	roster, err := s.LoadRoster()
	if err != nil {
		t.Fatal(err)
	}
	roster.Stakeholders = roster.Stakeholders[1:]
	if err := s.saveRoster(roster); err != nil {
		t.Fatal(err)
	}
	third, err := s.AddStakeholder("高橋", "情報システム部")
	if err != nil {
		t.Fatalf("3 件目の登録に失敗: %v", err)
	}
	if third.ID != "STK-003" {
		t.Fatalf("3 件目の ID が %q です（欠番 STK-001 が再利用されました）", third.ID)
	}
}

func TestAddStakeholderRejectsEmptyFields(t *testing.T) {
	s := newRosterStore(t)
	cases := map[string][2]string{
		"氏名が空":    {"", "営業部"},
		"所属が空":    {"佐藤", ""},
		"氏名が空白のみ": {"   ", "営業部"},
		"所属が空白のみ": {"佐藤", "  "},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.AddStakeholder(c[0], c[1]); err == nil {
				t.Fatalf("空の項目が受理されました")
			}
		})
	}
	roster, err := s.LoadRoster()
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Stakeholders) != 0 {
		t.Fatalf("拒否したはずの宛先が保存されています: %+v", roster.Stakeholders)
	}
}

func TestUpdateStakeholder(t *testing.T) {
	s := newRosterStore(t)
	added, err := s.AddStakeholder("佐藤", "営業部")
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}

	before, after, err := s.UpdateStakeholder(added.ID, "佐藤 一郎", "営業第一部")
	if err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}
	if before.Name != "佐藤" || before.Org != "営業部" {
		t.Fatalf("変更前の値が違います: %+v", before)
	}
	if after.Name != "佐藤 一郎" || after.Org != "営業第一部" || after.ID != added.ID {
		t.Fatalf("変更後の値が違います: %+v", after)
	}

	roster, err := s.LoadRoster()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := roster.Find(added.ID)
	if !ok || stored.Name != "佐藤 一郎" {
		t.Fatalf("保存内容が違います: %+v (ok=%v)", stored, ok)
	}

	if _, _, err := s.UpdateStakeholder("STK-999", "田中", "経理部"); err == nil {
		t.Fatalf("名簿にない ID の変更が受理されました")
	}
	if _, _, err := s.UpdateStakeholder(added.ID, "", "経理部"); err == nil {
		t.Fatalf("空の氏名が受理されました")
	}
}

// 発行済み質問票の宛先表示は発行時点の写しであり、
// 名簿の変更で書き換わらない。
func TestUpdateStakeholderKeepsIssuedQuestionnaireAddressee(t *testing.T) {
	s := newRosterStore(t)
	added, err := s.AddStakeholder("佐藤", "営業部")
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}

	// 発行済み質問票（保存構造は questionnaire.go。ここでは宛先の写しの不変性のみを見る）。
	qsDir := filepath.Join(s.Root(), "questionnaires", "QS-001")
	if err := os.MkdirAll(qsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	qsPath := filepath.Join(qsDir, "questionnaire.md")
	issued := "---\nid: QS-001\naddressee_ref: " + added.ID + "\naddressee: 佐藤（営業部）\nstatus: issued\n---\n"
	if err := os.WriteFile(qsPath, []byte(issued), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.UpdateStakeholder(added.ID, "佐藤 一郎", "営業第一部"); err != nil {
		t.Fatalf("変更に失敗: %v", err)
	}

	got, err := os.ReadFile(qsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != issued {
		t.Fatalf("発行済み質問票の宛先表示が書き換わりました:\n%s", string(got))
	}
}

// 名簿の書き込みは roster ロック内で行う（保持者がいる間は書き込まない）。
func TestAddStakeholderWaitsForRosterLock(t *testing.T) {
	s := newRosterStore(t)
	s.lockPolicy.shortTimeout = 200 * time.Millisecond
	lock, err := s.AcquireLock(LockRoster)
	if err != nil {
		t.Fatalf("ロックを取得できません: %v", err)
	}

	if _, err := s.AddStakeholder("佐藤", "営業部"); err == nil {
		_ = lock.Release()
		t.Fatalf("ロック保持中に書き込みが成立しました")
	}
	roster, err := s.LoadRoster()
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Stakeholders) != 0 {
		t.Fatalf("部分書き込みが発生しています: %+v", roster.Stakeholders)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("ロックを解放できません: %v", err)
	}
	if _, err := s.AddStakeholder("佐藤", "営業部"); err != nil {
		t.Fatalf("解放後の登録に失敗: %v", err)
	}
}
