package binding

// 本ファイルは成果物プレビュー・版差分の画面向けの公開バインディングのうち、
// 確定・差し戻し・フェーズ移行を担う。

import (
	"context"
	"errors"
	"fmt"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ConfirmCheckView は確定前チェックの結果。
type ConfirmCheckView struct {
	Confirmable bool `json:"confirmable"`
	// DraftRequirements はドラフト状態の要件項目 ID。
	DraftRequirements []string `json:"draftRequirements,omitempty"`
	// BlockingIssues は要件項目をブロックする未決状態の未決事項 ID。
	BlockingIssues []string `json:"blockingIssues,omitempty"`
	// NoRequirements は要件項目が 1 件も無いか（0 件では確定させない）。
	NoRequirements bool `json:"noRequirements,omitempty"`
	// HasDraft は確定できるドラフトがあるか。
	HasDraft bool `json:"hasDraft"`
	// Reason は確定できない理由（確定可のときは空）。
	Reason string `json:"reason,omitempty"`
}

// ConfirmResultView は確定の結果。
type ConfirmResultView struct {
	Version int `json:"version"`
	// AgreedRequirements は確定時に合意済みへ変えた要件項目 ID。
	AgreedRequirements []string `json:"agreedRequirements,omitempty"`
	// CanMoveToBasicDesign は基本設計フェーズへ移行できるか。
	CanMoveToBasicDesign bool `json:"canMoveToBasicDesign"`
}

// ConfirmCheck は確定前チェックを返す（完成度表示と同一の判定を使う）。
func (a *API) ConfirmCheck(kind string) (ConfirmCheckView, error) {
	s, err := a.current()
	if err != nil {
		return ConfirmCheckView{}, err
	}
	records, err := s.engine.Records()
	if err != nil {
		return ConfirmCheckView{}, err
	}
	conf := dialogue.Confirmable(records)
	draft, err := s.store.LoadDraft(kind)
	if err != nil {
		return ConfirmCheckView{}, err
	}
	out := ConfirmCheckView{
		DraftRequirements: conf.DraftRequirements,
		BlockingIssues:    conf.BlockingIssues,
		NoRequirements:    conf.NoRequirements,
		HasDraft:          len(draft) > 0,
	}
	// 確定を止めるのは「ブロックする未決事項の残存」と、
	// 確定する対象が無い場合（要件項目 0 件／ドラフト未生成）。
	// ドラフト状態の要件項目は一覧提示の対象であり、確定操作が合意済みへ変える。
	switch {
	case out.NoRequirements:
		out.Reason = "要件項目がまだ 1 件もありません。対話で要件を記録してから確定してください。"
	case !out.HasDraft:
		out.Reason = "確定できる成果物がまだありません。先に成果物を生成してください。"
	case len(out.BlockingIssues) > 0:
		out.Reason = fmt.Sprintf("要件項目をブロックする未決事項が %d 件あります。決着させてから確定してください。",
			len(out.BlockingIssues))
	default:
		out.Confirmable = true
	}
	return out, nil
}

// ConfirmDocument は確定操作。
//
// 確定前チェックを通ったときだけ、全要件項目を合意済みにしてドラフトを次の版へ複製する。
// work は着手時の進め方の選択。確定版の版番号は仮採番であり、取り込み時に
// 衝突したら内容を変えずに再採番する（複数の端末が同じ版番号で確定しうるため）。
func (a *API) ConfirmDocument(kind string, work WorkStart) (ConfirmResultView, error) {
	s, err := a.current()
	if err != nil {
		return ConfirmResultView{}, err
	}
	// 確定も成果物種別の予約の対象（同じ成果物を同時に 2 人で進めない）。開始時に記録し、完了時に解除する。
	release, err := a.beginDocumentWork(s, kind, work)
	if err != nil {
		return ConfirmResultView{}, err
	}
	defer release()
	check, err := a.ConfirmCheck(kind)
	if err != nil {
		return ConfirmResultView{}, err
	}
	if !check.Confirmable {
		return ConfirmResultView{}, errors.New(check.Reason)
	}

	// 全要件項目を合意済みにする。
	reqs, err := s.store.ListRequirements()
	if err != nil {
		return ConfirmResultView{}, err
	}
	var agreed []string
	for _, r := range reqs {
		if r.Status == projectstore.RequirementAgreed {
			continue
		}
		// 共有レコードの書き換えは基準版を検証してから行う（他の人の変更を黙って上書きしない）。
		base, err := s.store.CurrentBaseline(r.ID)
		if err != nil {
			return ConfirmResultView{}, err
		}
		if _, err := s.store.UpdateRequirementGuarded(base, r.ID, func(x *projectstore.Requirement) error {
			x.Status = projectstore.RequirementAgreed
			x.RevertedReason = ""
			return nil
		}); err != nil {
			return ConfirmResultView{}, err
		}
		agreed = append(agreed, r.ID)
		a.recordChange(s, auditlog.ChangeRecord{Target: r.ID, Change: auditlog.ChangeStatusChanged,
			Before: projectstore.RequirementDraft, After: projectstore.RequirementAgreed})
	}

	generator, err := a.newGenerator(s)
	if err != nil {
		return ConfirmResultView{}, err
	}
	version, err := generator.Confirm(kind)
	if err != nil {
		return ConfirmResultView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		Target: fmt.Sprintf("%s/v%d", kind, version.Version), Change: auditlog.ChangeStatusChanged,
		After: "confirmed"})

	return ConfirmResultView{
		Version: version.Version, AgreedRequirements: agreed,
		CanMoveToBasicDesign: kind == projectstore.DocKindRequirements,
	}, nil
}

// MoveToBasicDesign は基本設計フェーズへ移行する。
//
// 要件定義が確定していない状態では移行できない（確定へ誘導する）。
func (a *API) MoveToBasicDesign() error {
	s, err := a.current()
	if err != nil {
		return err
	}
	latest, err := s.store.LatestVersion(projectstore.DocKindRequirements)
	if err != nil {
		return err
	}
	if latest == 0 {
		return errors.New("要件定義がまだ確定していません。要件定義書を確定してから基本設計へ進んでください。")
	}
	before := s.store.Project().Phase
	if before == dialogue.PhaseBasicDesign {
		return nil
	}
	if err := s.store.UpdateProject(func(p *projectstore.Project) error {
		p.Phase = dialogue.PhaseBasicDesign
		return nil
	}); err != nil {
		return err
	}
	a.recordChange(s, auditlog.ChangeRecord{Target: "project", Change: auditlog.ChangeStatusChanged,
		Before: before, After: dialogue.PhaseBasicDesign})
	return nil
}

// newGenerator は現在の設定で生成器を組み立てる（対話エンジンと同じプロバイダ設定を使う）。
func (a *API) newGenerator(s *dialogueSession) (*docgen.Generator, error) {
	settings, err := a.settings()
	if err != nil {
		return nil, err
	}
	provider, ok := settings.DefaultProviderSetting()
	if !ok {
		return nil, errors.New("AI プロバイダが設定されていません。設定画面で登録してください。")
	}
	adapter, model, err := a.adapterFor(provider)
	if err != nil {
		return nil, err
	}
	logger, err := auditlog.New(s.store, s.store.Author().AuthorID)
	if err != nil {
		return nil, err
	}
	return docgen.NewGenerator(docgen.Config{
		Store:    s.store,
		Adapter:  adapter,
		Model:    model,
		Effort:   aiEffort(provider.Effort),
		Timeouts: a.adapterOptions().Timeouts,
		Recorder: auditlog.NewSendRecorder(logger, func(err error) {
			a.log.Error("audit.record_failed", "監査記録の保存に失敗しました",
				applog.F("source", "docgen"), applog.F("error", err))
			a.emitDialogue(dialogue.Event{Kind: dialogue.EventError,
				Message: "記録の保存に失敗しました: " + err.Error()})
		}),
		OnDegrade:    a.onEffortDegrade,
		OnPanic:      a.onStreamPanic,
		OnCallFailed: a.onAICallFailed,
	})
}

// EventDocument は成果物生成の Wails イベント名。
const EventDocument = "document:event"

// DocumentVersionView は版一覧の 1 行。
type DocumentVersionView struct {
	// Version は確定版の番号（0 = 最新ドラフト）。
	Version int    `json:"version"`
	Label   string `json:"label"`
	// ConfirmedAt は確定日時（ドラフトは空）。
	ConfirmedAt string `json:"confirmedAt,omitempty"`
	// SourceCount は収載したソースレコード数。
	SourceCount int `json:"sourceCount,omitempty"`
	// HasContent は内容があるか（ドラフト未生成なら false）。
	HasContent bool `json:"hasContent"`
}

// DocumentChapterView は成果物プレビューの 1 章。
type DocumentChapterView struct {
	FileName string   `json:"fileName"`
	Chapter  string   `json:"chapter"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Covers   []string `json:"covers,omitempty"`
}

// GenerateDocument は成果物を生成する。進行はイベントで届く。
//
// work は着手時の進め方の選択。開始時に当該成果物種別の予約を
// 確認・記録し、**生成が終わった時点で解除する**（進行が非同期のため解除も非同期側で行う）。
func (a *API) GenerateDocument(kind string, differential bool, work WorkStart) error {
	s, err := a.current()
	if err != nil {
		return err
	}
	if _, err := a.beginAICall(s, "成果物の生成"); err != nil {
		return err
	}
	release, err := a.beginDocumentWork(s, kind, work)
	if err != nil {
		return err
	}
	generator, err := a.newGenerator(s)
	if err != nil {
		release()
		return err
	}
	ctx, cancel := context.WithCancel(a.context())
	a.setCancel(cancel)

	var ch <-chan docgen.Event
	if differential {
		ch, err = generator.Regenerate(ctx, kind)
	} else {
		ch, err = generator.Generate(ctx, kind)
	}
	if err != nil {
		cancel()
		release()
		return err
	}
	// 閉じる・終わるときに完了を待てるようにする。
	a.background.Add(1)
	go func() {
		// 自前のゴルーチンのパニックは main の recover に届かず、記録なしでプロセスごと落ちる。
		// 最初に登録して最後に走らせ、後片づけを済ませてから記録・再送出する。
		defer a.log.RecoverPanic(applog.EventAppPanic)
		defer a.background.Done()
		defer cancel()
		defer release()
		for ev := range ch {
			if a.ctx == nil {
				continue
			}
			// エラーには利用者向けの 1 文を添える（正本は本層のエラーカタログ）。
			if ev.Kind == docgen.EventError && ev.UserMessage == "" {
				ev.UserMessage = a.aiUserMessage(ev.ErrorClass, ev.ErrorCode)
			}
			wailsruntime.EventsEmit(a.ctx, EventDocument, ev)
		}
	}()
	return nil
}

// DocumentVersions は版一覧（最新ドラフト + 確定版）を返す。
func (a *API) DocumentVersions(kind string) ([]DocumentVersionView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	draft, err := s.store.LoadDraft(kind)
	if err != nil {
		return nil, err
	}
	out := []DocumentVersionView{{Version: 0, Label: "ドラフト", HasContent: len(draft) > 0}}
	versions, err := s.store.Versions(kind)
	if err != nil {
		return nil, err
	}
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		out = append(out, DocumentVersionView{
			Version: v.Version, Label: fmt.Sprintf("確定版 v%d", v.Version),
			ConfirmedAt: v.ConfirmedAt.UTC().Format(time.RFC3339),
			SourceCount: len(v.SourceIDs), HasContent: true,
		})
	}
	return out, nil
}

// DocumentChapters は指定版の章一覧と本文を返す（0 = 最新ドラフト）。
func (a *API) DocumentChapters(kind string, version int) ([]DocumentChapterView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	chapters, err := a.chaptersOf(s, kind, version)
	if err != nil {
		return nil, err
	}
	titles, err := chapterTitles(kind)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentChapterView, 0, len(chapters))
	for _, c := range chapters {
		out = append(out, DocumentChapterView{
			FileName: c.FileName, Chapter: c.Chapter, Title: titles[c.FileName],
			Body: c.Body, Covers: c.Covers,
		})
	}
	return out, nil
}

// DocumentDiff は 2 つの版の差分を返す（0 = 最新ドラフト）。
func (a *API) DocumentDiff(kind string, from, to int) ([]docgen.ChapterDiff, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	before, err := a.chaptersOf(s, kind, from)
	if err != nil {
		return nil, err
	}
	after, err := a.chaptersOf(s, kind, to)
	if err != nil {
		return nil, err
	}
	return docgen.DiffDocuments(before, after), nil
}

// VerifyDocument は指定版の整合性検証を行う。
func (a *API) VerifyDocument(kind string, version int) (docgen.VerifyResult, error) {
	s, err := a.current()
	if err != nil {
		return docgen.VerifyResult{}, err
	}
	generator, err := a.newGenerator(s)
	if err != nil {
		return docgen.VerifyResult{}, err
	}
	return generator.VerifyCurrent(kind, version)
}

// ExportRequestView はエクスポートの入力。
type ExportRequestView struct {
	Destination        string `json:"destination"`
	Requirements       int    `json:"requirements"`
	BasicDesign        int    `json:"basicDesign"`
	IncludeBasicDesign bool   `json:"includeBasicDesign"`
	AcceptWarnings     bool   `json:"acceptWarnings"`
}

// ExportDocuments は成果物一式を出力する。
func (a *API) ExportDocuments(req ExportRequestView) (*docgen.ExportResult, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	generator, err := a.newGenerator(s)
	if err != nil {
		return nil, err
	}
	return generator.Export(docgen.ExportRequest{
		Destination:        req.Destination,
		Requirements:       req.Requirements,
		BasicDesign:        req.BasicDesign,
		IncludeBasicDesign: req.IncludeBasicDesign,
		AcceptWarnings:     req.AcceptWarnings,
	})
}

// chaptersOf は指定版（0 = ドラフト）の章を返す。
func (a *API) chaptersOf(s *dialogueSession, kind string, version int) ([]projectstore.DocumentChapter, error) {
	if version > 0 {
		return s.store.LoadVersion(kind, version)
	}
	return s.store.LoadDraft(kind)
}

// chapterTitles は章ファイル名 → 表示名（テンプレート定義から引く）。
func chapterTitles(kind string) (map[string]string, error) {
	tmpl, err := docgen.LoadTemplate()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range tmpl.Common {
		out[c.File] = c.Title
	}
	doc, err := tmpl.Document(kind)
	if err != nil {
		return nil, err
	}
	for _, c := range doc.Chapters {
		out[c.File] = c.Title
	}
	return out, nil
}
