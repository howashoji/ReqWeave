package binding

// 本ファイルは質問票管理画面のステークホルダー名簿の部分。
//
// 名簿の登録・変更は projectstore が roster ロック内で原子的に書き込み、
// 本層が変更履歴へ記録する（target: STK-nnn / change: created・updated）。

import (
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// StakeholderView は名簿の 1 行。
type StakeholderView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Org  string `json:"org"`
	// Label は宛先選択の表示名（氏名（所属））。
	Label string `json:"label"`
}

// StakeholderRequest は名簿の登録・変更の入力。
type StakeholderRequest struct {
	// ID は変更時のみ指定する（登録時は空）。
	ID   string `json:"id"`
	Name string `json:"name"`
	Org  string `json:"org"`
}

func stakeholderView(s projectstore.Stakeholder) StakeholderView {
	return StakeholderView{ID: s.ID, Name: s.Name, Org: s.Org, Label: stakeholderLabel(s)}
}

// stakeholderLabel は宛先の表示名。発行時にはこの値を質問票の addressee へ写す。
func stakeholderLabel(s projectstore.Stakeholder) string {
	return fmt.Sprintf("%s（%s）", s.Name, s.Org)
}

// Roster は名簿の一覧を返す（閲覧権限でも参照できる）。
func (a *API) Roster() ([]StakeholderView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	roster, err := s.store.LoadRoster()
	if err != nil {
		return nil, err
	}
	out := make([]StakeholderView, 0, len(roster.Stakeholders))
	for _, st := range roster.Stakeholders {
		out = append(out, stakeholderView(st))
	}
	return out, nil
}

// AddStakeholder は名簿へ宛先を登録する（未登録の相手はここで登録してから発行する）。
func (a *API) AddStakeholder(req StakeholderRequest) (StakeholderView, error) {
	s, err := a.current()
	if err != nil {
		return StakeholderView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "名簿の登録"); err != nil {
		return StakeholderView{}, err
	}
	added, err := s.store.AddStakeholder(req.Name, req.Org)
	if err != nil {
		return StakeholderView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At:     time.Now().UTC(),
		Author: s.store.Author().AuthorID,
		Target: added.ID,
		Change: auditlog.ChangeCreated,
		After:  stakeholderLabel(added),
	})
	return stakeholderView(added), nil
}

// UpdateStakeholder は名簿の宛先を変更する。
//
// 発行済み質問票の宛先表示は発行時点の写しであり、ここでの変更で書き換わらない。
func (a *API) UpdateStakeholder(req StakeholderRequest) (StakeholderView, error) {
	s, err := a.current()
	if err != nil {
		return StakeholderView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "名簿の変更"); err != nil {
		return StakeholderView{}, err
	}
	before, after, err := s.store.UpdateStakeholder(req.ID, req.Name, req.Org)
	if err != nil {
		return StakeholderView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At:     time.Now().UTC(),
		Author: s.store.Author().AuthorID,
		Target: after.ID,
		Change: auditlog.ChangeUpdated,
		Before: stakeholderLabel(before),
		After:  stakeholderLabel(after),
	})
	return stakeholderView(after), nil
}
