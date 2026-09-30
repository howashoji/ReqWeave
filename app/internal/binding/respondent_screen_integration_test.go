//go:build integration

package binding

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newRespondAPI は「初期設定もキーも無い」状態の API を返す（回答モードの前提。ステークホルダーは初期設定をしない）。
func newRespondAPI(t *testing.T) *API {
	t.Helper()
	return &API{paths: projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}}
}

// issueForRespond は担当者側で質問票を発行し、ファイルとパスコードを返す。
func issueForRespond(t *testing.T) (string, string) {
	t.Helper()
	f := newQuestionnaireAPI(t)
	issued, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	return issued.Path, issued.Passcode
}

// 初期設定・キーなしで、パスコード入力から返送出力まで到達できる。
func TestRespondBindingCompletesWithoutSetup(t *testing.T) {
	src, passcode := issueForRespond(t)
	a := newRespondAPI(t)

	// パスコード不一致では内容を返さない。
	if _, err := a.OpenQuestionnaireFile(src, "WrongPasscode99"); err == nil {
		t.Fatal("誤ったパスコードで開けた")
	} else if !strings.Contains(err.Error(), "システム担当者へ連絡してください") {
		t.Fatalf("担当者への連絡案内がない: %v", err)
	}

	opened, err := a.OpenQuestionnaireFile(src, passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	if len(opened.Questions) == 0 || opened.Questions[0].Text == "" {
		t.Fatalf("質問が返っていない: %+v", opened)
	}
	if opened.Restored {
		t.Fatalf("初回なのに復元として扱われた: %+v", opened)
	}

	// 未回答が残る間は確定できない。
	progress, err := a.RespondProgress()
	if err != nil {
		t.Fatal(err)
	}
	if progress.Answered != 0 || progress.Total != len(opened.Questions) {
		t.Fatalf("進捗が違う: %+v", progress)
	}
	dst := filepath.Join(t.TempDir(), "return"+exchange.ExtReturn)
	if _, err := a.FinalizeAnswers(dst); err == nil {
		t.Fatal("未回答のまま確定できた")
	}

	for _, q := range opened.Questions {
		in := AnswerInput{QuestionID: q.ID, Kind: projectstore.AnswerKindAnswered}
		if len(q.Choices) > 0 {
			in.Selected = []string{q.Choices[0]}
		} else {
			in.FreeText = "請求書の突合が手作業です。"
		}
		if _, err := a.SaveAnswer(in); err != nil {
			t.Fatalf("回答を保存できない（%s）: %v", q.ID, err)
		}
	}
	progress, err = a.RespondProgress()
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.UnansweredIDs) != 0 {
		t.Fatalf("未回答が残っている: %+v", progress)
	}

	result, err := a.FinalizeAnswers(dst)
	if err != nil {
		t.Fatalf("確定に失敗: %v", err)
	}
	if result.Path != dst || !strings.Contains(result.Notice, "担当者へ返送") {
		t.Fatalf("返送手順の案内がない: %+v", result)
	}
}

// 開き直すと入力済み回答が復元される（再開時もパスコードが要る）。
func TestRespondBindingRestoresAnswers(t *testing.T) {
	src, passcode := issueForRespond(t)
	a := newRespondAPI(t)

	opened, err := a.OpenQuestionnaireFile(src, passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	first := opened.Questions[0]
	if _, err := a.SaveAnswer(AnswerInput{
		QuestionID: first.ID, Kind: projectstore.AnswerKindUnknown, Body: "理由: 判断できません",
	}); err != nil {
		t.Fatalf("回答を保存できない: %v", err)
	}

	// 同じ作業領域で開き直す（アプリを閉じて開き直した状態）。
	again := &API{paths: a.paths}
	if _, err := again.OpenQuestionnaireFile(src, "WrongPasscode99"); err == nil {
		t.Fatal("再開時にパスコードなしで開けた")
	}
	restored, err := again.OpenQuestionnaireFile(src, passcode)
	if err != nil {
		t.Fatalf("再開できない: %v", err)
	}
	if !restored.Restored {
		t.Fatalf("復元として扱われていない: %+v", restored)
	}
	if len(restored.Answers) != 1 || restored.Answers[0].QuestionID != first.ID ||
		restored.Answers[0].Kind != projectstore.AnswerKindUnknown {
		t.Fatalf("入力済み回答が復元されていない: %+v", restored.Answers)
	}
	if restored.Answers[0].Body != "理由: 判断できません" {
		t.Fatalf("「不明」の理由が復元されていない: %+v", restored.Answers[0])
	}
}

// 画面へ返す値にパスコード・鍵・受け渡しファイルのバイト列を載せない。
func TestRespondOpenResultHasNoSecrets(t *testing.T) {
	src, passcode := issueForRespond(t)
	a := newRespondAPI(t)

	opened, err := a.OpenQuestionnaireFile(src, passcode)
	if err != nil {
		t.Fatalf("開けない: %v", err)
	}
	rendered := renderStruct(opened)
	if strings.Contains(rendered, passcode) {
		t.Fatalf("画面へ返す値にパスコードが含まれています: %s", rendered)
	}
	for _, banned := range []string{"payload.enc", "manifest.yaml", "return_key", "argon2id"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("画面へ返す値に内部の値（%q）が含まれています: %s", banned, rendered)
		}
	}
}

// 取込対象の返送ファイルは回答モードで開かない（返送ファイルは担当者モードで取り込む）。
func TestRespondBindingRejectsReturnFile(t *testing.T) {
	f := newQuestionnaireAPI(t)
	issued, err := f.api.IssueQuestionnaire(f.draftRequest(), filepath.Join(t.TempDir(), "QS"))
	if err != nil {
		t.Fatalf("発行に失敗: %v", err)
	}
	returnFile := respondAndReturn(t, issued)

	a := newRespondAPI(t)
	if _, err := a.OpenQuestionnaireFile(returnFile, issued.Passcode); err == nil {
		t.Fatal("返送ファイルを回答モードで開けた")
	}
}

// renderStruct は構造体を文字列化する（機密の混入検査用）。
func renderStruct(v any) string { return fmt.Sprintf("%+v", v) }
