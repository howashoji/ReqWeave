package docgen

// 本ファイルはエクスポートを担う。
//
// 出力は一時フォルダで組み立ててから移動する（途中失敗で出力先の既存ファイルを壊さない）。
// シークレットキー・キー参照名を出力に含めない
//（出力対象を成果物ドキュメントと検証結果に限る構造で担保する）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 出力ファイル名。
const (
	IntroFileName            = "CLAUDE.md"
	FeedbackTemplateFileName = "feedback-template.md"
	ExportReportFileName     = "export-report.md"
)

// commonDir は共通文書（用語集・決定/未決リスト）の出力先。
const commonDir = "00-project"

// ExportRequest はエクスポートの入力。
type ExportRequest struct {
	// Destination は出力先フォルダ。
	Destination string
	// Requirements は要件定義の対象版（0 = 最新ドラフト）。
	Requirements int
	// BasicDesign は基本設計の対象版（0 = 出力しない。同梱するときのみ指定）。
	BasicDesign int
	// IncludeBasicDesign は基本設計書を同梱するか。
	IncludeBasicDesign bool
	// AcceptWarnings は違反がある状態でも出力するか（警告付きエクスポート）。
	AcceptWarnings bool
}

// ExportResult はエクスポートの結果。
type ExportResult struct {
	// Files は出力したファイル（出力先からの相対パス）。
	Files []string `json:"files"`
	// Verification は出力対象の検証結果。
	Verification VerifyResult `json:"verification"`
	// Exported は実際に出力したか（違反があり AcceptWarnings が false なら false）。
	Exported bool `json:"exported"`
}

// Export は成果物一式を出力する。
//
// 違反があり AcceptWarnings が false の場合は出力せず、検証結果だけを返す。
func (g *Generator) Export(req ExportRequest) (*ExportResult, error) {
	if strings.TrimSpace(req.Destination) == "" {
		return nil, errors.New("出力先フォルダを指定してください。")
	}
	tmpl, err := LoadTemplate()
	if err != nil {
		return nil, err
	}
	project := g.cfg.Store.Project()

	reqChapters, err := g.chaptersFor(projectstore.DocKindRequirements, req.Requirements)
	if err != nil {
		return nil, err
	}
	if len(reqChapters) == 0 {
		return nil, errors.New("出力できる要件定義書がありません。先に成果物を生成してください。")
	}
	var designChapters []projectstore.DocumentChapter
	if req.IncludeBasicDesign {
		designChapters, err = g.chaptersFor(projectstore.DocKindBasicDesign, req.BasicDesign)
		if err != nil {
			return nil, err
		}
		if len(designChapters) == 0 {
			return nil, errors.New("出力できる基本設計書がありません。先に生成してください。")
		}
	}

	records, err := g.records()
	if err != nil {
		return nil, err
	}
	all := append(append([]projectstore.DocumentChapter{}, reqChapters...), designChapters...)
	verification := Verify(VerifyInput{Chapters: all, Records: records,
		AmbiguousWords: AmbiguousWordsFor(project)})

	out := &ExportResult{Verification: verification}
	if !verification.Passed() && !req.AcceptWarnings {
		return out, nil // 修正に戻る／警告付きで出力する の選択を呼び出し側へ返す
	}

	files, err := writeExport(req.Destination, exportContent{
		Template:       tmpl,
		Project:        project,
		Requirements:   reqChapters,
		BasicDesign:    designChapters,
		Records:        records,
		Verification:   verification,
		WithWarnings:   !verification.Passed(),
		RequirementsAt: req.Requirements,
		BasicDesignAt:  req.BasicDesign,
	})
	if err != nil {
		return nil, err
	}
	out.Files = files
	out.Exported = true
	return out, nil
}

// chaptersFor は指定版（0 なら最新ドラフト）の章を返す。
func (g *Generator) chaptersFor(kind string, version int) ([]projectstore.DocumentChapter, error) {
	if version > 0 {
		return g.cfg.Store.LoadVersion(kind, version)
	}
	return g.cfg.Store.LoadDraft(kind)
}

// exportContent は出力する内容一式。
type exportContent struct {
	Template       *Template
	Project        *projectstore.Project
	Requirements   []projectstore.DocumentChapter
	BasicDesign    []projectstore.DocumentChapter
	Records        Records
	Verification   VerifyResult
	WithWarnings   bool
	RequirementsAt int
	BasicDesignAt  int
}

// writeExport は一時フォルダへ組み立ててから出力先へ移す（原子性の確保）。
func writeExport(destination string, c exportContent) ([]string, error) {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("出力先を作成できません: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".export-*")
	if err != nil {
		return nil, fmt.Errorf("一時フォルダを作成できません: %w", err)
	}
	defer os.RemoveAll(tmp)

	var files []string
	write := func(rel, body string) error {
		path := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("出力先を作成できません（%s）: %w", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return fmt.Errorf("ファイルを書き出せません（%s）: %w", rel, err)
		}
		files = append(files, rel)
		return nil
	}

	reqDir, designDir := "10-requirements", "20-basic-design"
	for _, doc := range c.Template.Documents {
		switch doc.KindID {
		case projectstore.DocKindRequirements:
			reqDir = doc.Dir
		case projectstore.DocKindBasicDesign:
			designDir = doc.Dir
		}
	}

	// 成果物の章と共通文書（内部のフロントマターは落とし、箇条書きメタの本文だけを出す）。
	commonNames := map[string]bool{}
	for _, cm := range c.Template.Common {
		commonNames[cm.File] = true
	}
	for _, ch := range c.Requirements {
		dir := reqDir
		if commonNames[ch.FileName] {
			dir = commonDir
		}
		if err := write(dir+"/"+ch.FileName, ch.Body); err != nil {
			return nil, err
		}
	}
	for _, ch := range c.BasicDesign {
		if commonNames[ch.FileName] {
			continue // 共通文書は要件定義側で出力済み
		}
		if err := write(designDir+"/"+ch.FileName, ch.Body); err != nil {
			return nil, err
		}
	}

	// 導入ファイル・フィードバック様式・検証レポート。
	sort.Strings(files)
	if err := write(IntroFileName, buildIntro(c, files)); err != nil {
		return nil, err
	}
	if err := write(FeedbackTemplateFileName, buildFeedbackTemplate()); err != nil {
		return nil, err
	}
	if err := write(ExportReportFileName, buildExportReport(c)); err != nil {
		return nil, err
	}

	// 出力先へ移す（既存フォルダがあれば失敗させ、既存ファイルを壊さない）。
	if _, err := os.Stat(destination); err == nil {
		return nil, fmt.Errorf("出力先が既に存在します（%s）。別のフォルダを指定してください。", destination)
	}
	if err := os.Rename(tmp, destination); err != nil {
		return nil, fmt.Errorf("出力先へ書き込めません（%s）: %w", destination, err)
	}
	sort.Strings(files)
	return files, nil
}
