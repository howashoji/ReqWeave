package binding

// 本ファイルは進捗レポートのバインディング。
//
// 組立ての正本は docgen.ProgressReport であり、本層はデータの読み出しと期間の解釈、
// 出力（ファイル保存・クリップボードコピー）だけを担う。
//
// 完成度と確定可否の算出は対話エンジンが正本であり、
// 本層が docgen へ算出関数として渡す（docgen から対話エンジンを呼ばない = 依存の向き）。
//
// レポートはプロジェクトデータへ保存しない（使い切りの出力）。
// 生成中に AI プロバイダを呼ばない（オフライン・API 障害中も動作する。
// 本ファイルが aiprovider へ依存しないことが構造的な担保）。

import (
	"fmt"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/docgen"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ProgressReportRequest は進捗レポートの生成条件。
//
// From / To は表示タイムゾーンの暦日（YYYY-MM-DD）で受け取り、両端を含む。
type ProgressReportRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ProgressReportView は生成した進捗レポート。
//
// Markdown はそのままファイル保存・クリップボードコピーに使う（同一の本文 = 出力経路で内容が変わらない）。
type ProgressReportView struct {
	Markdown string `json:"markdown"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// ProgressReportFileName は保存時の既定ファイル名。
const ProgressReportFileName = "progress-report.md"

// ProgressReport は指定期間の進捗レポートを組み立てて返す（閲覧権限でも生成できる）。
func (a *API) ProgressReport(req ProgressReportRequest) (ProgressReportView, error) {
	s, err := a.current()
	if err != nil {
		return ProgressReportView{}, err
	}
	from, err := parsePeriodBound(req.From, false)
	if err != nil {
		return ProgressReportView{}, err
	}
	to, err := parsePeriodBound(req.To, true)
	if err != nil {
		return ProgressReportView{}, err
	}
	if from.IsZero() || to.IsZero() {
		return ProgressReportView{}, fmt.Errorf("対象期間の開始日と終了日を指定してください。")
	}

	in, err := progressReportInput(s.store, from, to)
	if err != nil {
		return ProgressReportView{}, err
	}
	markdown, err := docgen.ProgressReport(in)
	if err != nil {
		return ProgressReportView{}, err
	}
	return ProgressReportView{Markdown: markdown, From: req.From, To: req.To}, nil
}

// progressReportInput はレポートの入力をプロジェクトデータから読み出す（読み出しのみ）。
func progressReportInput(store *projectstore.Store, from, to time.Time) (docgen.ProgressReportInput, error) {
	reqs, err := store.ListRequirements()
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}
	decisions, err := store.ListDecisions()
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}
	issues, err := store.ListOpenIssues()
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}
	terms, err := store.LoadTerms()
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}
	questionnaires, err := store.ListQuestionnaires()
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}
	// 期首値の復元には期間より前の履歴が要るため、全期間の変更履歴を読む。
	changes, err := auditlog.ReadChanges(store.Root(), time.Time{}, time.Time{})
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}
	feedback, err := importer.New(store).SummarizeFeedback(from, to)
	if err != nil {
		return docgen.ProgressReportInput{}, err
	}

	project := store.Project()
	return docgen.ProgressReportInput{
		ProjectName:      project.TargetSystemName,
		Phase:            project.Phase,
		From:             from,
		To:               to,
		Now:              time.Now().UTC(),
		Location:         time.Local,
		ProjectCreatedAt: project.CreatedAt,
		Records: docgen.Records{Requirements: reqs, Decisions: decisions,
			OpenIssues: issues, Terms: terms.Terms},
		Changes:        changes,
		Questionnaires: questionnaires,
		Feedback:       feedback,
		Completeness:   reportCompleteness,
	}, nil
}

// reportCompleteness は充足率の算出を対話エンジンへ委ねる。
func reportCompleteness(phase string, r docgen.Records) ([]docgen.ChapterProgress, error) {
	chapters, err := dialogue.Completeness(phase, dialogue.Records{
		Requirements: r.Requirements, Decisions: r.Decisions,
		OpenIssues: r.OpenIssues, Terms: r.Terms})
	if err != nil {
		return nil, err
	}
	out := make([]docgen.ChapterProgress, 0, len(chapters))
	for _, c := range chapters {
		out = append(out, docgen.ChapterProgress{ChapterID: c.ChapterID, Name: c.Name,
			Percent: c.Percent, Satisfied: c.Satisfied, Total: c.Total})
	}
	return out, nil
}

// ChooseProgressReportDestination は進捗レポートの保存先を選ぶ（OS の保存ダイアログ）。
func (a *API) ChooseProgressReportDestination() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("保存先の選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "進捗レポートの保存先を選ぶ",
		DefaultFilename: ProgressReportFileName,
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Markdown (*.md)", Pattern: "*.md"},
		},
	})
}

// SaveProgressReport は進捗レポートを Markdown ファイルへ保存する。
//
// 保存先は利用者が選んだプロジェクトフォルダの外であり、プロジェクトデータは変化しない。
func (a *API) SaveProgressReport(req ProgressReportRequest, dst string) (string, error) {
	if strings.TrimSpace(dst) == "" {
		return "", fmt.Errorf("保存先のファイル名を指定してください。")
	}
	view, err := a.ProgressReport(req)
	if err != nil {
		return "", err
	}
	if err := docgen.SaveProgressReport(dst, view.Markdown); err != nil {
		return "", err
	}
	return dst, nil
}

// CopyProgressReport は進捗レポートをクリップボードへコピーする。
func (a *API) CopyProgressReport(req ProgressReportRequest) (string, error) {
	view, err := a.ProgressReport(req)
	if err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("クリップボードへコピーできません。アプリを再起動してください。")
	}
	if err := wailsruntime.ClipboardSetText(a.ctx, view.Markdown); err != nil {
		return "", fmt.Errorf("クリップボードへコピーできませんでした。ファイルへ保存してください。")
	}
	return view.Markdown, nil
}
