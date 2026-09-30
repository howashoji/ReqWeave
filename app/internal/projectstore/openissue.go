package projectstore

// 本ファイルは未決事項（open-issues/ISS-nnn.md）を担う。
//
// 未決事項は「誰が・いつまでに・何を決めるか」を保持する。
// ブロック対象の要件は保持しない（要件項目側の blocked_by が正）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dirOpenIssues は未決事項の配置。
const dirOpenIssues = "open-issues"

// 未決事項の状態（フロントマターの status）。
const (
	OpenIssueOpen     = "open"
	OpenIssueResolved = "resolved"
)

// dueLayout は期限の書式（フロントマターの due）。
const dueLayout = "2006-01-02"

// OpenIssue は未決事項のフロントマター。
type OpenIssue struct {
	ID string `yaml:"id"`
	// Owner は決める人（08: 決める人）。
	Owner string `yaml:"owner"`
	// Due は期限（YYYY-MM-DD）。空 = 未設定。
	Due    string `yaml:"due,omitempty"`
	Status string `yaml:"status"`
	// NeedsStakeholder は質問票発行の候補か（ステークホルダーへ質問票で確かめる対象の選定）。
	NeedsStakeholder bool `yaml:"needs_stakeholder,omitempty"`
	// Evidence は起票根拠への参照。1 件以上必須（根拠へ辿れない未決事項を作らない）。
	Evidence []string `yaml:"evidence"`
	// ResolvedBy は決着させた決定事項の ID。
	ResolvedBy string `yaml:"resolved_by,omitempty"`

	// Body は論点（Markdown）。
	Body string `yaml:"-"`
}

// OpenIssueFile は未決事項ファイルの相対パスを返す。
func OpenIssueFile(id string) string { return dirOpenIssues + "/" + id + ".md" }

// Validate は必須項目・値集合を検証する。
func (i *OpenIssue) Validate() error {
	if _, ok := IDOpenIssue.Parse(i.ID); !ok {
		return fmt.Errorf("未決事項の ID が不正です: %q", i.ID)
	}
	if strings.TrimSpace(i.Owner) == "" {
		return fmt.Errorf("未決事項には決める人が必要です（%s）", i.ID)
	}
	switch i.Status {
	case OpenIssueOpen:
	case OpenIssueResolved:
		if i.ResolvedBy == "" {
			return fmt.Errorf("決着した未決事項には決着させた決定事項の ID が必要です（%s）", i.ID)
		}
	default:
		return fmt.Errorf("未決事項の状態が不正です: %q", i.Status)
	}
	if i.Due != "" {
		if _, err := time.Parse(dueLayout, i.Due); err != nil {
			return fmt.Errorf("未決事項の期限は YYYY-MM-DD で指定してください（%s）: %q", i.ID, i.Due)
		}
	}
	if len(i.Evidence) == 0 {
		return fmt.Errorf("未決事項には根拠への参照が 1 件以上必要です（%s）", i.ID)
	}
	if strings.TrimSpace(i.Body) == "" {
		return fmt.Errorf("未決事項の論点がありません（%s）", i.ID)
	}
	return nil
}

// IsOverdue は期限を過ぎた未決状態かを返す（期限超過の識別表示に使う）。
// 期限当日は超過としない。決着済みは常に false。
func (i *OpenIssue) IsOverdue(now time.Time) bool {
	if i.Status != OpenIssueOpen || i.Due == "" {
		return false
	}
	due, err := time.Parse(dueLayout, i.Due)
	if err != nil {
		return false
	}
	// 期限日の終わり（ローカル時刻の 23:59:59）を過ぎたら超過とする。
	limit := time.Date(due.Year(), due.Month(), due.Day(), 23, 59, 59, 0, now.Location())
	return now.After(limit)
}

// CreateOpenIssue は未決事項を新規記録し、採番した ID を返す。
func (s *Store) CreateOpenIssue(i OpenIssue) (*OpenIssue, error) {
	if i.Status == "" {
		i.Status = OpenIssueOpen
	}
	id, err := s.AllocateID(IDOpenIssue, func(id string) error {
		i.ID = id
		if err := i.Validate(); err != nil {
			return err
		}
		data, err := marshalDocument(&i, i.Body)
		if err != nil {
			return err
		}
		return s.WriteFile(OpenIssueFile(id), data)
	})
	if err != nil {
		return nil, err
	}
	i.ID = id
	return &i, nil
}

// UpdateOpenIssue は未決事項を読み直して mutate を適用し、原子的に書き戻す。
//
// 未決事項は決着・担当・期限が変わるレコードのため更新を許す（決定事項の追記のみとは異なる）。
// 変更履歴への記録は上位（承認・反映の処理）が行う。
func (s *Store) updateOpenIssue(id string, mutate func(*OpenIssue) error) (*OpenIssue, error) {
	issue, err := s.LoadOpenIssue(id)
	if err != nil {
		return nil, err
	}
	if err := mutate(issue); err != nil {
		return nil, err
	}
	if issue.ID != id {
		return nil, fmt.Errorf("未決事項の ID は変更できません（%s → %s）", id, issue.ID)
	}
	if err := issue.Validate(); err != nil {
		return nil, err
	}
	data, err := marshalDocument(issue, issue.Body)
	if err != nil {
		return nil, err
	}
	if err := s.WriteFile(OpenIssueFile(id), data); err != nil {
		return nil, err
	}
	return issue, nil
}

// ResolveOpenIssue は未決事項を決着させる（決定事項の記録から続く一連操作の一部）。
func (s *Store) resolveOpenIssueUnguarded(id, decisionID string) (*OpenIssue, error) {
	if _, err := s.LoadDecision(decisionID); err != nil {
		return nil, fmt.Errorf("決着させた決定事項が見つかりません（%s）: %w", decisionID, err)
	}
	return s.updateOpenIssue(id, func(i *OpenIssue) error {
		if i.Status == OpenIssueResolved {
			return fmt.Errorf("未決事項 %s は既に %s で決着しています", id, i.ResolvedBy)
		}
		i.Status = OpenIssueResolved
		i.ResolvedBy = decisionID
		return nil
	})
}

// LoadOpenIssue は未決事項を読む。
func (s *Store) LoadOpenIssue(id string) (*OpenIssue, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(OpenIssueFile(id))))
	if err != nil {
		return nil, fmt.Errorf("未決事項を読み込めません（%s）: %w", id, err)
	}
	var i OpenIssue
	body, err := parseDocument(data, &i)
	if err != nil {
		return nil, fmt.Errorf("未決事項を解釈できません（%s）: %w", id, err)
	}
	i.Body = body
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return &i, nil
}

// ListOpenIssues は全未決事項を ID 順で返す（未決事項の一覧・対話エンジンへの文脈注入に使う）。
func (s *Store) ListOpenIssues() ([]OpenIssue, error) {
	ids, err := listRecordIDs(s.root, dirOpenIssues, IDOpenIssue)
	if err != nil {
		return nil, err
	}
	out := make([]OpenIssue, 0, len(ids))
	for _, id := range ids {
		i, err := s.LoadOpenIssue(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, nil
}
