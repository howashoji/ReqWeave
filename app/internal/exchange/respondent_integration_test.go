//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package exchange_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// respondFixture は「担当者が発行した .rwvq」と「回答モードの作業領域」を用意する。
type respondFixture struct {
	store         *projectstore.Store
	questionnaire *projectstore.Questionnaire
	issueFile     string
	passcode      string
	workspace     *exchange.Workspace
	appBase       string
}

func newRespondFixture(t *testing.T) respondFixture {
	t.Helper()
	store, q := newIssueProject(t)

	// パスコードは発行時に生成され、戻り値で 1 回だけ返る。
	issued, err := exchange.WriteIssue(store, buildContent(t, store, q), filepath.Join(t.TempDir(), q.ID))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	if issued.Passcode == "" {
		t.Fatal("パスコードが返っていません")
	}

	appBase := filepath.Join(t.TempDir(), "app")
	return respondFixture{
		store: store, questionnaire: q, issueFile: issued.Path, passcode: issued.Passcode,
		workspace: exchange.NewWorkspace(projectstore.AppPaths{Base: appBase}), appBase: appBase,
	}
}

// answerAll は全質問へ回答する（選択肢型は先頭の選択肢、自由記述は本文）。
func answerAll(t *testing.T, sess *exchange.RespondSession) {
	t.Helper()
	at := time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)
	for _, q := range sess.Questionnaire.Questions {
		a := projectstore.Answer{QuestionID: q.ID, Kind: projectstore.AnswerKindAnswered, At: at}
		if len(q.Choices) > 0 {
			a.Selected = []string{q.Choices[0]}
		} else {
			a.FreeText = "手作業なのは請求書の突合です。"
		}
		if err := sess.SetAnswer(a); err != nil {
			t.Fatalf("回答の保存に失敗（%s）: %v", q.ID, err)
		}
	}
}

// パスコードが一致するまで内容を一切返さない。
func TestOpenRejectsWrongPasscode(t *testing.T) {
	f := newRespondFixture(t)

	sess, err := f.workspace.Open(f.issueFile, "WrongPasscode99")
	if !errors.Is(err, exchange.ErrPasscodeMismatch) {
		t.Fatalf("パスコード不一致にならない: err=%v sess=%+v", err, sess)
	}
	if sess != nil {
		t.Fatalf("不一致なのに内容が返りました: %+v", sess)
	}
	// エラー文にパスコード・試行値を含めない。
	if strings.Contains(err.Error(), "WrongPasscode99") || strings.Contains(err.Error(), f.passcode) {
		t.Fatalf("エラー文にパスコードが出ています: %v", err)
	}
	// 回答作業領域が作られていない（内容が漏れていない）。
	if _, statErr := os.Stat(filepath.Join(f.appBase, "respondent")); !os.IsNotExist(statErr) {
		t.Fatalf("不一致なのに回答作業領域が作られました: %v", statErr)
	}

	// 正しいパスコードなら質問文・背景説明・宛先が復号される。
	ok, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("正しいパスコードで開けない: %v", err)
	}
	if len(ok.Questionnaire.Questions) == 0 || ok.Questionnaire.Questions[0].Text == "" {
		t.Fatalf("質問が復号されていません: %+v", ok.Questionnaire)
	}
	if ok.Questionnaire.Addressee == "" {
		t.Fatal("宛先が復号されていません")
	}
}

// 回答作業領域を平文検索しても質問文・回答本文が現れない。
func TestRespondAreaKeepsNoPlaintext(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	const secretAnswer = "請求書の突合を手作業でやっています"
	first := sess.Questionnaire.Questions[0]
	if err := sess.SetAnswer(projectstore.Answer{
		QuestionID: first.ID, Kind: projectstore.AnswerKindAnswered,
		FreeText: secretAnswer, At: time.Now(),
	}); err != nil {
		t.Fatalf("回答の保存に失敗: %v", err)
	}

	// 検索対象は回答作業領域配下の全ファイル（平文メタデータを含む）。
	needles := []string{secretAnswer, first.Text, first.Background, sess.Questionnaire.Addressee}
	err = filepath.Walk(filepath.Join(f.appBase, "respondent"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, needle := range needles {
			if needle == "" {
				t.Fatalf("検索語が空です（検証にならない）: %q", needles)
			}
			if bytes.Contains(body, []byte(needle)) {
				t.Fatalf("回答作業領域 %s に平文が現れました: %q", p, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 閉じて開き直すと入力済み回答が復元される（再開時もパスコードが要る）。
func TestRespondAreaRestoresAnswers(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	first := sess.Questionnaire.Questions[0]
	at := time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)
	if err := sess.SetAnswer(projectstore.Answer{
		QuestionID: first.ID, Kind: projectstore.AnswerKindUnknown, At: at,
		Body: "理由: 判断できません\n確認先: 経理部",
	}); err != nil {
		t.Fatalf("回答の保存に失敗: %v", err)
	}

	// 別の Workspace インスタンス（= アプリを閉じて開き直した状態）で再開する。
	reopened := exchange.NewWorkspace(projectstore.AppPaths{Base: f.appBase})
	if _, err := reopened.Open(f.issueFile, "WrongPasscode99"); !errors.Is(err, exchange.ErrPasscodeMismatch) {
		t.Fatalf("再開時にパスコードなしで開けました: %v", err)
	}
	again, err := reopened.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("再開できない: %v", err)
	}
	if !again.Restored {
		t.Fatalf("復元として扱われていません: %+v", again)
	}
	got, ok := again.Answers.Find(first.ID)
	if !ok {
		t.Fatalf("入力済み回答が復元されていません: %+v", again.Answers.Answers)
	}
	if got.Kind != projectstore.AnswerKindUnknown || !strings.Contains(got.Body, "確認先: 経理部") {
		t.Fatalf("復元された回答が違います: %+v", got)
	}
}

// 確定で返送ファイルが出力され、元の発行ファイルは変更されない。
func TestFinalizeWritesReturnAndLeavesIssueFileUnchanged(t *testing.T) {
	f := newRespondFixture(t)
	before, err := os.ReadFile(f.issueFile)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	dst := filepath.Join(t.TempDir(), sess.SuggestedReturnName())

	// 未回答が残る間は確定できず、返送ファイルも作られない。
	if err := sess.Finalize(dst); !errors.Is(err, exchange.ErrIncomplete) {
		t.Fatalf("未回答のまま確定できました: %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("確定前に返送ファイルが作られました: %v", statErr)
	}

	answerAll(t, sess)
	if err := sess.Finalize(dst); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	// 初期設定（アプリ設定）・シークレットキーなしでここまで到達する。
	if _, statErr := os.Stat(filepath.Join(f.appBase, "settings.json")); !os.IsNotExist(statErr) {
		t.Fatalf("回答モードがアプリ設定を要求しました: %v", statErr)
	}
	if sess.Status != exchange.RespondAnswered {
		t.Fatalf("回答作業領域の状態が %s です（期待 %s）", sess.Status, exchange.RespondAnswered)
	}

	// 元の発行用ファイルは 1 バイトも変わらない。
	after, err := os.ReadFile(f.issueFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("発行用ファイルが変更されました（%d バイト → %d バイト）", len(before), len(after))
	}

	// 出力した返送ファイルは担当者側で取り込める（取り込みの検証の経路に乗る）。
	review, err := exchange.ValidateReturn(f.store, dst)
	if err != nil {
		t.Fatalf("返送ファイルを担当者側で検証できない: %v", err)
	}
	if review.NeedsConfirmation() {
		t.Fatalf("正規の返送ファイルで確認が要求されました: %+v", review.Warnings)
	}
	if len(review.MissingQuestionIDs) != 0 || len(review.UnknownQuestionIDs) != 0 {
		t.Fatalf("質問 ID の突合が合いません: missing=%v unknown=%v",
			review.MissingQuestionIDs, review.UnknownQuestionIDs)
	}
	if review.Answers.Respondent != f.questionnaire.Addressee {
		t.Fatalf("回答者が発行時の宛先になっていません: %q", review.Answers.Respondent)
	}
}

// 確定後の再出力は確定時点の内容のまま（送付失敗時の再送付用）。
func TestExportReproducesFinalizedContent(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	answerAll(t, sess)
	dir := t.TempDir()
	first := filepath.Join(dir, "first"+exchange.ExtReturn)
	if err := sess.Finalize(first); err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}

	// 確定後は回答を変更できない（再出力の内容が確定時点のままであることを保つ）。
	target := sess.Questionnaire.Questions[0]
	if err := sess.SetAnswer(projectstore.Answer{
		QuestionID: target.ID, Kind: projectstore.AnswerKindAnswered,
		FreeText: "確定後の書き換え", At: time.Now(),
	}); err == nil {
		t.Fatal("確定後に回答を変更できました")
	}

	// 別セッション（開き直し）からの再出力でも確定時点のバイト列と一致する。
	reopened, err := exchange.NewWorkspace(projectstore.AppPaths{Base: f.appBase}).Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("再開できない: %v", err)
	}
	if reopened.Status != exchange.RespondAnswered {
		t.Fatalf("再開後の状態が %s です（期待 %s）", reopened.Status, exchange.RespondAnswered)
	}
	second := filepath.Join(dir, "second"+exchange.ExtReturn)
	if err := reopened.Export(second); err != nil {
		t.Fatalf("再出力に失敗: %v", err)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("再出力の内容が確定時点と違います")
	}
}

// 再発行: 新しいパスコード・salt の発行ファイルでは旧回答作業領域を引き継がず、その旨を案内する。
func TestReissuedFileStartsFreshRespondArea(t *testing.T) {
	f := newRespondFixture(t)
	sess, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	first := sess.Questionnaire.Questions[0]
	if err := sess.SetAnswer(projectstore.Answer{
		QuestionID: first.ID, Kind: projectstore.AnswerKindAnswered,
		FreeText: "旧ファイルで入力した回答", At: time.Now(),
	}); err != nil {
		t.Fatalf("回答の保存に失敗: %v", err)
	}

	// 同一質問票を再発行する（新しいパスコード・新しい salt）。
	reissued, err := exchange.WriteIssue(f.store, buildContent(t, f.store, f.questionnaire),
		filepath.Join(t.TempDir(), f.questionnaire.ID))
	if err != nil {
		t.Fatalf("再発行に失敗: %v", err)
	}
	if reissued.Passcode == f.passcode {
		t.Fatal("再発行で同じパスコードが返りました")
	}

	// 旧パスコードでは新ファイルを開けない。
	if _, err := f.workspace.Open(reissued.Path, f.passcode); !errors.Is(err, exchange.ErrPasscodeMismatch) {
		t.Fatalf("旧パスコードで再発行ファイルを開けました: %v", err)
	}
	fresh, err := f.workspace.Open(reissued.Path, reissued.Passcode)
	if err != nil {
		t.Fatalf("再発行ファイルを開けない: %v", err)
	}
	if fresh.Restored || len(fresh.Answers.Answers) != 0 {
		t.Fatalf("旧回答作業領域が引き継がれました: %+v", fresh.Answers.Answers)
	}
	if !strings.Contains(fresh.Notice, "引き継がれません") {
		t.Fatalf("引き継がれない旨の案内がありません: %q", fresh.Notice)
	}

	// 旧回答作業領域は壊されていない（旧ファイル + 旧パスコードで再開できる）。
	old, err := f.workspace.Open(f.issueFile, f.passcode)
	if err != nil {
		t.Fatalf("旧ファイルを開けない: %v", err)
	}
	if got, ok := old.Answers.Find(first.ID); !ok || got.FreeText != "旧ファイルで入力した回答" {
		t.Fatalf("旧回答作業領域の回答が失われました: %+v", old.Answers.Answers)
	}
}

// 返送用ファイルを回答モードで開こうとしても内容を出さない。
func TestOpenRejectsReturnFile(t *testing.T) {
	f := newRespondFixture(t)
	src := writeReturn(t, t.TempDir(), f.store, f.questionnaire, returnSpec{})

	if _, err := f.workspace.Open(src, f.passcode); err == nil {
		t.Fatal("返送用ファイルを回答モードで開けました")
	}
}
