package binding

// 本ファイルは予約と作業状況と、同一端末の別ウィンドウのロックのバインディング。メンバー管理画面が使う。
//
// - 予約された対象への着手は**禁止しない**。他メンバーの排他予約がある対象へは、予約者の作業者名・
//   予約日時と「最後に取り込んだ時点の情報」である旨を警告し、確認操作（confirmed）を経れば着手できる
//   （予約は他のメンバーへの知らせであり、機械的に止めない）。着手した場合は自分の名義で並行のエントリを追加する。
// - 他メンバーの予約の解除は編集権限以上（バインディング前段の権限判定）＋確認操作が必要。本人の解除は確認不要。
// - 予約の設定・解除は変更履歴へ reservation-set / reservation-released として記録する。
// - 予約は同期先へ反映して初めて他メンバーに効く。表示は最後に取り込んだ時点の情報であり、
//   その旨を Notice で画面へ渡す。
// - 予約に失効（ハートビート・残留）は無い。放置された予約の判断は利用者に委ねる。

import (
	"fmt"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// reservationTargetLabels は固定の予約対象の日本語表示（固定の対象 ID はこの 6 つで閉じている）。
var reservationTargetLabels = map[string]string{
	projectstore.ReservationDocumentsRequirements: "要件定義書",
	projectstore.ReservationDocumentsBasicDesign:  "基本設計書",
	projectstore.ReservationMembers:               "メンバー一覧",
	projectstore.ReservationRoster:                "ステークホルダー名簿",
	projectstore.ReservationTerms:                 "用語集",
	projectstore.ReservationPerspectives:          "ヒアリング観点",
}

// reservationTargetLabel は対象の表示名。個別レコードは種別名 + 成果物 ID（未知の対象は ID を出さず「その他の対象」）。
func reservationTargetLabel(target string) string {
	if label, ok := reservationTargetLabels[target]; ok {
		return label
	}
	switch {
	case strings.HasPrefix(target, projectstore.ReservationPrefixRequirement):
		return "要件項目 " + strings.TrimPrefix(target, projectstore.ReservationPrefixRequirement)
	case strings.HasPrefix(target, projectstore.ReservationPrefixDecision):
		return "決定事項 " + strings.TrimPrefix(target, projectstore.ReservationPrefixDecision)
	case strings.HasPrefix(target, projectstore.ReservationPrefixOpenIssue):
		return "未決事項 " + strings.TrimPrefix(target, projectstore.ReservationPrefixOpenIssue)
	}
	return "その他の対象"
}

// reservationModeLabels は進め方の日本語表示（値は排他・並行の 2 つで閉じている）。
var reservationModeLabels = map[string]string{
	projectstore.ReservationExclusive:  "排他（1 名で進める）",
	projectstore.ReservationConcurrent: "並行（同時に進める）",
}

func reservationModeLabel(mode string) string {
	if label, ok := reservationModeLabels[mode]; ok {
		return label
	}
	return "進め方不明"
}

// ReservationNotice は一覧に添える注記（予約は反映・取り込みを経て初めて互いに見える）。
const ReservationNotice = "表示は最後に取り込んだ時点の情報です。予約は同期先へ反映して初めて他のメンバーに効き、" +
	"他のメンバーの予約は取り込むまで表示されません。"

// ReservationView は予約・作業状況の 1 件（メンバー管理画面の一覧）。
type ReservationView struct {
	Target      string `json:"target"`
	TargetLabel string `json:"targetLabel"`
	Mode        string `json:"mode"`
	ModeLabel   string `json:"modeLabel"`
	// Author は作業者の表示名、AuthorID は利用者 ID（解除操作の指定に使う）。
	Author    string `json:"author"`
	AuthorID  string `json:"authorId"`
	StartedAt string `json:"startedAt"`
	// Self は自分の予約か（本人の解除は確認不要）。
	Self bool `json:"self"`
}

// ReservationsView は予約・作業状況の一覧と注記。
type ReservationsView struct {
	Items []ReservationView `json:"items"`
	// Notice は「最後に取り込んだ時点の情報」である旨（常に表示する）。
	Notice string `json:"notice"`
}

// ReservationCheckView は着手前の確認結果。
type ReservationCheckView struct {
	// Reserved は他のメンバーが排他で予約しているか。false なら警告なしに着手できる。
	Reserved bool   `json:"reserved"`
	Holder   string `json:"holder,omitempty"`
	HolderID string `json:"holderId,omitempty"`
	// StartedAt は予約日時（ローカル表示）。
	StartedAt string `json:"startedAt,omitempty"`
	// Warning は警告文（原因＋問いかけの 1 文）。Reserved のときのみ。
	Warning string `json:"warning,omitempty"`
}

func reservationView(r projectstore.Reservation, self string) ReservationView {
	return ReservationView{
		Target: r.Target, TargetLabel: reservationTargetLabel(r.Target),
		Mode: r.Mode, ModeLabel: reservationModeLabel(r.Mode),
		Author: r.DisplayName, AuthorID: r.AuthorID,
		StartedAt: r.StartedAt.Local().Format("2006-01-02 15:04"),
		Self:      r.AuthorID == self,
	}
}

// Reservations は未解除の予約・作業状況を返す（閲覧権限でも参照できる）。
func (a *API) Reservations() (ReservationsView, error) {
	s, err := a.current()
	if err != nil {
		return ReservationsView{}, err
	}
	list, err := s.store.LoadReservations()
	if err != nil {
		return ReservationsView{}, err
	}
	self := s.store.Author().AuthorID
	out := ReservationsView{Items: []ReservationView{}, Notice: ReservationNotice}
	for _, r := range list.Active() {
		out.Items = append(out.Items, reservationView(r, self))
	}
	return out, nil
}

// CheckReservation は対象へ着手する前に他のメンバーの排他予約の有無を返す。
func (a *API) CheckReservation(target string) (ReservationCheckView, error) {
	s, err := a.current()
	if err != nil {
		return ReservationCheckView{}, err
	}
	if err := projectstore.ValidateReservationTarget(target); err != nil {
		return ReservationCheckView{}, err
	}
	return a.checkReservation(s, target)
}

func (a *API) checkReservation(s *dialogueSession, target string) (ReservationCheckView, error) {
	list, err := s.store.LoadReservations()
	if err != nil {
		return ReservationCheckView{}, err
	}
	holder, reserved := list.ExclusiveBy(target, s.store.Author().AuthorID)
	if !reserved {
		return ReservationCheckView{}, nil
	}
	startedAt := holder.StartedAt.Local().Format("2006-01-02 15:04")
	return ReservationCheckView{
		Reserved: true, Holder: holder.DisplayName, HolderID: holder.AuthorID, StartedAt: startedAt,
		Warning: fmt.Sprintf(
			"%s（%s）は %s さんが 1 名で進める予定です（%s に予約。最後に取り込んだ時点の情報）。それでも着手しますか。",
			reservationTargetLabel(target), target, holder.DisplayName, startedAt),
	}, nil
}

// StartWork は対象への着手を宣言し、進め方（排他 = 予約 / 並行）を作業コピーへ記録する。
//
// 他のメンバーが排他で予約している対象は、confirmed（警告の表示と確認操作を経たこと）が無ければ
// 警告文つきで拒否する。confirmed なら着手でき、進め方は並行として記録する。
// 予約されていない対象・並行を選んだ対象は警告なしに着手できる。
func (a *API) StartWork(target, mode string, confirmed bool) (ReservationView, error) {
	s, err := a.current()
	if err != nil {
		return ReservationView{}, err
	}
	return a.startWork(s, target, mode, confirmed)
}

// startWork は着手の記録の本体（画面からの StartWork と、成果物操作の前段が共用する）。
func (a *API) startWork(s *dialogueSession, target, mode string, confirmed bool) (ReservationView, error) {
	if err := projectstore.ValidateReservationTarget(target); err != nil {
		return ReservationView{}, err
	}
	if !projectstore.ValidReservationMode(mode) {
		return ReservationView{}, fmt.Errorf("進め方を「排他（1 名で進める）」「並行（同時に進める）」から選んでください。")
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "作業の開始"); err != nil {
		return ReservationView{}, err
	}
	check, err := a.checkReservation(s, target)
	if err != nil {
		return ReservationView{}, err
	}
	overriding := ""
	if check.Reserved {
		if !confirmed {
			// 動作ログ（共同作業の拒否 = 対象・失敗種別・予約者の author_id）。
			// **禁止ではなく確認待ち**であり、確認して着手した場合は変更履歴側に残る。
			a.recordCollabDenied(target, collabKindReserved, check.HolderID)
			return ReservationView{}, fmt.Errorf("%s", check.Warning)
		}
		mode = projectstore.ReservationConcurrent
		overriding = fmt.Sprintf("（%s さんの予約に重ねて着手）", check.Holder)
	}
	set, err := s.store.SetReservation(target, mode)
	if err != nil {
		return ReservationView{}, err
	}
	self := s.store.Author()
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: self.AuthorID,
		Target: target, Change: auditlog.ChangeReservationSet,
		After: fmt.Sprintf("%s: %s%s", self.DisplayName, reservationModeLabel(set.Mode), overriding),
	})
	return reservationView(set, self.AuthorID), nil
}

// ReleaseReservation は予約を解除する。
//
// authorID が空か自分なら本人の解除（確認不要）。他のメンバーの予約は編集権限以上かつ confirmed
// （警告表示と明示の同意）が必要で、無ければ実行しない。いずれも変更履歴へ記録する。
func (a *API) ReleaseReservation(target, authorID string, confirmed bool) error {
	s, err := a.current()
	if err != nil {
		return err
	}
	self := s.store.Author()
	holderID := strings.TrimSpace(authorID)
	if holderID == "" {
		holderID = self.AuthorID
	} else if normalized, err := projectstore.NormalizeAuthorID(holderID); err == nil {
		holderID = normalized
	}
	other := holderID != self.AuthorID
	if other {
		if err := a.requireRole(s.store, projectstore.RoleEditor, "他のメンバーの予約の解除"); err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf(
				"他のメンバーの予約の解除には確認が必要です。予約者と着手日時を確認してから実行してください。")
		}
	}
	released, err := s.store.ReleaseReservation(target, holderID)
	if err != nil {
		return err
	}
	after := "解除（本人）"
	if other {
		after = fmt.Sprintf("解除（%s による）", self.DisplayName)
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: self.AuthorID,
		Target: target, Change: auditlog.ChangeReservationReleased,
		Before: fmt.Sprintf("%s（%s / %s / 着手 %s）", released.DisplayName, released.AuthorID,
			reservationModeLabel(released.Mode), released.StartedAt.Local().Format("2006-01-02 15:04")),
		After: after,
	})
	return nil
}

// ---- 同一端末の別ウィンドウのロック -----------------------------

// windowLockTargetLabels はロック対象の日本語表示（対象 ID はこの 6 つで閉じている）。
var windowLockTargetLabels = map[string]string{
	projectstore.LockRecords:               "共有レコードの反映",
	projectstore.LockMembers:               "メンバー一覧",
	projectstore.LockRoster:                "ステークホルダー名簿",
	projectstore.LockTerms:                 "用語集",
	projectstore.LockDocumentsRequirements: "要件定義書",
	projectstore.LockDocumentsBasicDesign:  "基本設計書",
}

func windowLockTargetLabel(target string) string {
	if label, ok := windowLockTargetLabels[target]; ok {
		return label
	}
	return "その他の対象"
}

// WindowLockView は同一端末の別ウィンドウ（または自分のウィンドウ）が処理中の対象 1 件。
// 同一端末内の事象のため作業者名を持たない。
type WindowLockView struct {
	Target      string `json:"target"`
	TargetLabel string `json:"targetLabel"`
	AcquiredAt  string `json:"acquiredAt,omitempty"`
	// Stale は残留の可能性があるか（解除の候補）。
	Stale bool `json:"stale"`
	// Self はこのウィンドウが保持しているか（解除の対象にしない）。
	Self bool `json:"self"`
}

// WindowLocks は `locks/` の保持状況を返す（閲覧権限でも参照できる）。
func (a *API) WindowLocks() ([]WindowLockView, error) {
	s, err := a.current()
	if err != nil {
		return nil, err
	}
	held, err := s.store.HeldLocks()
	if err != nil {
		return nil, err
	}
	out := make([]WindowLockView, 0, len(held))
	for _, h := range held {
		view := WindowLockView{
			Target: h.Target, TargetLabel: windowLockTargetLabel(h.Target),
			Stale: h.Stale, Self: h.SelfInstance,
		}
		if !h.Holder.AcquiredAt.IsZero() {
			view.AcquiredAt = h.Holder.AcquiredAt.Local().Format("2006-01-02 15:04")
		}
		out = append(out, view)
	}
	return out, nil
}

// ReleaseWindowLock は残留したロックを解除する。
//
// confirmed は画面での確認操作を経たことを示す。false のときは実行しない
// （確認操作なしに解除される経路を持たない）。同一端末内の事象のため変更履歴には記録しない
// （変更履歴にロックの解除を表すイベントの種類が無い）。
func (a *API) ReleaseWindowLock(target string, confirmed bool) error {
	s, err := a.current()
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf(
			"ロックの解除には確認が必要です。別のウィンドウが処理中でないことを確認してから実行してください。")
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "ロックの解除"); err != nil {
		return err
	}
	_, err = s.store.ForceReleaseLock(target)
	return err
}

// ---- 成果物操作の前後の予約 --------------------

// WorkStart は成果物への着手時の進め方の選択（成果物画面の予約選択 UI から渡す）。
//
// 生成・差分再生成・確定・差し戻しの操作開始時に当該成果物種別の予約を確認し、開始時に
// 自分の予約（排他）または並行の宣言を記録して、完了時に解除する。
// **既定値を置かない**（進め方は利用者が選ぶ。空のままでは着手できない）。
type WorkStart struct {
	// Mode は進め方（"exclusive" = 排他 / "concurrent" = 並行）。
	Mode string `json:"mode"`
	// Confirmed は他のメンバーの排他予約に重ねて着手する確認を経たか。
	Confirmed bool `json:"confirmed"`
}

// documentReservationTarget は成果物種別に対応する予約対象を返す。
func documentReservationTarget(kind string) (string, error) {
	switch kind {
	case projectstore.DocKindRequirements:
		return projectstore.ReservationDocumentsRequirements, nil
	case projectstore.DocKindBasicDesign:
		return projectstore.ReservationDocumentsBasicDesign, nil
	}
	return "", fmt.Errorf("成果物の種別が不正です。要件定義書か基本設計書を選んでください。")
}

// documentKindOfPhase は現在フェーズに対応する成果物種別を返す（フェーズは要件定義・基本設計の 2 つで閉じている）。
// フェーズと成果物種別を同じ文字列として扱わない（別の値集合であり、いずれかが変わっても壊れないようにする）。
func documentKindOfPhase(phase string) (string, error) {
	switch phase {
	case projectstore.PhaseRequirements:
		return projectstore.DocKindRequirements, nil
	case projectstore.PhaseBasicDesign:
		return projectstore.DocKindBasicDesign, nil
	}
	return "", fmt.Errorf("現在のフェーズを判別できません。プロジェクトを開き直してください。")
}

// CheckDocumentReservation は成果物種別への着手前の確認結果を返す（成果物画面の予約選択 UI 用）。
func (a *API) CheckDocumentReservation(kind string) (ReservationCheckView, error) {
	s, err := a.current()
	if err != nil {
		return ReservationCheckView{}, err
	}
	target, err := documentReservationTarget(kind)
	if err != nil {
		return ReservationCheckView{}, err
	}
	return a.checkReservation(s, target)
}

// CheckPhaseDocumentReservation は**現在フェーズ**の成果物種別への着手前の確認結果を返す。
//
// 差し戻しの予約選択 UI が使う。画面がフェーズと成果物種別の対応を持たないようにする。
func (a *API) CheckPhaseDocumentReservation() (ReservationCheckView, error) {
	s, err := a.current()
	if err != nil {
		return ReservationCheckView{}, err
	}
	kind, err := documentKindOfPhase(s.store.Project().Phase)
	if err != nil {
		return ReservationCheckView{}, err
	}
	target, err := documentReservationTarget(kind)
	if err != nil {
		return ReservationCheckView{}, err
	}
	return a.checkReservation(s, target)
}

// beginDocumentWork は成果物種別の予約を記録し、完了時に解除する後始末を返す。
//
// 他のメンバーが排他で予約している対象は、work.Confirmed が無ければ警告文つきで拒否する
// （機械的に禁止はしない。予約は他のメンバーへの知らせ）。解除は成功・失敗にかかわらず呼び出し側が defer で行う。
func (a *API) beginDocumentWork(s *dialogueSession, kind string, work WorkStart) (func(), error) {
	target, err := documentReservationTarget(kind)
	if err != nil {
		return nil, err
	}
	if _, err := a.startWork(s, target, strings.TrimSpace(work.Mode), work.Confirmed); err != nil {
		return nil, err
	}
	return func() {
		// 解除できなくても操作の結果は返す（予約は失効を持たず、画面から解除できる）。
		_ = a.releaseOwnReservation(s, target)
	}, nil
}

// releaseOwnReservation は自分の予約を解除して変更履歴へ記録する（ReleaseReservation の本人経路）。
func (a *API) releaseOwnReservation(s *dialogueSession, target string) error {
	self := s.store.Author()
	released, err := s.store.ReleaseReservation(target, self.AuthorID)
	if err != nil {
		return err
	}
	a.recordChange(s, auditlog.ChangeRecord{
		At: time.Now().UTC(), Author: self.AuthorID,
		Target: target, Change: auditlog.ChangeReservationReleased,
		Before: fmt.Sprintf("%s（%s / %s / 着手 %s）", released.DisplayName, released.AuthorID,
			reservationModeLabel(released.Mode), released.StartedAt.Local().Format("2006-01-02 15:04")),
		After: "解除（本人）",
	})
	return nil
}

// WorkModeOption は進め方の選択肢（成果物画面の予約選択 UI）。
//
// 画面にラベル・説明を二重に持たせない（値集合とラベルの正本はこのファイル）。
type WorkModeOption struct {
	Mode  string `json:"mode"`
	Label string `json:"label"`
	// Hint は選択の意味（予約は反映して初めて効く旨を含む）。
	Hint string `json:"hint"`
}

// workModeOptions は進め方の選択肢（**既定の選択を置かない**。着手のたびに選ぶ）。
var workModeOptions = []WorkModeOption{
	{Mode: projectstore.ReservationExclusive, Label: reservationModeLabel(projectstore.ReservationExclusive),
		Hint: "この対象は自分だけで進める予定であることを他のメンバーへ知らせます。" +
			"予約は同期先へ反映して初めて他のメンバーに効きます（他のメンバーの着手を止めるものではありません）。"},
	{Mode: projectstore.ReservationConcurrent, Label: reservationModeLabel(projectstore.ReservationConcurrent),
		Hint: "他のメンバーと同時に進めます。取り込み時に同じ対象が変更されていれば内容の確認（三面マージ）を行います。"},
}

// WorkModeOptions は進め方の選択肢を返す（成果物画面の予約選択 UI が使う）。
func (a *API) WorkModeOptions() []WorkModeOption { return workModeOptions }
