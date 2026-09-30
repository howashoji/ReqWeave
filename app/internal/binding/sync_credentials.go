package binding

import (
	"fmt"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 同期先の認証情報のバインディング。
//
// **認証情報の値をバインディングの戻り値・イベントに載せない**（シークレットキーと同一方針）。
// 画面が扱うのは「登録済みか」「認証方式」「マスク表記」だけで、平文の再表示機能を持たない。
// 接続確認（登録した認証情報で同期先へ接続できることの確認）は同期モジュールの取得・取り込み
// で実装する。

// SyncCredentialView は同期先の認証情報の画面表示内容。
// 値・利用者名・参照名の中身を載せない。
type SyncCredentialView struct {
	ProjectID string `json:"projectId"`
	// Registered は登録済みか（平文の再表示はしない）。
	Registered bool `json:"registered"`
	// Kind は認証方式（"ssh_key" / "token"。未登録は ""）。
	Kind      string `json:"kind"`
	KindLabel string `json:"kindLabel"`
	// State / Masked はシークレットキーと同じ表示様式（「登録済み」/「未設定」・マスク表記）。
	State  string `json:"state"`
	Masked string `json:"masked"`
	// Kinds は選べる認証方式の一覧（値と表示ラベル。UI 側にラベルを二重に持たせない）。
	Kinds []SyncCredentialKindOption `json:"kinds"`
}

// SyncCredentialKindOption は認証方式の選択肢。
type SyncCredentialKindOption struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Hint は入力欄の説明（パスワード型・マスク表示）。
	Hint string `json:"hint"`
}

// RegisterSyncCredentialRequest は登録・更新の入力。Secret は保存後に破棄され、戻り値に含めない。
type RegisterSyncCredentialRequest struct {
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
	// Username はアクセストークンと組で使う利用者名（任意。SSH 鍵では指定しない）。
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

var syncCredentialKindOptions = []SyncCredentialKindOption{
	{Kind: string(keymanager.CredentialSSHKey), Label: "SSH 鍵",
		Hint: "パスフレーズなしの秘密鍵（ed25519 を推奨）の内容を貼り付けてください。ssh:// または user@host:path 形式の同期先で使います。"},
	{Kind: string(keymanager.CredentialToken), Label: "アクセストークン",
		Hint: "https:// の同期先で使うアクセストークンを入力してください。利用者名は任意です（GitHub / GitLab のパーソナルアクセストークンでは省略できます）。"},
}

// syncCredentialKindLabel は認証方式の表示ラベル（型で網羅を強制し、生のコード値を画面へ出さない）。
var syncCredentialKindLabel = map[keymanager.CredentialKind]string{
	keymanager.CredentialSSHKey: "SSH 鍵",
	keymanager.CredentialToken:  "アクセストークン",
}

// syncCredentials は同期先の認証情報の保管庫（遅延生成。テストでは API を直接組み立てるため）。
func (a *API) syncCredentials() *keymanager.SyncCredentials {
	if a.syncKeys == nil {
		a.syncKeys = keymanager.NewSyncCredentials()
	}
	return a.syncKeys
}

// SyncCredentialView はプロジェクトの同期先の認証情報の状態を返す（値は返さない）。
func (a *API) SyncCredentialView(projectID string) (SyncCredentialView, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return SyncCredentialView{}, fmt.Errorf("プロジェクトを指定してください。")
	}
	settings, err := a.settings()
	if err != nil {
		return SyncCredentialView{}, err
	}
	view := SyncCredentialView{ProjectID: projectID, Kinds: syncCredentialKindOptions,
		State: keyStateLabel(false), Masked: keyMaskedText(false)}
	refStr, ok := settings.SyncCredentialRef(projectID)
	if !ok {
		return view, nil
	}
	ref, err := keymanager.ParseSyncRef(refStr)
	if err != nil || ref.ProjectID != projectID {
		// 参照名が壊れている: 「未設定」として扱い、登録し直せる状態にする（AI プロバイダ設定の key_ref と同じ扱い）
		return view, nil
	}
	registered, err := a.syncCredentials().Exists(ref)
	if err != nil {
		return SyncCredentialView{}, fmt.Errorf("同期先の認証情報の状態を確認できません。設定で登録し直してください。")
	}
	if !registered {
		return view, nil
	}
	view.Registered = true
	view.Kind = string(ref.Kind)
	view.KindLabel = syncCredentialKindLabel[ref.Kind]
	view.State = keyStateLabel(true)
	view.Masked = keyMaskedText(true)
	return view, nil
}

// RegisterSyncCredential は同期先の認証情報を OS セキュアストレージへ登録し、参照名をアプリ設定へ保存する。
// 既存の登録（別方式を含む）は置き換える。失敗した場合は変更前の状態を保つ。
func (a *API) RegisterSyncCredential(req RegisterSyncCredentialRequest) (SyncCredentialView, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return SyncCredentialView{}, fmt.Errorf("プロジェクトを指定してください。")
	}
	kind := keymanager.CredentialKind(strings.TrimSpace(req.Kind))
	if err := kind.Validate(); err != nil {
		return SyncCredentialView{}, fmt.Errorf("認証方式を選んでください。")
	}
	if strings.TrimSpace(req.Secret) == "" {
		return SyncCredentialView{}, fmt.Errorf("認証情報を入力してください。")
	}
	settings, err := a.settings()
	if err != nil {
		return SyncCredentialView{}, err
	}
	store := a.syncCredentials()
	ref := keymanager.SyncRef{ProjectID: projectID, Kind: kind}
	cred := keymanager.SyncCredential{Kind: kind, Username: req.Username, Secret: req.Secret}
	req.Secret = "" // 以後この関数内で値を参照しない（構造的非出力）

	if err := store.Register(ref, cred); err != nil {
		return SyncCredentialView{}, err
	}
	// 方式を変えた場合は古いエントリを消す（認証情報の複製を残さない）
	if oldStr, ok := settings.SyncCredentialRef(projectID); ok && oldStr != ref.String() {
		if old, err := keymanager.ParseSyncRef(oldStr); err == nil {
			_ = store.Delete(old)
		}
	}
	if err := settings.SetSyncCredentialRef(projectID, ref.String()); err != nil {
		_ = store.Delete(ref)
		return SyncCredentialView{}, err
	}
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		_ = store.Delete(ref)
		return SyncCredentialView{}, err
	}
	return a.SyncCredentialView(projectID)
}

// DeleteSyncCredential は同期先の認証情報を削除して「未登録」へ戻す。
func (a *API) DeleteSyncCredential(projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return fmt.Errorf("プロジェクトを指定してください。")
	}
	settings, err := a.settings()
	if err != nil {
		return err
	}
	refStr, ok := settings.SyncCredentialRef(projectID)
	if !ok {
		return nil
	}
	if ref, err := keymanager.ParseSyncRef(refStr); err == nil {
		if err := a.syncCredentials().Delete(ref); err != nil {
			return err
		}
	}
	settings.RemoveSyncCredentialRef(projectID)
	return projectstore.SaveSettings(a.paths, settings)
}
