package exchange

// 単体テスト（回答モードの AI 対話回答で AI へ送る範囲）。
//
// 送信範囲を次の (a)〜(f) で検査する:
//
//	(a) 全質問の本文が現れること
//	(b) 同梱用語がすべて現れること
//	(c) 入力済み回答がすべて現れること
//	(d) STK- / QS- / ISS- / q- で始まる識別子が 1 つも現れないこと
//	(e) content_hash / return_key / salt / パスコードの実値が現れないこと
//	(f) 回答作業領域のパス文字列が現れないこと

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// aiContextFixture は検査に使う質問票・用語・回答を組む。
//
// 含めてはいけない値（宛先・各種 ID・保護の材料）を**実際に持たせた**うえで、
// 出力に現れないことを見る（持たせなければ検査が空振りする）。
func aiContextFixture() *RespondSession {
	return &RespondSession{
		Questionnaire: &projectstore.Questionnaire{
			ID:           "QS-007",
			AddresseeRef: "STK-003",
			Addressee:    "山田 花子（営業部）",
			IssuedBy:     "AUTHOR-001",
			IssuedAt:     time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC),
			Questions: []projectstore.Question{
				{
					ID: "q-01", SourceIssue: "ISS-012",
					AnswerFormat: projectstore.AnswerFormatChoiceWithFree,
					Choices:      []string{"毎日", "週次", "月次"},
					Text:         "現物の棚卸しはどの頻度で行っていますか。",
					Background:   "現状業務の記録のために確認します。",
					Terms:        []string{"棚卸し"},
				},
				{
					ID: "q-02", SourceIssue: "ISS-013",
					AnswerFormat: projectstore.AnswerFormatFree,
					Text:         "欠品が起きたときの連絡経路を教えてください。",
				},
				{
					ID: "q-03", SourceIssue: "ISS-014",
					AnswerFormat: projectstore.AnswerFormatChoice,
					Choices:      []string{"ある", "ない"},
					Text:         "返品の受け入れ基準は文書化されていますか。",
				},
			},
		},
		Terms: []projectstore.Term{
			{Name: "棚卸し", NameEn: "stocktaking", Definition: "在庫の現物を数えて帳簿と突き合わせる作業。"},
			{Name: "欠品", Definition: "注文に対して引き当てる在庫が足りない状態。"},
		},
		Answers: &projectstore.Answers{
			QuestionnaireID: "QS-007",
			Respondent:      "山田 花子",
			Answers: []projectstore.Answer{
				{QuestionID: "q-01", Kind: projectstore.AnswerKindAnswered,
					Selected: []string{"月次"}, FreeText: "月末の営業日に実施しています。"},
				{QuestionID: "q-03", Kind: projectstore.AnswerKindUnknown,
					Body: "品質保証部に確認が必要です。"},
			},
		},
	}
}

func TestAIContextIncludesAllQuestionsAndTerms(t *testing.T) {
	got := aiContextFixture().AIContext()

	// (a) 全質問の本文
	for _, want := range []string{
		"現物の棚卸しはどの頻度で行っていますか。",
		"欠品が起きたときの連絡経路を教えてください。",
		"返品の受け入れ基準は文書化されていますか。",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("質問の本文が送信本文に無い: %q", want)
		}
	}
	// 背景説明・選択肢も同表の「含める」。
	if !strings.Contains(got, "現状業務の記録のために確認します。") {
		t.Error("背景説明が送信本文に無い")
	}
	for _, choice := range []string{"毎日", "週次", "月次", "ある", "ない"} {
		if !strings.Contains(got, choice) {
			t.Errorf("選択肢が送信本文に無い: %q", choice)
		}
	}
	// (b) 同梱用語がすべて
	for _, want := range []string{
		"在庫の現物を数えて帳簿と突き合わせる作業。",
		"注文に対して引き当てる在庫が足りない状態。",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("用語の定義が送信本文に無い: %q", want)
		}
	}
	// (c) 入力済み回答がすべて
	for _, want := range []string{"月末の営業日に実施しています。", "品質保証部に確認が必要です。"} {
		if !strings.Contains(got, want) {
			t.Errorf("入力済みの回答が送信本文に無い: %q", want)
		}
	}
	// 未入力の質問も「未入力」として載せる。
	if !strings.Contains(got, "未入力") {
		t.Error("未入力の質問が示されていない")
	}
}

func TestAIContextExcludesIdentifiersAndAddressee(t *testing.T) {
	got := aiContextFixture().AIContext()

	// (d) 内部識別子が 1 つも現れない。
	for _, pattern := range []string{`STK-\d`, `QS-\d`, `ISS-\d`, `\bq-\d`, `AUTHOR-\d`} {
		if regexp.MustCompile(pattern).MatchString(got) {
			t.Errorf("内部識別子が送信本文に現れた: /%s/", pattern)
		}
	}
	// 宛先の氏名・所属。
	for _, want := range []string{"山田 花子", "営業部"} {
		if strings.Contains(got, want) {
			t.Errorf("宛先の情報が送信本文に現れた: %q", want)
		}
	}
	// 走査が空振りしていないこと（対象の値をフィクスチャが実際に持っている）。
	s := aiContextFixture()
	if s.Questionnaire.AddresseeRef == "" || s.Questionnaire.Addressee == "" ||
		s.Questionnaire.Questions[0].SourceIssue == "" {
		t.Fatal("フィクスチャが検査対象の値を持っていない（テストが成立していない）")
	}
}

func TestAIContextExcludesProtectionMaterialAndPaths(t *testing.T) {
	s := aiContextFixture()
	// (e)(f) 保護の材料と作業領域のパスは、そもそも組み立ての入力に含めない設計である。
	// ここでは「持っていても出ない」ことを見る。
	s.contentYAML = []byte("content_hash: 5f2b9c1d0a\nreturn_key: BASE64RETURNKEYVALUE==\n")
	s.returnKey = []byte("BASE64RETURNKEYVALUE==")
	s.key = []byte("DERIVEDKEYMATERIAL")
	s.dir = "/Users/tester/Library/Application Support/ReqWeave/respondent/ab12cd34"
	s.manifest = Manifest{}

	got := s.AIContext()
	for _, secret := range []string{
		"5f2b9c1d0a", "BASE64RETURNKEYVALUE==", "DERIVEDKEYMATERIAL",
		"content_hash", "return_key",
		"/Users/tester/Library/Application Support/ReqWeave/respondent/ab12cd34",
		"respondent/ab12cd34",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("送ってはいけない値が送信本文に現れた: %q", secret)
		}
	}
	// 走査が空振りしていないこと。
	if !strings.Contains(got, "現物の棚卸しはどの頻度で行っていますか。") {
		t.Fatal("送信本文が組み立てられていない（テストが成立していない）")
	}
}

func TestAIContextUsesOrdinalInsteadOfQuestionID(t *testing.T) {
	got := aiContextFixture().AIContext()
	for i := 1; i <= 3; i++ {
		want := ordinalQuestionLabel(i - 1)
		if !strings.Contains(got, want) {
			t.Errorf("質問の指し示しが無い: %q（内部 ID の代わりに並びで指す）", want)
		}
	}
}

func TestAnswerFormatLabelsCoverAllFormats(t *testing.T) {
	all := []string{
		projectstore.AnswerFormatChoice,
		projectstore.AnswerFormatMultiChoice,
		projectstore.AnswerFormatFree,
		projectstore.AnswerFormatChoiceWithFree,
	}
	for _, format := range all {
		label, ok := answerFormatLabels[format]
		if !ok {
			t.Errorf("回答形式 %q の表示文言が無い（形式を増やしたら答えも足す）", format)
			continue
		}
		if strings.Contains(label, format) {
			t.Errorf("回答形式 %q の表示文言に内部のコード値が入っている: %q", format, label)
		}
	}
	if len(answerFormatLabels) != len(all) {
		t.Errorf("表示文言の件数が回答形式の数と合わない: %d（期待 %d）", len(answerFormatLabels), len(all))
	}
	// 未知の値はコード値を出さずに倒す（生のコード値を外へ出さない）。
	if got := answerFormatLabel("brand_new_format"); strings.Contains(got, "brand_new_format") {
		t.Errorf("未知の回答形式で生のコード値が出た: %q", got)
	}
}

func TestAIContextMarksUnknownAnswerWithReason(t *testing.T) {
	got := aiContextFixture().AIContext()
	if !strings.Contains(got, "「不明」") {
		t.Error("「不明」の回答がそうと分かる形で載っていない")
	}
	if !strings.Contains(got, "品質保証部に確認が必要です。") {
		t.Error("「不明」の理由・確認先が載っていない")
	}
}

// AISystemPrompt は指示文 + 文脈であり、**送信範囲の検査は指示文を含めても成り立つ**こと。
//
// 指示文を別経路で組み立てると、そこが送信範囲の抜け道になる。ここで同じ検査を掛けて塞ぐ。
func TestAISystemPromptKeepsSendScope(t *testing.T) {
	s := aiContextFixture()
	s.contentYAML = []byte("content_hash: 5f2b9c1d0a\nreturn_key: BASE64RETURNKEYVALUE==\n")
	s.returnKey = []byte("BASE64RETURNKEYVALUE==")
	s.key = []byte("DERIVEDKEYMATERIAL")
	s.dir = "/Users/tester/Library/Application Support/ReqWeave/respondent/ab12cd34"

	got := s.AISystemPrompt()
	if !strings.Contains(got, aiSystemInstruction) {
		t.Fatal("指示文が載っていない")
	}
	if !strings.Contains(got, s.AIContext()) {
		t.Fatal("質問票の文脈が載っていない（毎回の呼び出しに含める）")
	}
	for _, pattern := range []string{`STK-\d`, `QS-\d`, `ISS-\d`, `\bq-\d`, `AUTHOR-\d`} {
		if regexp.MustCompile(pattern).MatchString(got) {
			t.Errorf("内部識別子が送信本文に現れた: /%s/", pattern)
		}
	}
	for _, secret := range []string{
		"山田 花子", "営業部", "5f2b9c1d0a", "BASE64RETURNKEYVALUE==", "DERIVEDKEYMATERIAL",
		"respondent/ab12cd34",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("送ってはいけない値が送信本文に現れた: %q", secret)
		}
	}
}

// 指示文は固定文であること（可変部を持たせると、そこが送信範囲の抜け道になる）。
func TestAISystemInstructionIsIndependentOfSession(t *testing.T) {
	first := aiContextFixture()
	second := aiContextFixture()
	second.Questionnaire.Addressee = "鈴木 一郎（購買部）"
	second.Questionnaire.ID = "QS-999"

	a := strings.TrimSuffix(first.AISystemPrompt(), first.AIContext())
	b := strings.TrimSuffix(second.AISystemPrompt(), second.AIContext())
	if a != b {
		t.Errorf("指示文が質問票によって変わる（固定文でないと送信範囲を検査しきれない）:\n%q\n%q", a, b)
	}
	if strings.Contains(aiSystemInstruction, "山田") || strings.Contains(aiSystemInstruction, "QS-") {
		t.Error("指示文が利用者データを含んでいる")
	}
}

// 中断発話は残るが、空の本文では発話を作らない（中身の無い記録を残さない）。
func TestAppendInterruptedUtteranceIgnoresEmptyBody(t *testing.T) {
	s := aiContextFixture()
	if err := s.AppendInterruptedUtterance("   \n "); err != nil {
		t.Fatal(err)
	}
	if len(s.Utterances) != 0 {
		t.Fatalf("空の中断発話を作った: %+v", s.Utterances)
	}
}
