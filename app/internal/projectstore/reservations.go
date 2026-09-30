package projectstore

// 本ファイルは予約と作業状況（reservations.yaml）。
//
// 以前の版の文書ロック（`locks/` による端末間の排他）を置き換えたもの（作業コピーを同期する方式への移行に伴う）。
// 予約は `reservations.yaml`（同期対象）に持ち、**同期先へ反映して初めて他メンバーに効く**。
// 作業コピーは 1 名の利用者が使うため端末間のロックは成立せず、排他は「予約」として扱う。
//
// - 予約された対象への着手は**禁止しない**（警告と確認操作を経て着手できる）。
//   確認操作の要否・権限の判定は呼び出し側（バインディング層の前段共通実装）が担い、
//   本ファイルは予約の読み書きと不変条件だけを守る。
// - **ハートビート・残留判定を持たない**。予約は同期のたびにしか更新されないため、経過時間で
//   自動失効させると他メンバーの作業中の予約を誤って落とす。
// - 変更履歴への記録（`reservation-set` / `reservation-released`）は呼び出し側が行う
//   （members.yaml / roster.yaml と同じ分担）。
// - 同一端末で同じ作業コピーを複数プロセスが開いている場合の直列化は `locks/records`。
// - 同一対象のエントリを双方が変更した場合はエントリ単位の三面マージで解決する（同期モジュール側）。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 予約の進め方（reservations.yaml の mode）。
const (
	// ReservationExclusive は排他（予約して 1 名で進める）。
	ReservationExclusive = "exclusive"
	// ReservationConcurrent は並行（複数メンバーが同時に進める）。
	ReservationConcurrent = "concurrent"
)

// 予約対象 ID（予約できる対象の一覧。個別レコードは接頭辞 + 成果物 ID）。
const (
	ReservationDocumentsRequirements = "documents-requirements"
	ReservationDocumentsBasicDesign  = "documents-basic-design"
	ReservationMembers               = "members"
	ReservationRoster                = "roster"
	ReservationTerms                 = "terms"
	ReservationPerspectives          = "perspectives"

	ReservationPrefixRequirement = "requirement:"
	ReservationPrefixDecision    = "decision:"
	ReservationPrefixOpenIssue   = "open-issue:"
)

// fixedReservationTargets は接頭辞を持たない予約対象 ID の集合。
var fixedReservationTargets = map[string]bool{
	ReservationDocumentsRequirements: true,
	ReservationDocumentsBasicDesign:  true,
	ReservationMembers:               true,
	ReservationRoster:                true,
	ReservationTerms:                 true,
	ReservationPerspectives:          true,
}

// ValidateReservationTarget は予約対象 ID が予約できる対象の一覧に含まれるかを検証する。
//
// 個別レコードの予約は成果物 ID の形式まで検証する（存在の確認は行わない。予約は同期先の
// 状態を含む共有情報のため、自分の作業コピーに無いレコードを予約する余地を残す）。
func ValidateReservationTarget(target string) error {
	if fixedReservationTargets[target] {
		return nil
	}
	switch {
	case strings.HasPrefix(target, ReservationPrefixRequirement):
		id := strings.TrimPrefix(target, ReservationPrefixRequirement)
		if !requirementIDRe.MatchString(id) {
			return fmt.Errorf("予約する要件項目の ID が不正です: %q", id)
		}
		return nil
	case strings.HasPrefix(target, ReservationPrefixDecision):
		id := strings.TrimPrefix(target, ReservationPrefixDecision)
		if _, ok := IDDecision.Parse(id); !ok {
			return fmt.Errorf("予約する決定事項の ID が不正です: %q", id)
		}
		return nil
	case strings.HasPrefix(target, ReservationPrefixOpenIssue):
		id := strings.TrimPrefix(target, ReservationPrefixOpenIssue)
		if _, ok := IDOpenIssue.Parse(id); !ok {
			return fmt.Errorf("予約する未決事項の ID が不正です: %q", id)
		}
		return nil
	}
	return fmt.Errorf("予約できない対象です: %q", target)
}

// ValidReservationMode は mode が exclusive / concurrent のいずれかかを返す。
func ValidReservationMode(mode string) bool {
	return mode == ReservationExclusive || mode == ReservationConcurrent
}

// Reservation は reservations.yaml の 1 エントリ。
type Reservation struct {
	Target      string    `yaml:"target"`
	Mode        string    `yaml:"mode"`
	AuthorID    string    `yaml:"author_id"`
	DisplayName string    `yaml:"display_name"`
	StartedAt   time.Time `yaml:"started_at"`
	// ReleasedAt は解除日時（未解除はゼロ値。YAML では省略）。
	ReleasedAt time.Time `yaml:"released_at,omitempty"`
	// ReleasedBy は解除した作業者（他メンバーによる解除の追跡用。未解除は空）。
	ReleasedBy string `yaml:"released_by,omitempty"`
}

// Active は未解除かを返す。
func (r Reservation) Active() bool { return r.ReleasedAt.IsZero() }

// Reservations は reservations.yaml 全体。
type Reservations struct {
	Reservations []Reservation `yaml:"reservations"`
}

// UnmarshalReservations は reservations.yaml のバイト列を解釈する。
func UnmarshalReservations(data []byte) (*Reservations, error) {
	var r Reservations
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("reservations.yaml を解釈できません: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Marshal は reservations.yaml のバイト列を組み立てる。
func (r *Reservations) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.Reservations == nil {
		return []byte("reservations: []\n"), nil
	}
	out, err := yaml.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("reservations.yaml を組み立てられません: %w", err)
	}
	return out, nil
}

// Validate は reservations.yaml の制約を検証する。
//
// 同じ作業者が同じ対象に持つ**未解除**のエントリは 1 件まで（解除済みは履歴として複数残る）。
func (r *Reservations) Validate() error {
	activeKey := map[string]bool{}
	for i, e := range r.Reservations {
		if err := ValidateReservationTarget(e.Target); err != nil {
			return fmt.Errorf("reservations.yaml の %d 件目: %w", i+1, err)
		}
		if !ValidReservationMode(e.Mode) {
			return fmt.Errorf("reservations.yaml の %d 件目: 進め方が %q / %q 以外です: %q",
				i+1, ReservationExclusive, ReservationConcurrent, e.Mode)
		}
		if _, err := NormalizeAuthorID(e.AuthorID); err != nil {
			return fmt.Errorf("reservations.yaml の %d 件目: %w", i+1, err)
		}
		if e.DisplayName == "" {
			return fmt.Errorf("reservations.yaml の %d 件目に表示名がありません", i+1)
		}
		if e.StartedAt.IsZero() {
			return fmt.Errorf("reservations.yaml の %d 件目に着手日時がありません", i+1)
		}
		if !e.ReleasedAt.IsZero() && e.ReleasedBy == "" {
			return fmt.Errorf("reservations.yaml の %d 件目に解除者がありません", i+1)
		}
		if e.Active() {
			key := e.AuthorID + "\x00" + e.Target
			if activeKey[key] {
				return fmt.Errorf("reservations.yaml に同じ作業者・同じ対象の未解除の予約が重複しています: %s / %s",
					e.AuthorID, e.Target)
			}
			activeKey[key] = true
		}
	}
	return nil
}

// Active は未解除のエントリを対象 ID・着手日時の昇順で返す（作業状況の一覧）。
func (r *Reservations) Active() []Reservation {
	var out []Reservation
	for _, e := range r.Reservations {
		if e.Active() {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out
}

// ExclusiveBy は対象を排他で予約している未解除のエントリのうち、authorID **以外**の作業者のものを返す
// （重ねての着手の警告に使う）。
func (r *Reservations) ExclusiveBy(target, exceptAuthorID string) (Reservation, bool) {
	for _, e := range r.Active() {
		if e.Target == target && e.Mode == ReservationExclusive && e.AuthorID != exceptAuthorID {
			return e, true
		}
	}
	return Reservation{}, false
}

// activeIndex は authorID が target に持つ未解除エントリの添字を返す（無ければ -1）。
func (r *Reservations) activeIndex(target, authorID string) int {
	for i, e := range r.Reservations {
		if e.Active() && e.Target == target && e.AuthorID == authorID {
			return i
		}
	}
	return -1
}

// LoadReservations は reservations.yaml を読む。ファイルが無い（データ形式 1.4 より前に作成したプロジェクト）
// ときは空として扱う（後から足した任意のファイルのため、古いプロジェクトも開けるように）。
func (s *Store) LoadReservations() (*Reservations, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FileReservations))
	if os.IsNotExist(err) {
		return &Reservations{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("予約と作業状況を読み込めません: %w", err)
	}
	return UnmarshalReservations(data)
}

// SaveReservations は reservations.yaml を原子的に書き込む（保存キュー経由）。
func (s *Store) SaveReservations(r *Reservations) error {
	out, err := r.Marshal()
	if err != nil {
		return err
	}
	return s.write(func() error {
		return WriteFileAtomic(filepath.Join(s.root, FileReservations), out)
	})
}

// SetReservation は自分の名義で対象の予約（排他）または並行の宣言を記録する。
//
// 同じ対象に自分の未解除エントリが既にある場合は進め方だけを更新し、着手日時は保たれる
// （進め方も同じなら書き込みを生じない）。他メンバーの排他予約があっても**拒否しない**
// （警告と確認操作は呼び出し側）。
func (s *Store) SetReservation(target, mode string) (Reservation, error) {
	if err := ValidateReservationTarget(target); err != nil {
		return Reservation{}, err
	}
	if !ValidReservationMode(mode) {
		return Reservation{}, fmt.Errorf("進め方が不正です（%s / %s のいずれか）: %q",
			ReservationExclusive, ReservationConcurrent, mode)
	}
	var result Reservation
	err := s.WithShortLock(LockRecords, func() error {
		reservations, err := s.LoadReservations()
		if err != nil {
			return err
		}
		if idx := reservations.activeIndex(target, s.author.AuthorID); idx >= 0 {
			if reservations.Reservations[idx].Mode == mode {
				result = reservations.Reservations[idx]
				return nil
			}
			reservations.Reservations[idx].Mode = mode
			result = reservations.Reservations[idx]
			return s.SaveReservations(reservations)
		}
		result = Reservation{
			Target: target, Mode: mode,
			AuthorID: s.author.AuthorID, DisplayName: s.author.DisplayName,
			StartedAt: time.Now().UTC().Truncate(time.Second),
		}
		reservations.Reservations = append(reservations.Reservations, result)
		return s.SaveReservations(reservations)
	})
	if err != nil {
		return Reservation{}, err
	}
	return result, nil
}

// ReleaseReservation は authorID が対象に持つ未解除の予約を解除する。
//
// 解除者は操作中の作業者（released_by）。他メンバーの予約の解除に要する確認操作・権限判定は
// 呼び出し側の責務。解除した時点のエントリ（解除情報つき）を返す。
func (s *Store) ReleaseReservation(target, authorID string) (Reservation, error) {
	if strings.TrimSpace(target) == "" {
		return Reservation{}, fmt.Errorf("解除する予約を選んでください")
	}
	normalized, err := NormalizeAuthorID(authorID)
	if err != nil {
		return Reservation{}, err
	}
	var released Reservation
	err = s.WithShortLock(LockRecords, func() error {
		reservations, err := s.LoadReservations()
		if err != nil {
			return err
		}
		idx := reservations.activeIndex(target, normalized)
		if idx < 0 {
			return fmt.Errorf("この予約は既に解除されています。一覧を更新してください。")
		}
		reservations.Reservations[idx].ReleasedAt = time.Now().UTC().Truncate(time.Second)
		reservations.Reservations[idx].ReleasedBy = s.author.AuthorID
		released = reservations.Reservations[idx]
		return s.SaveReservations(reservations)
	})
	if err != nil {
		return Reservation{}, err
	}
	return released, nil
}

// ActiveReservationsAt はプロジェクトを開かずに未解除の予約を読む（プロジェクト一覧の遅延読込）。
//
// 読めない・壊れている場合は空を返す（一覧表示のために誤った作業状況を出さない）。
func ActiveReservationsAt(root string) []Reservation {
	data, err := os.ReadFile(filepath.Join(root, FileReservations))
	if err != nil {
		return nil
	}
	r, err := UnmarshalReservations(data)
	if err != nil {
		return nil
	}
	return r.Active()
}
