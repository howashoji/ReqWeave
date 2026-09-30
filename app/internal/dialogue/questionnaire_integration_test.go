//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package dialogue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// createOpenIssues は質問票の元になる未決事項を作る。
func createOpenIssues(t *testing.T, store *projectstore.Store) []string {
	t.Helper()
	var ids []string
	for _, spec := range []struct{ owner, body string }{
		{"営業部長", "在庫引当のタイミングを即時とするか日次とするか"},
		{"情報システム部", "月末締めで手作業になっている範囲"},
	} {
		issue, err := store.CreateOpenIssue(projectstore.OpenIssue{
			Owner: spec.owner, Status: projectstore.OpenIssueOpen,
			Evidence: []string{"S-0001#utt-00001"}, NeedsStakeholder: true, Body: spec.body,
		})
		if err != nil {
			t.Fatalf("未決事項を作れない: %v", err)
		}
		ids = append(ids, issue.ID)
	}
	return ids
}

const questionnaireScript = `{"questions":[
 {"source_open_issue_id":"ISS-001","question_text":"在庫の引き当ては、注文を受けた時点で行いますか。",
  "background":"引き当てとは、受注に対して在庫を確保することです。取り消しの扱いが変わります。",
  "answer_format":"choice","choices":["受注した時点で行う","1日1回まとめて行う","不明"]},
 {"source_open_issue_id":"ISS-002","question_text":"月末の締め作業で手作業になっている工程を教えてください。",
  "background":"どこまでを対象にするかの判断に使います。","answer_format":"free","choices":[]}
]}`

// 未決事項ごとに質問が生成され、発行元と紐づく。
func TestGenerateQuestionnaire(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionnaireScript}}
	e, store := newTestEngine(t, stub)
	ids := createOpenIssues(t, store)

	got, err := e.GenerateQuestionnaire(context.Background(), ids)
	if err != nil {
		t.Fatalf("質問文の生成に失敗: %v", err)
	}
	if got.Fallback {
		t.Fatalf("正常応答なのに縮退しました: %+v", got)
	}
	if len(got.Drafts) != 2 {
		t.Fatalf("質問数が %d です（期待 2）: %+v", len(got.Drafts), got.Drafts)
	}

	covered := map[string]bool{}
	for _, d := range got.Drafts {
		covered[d.SourceOpenIssueID] = true
		if d.QuestionText == "" || d.Background == "" {
			t.Fatalf("質問文・背景説明が空です: %+v", d)
		}
		switch d.AnswerFormat {
		case projectstore.AnswerFormatChoice, projectstore.AnswerFormatMultiChoice,
			projectstore.AnswerFormatFree, projectstore.AnswerFormatChoiceWithFree:
		default:
			t.Fatalf("回答形式が列挙外です: %q", d.AnswerFormat)
		}
		for _, c := range d.Choices {
			if c == "不明" {
				t.Fatalf("「不明」の選択肢が残っています: %+v", d)
			}
		}
	}
	for _, id := range ids {
		if !covered[id] {
			t.Fatalf("未決事項 %s に質問が付いていません", id)
		}
	}

	// 送信内容に未決事項・用語の文脈が入る。
	if len(stub.reqs) != 1 {
		t.Fatalf("AI 呼び出し回数が %d です（期待 1）", len(stub.reqs))
	}
	prompt := stub.reqs[0].Messages[0].Content
	if !strings.Contains(prompt, "在庫引当のタイミング") || !strings.Contains(prompt, ids[0]) {
		t.Fatalf("未決事項の論点が送信内容に入っていません:\n%s", prompt)
	}
	if !strings.Contains(stub.reqs[0].System, "「不明」の選択肢は作りません") {
		t.Fatalf("生成規律が出力契約に入っていません")
	}
}

// AI 障害でも縮退して手入力へ進める（エラーで止まらない）。
func TestGenerateQuestionnaireFallsBackOnProviderError(t *testing.T) {
	stub := &stubAdapter{err: &aiprovider.ProviderError{
		Class: aiprovider.ErrClassConfig, Provider: aiprovider.ProviderAnthropic, Message: "invalid api key"}}
	e, store := newTestEngine(t, stub)
	ids := createOpenIssues(t, store)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := e.GenerateQuestionnaire(ctx, ids)
	if err != nil {
		t.Fatalf("AI 障害でエラーが返りました（縮退すべき）: %v", err)
	}
	if !got.Fallback {
		t.Fatalf("縮退として扱われていません: %+v", got)
	}
	if len(got.Drafts) != len(ids) {
		t.Fatalf("テンプレート数が %d です（期待 %d）", len(got.Drafts), len(ids))
	}
	if !strings.Contains(got.Drafts[0].QuestionText, "在庫引当") {
		t.Fatalf("論点が転記されていません: %+v", got.Drafts[0])
	}
	if got.Notice == "" || strings.Contains(got.Notice, "invalid api key") {
		t.Fatalf("案内が不適切です（内部メッセージの露出）: %q", got.Notice)
	}
}

// 応答が壊れていても縮退する（形式不正で止まらない）。
func TestGenerateQuestionnaireFallsBackOnBrokenResponse(t *testing.T) {
	stub := &stubAdapter{scripts: []string{"JSON ではない応答"}}
	e, store := newTestEngine(t, stub)
	ids := createOpenIssues(t, store)

	got, err := e.GenerateQuestionnaire(context.Background(), ids)
	if err != nil {
		t.Fatalf("形式不正でエラーが返りました（縮退すべき）: %v", err)
	}
	if !got.Fallback || len(got.Drafts) != len(ids) {
		t.Fatalf("縮退の内容が違います: %+v", got)
	}
}

// 一部の未決事項に質問が付かなかった場合はテンプレートで補う（1 未決事項 = 1 件以上）。
func TestGenerateQuestionnaireFillsMissingIssues(t *testing.T) {
	partial := `{"questions":[{"source_open_issue_id":"ISS-001","question_text":"在庫の引き当てはいつ行いますか。",
	 "background":"取り消しの扱いが変わります。","answer_format":"free","choices":[]}]}`
	stub := &stubAdapter{scripts: []string{partial}}
	e, store := newTestEngine(t, stub)
	ids := createOpenIssues(t, store)

	got, err := e.GenerateQuestionnaire(context.Background(), ids)
	if err != nil {
		t.Fatalf("生成に失敗: %v", err)
	}
	if got.Fallback {
		t.Fatalf("一部生成できているのに全体が縮退しました")
	}
	covered := map[string]bool{}
	for _, d := range got.Drafts {
		covered[d.SourceOpenIssueID] = true
	}
	for _, id := range ids {
		if !covered[id] {
			t.Fatalf("未決事項 %s に質問が付いていません: %+v", id, got.Drafts)
		}
	}
}

// 決着済みの未決事項は質問票にできない。
func TestGenerateQuestionnaireRejectsResolvedIssue(t *testing.T) {
	stub := &stubAdapter{scripts: []string{questionnaireScript}}
	e, store := newTestEngine(t, stub)
	ids := createOpenIssues(t, store)
	decision, err := store.CreateDecision(projectstore.Decision{
		TopicKey: "background/current-state", Evidence: []string{"S-0001#utt-00001"},
		Body: "在庫引当は受注時点で行う。",
	})
	if err != nil {
		t.Fatalf("決定事項を作れない: %v", err)
	}
	base, err := store.CurrentBaseline(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveOpenIssueGuarded(base, ids[0], decision.ID); err != nil {
		t.Fatalf("未決事項を決着させられない: %v", err)
	}

	if _, err := e.GenerateQuestionnaire(context.Background(), ids); err == nil {
		t.Fatalf("決着済みの未決事項が受理されました")
	}
	if _, err := e.GenerateQuestionnaire(context.Background(), nil); err == nil {
		t.Fatalf("未決事項の指定なしが受理されました")
	}
}
