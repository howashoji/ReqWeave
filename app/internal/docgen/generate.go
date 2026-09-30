package docgen

// 本ファイルは生成パイプラインの章生成と保存を担う。
//
// 章単位で AI 生成し、全章の完了後に原子的にドラフトを置換する。
// 途中失敗（AI API 障害を含む）では既存ドラフト・確定版を変更しない。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 生成イベントの種別（画面へは Wails イベントとして中継する）。
const (
	// EventChapterStart は 1 章の生成開始。
	EventChapterStart = "chapter-start"
	// EventChapterDone は 1 章の生成完了。
	EventChapterDone = "chapter-done"
	// EventDone は全章の完了（ドラフト置換まで済んだ）。
	EventDone = "done"
	// EventError は生成の失敗（既存ドラフト・確定版は変更されていない）。
	EventError = "error"
)

// Event は生成の進行状況。
type Event struct {
	Kind    string `json:"kind"`
	Chapter string `json:"chapter,omitempty"`
	Title   string `json:"title,omitempty"`
	Index   int    `json:"index,omitempty"`
	Total   int    `json:"total,omitempty"`
	// Reused は差分再生成で前回本文を再利用した章か。
	Reused bool `json:"reused,omitempty"`
	// Errors / Warnings は EventDone のときの自己検証の違反件数。
	Errors     int    `json:"errors,omitempty"`
	Warnings   int    `json:"warnings,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
	// ErrorCode は EventError のときのプロバイダ固有コード（ProviderError.Code）。
	// 画面の文言は分類 × Code から作る（未知の Code は分類ごとの既定へ倒す）。
	ErrorCode string `json:"errorCode,omitempty"`
	// UserMessage は利用者向けの 1 文（原因＋次の行動）。
	// **公開バインディング層が組み立てて詰める**（エラーの文言の一覧を 1 か所に保つため）。
	UserMessage string `json:"userMessage,omitempty"`
	Message     string `json:"message,omitempty"`
}

// 生成の並列度。
const (
	// defaultParallelism は章単位生成の並列度（許容 1〜3）。
	defaultParallelism = 2
	// throttledParallelism はレート制限を検知したときの縮退値。
	throttledParallelism = 1
)

// Config は生成器の構成。
type Config struct {
	Store    *projectstore.Store
	Adapter  aiprovider.Adapter
	Model    aiprovider.ModelInfo
	Effort   aiprovider.Effort
	Timeouts aiprovider.Timeouts
	Recorder aiprovider.SendRecorder
	// Parallelism は章生成の並列度（0 = 既定）。
	Parallelism int
	// OnDegrade は推論努力パラメータの縮退が起きたときの通知先。
	// 利用者へは通知せず動作ログへ記録するための口（nil なら記録しない）。
	OnDegrade func(model aiprovider.ModelInfo)
	// OnPanic はストリーミング用ゴルーチンのパニックの記録先。
	// 記録先を知るのはバインディング層だけ（本層は動作ログのパッケージへ依存しない）。
	OnPanic aiprovider.PanicRecorder
	// OnCallFailed は AI 呼び出しの失敗の記録先（失敗の分類・コードを動作ログへ残す）。
	// OnPanic と同じ委譲の形（本層は動作ログのパッケージへ依存しない）。nil なら記録しない。
	OnCallFailed aiprovider.FailureRecorder
}

// effortParams は推論努力の写像・モデル上限での丸め・縮退をまとめて行う。
// 縮退の通知は OnDegrade（対話エンジンの同名メソッドと同じ扱い）。
func (g *Generator) effortParams() aiprovider.EffortParams {
	effort := aiprovider.MapEffort(g.cfg.Adapter.ID(), g.cfg.Effort).ClampToModel(g.cfg.Model)
	effort, degraded := effort.Degrade(g.cfg.Model)
	if degraded && g.cfg.OnDegrade != nil {
		g.cfg.OnDegrade(g.cfg.Model)
	}
	return effort
}

// Generator は成果物ドキュメントの生成器。
type Generator struct {
	cfg Config
	// throttleMu / throttled はレート制限を検知して並列度を 1 へ落とした状態（生成器ごと）。
	throttleMu sync.Mutex
	throttled  bool
	// lastResult は直近の生成での自己検証結果（画面の検証結果一覧に使う）。
	lastResult *VerifyResult
}

// LastVerifyResult は直近の生成での自己検証結果を返す（未生成なら nil）。
func (g *Generator) LastVerifyResult() *VerifyResult {
	g.throttleMu.Lock()
	defer g.throttleMu.Unlock()
	return g.lastResult
}

// NewGenerator は生成器を作る。
func NewGenerator(cfg Config) (*Generator, error) {
	if cfg.Store == nil {
		return nil, errors.New("プロジェクトが開かれていません")
	}
	if cfg.Adapter == nil || cfg.Model.ID == "" {
		return nil, errors.New("AI プロバイダ・モデルが設定されていません")
	}
	return &Generator{cfg: cfg}, nil
}

// Result は生成の結果。
type Result struct {
	// Chapters は生成・組立てした章（保存済みのドラフト内容）。
	Chapters []projectstore.DocumentChapter
	// Dependencies は章ファイル名 → 収載ソース ID（版履歴へ渡す）。
	Dependencies map[string][]string
	// SourceIDs は全章の収載ソース ID。
	SourceIDs []string
}

// Generate は成果物ドキュメントを生成してドラフトを置き換える。
//
// 返すチャネルは EventDone または EventError で必ず閉じる（呼び出し側は読み切ること）。
func (g *Generator) Generate(ctx context.Context, kind string) (<-chan Event, error) {
	tmpl, err := LoadTemplate()
	if err != nil {
		return nil, err
	}
	doc, err := tmpl.Document(kind)
	if err != nil {
		return nil, err
	}
	records, err := g.records()
	if err != nil {
		return nil, err
	}

	out := make(chan Event, 16)
	go g.run(ctx, doc, tmpl, records, out, nil)
	return out, nil
}

// records はレコード類（章割当のデータソース）を読み出す。
func (g *Generator) records() (Records, error) {
	var r Records
	reqs, err := g.cfg.Store.ListRequirements()
	if err != nil {
		return r, err
	}
	decisions, err := g.cfg.Store.ListDecisions()
	if err != nil {
		return r, err
	}
	issues, err := g.cfg.Store.ListOpenIssues()
	if err != nil {
		return r, err
	}
	terms, err := g.cfg.Store.LoadTerms()
	if err != nil {
		return r, err
	}
	r.Requirements, r.Decisions, r.OpenIssues, r.Terms = reqs, decisions, issues, terms.Terms
	return r, nil
}

// reuse は差分再生成で前回本文を再利用する章（章ファイル名 → 本文）。nil なら全章を生成する。
type reuse map[string]string

// run は章生成 → 組立て → ドラフト置換を行う。
func (g *Generator) run(ctx context.Context, doc *DocumentTemplate, tmpl *Template,
	records Records, out chan<- Event, reusable reuse) {

	defer close(out)

	assignment := Assign(doc, records)
	generated := doc.GeneratedChapters()
	total := len(generated)
	project := g.cfg.Store.Project()
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)

	bodies := make([]string, len(assignment.Sources))
	var mu sync.Mutex
	var firstErr *aiprovider.ProviderError

	sem := make(chan struct{}, g.parallelism())
	var wg sync.WaitGroup
	for i, src := range assignment.Sources {
		if body, ok := reusable[src.Chapter.File]; ok {
			bodies[i] = body
			out <- Event{Kind: EventChapterDone, Chapter: src.Chapter.Chapter,
				Title: src.Chapter.Title, Index: i + 1, Total: total, Reused: true}
			continue
		}
		wg.Add(1)
		go func(i int, src ChapterSources) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mu.Lock()
			stop := firstErr != nil
			mu.Unlock()
			if stop || ctx.Err() != nil {
				return
			}

			out <- Event{Kind: EventChapterStart, Chapter: src.Chapter.Chapter,
				Title: src.Chapter.Title, Index: i + 1, Total: total}

			body, err := g.generateChapter(ctx, src)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			bodies[i] = body
			out <- Event{Kind: EventChapterDone, Chapter: src.Chapter.Chapter,
				Title: src.Chapter.Title, Index: i + 1, Total: total}
		}(i, src)
	}
	wg.Wait()

	if ctx.Err() != nil {
		out <- Event{Kind: EventError, ErrorClass: aiprovider.ErrClassTransient.String(),
			Message: "生成を中断しました"}
		return
	}
	if firstErr != nil {
		// 途中失敗では何も書かない（既存ドラフト・確定版は変更されない）。
		out <- Event{Kind: EventError, ErrorClass: firstErr.Class.String(), ErrorCode: firstErr.Code,
			Message: firstErr.Message}
		return
	}

	chapters := make([]projectstore.DocumentChapter, 0, len(doc.Chapters)+len(tmpl.Common))
	for i, src := range assignment.Sources {
		covers := requirementIDs(src.Requirements)
		chapters = append(chapters, projectstore.DocumentChapter{
			Chapter: src.Chapter.Chapter, FileName: src.Chapter.File,
			GeneratedAt: now, Covers: covers,
			Body: chapterHeader(src.Chapter, project.TargetSystemName, stamp, src.SourceIDs()) + bodies[i],
		})
	}
	// 機械組立て文書（目次・用語集・決定/未決リスト）は差分再生成でも常に組み立てる。
	in := AssembleInput{Document: doc, Records: records,
		TargetName: project.TargetSystemName, GeneratedAt: now}
	for _, c := range doc.Chapters {
		if c.IsGenerated() {
			continue
		}
		chapters = append(chapters, projectstore.DocumentChapter{
			Chapter: c.Chapter, FileName: c.File, GeneratedAt: now, Body: AssembleIndex(in),
		})
	}
	for _, c := range tmpl.Common {
		body := ""
		switch c.Chapter {
		case "glossary":
			body = AssembleGlossary(in)
		case "decisions":
			body = AssembleDecisions(in)
		case "issues":
			body = AssembleIssues(in)
		default:
			continue
		}
		chapters = append(chapters, projectstore.DocumentChapter{
			Chapter: c.Chapter, FileName: c.File, GeneratedAt: now, Body: body,
		})
	}

	// 3. 自己検証（機械検証。AI を使わない）。
	for i := range chapters {
		chapters[i].DocKind = doc.KindID
	}
	result := Verify(VerifyInput{Chapters: chapters, Records: records,
		AmbiguousWords: AmbiguousWordsFor(project)})

	// 4. 提示: 検証結果とともにドラフトとして保存する（違反があっても保存し、一覧で示す）。
	if err := g.cfg.Store.ReplaceDraft(doc.KindID, chapters); err != nil {
		out <- Event{Kind: EventError, ErrorClass: aiprovider.ErrClassPermanent.String(), Message: err.Error()}
		return
	}
	g.lastResult = &result
	out <- Event{Kind: EventDone, Total: total, Errors: result.Errors(), Warnings: result.Warnings()}
}

// parallelism は章生成の並列度を返す（許容 1〜3）。
func (g *Generator) parallelism() int {
	if g.isThrottled() {
		return throttledParallelism
	}
	n := g.cfg.Parallelism
	if n <= 0 {
		n = defaultParallelism
	}
	if n > 3 {
		n = 3
	}
	return n
}

// generateChapter は 1 章を生成する。レート制限を検知したら並列度 1 で 1 度だけ再試行する
// （プロバイダのレート制限が続くときは負荷を下げる）。
func (g *Generator) generateChapter(ctx context.Context, src ChapterSources) (string, *aiprovider.ProviderError) {
	body, err := g.callOnce(ctx, src)
	if err == nil {
		return body, nil
	}
	if !isRateLimited(err) {
		return "", err
	}
	g.throttle()
	return g.callOnce(ctx, src)
}

// throttle はレート制限の検知後に並列度を 1 へ落とす（以後の章にも効く）。
func (g *Generator) throttle() {
	g.throttleMu.Lock()
	defer g.throttleMu.Unlock()
	g.throttled = true
}

// isThrottled はレート制限による縮退中かを返す。
func (g *Generator) isThrottled() bool {
	g.throttleMu.Lock()
	defer g.throttleMu.Unlock()
	return g.throttled
}

func isRateLimited(err *aiprovider.ProviderError) bool {
	return err != nil && (err.HTTPStatus == 429 || err.RetryAfter > 0)
}

// callOnce は 1 章ぶんの AI 呼び出しを行い、本文を返す。
func (g *Generator) callOnce(ctx context.Context, src ChapterSources) (string, *aiprovider.ProviderError) {
	system := buildSystemPrompt(currentDocument(src), src.Chapter)
	user, labels := buildUserPrompt(src)
	effort := g.effortParams()

	ch, err := aiprovider.StreamRetrying(ctx, g.cfg.Adapter, aiprovider.ChatRequest{
		Model:    g.cfg.Model.ID,
		System:   system,
		Messages: []aiprovider.Message{{Role: aiprovider.RoleUser, Content: user}},
		Effort:   effort,
	}, aiprovider.StreamOptions{
		Timeouts:  g.cfg.Timeouts,
		Recorder:  g.cfg.Recorder,
		OnPanic:   g.cfg.OnPanic,
		OnFailure: g.cfg.OnCallFailed,
		Context:   aiprovider.RecordContext{Included: labels},
	})
	if err != nil {
		return "", &aiprovider.ProviderError{Class: aiprovider.ErrClassConfig,
			Provider: g.cfg.Adapter.ID(), Message: err.Error()}
	}

	var body strings.Builder
	var failure *aiprovider.ProviderError
	interrupted := false
	for ev := range ch {
		switch ev.Kind {
		case aiprovider.EventTextDelta:
			body.WriteString(ev.Text)
		case aiprovider.EventDone:
			interrupted = ev.Interrupted
		case aiprovider.EventError:
			failure = ev.Err
		}
	}
	if failure != nil {
		return "", failure
	}
	if interrupted {
		return "", &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient,
			Provider: g.cfg.Adapter.ID(), Message: "生成を中断しました"}
	}
	text := strings.TrimSpace(body.String())
	if text == "" {
		return "", &aiprovider.ProviderError{Class: aiprovider.ErrClassTransient,
			Provider: g.cfg.Adapter.ID(), Message: fmt.Sprintf("章 %s の本文が空でした", src.Chapter.Chapter)}
	}
	return text + "\n", nil
}

// currentDocument は章テンプレートが属する成果物種別を引く（プロンプトの役割定義に使う）。
func currentDocument(src ChapterSources) *DocumentTemplate {
	tmpl, err := LoadTemplate()
	if err != nil {
		return &DocumentTemplate{Name: "成果物ドキュメント"}
	}
	for i := range tmpl.Documents {
		if _, ok := tmpl.Documents[i].Chapter(src.Chapter.Chapter); ok {
			return &tmpl.Documents[i]
		}
	}
	return &DocumentTemplate{Name: "成果物ドキュメント"}
}

// Confirm はドラフトを次の確定版へ複製する。
//
// 確定前チェックは呼び出し側が行う（完成度表示と同一の判定を使うため）。
// 版履歴には確定時のソースレコード ID 集合と章ごとの依存マップを記録する。
func (g *Generator) Confirm(kind string) (*projectstore.DocumentVersion, error) {
	tmpl, err := LoadTemplate()
	if err != nil {
		return nil, err
	}
	doc, err := tmpl.Document(kind)
	if err != nil {
		return nil, err
	}
	records, err := g.records()
	if err != nil {
		return nil, err
	}
	assignment := Assign(doc, records)
	return g.cfg.Store.ConfirmDraft(kind, assignment.AllSourceIDs(), assignment.DependencyMap())
}

// VerifyCurrent は現在のドラフト（または指定版）を検証する（エクスポート前検証）。
//
// 生成時の自己検証と同じ検証器を使う。
func (g *Generator) VerifyCurrent(kind string, version int) (VerifyResult, error) {
	var chapters []projectstore.DocumentChapter
	var err error
	if version > 0 {
		chapters, err = g.cfg.Store.LoadVersion(kind, version)
	} else {
		chapters, err = g.cfg.Store.LoadDraft(kind)
	}
	if err != nil {
		return VerifyResult{}, err
	}
	records, err := g.records()
	if err != nil {
		return VerifyResult{}, err
	}
	for i := range chapters {
		chapters[i].DocKind = kind
	}
	return Verify(VerifyInput{Chapters: chapters, Records: records,
		AmbiguousWords: AmbiguousWordsFor(g.cfg.Store.Project())}), nil
}
