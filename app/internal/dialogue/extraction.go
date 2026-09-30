package dialogue

// 本ファイルは抽出出力（回答分析の結果）の型と検証を担う。
//
// スキーマ検証に失敗した場合は 1 回だけ再要求し、なお失敗する場合は応答原文を提示して
// 手動起票へ縮退する（AI の出力が壊れていても記録を続けられるように）。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// 要件項目の反映操作（出力の operation）。
const (
	OperationCreate = "create"
	OperationUpdate = "update"
)

// Extraction は回答分析の出力（ExtractionSchemaJSON に対応する）。
type Extraction struct {
	Decisions          []DecisionCandidate    `json:"decisions"`
	OpenIssues         []OpenIssueCandidate   `json:"open_issues"`
	RequirementUpdates []RequirementCandidate `json:"requirement_updates"`
	TermCandidates     []TermCandidate        `json:"term_candidates"`
	Contradictions     []Contradiction        `json:"contradictions"`

	// PerspectiveCandidates は取り込み分析でのみ生成される観点候補。
	// 対話抽出・回答取込分析では常に空にする（DropPerspectiveCandidates）。
	PerspectiveCandidates []PerspectiveCandidate `json:"perspective_candidates,omitempty"`
}

// DecisionCandidate は決定事項候補。
type DecisionCandidate struct {
	TopicKey             string   `json:"topic_key"`
	Body                 string   `json:"body"`
	Rationale            string   `json:"rationale"`
	EvidenceRefs         []string `json:"evidence_refs"`
	SupersedesDecisionID string   `json:"supersedes_decision_id,omitempty"`
	DuplicateOf          string   `json:"duplicate_of,omitempty"`
	// RelatedIDs はフィードバック論点化での紐づけ候補。
	RelatedIDs []string `json:"related_ids,omitempty"`
	// MissingEvidence は実在しない参照を除いた結果、根拠が空になったか（参照欠落として一覧に出す）。
	MissingEvidence bool `json:"missing_evidence,omitempty"`
}

// OpenIssueCandidate は未決事項候補。
type OpenIssueCandidate struct {
	Topic                string   `json:"topic"`
	Owner                string   `json:"owner,omitempty"`
	Due                  string   `json:"due,omitempty"`
	NeedsStakeholder     bool     `json:"needs_stakeholder,omitempty"`
	BlocksRequirementIDs []string `json:"blocks_requirement_ids,omitempty"`
	EvidenceRefs         []string `json:"evidence_refs"`
	DuplicateOf          string   `json:"duplicate_of,omitempty"`
	// RelatedIDs はフィードバック論点化での紐づけ候補。
	RelatedIDs      []string `json:"related_ids,omitempty"`
	MissingEvidence bool     `json:"missing_evidence,omitempty"`
}

// RequirementCandidate は要件項目への反映案。
type RequirementCandidate struct {
	Operation          string   `json:"operation"`
	TargetID           string   `json:"target_id,omitempty"`
	Chapter            string   `json:"chapter"`
	Title              string   `json:"title"`
	BodyAfter          string   `json:"body_after"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs"`
	DuplicateOf        string   `json:"duplicate_of,omitempty"`
	// IDGroup は新規作成時の ID グループ（FR-<グループ>-nnn の <グループ>）。
	//
	// AI が提案し、アプリが形式を検証して採る。不正・未提案のときは
	// 章観点の既定（metamodel.yaml の id_group）へ倒すため、**利用者には入力させない**。
	IDGroup string `json:"id_group,omitempty"`
	// Kind は要件項目の種別（functional / non-functional）。AI の出力ではなく
	// 章観点から決める。
	Kind string `json:"kind,omitempty"`
	// RelatedIDs はフィードバック論点化での紐づけ候補。
	RelatedIDs      []string `json:"related_ids,omitempty"`
	MissingEvidence bool     `json:"missing_evidence,omitempty"`
}

// TermCandidate は用語の追加候補。
type TermCandidate struct {
	Term         string   `json:"term"`
	English      string   `json:"english"`
	Definition   string   `json:"definition"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// PerspectiveCandidate はプロジェクト固有の質問観点の候補。
//
// 取り込み分析（資料・フィードバック）でのみ生成させる。対話抽出では
// 出力させない（スキーマに含めない = SchemaForMode）。承認時の登録先は perspectives.yaml。
type PerspectiveCandidate struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// EvidenceRefs は根拠（取り込み分析では IMP-nnn#Lm-Ln）。
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	// DuplicateOf は重複する既存観点（PRS-nnn またはプリセット観点キー）。
	DuplicateOf     string `json:"duplicate_of,omitempty"`
	MissingEvidence bool   `json:"missing_evidence,omitempty"`
}

// Contradiction は既存決定との矛盾。
type Contradiction struct {
	WithDecisionID string   `json:"with_decision_id"`
	Description    string   `json:"description"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
}

// IsEmpty は候補が 1 件も無いかを返す。
func (e Extraction) IsEmpty() bool {
	return len(e.Decisions) == 0 && len(e.OpenIssues) == 0 && len(e.RequirementUpdates) == 0 &&
		len(e.TermCandidates) == 0 && len(e.Contradictions) == 0 && len(e.PerspectiveCandidates) == 0
}

// DropPerspectiveCandidates は観点候補を捨てる（取り込み分析以外の経路で使う）。
//
// スキーマに含めていないため通常は出力されないが、モデルが勝手に付けた場合に
// 対話抽出・回答取込の承認画面へ流さない（出力契約の一方向の担保）。
func (e *Extraction) DropPerspectiveCandidates() { e.PerspectiveCandidates = nil }

// evidenceRefRe は根拠参照の形式（発話 / 回答 / 取り込み資料）。
var evidenceRefRe = regexp.MustCompile(`^(S-\d{4}#utt-\d{5}|QS-\d{3}#q-\d{2}|IMP-\d{3}#L\d+-L\d+)$`)

// ParseExtraction は応答本文から抽出結果を解釈する（前後の説明文・コードフェンスは取り除く）。
func ParseExtraction(body string) (*Extraction, error) {
	jsonText, err := extractJSONObject(body)
	if err != nil {
		return nil, err
	}
	var out Extraction
	dec := json.NewDecoder(strings.NewReader(jsonText))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		// 未知フィールドの混入だけで捨てず、寛容な解釈を 1 度試す（形式の本質は配列構造）。
		if err2 := json.Unmarshal([]byte(jsonText), &out); err2 != nil {
			return nil, fmt.Errorf("抽出結果の形式が不正です: %w", err2)
		}
	}
	if err := out.validate(); err != nil {
		return nil, err
	}
	return &out, nil
}

// extractJSONObject は本文から最初の JSON オブジェクトを取り出す。
func extractJSONObject(body string) (string, error) {
	text := strings.TrimSpace(body)
	if fence := strings.Index(text, "```"); fence >= 0 {
		rest := text[fence+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			text = strings.TrimSpace(rest[:end])
		}
	}
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return "", fmt.Errorf("抽出結果に JSON が含まれていません")
	}
	return text[start : end+1], nil
}

// validate は必須項目・値集合を検証する。
func (e *Extraction) validate() error {
	for i, d := range e.Decisions {
		if strings.TrimSpace(d.TopicKey) == "" {
			return fmt.Errorf("決定事項候補 %d に論点キーがありません", i+1)
		}
		if strings.TrimSpace(d.Body) == "" {
			return fmt.Errorf("決定事項候補 %d に本文がありません", i+1)
		}
	}
	for i, o := range e.OpenIssues {
		if strings.TrimSpace(o.Topic) == "" {
			return fmt.Errorf("未決事項候補 %d に論点がありません", i+1)
		}
	}
	for i, r := range e.RequirementUpdates {
		switch r.Operation {
		case OperationCreate:
			if r.TargetID != "" {
				return fmt.Errorf("要件項目の反映案 %d: create に対象 ID は指定できません", i+1)
			}
		case OperationUpdate:
			if r.TargetID == "" {
				return fmt.Errorf("要件項目の反映案 %d: update には対象 ID が必要です", i+1)
			}
		default:
			return fmt.Errorf("要件項目の反映案 %d の操作が不正です: %q", i+1, r.Operation)
		}
		if strings.TrimSpace(r.Chapter) == "" {
			return fmt.Errorf("要件項目の反映案 %d に章観点がありません", i+1)
		}
		if strings.TrimSpace(r.BodyAfter) == "" {
			return fmt.Errorf("要件項目の反映案 %d に反映後の本文がありません", i+1)
		}
	}
	for i, t := range e.TermCandidates {
		if strings.TrimSpace(t.Term) == "" || strings.TrimSpace(t.Definition) == "" {
			return fmt.Errorf("用語候補 %d に表記・定義がありません", i+1)
		}
	}
	for i, p := range e.PerspectiveCandidates {
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Summary) == "" {
			return fmt.Errorf("観点候補 %d に名称・要旨がありません", i+1)
		}
	}
	return nil
}

// VerifyEvidence は根拠参照の実在を検証し、存在しない ID を除去する（AI が推測した ID を根拠として残さない）。
//
// 除去の結果、根拠が空になった候補には MissingEvidence を立てる
// （参照欠落として一覧表示する）。
func (e *Extraction) VerifyEvidence(existing map[string]bool) {
	e.VerifyEvidenceWith(func(ref string) bool { return existing == nil || existing[ref] })
}

// VerifyEvidenceWith は実在判定を述語で与える版。
//
// 取り込み分析の根拠（IMP-nnn#Lm-Ln）は行範囲を持つため列挙できない。
// 資料 ID と行範囲の妥当性を述語で判定する（materialanalysis.go）。
func (e *Extraction) VerifyEvidenceWith(valid func(ref string) bool) {
	existing := valid
	for i := range e.Decisions {
		e.Decisions[i].EvidenceRefs, e.Decisions[i].MissingEvidence = filterRefs(e.Decisions[i].EvidenceRefs, existing)
	}
	for i := range e.OpenIssues {
		e.OpenIssues[i].EvidenceRefs, e.OpenIssues[i].MissingEvidence = filterRefs(e.OpenIssues[i].EvidenceRefs, existing)
	}
	for i := range e.RequirementUpdates {
		e.RequirementUpdates[i].EvidenceRefs, e.RequirementUpdates[i].MissingEvidence =
			filterRefs(e.RequirementUpdates[i].EvidenceRefs, existing)
	}
	for i := range e.TermCandidates {
		e.TermCandidates[i].EvidenceRefs, _ = filterRefs(e.TermCandidates[i].EvidenceRefs, existing)
	}
	for i := range e.Contradictions {
		e.Contradictions[i].EvidenceRefs, _ = filterRefs(e.Contradictions[i].EvidenceRefs, existing)
	}
	for i := range e.PerspectiveCandidates {
		e.PerspectiveCandidates[i].EvidenceRefs, e.PerspectiveCandidates[i].MissingEvidence =
			filterRefs(e.PerspectiveCandidates[i].EvidenceRefs, existing)
	}
}

// FillMissingEvidence は根拠が空になった候補へ、抽出対象の発話参照を付与する
// （根拠発話の自動付与。どの候補にも根拠の発話が付いているようにする）。
//
// 対話抽出の入力は直前の 1 往復だけであり、候補はすべてその往復から作られる。
// AI が根拠の発話 ID を書き損じても、根拠そのものは特定できる。空のまま承認へ回すと
// 決定事項・未決事項は根拠が必須のため記録できず、利用者には直す手立てが無い。
// refs が空（抽出対象を特定できない）のときは何もしない（参照欠落のまま示す）。
func (e *Extraction) FillMissingEvidence(refs []string) {
	if len(refs) == 0 {
		return
	}
	fill := func(current []string, missing bool) ([]string, bool) {
		if !missing && len(current) > 0 {
			return current, false
		}
		return append([]string(nil), refs...), false
	}
	for i := range e.Decisions {
		e.Decisions[i].EvidenceRefs, e.Decisions[i].MissingEvidence =
			fill(e.Decisions[i].EvidenceRefs, e.Decisions[i].MissingEvidence)
	}
	for i := range e.OpenIssues {
		e.OpenIssues[i].EvidenceRefs, e.OpenIssues[i].MissingEvidence =
			fill(e.OpenIssues[i].EvidenceRefs, e.OpenIssues[i].MissingEvidence)
	}
	for i := range e.RequirementUpdates {
		e.RequirementUpdates[i].EvidenceRefs, e.RequirementUpdates[i].MissingEvidence =
			fill(e.RequirementUpdates[i].EvidenceRefs, e.RequirementUpdates[i].MissingEvidence)
	}
}

// filterRefs は形式が正しく実在する参照だけを残す。missing は残りが 0 件かどうか。
func filterRefs(refs []string, valid func(string) bool) (kept []string, missing bool) {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !evidenceRefRe.MatchString(ref) {
			continue
		}
		if valid != nil && !valid(ref) {
			continue
		}
		kept = append(kept, ref)
	}
	return kept, len(kept) == 0
}

// decodeExtraction は併置メタデータから読み直した汎用構造を Extraction へ戻す。
//
// projectstore は pending_candidates の構造を解釈しないため（スキーマの正本は本パッケージ）、
// ここで JSON を経由して型へ戻す。
func decodeExtraction(v any) (*Extraction, error) {
	data, err := json.Marshal(normalizeKeys(v))
	if err != nil {
		return nil, fmt.Errorf("未承認候補を解釈できません: %w", err)
	}
	var out Extraction
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("未承認候補を解釈できません: %w", err)
	}
	return &out, nil
}

// normalizeKeys は YAML 由来の map[any]any を map[string]any へ揃える。
func normalizeKeys(v any) any {
	switch m := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = normalizeKeys(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = normalizeKeys(val)
		}
		return out
	case []any:
		out := make([]any, len(m))
		for i, val := range m {
			out[i] = normalizeKeys(val)
		}
		return out
	default:
		return v
	}
}

// encodeExtraction は併置メタデータへ保存できる汎用構造（JSON タグ準拠の map）へ変換する。
//
// 保存形式を JSON タグ（出力スキーマのフィールド名）へ揃え、読み直し（decodeExtraction）と対称にする。
func encodeExtraction(v any) any {
	ex, ok := v.(*Extraction)
	if !ok {
		return v
	}
	data, err := json.Marshal(ex)
	if err != nil {
		return v
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return v
	}
	return m
}
