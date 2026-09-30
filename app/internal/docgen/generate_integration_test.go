//go:build integration

// 結合テスト（生成パイプライン × 実ファイル）。AI プロバイダはスタブへ差し替える。

package docgen

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// stubAdapter は章ごとに固定の本文を返すアダプタ。
type stubAdapter struct {
	mu       sync.Mutex
	calls    int
	requests []aiprovider.ChatRequest
	// failAfter が 0 より大きいとき、その回数を超えた呼び出しを失敗させる。
	failAfter int
	failErr   *aiprovider.ProviderError
	// rateLimitChapter が空でないとき、その章観点 ID の生成を必ずレート制限エラーにする
	// （AIプロバイダ層の再試行を超えても解消しない状況）。
	rateLimitChapter string
	// bodySuffix は生成本文の末尾に足す文字列（再生成で内容が変わることの確認に使う）。
	bodySuffix string
}

func (s *stubAdapter) ID() aiprovider.ProviderID                                  { return aiprovider.ProviderAnthropic }
func (s *stubAdapter) ListModels(context.Context) ([]aiprovider.ModelInfo, error) { return nil, nil }
func (s *stubAdapter) VerifyKey(context.Context) error                            { return nil }

func (s *stubAdapter) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.requests = append(s.requests, req)
	rateLimit := s.rateLimitChapter != "" &&
		strings.Contains(req.System, "（"+s.rateLimitChapter+"）")
	s.mu.Unlock()

	ch := make(chan aiprovider.StreamEvent, 4)
	go func() {
		defer close(ch)
		if rateLimit {
			ch <- aiprovider.StreamEvent{Kind: aiprovider.EventError,
				Err: &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient, HTTPStatus: 429,
					Message: "rate limited"}}
			return
		}
		if s.failAfter > 0 && n > s.failAfter {
			ch <- aiprovider.StreamEvent{Kind: aiprovider.EventError, Err: s.failErr}
			return
		}
		s.mu.Lock()
		suffix := s.bodySuffix
		s.mu.Unlock()
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta,
			Text: "## 本文\n\n生成された内容です。FR-INV-001 を参照します。" + suffix}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone}
	}()
	return ch, nil
}

func newTestGenerator(t *testing.T, stub *stubAdapter) (*Generator, *projectstore.Store) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "在庫管理システム")
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 生成対象のレコードを用意する。
	d, err := store.CreateDecision(projectstore.Decision{TopicKey: "background/current-state",
		Body: "現状は Excel 台帳。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "営業部 佐藤", Due: "2026-09-30",
		Body: "引当の単位", Evidence: []string{"S-0001#utt-00002"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Body: "受注確定時に在庫を引き当てること。",
		AcceptanceCriteria: []string{"3 秒以内"}, Decisions: []string{d.ID},
		BlockedBy: []string{issue.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertTermGuarded(projectstore.RecordBaseline{}, projectstore.Term{Name: "在庫引当", NameEn: "Stock Allocation",
		Definition: "受注に対して在庫を確保すること。"}); err != nil {
		t.Fatal(err)
	}

	g, err := NewGenerator(Config{Store: store, Adapter: stub,
		Model:  aiprovider.ModelInfo{ID: "claude-test", ContextWindow: 200000, MaxOutput: 8192},
		Effort: aiprovider.EffortStandard})
	if err != nil {
		t.Fatal(err)
	}
	return g, store
}

func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	// AIプロバイダ層の再試行（指数バックオフ）を含むため余裕を取る。
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatalf("チャネルが閉じられない（受信済み %d 件）", len(out))
		}
	}
}

// 生成でメタモデルの全章（目次・用語集・決定/未決リストを含む）が生成される。
func TestGenerateAllChapters(t *testing.T) {
	stub := &stubAdapter{}
	g, store := newTestGenerator(t, stub)

	ch, err := g.Generate(context.Background(), projectstore.DocKindRequirements)
	if err != nil {
		t.Fatalf("生成を開始できない: %v", err)
	}
	events := collect(t, ch)
	if last := events[len(events)-1]; last.Kind != EventDone {
		t.Fatalf("生成が完了しない: %+v", events[len(events)-1])
	}

	chapters, err := store.LoadDraft(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	// 12 章観点 + 目次 + 共通 3 文書 = 16 ファイル。
	if len(chapters) != 16 {
		t.Fatalf("生成された文書数が違う: %d", len(chapters))
	}
	byName := map[string]projectstore.DocumentChapter{}
	for _, c := range chapters {
		byName[c.FileName] = c
	}
	for _, want := range []string{"00-index.md", "01-business-context.md", "12-risks-assumptions.md",
		"glossary.md", "decisions.md", "issues.md"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("%s が生成されていない", want)
		}
	}
	// AI 生成の章は 12 回だけ呼ぶ（機械組立て文書で AI を呼ばない）。
	if stub.calls != 12 {
		t.Errorf("AI 呼び出し回数が違う: %d（期待 12）", stub.calls)
	}
	// 冒頭メタ節と収載レコード（章の covers）。
	fr := byName["06-functional-requirements.md"]
	if !strings.Contains(fr.Body, "- 対象システム: 在庫管理システム") {
		t.Errorf("冒頭メタ節が無い:\n%s", fr.Body)
	}
	if len(fr.Covers) != 1 || fr.Covers[0] != "FR-INV-001" {
		t.Errorf("収載要件項目が記録されていない: %+v", fr.Covers)
	}
}

// 送信は当該章のソースレコード・用語・章仕様に限る（AI へ送る範囲を必要最小限にする）。
func TestGenerateSendsOnlyChapterSources(t *testing.T) {
	stub := &stubAdapter{}
	g, _ := newTestGenerator(t, stub)
	ch, _ := g.Generate(context.Background(), projectstore.DocKindRequirements)
	collect(t, ch)

	var background, functional aiprovider.ChatRequest
	for _, req := range stub.requests {
		switch {
		case strings.Contains(req.System, "（background）"):
			background = req
		case strings.Contains(req.System, "（functional-requirements）"):
			functional = req
		}
	}
	if background.System == "" || functional.System == "" {
		t.Fatal("章ごとのリクエストが見つからない")
	}
	// 業務背景の章に機能要件のレコードを載せない（章をまたいだ送信をしない）。
	if strings.Contains(background.Messages[0].Content, "FR-INV-001") {
		t.Errorf("他章の要件項目が送信されている:\n%s", background.Messages[0].Content)
	}
	// 機能要件の章にはブロックする未決事項が明示される（推測補完の禁止）。
	body := functional.Messages[0].Content
	if !strings.Contains(body, "ISS-001") || !strings.Contains(body, "推測で埋めず") {
		t.Errorf("未決事項の明示指示が無い:\n%s", body)
	}
	// 用語集は全章へ注入する（表記の統一）。
	if !strings.Contains(body, "Stock Allocation") {
		t.Errorf("用語集が注入されていない:\n%s", body)
	}
	// 推測補完の禁止をシステムプロンプトで指示する。
	if !strings.Contains(functional.System, "推測で補完しません") {
		t.Errorf("推測補完の禁止が指示されていない:\n%s", functional.System)
	}
}

// 途中失敗で既存ドラフト・確定版が変更されない。
func TestGenerateFailureKeepsExisting(t *testing.T) {
	stub := &stubAdapter{}
	g, store := newTestGenerator(t, stub)

	// 1 回目は成功させ、ドラフトと確定版を作る。
	ch, _ := g.Generate(context.Background(), projectstore.DocKindRequirements)
	collect(t, ch)
	if _, err := store.ConfirmDraft(projectstore.DocKindRequirements, nil, nil); err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadDraft(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}

	// 2 回目は途中で失敗させる。
	stub.failAfter = stub.calls + 3
	stub.failErr = &aiprovider.ProviderError{Class: aiprovider.ErrClassConfig,
		Provider: aiprovider.ProviderAnthropic, HTTPStatus: 401, Code: "unauthorized",
		Message: "invalid api key"}
	ch, _ = g.Generate(context.Background(), projectstore.DocKindRequirements)
	events := collect(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventError || last.ErrorClass != "config" {
		t.Fatalf("失敗が通知されない: %+v", last)
	}
	// プロバイダ固有コードも画面まで運ぶ（画面の文言を分類 × コードで出し分けるため）。
	if last.ErrorCode != "unauthorized" {
		t.Errorf("プロバイダ固有コードが渡らない: %+v", last)
	}

	after, err := store.LoadDraft(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("ドラフトの文書数が変わった: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Body != after[i].Body {
			t.Errorf("ドラフトが変更された: %s", before[i].FileName)
		}
	}
	confirmed, err := store.LoadVersion(projectstore.DocKindRequirements, 1)
	if err != nil || len(confirmed) != len(before) {
		t.Errorf("確定版が変更された: %d 件 %v", len(confirmed), err)
	}
}

// AI プロバイダ層の再試行を超えるレート制限では、章生成の並列度を 1 へ縮退する。
//
// 一時的なレート制限は AIプロバイダ抽象化層が再試行で吸収する。ここで扱うのは
// 再試行しても解消しない場合であり、以後の章を 1 並列に落として負荷を下げる。
func TestGenerateThrottlesOnRateLimit(t *testing.T) {
	stub := &stubAdapter{rateLimitChapter: "scope"}
	g, store := newTestGenerator(t, stub)

	ch, _ := g.Generate(context.Background(), projectstore.DocKindRequirements)
	events := collect(t, ch)
	last := events[len(events)-1]
	if last.Kind != EventError || last.ErrorClass != "transient" {
		t.Fatalf("レート制限が一時的エラーとして通知されない: %+v", last)
	}
	if !g.isThrottled() {
		t.Error("レート制限を検知しても縮退していない")
	}
	if g.parallelism() != 1 {
		t.Errorf("並列度が 1 へ縮退していない: %d", g.parallelism())
	}
	// 失敗した生成ではドラフトを書かない。
	if chapters, _ := store.LoadDraft(projectstore.DocKindRequirements); len(chapters) != 0 {
		t.Errorf("失敗したのにドラフトが作られた: %d 件", len(chapters))
	}
}

// 章単位生成の並列度は 1〜3 に収める。
func TestParallelismBounds(t *testing.T) {
	cases := map[int]int{0: defaultParallelism, 1: 1, 3: 3, 9: 3}
	for given, want := range cases {
		g := &Generator{cfg: Config{Parallelism: given}}
		if got := g.parallelism(); got != want {
			t.Errorf("並列度 %d → %d（期待 %d）", given, got, want)
		}
	}
}

// 生成の中断（ctx キャンセル）でもドラフトを書き換えない。
func TestGenerateInterrupted(t *testing.T) {
	stub := &stubAdapter{}
	g, store := newTestGenerator(t, stub)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := g.Generate(ctx, projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, ch)
	if last := events[len(events)-1]; last.Kind != EventError {
		t.Fatalf("中断が通知されない: %+v", last)
	}
	if chapters, _ := store.LoadDraft(projectstore.DocKindRequirements); len(chapters) != 0 {
		t.Errorf("中断したのにドラフトが作られた: %d 件", len(chapters))
	}
}

// 生成器の構成が足りないときは開始前にエラーにする。
func TestNewGeneratorValidates(t *testing.T) {
	if _, err := NewGenerator(Config{}); err == nil {
		t.Error("プロジェクトなしで生成器が作れた")
	}
	if _, err := NewGenerator(Config{Store: &projectstore.Store{}}); err == nil {
		t.Error("アダプタなしで生成器が作れた")
	}
	_ = errors.New
}

// 生成の完了時に自己検証（V1〜V6）の結果が付いてくる。
func TestGenerateRunsSelfVerification(t *testing.T) {
	stub := &stubAdapter{}
	g, _ := newTestGenerator(t, stub)

	ch, _ := g.Generate(context.Background(), projectstore.DocKindRequirements)
	events := collect(t, ch)
	done := events[len(events)-1]
	if done.Kind != EventDone {
		t.Fatalf("生成が完了しない: %+v", done)
	}
	// テストデータにはブロックする未決事項（ISS-001）があるため V3 のエラーが 1 件出る。
	if done.Errors == 0 {
		t.Errorf("自己検証のエラーが数えられていない: %+v", done)
	}
	result := g.LastVerifyResult()
	if result == nil {
		t.Fatal("検証結果が保持されていない")
	}
	var hasV3 bool
	for _, v := range result.Violations {
		if v.Check == CheckBlockingOpenIssue && v.Target == "ISS-001" {
			hasV3 = true
		}
	}
	if !hasV3 {
		t.Errorf("ブロックする未決事項が検証結果に出ない: %+v", result.Violations)
	}
}

// 影響章のみ再生成し、変更のない章の本文はバイト単位で変化しない。
func TestRegenerateOnlyAffectedChapters(t *testing.T) {
	stub := &stubAdapter{}
	g, store := newTestGenerator(t, stub)

	// 1 回目: 全章生成。
	ch, _ := g.Generate(context.Background(), projectstore.DocKindRequirements)
	collect(t, ch)
	before, err := store.LoadDraft(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := stub.calls

	// 業務背景の決定事項だけを変更する（変更履歴へ記録する）。
	logger, err := auditlog.New(store, "k.sato@example.co.jp")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // 生成日時より後の変更として記録されるようにする
	if err := logger.RecordChange(auditlog.ChangeRecord{Target: "DEC-001",
		Change: auditlog.ChangeUpdated, After: "現状は Excel 台帳と紙の受払簿。"}); err != nil {
		t.Fatal(err)
	}

	// 2 回目: 差分再生成。
	stub.mu.Lock()
	stub.bodySuffix = "（再生成）"
	stub.mu.Unlock()
	ch, err = g.Regenerate(context.Background(), projectstore.DocKindRequirements)
	if err != nil {
		t.Fatalf("差分再生成を開始できない: %v", err)
	}
	events := collect(t, ch)
	if last := events[len(events)-1]; last.Kind != EventDone {
		t.Fatalf("再生成が完了しない: %+v", last)
	}

	// 影響章（業務背景）だけ AI を呼ぶ。
	regenerated := stub.calls - callsAfterFirst
	if regenerated != 1 {
		t.Errorf("再生成した章数が違う: %d（期待 1）", regenerated)
	}
	// 再利用した章はイベントで示される。
	reused := 0
	for _, ev := range events {
		if ev.Kind == EventChapterDone && ev.Reused {
			reused++
		}
	}
	if reused != 11 {
		t.Errorf("再利用した章数が違う: %d（期待 11）", reused)
	}

	after, err := store.LoadDraft(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	beforeByName := map[string]projectstore.DocumentChapter{}
	for _, c := range before {
		beforeByName[c.FileName] = c
	}
	// 変更のない章の本文はメタ節の生成日時を除いて一致する。
	diffs := DiffDocuments(before, after)
	for _, d := range diffs {
		switch d.FileName {
		case "01-business-context.md":
			if !d.Changed() {
				t.Errorf("影響章が再生成されていない: %+v", d)
			}
		case "06-functional-requirements.md", "12-risks-assumptions.md":
			// 本文（メタ節を除く）が変わっていないこと。
			body := beforeByName[d.FileName].Body
			for _, c := range after {
				if c.FileName != d.FileName {
					continue
				}
				if stripHeader(body) != stripHeader(c.Body) {
					t.Errorf("非影響章の本文が変化した: %s", d.FileName)
				}
			}
		}
	}
}

// stripHeader は冒頭メタ節を除いた本文を返す（生成日時の差を無視して比較する）。
func stripHeader(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return body
}

// 差分再生成でも機械組立て文書は常に再組立てする。
func TestRegenerateAlwaysRebuildsAssembled(t *testing.T) {
	stub := &stubAdapter{}
	g, store := newTestGenerator(t, stub)
	ch, _ := g.Generate(context.Background(), projectstore.DocKindRequirements)
	collect(t, ch)

	// 新しい未決事項を足す（機械組立ての未決事項リストに出るはず）。
	if _, err := store.CreateOpenIssue(projectstore.OpenIssue{Owner: "情シス",
		Body: "バックアップ方針", Evidence: []string{"S-0001#utt-00003"}}); err != nil {
		t.Fatal(err)
	}
	ch, err := g.Regenerate(context.Background(), projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)

	chapters, err := store.LoadDraft(projectstore.DocKindRequirements)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chapters {
		if c.FileName != "issues.md" {
			continue
		}
		if !strings.Contains(c.Body, "バックアップ方針") {
			t.Errorf("機械組立て文書が再組立てされていない:\n%s", c.Body)
		}
		return
	}
	t.Fatal("未決事項リストが見つからない")
}
