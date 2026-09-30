package projectstore

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/masking"
	"gopkg.in/yaml.v3"
)

// フェーズ（project.yaml の phase）。
const (
	PhaseRequirements = "requirements"
	PhaseBasicDesign  = "basic-design"
)

// AmbiguousTerms は曖昧語リストの調整（project.yaml の ambiguous_terms）。
//
// 初期リストの正本は整合性検証 V5 の語の表（アプリ組み込み）。ここでは追加語と除外語だけを持つ。
type AmbiguousTerms struct {
	Added    []string `yaml:"added,omitempty"`
	Excluded []string `yaml:"excluded,omitempty"`
}

// UsageLimit は AI 利用量上限設定（project.yaml の usage_limit）。
// キーが無い = 上限未設定であり、警告・停止は発生しない。
type UsageLimit struct {
	TokensMax int      `yaml:"tokens_max"`
	WarnRatio *float64 `yaml:"warn_ratio,omitempty"`
}

// 同期先の種別（project.yaml の sync.kind。既定は共有フォルダ）。
const (
	SyncKindFolder      = "folder"
	SyncKindGitInternal = "git_internal"
	SyncKindGitExternal = "git_external"
)

// syncKindLabels は同期先の種別の表示名（生のコード値を画面へ出さない）。
var syncKindLabels = map[string]string{
	SyncKindFolder:      "共有フォルダ上のリポジトリ",
	SyncKindGitInternal: "社内 Git サーバ",
	SyncKindGitExternal: "外部 Git サーバ",
}

// SyncKindLabel は同期先の種別の表示名を返す。
func SyncKindLabel(kind string) string {
	if label, ok := syncKindLabels[kind]; ok {
		return label
	}
	return "同期先"
}

// SyncSetting は同期先の設定（project.yaml の sync）。
// プロジェクト単位で全メンバー共通。認証情報は含めない（端末ごと = settings.json の sync_credentials）。
type SyncSetting struct {
	Kind     string `yaml:"kind"`
	Location string `yaml:"location"`
}

// Validate は種別と所在の形式を検証する（所在の詳細な検証は同期モジュールが行う）。
func (s *SyncSetting) Validate() error {
	switch s.Kind {
	case SyncKindFolder, SyncKindGitInternal, SyncKindGitExternal:
	default:
		return fmt.Errorf("project.yaml の sync.kind が %q / %q / %q 以外です: %q", SyncKindFolder, SyncKindGitInternal, SyncKindGitExternal, s.Kind)
	}
	if strings.TrimSpace(s.Location) == "" {
		return fmt.Errorf("project.yaml の sync.location が空です")
	}
	if masking.StripURLCredentials(s.Location) != s.Location {
		// 所在に認証情報を含めない（project.yaml は同期で共有されるため、認証情報は端末側に置く）
		return fmt.Errorf("project.yaml の sync.location に認証情報が含まれています（所在のみを保持すること）")
	}
	return nil
}

// Project は project.yaml。
//
// 日時は UTC の ISO 8601 で保持する（タイムゾーン付きの形式にする。
// 表示時にローカルへ変換する）。
type Project struct {
	FormatVersion    string      `yaml:"format_version"`
	ProjectID        string      `yaml:"project_id"`
	TargetSystemName string      `yaml:"target_system_name"`
	Summary          string      `yaml:"summary,omitempty"` // 対象システムの概要（任意）
	Phase            string      `yaml:"phase"`
	DomainPresets    []string    `yaml:"domain_presets,omitempty"`
	UsageLimit       *UsageLimit `yaml:"usage_limit,omitempty"`
	// AmbiguousTerms は検証 V5（曖昧語）の語リストの調整。
	// nil = 調整なし（アプリ組み込みの初期リストのみで検証する）。
	AmbiguousTerms *AmbiguousTerms `yaml:"ambiguous_terms,omitempty"`
	// Sync は同期先の設定。nil = 同期先未設定（単独利用のローカルプロジェクト）。
	Sync         *SyncSetting `yaml:"sync,omitempty"`
	CreatedAt    time.Time    `yaml:"created_at"`
	MigratedFrom string       `yaml:"migrated_from,omitempty"`

	// unknown は自版が知らないフィールド（自版より新しいマイナー形式で追加されたもの）。
	// 書き戻しで削除しないために保持する（新しい版が足した項目を古い版が消さないように）。
	unknown map[string]*yaml.Node
}

// knownProjectFields は Project が構造体として扱うフィールド名。
var knownProjectFields = map[string]bool{
	"format_version": true, "project_id": true, "target_system_name": true,
	"summary": true, "phase": true, "domain_presets": true, "usage_limit": true,
	"ambiguous_terms": true, "sync": true,
	"created_at": true, "migrated_from": true,
}

// UnmarshalProject は project.yaml のバイト列を解釈する。未知フィールドは保持する（前方互換のため）。
func UnmarshalProject(data []byte) (*Project, error) {
	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("project.yaml を解釈できません: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("project.yaml を解釈できません: %w", err)
	}
	for k, v := range mappingPairs(&doc) {
		if knownProjectFields[k] {
			continue
		}
		if p.unknown == nil {
			p.unknown = map[string]*yaml.Node{}
		}
		p.unknown[k] = v
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Marshal は project.yaml のバイト列を組み立てる。保持していた未知フィールドを末尾に復元する。
func (p *Project) Marshal() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var doc yaml.Node
	known, err := yaml.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("project.yaml を組み立てられません: %w", err)
	}
	if err := yaml.Unmarshal(known, &doc); err != nil {
		return nil, fmt.Errorf("project.yaml を組み立てられません: %w", err)
	}
	if len(p.unknown) > 0 {
		mapping := doc.Content[0]
		for _, k := range sortedKeys(p.unknown) {
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
			mapping.Content = append(mapping.Content, key, p.unknown[k])
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("project.yaml を組み立てられません: %w", err)
	}
	return out, nil
}

// Validate は project.yaml の必須項目・値集合を検証する。
func (p *Project) Validate() error {
	if _, err := ParseFormatVersion(p.FormatVersion); err != nil {
		return fmt.Errorf("project.yaml の format_version が不正です: %w", err)
	}
	id, err := uuid.Parse(p.ProjectID)
	if err != nil {
		return fmt.Errorf("project.yaml の project_id が UUID ではありません: %q", p.ProjectID)
	}
	if id.Version() != 4 {
		return fmt.Errorf("project.yaml の project_id が UUIDv4 ではありません（version %d）", id.Version())
	}
	if p.TargetSystemName == "" {
		return fmt.Errorf("project.yaml の target_system_name が空です")
	}
	if p.Phase != PhaseRequirements && p.Phase != PhaseBasicDesign {
		return fmt.Errorf("project.yaml の phase が %q / %q 以外です: %q", PhaseRequirements, PhaseBasicDesign, p.Phase)
	}
	if p.CreatedAt.IsZero() {
		return fmt.Errorf("project.yaml の created_at が空です")
	}
	if p.Sync != nil {
		if err := p.Sync.Validate(); err != nil {
			return err
		}
	}
	if p.UsageLimit != nil && p.UsageLimit.TokensMax <= 0 {
		return fmt.Errorf("project.yaml の usage_limit.tokens_max が正の数ではありません: %d", p.UsageLimit.TokensMax)
	}
	// warn_ratio は「上限に対する割合」。0 以下・1 超は割合として成立しない。
	if p.UsageLimit != nil && p.UsageLimit.WarnRatio != nil &&
		(*p.UsageLimit.WarnRatio <= 0 || *p.UsageLimit.WarnRatio > 1) {
		return fmt.Errorf("project.yaml の usage_limit.warn_ratio が 0 超 1 以下ではありません: %v",
			*p.UsageLimit.WarnRatio)
	}
	return nil
}

// mappingPairs は YAML ドキュメントの最上位マッピングをキー→値ノードで返す。
// 最上位がマッピングでない場合は空を返す（値の検証は Validate が行う）。
func mappingPairs(doc *yaml.Node) map[string]*yaml.Node {
	pairs := map[string]*yaml.Node{}
	if doc == nil || len(doc.Content) == 0 {
		return pairs
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return pairs
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		pairs[m.Content[i].Value] = m.Content[i+1]
	}
	return pairs
}

// FormatCompatibility は自版との形式バージョンの関係を返す。
func (p *Project) FormatCompatibility() (Compatibility, error) {
	v, err := ParseFormatVersion(p.FormatVersion)
	if err != nil {
		return 0, err
	}
	return Classify(v), nil
}
