package sync

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/masking"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 同期先（保持先の正本は project.yaml の sync = projectstore.SyncSetting）。

// RemoteKind は同期先の種別（共有フォルダ上のリポジトリ / 社内の Git サーバ / 外部の Git サーバ）。
type RemoteKind = string

const (
	RemoteFolder      RemoteKind = projectstore.SyncKindFolder
	RemoteGitInternal RemoteKind = projectstore.SyncKindGitInternal
	RemoteGitExternal RemoteKind = projectstore.SyncKindGitExternal
)

// Remote は同期先の所在。
type Remote struct {
	Kind     RemoteKind
	Location string
}

// RemoteFromProject は project.yaml の sync から同期先を作る。未設定なら ok = false。
func RemoteFromProject(p *projectstore.Project) (Remote, bool) {
	if p == nil || p.Sync == nil {
		return Remote{}, false
	}
	return Remote{Kind: p.Sync.Kind, Location: p.Sync.Location}, true
}

// Validate は所在の形式を検証する（認証情報を含む URL は拒否する。設定ファイルへ平文で残さないため）。
func (r Remote) Validate() error {
	loc := strings.TrimSpace(r.Location)
	if loc == "" {
		return fmt.Errorf("同期先の所在を入力してください。")
	}
	if masking.StripURLCredentials(loc) != loc {
		return fmt.Errorf("同期先の所在に認証情報を含めることはできません。認証情報は設定で登録してください。")
	}
	switch r.Kind {
	case RemoteFolder:
		if strings.Contains(loc, "://") {
			return fmt.Errorf("共有フォルダの同期先はフォルダのパスで指定してください。")
		}
	case RemoteGitInternal, RemoteGitExternal:
		if scheme, _, ok := strings.Cut(loc, "://"); ok {
			switch strings.ToLower(scheme) {
			case "https", "ssh":
			case "http":
				return fmt.Errorf("同期先には暗号化された接続（https:// または ssh://）を指定してください。")
			default:
				return fmt.Errorf("同期先の URL は https:// または ssh:// で始まる形式で指定してください。")
			}
			if u, err := url.Parse(loc); err != nil || u.Host == "" {
				return fmt.Errorf("同期先の URL を解釈できません。所在を確認してください。")
			}
		} else if !scpLike(loc) {
			return fmt.Errorf("同期先は https:// / ssh:// の URL か user@host:path の形式で指定してください。")
		}
	default:
		return fmt.Errorf("同期先の種別が対応外です。")
	}
	return nil
}

// scpLike は `user@host:path` 形式（SSH の省略記法）かを返す。
func scpLike(loc string) bool {
	at := strings.Index(loc, "@")
	colon := strings.Index(loc, ":")
	return at > 0 && colon > at+1 && !strings.ContainsAny(loc[:colon], " \t")
}

// RequiresCredential は認証情報の登録を要するかを返す（共有フォルダは OS のアクセス権による）。
func (r Remote) RequiresCredential() bool { return r.Kind != RemoteFolder }

// CredentialKind は所在の形式から要する認証方式を返す（https = トークン / ssh = SSH 鍵）。
func (r Remote) CredentialKind() keymanager.CredentialKind {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Location)), "https://") {
		return keymanager.CredentialToken
	}
	return keymanager.CredentialSSHKey
}

// Display は記録・表示用の所在（資格情報部を除去する）。
func (r Remote) Display() string { return masking.StripURLCredentials(strings.TrimSpace(r.Location)) }

// gitURL は git へ渡す所在。共有フォルダはパスをそのまま渡す（git はローカルパスを直接扱える）。
func (r Remote) gitURL() string { return strings.TrimSpace(r.Location) }
