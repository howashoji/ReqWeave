package keymanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/ssh"
)

// SyncServiceName は同期先の認証情報を置く OS セキュアストレージのサービス名
// （キーマネージャと同じ機構・**別のサービス名**）。
// シークレットキー（ServiceName）と名前空間を分け、互いのエントリを列挙・上書きしない。
const SyncServiceName = "howashoji.reqweave.sync"

// SecureStorageMaxBytes は 1 エントリに保存できる値の上限（バイト）。
//
// Windows の資格情報マネージャーの上限（CRED_MAX_CREDENTIAL_BLOB_SIZE = 2560）に合わせる。
// 認証情報は端末ごとに保持するため OS 間で共有されないが、「macOS では登録できたのに
// Windows では登録できない」という差を作らないよう、全 OS で同じ上限を課す
// （ed25519 の秘密鍵は 400 バイト程度、RSA 2048 は 1.7KB 程度で収まる。RSA 4096 は超える）。
const SecureStorageMaxBytes = 2560

// ErrSyncCredentialNotSet は同期先の認証情報が未登録（または OS 側で削除済み）であることを表す。
// 外部でエントリが消えていた場合も本エラーへ正規化し、「認証情報が未登録」の振る舞いを一本化する
// （シークレットキーの「キー未設定」と同じ規律）。
var ErrSyncCredentialNotSet = errors.New("同期先の認証情報が未登録です")

// CredentialKind は認証方式（SSH 鍵 / アクセストークン）。
type CredentialKind string

const (
	// CredentialSSHKey は SSH 秘密鍵（同期先が SSH URL のとき）。
	CredentialSSHKey CredentialKind = "ssh_key"
	// CredentialToken はアクセストークン（同期先が HTTPS URL のとき）。
	CredentialToken CredentialKind = "token"
)

// Validate は認証方式が値集合内かを検証する。
func (k CredentialKind) Validate() error {
	switch k {
	case CredentialSSHKey, CredentialToken:
		return nil
	default:
		return fmt.Errorf("同期先の認証方式が対応外です: %q", string(k))
	}
}

// SyncRef は同期先の認証情報の参照名（アプリ設定 `sync_credentials` の値）。
// 形式は `<project_id>/<認証方式>`。参照名から認証情報の値は復元できない。
type SyncRef struct {
	ProjectID string
	Kind      CredentialKind
}

// String はセキュアストレージのアカウント名（= アプリ設定に保持する参照名）を返す。
func (r SyncRef) String() string { return r.ProjectID + "/" + string(r.Kind) }

// Validate は参照名の形式を検証する。
func (r SyncRef) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return fmt.Errorf("同期先の認証情報の参照名にプロジェクト ID がありません")
	}
	if strings.ContainsAny(r.ProjectID, "/ \t\r\n") {
		return fmt.Errorf("同期先の認証情報の参照名のプロジェクト ID に使えない文字があります: %q", r.ProjectID)
	}
	return r.Kind.Validate()
}

// ParseSyncRef は `<project_id>/<認証方式>` を解釈する（アプリ設定の sync_credentials から復元する）。
func ParseSyncRef(s string) (SyncRef, error) {
	projectID, kind, ok := strings.Cut(s, "/")
	if !ok {
		return SyncRef{}, fmt.Errorf("同期先の認証情報の参照名の形式が違います（<プロジェクト ID>/<認証方式>）: %q", s)
	}
	r := SyncRef{ProjectID: projectID, Kind: CredentialKind(kind)}
	if err := r.Validate(); err != nil {
		return SyncRef{}, err
	}
	return r, nil
}

// SyncCredential は同期先の認証情報の値。**メモリ上でのみ扱い、永続化先は OS セキュアストレージだけ**
// （ファイルに平文で残さない）。フロントエンドへ返す型・ログへ渡す型に本型を含めてはならない。
//
// セキュアストレージには本型の JSON を 1 エントリとして置く（認証方式・利用者名・値を 1 つに
// まとめ、参照名からは方式しか分からないようにする）。
type SyncCredential struct {
	Kind CredentialKind `json:"kind"`
	// Username はアクセストークンと組で使う利用者名（HTTPS の Basic 認証の利用者名部）。
	// GitHub / GitLab のパーソナルアクセストークンでは任意の値でよく、空なら利用側が既定値を補う。
	// SSH 鍵では使わない。
	Username string `json:"username,omitempty"`
	// Secret は SSH 秘密鍵の本文（PEM / OpenSSH 形式）またはアクセストークン。
	Secret string `json:"secret"`
}

// Validate は認証情報の値を検証する。**エラーメッセージに値を含めない**。
//
//   - SSH 鍵: 秘密鍵として解釈できること。パスフレーズ付きの鍵は受け付けない
//     （git 実装は対話なし = BatchMode で動かすため、パスフレーズを求められた時点で失敗する。
//     登録時に弾いて原因を先に示す）。
//   - トークン: 空でなく、改行を含まないこと。
func (c SyncCredential) Validate() error {
	if err := c.Kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Secret) == "" {
		return fmt.Errorf("同期先の認証情報を入力してください")
	}
	switch c.Kind {
	case CredentialSSHKey:
		if c.Username != "" {
			return fmt.Errorf("SSH 鍵には利用者名を指定できません")
		}
		if _, err := ssh.ParseRawPrivateKey([]byte(c.Secret)); err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return fmt.Errorf("パスフレーズ付きの SSH 鍵は登録できません。パスフレーズなしの鍵（ed25519 を推奨）を用意してください")
			}
			return fmt.Errorf("SSH 秘密鍵として読み取れません。OpenSSH 形式の秘密鍵ファイルの内容を貼り付けてください")
		}
	case CredentialToken:
		if strings.ContainsAny(c.Secret, "\r\n") {
			return fmt.Errorf("アクセストークンに改行を含めることはできません")
		}
		if strings.ContainsAny(c.Username, "\r\n") {
			return fmt.Errorf("利用者名に改行を含めることはできません")
		}
	}
	return nil
}

// SyncCredentials は同期先の認証情報の保管庫。
// 公開操作は Register / Credential / Delete / Exists の 4 つのみ（キーマネージャと同型）。
// 値を返すのは Credential だけで、呼び出しは同期モジュール（internal/sync）の受け渡し処理に限る。
type SyncCredentials struct {
	service string
	backend backend
}

// NewSyncCredentials は OS セキュアストレージ直結の保管庫を返す。
func NewSyncCredentials() *SyncCredentials {
	return &SyncCredentials{service: SyncServiceName, backend: osKeyring{}}
}

// Register は認証情報を登録する（既存の登録は上書きする）。
// ref.Kind と cred.Kind が一致しない登録は拒否する。
func (s *SyncCredentials) Register(ref SyncRef, cred SyncCredential) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	cred.Secret = strings.TrimSpace(cred.Secret)
	cred.Username = strings.TrimSpace(cred.Username)
	if err := cred.Validate(); err != nil {
		return err
	}
	if cred.Kind != ref.Kind {
		return fmt.Errorf("同期先の認証方式が参照名と一致しません（%s）", ref)
	}
	payload, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("同期先の認証情報を保存できません（%s）: 値を組み立てられません", ref)
	}
	if len(payload) > SecureStorageMaxBytes {
		return fmt.Errorf("同期先の認証情報が長すぎます（上限 %d バイト）。SSH 鍵は ed25519 など短い形式を使ってください", SecureStorageMaxBytes)
	}
	if err := s.backend.Set(s.service, ref.String(), string(payload)); err != nil {
		return fmt.Errorf("同期先の認証情報を保存できません（%s）: OS のセキュアストレージへの保存に失敗しました", ref)
	}
	return nil
}

// Credential は認証情報の値を返す。未登録・OS 側で削除済みの場合は ErrSyncCredentialNotSet を返す。
func (s *SyncCredentials) Credential(ctx context.Context, ref SyncRef) (SyncCredential, error) {
	if err := ctx.Err(); err != nil {
		return SyncCredential{}, err
	}
	if err := ref.Validate(); err != nil {
		return SyncCredential{}, err
	}
	raw, err := s.backend.Get(s.service, ref.String())
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return SyncCredential{}, fmt.Errorf("%w（%s）", ErrSyncCredentialNotSet, ref)
		}
		return SyncCredential{}, fmt.Errorf("同期先の認証情報を読み出せません（%s）: OS のセキュアストレージへのアクセスに失敗しました", ref)
	}
	if strings.TrimSpace(raw) == "" {
		return SyncCredential{}, fmt.Errorf("%w（%s）", ErrSyncCredentialNotSet, ref)
	}
	var cred SyncCredential
	if err := json.Unmarshal([]byte(raw), &cred); err != nil || cred.Kind != ref.Kind || strings.TrimSpace(cred.Secret) == "" {
		// 本システム以外が書いた値・壊れた値は「未登録」ではなく読み出し失敗として区別する
		// （黙って未登録扱いにすると、登録し直しても原因が見えないため）。値は出さない。
		return SyncCredential{}, fmt.Errorf("同期先の認証情報を読み出せません（%s）: 保存されている値の形式が違います。登録し直してください", ref)
	}
	return cred, nil
}

// Delete はエントリを削除する。既に無い場合も成功として扱う。
func (s *SyncCredentials) Delete(ref SyncRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := s.backend.Delete(s.service, ref.String()); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("同期先の認証情報を削除できません（%s）: OS のセキュアストレージへのアクセスに失敗しました", ref)
	}
	return nil
}

// Exists は認証情報が登録済みかを返す（値は返さない）。
func (s *SyncCredentials) Exists(ref SyncRef) (bool, error) {
	_, err := s.Credential(context.Background(), ref)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrSyncCredentialNotSet) {
		return false, nil
	}
	return false, err
}
