//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package projectstore

import (
	"os"
	"path/filepath"
	"testing"
)

func newQuestionnaireStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := CreateProject(root, CreateOptions{TargetSystemName: "在庫管理システム", Author: testAuthor()})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// QS-nnn の採番と、発行控え置き場の作成。
func TestCreateQuestionnaire(t *testing.T) {
	s := newQuestionnaireStore(t)

	q := sampleQuestionnaire()
	q.ID = "" // 採番させる
	created, err := s.CreateQuestionnaire(q)
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}
	if created.ID != "QS-001" {
		t.Fatalf("採番が %q です（期待 QS-001）", created.ID)
	}
	if created.Status != QuestionnaireIssued {
		t.Fatalf("初期状態が %q です（期待 %s）", created.Status, QuestionnaireIssued)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(QuestionnaireExchangeDir("QS-001")))); err != nil {
		t.Fatalf("発行控えの置き場がありません: %v", err)
	}

	second := sampleQuestionnaire()
	second.ID = ""
	created2, err := s.CreateQuestionnaire(second)
	if err != nil {
		t.Fatalf("2 件目の作成に失敗: %v", err)
	}
	if created2.ID != "QS-002" {
		t.Fatalf("2 件目の採番が %q です（期待 QS-002）", created2.ID)
	}

	list, err := s.ListQuestionnaires()
	if err != nil {
		t.Fatalf("一覧に失敗: %v", err)
	}
	if len(list) != 2 || list[0].ID != "QS-001" || list[1].ID != "QS-002" {
		t.Fatalf("一覧が違います: %+v", list)
	}
}

// 状態遷移は issued → answered → imported のみ。逆行・飛び越しは拒否する。
func TestQuestionnaireStatusTransitions(t *testing.T) {
	s := newQuestionnaireStore(t)
	q := sampleQuestionnaire()
	q.ID = ""
	created, err := s.CreateQuestionnaire(q)
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}
	id := created.ID

	// 飛び越し（issued → imported）は拒否。
	if err := s.SetQuestionnaireStatus(id, QuestionnaireImported); err == nil {
		t.Fatalf("状態の飛び越しが受理されました")
	}
	assertStatus(t, s, id, QuestionnaireIssued)

	// 据え置き（発行済みのまま = 一時保存・再発行）は許す。
	if err := s.SetQuestionnaireStatus(id, QuestionnaireIssued); err != nil {
		t.Fatalf("同じ状態への据え置きが拒否されました: %v", err)
	}

	if err := s.SetQuestionnaireStatus(id, QuestionnaireAnswered); err != nil {
		t.Fatalf("回答済みへの遷移に失敗: %v", err)
	}
	assertStatus(t, s, id, QuestionnaireAnswered)

	// 逆行（answered → issued）は拒否。
	if err := s.SetQuestionnaireStatus(id, QuestionnaireIssued); err == nil {
		t.Fatalf("状態の逆行が受理されました")
	}
	assertStatus(t, s, id, QuestionnaireAnswered)

	if err := s.SetQuestionnaireStatus(id, QuestionnaireImported); err != nil {
		t.Fatalf("取込済みへの遷移に失敗: %v", err)
	}
	assertStatus(t, s, id, QuestionnaireImported)

	// 取込済みからの逆行も SetQuestionnaireStatus では拒否する。
	if err := s.SetQuestionnaireStatus(id, QuestionnaireAnswered); err == nil {
		t.Fatalf("取込済みからの逆行が受理されました")
	}

	// 再取込は明示的な操作としてのみ許す。
	if err := s.ReopenQuestionnaireForReimport(id); err != nil {
		t.Fatalf("再取込のための状態戻しに失敗: %v", err)
	}
	assertStatus(t, s, id, QuestionnaireAnswered)
	if err := s.ReopenQuestionnaireForReimport(id); err == nil {
		t.Fatalf("取込済みでない質問票の再取込が受理されました")
	}

	// 列挙外の状態は受け付けない。
	if err := s.SetQuestionnaireStatus(id, "draft"); err == nil {
		t.Fatalf("列挙外の状態が受理されました")
	}
}

func assertStatus(t *testing.T, s *Store, id, want string) {
	t.Helper()
	q, err := s.LoadQuestionnaire(id)
	if err != nil {
		t.Fatalf("読み込みに失敗: %v", err)
	}
	if q.Status != want {
		t.Fatalf("状態が %q です（期待 %q）", q.Status, want)
	}
}

// SaveQuestionnaire は状態を変えられない（状態遷移の経路を 1 本に保つ）。
func TestSaveQuestionnaireDoesNotChangeStatus(t *testing.T) {
	s := newQuestionnaireStore(t)
	q := sampleQuestionnaire()
	q.ID = ""
	created, err := s.CreateQuestionnaire(q)
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}

	created.Status = QuestionnaireImported
	if err := s.SaveQuestionnaire(created); err == nil {
		t.Fatalf("保存経由で状態が変えられました")
	}
	assertStatus(t, s, created.ID, QuestionnaireIssued)

	// 宛先・発行日時の更新（再発行）は保存できる。
	created.Status = QuestionnaireIssued
	created.Addressee = "佐藤 一郎（営業第一部）"
	if err := s.SaveQuestionnaire(created); err != nil {
		t.Fatalf("再発行の保存に失敗: %v", err)
	}
	reloaded, err := s.LoadQuestionnaire(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Addressee != "佐藤 一郎（営業第一部）" {
		t.Fatalf("宛先表示名が保存されていません: %+v", reloaded)
	}
}

func TestAnswersStoreRoundTrip(t *testing.T) {
	s := newQuestionnaireStore(t)
	q := sampleQuestionnaire()
	q.ID = ""
	created, err := s.CreateQuestionnaire(q)
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}

	if got, err := s.LoadAnswers(created.ID); err != nil || got != nil {
		t.Fatalf("未取込の回答が nil ではありません: %+v (%v)", got, err)
	}

	a := sampleAnswers()
	a.QuestionnaireID = created.ID
	if err := s.SaveAnswers(&a); err != nil {
		t.Fatalf("回答の保存に失敗: %v", err)
	}
	got, err := s.LoadAnswers(created.ID)
	if err != nil {
		t.Fatalf("回答の読み込みに失敗: %v", err)
	}
	if got == nil || len(got.Answers) != 3 || got.Respondent != "佐藤" {
		t.Fatalf("保存内容が違います: %+v", got)
	}
}

// 発行控え・返送原本は questionnaires/QS-nnn/exchange/ に保存する。
func TestSaveExchangeArtifact(t *testing.T) {
	s := newQuestionnaireStore(t)
	q := sampleQuestionnaire()
	q.ID = ""
	created, err := s.CreateQuestionnaire(q)
	if err != nil {
		t.Fatalf("作成に失敗: %v", err)
	}

	if err := s.SaveExchangeArtifact(created.ID, "QS-001.rwvq", []byte("dummy-container")); err != nil {
		t.Fatalf("控えの保存に失敗: %v", err)
	}
	got, err := os.ReadFile(s.ExchangeArtifactPath(created.ID, "QS-001.rwvq"))
	if err != nil {
		t.Fatalf("控えを読めません: %v", err)
	}
	if string(got) != "dummy-container" {
		t.Fatalf("控えの内容が違います: %q", string(got))
	}

	for _, bad := range []string{"", "../escape.rwvq", "sub/dir.rwvq"} {
		if err := s.SaveExchangeArtifact(created.ID, bad, []byte("x")); err == nil {
			t.Fatalf("不正なファイル名が受理されました: %q", bad)
		}
	}
}

func TestLoadQuestionnaireMissing(t *testing.T) {
	s := newQuestionnaireStore(t)
	if _, err := s.LoadQuestionnaire("QS-999"); err == nil {
		t.Fatalf("存在しない質問票が読めました")
	}
}
