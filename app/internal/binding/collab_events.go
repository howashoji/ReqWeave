package binding

// 本ファイルは共同作業の開閉イベントと端末紐づけの記録。
//
// プロジェクトを開く・閉じるたびに変更履歴へ project-opened / project-closed を記録する。
//
// **変更要約の単位は「取り込み」**であり、開閉の日時ではない（開いていない間に入った変更も漏らさないため）。
// 起点は「前回確認した取り込みの位置」で、端末ごとのアプリ設定（`recent_projects` の要素）に持つ。
// 入ってきた変更履歴レコードの特定は同期モジュールが行う。
//
// 当該端末（正規化済み OS ユーザー名）と利用者 ID の組で**初めて開くとき**だけ author-binding を
// 併せて記録する（なりすまし・誤登録を後から監査できるように）。OS ユーザー名を記録してよいのは
// このイベントだけであり（端末固有の情報をプロジェクトデータへ書かない原則の例外）、
// 他のレコードへは書かない。

import (
	"fmt"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// recordProjectOpened はプロジェクトを開いた事実を記録する。
//
// 記録に失敗しても開く操作は止めない（監査記録の失敗で作業を止めない = recordChange の方針）。
func (a *API) recordProjectOpened(s *dialogueSession) {
	author := s.store.Author()
	a.recordAuthorBindingIfNew(s, author)
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: author.AuthorID,
		Target: s.store.Project().ProjectID, Change: auditlog.ChangeProjectOpened,
		After: author.DisplayName,
	})
}

// recordProjectClosed はプロジェクトを閉じた事実を記録する。
func (a *API) recordProjectClosed(s *dialogueSession) {
	author := s.store.Author()
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: author.AuthorID,
		Target: s.store.Project().ProjectID, Change: auditlog.ChangeProjectClosed,
		After: author.DisplayName,
	})
}

// recordAuthorBindingIfNew は端末の OS アカウントと利用者 ID の紐づけを初回だけ記録する。
//
// 既出の組（同じ作業者・同じ OS ユーザー名）は再記録しない。
// OS ユーザー名を取得できない場合は記録しない（取得可否を識別の成立条件にしない。
// 共有プロジェクトを開けるかどうかの判定は別経路 = openProject が担う）。
func (a *API) recordAuthorBindingIfNew(s *dialogueSession, author projectstore.Author) {
	osUser, err := projectstore.CurrentOSUser()
	if err != nil || osUser == "" {
		return
	}
	if a.hasAuthorBinding(s, author.AuthorID, osUser) {
		return
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: author.AuthorID,
		Target: s.store.Project().ProjectID, Change: auditlog.ChangeAuthorBinding,
		After: author.DisplayName, OSUser: osUser,
	})
}

// hasAuthorBinding は当該作業者・当該端末の紐づけが既に記録されているかを返す。
func (a *API) hasAuthorBinding(s *dialogueSession, authorID, osUser string) bool {
	changes, err := auditlog.ReadChanges(s.store.Root(), time.Time{}, time.Time{})
	if err != nil {
		// 履歴を読めない場合は記録側へ倒す（監査の取りこぼしより重複記録を選ぶ）。
		return false
	}
	for _, c := range changes {
		if c.Change == auditlog.ChangeAuthorBinding && c.Author == authorID && c.OSUser == osUser {
			return true
		}
	}
	return false
}

// ---- 変更要約 --------------------------------------------------------------

// ChangeSummaryItemView は変更要約の 1 項目。
type ChangeSummaryItemView struct {
	// Kind は区分（decisions / open-issues / requirements / questionnaires / confirmations / members）。
	Kind string `json:"kind"`
	// KindLabel は画面表示用の区分名（内部値をそのまま出さない）。
	KindLabel string `json:"kindLabel"`
	Target    string `json:"target"`
	// Summary は変更内容の 1 行（変更履歴の before / after から組み立てる）。
	Summary string `json:"summary"`
	// Author は変更した作業者（表示名が分かる場合は表示名、無ければ利用者 ID）。
	Author string `json:"author"`
	At     string `json:"at"`
}

// ChangeSummaryView は前回確認した取り込み以降に入った、他の作業者による変更。
type ChangeSummaryView struct {
	// Incorporation は今回集計した取り込みの位置（確認済みとして保存する値）。
	// **端末内の識別子であり画面へ出さない**（利用者に git を見せない）。
	Incorporation string `json:"incorporation,omitempty"`
	// NoIncorporation は起点となる取り込みが無い状態か（対象なしとして扱う）。
	// 同期先が未設定・まだ一度も取り込んでいない・確認済みの位置が失われた場合。
	NoIncorporation bool `json:"noIncorporation"`
	// Counts は区分ごとの件数（区分 → 件数）。
	Counts map[string]int          `json:"counts,omitempty"`
	Items  []ChangeSummaryItemView `json:"items,omitempty"`
	// Notice は画面へ出す案内（変更なし・初回オープン）。無ければ空。
	Notice string `json:"notice,omitempty"`
}

// summaryKindLabels は区分の日本語表示（値集合は変更要約の区分で閉じている）。
var summaryKindLabels = map[string]string{
	projectstore.SummaryDecisions:     "決定事項",
	projectstore.SummaryOpenIssues:    "未決事項",
	projectstore.SummaryRequirements:  "要件項目",
	projectstore.SummaryQuestionnaire: "質問票",
	projectstore.SummaryConfirmation:  "確定・差し戻し",
	projectstore.SummaryMembers:       "メンバー",
	projectstore.SummaryReservations:  "予約と作業状況",
}

func summaryKindLabel(kind string) string {
	if label, ok := summaryKindLabels[kind]; ok {
		return label
	}
	return "その他"
}

// ChangeSummary は前回確認した取り込み以降に入った、他の作業者による変更を返す。
//
// 起点は端末ごとのアプリ設定に持つ「確認済みの取り込みの位置」。
// 起点が無い場合は**全期間を対象にせず対象なしとして返す**（初回に過去の全変更を見せない）。
//
// AI 呼び出しを伴わず、プロジェクトデータもアプリ設定も変更しない（要約を保存しない。
// 確認済みの位置の更新は AcknowledgeChangeSummary）。閲覧権限でも参照できる。
func (a *API) ChangeSummary() (ChangeSummaryView, error) {
	s, err := a.current()
	if err != nil {
		return ChangeSummaryView{}, err
	}
	incoming, err := a.incomingChanges(s)
	if err != nil {
		return ChangeSummaryView{}, err
	}
	summary := s.store.ChangeSummaryOfIncorporation(incoming.ID, incoming.NoStartingPoint, incoming.Changes)
	members, err := s.store.LoadMembers()
	if err != nil {
		return ChangeSummaryView{}, err
	}

	out := ChangeSummaryView{
		Incorporation:   summary.Incorporation,
		NoIncorporation: summary.NoIncorporation,
		Counts:          summary.CountByKind(),
	}
	for _, item := range summary.Items {
		out.Items = append(out.Items, ChangeSummaryItemView{
			Kind: item.Kind, KindLabel: summaryKindLabel(item.Kind), Target: item.Target,
			Summary: changeSummaryLine(item), Author: authorDisplayName(members, item.Author),
			At: item.At.Local().Format("2006-01-02 15:04"),
		})
	}
	switch {
	case summary.NoIncorporation:
		out.Notice = "まだ取り込みを行っていないため、表示する変更はありません。"
	case len(out.Items) == 0:
		out.Notice = "前回確認した取り込み以降、他の作業者による変更はありません。"
	}
	return out, nil
}

// incomingChanges は前回確認した取り込みから現在までに入った内容を返す。
func (a *API) incomingChanges(s *dialogueSession) (*syncmod.Incoming, error) {
	client, err := a.syncClient(s)
	if err != nil {
		return nil, err
	}
	if !client.Availability().Available {
		// 同期に必要なプログラムが無い端末。取り込みは行えないため対象なしとして扱う
		// （他の機能は止めない）。
		return &syncmod.Incoming{NoStartingPoint: true}, nil
	}
	since := ""
	if settings, err := a.settings(); err == nil {
		if recent, ok := settings.RecentProject(s.projectPath); ok {
			since = recent.AcknowledgedIncorporation
		}
	}
	return client.Incoming(a.context(), s.store.Root(), since)
}

// AcknowledgeChangeSummary は変更要約を確認したことを記録する（「確認済みの位置」を進める）。
//
// 位置は**端末ごとのアプリ設定**（`recent_projects` の要素）へ持ち、プロジェクトデータへ書かない。
// 以後の変更要約はこの位置以降に入った分だけを提示する。
func (a *API) AcknowledgeChangeSummary() error {
	s, err := a.current()
	if err != nil {
		return err
	}
	incoming, err := a.incomingChanges(s)
	if err != nil {
		return err
	}
	if incoming.ID == "" {
		return nil // 同期先を設定していない作業コピー（記録する位置が無い）
	}
	settings, err := a.settings()
	if err != nil {
		return err
	}
	settings.AcknowledgeIncorporation(s.projectPath, incoming.ID, time.Now().UTC())
	return projectstore.SaveSettings(a.paths, settings)
}

// applyIncorporationToIndex は取り込みで変更されたファイルを派生インデックスへ反映する
// （通常操作では再読込せず、取り込みの完了時にだけ差分再構築する）。
//
// 索引は派生データのため、失敗しても取り込みは止めない（次回の読み込みで全再構築へ倒れる）。
func (a *API) applyIncorporationToIndex(s *dialogueSession, incoming *syncmod.Incoming) {
	if incoming == nil || len(incoming.Paths) == 0 || a.pathsErr != nil {
		return
	}
	ix, err := projectstore.LoadRecordIndex(a.paths, s.store)
	if err != nil {
		return
	}
	_ = ix.ApplyChangedPaths(a.paths, s.store, incoming.Paths)
}

// changeSummaryLine は 1 項目の変更内容を 1 行にする（変更履歴の before / after を用いる）。
func changeSummaryLine(item projectstore.ChangeSummaryItem) string {
	switch {
	case item.Before != "" && item.After != "":
		return fmt.Sprintf("%s → %s", item.Before, item.After)
	case item.After != "":
		return item.After
	case item.Before != "":
		return item.Before
	default:
		return changeKindLabel(item.Change)
	}
}

// changeKindLabel は変更種別の日本語表示（before / after が無いときの代替）。
func changeKindLabel(change string) string {
	switch change {
	case auditlog.ChangeCreated:
		return "追加"
	case auditlog.ChangeUpdated:
		return "変更"
	case auditlog.ChangeRemoved:
		return "削除"
	case auditlog.ChangeStatusChanged:
		return "状態の変更"
	default:
		return "変更"
	}
}

// authorDisplayName は利用者 ID を表示名へ直す（メンバーに居ない場合は利用者 ID のまま）。
func authorDisplayName(members *projectstore.Members, authorID string) string {
	if m, ok := members.Find(authorID); ok && m.DisplayName != "" {
		return m.DisplayName
	}
	return authorID
}
