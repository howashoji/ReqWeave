package dialogue

// 本ファイルはドメインプリセット観点（業務領域ごとに追加で尋ねる質問観点）を担う。
//
// プリセットはメタモデル定義と同じアプリ組み込みの静的データ（Go embed）であり、
// 外部から動的に取得しない（実行時に外部の内容を取り込まないため）。
// 質問の固定文言は持たず、質問文は観点から AI が生成する（通常の質問生成と同じ経路）。
//
// プリセット観点は充足率の分母に加えない。未選択・未消化でも
// 確定をブロックしない（プリセットを必須経路にしない）。

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed presets.yaml
var presetsYAML []byte

// PresetTopicPrefix は論点キーの接頭辞（`preset/<領域ID>/<観点ID>`）。
const PresetTopicPrefix = "preset"

// DomainPresets はプリセット定義の全体。
type DomainPresets struct {
	Domains []DomainPreset `yaml:"domains"`
}

// DomainPreset は 1 業務領域（project.yaml の domain_presets の値集合）。
type DomainPreset struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	// Summary は業務領域の選択画面に出す 1 行の説明。
	Summary      string        `yaml:"summary"`
	Perspectives []Perspective `yaml:"perspectives"`
}

// Perspective は 1 観点。
type Perspective struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	// Chapter は関連章観点ID（metamodel.yaml の章観点）。この章の走査時に論点として連結する。
	Chapter string `yaml:"chapter"`
	// Topics は典型論点の要旨（プロンプトへ載せる。質問文そのものではない）。
	Topics string `yaml:"topics"`
}

// TopicKey は論点キー（preset/<領域ID>/<観点ID>）を返す。
func (d DomainPreset) TopicKey(perspectiveID string) string {
	return PresetTopicPrefix + "/" + d.ID + "/" + perspectiveID
}

var (
	presetOnce sync.Once
	presetVal  *DomainPresets
	presetErr  error
)

// LoadDomainPresets は組み込みのプリセット定義を返す（初回のみ解釈する）。
func LoadDomainPresets() (*DomainPresets, error) {
	presetOnce.Do(func() {
		var p DomainPresets
		if err := yaml.Unmarshal(presetsYAML, &p); err != nil {
			presetErr = fmt.Errorf("プリセット観点の定義を読み込めません: %w", err)
			return
		}
		if err := p.validate(); err != nil {
			presetErr = err
			return
		}
		presetVal = &p
	})
	return presetVal, presetErr
}

// validate は定義データの整合を検査する（同梱データの取り違えを起動時に落とす）。
func (p *DomainPresets) validate() error {
	seenDomain := map[string]bool{}
	for _, d := range p.Domains {
		if d.ID == "" || d.Name == "" {
			return fmt.Errorf("業務領域の ID・名称がありません: %+v", d)
		}
		if seenDomain[d.ID] {
			return fmt.Errorf("業務領域 ID が重複しています: %s", d.ID)
		}
		seenDomain[d.ID] = true
		if len(d.Perspectives) == 0 {
			return fmt.Errorf("業務領域 %s に観点がありません", d.ID)
		}
		seen := map[string]bool{}
		for _, v := range d.Perspectives {
			if v.ID == "" || v.Name == "" || v.Chapter == "" {
				return fmt.Errorf("観点の ID・名称・関連章観点がありません（%s）: %+v", d.ID, v)
			}
			if seen[v.ID] {
				return fmt.Errorf("観点 ID が重複しています（%s）: %s", d.ID, v.ID)
			}
			seen[v.ID] = true
		}
	}
	return nil
}

// Domain は業務領域 ID から定義を引く。
func (p *DomainPresets) Domain(id string) (DomainPreset, bool) {
	for _, d := range p.Domains {
		if d.ID == id {
			return d, true
		}
	}
	return DomainPreset{}, false
}

// SelectedPerspective は選択中の業務領域に属する観点 1 件（論点キー付き）。
type SelectedPerspective struct {
	DomainID   string
	DomainName string
	Perspective
	TopicKey string
}

// SelectedPerspectives は選択された業務領域の観点を定義順に返す。
//
// 未選択・未知の領域 ID は黙って飛ばす（プリセットは必須経路ではないため、
// 定義の版差で対話を止めない）。
func SelectedPerspectives(selected []string) ([]SelectedPerspective, error) {
	presets, err := LoadDomainPresets()
	if err != nil {
		return nil, err
	}
	var out []SelectedPerspective
	for _, id := range selected {
		d, ok := presets.Domain(id)
		if !ok {
			continue
		}
		for _, v := range d.Perspectives {
			out = append(out, SelectedPerspective{DomainID: d.ID, DomainName: d.Name,
				Perspective: v, TopicKey: d.TopicKey(v.ID)})
		}
	}
	return out, nil
}

// PerspectiveLines はシステムプロンプトへ注入する追加の質問観点を返す。
func PerspectiveLines(perspectives []SelectedPerspective) []string {
	out := make([]string, 0, len(perspectives))
	for _, v := range perspectives {
		out = append(out, fmt.Sprintf("%s / %s（論点キー: %s）: %s",
			v.DomainName, v.Name, v.TopicKey, v.Topics))
	}
	return out
}

// IsPresetTopicKey は論点キーがプリセット観点のものかを返す。
func IsPresetTopicKey(topicKey string) bool {
	return strings.HasPrefix(topicKey, PresetTopicPrefix+"/")
}
