package projectstore

import (
	"strings"
	"testing"
	"time"
)

func sampleAnswers() Answers {
	at := time.Date(2026, 8, 28, 5, 6, 7, 0, time.UTC)
	return Answers{
		QuestionnaireID: "QS-001",
		Respondent:      "佐藤",
		AnsweredAt:      at,
		Answers: []Answer{
			{QuestionID: "q-01", Kind: AnswerKindAnswered, Selected: []string{"即時"}, At: at},
			{QuestionID: "q-02", Kind: AnswerKindAnswered, FreeText: "月末は\n手作業が多いです。", At: at,
				Body: "補足: 締め日は月末営業日です。"},
			{QuestionID: "q-03", Kind: AnswerKindUnknown, At: at, Body: "理由: 判断できません。確認先: 経理部の田中さん"},
		},
	}
}

// 往復で全フィールドが失われない（改行を含む自由記述を含む）。
func TestAnswersRoundTrip(t *testing.T) {
	a := sampleAnswers()
	data, err := a.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	got, err := UnmarshalAnswers(data)
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v\n%s", err, string(data))
	}
	if got.QuestionnaireID != a.QuestionnaireID || got.Respondent != a.Respondent || !got.AnsweredAt.Equal(a.AnsweredAt) {
		t.Fatalf("フロントマターが往復で変化しました: %+v", *got)
	}
	if len(got.Answers) != 3 {
		t.Fatalf("回答数が %d です（期待 3）", len(got.Answers))
	}
	if strings.Join(got.Answers[0].Selected, "|") != "即時" {
		t.Fatalf("選択値が往復で変化しました: %+v", got.Answers[0])
	}
	if got.Answers[1].FreeText != "月末は\n手作業が多いです。" {
		t.Fatalf("改行を含む自由記述が往復で変化しました: %q", got.Answers[1].FreeText)
	}
	if got.Answers[1].Body != "補足: 締め日は月末営業日です。" {
		t.Fatalf("補足本文が往復で変化しました: %q", got.Answers[1].Body)
	}
	if got.Answers[2].Kind != AnswerKindUnknown || !strings.Contains(got.Answers[2].Body, "確認先") {
		t.Fatalf("「不明」の回答が往復で変化しました: %+v", got.Answers[2])
	}

	again, err := got.Marshal()
	if err != nil {
		t.Fatalf("2 回目の Marshal に失敗: %v", err)
	}
	if string(again) != string(data) {
		t.Fatalf("往復でバイト列が変化しました:\n--- 1 回目\n%s\n--- 2 回目\n%s", string(data), string(again))
	}
}

func TestAnswersValidate(t *testing.T) {
	cases := map[string]func(a *Answers){
		"質問票 ID が不正":    func(a *Answers) { a.QuestionnaireID = "QS-1" },
		"回答者名がない":       func(a *Answers) { a.Respondent = "" },
		"回答日時がない":       func(a *Answers) { a.AnsweredAt = time.Time{} },
		"質問 ID の形式違い":   func(a *Answers) { a.Answers[0].QuestionID = "1" },
		"質問 ID が重複":     func(a *Answers) { a.Answers[1].QuestionID = "q-01" },
		"種別が列挙外":        func(a *Answers) { a.Answers[0].Kind = "maybe" },
		"回答日時がない（ブロック）": func(a *Answers) { a.Answers[0].At = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := sampleAnswers()
			mutate(&a)
			if err := a.Validate(); err == nil {
				t.Fatalf("不正な回答が受理されました")
			}
		})
	}
}

func TestAnswersFind(t *testing.T) {
	a := sampleAnswers()
	if got, ok := a.Find("q-03"); !ok || got.Kind != AnswerKindUnknown {
		t.Fatalf("回答を引けません: %+v (ok=%v)", got, ok)
	}
	if _, ok := a.Find("q-09"); ok {
		t.Fatalf("存在しない回答が引けました")
	}
}

// 回答ファイルにもパスコード相当の値を保持しない。
func TestAnswersDropsPasscodeFields(t *testing.T) {
	src := `---
questionnaire_id: QS-001
respondent: 佐藤
answered_at: 2026-08-28T05:06:07Z
passcode: AbCdEfGh2345
---

### q-01
- kind: answered
- at: 2026-08-28T05:06:07Z
- passcode: AbCdEfGh2345
`
	a, err := UnmarshalAnswers([]byte(src))
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v", err)
	}
	out, err := a.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	if strings.Contains(string(out), "AbCdEfGh2345") || strings.Contains(string(out), "passcode") {
		t.Fatalf("パスコード相当の値が保持されました:\n%s", string(out))
	}
}
