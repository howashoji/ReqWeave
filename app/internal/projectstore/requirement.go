package projectstore

// 本ファイルは要件項目（requirements/<成果物ID>.md）を担う。
//
// 要件項目 ID は FR-<グループ>-nnn / NFR-<グループ>-nnn。
// 一度発番した ID は変更・再利用しない（成果物や決定事項からの参照が別の項目を指さないように）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// dirRequirements は要件項目の配置。
const dirRequirements = "requirements"

// 要件項目の種別（フロントマターの kind）。
const (
	RequirementFunctional    = "functional"
	RequirementNonFunctional = "non-functional"
)

// 要件項目の優先度（フロントマターの priority）。
const (
	PriorityMust   = "must"
	PriorityShould = "should"
	PriorityMay    = "may"
)

// 要件項目の状態（フロントマターの status。draft と agreed の間を行き来する）。
const (
	RequirementDraft  = "draft"
	RequirementAgreed = "agreed"
)

// requirementIDRe は FR-<グループ>-nnn / NFR-<グループ>-nnn。
// グループは英大文字・数字（章観点から AI が提案し担当者が承認する）。
// 桁あふれ（番号帯の採番で連番が 3 桁を超える場合）を受け入れるため 3 桁以上を許す。
// 4 桁以上のときに先頭のゼロは認めない（`FR-INV-0001` を `FR-INV-001` と別物にしない）。
var requirementIDRe = regexp.MustCompile(`^(FR|NFR)-([A-Z0-9]+)-([1-9]\d{3,}|\d{3})$`)

// requirementDigits は連番の桁数。
const requirementDigits = 3

// Requirement は要件項目のフロントマター。
type Requirement struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	// Chapter は章観点ID（文書生成の章割当・充足率の算出に使う）。
	Chapter  string `yaml:"chapter"`
	Kind     string `yaml:"kind"`
	Priority string `yaml:"priority"`
	Status   string `yaml:"status"`
	// Decisions は本項目を確定させた決定事項 ID。
	Decisions []string `yaml:"decisions,omitempty"`
	// AcceptanceCriteria は測定可能な受け入れ条件の列。
	// **受け入れ条件の件数を機械判定する唯一のデータソース**（充足率・整合性検証 V6）。
	// 本文にも人が読める形で書くが、件数判定に本文は使わない。
	AcceptanceCriteria []string `yaml:"acceptance_criteria,omitempty"`
	// Evidence は根拠への参照（決定事項経由の項目では任意）。
	Evidence []string `yaml:"evidence,omitempty"`
	// BlockedBy は本項目をブロックする未決事項 ID（未決事項から影響する要件を逆引きする元）。
	BlockedBy []string `yaml:"blocked_by,omitempty"`
	// RevertedReason は差し戻し（agreed→draft）の理由。
	RevertedReason string `yaml:"reverted_reason,omitempty"`

	// Body は要件文・受け入れ条件（Markdown）。
	Body string `yaml:"-"`
}

// RequirementFile は要件項目ファイルの相対パスを返す。
func RequirementFile(id string) string { return dirRequirements + "/" + id + ".md" }

// ParseRequirementID は要件項目 ID を種別プレフィックス・グループ・連番に分解する。
func ParseRequirementID(id string) (prefix, group string, number int, ok bool) {
	m := requirementIDRe.FindStringSubmatch(id)
	if m == nil {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return "", "", 0, false
	}
	return m[1], m[2], n, true
}

// RequirementIDPrefix は種別に対応する ID プレフィックスを返す。
func RequirementIDPrefix(kind string) (string, error) {
	switch kind {
	case RequirementFunctional:
		return "FR", nil
	case RequirementNonFunctional:
		return "NFR", nil
	default:
		return "", fmt.Errorf("要件項目の種別が不正です: %q", kind)
	}
}

// Validate は必須項目・値集合を検証する。
func (r *Requirement) Validate() error {
	prefix, _, _, ok := ParseRequirementID(r.ID)
	if !ok {
		return fmt.Errorf("要件項目の ID が不正です: %q（FR-<グループ>-nnn / NFR-<グループ>-nnn）", r.ID)
	}
	wantPrefix, err := RequirementIDPrefix(r.Kind)
	if err != nil {
		return err
	}
	if prefix != wantPrefix {
		return fmt.Errorf("要件項目の ID と種別が一致しません（%s / %s）", r.ID, r.Kind)
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("要件項目の要件名がありません（%s）", r.ID)
	}
	if strings.TrimSpace(r.Chapter) == "" {
		return fmt.Errorf("要件項目の章観点がありません（%s）", r.ID)
	}
	switch r.Priority {
	case PriorityMust, PriorityShould, PriorityMay:
	default:
		return fmt.Errorf("要件項目の優先度が不正です: %q", r.Priority)
	}
	switch r.Status {
	case RequirementDraft, RequirementAgreed:
	default:
		return fmt.Errorf("要件項目の状態が不正です: %q", r.Status)
	}
	if strings.TrimSpace(r.Body) == "" {
		return fmt.Errorf("要件項目の本文がありません（%s）", r.ID)
	}
	return nil
}

// HasAcceptanceCriteria は受け入れ条件を持つかを返す（false = 整合性検証 V6 の警告対象）。
func (r *Requirement) HasAcceptanceCriteria() bool { return len(r.AcceptanceCriteria) > 0 }

// HasEvidence は根拠へたどれるかを返す（false = 参照欠落の一覧に載る対象）。
func (r *Requirement) HasEvidence() bool { return len(r.Decisions) > 0 || len(r.Evidence) > 0 }

// CreateRequirement は要件項目を新規記録する。ID はグループ内連番で採番する。
func (s *Store) CreateRequirement(group string, r Requirement) (*Requirement, error) {
	prefix, err := RequirementIDPrefix(r.Kind)
	if err != nil {
		return nil, err
	}
	group = strings.ToUpper(strings.TrimSpace(group))
	if group == "" || !regexp.MustCompile(`^[A-Z0-9]+$`).MatchString(group) {
		return nil, fmt.Errorf("要件項目のグループは英大文字・数字で指定してください: %q", group)
	}
	if r.Status == "" {
		r.Status = RequirementDraft
	}
	if r.Priority == "" {
		r.Priority = PriorityMust
	}

	var created *Requirement
	err = s.WithShortLock(LockRecords, func() error {
		n, err := s.nextRequirementNumber(prefix, group)
		if err != nil {
			return err
		}
		r.ID = fmt.Sprintf("%s-%s-%0*d", prefix, group, requirementDigits, n)
		if err := r.Validate(); err != nil {
			return err
		}
		data, err := marshalDocument(&r, r.Body)
		if err != nil {
			return err
		}
		if err := s.WriteFile(RequirementFile(r.ID), data); err != nil {
			return err
		}
		created = &r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// nextRequirementNumber は次に使う連番を返す。
//
// 番号帯を使うプロジェクトでは自分の区間の未使用最小値。使用済みの判定は**全グループの和集合**で行う
// （区間は作業者ごとに重ならないため、和集合で見ても他の作業者の番号を奪わない。グループごとに
// 番号が飛ぶことは許容する）。単独利用では同じ種別・グループの最大値 + 1。
func (s *Store) nextRequirementNumber(prefix, group string) (int, error) {
	if !s.UsesIDRanges() {
		max, err := maxRequirementNumber(s.root, prefix, group)
		if err != nil {
			return 0, err
		}
		return max + 1, nil
	}
	used, err := s.usedNumbers(RangeRequirement)
	if err != nil {
		return 0, err
	}
	return s.nextInRanges(RangeRequirement, used)
}

// maxRequirementNumber は同じ種別・グループの既存 ID から最大連番を返す（0 = 未採番）。
func maxRequirementNumber(root, prefix, group string) (int, error) {
	ids, err := listRequirementIDs(root)
	if err != nil {
		return 0, err
	}
	max := 0
	for _, id := range ids {
		p, g, n, ok := ParseRequirementID(id)
		if ok && p == prefix && g == group && n > max {
			max = n
		}
	}
	return max, nil
}

// listRequirementIDs は requirements/ 配下の要件項目 ID を昇順で返す。
func listRequirementIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, dirRequirements))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("要件項目を走査できません: %w", err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if _, _, _, ok := ParseRequirementID(id); !ok {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// UpdateRequirement は要件項目を読み直して mutate を適用し、原子的に書き戻す。
//
// 手動編集・承認による反映・差し戻しの共通経路。
// 変更履歴への記録は上位が行う。
func (s *Store) updateRequirement(id string, mutate func(*Requirement) error) (*Requirement, error) {
	r, err := s.LoadRequirement(id)
	if err != nil {
		return nil, err
	}
	before := r.Status
	if err := mutate(r); err != nil {
		return nil, err
	}
	if r.ID != id {
		return nil, fmt.Errorf("要件項目の ID は変更できません（%s → %s）", id, r.ID)
	}
	// 差し戻し（agreed→draft）は理由の記録を必須にする（後から差し戻した経緯を辿れるように）。
	if before == RequirementAgreed && r.Status == RequirementDraft && strings.TrimSpace(r.RevertedReason) == "" {
		return nil, fmt.Errorf("差し戻しには理由が必要です（%s）", id)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	data, err := marshalDocument(r, r.Body)
	if err != nil {
		return nil, err
	}
	if err := s.WriteFile(RequirementFile(id), data); err != nil {
		return nil, err
	}
	return r, nil
}

// LoadRequirement は要件項目を読む。
func (s *Store) LoadRequirement(id string) (*Requirement, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(RequirementFile(id))))
	if err != nil {
		return nil, fmt.Errorf("要件項目を読み込めません（%s）: %w", id, err)
	}
	var r Requirement
	body, err := parseDocument(data, &r)
	if err != nil {
		return nil, fmt.Errorf("要件項目を解釈できません（%s）: %w", id, err)
	}
	r.Body = body
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRequirements は全要件項目を ID 順で返す（要件の一覧・充足率の算出に使う）。
func (s *Store) ListRequirements() ([]Requirement, error) {
	ids, err := listRequirementIDs(s.root)
	if err != nil {
		return nil, err
	}
	out := make([]Requirement, 0, len(ids))
	for _, id := range ids {
		r, err := s.LoadRequirement(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, nil
}
