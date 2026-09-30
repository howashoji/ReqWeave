package dialogue

import (
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

func testIssues() []projectstore.OpenIssue {
	return []projectstore.OpenIssue{
		{ID: "ISS-003", Owner: "営業部長", Status: projectstore.OpenIssueOpen, Body: "在庫引当のタイミングを即時とするか日次とするか"},
		{ID: "ISS-004", Owner: "情報システム部", Status: projectstore.OpenIssueOpen, Body: "月末締めの手作業の範囲"},
	}
}

func TestParseQuestionDrafts(t *testing.T) {
	body := "以下が結果です。\n```json\n{\"questions\":[{\"source_open_issue_id\":\"ISS-003\"," +
		"\"question_text\":\"在庫の引き当てはいつ行いますか。\",\"background\":\"取り消しの扱いが変わります。\"," +
		"\"answer_format\":\"choice\",\"choices\":[\"即時\",\"日次\"]}]}\n```\n"
	got, err := ParseQuestionDrafts(body)
	if err != nil {
		t.Fatalf("解釈に失敗: %v", err)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("質問数が %d です（期待 1）", len(got.Questions))
	}
	q := got.Questions[0]
	if q.SourceOpenIssueID != "ISS-003" || q.AnswerFormat != projectstore.AnswerFormatChoice || len(q.Choices) != 2 {
		t.Fatalf("内容が違います: %+v", q)
	}

	if _, err := ParseQuestionDrafts("JSON がありません"); err == nil {
		t.Fatalf("JSON のない応答が受理されました")
	}
	if _, err := ParseQuestionDrafts(`{"questions": "配列ではない"}`); err == nil {
		t.Fatalf("形式違いの応答が受理されました")
	}
}

// 質問文の生成規律。
func TestQuestionDraftsNormalize(t *testing.T) {
	d := &QuestionDrafts{Questions: []QuestionDraft{
		// 「不明」の選択肢は取り除く（回答モード UI が常設する）。
		{SourceOpenIssueID: "ISS-003", QuestionText: "在庫の引き当てはいつ行いますか。", Background: "取り消しの扱いが変わります。",
			AnswerFormat: projectstore.AnswerFormatChoice, Choices: []string{"即時", "不明", "日次", "わからない"}},
		// 指定していない未決事項に紐づく質問は捨てる。
		{SourceOpenIssueID: "ISS-999", QuestionText: "関係のない質問", Background: "関係のない背景",
			AnswerFormat: projectstore.AnswerFormatFree},
		// 質問文・背景説明が空のものは捨てる。
		{SourceOpenIssueID: "ISS-004", QuestionText: "", Background: "背景だけある", AnswerFormat: projectstore.AnswerFormatFree},
		{SourceOpenIssueID: "ISS-004", QuestionText: "質問だけある", Background: "  ", AnswerFormat: projectstore.AnswerFormatFree},
		// 列挙外の回答形式は自由記述に倒す。
		{SourceOpenIssueID: "ISS-004", QuestionText: "月末の締めで困っていることは何ですか。", Background: "対象範囲の判断に使います。",
			AnswerFormat: "yes_no", Choices: []string{"はい", "いいえ"}},
		// 選択肢が「不明」だけの選択式も自由記述に倒す。
		{SourceOpenIssueID: "ISS-003", QuestionText: "他に確認したいことはありますか。", Background: "補足のためです。",
			AnswerFormat: projectstore.AnswerFormatMultiChoice, Choices: []string{"不明"}},
	}}
	d.normalize(testIssues())

	if len(d.Questions) != 3 {
		t.Fatalf("残った質問数が %d です（期待 3）: %+v", len(d.Questions), d.Questions)
	}
	first := d.Questions[0]
	if strings.Join(first.Choices, "|") != "即時|日次" {
		t.Fatalf("「不明」の選択肢が残っています: %+v", first.Choices)
	}
	for _, q := range d.Questions {
		if q.SourceOpenIssueID == "ISS-999" {
			t.Fatalf("指定していない未決事項の質問が残っています")
		}
		switch q.AnswerFormat {
		case projectstore.AnswerFormatChoice, projectstore.AnswerFormatMultiChoice,
			projectstore.AnswerFormatFree, projectstore.AnswerFormatChoiceWithFree:
		default:
			t.Fatalf("回答形式が列挙外です: %q", q.AnswerFormat)
		}
		if q.AnswerFormat == projectstore.AnswerFormatFree && len(q.Choices) != 0 {
			t.Fatalf("自由記述に選択肢が付いています: %+v", q)
		}
		for _, c := range q.Choices {
			for _, banned := range unknownChoiceLabels {
				if c == banned {
					t.Fatalf("「不明」相当の選択肢が残っています: %q", c)
				}
			}
		}
	}
	if d.Questions[1].AnswerFormat != projectstore.AnswerFormatFree || len(d.Questions[1].Choices) != 0 {
		t.Fatalf("列挙外の回答形式が自由記述に倒れていません: %+v", d.Questions[1])
	}
	if d.Questions[2].AnswerFormat != projectstore.AnswerFormatFree {
		t.Fatalf("選択肢の無い選択式が自由記述に倒れていません: %+v", d.Questions[2])
	}
}

// AI 障害時は論点を転記したテンプレートを返す。
func TestFallbackResult(t *testing.T) {
	issues := testIssues()
	got := fallbackResult(issues, nil)
	if !got.Fallback {
		t.Fatalf("縮退として扱われていません")
	}
	if len(got.Drafts) != len(issues) {
		t.Fatalf("テンプレート数が %d です（期待 %d）", len(got.Drafts), len(issues))
	}
	for i, d := range got.Drafts {
		if d.SourceOpenIssueID != issues[i].ID {
			t.Fatalf("発行元未決事項が違います: %+v", d)
		}
		if !strings.Contains(d.QuestionText, "在庫引当") && !strings.Contains(d.QuestionText, "月末締め") {
			t.Fatalf("論点が転記されていません: %+v", d)
		}
		if d.AnswerFormat != projectstore.AnswerFormatFree {
			t.Fatalf("テンプレートの回答形式が自由記述ではありません: %+v", d)
		}
	}
	if got.Notice == "" {
		t.Fatalf("縮退の案内がありません")
	}
	if !strings.Contains(got.Notice, "入力して発行") {
		t.Fatalf("次の行動が案内されていません: %q", got.Notice)
	}
}

func TestTemplatesForMissing(t *testing.T) {
	issues := testIssues()
	drafts := []QuestionDraft{{SourceOpenIssueID: "ISS-003", QuestionText: "質問", Background: "背景"}}
	got := templatesForMissing(issues, drafts)
	if len(got) != 1 || got[0].SourceOpenIssueID != "ISS-004" {
		t.Fatalf("補われたテンプレートが違います: %+v", got)
	}
}

// 質問文生成の出力契約がプロンプトへ載る。
func TestQuestionnairePromptContract(t *testing.T) {
	got := outputContractSection(ModeQuestionnaire)
	for _, want := range []string{"source_open_issue_id", "業務側の言葉", "「不明」の選択肢は作りません", "answer_format"} {
		if !strings.Contains(got, want) {
			t.Fatalf("出力契約に %q がありません:\n%s", want, got)
		}
	}
}
