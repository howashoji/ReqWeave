package binding

// 本ファイルは取り込み画面のプロジェクト観点の部分。
//
// 観点の追加・編集・削除は projectstore が records ロック内で原子的に書き込み、
// 本層が権限を判定し（閲覧権限では拒否）変更履歴へ記録する
// （target: PRS-nnn / change: created・updated・removed）。
//
// いずれの操作も AI プロバイダを呼ばない（AI API 障害中も操作できる。
// 本ファイルが aiprovider へ依存しないことが構造的な担保）。

import (
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// PerspectiveView はプロジェクト観点の 1 行。
type PerspectiveView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Origin は由来（import / manual）。
	Origin string `json:"origin"`
	// OriginLabel は画面表示用の由来（内部の値をそのまま出さない）。
	OriginLabel string `json:"originLabel"`
	// Evidence は取り込み元の該当箇所（IMP-nnn#Lm-Ln。手動登録では空）。
	Evidence string `json:"evidence,omitempty"`
	// TopicKey は質問生成で使う論点キー（custom/PRS-nnn）。
	TopicKey  string    `json:"topicKey"`
	CreatedAt time.Time `json:"createdAt"`
	Author    string    `json:"author"`
}

// PerspectiveRequest はプロジェクト観点の登録・変更の入力。
type PerspectiveRequest struct {
	// ID は変更時のみ指定する（登録時は空）。
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

func perspectiveView(p projectstore.Perspective) PerspectiveView {
	return PerspectiveView{
		ID: p.ID, Name: p.Name, Summary: p.Summary,
		Origin: p.Origin, OriginLabel: perspectiveOriginLabel(p.Origin),
		Evidence: p.Evidence, TopicKey: p.TopicKey(),
		CreatedAt: p.CreatedAt, Author: p.Author,
	}
}

// perspectiveOriginLabel は由来の日本語表示（値集合は import / manual で閉じている）。
func perspectiveOriginLabel(origin string) string {
	switch origin {
	case projectstore.PerspectiveOriginImport:
		return "資料の分析から登録"
	case projectstore.PerspectiveOriginManual:
		return "手動で登録"
	default:
		return "由来不明"
	}
}

// Perspectives は登録済みのプロジェクト観点を返す（閲覧権限でも参照できる）。
//
// 論理削除された観点は含まない（削除済みの観点は読む側がすべて除外する）。
func (a *API) Perspectives() ([]PerspectiveView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	registered, err := s.store.ActivePerspectives()
	if err != nil {
		return nil, err
	}
	out := make([]PerspectiveView, 0, len(registered))
	for _, p := range registered {
		out = append(out, perspectiveView(p))
	}
	return out, nil
}

// AddPerspective はプロジェクト観点を手動で登録する。
//
// 取り込み分析の観点候補からの登録は承認操作（ApplyMaterialApproval）を通る。
func (a *API) AddPerspective(req PerspectiveRequest) (PerspectiveView, error) {
	s, err := a.current()
	if err != nil {
		return PerspectiveView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "プロジェクト観点の登録"); err != nil {
		return PerspectiveView{}, err
	}
	added, err := s.store.AddPerspective(req.Name, req.Summary, projectstore.PerspectiveOriginManual, "")
	if err != nil {
		return PerspectiveView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At:     time.Now().UTC(),
		Author: s.store.Author().AuthorID,
		Target: added.ID,
		Change: auditlog.ChangeCreated,
		After:  added.Name,
	})
	return perspectiveView(added), nil
}

// UpdatePerspective はプロジェクト観点の観点名・要旨を変更する。
//
// 論点キー（custom/PRS-nnn）は変わらないため、既存の決定事項との既決判定
// （決着済みの論点を再び質問しない判定）は変更前後で一致し続ける。
func (a *API) UpdatePerspective(req PerspectiveRequest) (PerspectiveView, error) {
	s, err := a.current()
	if err != nil {
		return PerspectiveView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "プロジェクト観点の変更"); err != nil {
		return PerspectiveView{}, err
	}
	before, after, err := s.store.UpdatePerspective(req.ID, req.Name, req.Summary)
	if err != nil {
		return PerspectiveView{}, err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At:     time.Now().UTC(),
		Author: s.store.Author().AuthorID,
		Target: after.ID,
		Change: auditlog.ChangeUpdated,
		Before: before.Name,
		After:  after.Name,
	})
	return perspectiveView(after), nil
}

// RemovePerspective はプロジェクト観点を削除する。
//
// 実体は消さず deleted_at / deleted_by を立てる論理削除（既存の決定事項から参照され続けるため）。削除後は一覧・
// 質問生成の論点連結・プロンプトへの注入から外れ、PRS-nnn は再採番されない（ID は使い回さない）。
// 既存の決定事項に残る論点キー custom/PRS-nnn は書き換えない（決定の記録は追記のみ。
// 過去の決定は既決のまま扱われる）。
func (a *API) RemovePerspective(id string) error {
	s, err := a.current()
	if err != nil {
		return err
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "プロジェクト観点の削除"); err != nil {
		return err
	}
	removed, err := s.store.RemovePerspective(id)
	if err != nil {
		return err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At:     time.Now().UTC(),
		Author: s.store.Author().AuthorID,
		Target: removed.ID,
		Change: auditlog.ChangeRemoved,
		Before: removed.Name,
	})
	return nil
}
