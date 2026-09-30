package binding

// 本ファイルは同期モジュール（internal/sync）の組み立て。
//
// 依存規則: 画面・バインディング層は git を直接触らず、同期モジュール経由でのみ扱う。
// 同期の受け口（番号帯 / 版番号の再採番 / 三面マージの記録 /
// 認証情報 / 進行状況）は**ここで一括して組み込み**、各画面は
// 組み立て済みのクライアントを使う（受け口の配線を画面ごとに散らさない）。

import (
	"context"
	"fmt"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// EventSyncProgress は同期の進行状況。
// 段階の表示名は同期モジュールが持つ（git の語を画面へ出さない）。
const EventSyncProgress = "sync:progress"

// SyncProgressEvent は進行状況の 1 通知。
type SyncProgressEvent struct {
	// Stage は段階の識別子（画面には出さず、表示は Label を使う）。
	Stage string `json:"stage"`
	// Label は利用者向けの段階名（「同期先へ接続しています」等）。
	Label string `json:"label"`
}

// emitSyncProgress は進行状況をフロントエンドへ通知する（Wails 未起動時は何もしない）。
func (a *API) emitSyncProgress(stage syncmod.Stage) {
	if a.ctx == nil {
		return
	}
	label, ok := syncmod.StageLabel[stage]
	if !ok {
		label = "同期しています"
	}
	wailsruntime.EventsEmit(a.ctx, EventSyncProgress, SyncProgressEvent{Stage: string(stage), Label: label})
}

// syncCredentialProvider は同期モジュールへ認証情報を渡す受け口。
//
// 参照名はアプリ設定（端末ごと）、値は OS セキュアストレージにあり、**値はここから外へ出ない**
// （戻り値は同期モジュールの受け渡し処理だけが使う）。
type syncCredentialProvider struct{ api *API }

// SyncCredential はプロジェクトの認証情報を返す。未登録なら keymanager.ErrSyncCredentialNotSet。
func (p syncCredentialProvider) SyncCredential(ctx context.Context, projectID string) (keymanager.SyncCredential, error) {
	settings, err := p.api.settings()
	if err != nil {
		return keymanager.SyncCredential{}, err
	}
	refStr, ok := settings.SyncCredentialRef(projectID)
	if !ok {
		return keymanager.SyncCredential{}, keymanager.ErrSyncCredentialNotSet
	}
	ref, err := keymanager.ParseSyncRef(refStr)
	if err != nil {
		// 参照名が壊れている: 未登録として扱い、登録し直せる状態にする（SyncCredentialView と同じ扱い）
		return keymanager.SyncCredential{}, keymanager.ErrSyncCredentialNotSet
	}
	return p.api.syncCredentials().Credential(ctx, ref)
}

// syncClient は開いているプロジェクト用の同期モジュールを組み立てる。
//
// 作業者が未登録の場合はエラー（共有プロジェクトを開く条件と同じ）。
func (a *API) syncClient(s *dialogueSession) (*syncmod.Client, error) {
	return a.syncClientResolving(s, nil)
}

// syncClientResolving は三面マージの承認の受け口つきで組み立てる（三面マージ画面が使う）。
//
// resolver が nil のときは競合があれば取り込みを中止する（既定の選択を置かない。どちらを残すかは利用者が選ぶ）。
func (a *API) syncClientResolving(s *dialogueSession, resolver syncmod.ConflictResolver) (*syncmod.Client, error) {
	if a.pathsErr != nil {
		return nil, a.pathsErr
	}
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("プロジェクトが開かれていません。")
	}
	return syncmod.New(syncmod.Options{
		Author:       s.store.Author(),
		ConfigDir:    a.paths.SyncDir(),
		Credentials:  syncCredentialProvider{api: a},
		Ranges:       projectstore.IDRangeReserver{Store: s.store},
		Versions:     a.versionResolver(s),
		Resolver:     resolver,
		MergeAuditor: a.mergeAuditor(s),
		Recorder:     syncRecorder{author: s.store.Author()},
		Progress:     a.emitSyncProgress,
	})
}

// syncCloneClient は取得（参加）用の同期モジュールを組み立てる。
//
// 取得の時点では作業コピーが無いため、作業コピーに紐づく受け口（番号帯・版番号・三面マージの記録）は
// 持たない。認証情報は取得の入力として呼び出し側が渡す（プロジェクト ID が未確定のため参照名で引けない）。
func (a *API) syncCloneClient(resolver syncmod.ConflictResolver) (*syncmod.Client, error) {
	if a.pathsErr != nil {
		return nil, a.pathsErr
	}
	settings, err := a.settings()
	if err != nil {
		return nil, err
	}
	author, ok := settings.Author()
	if !ok {
		return nil, fmt.Errorf("メールアドレス（利用者 ID）が未登録です。設定でメールアドレスを登録してください。")
	}
	return syncmod.New(syncmod.Options{
		Author:    author,
		ConfigDir: a.paths.SyncDir(),
		Resolver:  resolver,
		Recorder:  syncRecorder{author: author},
		Progress:  a.emitSyncProgress,
	})
}
