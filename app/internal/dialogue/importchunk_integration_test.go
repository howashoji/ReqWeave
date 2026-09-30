//go:build integration

// 結合テスト（対話エンジン × 抽象化層 × 監査ログ）。外部 API は呼ばずアダプタをスタブへ差し替える。
// 対象: 同意ゲート・import_refs の記録 / 分割送信・部分失敗の再実行。

package dialogue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// chunkStub はチャンクごとに成否を切り替えられるスタブアダプタ。
type chunkStub struct {
	// failFor に含まれる呼び出し回数（1 始まり）で一時的エラーを返す。
	failFor map[int]bool
	calls   int
	reqs    []aiprovider.ChatRequest
}

func (s *chunkStub) ID() aiprovider.ProviderID                                  { return aiprovider.ProviderAnthropic }
func (s *chunkStub) ListModels(context.Context) ([]aiprovider.ModelInfo, error) { return nil, nil }
func (s *chunkStub) VerifyKey(context.Context) error                            { return nil }

func (s *chunkStub) StreamMessage(ctx context.Context, req aiprovider.ChatRequest) (<-chan aiprovider.StreamEvent, error) {
	s.calls++
	call := s.calls
	s.reqs = append(s.reqs, req)
	ch := make(chan aiprovider.StreamEvent, 4)
	go func() {
		defer close(ch)
		if s.failFor[call] {
			ch <- aiprovider.StreamEvent{Kind: aiprovider.EventError,
				Err: &aiprovider.ProviderError{Class: aiprovider.ErrClassPermanent,
					Provider: aiprovider.ProviderAnthropic, HTTPStatus: 400, Message: "入力不正"}}
			return
		}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventTextDelta, Text: `{"decisions":[]}`}
		ch <- aiprovider.StreamEvent{Kind: aiprovider.EventDone}
	}()
	return ch, nil
}

// newChunkEngine は監査記録つきの対話エンジンを作る（分割・記録の結合検証用）。
func newChunkEngine(t *testing.T, stub *chunkStub) (*Engine, *projectstore.Store) {
	t.Helper()
	root := t.TempDir() + "/在庫管理システム"
	store, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatalf("プロジェクトを作成できない: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger, err := auditlog.New(store, "k.sato@example.co.jp")
	if err != nil {
		t.Fatalf("監査ログを作れない: %v", err)
	}
	e, err := New(Config{
		Store:    store,
		Adapter:  stub,
		Model:    aiprovider.ModelInfo{ID: "claude-test", ContextWindow: 5000, MaxOutput: 1500},
		Effort:   aiprovider.EffortStandard,
		Recorder: auditlog.NewSendRecorder(logger, func(err error) { t.Errorf("実績の追記に失敗: %v", err) }),
	})
	if err != nil {
		t.Fatalf("対話エンジンを作れない: %v", err)
	}
	return e, store
}

var testRef = aiprovider.ImportRef{ID: "IMP-001", SourceName: "現行業務フロー.docx",
	ImportedAt: time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC)}

const injectedContext = "### 既存の決定（1 行要約）\n\n- DEC-001: 在庫単位はロットとする"

func testSend(consent bool) ImportSend {
	return ImportSend{
		Refs: []aiprovider.ImportRef{testRef}, ConsentGiven: consent,
		System: "システムプロンプト", Context: injectedContext,
		Effort: aiprovider.MapEffort(aiprovider.ProviderAnthropic, aiprovider.EffortStandard),
		Labels: []string{"decisions"},
	}
}

func mustPlan(t *testing.T, text string) *ImportChunkRun {
	t.Helper()
	plan, err := PlanImportChunks(text,
		ImportChunkBudget{ContextWindow: 5000, FixedTokens: 200, MaxOutputTokens: 1500}, 20)
	if err != nil {
		t.Fatalf("分割計画を作れない: %v", err)
	}
	if len(plan.Chunks) < 3 {
		t.Fatalf("前提が崩れている: 部分失敗の検証には 3 チャンク以上が必要（%d 件）", len(plan.Chunks))
	}
	run, err := NewImportChunkRun(plan)
	if err != nil {
		t.Fatalf("実行状態を作れない: %v", err)
	}
	return run
}

// 同意なしの取り込み分析はプロバイダへ送信されない。
func TestRunImportChunksBlockedWithoutConsent(t *testing.T) {
	stub := &chunkStub{}
	e, store := newChunkEngine(t, stub)
	run := mustPlan(t, sampleDocument(6, 2, 6))

	if err := e.RunImportChunks(context.Background(), run, testSend(false)); err != nil {
		t.Fatalf("実行自体が失敗した: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("同意なしで送信された: %d 回", stub.calls)
	}
	if len(run.Succeeded()) != 0 {
		t.Errorf("送信していないのに成功チャンクがある: %d 件", len(run.Succeeded()))
	}
	failed := run.Failed()
	if len(failed) == 0 || failed[0].Err == nil {
		t.Fatalf("同意ゲートの失敗が記録されていない: %+v", failed)
	}
	if !strings.Contains(failed[0].Err.Message, "同意") {
		t.Errorf("原因が同意ゲートだと分からない: %q", failed[0].Err.Message)
	}
	sends, err := auditlog.ReadAISends(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("監査記録を読めない: %v", err)
	}
	if len(sends) != 0 {
		t.Errorf("送信していないのに送信記録が残った: %+v", sends)
	}
}

// 各チャンクは独立の送信になり、同一の注入文脈を持つ。
// 送信記録に import_refs が残り、セッション ID は付かない。
func TestRunImportChunksSendsEachChunkWithSameContext(t *testing.T) {
	stub := &chunkStub{}
	e, store := newChunkEngine(t, stub)
	text := sampleDocument(6, 2, 6)
	run := mustPlan(t, text)
	total := len(run.Plan.Chunks)

	if err := e.RunImportChunks(context.Background(), run, testSend(true)); err != nil {
		t.Fatalf("分割送信に失敗: %v", err)
	}
	if stub.calls != total {
		t.Fatalf("チャンク数ぶん送信されていない: %d 回（チャンク %d 件）", stub.calls, total)
	}
	if !run.Complete() {
		t.Errorf("全チャンク成功のはずが未完了: %+v", run.Failed())
	}
	for i, req := range stub.reqs {
		if len(req.Messages) != 1 {
			t.Fatalf("送信 %d のメッセージ数が違う: %d", i+1, len(req.Messages))
		}
		content := req.Messages[0].Content
		if !strings.HasPrefix(content, injectedContext) {
			t.Errorf("送信 %d に同一の注入文脈が付いていない: %.60q", i+1, content)
		}
		if !strings.Contains(content, run.Plan.Chunks[i].Text) {
			t.Errorf("送信 %d に当該チャンクの本文が入っていない", i+1)
		}
		// チャンク間で候補を受け渡さない（前のチャンクの本文が混ざらない）。
		if i > 0 && strings.Contains(content, run.Plan.Chunks[i-1].Text) {
			t.Errorf("送信 %d に前チャンクの本文が混ざっている", i+1)
		}
		if !req.ConsentGiven || len(req.ImportRefs) != 1 || req.ImportRefs[0].ID != "IMP-001" {
			t.Errorf("送信 %d に同意・資料識別が付いていない: %+v", i+1, req.ImportRefs)
		}
	}

	sends, err := auditlog.ReadAISends(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("監査記録を読めない: %v", err)
	}
	if len(sends) != total {
		t.Fatalf("送信記録がチャンク数ぶん残っていない: %d 件", len(sends))
	}
	for _, s := range sends {
		if len(s.ImportRefs) != 1 || s.ImportRefs[0].ID != "IMP-001" ||
			s.ImportRefs[0].SourceName != "現行業務フロー.docx" ||
			!s.ImportRefs[0].ImportedAt.Equal(testRef.ImportedAt) {
			t.Errorf("import_refs が記録されていない: %+v", s.ImportRefs)
		}
		if s.Session != "" {
			t.Errorf("取り込み分析の送信にセッション ID が付いている: %q", s.Session)
		}
		if !strings.Contains(strings.Join(s.Included, " "), "IMP-001#L") {
			t.Errorf("送信範囲（行）が included に残っていない: %+v", s.Included)
		}
		if !strings.Contains(s.Prompt, "システムプロンプト") || !strings.Contains(s.Prompt, "在庫の引当") {
			t.Errorf("送信本文（System + Messages）が記録されていない: %.80q", s.Prompt)
		}
	}
}

// 一部チャンクが失敗しても成功済みは保持され、失敗分だけ再実行できる。
func TestRunImportChunksRetriesOnlyFailedChunks(t *testing.T) {
	stub := &chunkStub{failFor: map[int]bool{2: true}}
	e, _ := newChunkEngine(t, stub)
	run := mustPlan(t, sampleDocument(6, 2, 6))
	total := len(run.Plan.Chunks)

	if err := e.RunImportChunks(context.Background(), run, testSend(true)); err != nil {
		t.Fatalf("分割送信に失敗: %v", err)
	}
	if got := len(run.Succeeded()); got != total-1 {
		t.Fatalf("成功チャンクの件数が違う: %d（期待 %d）", got, total-1)
	}
	failed := run.Failed()
	if len(failed) != 1 || failed[0].Index != 2 {
		t.Fatalf("失敗チャンクが特定できていない: %+v", failed)
	}
	if run.Complete() {
		t.Error("失敗があるのに完了扱いになっている")
	}

	// 再実行: 失敗した 1 件だけを送り直す（成功済みは再送しない）。
	stub.failFor = nil
	before := stub.calls
	if err := e.RunImportChunks(context.Background(), run, testSend(true)); err != nil {
		t.Fatalf("再実行に失敗: %v", err)
	}
	if got := stub.calls - before; got != 1 {
		t.Errorf("再実行で送り直した件数が違う: %d 回（期待 1 回）", got)
	}
	if !run.Complete() {
		t.Fatalf("再実行後も未完了: %+v", run.Failed())
	}
	if got := len(run.Succeeded()); got != total {
		t.Errorf("再実行後の成功件数が違う: %d（期待 %d）", got, total)
	}
	// 再送したのは失敗したチャンクの本文であること。
	last := stub.reqs[len(stub.reqs)-1].Messages[0].Content
	if !strings.Contains(last, run.Plan.Chunks[1].Text) {
		t.Error("再実行で失敗チャンク以外を送っている")
	}
}

// 送信前プレビューは実際の送信内容そのものである。
//
// プレビュー用の説明文を別に持たない（表示と送信が食い違わない）ことを、
// プレビューの各値と実際にアダプタへ渡ったリクエストの突き合わせで固定する。
func TestPreviewImportAnalysisMatchesActualSend(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	meta := newMaterial(t, store, importer.KindMaterial)

	preview, err := e.PreviewImportAnalysis(meta.ID)
	if err != nil {
		t.Fatalf("プレビューに失敗: %v", err)
	}
	if preview.TooLarge {
		t.Fatalf("収まる資料が大きすぎると判定された: %+v", preview)
	}
	if preview.ChunkCount != len(preview.Chunks) || preview.ChunkCount == 0 {
		t.Fatalf("分割数と本文が一致しない: %+v", preview)
	}
	if preview.EstimatedTokens <= 0 {
		t.Errorf("概算トークンが出ていない: %+v", preview)
	}
	// プレビューは送信しない（この時点でアダプタは呼ばれない）。
	if stub.calls != 0 {
		t.Fatalf("プレビューで送信された: %d 回", stub.calls)
	}
	// 一時状態も作らない（分析していないため）。
	if saved, err := importer.New(store).LoadAnalysis(meta.ID); err != nil || saved != nil {
		t.Errorf("プレビューで一時状態が作られた: %+v %v", saved, err)
	}

	if _, err := e.AnalyzeImport(context.Background(), meta.ID, true); err != nil {
		t.Fatalf("分析に失敗: %v", err)
	}
	if len(stub.reqs) != preview.ChunkCount {
		t.Fatalf("送信回数が分割数と違う: %d（プレビュー %d）", len(stub.reqs), preview.ChunkCount)
	}
	sent := stub.reqs[0]
	if sent.System != preview.System {
		t.Errorf("システムプロンプトがプレビューと違う:\n--- preview ---\n%s\n--- sent ---\n%s",
			preview.System, sent.System)
	}
	if !strings.Contains(sent.Messages[0].Content, preview.Context) {
		t.Errorf("注入文脈がプレビューと違う:\n--- preview ---\n%s\n--- sent ---\n%s",
			preview.Context, sent.Messages[0].Content)
	}
	for _, chunk := range preview.Chunks {
		if !strings.Contains(stub.reqs[chunk.Index-1].Messages[0].Content, chunk.Text) {
			t.Errorf("%d/%d 部の本文がプレビューと違う", chunk.Index, preview.ChunkCount)
		}
	}
}

// 分割上限を超える資料はプレビューで案内し、分析を開始しない。
func TestPreviewImportAnalysisReportsTooLarge(t *testing.T) {
	stub := &stubAdapter{scripts: []string{materialResponse}}
	e, store := newTestEngine(t, stub)
	// コンテキストウィンドウと最大出力を絞り、1 チャンクの割り当てを小さくして上限超過を作る。
	e.cfg.Model.ContextWindow = 16000
	e.cfg.Model.MaxOutput = 1024
	im := importer.New(store)
	// 段落（空行区切り）を大量に作る。1 段落が分割の最小単位のため、段落が無いと分割できない。
	var body strings.Builder
	for i := 0; i < 20000; i++ {
		body.WriteString("在庫管理の現行業務に関する記述です。\n\n")
	}
	meta, err := im.Import(importer.Input{
		Kind: importer.KindMaterial, SourceName: "大きい資料.md", SourceFormat: importer.FormatMD,
		Content:          []byte(body.String()),
		ExtractionStatus: importer.StatusExtracted, ExtractedText: body.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := e.PreviewImportAnalysis(meta.ID)
	if err != nil {
		t.Fatalf("プレビューでエラーになった（案内を返すこと）: %v", err)
	}
	if !preview.TooLarge {
		t.Fatalf("上限超過が示されていない: %+v", preview)
	}
	if !strings.Contains(preview.Notice, "範囲") {
		t.Errorf("次の行動が案内されていない: %q", preview.Notice)
	}
	if stub.calls != 0 {
		t.Errorf("上限超過で送信された: %d 回", stub.calls)
	}
}
