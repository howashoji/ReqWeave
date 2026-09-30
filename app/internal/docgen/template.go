package docgen

// 本ファイルは成果物ドキュメントのテンプレート定義を担う。
//
// 定義はアプリ組み込みの静的データ（Go embed）。章観点 ID は対話のメタモデルの章観点と同一。

import (
	_ "embed"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

// 章の生成方式。
const (
	// ChapterGenerated は AI 生成の対象。
	ChapterGenerated = "generated"
	// ChapterAssembled はレコードからの機械組立て（AI 生成しない）。
	ChapterAssembled = "assembled"
)

//go:embed template.yaml
var templateYAML []byte

// Template は成果物ドキュメントのテンプレート定義。
type Template struct {
	Documents []DocumentTemplate `yaml:"documents"`
	// Common は成果物種別によらず出力する共通文書（用語集・決定/未決リスト）。
	Common []ChapterTemplate `yaml:"common"`
}

// DocumentTemplate は 1 成果物種別ぶんの章構成。
type DocumentTemplate struct {
	// KindID は projectstore の成果物種別（requirements / basic-design）。
	KindID   string            `yaml:"kind_id"`
	Name     string            `yaml:"name"`
	Dir      string            `yaml:"dir"`
	Chapters []ChapterTemplate `yaml:"chapters"`
}

// ChapterTemplate は 1 章（＝ 1 ファイル）の仕様。
type ChapterTemplate struct {
	File    string `yaml:"file"`
	Chapter string `yaml:"chapter"`
	Title   string `yaml:"title"`
	Kind    string `yaml:"kind"`
	// Dir は共通文書のみ（成果物種別のディレクトリではなく 00-project へ出す）。
	Dir string `yaml:"dir,omitempty"`
}

// IsGenerated は AI 生成の対象かを返す。
func (c ChapterTemplate) IsGenerated() bool { return c.Kind == ChapterGenerated }

var (
	tmplOnce sync.Once
	tmplVal  *Template
	tmplErr  error
)

// LoadTemplate は組み込みのテンプレート定義を返す（初回のみ解釈する）。
func LoadTemplate() (*Template, error) {
	tmplOnce.Do(func() {
		var t Template
		if err := yaml.Unmarshal(templateYAML, &t); err != nil {
			tmplErr = fmt.Errorf("テンプレート定義を解釈できません: %w", err)
			return
		}
		if err := t.validate(); err != nil {
			tmplErr = err
			return
		}
		tmplVal = &t
	})
	return tmplVal, tmplErr
}

// Document は成果物種別のテンプレートを返す。
func (t *Template) Document(kindID string) (*DocumentTemplate, error) {
	for i := range t.Documents {
		if t.Documents[i].KindID == kindID {
			return &t.Documents[i], nil
		}
	}
	return nil, fmt.Errorf("テンプレート定義に無い成果物種別です: %q", kindID)
}

// Chapter は章観点 ID から章テンプレートを引く。
func (d *DocumentTemplate) Chapter(chapter string) (ChapterTemplate, bool) {
	for _, c := range d.Chapters {
		if c.Chapter == chapter {
			return c, true
		}
	}
	return ChapterTemplate{}, false
}

// GeneratedChapters は AI 生成の対象章を定義順で返す。
func (d *DocumentTemplate) GeneratedChapters() []ChapterTemplate {
	var out []ChapterTemplate
	for _, c := range d.Chapters {
		if c.IsGenerated() {
			out = append(out, c)
		}
	}
	return out
}

func (t *Template) validate() error {
	if len(t.Documents) == 0 {
		return fmt.Errorf("テンプレート定義に成果物種別がありません")
	}
	for _, d := range t.Documents {
		if d.KindID == "" || d.Dir == "" {
			return fmt.Errorf("成果物種別の ID・出力先がありません: %+v", d)
		}
		seenFile := map[string]bool{}
		seenChapter := map[string]bool{}
		for _, c := range d.Chapters {
			if err := c.validate(d.KindID); err != nil {
				return err
			}
			if seenFile[c.File] {
				return fmt.Errorf("章ファイル名が重複しています: %s/%s", d.KindID, c.File)
			}
			seenFile[c.File] = true
			if seenChapter[c.Chapter] {
				return fmt.Errorf("章観点 ID が重複しています: %s/%s", d.KindID, c.Chapter)
			}
			seenChapter[c.Chapter] = true
		}
	}
	for _, c := range t.Common {
		if err := c.validate("common"); err != nil {
			return err
		}
		if c.Dir == "" {
			return fmt.Errorf("共通文書の出力先がありません: %s", c.File)
		}
		if c.IsGenerated() {
			return fmt.Errorf("共通文書は機械組立てのみです: %s", c.File)
		}
	}
	return nil
}

func (c ChapterTemplate) validate(owner string) error {
	if c.File == "" || c.Chapter == "" || c.Title == "" {
		return fmt.Errorf("章テンプレートの項目が欠けています（%s）: %+v", owner, c)
	}
	switch c.Kind {
	case ChapterGenerated, ChapterAssembled:
	default:
		return fmt.Errorf("章の生成方式が不正です（%s/%s）: %q", owner, c.File, c.Kind)
	}
	return nil
}
