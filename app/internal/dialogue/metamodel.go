// Package dialogue は対話エンジン。対話ループ・質問生成・抽出・完成度算出を担う。
package dialogue

// 本ファイルはメタモデル定義（章観点と必須項目）を担う。

import (
	_ "embed"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

// フェーズ（project.yaml の phase）。
const (
	PhaseRequirements = "requirements"
	PhaseBasicDesign  = "basic-design"
)

// 判定条件の種別（意味は metamodel.yaml のコメントに書いてある）。
const (
	CondDecision            = "decision"
	CondRequirement         = "requirement"
	CondRequirementCriteria = "requirement_criteria"
	CondTerm                = "term"
)

//go:embed metamodel.yaml
var metamodelYAML []byte

// MetaModel は章観点と必須項目の定義。
type MetaModel struct {
	Phases []Phase `yaml:"phases"`
}

// Phase は 1 フェーズぶんの章観点定義。
type Phase struct {
	ID       string    `yaml:"id"`
	Chapters []Chapter `yaml:"chapters"`
}

// Chapter は章観点。並び順が、質問生成で未充足の論点を探す走査順になる。
type Chapter struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	// IDGroup は新規の要件項目に使う既定の ID グループ（FR-<グループ>-nnn の <グループ>）。
	// AI の提案が無い・形式が不正なときに使う。
	IDGroup string `yaml:"id_group"`
	Items   []Item `yaml:"items"`
}

// Item は必須項目。
type Item struct {
	ID        string    `yaml:"id"`
	Name      string    `yaml:"name"`
	Condition Condition `yaml:"condition"`
}

// TopicKey は論点キー（章観点ID/必須項目ID）を返す。
func (c Chapter) TopicKey(itemID string) string { return c.ID + "/" + itemID }

// Condition は必須項目の充足判定条件。
type Condition struct {
	Kind string `yaml:"kind"`
	Min  int    `yaml:"min"`
	// RequirementKind は requirement / requirement_criteria で対象を絞る種別
	//（projectstore.RequirementFunctional / RequirementNonFunctional）。
	RequirementKind string `yaml:"requirement_kind,omitempty"`
}

var (
	metaOnce sync.Once
	metaVal  *MetaModel
	metaErr  error
)

// LoadMetaModel は組み込みのメタモデル定義を返す（初回のみ解釈する）。
func LoadMetaModel() (*MetaModel, error) {
	metaOnce.Do(func() {
		var m MetaModel
		if err := yaml.Unmarshal(metamodelYAML, &m); err != nil {
			metaErr = fmt.Errorf("メタモデル定義を解釈できません: %w", err)
			return
		}
		if err := m.validate(); err != nil {
			metaErr = err
			return
		}
		metaVal = &m
	})
	return metaVal, metaErr
}

// Phase は該当フェーズの章観点定義を返す。
func (m *MetaModel) Phase(id string) (*Phase, error) {
	for i := range m.Phases {
		if m.Phases[i].ID == id {
			return &m.Phases[i], nil
		}
	}
	return nil, fmt.Errorf("メタモデル定義に無いフェーズです: %q", id)
}

// validate は定義の整合（ID の重複・条件種別・件数）を確認する。
func (m *MetaModel) validate() error {
	if len(m.Phases) == 0 {
		return fmt.Errorf("メタモデル定義にフェーズがありません")
	}
	for _, p := range m.Phases {
		if len(p.Chapters) == 0 {
			return fmt.Errorf("フェーズ %q に章観点がありません", p.ID)
		}
		seenChapter := map[string]bool{}
		for _, c := range p.Chapters {
			if c.ID == "" || c.Name == "" {
				return fmt.Errorf("章観点の ID・名称がありません（フェーズ %q）", p.ID)
			}
			if seenChapter[c.ID] {
				return fmt.Errorf("章観点 ID が重複しています: %q", c.ID)
			}
			seenChapter[c.ID] = true
			if len(c.Items) == 0 {
				return fmt.Errorf("章観点 %q に必須項目がありません", c.ID)
			}
			seenItem := map[string]bool{}
			for _, it := range c.Items {
				if it.ID == "" || it.Name == "" {
					return fmt.Errorf("必須項目の ID・名称がありません（章観点 %q）", c.ID)
				}
				if seenItem[it.ID] {
					return fmt.Errorf("必須項目 ID が重複しています: %q", c.TopicKey(it.ID))
				}
				seenItem[it.ID] = true
				if err := it.Condition.validate(c.TopicKey(it.ID)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c Condition) validate(topicKey string) error {
	switch c.Kind {
	case CondDecision, CondTerm:
	case CondRequirement, CondRequirementCriteria:
		if c.RequirementKind == "" {
			return fmt.Errorf("要件項目の判定には種別が必要です（%s）", topicKey)
		}
	default:
		return fmt.Errorf("判定条件の種別が不正です（%s）: %q", topicKey, c.Kind)
	}
	if c.Min < 1 {
		return fmt.Errorf("判定条件の件数は 1 以上にしてください（%s）: %d", topicKey, c.Min)
	}
	return nil
}
