package projectstore

// 本ファイルは共有レコードの競合検知とマージを担う。
//
// 楽観的競合検知: 反映候補の生成時に対象レコードの**基準版**（内容ハッシュ + 表示用の本文）を
// 添え、書き込み直前に再読して比較する。相違があれば競合として**書き込みを中断**し、
// 三面（基準版 / 現在の内容 / 自分の反映案）を提示できる情報を返す（提示は競合解決の画面）。
//
// **後勝ち上書きの経路を持たない**: 既存の共有レコードを書き換える公開 API は
// 基準版を要求する Guarded 版だけであり、基準版なしで書き換える公開 API を設けない
// （検証は conflict_api_test.go が構造として固定する）。
//
// マージは反映を行う担当者本人の承認でのみ成立する（AI や自動処理で決着させない）。承認の結果として
// 「自分の案を通す（= 現在版を新しい基準版にして再実行）」「相手を採用（= 反映しない）」
// 「未決事項化（= 起票して反映しない）」のいずれかを呼び出し側が選ぶ。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RecordBaseline は反映候補が持つ基準版。
//
// Hash は検知に、Body は三面表示（基準版の内容）に使う。**保存はしない**（候補と同じ寿命）。
type RecordBaseline struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
	Body string `json:"body"`
}

// IsNew は新規作成（基準版が無い）かを返す。新規作成は競合しない。
func (b RecordBaseline) IsNew() bool { return b.Hash == "" }

// RecordConflict は検知した競合 1 件（三面のうち、基準版と現在の内容）。
type RecordConflict struct {
	ID string `json:"id"`
	// BaselineBody は候補生成時点の内容（三面の左）。
	BaselineBody string `json:"baselineBody"`
	// CurrentBody は現在の内容（他メンバーの変更。三面の中央）。
	CurrentBody string `json:"currentBody"`
	// CurrentHash は現在の内容ハッシュ（マージ承認時の新しい基準版になる）。
	CurrentHash string `json:"currentHash"`
}

// ErrRecordConflict は競合により書き込みを中断したことを表す。
type ErrRecordConflict struct {
	Conflicts []RecordConflict
}

func (e *ErrRecordConflict) Error() string {
	ids := make([]string, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		ids = append(ids, c.ID)
	}
	return fmt.Sprintf(
		"他のメンバーの変更と競合しています（%s）。変更内容を確認し、反映のしかたを選んでください。",
		strings.Join(ids, " "))
}

// CurrentBaseline は現在の内容から基準版を作る（候補生成時に呼ぶ）。
//
// 対象が存在しない場合は空の基準版（新規作成）を返す。ハッシュの算出は派生インデックス
// （index.go）と同一の規則にそろえる（同じ ID に 2 種類のハッシュを作らない）。
func (s *Store) CurrentBaseline(id string) (RecordBaseline, error) {
	if name, ok := strings.CutPrefix(id, TermIndexPrefix); ok {
		return s.currentTermBaseline(id, name)
	}
	rel, ok := recordFileOf(id)
	if !ok {
		return RecordBaseline{}, fmt.Errorf("基準版を取れないレコードです: %q", id)
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return RecordBaseline{ID: id}, nil
	}
	if err != nil {
		return RecordBaseline{}, fmt.Errorf("基準版を読み込めません（%s）: %w", id, err)
	}
	return RecordBaseline{ID: id, Hash: contentHash(data), Body: string(data)}, nil
}

// currentTermBaseline は用語 1 件の基準版を返す（用語は terms.yaml に同居するため名前で引く）。
func (s *Store) currentTermBaseline(id, name string) (RecordBaseline, error) {
	terms, err := s.LoadTerms()
	if err != nil {
		return RecordBaseline{}, err
	}
	for _, t := range terms.Terms {
		if t.Name != name {
			continue
		}
		return RecordBaseline{ID: id, Hash: contentHash(termContent(t)),
			Body: fmt.Sprintf("%s（%s）: %s", t.Name, t.NameEn, t.Definition)}, nil
	}
	return RecordBaseline{ID: id}, nil
}

// checkBaseline は基準版と現在の内容を比べ、相違があれば競合を返す。
func (s *Store) checkBaseline(baseline RecordBaseline) (*RecordConflict, error) {
	current, err := s.CurrentBaseline(baseline.ID)
	if err != nil {
		return nil, err
	}
	if baseline.IsNew() {
		// 新規作成のつもりで既に実体がある = 並行作成（重複）。
		if current.Hash != "" {
			return &RecordConflict{ID: baseline.ID, CurrentBody: current.Body, CurrentHash: current.Hash}, nil
		}
		return nil, nil
	}
	if current.Hash == baseline.Hash {
		return nil, nil
	}
	return &RecordConflict{
		ID: baseline.ID, BaselineBody: baseline.Body,
		CurrentBody: current.Body, CurrentHash: current.Hash,
	}, nil
}

// withRecordGuard は locks/records の中で基準版を再検証し、一致していれば write を実行する。
//
// 競合していれば write を呼ばずに *ErrRecordConflict を返す。
func (s *Store) withRecordGuard(baseline RecordBaseline, write func() error) error {
	return s.WithShortLock(LockRecords, func() error {
		conflict, err := s.checkBaseline(baseline)
		if err != nil {
			return err
		}
		if conflict != nil {
			return &ErrRecordConflict{Conflicts: []RecordConflict{*conflict}}
		}
		return write()
	})
}

// recordFileOf は共有レコードの相対パスを返す（対象外の ID は ok = false）。
func recordFileOf(id string) (string, bool) {
	switch {
	case strings.HasPrefix(id, IDDecision.Prefix+"-"):
		return DecisionFile(id), true
	case strings.HasPrefix(id, IDOpenIssue.Prefix+"-"):
		return OpenIssueFile(id), true
	case strings.HasPrefix(id, "FR-"), strings.HasPrefix(id, "NFR-"):
		return RequirementFile(id), true
	case strings.HasPrefix(id, TermIndexPrefix):
		return FileTerms, true
	}
	return "", false
}

// ---- 基準版を要求する書き込み API（これ以外に既存レコードを書き換える公開 API を設けない） ----

// UpdateRequirementGuarded は基準版を検証してから要件項目を更新する。
func (s *Store) UpdateRequirementGuarded(baseline RecordBaseline, id string,
	mutate func(*Requirement) error) (*Requirement, error) {

	var out *Requirement
	err := s.withRecordGuard(baseline, func() error {
		updated, err := s.updateRequirement(id, mutate)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateOpenIssueGuarded は基準版を検証してから未決事項を更新する。
func (s *Store) UpdateOpenIssueGuarded(baseline RecordBaseline, id string,
	mutate func(*OpenIssue) error) (*OpenIssue, error) {

	var out *OpenIssue
	err := s.withRecordGuard(baseline, func() error {
		updated, err := s.updateOpenIssue(id, mutate)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveOpenIssueGuarded は基準版を検証してから未決事項を決着させる。
func (s *Store) ResolveOpenIssueGuarded(baseline RecordBaseline, id, decisionID string) (*OpenIssue, error) {
	if _, err := s.LoadDecision(decisionID); err != nil {
		return nil, fmt.Errorf("決着させた決定事項が見つかりません（%s）: %w", decisionID, err)
	}
	return s.UpdateOpenIssueGuarded(baseline, id, func(i *OpenIssue) error {
		if i.Status == OpenIssueResolved {
			return fmt.Errorf("未決事項 %s は既に %s で決着しています", id, i.ResolvedBy)
		}
		i.Status = OpenIssueResolved
		i.ResolvedBy = decisionID
		return nil
	})
}

// UpsertTermGuarded は基準版を検証してから用語を追加・更新する。
//
// 用語は terms.yaml に同居するため、基準版は当該用語の内容（TermIndexID(name)）で取る。
func (s *Store) UpsertTermGuarded(baseline RecordBaseline, term Term) (*Terms, error) {
	if baseline.ID == "" {
		// 呼び出し側が基準版を持たない（新規追加）場合は当該用語のキーで検証する。
		baseline.ID = TermIndexID(term.Name)
	}
	var out *Terms
	err := s.withRecordGuard(baseline, func() error {
		updated, err := s.upsertTerm(term)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DuplicateDecisionOf は同一論点キーへの並行決定（重複）を返す（決定事項の競合の扱い）。
//
// 決定事項は追記のみのため本文の競合は生じない。競合は「同じ論点キーに対する
// 別メンバーの決定が既にある」ことであり、片方を supersedes 参照で整理する案を提示する。
// 覆された決定（superseded_by つき）は対象にしない。
func (s *Store) DuplicateDecisionOf(topicKey string) (*Decision, error) {
	if strings.TrimSpace(topicKey) == "" {
		return nil, nil
	}
	decisions, err := s.ListDecisions()
	if err != nil {
		return nil, err
	}
	for i := len(decisions) - 1; i >= 0; i-- {
		if decisions[i].TopicKey == topicKey && decisions[i].SupersededBy == "" {
			d := decisions[i]
			return &d, nil
		}
	}
	return nil, nil
}
