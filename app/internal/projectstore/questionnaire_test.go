package projectstore

import (
	"strings"
	"testing"
	"time"
)

func sampleQuestionnaire() Questionnaire {
	return Questionnaire{
		ID:           "QS-001",
		AddresseeRef: "STK-001",
		Addressee:    "佐藤（営業部）",
		IssuedAt:     time.Date(2026, 8, 28, 1, 2, 3, 0, time.UTC),
		IssuedBy:     "k.sato@example.co.jp",
		Status:       QuestionnaireIssued,
		Questions: []Question{
			{
				ID: "q-01", SourceIssue: "ISS-003", AnswerFormat: AnswerFormatChoice,
				Choices: []string{"即時", "日次バッチ"}, Terms: []string{"在庫引当"},
				Text: "在庫の引き当ては、注文を受けた時点で行いますか。", Background: "取り消しの扱いが変わります。",
			},
			{
				ID: "q-02", SourceIssue: "ISS-004", AnswerFormat: AnswerFormatFree,
				Text: "月末の締め作業で困っていることを教えてください。", Background: "対象範囲の判断に使います。",
			},
		},
	}
}

// 往復で全フィールドが失われない。
func TestQuestionnaireRoundTrip(t *testing.T) {
	q := sampleQuestionnaire()
	data, err := q.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	got, err := UnmarshalQuestionnaire(data)
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v\n%s", err, string(data))
	}
	if got.ID != q.ID || got.AddresseeRef != q.AddresseeRef || got.Addressee != q.Addressee ||
		!got.IssuedAt.Equal(q.IssuedAt) || got.IssuedBy != q.IssuedBy || got.Status != q.Status {
		t.Fatalf("フロントマターが往復で変化しました: %+v", *got)
	}
	if len(got.Questions) != 2 {
		t.Fatalf("質問数が %d です（期待 2）", len(got.Questions))
	}
	first := got.Questions[0]
	if first.ID != "q-01" || first.SourceIssue != "ISS-003" || first.AnswerFormat != AnswerFormatChoice {
		t.Fatalf("質問のメタが往復で変化しました: %+v", first)
	}
	if strings.Join(first.Choices, "|") != "即時|日次バッチ" || strings.Join(first.Terms, "|") != "在庫引当" {
		t.Fatalf("選択肢・用語が往復で変化しました: %+v", first)
	}
	if first.Text != q.Questions[0].Text || first.Background != q.Questions[0].Background {
		t.Fatalf("質問本文・背景説明が往復で変化しました: %+v", first)
	}
	if got.Questions[1].Choices != nil {
		t.Fatalf("自由記述に選択肢が付きました: %+v", got.Questions[1])
	}

	// 2 回目の往復でバイト列が安定する。
	again, err := got.Marshal()
	if err != nil {
		t.Fatalf("2 回目の Marshal に失敗: %v", err)
	}
	if string(again) != string(data) {
		t.Fatalf("往復でバイト列が変化しました:\n--- 1 回目\n%s\n--- 2 回目\n%s", string(data), string(again))
	}
}

// 区切り文字を含む選択肢でも往復が壊れない（メタ行の値は YAML 表記で書く）。
func TestQuestionnaireRoundTripWithDelimiterInChoice(t *testing.T) {
	q := sampleQuestionnaire()
	q.Questions[0].Choices = []string{"即時, ただし夜間を除く", "日次バッチ"}
	data, err := q.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	got, err := UnmarshalQuestionnaire(data)
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v", err)
	}
	if len(got.Questions[0].Choices) != 2 || got.Questions[0].Choices[0] != "即時, ただし夜間を除く" {
		t.Fatalf("選択肢が壊れました: %+v", got.Questions[0].Choices)
	}
}

// 自版が知らないフィールド・メタ行を書き戻しで失わない。
func TestQuestionnaireKeepsUnknownFields(t *testing.T) {
	src := `---
id: QS-001
addressee_ref: STK-001
addressee: 佐藤（営業部）
issued_at: 2026-08-28T01:02:03Z
issued_by: k.sato@example.co.jp
status: issued
future_field: 将来の値
---

### q-01
- source_issue: ISS-003
- answer_format: free
- future_meta: 将来のメタ

#### 質問
在庫の引き当てはいつ行いますか。
#### 背景説明
取り消しの扱いが変わります。
`
	q, err := UnmarshalQuestionnaire([]byte(src))
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v", err)
	}
	out, err := q.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	if !strings.Contains(string(out), "future_field: 将来の値") {
		t.Fatalf("未知のフロントマターが失われました:\n%s", string(out))
	}
	if !strings.Contains(string(out), "- future_meta: 将来のメタ") {
		t.Fatalf("未知のメタ行が失われました:\n%s", string(out))
	}
}

// パスコード・その導出値は保持しない（読み込んだファイルに紛れていても捨てる）。
func TestQuestionnaireDropsPasscodeFields(t *testing.T) {
	src := `---
id: QS-001
addressee_ref: STK-001
addressee: 佐藤（営業部）
issued_at: 2026-08-28T01:02:03Z
issued_by: k.sato@example.co.jp
status: issued
passcode: AbCdEfGh2345
kdf_salt: c2FsdA==
---

### q-01
- source_issue: ISS-003
- answer_format: free
- passcode_hint: AbCdEfGh2345

#### 質問
在庫の引き当てはいつ行いますか。
#### 背景説明
取り消しの扱いが変わります。
`
	q, err := UnmarshalQuestionnaire([]byte(src))
	if err != nil {
		t.Fatalf("Unmarshal に失敗: %v", err)
	}
	out, err := q.Marshal()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	for _, banned := range []string{"AbCdEfGh2345", "passcode", "kdf_salt"} {
		if strings.Contains(string(out), banned) {
			t.Fatalf("パスコード相当の値が保持されました（%s）:\n%s", banned, string(out))
		}
	}
}

// 受け渡し用の写しは発行者の利用者 ID と状態を含まない。
func TestMarshalForExchange(t *testing.T) {
	q := sampleQuestionnaire()
	data, err := q.MarshalForExchange()
	if err != nil {
		t.Fatalf("Marshal に失敗: %v", err)
	}
	for _, banned := range []string{"issued_by", "k.sato@example.co.jp", "status:"} {
		if strings.Contains(string(data), banned) {
			t.Fatalf("受け渡し用の写しに %q が含まれています:\n%s", banned, string(data))
		}
	}
	// 回答に必要な項目は残る。
	for _, want := range []string{"id: QS-001", "addressee_ref: STK-001", "addressee: 佐藤（営業部）",
		"issued_at:", "### q-01", "source_issue: ISS-003", "在庫の引き当ては、注文を受けた時点で行いますか。", "取り消しの扱いが変わります。"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("受け渡し用の写しに %q がありません:\n%s", want, string(data))
		}
	}

	// 写しは専用の読み取りでのみ解釈できる（状態・発行者を必須としない）。
	got, err := UnmarshalExchangeQuestionnaire(data)
	if err != nil {
		t.Fatalf("写しを解釈できません: %v", err)
	}
	if got.ID != q.ID || got.AddresseeRef != q.AddresseeRef || len(got.Questions) != len(q.Questions) {
		t.Fatalf("写しの内容が違います: %+v", got)
	}
	if got.IssuedBy != "" || got.Status != "" {
		t.Fatalf("写しに発行者・状態が入っています: %+v", got)
	}
	if _, err := UnmarshalQuestionnaire(data); err == nil {
		t.Fatalf("写しがプロジェクトデータの質問票として受理されました（状態・発行者が必須のはず）")
	}

	// 写しの往復でバイト列が安定する。
	again, err := got.MarshalForExchange()
	if err != nil {
		t.Fatalf("2 回目の Marshal に失敗: %v", err)
	}
	if string(again) != string(data) {
		t.Fatalf("写しの往復でバイト列が変化しました")
	}
}

func TestQuestionnaireValidate(t *testing.T) {
	cases := map[string]func(q *Questionnaire){
		"ID が不正":       func(q *Questionnaire) { q.ID = "QS-1" },
		"宛先の名簿 ID が不正": func(q *Questionnaire) { q.AddresseeRef = "佐藤" },
		"宛先表示名がない":     func(q *Questionnaire) { q.Addressee = "" },
		"発行日時がない":      func(q *Questionnaire) { q.IssuedAt = time.Time{} },
		"発行者がない":       func(q *Questionnaire) { q.IssuedBy = "" },
		"状態が 3 状態以外":   func(q *Questionnaire) { q.Status = "draft" },
		"質問がない":        func(q *Questionnaire) { q.Questions = nil },
		"質問 ID が重複":    func(q *Questionnaire) { q.Questions[1].ID = "q-01" },
		"質問 ID の形式違い":  func(q *Questionnaire) { q.Questions[0].ID = "q1" },
		"発行元未決事項がない":   func(q *Questionnaire) { q.Questions[0].SourceIssue = "" },
		"回答形式が列挙外":     func(q *Questionnaire) { q.Questions[0].AnswerFormat = "yes_no" },
		"選択肢型に選択肢がない":  func(q *Questionnaire) { q.Questions[0].Choices = nil },
		"複数選択に選択肢がない": func(q *Questionnaire) {
			q.Questions[1].AnswerFormat = AnswerFormatMultiChoice
		},
		"選択肢＋自由記述に選択肢がない": func(q *Questionnaire) {
			q.Questions[1].AnswerFormat = AnswerFormatChoiceWithFree
		},
		"質問本文がない": func(q *Questionnaire) { q.Questions[0].Text = "" },
		"背景説明がない": func(q *Questionnaire) { q.Questions[0].Background = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := sampleQuestionnaire()
			mutate(&q)
			if err := q.Validate(); err == nil {
				t.Fatalf("不正な質問票が受理されました")
			}
			if _, err := q.Marshal(); err == nil {
				t.Fatalf("不正な質問票が書き出されました")
			}
		})
	}
}

func TestQuestionnaireFindQuestionAndRefs(t *testing.T) {
	q := sampleQuestionnaire()
	if got, ok := q.FindQuestion("q-02"); !ok || got.AnswerFormat != AnswerFormatFree {
		t.Fatalf("質問を引けません: %+v (ok=%v)", got, ok)
	}
	if _, ok := q.FindQuestion("q-09"); ok {
		t.Fatalf("存在しない質問が引けました")
	}
	if got := AnswerRef("QS-001", "q-02"); got != "QS-001#q-02" {
		t.Fatalf("参照形式が違います: %q", got)
	}
	if got := FormatQuestionID(3); got != "q-03" {
		t.Fatalf("質問 ID の形式が違います: %q", got)
	}
}

// 回答参照（QS-nnn#q-nn）の分解。AnswerRef と往復し、他の参照形式を拾わない。
func TestParseAnswerRef(t *testing.T) {
	for _, ref := range []string{"QS-001#q-01", "QS-100#q-99", "QS-007#q-001"} {
		qid, question, ok := ParseAnswerRef(ref)
		if !ok {
			t.Fatalf("回答参照を解釈できない: %q", ref)
		}
		if got := AnswerRef(qid, question); got != ref {
			t.Errorf("往復しない: %q → %q", ref, got)
		}
	}
	// 他の参照形式・壊れた値を拾わない（拾うと発話・資料の遡及を横取りする）。
	for _, ref := range []string{
		"S-0001#utt-00001", // 発話参照
		"IMP-001#L10-L20",  // 取り込み資料参照
		"QS-001",           // 設問が無い
		"QS-1#q-01",        // 質問票 ID の桁数違い
		"QS-001#q-1",       // 設問 ID の桁数不足
		"QS-001#qq-01",     // 設問 ID の接頭辞違い
		"QS-001#q-0a",      // 数字でない
		"#q-01",            // 質問票 ID が空
		"",
	} {
		if _, _, ok := ParseAnswerRef(ref); ok {
			t.Errorf("回答参照でない値を解釈してしまった: %q", ref)
		}
	}
}
