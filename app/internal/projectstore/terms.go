package projectstore

// 本ファイルは用語集（terms.yaml）を担う。
//
// 用語は成果物の本文に含まれない純構造化データ。
// 用語集は全文書の用語の正本であり、同義語を新造しない。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Term は 1 用語。
type Term struct {
	Name       string `yaml:"name"`
	NameEn     string `yaml:"name_en"`
	Definition string `yaml:"definition"`
	// Forbidden は禁止同義語（表記ゆれの検出に使う）。
	Forbidden []string `yaml:"forbidden,omitempty"`
}

// Terms は terms.yaml 全体。
type Terms struct {
	Terms []Term `yaml:"terms"`
}

// Validate は必須項目と一意性を検証する。
func (t *Terms) Validate() error {
	seen := map[string]bool{}
	seenEn := map[string]bool{}
	for _, term := range t.Terms {
		if strings.TrimSpace(term.Name) == "" {
			return fmt.Errorf("用語の表記がありません")
		}
		if strings.TrimSpace(term.NameEn) == "" {
			return fmt.Errorf("用語の英語識別子がありません（%s）", term.Name)
		}
		if strings.TrimSpace(term.Definition) == "" {
			return fmt.Errorf("用語の定義がありません（%s）", term.Name)
		}
		if seen[term.Name] {
			return fmt.Errorf("用語の表記が重複しています: %s", term.Name)
		}
		seen[term.Name] = true
		if seenEn[term.NameEn] {
			return fmt.Errorf("用語の英語識別子が重複しています: %s", term.NameEn)
		}
		seenEn[term.NameEn] = true
		for _, f := range term.Forbidden {
			if f == term.Name {
				return fmt.Errorf("禁止同義語に表記そのものが入っています（%s）", term.Name)
			}
		}
	}
	return nil
}

// Find は表記で用語を探す。
func (t *Terms) Find(name string) (Term, bool) {
	for _, term := range t.Terms {
		if term.Name == name {
			return term, true
		}
	}
	return Term{}, false
}

// LoadTerms は terms.yaml を読む（未作成・空でも空の Terms を返す）。
func (s *Store) LoadTerms() (*Terms, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FileTerms))
	if os.IsNotExist(err) {
		return &Terms{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("用語集を読み込めません: %w", err)
	}
	var t Terms
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("用語集を解釈できません: %w", err)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

// SaveTerms は terms.yaml を原子的に書き込む（表記の昇順に整列して保存する）。
func (s *Store) SaveTerms(t *Terms) error {
	if t == nil {
		return fmt.Errorf("用語集がありません")
	}
	if err := t.Validate(); err != nil {
		return err
	}
	sorted := Terms{Terms: append([]Term(nil), t.Terms...)}
	sort.SliceStable(sorted.Terms, func(i, j int) bool { return sorted.Terms[i].Name < sorted.Terms[j].Name })
	data, err := yaml.Marshal(&sorted)
	if err != nil {
		return fmt.Errorf("用語集を組み立てられません: %w", err)
	}
	return s.WriteFile(FileTerms, data)
}

// UpsertTerm は用語を追加または更新する（表記が一致するものを置き換える）。
func (s *Store) upsertTerm(term Term) (*Terms, error) {
	t, err := s.LoadTerms()
	if err != nil {
		return nil, err
	}
	replaced := false
	for i := range t.Terms {
		if t.Terms[i].Name == term.Name {
			t.Terms[i] = term
			replaced = true
			break
		}
	}
	if !replaced {
		t.Terms = append(t.Terms, term)
	}
	if err := s.SaveTerms(t); err != nil {
		return nil, err
	}
	return s.LoadTerms()
}
