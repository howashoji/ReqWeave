// Package keymanager はキーマネージャ。シークレットキーを OS セキュアストレージ直結で扱う。キー本体をフロントエンドへ渡す API を設けない（画面側からキー本体に触れる経路を作らない）。
//
// キー本体はファイルに書かず、ログ・エラーメッセージにも出さない。突き合わせは末尾 4 文字だけで行う。
package keymanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// ServiceName は OS セキュアストレージのサービス名（製品名に基づく。変えると登録済みのキーが見えなくなる）。
const ServiceName = "howashoji.reqweave"

// ErrKeyNotSet はキーが未登録（または OS 側で削除済み）であることを表す。
// 外部でエントリが消えていた場合も本エラーへ正規化し、「キー未設定」の振る舞いを一本化する
// （OS の管理画面から消された場合も、アプリ側は「未設定」として登録を促す）。
var ErrKeyNotSet = errors.New("キーが未設定です")

// Ref はキー参照名（セキュアストレージのアカウント名 `<ProviderID>/<参照名>`）。
// 参照名からキー本体は復元できない。
type Ref struct {
	ProviderID string // AI プロバイダ抽象化層のプロバイダ ID（anthropic / openai / google）
	Label      string // 利用者が付ける参照名（例: 本番用）
}

// String はセキュアストレージのアカウント名を返す。
func (r Ref) String() string { return r.ProviderID + "/" + r.Label }

// Validate は参照名の形式を検証する。
func (r Ref) Validate() error {
	if strings.TrimSpace(r.ProviderID) == "" {
		return fmt.Errorf("キー参照名のプロバイダが空です")
	}
	if strings.TrimSpace(r.Label) == "" {
		return fmt.Errorf("キー参照名のラベルが空です")
	}
	if strings.Contains(r.ProviderID, "/") {
		return fmt.Errorf("キー参照名のプロバイダに / は使えません: %q", r.ProviderID)
	}
	return nil
}

// ParseRef は `<ProviderID>/<参照名>` を解釈する（アプリ設定の key_ref から復元する）。
func ParseRef(s string) (Ref, error) {
	provider, label, ok := strings.Cut(s, "/")
	if !ok {
		return Ref{}, fmt.Errorf("キー参照名の形式が違います（<プロバイダ>/<参照名>）: %q", s)
	}
	r := Ref{ProviderID: provider, Label: label}
	if err := r.Validate(); err != nil {
		return Ref{}, err
	}
	return r, nil
}

// backend は OS セキュアストレージへの操作（テストで差し替える）。
type backend interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}
func (osKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (osKeyring) Delete(service, user string) error        { return keyring.Delete(service, user) }

// Manager はキーマネージャ。公開操作は Register / SecretKey / Delete / Exists の 4 つのみ
// （キー本体を返すのは SecretKey だけで、呼び出しは AI API 呼び出し時と疎通確認時に限る）。
type Manager struct {
	service string
	backend backend
}

// New は OS セキュアストレージ直結のキーマネージャを返す。
func New() *Manager { return &Manager{service: ServiceName, backend: osKeyring{}} }

// Register はキーを登録する（既存の登録は上書きする）。
//
// エラーメッセージにキー本体を含めない（構造的非出力）。
func (m *Manager) Register(ref Ref, key string) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("キーを入力してください")
	}
	if err := m.backend.Set(m.service, ref.String(), key); err != nil {
		return fmt.Errorf("キーを保存できません（%s）: OS のセキュアストレージへの保存に失敗しました", ref)
	}
	return nil
}

// SecretKey はキー本体を返す（AI プロバイダ抽象化層の KeyProvider 実装）。
// 未登録・OS 側で削除済みの場合は ErrKeyNotSet を返す。
func (m *Manager) SecretKey(ctx context.Context, ref Ref) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := ref.Validate(); err != nil {
		return "", err
	}
	key, err := m.backend.Get(m.service, ref.String())
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("%w（%s）", ErrKeyNotSet, ref)
		}
		return "", fmt.Errorf("キーを読み出せません（%s）: OS のセキュアストレージへのアクセスに失敗しました", ref)
	}
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w（%s）", ErrKeyNotSet, ref)
	}
	return key, nil
}

// Delete はエントリを削除する。既に無い場合も成功として扱う。
func (m *Manager) Delete(ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := m.backend.Delete(m.service, ref.String()); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("キーを削除できません（%s）: OS のセキュアストレージへのアクセスに失敗しました", ref)
	}
	return nil
}

// Exists はキーが登録済みかを返す（キー本体は返さない）。
func (m *Manager) Exists(ref Ref) (bool, error) {
	_, err := m.SecretKey(context.Background(), ref)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrKeyNotSet) {
		return false, nil
	}
	return false, err
}

// MaskKey は画面・記録での突き合わせ用のマスク表記を返す。
// 末尾 4 文字のみを残し、それより短いキーは全体を伏せる。平文の再表示機能は設けない（登録後のキーは画面に戻さない）。
func MaskKey(key string) string {
	key = strings.TrimSpace(key)
	const tail = 4
	if len([]rune(key)) <= tail {
		return "••••"
	}
	r := []rune(key)
	return "••••" + string(r[len(r)-tail:])
}
