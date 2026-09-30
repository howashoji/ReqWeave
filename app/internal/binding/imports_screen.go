package binding

// 本ファイルは取り込み画面のバインディング。
//
// 取り込み・抽出テキストと原本の参照（抽出できない資料も原本は保持する）・送信前プレビューと同意・
// 分析と候補の承認を担う。
//
// 権限判定は前段の共通実装（requireRole）で行い、画面側の判定に依存しない。
// 分類の付与は feedback_screen.go、プロジェクト観点は perspectives_screen.go が担う。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/importer"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// ImportKindOption は種別の選択肢（取り込み記録の kind。画面へ生の値を出さない）。
type ImportKindOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Summary は選択時の説明（1 行）。
	Summary string `json:"summary"`
}

// ImportKinds は取り込み種別の選択肢を返す（値集合は資料・議事録・開発AIフィードバックの 3 つで閉じている）。
func (a *API) ImportKinds() []ImportKindOption {
	return []ImportKindOption{
		{ID: string(importer.KindMaterial), Label: "資料",
			Summary: "既存の業務資料・仕様書など"},
		{ID: string(importer.KindMinutes), Label: "議事録",
			Summary: "打ち合わせの記録。決定・未決の候補を抽出する"},
		{ID: string(importer.KindDevAIFeedback), Label: "開発AIフィードバック",
			Summary: "開発AIからの質問・指摘・修正依頼"},
	}
}

// ImportView は取り込み済み一覧の 1 行。
type ImportView struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// KindLabel / StatusLabel / ClassificationLabel は画面表示用（内部値をそのまま出さない）。
	KindLabel  string `json:"kindLabel"`
	SourceName string `json:"sourceName"`
	ImportedAt string `json:"importedAt"`
	// Extracted はテキスト抽出に成功したか。false のときは原本のみ保持している。
	Extracted   bool   `json:"extracted"`
	StatusLabel string `json:"statusLabel"`
	// Classification は開発AIフィードバックのみ（未分類は空）。
	Classification      string `json:"classification,omitempty"`
	ClassificationLabel string `json:"classificationLabel,omitempty"`
	// FromTemplate は定型テンプレ由来か（開発AIフィードバックのみ）。
	FromTemplate bool `json:"fromTemplate,omitempty"`
	// AnalysisState は分析の一時状態（未分析は空。分析中 / 承認待ち）。
	AnalysisState string `json:"analysisState,omitempty"`
	AnalysisLabel string `json:"analysisLabel,omitempty"`
}

// importKindLabel は種別の日本語表示（値集合は 3 つで閉じている）。
func importKindLabel(kind importer.Kind) string {
	switch kind {
	case importer.KindMaterial:
		return "資料"
	case importer.KindMinutes:
		return "議事録"
	case importer.KindDevAIFeedback:
		return "開発AIフィードバック"
	default:
		return "種別不明"
	}
}

// importStatusLabel は抽出状態の日本語表示。
func importStatusLabel(status importer.ExtractionStatus) string {
	switch status {
	case importer.StatusExtracted:
		return "抽出済み"
	case importer.StatusFailed:
		return "抽出できません（原本のみ保持）"
	default:
		return "状態不明"
	}
}

// importAnalysisLabel は分析の一時状態の日本語表示。
func importAnalysisLabel(state string) string {
	switch state {
	case importer.AnalysisAnalyzing:
		return "分析中"
	case importer.AnalysisAwaitingApproval:
		return "承認待ち"
	default:
		return ""
	}
}

func importView(im *importer.Importer, meta importer.Meta) ImportView {
	out := ImportView{
		ID: meta.ID, Kind: string(meta.Kind), KindLabel: importKindLabel(meta.Kind),
		SourceName:  meta.SourceName,
		ImportedAt:  meta.ImportedAt.Local().Format("2006-01-02 15:04"),
		Extracted:   meta.ExtractionStatus == importer.StatusExtracted,
		StatusLabel: importStatusLabel(meta.ExtractionStatus),
	}
	if meta.Kind == importer.KindDevAIFeedback {
		out.FromTemplate = meta.FromTemplate != nil && *meta.FromTemplate
		if meta.Classification != "" {
			out.Classification = string(meta.Classification)
			out.ClassificationLabel = importer.ClassificationLabel(meta.Classification)
		}
	}
	if analysis, err := im.LoadAnalysis(meta.ID); err == nil && analysis != nil {
		out.AnalysisState = analysis.AnalysisState
		out.AnalysisLabel = importAnalysisLabel(analysis.AnalysisState)
	}
	return out
}

// Imports は取り込み済み一覧を返す（閲覧権限でも参照できる）。
func (a *API) Imports() ([]ImportView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	im := importer.New(s.store)
	list, err := im.List()
	if err != nil {
		return nil, err
	}
	out := make([]ImportView, 0, len(list))
	for _, meta := range list {
		out = append(out, importView(im, meta))
	}
	return out, nil
}

// ChooseImportFile は取り込む資料ファイルを選ぶ（OS の選択ダイアログ。取り込める形式に絞る）。
func (a *API) ChooseImportFile() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("ファイルの選択を開けません。アプリを再起動してください。")
	}
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "取り込む資料を選ぶ",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "取り込める資料 (*.txt;*.md;*.docx;*.xlsx;*.pptx;*.pdf)",
				Pattern: "*.txt;*.md;*.docx;*.xlsx;*.pptx;*.pdf"},
		},
	})
}

// ImportFile はファイルを取り込む（抽出できない資料も原本を保持する）。
func (a *API) ImportFile(path, kind string) (ImportView, error) {
	s, err := a.current()
	if err != nil {
		return ImportView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "資料の取り込み"); err != nil {
		return ImportView{}, err
	}
	if strings.TrimSpace(path) == "" {
		return ImportView{}, fmt.Errorf("取り込むファイルを選んでください。")
	}
	format, err := importFormatOf(path)
	if err != nil {
		// 動作ログ（取り込み系。**ファイル名・パスは残さない**ため拡張子だけを添える）。
		a.recordImportFailure("", importKindUnsupported, strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."))
		return ImportView{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ImportView{}, fmt.Errorf("ファイルを読み込めません（%s）。ファイルの場所と権限を確認してください。",
			filepath.Base(path))
	}
	return a.importInput(s, importer.Input{
		Kind: importer.Kind(kind), SourceName: filepath.Base(path),
		SourceFormat: format, Content: content,
	})
}

// ImportClipboardText は貼り付けたテキストを取り込む（クリップボード経路）。
func (a *API) ImportClipboardText(text, kind string) (ImportView, error) {
	s, err := a.current()
	if err != nil {
		return ImportView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "資料の取り込み"); err != nil {
		return ImportView{}, err
	}
	if strings.TrimSpace(text) == "" {
		return ImportView{}, fmt.Errorf("取り込む本文を貼り付けてください。")
	}
	return a.importInput(s, importer.Input{
		Kind: importer.Kind(kind), SourceFormat: importer.FormatClipboard, Content: []byte(text),
	})
}

// importInput は種別に応じた取り込み経路を選ぶ（ファイル・貼り付けで共通）。
//
// 開発AIフィードバックは定型テンプレ由来かどうかを判定して `from_template` を立てる必要があるため、
// 対話エンジンの ImportFeedback を通す（判定は開発AIフィードバックの分析と同じパースの実装を使う）。
// 資料・議事録は取り込みモジュールの通常経路（抽出して保存）。
func (a *API) importInput(s *dialogueSession, in importer.Input) (ImportView, error) {
	im := importer.New(s.store)
	var (
		meta *importer.Meta
		err  error
	)
	if in.Kind == importer.KindDevAIFeedback {
		meta, err = s.engine.ImportFeedback(in)
	} else {
		meta, err = im.ImportWithExtraction(in)
	}
	if err != nil {
		return ImportView{}, err
	}
	if meta.ExtractionStatus != importer.StatusExtracted {
		// 抽出できなかった事実を残す（原本は保持されている。画面の案内は別物）。
		a.recordImportFailure(meta.ID, importKindNotExtractable, string(meta.SourceFormat))
	}
	return importView(im, *meta), nil
}

// importFormatOf は拡張子から原本の形式を決める（対象形式は下の 6 つで閉じている）。
func importFormatOf(path string) (importer.Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		return importer.FormatTxt, nil
	case ".md":
		return importer.FormatMD, nil
	case ".docx":
		return importer.FormatDocx, nil
	case ".xlsx":
		return importer.FormatXlsx, nil
	case ".pptx":
		return importer.FormatPptx, nil
	case ".pdf":
		return importer.FormatPDF, nil
	default:
		return "", fmt.Errorf("この形式は取り込めません（%s）。txt / md / docx / xlsx / pptx / pdf のいずれかを選んでください。",
			filepath.Base(path))
	}
}

// ImportContentView は原本と抽出テキストの表示。
type ImportContentView struct {
	ID string `json:"id"`
	// Extracted は抽出テキスト（抽出できていない場合は空）。
	Extracted string `json:"extracted"`
	// ExtractionFailed は抽出できなかったか。原本は保持している。
	ExtractionFailed bool `json:"extractionFailed"`
	// SourceText は原本がテキスト形式のときの本文（docx / xlsx / pptx / pdf は空）。
	SourceText string `json:"sourceText,omitempty"`
	// SourcePath は原本の所在（プロジェクトフォルダからの相対パス）。
	SourcePath string `json:"sourcePath"`
	// SourceName は元ファイル名。
	SourceName string `json:"sourceName"`
	// Notice は画面へ出す案内（原因＋次の行動）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// ImportContent は原本と抽出テキストを返す（閲覧権限でも参照できる）。
func (a *API) ImportContent(importID string) (ImportContentView, error) {
	s, err := a.current()
	if err != nil {
		return ImportContentView{}, err
	}
	im := importer.New(s.store)
	meta, err := im.Load(importID)
	if err != nil {
		return ImportContentView{}, err
	}
	out := ImportContentView{
		ID: meta.ID, SourceName: meta.SourceName,
		SourcePath:       importer.SourcePath(meta.ID, meta.SourceFormat),
		ExtractionFailed: meta.ExtractionStatus != importer.StatusExtracted,
	}
	if out.ExtractionFailed {
		out.Notice = "この資料からはテキストを取り出せませんでした。原本は保持しています。" +
			"本文を貼り付けて取り込み直すと分析できます。"
	} else {
		text, err := im.ReadExtracted(meta.ID)
		if err != nil {
			return ImportContentView{}, err
		}
		out.Extracted = text
	}
	// 原本がテキスト形式のときは本文も返す（docx / xlsx / pptx / pdf は表示できないため所在のみ）。
	switch meta.SourceFormat {
	case importer.FormatTxt, importer.FormatMD, importer.FormatClipboard:
		raw, err := im.ReadSource(meta.ID)
		if err != nil {
			return ImportContentView{}, err
		}
		out.SourceText = string(raw)
	}
	return out, nil
}

// PreviewImportAnalysis は分析の送信前プレビューを返す（送信しない）。
//
// 表示内容は実際の送信内容そのもの（dialogue.PreviewImportAnalysis）。
func (a *API) PreviewImportAnalysis(importID string) (*dialogue.ImportSendPreview, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "資料の分析"); err != nil {
		return nil, err
	}
	return s.engine.PreviewImportAnalysis(importID)
}

// AnalyzeImport は取り込み資料を分析する。
//
// consentGiven が false のときは抽象化層の同意ゲートが送信を止める。
// 画面から同意なしで呼べる経路であっても資料は外部送信されない（同意なしの送信を構造で防ぐ）。
func (a *API) AnalyzeImport(importID string, consentGiven bool) (*dialogue.FeedbackAnalysis, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "資料の分析"); err != nil {
		return nil, err
	}
	if _, err := a.beginAICall(s, "資料の分析"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(a.context())
	a.setCancel(cancel)
	defer cancel()
	analysis, err := s.engine.AnalyzeImport(ctx, importID, consentGiven)
	if errors.Is(err, dialogue.ErrImportTooLarge) {
		// 分割上限の超過。**AI へは送っていない**ので AI 通信系ではない。
		a.recordImportFailure(importID, importKindTooLarge, "")
	}
	return analysis, err
}

// PendingImportCandidates は保全されている未承認候補を返す（中断後の復元）。
func (a *API) PendingImportCandidates(importID string) (*dialogue.Extraction, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	return s.engine.PendingMaterialAnalysis(importID)
}

// ApproveImportCandidates は承認した候補を記録へ反映する。
//
// 開発AIフィードバックは差し戻し手順へ接続する経路を通す。
// 承認していない候補は渡らないため反映されない。
func (a *API) ApproveImportCandidates(importID string, req dialogue.FeedbackApproval) (ApprovalOutcome, error) {
	s, err := a.current()
	if err != nil {
		return ApprovalOutcome{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "候補の承認"); err != nil {
		return ApprovalOutcome{}, err
	}
	meta, err := importer.New(s.store).Load(importID)
	if err != nil {
		return ApprovalOutcome{}, err
	}
	var applied *dialogue.FeedbackApplyResult
	if meta.Kind == importer.KindDevAIFeedback {
		applied, err = s.engine.ApplyFeedbackApproval(importID, req)
	} else {
		var material *dialogue.MaterialApplyResult
		material, err = s.engine.ApplyMaterialApproval(importID, req.MaterialApproval)
		if material != nil {
			applied = &dialogue.FeedbackApplyResult{MaterialApplyResult: material}
		}
	}
	if err != nil {
		// 競合はエラーではなく結果として返す（画面が三面を出して本人が承認する）。
		if outcome, ok := conflictOutcome(err); ok {
			return outcome, nil
		}
		return ApprovalOutcome{}, err
	}
	return ApprovalOutcome{Applied: appliedOf(applied.ApprovalResult, applied)}, nil
}
