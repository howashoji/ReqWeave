package importer

// 本ファイルは `analysis.meta.yaml`（取り込み分析の一時状態）を担う。
//
// AI API 障害・中断時の未承認候補を保全し、原本・抽出テキストの参照を保ったまま
// 分析だけを後から再実行できるようにする。
// 全候補の承認・破棄が完了したらファイルごと削除する。
//
// `pending_candidates` の**構造の正本は対話エンジン**（抽出候補のスキーマ）であり、
// 本パッケージは中身を解釈しない（対話セッションに併置するメタデータと同じ扱い）。

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// AnalysisMetaFile は取り込み分析の一時状態ファイル名。
const AnalysisMetaFile = "analysis.meta.yaml"

// 分析の一時状態（analysis_state の値）。
const (
	// AnalysisAnalyzing は分析中（送信済み・候補が未確定）。
	AnalysisAnalyzing = "analyzing"
	// AnalysisAwaitingApproval は承認待ち（候補あり・未承認）。
	AnalysisAwaitingApproval = "awaiting-approval"
)

var validAnalysisStates = map[string]bool{
	AnalysisAnalyzing: true, AnalysisAwaitingApproval: true,
}

// AnalysisMeta は analysis.meta.yaml の内容。
type AnalysisMeta struct {
	AnalysisState string `yaml:"analysis_state"`
	// PendingCandidates は未承認の抽出候補（対話エンジンのスキーマ）。
	// 本パッケージは構造を解釈せず、そのまま保存・復元する。
	PendingCandidates any `yaml:"pending_candidates,omitempty"`
	// Baselines は候補が対象とする共有レコードの基準版（承認するときに、そのあいだに他のメンバーが
	// 同じレコードを変えていないかを確かめるため）。
	// 構造の正本はプロジェクトストア（RecordBaseline）であり、本パッケージは保存・復元だけを行う。
	Baselines map[string]projectstore.RecordBaseline `yaml:"baselines,omitempty"`
}

// Validate は状態の値集合を検証する（不正値をファイルへ残さない）。
func (m *AnalysisMeta) Validate() error {
	if !validAnalysisStates[m.AnalysisState] {
		return fmt.Errorf("取り込み分析の状態が不正です（%s / %s のいずれか）: %q",
			AnalysisAnalyzing, AnalysisAwaitingApproval, m.AnalysisState)
	}
	return nil
}

// AnalysisPath は analysis.meta.yaml の相対パス。
func AnalysisPath(id string) string { return Dir(id) + "/" + AnalysisMetaFile }

// SaveAnalysis は分析の一時状態を保存する（保存キュー経由）。
func (im *Importer) SaveAnalysis(id string, m AnalysisMeta) error {
	if _, err := im.Load(id); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(&m)
	if err != nil {
		return fmt.Errorf("取り込み分析の一時状態を組み立てられません（%s）: %w", id, err)
	}
	return im.store.WriteFile(AnalysisPath(id), data)
}

// LoadAnalysis は分析の一時状態を読む。保全されていない場合は nil を返す（エラーではない）。
func (im *Importer) LoadAnalysis(id string) (*AnalysisMeta, error) {
	data, err := os.ReadFile(im.abs(AnalysisPath(id)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("取り込み分析の一時状態を読み込めません（%s）: %w", id, err)
	}
	var m AnalysisMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("取り込み分析の一時状態を解釈できません（%s）: %w", id, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteAnalysis は分析の一時状態を破棄する（全候補の承認・破棄の完了時）。
//
// 原本・抽出テキスト・import.yaml は削除しない（取り込み後に変更しない）。
func (im *Importer) DeleteAnalysis(id string) error {
	return im.store.RemoveFile(AnalysisPath(id))
}
