package projectstore

// 本ファイルは roster.yaml（ステークホルダー名簿）を担う。
//
// 質問票の `addressee_ref` が参照する。回答の帰属は質問票 → addressee_ref で
// 解決し、名簿側に回答情報を持たない（二重管理禁止）。
// 名簿全体は受け渡しファイル・エクスポートに含めない（宛先自身の情報のみ質問票へ写す。社外へ他の宛先を渡さない）。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Stakeholder は名簿の 1 宛先。
type Stakeholder struct {
	ID   string `yaml:"id"`   // STK-nnn
	Name string `yaml:"name"` // 氏名
	Org  string `yaml:"org"`  // 所属
}

// Roster は roster.yaml 全体。
type Roster struct {
	Stakeholders []Stakeholder `yaml:"stakeholders"`
}

// Validate は必須項目と ID の一意性を検証する。
func (r *Roster) Validate() error {
	seen := map[string]bool{}
	for i, s := range r.Stakeholders {
		if _, ok := IDStakeholder.Parse(s.ID); !ok {
			return fmt.Errorf("名簿の %d 件目の ID が %s-nnn の形式ではありません: %q", i+1, IDStakeholder.Prefix, s.ID)
		}
		if seen[s.ID] {
			return fmt.Errorf("名簿の ID が重複しています: %s", s.ID)
		}
		seen[s.ID] = true
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("名簿の %s に氏名がありません", s.ID)
		}
		if strings.TrimSpace(s.Org) == "" {
			return fmt.Errorf("名簿の %s に所属がありません", s.ID)
		}
	}
	return nil
}

// Find は ID で宛先を探す。
func (r *Roster) Find(id string) (Stakeholder, bool) {
	for _, s := range r.Stakeholders {
		if s.ID == id {
			return s, true
		}
	}
	return Stakeholder{}, false
}

// LoadRoster は roster.yaml を読む（未作成・空でも空の Roster を返す）。
func (s *Store) LoadRoster() (*Roster, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FileRoster))
	if os.IsNotExist(err) {
		return &Roster{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("名簿を読み込めません: %w", err)
	}
	var r Roster
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("名簿を解釈できません: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// saveRoster は roster.yaml を原子的に書き込む（ID の昇順は採番順と一致するため並べ替えない）。
func (s *Store) saveRoster(r *Roster) error {
	if err := r.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("名簿を組み立てられません: %w", err)
	}
	return s.WriteFile(FileRoster, data)
}

// AddStakeholder は名簿へ宛先を登録する。
//
// `locks/roster.lock` 内で読み直し → 検証 → 原子的書き込みを行う。
// 採番は roster ロック内で実体（roster.yaml）を再走査して確定する（カウンタを別に持たない）。
// ロックを保持したまま採番するため allocateIDLocked を使う（AllocateID は再入できない）。
func (s *Store) AddStakeholder(name, org string) (Stakeholder, error) {
	name, org = strings.TrimSpace(name), strings.TrimSpace(org)
	if name == "" {
		return Stakeholder{}, fmt.Errorf("宛先の氏名を入力してください")
	}
	if org == "" {
		return Stakeholder{}, fmt.Errorf("宛先の所属を入力してください")
	}

	var added Stakeholder
	err := s.WithShortLock(LockRoster, func() error {
		_, err := s.allocateIDLocked(IDStakeholder, func(id string) error {
			r, err := s.LoadRoster()
			if err != nil {
				return err
			}
			added = Stakeholder{ID: id, Name: name, Org: org}
			r.Stakeholders = append(r.Stakeholders, added)
			return s.saveRoster(r)
		})
		return err
	})
	if err != nil {
		return Stakeholder{}, err
	}
	return added, nil
}

// UpdateStakeholder は名簿の宛先を変更し、変更前後を返す。
//
// 発行済み質問票の宛先表示は発行時点の写し（質問票の `addressee`）であり、
// ここでの変更では書き換わらない（過去の発行内容を後から変えない）。
func (s *Store) UpdateStakeholder(id, name, org string) (before, after Stakeholder, err error) {
	name, org = strings.TrimSpace(name), strings.TrimSpace(org)
	if name == "" {
		return Stakeholder{}, Stakeholder{}, fmt.Errorf("宛先の氏名を入力してください")
	}
	if org == "" {
		return Stakeholder{}, Stakeholder{}, fmt.Errorf("宛先の所属を入力してください")
	}

	err = s.WithShortLock(LockRoster, func() error {
		r, loadErr := s.LoadRoster()
		if loadErr != nil {
			return loadErr
		}
		idx := -1
		for i := range r.Stakeholders {
			if r.Stakeholders[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("名簿にない宛先です（%s）。一覧から選び直してください", id)
		}
		before = r.Stakeholders[idx]
		after = Stakeholder{ID: id, Name: name, Org: org}
		r.Stakeholders[idx] = after
		return s.saveRoster(r)
	})
	if err != nil {
		return Stakeholder{}, Stakeholder{}, err
	}
	return before, after, nil
}
