package projectstore

// 本ファイルは perspectives.yaml（プロジェクト観点）を担う。
//
// 追加（取り込み分析の観点候補の承認、または手動登録）・編集・削除は
// `locks/records`（同一端末の多重プロセス保護）の短時間ロック内で原子的に書き込む。変更履歴への記録は
// 呼び出し側（バインディング層・対話エンジン）が行う（roster.yaml と同じ分担）。
//
// 削除は論理削除。エントリを消さずに deleted_at / deleted_by を立てる。
// 物理削除の経路は持たない（削除すると PRS-nnn が再採番され、決定事項に残る論点キー
// custom/PRS-nnn が別の観点を指してしまう）。
//
// ドメインプリセット（project.yaml の domain_presets）とは独立に保持し、相互参照しない。
// 定義データを外部から動的に取得しない（外部への通信を増やさない）。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 観点の由来（perspectives.yaml の origin）。
const (
	// PerspectiveOriginImport は取り込み分析の観点候補を承認して登録したもの。
	PerspectiveOriginImport = "import"
	// PerspectiveOriginManual は手動登録（AI 呼び出しを伴わない。AI が使えないときも登録できる）。
	PerspectiveOriginManual = "manual"
)

// perspectiveEvidenceRe は取り込み元の該当箇所（IMP-nnn#Lm-Ln。取り込み資料の根拠と同形式）。
var perspectiveEvidenceRe = regexp.MustCompile(`^IMP-\d{3}#L\d+-L\d+$`)

// Perspective はプロジェクト観点 1 件。
type Perspective struct {
	ID      string `yaml:"id"`      // PRS-nnn
	Name    string `yaml:"name"`    // 観点名
	Summary string `yaml:"summary"` // 典型論点の要旨（プロンプトへ載せる）
	Origin  string `yaml:"origin"`  // import / manual
	// Evidence は origin: import のとき必須（IMP-nnn#Lm-Ln）。
	Evidence  string    `yaml:"evidence,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
	Author    string    `yaml:"author"` // 登録した作業者の利用者 ID
	// DeletedAt / DeletedBy は論理削除の印。立っている観点は利用側から除外する。
	DeletedAt time.Time `yaml:"deleted_at,omitempty"`
	DeletedBy string    `yaml:"deleted_by,omitempty"`
}

// Deleted は論理削除済みかを返す。
func (p Perspective) Deleted() bool { return !p.DeletedAt.IsZero() }

// TopicKey は論点キー（custom/PRS-nnn）を返す。
func (p Perspective) TopicKey() string { return PerspectiveTopicPrefix + "/" + p.ID }

// PerspectiveTopicPrefix は論点キーの接頭辞（custom/<PRS-nnn>）。
const PerspectiveTopicPrefix = "custom"

// Perspectives は perspectives.yaml 全体。
type Perspectives struct {
	Perspectives []Perspective `yaml:"perspectives"`
}

// Active は論理削除されていない観点だけを登録順に返す（利用側の除外規則）。
func (p *Perspectives) Active() []Perspective {
	out := make([]Perspective, 0, len(p.Perspectives))
	for _, v := range p.Perspectives {
		if !v.Deleted() {
			out = append(out, v)
		}
	}
	return out
}

// Find は ID で観点を引く（論理削除済みも返す。呼び出し側で Deleted を見る）。
func (p *Perspectives) Find(id string) (Perspective, bool) {
	for _, v := range p.Perspectives {
		if v.ID == id {
			return v, true
		}
	}
	return Perspective{}, false
}

// Validate は必須項目・値集合・ID の一意性を検証する。
func (p *Perspectives) Validate() error {
	seen := map[string]bool{}
	for i, v := range p.Perspectives {
		if _, ok := IDPerspective.Parse(v.ID); !ok {
			return fmt.Errorf("プロジェクト観点の %d 件目の ID が %s-nnn の形式ではありません: %q",
				i+1, IDPerspective.Prefix, v.ID)
		}
		if seen[v.ID] {
			return fmt.Errorf("プロジェクト観点の ID が重複しています: %s", v.ID)
		}
		seen[v.ID] = true
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("プロジェクト観点 %s に観点名がありません", v.ID)
		}
		if strings.TrimSpace(v.Summary) == "" {
			return fmt.Errorf("プロジェクト観点 %s に要旨がありません", v.ID)
		}
		switch v.Origin {
		case PerspectiveOriginImport:
			if v.Evidence == "" {
				return fmt.Errorf("プロジェクト観点 %s に取り込み元の該当箇所（IMP-nnn#Lm-Ln）がありません", v.ID)
			}
		case PerspectiveOriginManual:
		default:
			return fmt.Errorf("プロジェクト観点 %s の由来が不正です: %q", v.ID, v.Origin)
		}
		if v.Evidence != "" && !perspectiveEvidenceRe.MatchString(v.Evidence) {
			return fmt.Errorf("プロジェクト観点 %s の該当箇所の形式が不正です（IMP-nnn#Lm-Ln）: %q",
				v.ID, v.Evidence)
		}
		if v.CreatedAt.IsZero() {
			return fmt.Errorf("プロジェクト観点 %s に登録日時がありません", v.ID)
		}
		if strings.TrimSpace(v.Author) == "" {
			return fmt.Errorf("プロジェクト観点 %s に登録した作業者がありません", v.ID)
		}
		if v.Deleted() != (strings.TrimSpace(v.DeletedBy) != "") {
			return fmt.Errorf("プロジェクト観点 %s の削除記録が揃っていません（削除日時と削除者は両方必要です）", v.ID)
		}
	}
	return nil
}

// LoadPerspectives は perspectives.yaml を読む（未作成・空でも空の Perspectives を返す）。
//
// 論理削除済みの観点も含めて返す（採番の走査と削除済みの表示判断のため）。
// 一覧・質問生成などの利用側は Active を使う。
func (s *Store) LoadPerspectives() (*Perspectives, error) {
	data, err := os.ReadFile(filepath.Join(s.root, FilePerspectives))
	if os.IsNotExist(err) {
		return &Perspectives{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("プロジェクト観点を読み込めません: %w", err)
	}
	var p Perspectives
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("プロジェクト観点を解釈できません: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// ActivePerspectives は論理削除されていない観点を登録順に返す（利用側の入口）。
func (s *Store) ActivePerspectives() ([]Perspective, error) {
	p, err := s.LoadPerspectives()
	if err != nil {
		return nil, err
	}
	return p.Active(), nil
}

// savePerspectives は perspectives.yaml を原子的に書き込む（ID の昇順 = 採番順のため並べ替えない）。
func (s *Store) savePerspectives(p *Perspectives) error {
	if err := p.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("プロジェクト観点を組み立てられません: %w", err)
	}
	return s.WriteFile(FilePerspectives, data)
}

// AddPerspective はプロジェクト観点を登録する（承認・手動の共通経路）。
//
// `locks/records` 内で読み直し → 検証 → 原子的書き込みを行う。
// 採番は records ロック内で実体（perspectives.yaml）を再走査して確定する（カウンタを別に持たない）。
// AI 呼び出しは行わない（AI API 障害中も成功する）。
func (s *Store) AddPerspective(name, summary, origin, evidence string) (Perspective, error) {
	name, summary, evidence = strings.TrimSpace(name), strings.TrimSpace(summary), strings.TrimSpace(evidence)
	if name == "" {
		return Perspective{}, fmt.Errorf("観点名を入力してください")
	}
	if summary == "" {
		return Perspective{}, fmt.Errorf("観点の要旨を入力してください")
	}

	var added Perspective
	err := s.WithShortLock(LockRecords, func() error {
		_, err := s.allocateIDLocked(IDPerspective, func(id string) error {
			p, err := s.LoadPerspectives()
			if err != nil {
				return err
			}
			added = Perspective{
				ID: id, Name: name, Summary: summary, Origin: origin, Evidence: evidence,
				CreatedAt: time.Now().UTC().Truncate(time.Second), Author: s.author.AuthorID,
			}
			p.Perspectives = append(p.Perspectives, added)
			return s.savePerspectives(p)
		})
		return err
	})
	if err != nil {
		return Perspective{}, err
	}
	return added, nil
}

// UpdatePerspective は観点名・要旨を変更し、変更前後を返す。
//
// 由来・根拠・登録者・登録日時は変えない（登録時点の事実であるため）。
// 論理削除済みの観点は編集できない（削除を取り消してから編集する運用にはしない）。
func (s *Store) UpdatePerspective(id, name, summary string) (before, after Perspective, err error) {
	name, summary = strings.TrimSpace(name), strings.TrimSpace(summary)
	if name == "" {
		return Perspective{}, Perspective{}, fmt.Errorf("観点名を入力してください")
	}
	if summary == "" {
		return Perspective{}, Perspective{}, fmt.Errorf("観点の要旨を入力してください")
	}

	err = s.WithShortLock(LockRecords, func() error {
		p, loadErr := s.LoadPerspectives()
		if loadErr != nil {
			return loadErr
		}
		idx, findErr := indexOfPerspective(p, id)
		if findErr != nil {
			return findErr
		}
		before = p.Perspectives[idx]
		p.Perspectives[idx].Name = name
		p.Perspectives[idx].Summary = summary
		after = p.Perspectives[idx]
		return s.savePerspectives(p)
	})
	if err != nil {
		return Perspective{}, Perspective{}, err
	}
	return before, after, nil
}

// RemovePerspective は観点を論理削除する。
//
// エントリは残したまま deleted_at / deleted_by を立てる。採番の走査対象に残るため、
// 削除した PRS-nnn は再採番されない。
func (s *Store) RemovePerspective(id string) (Perspective, error) {
	var removed Perspective
	err := s.WithShortLock(LockRecords, func() error {
		p, loadErr := s.LoadPerspectives()
		if loadErr != nil {
			return loadErr
		}
		idx, findErr := indexOfPerspective(p, id)
		if findErr != nil {
			return findErr
		}
		p.Perspectives[idx].DeletedAt = time.Now().UTC().Truncate(time.Second)
		p.Perspectives[idx].DeletedBy = s.author.AuthorID
		removed = p.Perspectives[idx]
		return s.savePerspectives(p)
	})
	if err != nil {
		return Perspective{}, err
	}
	return removed, nil
}

// indexOfPerspective は編集・削除の対象位置を返す（未登録・削除済みは操作させない）。
func indexOfPerspective(p *Perspectives, id string) (int, error) {
	for i := range p.Perspectives {
		if p.Perspectives[i].ID != id {
			continue
		}
		if p.Perspectives[i].Deleted() {
			return 0, fmt.Errorf("削除済みのプロジェクト観点です（%s）。一覧から選び直してください", id)
		}
		return i, nil
	}
	return 0, fmt.Errorf("登録されていないプロジェクト観点です（%s）。一覧から選び直してください", id)
}
