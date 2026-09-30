package projectstore

// 本ファイルは根拠参照の逆引き（逆方向の解決）と参照欠落の抽出を担う。
//
// 追跡連鎖は**片方向 1 箇所**にだけ保持する（二重管理で食い違わないように）。
// 「根拠 → それを引くレコード」の逆方向は保持せず、レコード側の evidence から毎回導出する
// （派生データ。キャッシュ先は派生インデックスであり、正はレコードのファイル）。

import (
	"sort"
	"strings"
)

// レコード種別（逆引き結果の区別に使う）。
const (
	RecordKindDecision    = "decision"
	RecordKindOpenIssue   = "open-issue"
	RecordKindRequirement = "requirement"
)

// EvidenceCitation は「あるレコードが、ある根拠を引いている」1 件。
type EvidenceCitation struct {
	// RecordID は引いている側のレコード（DEC-nnn / ISS-nnn / FR-*・NFR-*）。
	RecordID   string `json:"recordId"`
	RecordKind string `json:"recordKind"`
	// Title は一覧表示用の見出し（決定・未決事項は本文の 1 行目）。
	Title string `json:"title"`
	// Ref は根拠参照そのもの（S-nnnn#utt-nnnnn / QS-nnn#q-nn / IMP-nnn#Lm-Ln）。
	Ref string `json:"ref"`
}

// CitingRecords は指定の接頭辞で始まる根拠を引いているレコードを返す（逆方向の解決）。
//
// prefix には資料 ID（`IMP-001`）・セッション ID などを渡す。空文字は全件。
// 結果はレコード ID 順・同一レコード内は根拠の出現順とする。
//
// 逆方向の参照はどのファイルにも保持しない。この関数はレコード側の evidence を走査して
// 毎回導出する（資料側 import.yaml に承認レコードを書き戻さない）。
func (s *Store) CitingRecords(prefix string) ([]EvidenceCitation, error) {
	var out []EvidenceCitation

	decisions, err := s.ListDecisions()
	if err != nil {
		return nil, err
	}
	for _, d := range decisions {
		out = append(out, citations(d.ID, RecordKindDecision, firstLine(d.Body), d.Evidence, prefix)...)
	}
	issues, err := s.ListOpenIssues()
	if err != nil {
		return nil, err
	}
	for _, i := range issues {
		out = append(out, citations(i.ID, RecordKindOpenIssue, firstLine(i.Body), i.Evidence, prefix)...)
	}
	reqs, err := s.ListRequirements()
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		out = append(out, citations(r.ID, RecordKindRequirement, r.Title, r.Evidence, prefix)...)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].RecordID < out[j].RecordID })
	return out, nil
}

// citations は 1 レコードの根拠のうち prefix に合致するものを取り出す。
func citations(id, kind, title string, evidence []string, prefix string) []EvidenceCitation {
	var out []EvidenceCitation
	for _, ref := range evidence {
		if prefix != "" && !strings.HasPrefix(ref, prefix) {
			continue
		}
		out = append(out, EvidenceCitation{RecordID: id, RecordKind: kind, Title: title, Ref: ref})
	}
	return out
}

// MissingEvidenceRecord は根拠へたどれないレコード（参照欠落）。
type MissingEvidenceRecord struct {
	RecordID   string `json:"recordId"`
	RecordKind string `json:"recordKind"`
	Title      string `json:"title"`
}

// MissingEvidenceRecords は根拠参照を持たないレコードを一覧で返す（参照欠落の一覧）。
//
// 取り込み分析で根拠が除去された候補（missing_evidence）と同じ「参照欠落」であり、
// 承認後は本関数が同じ一覧の対象として拾う（候補と記録で判定を分けない）。
func (s *Store) MissingEvidenceRecords() ([]MissingEvidenceRecord, error) {
	var out []MissingEvidenceRecord

	decisions, err := s.ListDecisions()
	if err != nil {
		return nil, err
	}
	for _, d := range decisions {
		if len(d.Evidence) == 0 {
			out = append(out, MissingEvidenceRecord{RecordID: d.ID,
				RecordKind: RecordKindDecision, Title: firstLine(d.Body)})
		}
	}
	issues, err := s.ListOpenIssues()
	if err != nil {
		return nil, err
	}
	for _, i := range issues {
		if len(i.Evidence) == 0 {
			out = append(out, MissingEvidenceRecord{RecordID: i.ID,
				RecordKind: RecordKindOpenIssue, Title: firstLine(i.Body)})
		}
	}
	reqs, err := s.ListRequirements()
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		if !r.HasEvidence() {
			out = append(out, MissingEvidenceRecord{RecordID: r.ID,
				RecordKind: RecordKindRequirement, Title: r.Title})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RecordID < out[j].RecordID })
	return out, nil
}

// firstLine は本文の 1 行目（空行は飛ばす）。
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
