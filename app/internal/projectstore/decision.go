package projectstore

// 本ファイルは決定事項（decisions/DEC-nnn.md）を担う。
//
// 決定事項は**追記のみ**で記録し、本文を書き換える API を持たない（覆すときは新しい決定で supersedes する）。
// 覆す場合は新しい決定を作り、旧決定には置き換え先への参照（superseded_by）だけを付ける。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// dirDecisions は決定事項の配置。
const dirDecisions = "decisions"

// Decision は決定事項のフロントマター。
type Decision struct {
	ID string `yaml:"id"`
	// TopicKey は論点キー（章観点ID/必須項目ID。対話エンジンの再質問防止と充足率算出のデータソース）。
	TopicKey  string    `yaml:"topic_key"`
	DecidedAt time.Time `yaml:"decided_at"`
	// Evidence は根拠への参照（S-nnnn#utt-nnnnn / QS-nnn#q-nn / IMP-nnn#Lm-Ln）。1 件以上必須（根拠へ辿れない決定を作らない）。
	Evidence []string `yaml:"evidence"`
	// Supersedes は覆した旧決定の ID。
	Supersedes string `yaml:"supersedes,omitempty"`
	// SupersededBy は本決定を覆した新しい決定の ID（旧決定側に後から付く）。
	SupersededBy string `yaml:"superseded_by,omitempty"`

	// Body は決定内容・根拠の要旨（Markdown）。作成後に書き換えない。
	Body string `yaml:"-"`
}

// DecisionFile は決定事項ファイルの相対パスを返す。
func DecisionFile(id string) string { return dirDecisions + "/" + id + ".md" }

// Validate は必須項目を検証する。
func (d *Decision) Validate() error {
	if _, ok := IDDecision.Parse(d.ID); !ok {
		return fmt.Errorf("決定事項の ID が不正です: %q", d.ID)
	}
	if strings.TrimSpace(d.TopicKey) == "" {
		return fmt.Errorf("決定事項の論点キーがありません（%s）", d.ID)
	}
	if d.DecidedAt.IsZero() {
		return fmt.Errorf("決定事項の決定日がありません（%s）", d.ID)
	}
	if len(d.Evidence) == 0 {
		return fmt.Errorf("決定事項には根拠への参照が 1 件以上必要です（%s）", d.ID)
	}
	if strings.TrimSpace(d.Body) == "" {
		return fmt.Errorf("決定事項の本文がありません（%s）", d.ID)
	}
	return nil
}

// CreateDecision は決定事項を新規記録し、採番した ID を返す（DEC-nnn の連番）。
//
// Supersedes が指定されている場合、旧決定へ superseded_by を追記する
// （旧決定の本文は書き換えない）。
func (s *Store) CreateDecision(d Decision) (*Decision, error) {
	if d.DecidedAt.IsZero() {
		d.DecidedAt = time.Now()
	}
	if d.Supersedes != "" {
		if _, err := s.LoadDecision(d.Supersedes); err != nil {
			return nil, fmt.Errorf("置き換え元の決定事項が見つかりません（%s）: %w", d.Supersedes, err)
		}
	}
	id, err := s.AllocateID(IDDecision, func(id string) error {
		d.ID = id
		if err := d.Validate(); err != nil {
			return err
		}
		data, err := marshalDocument(&d, d.Body)
		if err != nil {
			return err
		}
		return s.WriteFile(DecisionFile(id), data)
	})
	if err != nil {
		return nil, err
	}
	d.ID = id
	if d.Supersedes != "" {
		if err := s.markSuperseded(d.Supersedes, id); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// markSuperseded は旧決定へ置き換え先への参照を付ける（本文・他のフィールドは変えない）。
func (s *Store) markSuperseded(oldID, newID string) error {
	old, err := s.LoadDecision(oldID)
	if err != nil {
		return err
	}
	if old.SupersededBy != "" && old.SupersededBy != newID {
		return fmt.Errorf("決定事項 %s は既に %s で置き換えられています", oldID, old.SupersededBy)
	}
	old.SupersededBy = newID
	data, err := marshalDocument(old, old.Body)
	if err != nil {
		return err
	}
	return s.WriteFile(DecisionFile(oldID), data)
}

// LoadDecision は決定事項を読む。
func (s *Store) LoadDecision(id string) (*Decision, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(DecisionFile(id))))
	if err != nil {
		return nil, fmt.Errorf("決定事項を読み込めません（%s）: %w", id, err)
	}
	var d Decision
	body, err := parseDocument(data, &d)
	if err != nil {
		return nil, fmt.Errorf("決定事項を解釈できません（%s）: %w", id, err)
	}
	d.Body = body
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// ListDecisions は全決定事項を ID 順で返す（対話エンジンへの文脈注入・一覧に使う）。
func (s *Store) ListDecisions() ([]Decision, error) {
	ids, err := listRecordIDs(s.root, dirDecisions, IDDecision)
	if err != nil {
		return nil, err
	}
	out := make([]Decision, 0, len(ids))
	for _, id := range ids {
		d, err := s.LoadDecision(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, nil
}

// listRecordIDs は <dir>/<ID>.md のファイル名から ID を集めて昇順で返す。
func listRecordIDs(root, dir string, kind IDKind) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を走査できません: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if _, ok := kind.Parse(id); !ok {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
