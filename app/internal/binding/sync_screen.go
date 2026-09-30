package binding

// 本ファイルは同期パネルと同期先の設定のバインディング。
// 認証情報は sync_credentials.go、三面マージは sync_merge_screen.go。
//
// 規約:
//   - **git の語・生のエラー文言を画面へ出さない**（利用者は git を知らない前提で使う）。失敗は sync.Failure の
//     Message（原因＋次の行動＋作業コピーが変わらない旨）をそのまま渡し、Detail は折りたたみ用。
//   - 権限判定はバインディング前段で行う（画面ごとに判定を分散させない）。反映は編集権限以上、同期先の設定はオーナー
//     （閲覧権限では**無効化＋理由表示**であって、操作を隠さない）。
//   - 同期先へ到達できないことを常時の警告にしない（作業を妨げない）。

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/applog"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// SyncSummaryItemView は変更の区分ごとの件数 1 行（反映の事前提示・取り込みの結果）。
type SyncSummaryItemView struct {
	CategoryLabel string `json:"categoryLabel"`
	Added         int    `json:"added"`
	Modified      int    `json:"modified"`
	Removed       int    `json:"removed"`
	Total         int    `json:"total"`
}

// syncSummaryItems は要約を表示順（区分の並び）で writes する。
func syncSummaryItems(s syncmod.Summary) []SyncSummaryItemView {
	out := make([]SyncSummaryItemView, 0, len(s))
	for _, cat := range s.Categories() {
		c := s[cat]
		out = append(out, SyncSummaryItemView{CategoryLabel: syncmod.CategoryLabel(cat),
			Added: c.Added, Modified: c.Modified, Removed: c.Removed, Total: c.Total()})
	}
	return out
}

// SyncFailureView は同期の失敗の画面表示。
type SyncFailureView struct {
	// KindLabel は失敗種別の表示名（生のコード値を出さない）。
	KindLabel string `json:"kindLabel"`
	// Message は原因＋次に取る行動＋作業コピーが変わらない旨の 1 文。
	Message string `json:"message"`
	// Detail は折りたたみ表示用の詳細（マスキング済み。空のこともある）。
	Detail string `json:"detail,omitempty"`
}

// syncFailureView は error を画面表示用の失敗へ写す（同期の失敗でなければ ok = false）。
func syncFailureView(err error) (SyncFailureView, bool) {
	var f *syncmod.Failure
	if !errors.As(err, &f) {
		return SyncFailureView{}, false
	}
	return SyncFailureView{KindLabel: f.Kind.Label(), Message: f.Message, Detail: f.Detail}, true
}

// syncFailure は同期の失敗を画面表示用へ写し、あわせて動作ログへ残す。
//
// 同期の失敗が画面へ出る経路はすべてここを通るため、記録の入口が 1 か所に閉じる。
// 記録するのは失敗種別・操作・提示文言までで、**同期先の所在・認証情報は載せない**
// （ログから所在や鍵が漏れないように）。**Detail（詳細）も記録しない**: 同期モジュールで
// マスキング済みだが、共有フォルダの絶対パスなど**端末固有情報**（利用者のホームフォルダ配下の
// パス等）が混じりうるため（動作ログに端末固有情報を残さない）。詳細は画面の折りたたみ表示で
// 利用者が確認する。
func (a *API) syncFailure(err error) (SyncFailureView, bool) {
	view, ok := syncFailureView(err)
	if !ok {
		return view, false
	}
	var f *syncmod.Failure
	errors.As(err, &f)
	a.log.Error("sync.failed", view.Message,
		applog.F("kind", string(f.Kind)), applog.F("op", string(f.Op)))
	return view, true
}

// ---- 同期パネル ----------------------------------------------------------------

// SyncStatusView は同期パネルの常時表示。
type SyncStatusView struct {
	// Configured は同期先が設定されているか（false = 単独利用のローカルプロジェクト）。
	Configured bool `json:"configured"`
	// KindLabel / Location は同期先の表示（所在は資格情報部を含まない）。
	KindLabel string `json:"kindLabel,omitempty"`
	Location  string `json:"location,omitempty"`
	// Available は同期を実行できるか（git が使えるか）。
	Available bool `json:"available"`
	// UnavailableReason は実行できない理由と次の行動（Available が真なら空）。
	UnavailableReason string `json:"unavailableReason,omitempty"`
	// CanIncorporate / CanPublish は権限による可否（閲覧権限は取り込みのみ）。
	CanIncorporate bool   `json:"canIncorporate"`
	CanPublish     bool   `json:"canPublish"`
	PublishReason  string `json:"publishReason,omitempty"`
	// LastSyncedAt は最後に同期した日時（記録の最新。空 = まだ同期していない）。
	LastSyncedAt string `json:"lastSyncedAt,omitempty"`
	// LastIncorporationAt は最後に取り込んだ日時（変更要約の起点の目安）。
	LastIncorporationAt string `json:"lastIncorporationAt,omitempty"`
	// HasUnpublished は同期先へまだ載せていない変更があるか。
	HasUnpublished bool `json:"hasUnpublished"`
	// UnpublishedSummary は未反映の変更の 1 行（「要件項目 3 / 決定事項 1」）。
	UnpublishedSummary string `json:"unpublishedSummary,omitempty"`
	// CredentialRequired は同期先が認証情報を要するか（共有フォルダは不要）。
	CredentialRequired bool `json:"credentialRequired"`
	// CredentialRegistered は認証情報が登録済みか（未登録なら認証情報の画面へ誘導する）。
	CredentialRegistered bool `json:"credentialRegistered"`
	// NeedsReattach はバックアップから復元した作業コピーで、同期先からの取り直しが要るか
	// （自動退避の zip には同期の管理情報を含めないため）。
	NeedsReattach bool `json:"needsReattach"`
	// RestorePending は取り直しの直後で、反映に利用者の確認が要るか。
	RestorePending bool `json:"restorePending"`
	// Notice は同期先未設定などの案内（原因＋次の行動の 1 文）。
	Notice string `json:"notice,omitempty"`
}

// SyncStatus は同期パネルの表示内容を返す（同期先へは接続しない。閲覧権限でも参照できる）。
func (a *API) SyncStatus() (SyncStatusView, error) {
	s, err := a.current()
	if err != nil {
		return SyncStatusView{}, err
	}
	out := SyncStatusView{}
	remote, configured := syncmod.RemoteFromProject(s.store.Project())
	out.Configured = configured
	if !configured {
		out.Notice = "同期先が設定されていません。オーナーが同期先を設定すると、他のメンバーと成果物を共有できます。"
		return out, nil
	}
	out.KindLabel = projectstore.SyncKindLabel(remote.Kind)
	out.Location = remote.Display()
	out.CredentialRequired = remote.RequiresCredential()

	client, err := a.syncClient(s)
	if err != nil {
		return SyncStatusView{}, err
	}
	availability := client.Availability()
	out.Available = availability.Available
	out.UnavailableReason = availability.Reason

	// 権限（閲覧権限では反映を無効化し理由を示す）
	if err := a.requireRole(s.store, projectstore.RoleEditor, "同期先への反映"); err != nil {
		out.PublishReason = err.Error()
	} else {
		out.CanPublish = availability.Available
	}
	out.CanIncorporate = availability.Available

	if out.CredentialRequired {
		if view, err := a.SyncCredentialView(s.store.Project().ProjectID); err == nil {
			out.CredentialRegistered = view.Registered
		}
	}

	log, err := a.SyncLog()
	if err != nil {
		return SyncStatusView{}, err
	}
	if len(log.Entries) > 0 {
		out.LastSyncedAt = log.Entries[0].At
	}
	out.LastIncorporationAt = log.LastIncorporation

	state, err := client.PublishStateOf(a.context(), s.store.Root())
	if err == nil {
		out.HasUnpublished = state.HasUnpublished
		if state.HasUnpublished {
			out.UnpublishedSummary = state.Summary.Describe()
		}
	}
	// 復元した作業コピーの状態（同期先へは接続しない）
	out.NeedsReattach = syncmod.NeedsReattach(s.store.Root())
	if pending, err := client.RestorePending(a.context(), s.store.Root()); err == nil {
		out.RestorePending = pending
	}

	switch {
	case !out.Available:
		out.Notice = availability.Reason
	case out.CredentialRequired && !out.CredentialRegistered:
		out.Notice = "この同期先には認証情報の登録が必要です。設定で登録してください。"
	case out.NeedsReattach:
		out.Notice = "この作業コピーはバックアップから復元されています。同期先から取り直してから、取り込み・反映を行ってください。"
	case out.RestorePending:
		out.Notice = "復元した内容がまだ同期先へ載っていません。反映する内容を確認してから反映してください。"
	}
	return out, nil
}

// ---- 取り直し ------------------------------------------------------------------

// SyncReattachView は取り直しの結果。
type SyncReattachView struct {
	Done bool `json:"done"`
	// Items / Summary は取り直しのあとに反映すると載る内容（復元した内容と同期先の差）。
	Items   []SyncSummaryItemView `json:"items,omitempty"`
	Summary string                `json:"summary,omitempty"`
	// RestorePending は反映に利用者の確認が要るか（取り直した直後は真）。
	RestorePending bool             `json:"restorePending"`
	Failure        *SyncFailureView `json:"failure,omitempty"`
	Notice         string           `json:"notice"`
}

// ReattachSync は復元した作業コピーの管理情報を同期先から取り直す。
//
// 同期先からの受信のみで**同期先へは書かない**ため、閲覧権限でも実行できる（取り込みと同じ扱い）。
// 復元した内容は「未反映の変更」として残り、反映は利用者の確認を経て別操作で行う。
func (a *API) ReattachSync() (SyncReattachView, error) {
	s, err := a.current()
	if err != nil {
		return SyncReattachView{}, err
	}
	remote, configured := syncmod.RemoteFromProject(s.store.Project())
	if !configured {
		return SyncReattachView{}, errors.New(
			"同期先が設定されていません。オーナーに同期先の設定を依頼してください。")
	}
	client, err := a.syncClient(s)
	if err != nil {
		return SyncReattachView{}, err
	}
	result, err := client.Reattach(a.context(), s.store.Root(), remote, s.store.Project().ProjectID)
	if err != nil {
		if failure, ok := a.syncFailure(err); ok {
			return SyncReattachView{Failure: &failure, Notice: failure.Message}, nil
		}
		return SyncReattachView{}, err
	}
	out := SyncReattachView{Done: true, Items: syncSummaryItems(result.Summary),
		Summary: result.Summary.Describe(), RestorePending: result.RestorePending}
	switch {
	case !result.Reconstructed:
		out.Notice = "同期先にはまだ反映された内容がありません。オーナーが最初の反映を行うと共有できます。"
	case result.Summary.Total() == 0:
		out.Notice = "同期先から取り直しました。復元した内容は同期先と同じで、反映する変更はありません。"
	default:
		out.Notice = fmt.Sprintf(
			"同期先から取り直しました。復元した内容には同期先と異なる変更があります（%s）。反映する内容を確認してから反映してください。",
			out.Summary)
	}
	return out, nil
}

// ---- 取り込み ------------------------------------------------------------------

// SyncIncorporateView は取り込みの結果（同期パネルから変更要約・三面マージの画面へつなぐ）。
type SyncIncorporateView struct {
	// Done は取り込みが完了したか（false = 競合の承認待ち、または失敗）。
	Done bool `json:"done"`
	// Items は取り込みで作業コピーへ入った変更の区分ごとの件数。
	Items []SyncSummaryItemView `json:"items,omitempty"`
	// Summary は上記の 1 行表記（変更が無ければ「変更なし」）。
	Summary string `json:"summary,omitempty"`
	// Conflicts は本人の承認が要る競合（三面マージの画面の材料）。空でなければ Done は false。
	Conflicts []SyncConflictView `json:"conflicts,omitempty"`
	// ConflictCount は今回の取り込みで解決した競合の件数（完了時）。
	ConflictCount int `json:"conflictCount,omitempty"`
	// Failure は失敗の提示（成功・承認待ちのときは nil）。
	Failure *SyncFailureView `json:"failure,omitempty"`
	// Notice は結果の案内（原因＋次の行動の 1 文）。
	Notice string `json:"notice"`
}

// IncorporateSync は同期先の他メンバーの変更を取り込む。
//
// resolutions は三面マージの承認（競合ごとの選択）。**初回は空で呼ぶ**。競合があれば統合せずに
// 中止して（作業コピーは取り込み前の状態のまま）競合を返すので、画面で承認を得て
// 同じ承認を添えて再実行する。承認が 1 件でも欠ければ再び中止する（既定の選択を置かない）。
//
// 完了時は派生インデックスを差分再構築し、画面は続けて ChangeSummary（他メンバーの変更の要約）を表示する。
func (a *API) IncorporateSync(resolutions []SyncResolutionInput) (SyncIncorporateView, error) {
	s, err := a.current()
	if err != nil {
		return SyncIncorporateView{}, err
	}
	remote, configured := syncmod.RemoteFromProject(s.store.Project())
	if !configured {
		return SyncIncorporateView{}, errors.New(
			"同期先が設定されていません。オーナーに同期先の設定を依頼してください。")
	}
	resolver := newSyncResolver(resolutions)
	client, err := a.syncClientResolving(s, resolver)
	if err != nil {
		return SyncIncorporateView{}, err
	}
	result, err := client.Incorporate(a.context(), s.store.Root(), remote, s.store.Project().ProjectID)
	if err != nil {
		return a.syncIncorporateFailure(s, resolver, err)
	}
	// 取り込みで変更されたファイルを派生インデックスへ反映する。
	a.applyIncorporationToIndex(s, result.Incoming)

	out := SyncIncorporateView{Done: true, Items: syncSummaryItems(result.Summary),
		Summary: result.Summary.Describe(), ConflictCount: result.Conflicts}
	switch {
	case len(result.Integrated) == 0:
		out.Notice = "取り込む変更はありませんでした。"
	case result.Conflicts > 0:
		out.Notice = fmt.Sprintf("取り込みが完了しました（%s）。%d 件の競合を承認どおりに統合しました。",
			out.Summary, result.Conflicts)
	default:
		out.Notice = fmt.Sprintf("取り込みが完了しました（%s）。", out.Summary)
	}
	return out, nil
}

// syncIncorporateFailure は取り込みの失敗を結果へ写す。
//
// 競合の承認待ち（提示された競合がある）は**エラーではなく結果**として返し、画面が三面を表示する。
func (a *API) syncIncorporateFailure(s *dialogueSession, resolver *syncResolver, err error) (SyncIncorporateView, error) {
	if len(resolver.collected) > 0 {
		members, mErr := s.store.LoadMembers()
		if mErr != nil {
			return SyncIncorporateView{}, mErr
		}
		return SyncIncorporateView{
			Conflicts: resolver.views(members),
			Notice: "他のメンバーと同じ対象が変更されています。取り込みは行っていません" +
				"（作業コピーの内容は変わっていません）。内容を確認して、対象ごとに扱いを選んでください。",
		}, nil
	}
	if failure, ok := a.syncFailure(err); ok {
		return SyncIncorporateView{Failure: &failure, Notice: failure.Message}, nil
	}
	return SyncIncorporateView{}, err
}

// ---- 反映 ----------------------------------------------------------------------

// SyncPublishPreviewView は反映の事前提示（同期先へ何が載るかを反映の前に示す）。
type SyncPublishPreviewView struct {
	KindLabel string                `json:"kindLabel"`
	Location  string                `json:"location"`
	Items     []SyncSummaryItemView `json:"items,omitempty"`
	Total     int                   `json:"total"`
	Summary   string                `json:"summary"`
	// FirstPublish はこの作業コピーからの初回の反映か。
	FirstPublish bool `json:"firstPublish"`
	// NeedsReattach は同期先からの取り直しが先に要る（復元した作業コピー）。
	NeedsReattach bool `json:"needsReattach"`
	// RestorePending は復元した内容を載せようとしている（反映に利用者の確認が要る）。
	RestorePending bool `json:"restorePending"`
	// Failure は事前提示に失敗した場合（同期先へは接続しないため通常は nil）。
	Failure *SyncFailureView `json:"failure,omitempty"`
	Notice  string           `json:"notice"`
}

// PreviewSyncPublish は「何が同期先へ載るか」を返す（同期先へは接続しない）。
//
// 反映はこの提示と確認操作を経てから実行する（確認なしに反映する経路を作らない）。
func (a *API) PreviewSyncPublish() (SyncPublishPreviewView, error) {
	s, err := a.current()
	if err != nil {
		return SyncPublishPreviewView{}, err
	}
	remote, configured := syncmod.RemoteFromProject(s.store.Project())
	if !configured {
		return SyncPublishPreviewView{}, errors.New(
			"同期先が設定されていません。オーナーに同期先の設定を依頼してください。")
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "同期先への反映"); err != nil {
		return SyncPublishPreviewView{}, err
	}
	client, err := a.syncClient(s)
	if err != nil {
		return SyncPublishPreviewView{}, err
	}
	preview, err := client.PreviewPublish(a.context(), s.store.Root(), remote)
	if err != nil {
		if failure, ok := a.syncFailure(err); ok {
			return SyncPublishPreviewView{Failure: &failure, Notice: failure.Message}, nil
		}
		return SyncPublishPreviewView{}, err
	}
	out := SyncPublishPreviewView{
		KindLabel: projectstore.SyncKindLabel(preview.RemoteKind), Location: preview.RemoteLocation,
		Items: syncSummaryItems(preview.Summary), Total: preview.Summary.Total(),
		Summary: preview.Summary.Describe(), FirstPublish: preview.FirstPublish,
		NeedsReattach: preview.NeedsReattach, RestorePending: preview.RestorePending,
	}
	switch {
	case out.NeedsReattach:
		out.Notice = "この作業コピーはバックアップから復元されています。先に同期先から取り直すと、載せる内容が分かります。"
	case out.Total == 0:
		out.Notice = "同期先へ載せる変更はありません。"
	case out.RestorePending:
		out.Notice = fmt.Sprintf(
			"バックアップから復元した内容を %s（%s）へ載せます。他のメンバーの成果より古い内容でないかを確かめてから反映してください。",
			out.KindLabel, out.Location)
	case out.FirstPublish:
		out.Notice = fmt.Sprintf("次の内容を %s（%s）へ初めて載せます。よろしければ反映してください。",
			out.KindLabel, out.Location)
	default:
		out.Notice = fmt.Sprintf("次の内容を %s（%s）へ載せます。よろしければ反映してください。",
			out.KindLabel, out.Location)
	}
	return out, nil
}

// SyncPublishView は反映の結果。
type SyncPublishView struct {
	Done    bool                  `json:"done"`
	Items   []SyncSummaryItemView `json:"items,omitempty"`
	Summary string                `json:"summary,omitempty"`
	Failure *SyncFailureView      `json:"failure,omitempty"`
	Notice  string                `json:"notice"`
}

// PublishSync は自分の変更を同期先へ反映する（編集権限以上）。
//
// 共有フォルダの同期先がまだ無い場合は、**オーナーの反映のときだけ**作る。
//
// acknowledgeRestore は「バックアップから復元した内容を載せてよい」という利用者の確認。
// 復元直後（SyncStatusView.RestorePending が真）はこの確認が無いと同期先へ書かない。画面は事前提示（PreviewSyncPublish）で内容を示してから真で呼ぶ。
func (a *API) PublishSync(acknowledgeRestore bool) (SyncPublishView, error) {
	s, err := a.current()
	if err != nil {
		return SyncPublishView{}, err
	}
	remote, configured := syncmod.RemoteFromProject(s.store.Project())
	if !configured {
		return SyncPublishView{}, errors.New(
			"同期先が設定されていません。オーナーに同期先の設定を依頼してください。")
	}
	if err := a.requireRole(s.store, projectstore.RoleEditor, "同期先への反映"); err != nil {
		return SyncPublishView{}, err
	}
	createIfAbsent := a.requireRole(s.store, projectstore.RoleOwner, "同期先の作成") == nil
	client, err := a.syncClient(s)
	if err != nil {
		return SyncPublishView{}, err
	}
	result, err := client.Publish(a.context(), s.store.Root(), remote, s.store.Project().ProjectID,
		syncmod.PublishOptions{CreateIfAbsent: createIfAbsent, AcknowledgeRestore: acknowledgeRestore})
	if err != nil {
		if failure, ok := a.syncFailure(err); ok {
			return SyncPublishView{Failure: &failure, Notice: failure.Message}, nil
		}
		return SyncPublishView{}, err
	}
	out := SyncPublishView{Done: true, Items: syncSummaryItems(result.Summary),
		Summary: result.Summary.Describe()}
	if result.NothingToPublish {
		out.Notice = "同期先は既に最新です。載せる変更はありませんでした。"
	} else {
		out.Notice = fmt.Sprintf("同期先へ反映しました（%s）。", out.Summary)
	}
	return out, nil
}

// ---- 同期先の設定 --------------------------------------------------------------

// SyncKindOption は同期先の種別の選択肢。
type SyncKindOption struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Hint は所在の入力形式の説明。
	Hint string `json:"hint"`
	// RequiresConsent は保存前に明示同意が要るか（外部 Git サーバ）。
	RequiresConsent bool `json:"requiresConsent"`
	// ConsentText は同意を求める文（RequiresConsent が真のときのみ）。
	ConsentText string `json:"consentText,omitempty"`
	// RequiresCredential は認証情報の登録が要るか（共有フォルダは不要）。
	RequiresCredential bool `json:"requiresCredential"`
}

// externalConsentText は外部 Git サーバを選んだときの明示表示（保管先の判断と責任が利用組織にあることを示す）。
const externalConsentText = "外部 Git サーバを同期先にすると、要件定義データが利用組織の外部が運用する" +
	"ホストへ保管されます。保管先の選択と、その適否の判断・責任は利用組織が負います。この内容に同意する場合だけ保存できます。"

// syncKindOptions は種別の選択肢（既定は共有フォルダ）。
var syncKindOptions = []SyncKindOption{
	{Kind: projectstore.SyncKindFolder, Label: projectstore.SyncKindLabel(projectstore.SyncKindFolder),
		Hint: "共有フォルダ上のフォルダのパスを指定します（例: /Volumes/share/reqweave/案件A.sync）。"},
	{Kind: projectstore.SyncKindGitInternal, Label: projectstore.SyncKindLabel(projectstore.SyncKindGitInternal),
		Hint:               "https:// または ssh:// の URL、あるいは user@host:path 形式で指定します。",
		RequiresCredential: true},
	{Kind: projectstore.SyncKindGitExternal, Label: projectstore.SyncKindLabel(projectstore.SyncKindGitExternal),
		Hint:            "https:// または ssh:// の URL、あるいは user@host:path 形式で指定します。",
		RequiresConsent: true, ConsentText: externalConsentText, RequiresCredential: true},
}

// SyncKindOptions は同期先の種別の選択肢を返す（プロジェクトを開いていなくても使える）。
//
// 同期先から取得して参加する画面（プロジェクト一覧から開く）が、まだ作業コピーが無い状態で使う。
func (a *API) SyncKindOptions() []SyncKindOption { return syncKindOptions }

// SyncRemoteView は同期先の設定画面の表示内容。
type SyncRemoteView struct {
	Configured bool   `json:"configured"`
	Kind       string `json:"kind,omitempty"`
	KindLabel  string `json:"kindLabel,omitempty"`
	Location   string `json:"location,omitempty"`
	// Kinds は選択肢（画面にラベル・同意文を二重に持たせない）。
	Kinds []SyncKindOption `json:"kinds"`
	// CanManage はオーナー権限か（編集・閲覧権限では無効化＋理由表示）。
	CanManage    bool   `json:"canManage"`
	ManageReason string `json:"manageReason,omitempty"`
	// Credential は認証情報の状態（認証情報の画面への導線に使う。値は含まない）。
	Credential SyncCredentialView `json:"credential"`
	Notice     string             `json:"notice,omitempty"`
}

// SyncRemote は同期先の設定を返す（閲覧権限でも参照できる。値の変更はオーナーのみ）。
func (a *API) SyncRemote() (SyncRemoteView, error) {
	s, err := a.current()
	if err != nil {
		return SyncRemoteView{}, err
	}
	out := SyncRemoteView{Kinds: syncKindOptions, Kind: projectstore.SyncKindFolder}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "同期先の設定"); err != nil {
		out.ManageReason = err.Error()
	} else {
		out.CanManage = true
	}
	if remote, configured := syncmod.RemoteFromProject(s.store.Project()); configured {
		out.Configured = true
		out.Kind = remote.Kind
		out.KindLabel = projectstore.SyncKindLabel(remote.Kind)
		out.Location = remote.Display()
	} else {
		out.Notice = "同期先はまだ設定されていません。設定するまでは、このプロジェクトはこの端末の中だけで扱われます。"
	}
	credential, err := a.SyncCredentialView(s.store.Project().ProjectID)
	if err != nil {
		return SyncRemoteView{}, err
	}
	out.Credential = credential
	return out, nil
}

// SetSyncRemoteRequest は同期先の設定の入力。
type SetSyncRemoteRequest struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	// ExternalConsent は外部 Git サーバを選んだときの明示同意（同意なしには保存しない）。
	ExternalConsent bool `json:"externalConsent"`
}

// SetSyncRemote は同期先を設定する（**オーナーのみ**）。
//
// 所在の形式は同期モジュールが検証する（資格情報を含む URL・平文 http は受け付けない）。
// 同期先はプロジェクト単位で全メンバー共通のため、反映して初めて他メンバーへ及ぶ。
func (a *API) SetSyncRemote(req SetSyncRemoteRequest) (SyncRemoteView, error) {
	s, err := a.current()
	if err != nil {
		return SyncRemoteView{}, err
	}
	if err := a.requireRole(s.store, projectstore.RoleOwner, "同期先の設定"); err != nil {
		return SyncRemoteView{}, err
	}
	kind := strings.TrimSpace(req.Kind)
	option, ok := syncKindOption(kind)
	if !ok {
		return SyncRemoteView{}, errors.New("同期先の種別を一覧から選んでください。")
	}
	if option.RequiresConsent && !req.ExternalConsent {
		return SyncRemoteView{}, errors.New(option.ConsentText)
	}
	location := strings.TrimSpace(req.Location)
	remote := syncmod.Remote{Kind: kind, Location: location}
	if err := remote.Validate(); err != nil {
		return SyncRemoteView{}, err
	}
	setting := &projectstore.SyncSetting{Kind: kind, Location: location}
	if err := setting.Validate(); err != nil {
		return SyncRemoteView{}, err
	}
	if err := s.store.UpdateProject(func(p *projectstore.Project) error {
		p.Sync = setting
		return nil
	}); err != nil {
		return SyncRemoteView{}, err
	}
	return a.SyncRemote()
}

// syncKindOption は種別の選択肢を引く。
func syncKindOption(kind string) (SyncKindOption, bool) {
	for _, o := range syncKindOptions {
		if o.Kind == kind {
			return o, true
		}
	}
	return SyncKindOption{}, false
}

// ---- 接続確認（認証情報の画面・同期先の設定画面から使う） ----------------------

// SyncCheckView は接続確認の結果（認証エラー / 到達不能 / その他を区別する）。
type SyncCheckView struct {
	OK      bool             `json:"ok"`
	Failure *SyncFailureView `json:"failure,omitempty"`
	Notice  string           `json:"notice"`
}

// CheckSyncConnection は同期先へ到達・認証できるかを確認する（内容は変えない・記録に残さない）。
//
// kind / location が空なら設定済みの同期先を使う（同期先の設定画面は保存前の所在を渡せる）。
// 認証情報は**登録済みのものだけ**を使う（値をバインディングの引数で受け取らない）。
func (a *API) CheckSyncConnection(kind, location string) (SyncCheckView, error) {
	s, err := a.current()
	if err != nil {
		return SyncCheckView{}, err
	}
	remote := syncmod.Remote{Kind: strings.TrimSpace(kind), Location: strings.TrimSpace(location)}
	if remote.Kind == "" && remote.Location == "" {
		saved, configured := syncmod.RemoteFromProject(s.store.Project())
		if !configured {
			return SyncCheckView{}, errors.New("同期先が設定されていません。先に同期先を設定してください。")
		}
		remote = saved
	}
	client, err := a.syncClient(s)
	if err != nil {
		return SyncCheckView{}, err
	}
	if err := client.CheckConnection(a.context(), remote, s.store.Project().ProjectID, nil); err != nil {
		if failure, ok := a.syncFailure(err); ok {
			return SyncCheckView{Failure: &failure, Notice: failure.Message}, nil
		}
		return SyncCheckView{}, err
	}
	return SyncCheckView{OK: true, Notice: "同期先へ接続できました。"}, nil
}

// ---- 同期先から取得して参加 ----------------------------------------------------

// CloneSyncProjectRequest は取得（参加）の入力。
//
// 取得の時点ではプロジェクト ID が分からず、登録済みの認証情報を参照名で引けないため、
// **この操作に限り**認証情報の値を受け取る。値は同期モジュールへ渡すだけで戻り値・記録に載せず、
// 取得の成功後に取得したプロジェクト ID で OS セキュアストレージへ登録する。
type CloneSyncProjectRequest struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	// Dest は作業コピーの作成先（存在しないフォルダ）。
	Dest string `json:"dest"`
	// CredentialKind / Username / Secret は同期先が認証を要する場合の入力（認証情報の画面と同じ様式）。
	CredentialKind string `json:"credentialKind,omitempty"`
	Username       string `json:"username,omitempty"`
	Secret         string `json:"secret,omitempty"`
	// ExternalConsent は外部 Git サーバから取得するときの明示同意。
	ExternalConsent bool `json:"externalConsent"`
}

// CloneSyncProjectView は取得の結果。
type CloneSyncProjectView struct {
	Done bool `json:"done"`
	// Project は一覧へ加えた作業コピー（Done のときのみ）。
	Project *ProjectSummary  `json:"project,omitempty"`
	Failure *SyncFailureView `json:"failure,omitempty"`
	Notice  string           `json:"notice"`
}

// CloneSyncProject は同期先から作業コピーを取得して一覧へ加える。
//
// 失敗したときは**中途半端な作業コピーを一覧へ加えない**（同期モジュールが一時領域で行い、
// 成功したときだけ作成先へ移す）。
func (a *API) CloneSyncProject(req CloneSyncProjectRequest) (CloneSyncProjectView, error) {
	option, ok := syncKindOption(strings.TrimSpace(req.Kind))
	if !ok {
		return CloneSyncProjectView{}, errors.New("同期先の種別を一覧から選んでください。")
	}
	if option.RequiresConsent && !req.ExternalConsent {
		return CloneSyncProjectView{}, errors.New(option.ConsentText)
	}
	dest := strings.TrimSpace(req.Dest)
	if dest == "" {
		return CloneSyncProjectView{}, errors.New("作業コピーの作成先フォルダを選んでください。")
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return CloneSyncProjectView{}, errors.New(
			"作成先フォルダの場所を特定できません。別のフォルダを選び直してください。")
	}
	remote := syncmod.Remote{Kind: option.Kind, Location: strings.TrimSpace(req.Location)}
	if err := remote.Validate(); err != nil {
		return CloneSyncProjectView{}, err
	}
	credential, err := cloneCredential(remote, req)
	if err != nil {
		return CloneSyncProjectView{}, err
	}
	settings, err := a.settings()
	if err != nil {
		return CloneSyncProjectView{}, err
	}
	client, err := a.syncCloneClient(nil)
	if err != nil {
		return CloneSyncProjectView{}, err
	}
	result, err := client.Clone(a.context(), syncmod.CloneOptions{
		Remote: remote, Dest: abs, Credential: credential,
		KnownProject: a.knownProjectLookup(settings),
	})
	if err != nil {
		if failure, ok := a.syncFailure(err); ok {
			return CloneSyncProjectView{Failure: &failure, Notice: failure.Message}, nil
		}
		return CloneSyncProjectView{}, err
	}
	// 取得できたら、入力された認証情報を取得したプロジェクト ID で登録する（以後は参照名で引く）。
	if credential != nil {
		if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{
			ProjectID: result.ProjectID, Kind: string(credential.Kind),
			Username: credential.Username, Secret: credential.Secret,
		}); err != nil {
			return CloneSyncProjectView{}, fmt.Errorf(
				"作業コピーは取得できましたが、認証情報を保存できませんでした。設定で登録し直してください: %w", err)
		}
	}
	settings, err = a.settings()
	if err != nil {
		return CloneSyncProjectView{}, err
	}
	settings.AddRecentProject(result.Root, 0)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		return CloneSyncProjectView{}, err
	}
	author, _ := settings.Author()
	summary := a.summarize(result.Root, author)
	return CloneSyncProjectView{Done: true, Project: &summary,
		Notice: fmt.Sprintf("「%s」を取得しました（%s）。一覧から開いて作業を始められます。",
			result.TargetSystemName, result.Summary.Describe())}, nil
}

// knownProjectLookup は同一プロジェクトの作業コピーが既にこの端末にあるかを引く
// （同じプロジェクトを重複して取得しないため）。
func (a *API) knownProjectLookup(settings *projectstore.Settings) func(string) (string, bool) {
	return func(projectID string) (string, bool) {
		for _, path := range settings.RecentProjectPaths() {
			project, err := loadProject(path)
			if err != nil {
				continue
			}
			if project.ProjectID == projectID {
				return path, true
			}
		}
		return "", false
	}
}

// cloneCredential は取得の入力から認証情報を組み立てる（不要な同期先では nil）。
func cloneCredential(remote syncmod.Remote, req CloneSyncProjectRequest) (*keymanager.SyncCredential, error) {
	if !remote.RequiresCredential() {
		return nil, nil
	}
	kind := keymanager.CredentialKind(strings.TrimSpace(req.CredentialKind))
	if kind == "" {
		kind = remote.CredentialKind()
	}
	if err := kind.Validate(); err != nil {
		return nil, errors.New("認証方式を選んでください。")
	}
	if strings.TrimSpace(req.Secret) == "" {
		return nil, errors.New("この同期先には認証情報が必要です。SSH 鍵またはアクセストークンを入力してください。")
	}
	cred := keymanager.SyncCredential{Kind: kind, Username: strings.TrimSpace(req.Username),
		Secret: strings.TrimSpace(req.Secret)}
	if err := cred.Validate(); err != nil {
		return nil, err
	}
	return &cred, nil
}

// ---- プロジェクト一覧の同期状態 ------------------------------------------------

// syncStatusClient は一覧の同期状態を読むための同期モジュールを 1 つ作る
// （プロジェクトごとに git を探し直さない）。作業者が未登録・組み立てに失敗したときは nil。
func (a *API) syncStatusClient(author projectstore.Author) *syncmod.Client {
	if a.pathsErr != nil || author.AuthorID == "" || author.DisplayName == "" {
		return nil
	}
	client, err := syncmod.New(syncmod.Options{Author: author, ConfigDir: a.paths.SyncDir()})
	if err != nil {
		return nil
	}
	return client
}

// applySyncSummary は一覧行へ同期の状態を足す（最後に同期した日時・未反映の変更の有無）。
//
// **同期先へ接続せず、作業コピーも変更しない**（PublishStateOf）。読めない場合は何も足さない
// （同期の状態を読めないことで一覧の表示を止めない）。
func (a *API) applySyncSummary(summary *ProjectSummary, client *syncmod.Client) {
	if !summary.Available || !summary.SyncConfigured {
		return
	}
	if last, err := auditlog.LastSyncAt(summary.Path); err == nil && !last.IsZero() {
		summary.LastSyncedAt = last.Local().Format("2006-01-02 15:04")
	}
	if client == nil {
		return
	}
	state, err := client.PublishStateOf(a.context(), summary.Path)
	if err != nil {
		return
	}
	summary.HasUnpublished = state.HasUnpublished
}
