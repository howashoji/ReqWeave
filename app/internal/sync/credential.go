package sync

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
)

// 認証情報の受け渡し。
//
// git 実装へ認証情報を渡すとき、**コマンドライン引数と環境変数に平文を置かない**
// （`ps` / `ps e` で他プロセスから読めるため）。
//
//   - SSH 鍵: パーミッション 0600 の一時ファイルへ書き、GIT_SSH_COMMAND で参照させる
//     （環境変数に載るのはファイルのパスであり鍵ではない）。
//   - アクセストークン: git 組み込みの資格情報ヘルパ `credential-store` を、パーミッション 0600 の
//     一時ファイルを保存先にして使う。トークンはヘルパの標準出力（パイプ）経由で git へ渡り、
//     引数・環境変数には現れない。他のヘルパ（OS のキーチェーン連携等）は `credential.helper=`
//     で無効化し、**トークンの複製が本システムの保管庫の外へ残らない**ようにする
//     （同期先の認証情報を OS のセキュアストレージの外に残さない）。
//   - 一時ファイルは操作の完了・失敗のいずれでも削除する（Cleanup を defer で呼ぶ）。
//     異常終了で残った一時領域は次回の受け渡し時に掃除する（staleHandoffAge より古いもの）。
//
// 実装上の選択:
//   - SSH は対話なし（BatchMode）で動かし、既知ホストの初回接続は accept-new で受け入れる
//     （利用者の ~/.ssh/known_hosts に追記される。既知ホストの鍵が変わった場合は失敗する）。
//   - トークンの利用者名が空のときは "git" を補う（GitHub / GitLab のパーソナルアクセストークンは
//     任意の利用者名で通る。Bitbucket のアプリパスワード等は本人の利用者名を登録する）。
//   - トークンは HTTPS の同期先にのみ渡す（http:// は拒否 = TLS 強制）。

const (
	handoffPrefix   = "reqweave-sync-"
	staleHandoffAge = time.Hour

	// defaultTokenUsername はトークンの利用者名が未登録のときに補う値。
	defaultTokenUsername = "git"
)

// Handoff は git 実装 1 回の呼び出しに渡す認証の資材。使い終わったら必ず Cleanup を呼ぶ。
//
// Env・ConfigArgs のいずれにも認証情報の平文を含めない（含めないことをテストで固定する）。
type Handoff struct {
	env        []string
	configArgs []string
	dir        string
	cleaned    bool
}

// Env は git 実装のプロセスへ追加する環境変数（`KEY=VALUE` 形式）を返す。
func (h *Handoff) Env() []string { return append([]string(nil), h.env...) }

// ConfigArgs は git コマンドの先頭に置く `-c key=value` 引数の列を返す。
func (h *Handoff) ConfigArgs() []string { return append([]string(nil), h.configArgs...) }

// Cleanup は一時ファイルを削除する。複数回呼んでも安全。
func (h *Handoff) Cleanup() error {
	if h == nil || h.cleaned || h.dir == "" {
		return nil
	}
	h.cleaned = true
	if err := os.RemoveAll(h.dir); err != nil {
		return fmt.Errorf("同期の一時ファイルを削除できません: %w", err)
	}
	return nil
}

// NoCredential は認証情報を要しない同期先（共有フォルダ上のリポジトリ）向けの資材を返す。
func NoCredential() *Handoff {
	return &Handoff{env: baseEnv()}
}

// baseEnv は認証方式によらず付ける環境変数。git が端末へ資格情報を問い合わせて止まる経路を閉じる。
func baseEnv() []string {
	return []string{"GIT_TERMINAL_PROMPT=0"}
}

// PrepareHandoff は認証情報 cred を同期先 remote（URL または所在）へ渡すための資材を作る。
// 失敗したときは一時ファイルを残さない。
func PrepareHandoff(cred keymanager.SyncCredential, remote string) (h *Handoff, err error) {
	if err := cred.Validate(); err != nil {
		return nil, err
	}
	if err := checkRemoteMatchesKind(cred.Kind, remote); err != nil {
		return nil, err
	}
	sweepStaleHandoffs(os.TempDir(), time.Now())

	dir, err := os.MkdirTemp("", handoffPrefix)
	if err != nil {
		return nil, fmt.Errorf("同期の一時ファイルを作成できません: %w", err)
	}
	h = &Handoff{dir: dir, env: baseEnv()}
	defer func() {
		if err != nil {
			_ = h.Cleanup()
			h = nil
		}
	}()
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("同期の一時ファイルの権限を設定できません: %w", err)
	}
	if strings.ContainsAny(dir, "'\n\r") {
		// シェル経由で参照させるため、単一引用符で囲めないパスは扱わない
		return nil, fmt.Errorf("同期の一時ファイルの置き場所に使えない文字が含まれています")
	}

	switch cred.Kind {
	case keymanager.CredentialSSHKey:
		keyFile := filepath.Join(dir, "id")
		if err := writePrivate(keyFile, cred.Secret+"\n"); err != nil {
			return nil, err
		}
		h.env = append(h.env, "GIT_SSH_COMMAND="+sshCommand(keyFile))
	case keymanager.CredentialToken:
		storeFile := filepath.Join(dir, "credentials")
		line, err := credentialStoreLine(cred, remote)
		if err != nil {
			return nil, err
		}
		if err := writePrivate(storeFile, line+"\n"); err != nil {
			return nil, err
		}
		h.configArgs = []string{
			"-c", "credential.helper=", // 既存のヘルパ一覧をリセット（OS キーチェーン等へ複製させない）
			"-c", "credential.helper=store --file=" + shellQuote(filepath.ToSlash(storeFile)),
		}
	}
	return h, nil
}

// checkRemoteMatchesKind は認証方式と同期先の形式の整合を検証する（所在の検証自体は Remote.Validate）。
func checkRemoteMatchesKind(kind keymanager.CredentialKind, remote string) error {
	remote = strings.TrimSpace(remote)
	switch kind {
	case keymanager.CredentialToken:
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return fmt.Errorf("アクセストークンを使う同期先は https:// で始まる URL で指定してください")
		}
		if !strings.EqualFold(u.Scheme, "https") {
			return fmt.Errorf("アクセストークンは https:// の同期先にのみ使えます（暗号化されない接続には渡しません）")
		}
		if u.User != nil {
			return fmt.Errorf("同期先の URL に認証情報を含めることはできません。認証情報は設定で登録してください")
		}
	case keymanager.CredentialSSHKey:
		if scheme, _, ok := strings.Cut(remote, "://"); ok && !strings.EqualFold(scheme, "ssh") {
			return fmt.Errorf("SSH 鍵は ssh:// または user@host:path 形式の同期先にのみ使えます")
		}
	}
	return nil
}

// writePrivate は本人のみ読める（0600）ファイルを新規作成して内容を書く。
// 既存ファイルがあれば失敗する（他プロセスが用意したパスへ書かない）。
//
// Windows ではモードビットが ACL に写らないが、一時領域（%TEMP%）は OS アカウントごとの領域であり
// 他アカウントから読めない（要確認: Windows 実機では未確認）。
func writePrivate(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("同期の一時ファイルを作成できません: %w", err)
	}
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("同期の一時ファイルを書き込めません: %w", werr)
	}
	if cerr != nil {
		return fmt.Errorf("同期の一時ファイルを書き込めません: %w", cerr)
	}
	return nil
}

// sshCommand は GIT_SSH_COMMAND の値を組み立てる（鍵ファイルのパスのみを含む）。
func sshCommand(keyFile string) string {
	return "ssh -i " + shellQuote(filepath.ToSlash(keyFile)) +
		" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new"
}

// credentialStoreLine は git-credential-store の保存形式 `https://user:pass@host[:port]` を組み立てる。
func credentialStoreLine(cred keymanager.SyncCredential, remote string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(remote))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("同期先の URL を解釈できません")
	}
	user := cred.Username
	if user == "" {
		user = defaultTokenUsername
	}
	return "https://" + percentEncode(user) + ":" + percentEncode(cred.Secret) + "@" + u.Host, nil
}

// percentEncode は資格情報部の値を URL 符号化する（英数字と -._~ 以外をすべて %XX にする）。
// git-credential-store は保存行の利用者名・パスワードを URL 復号して扱う。
func percentEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// shellQuote は POSIX シェルの単一引用符で囲む（git はヘルパ文字列・GIT_SSH_COMMAND を sh 経由で実行する）。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sweepStaleHandoffs は異常終了で残った一時領域を掃除する（best effort。失敗は無視する）。
// 進行中の別プロセス（同一端末の多重起動）の領域を消さないよう、古いものだけを対象にする。
func sweepStaleHandoffs(tmpDir string, now time.Time) {
	matches, err := filepath.Glob(filepath.Join(tmpDir, handoffPrefix+"*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() {
			continue
		}
		if now.Sub(info.ModTime()) < staleHandoffAge {
			continue
		}
		_ = os.RemoveAll(m)
	}
}

// ErrCredentialNotSet は同期先の認証情報が未登録であることを表す（keymanager.ErrSyncCredentialNotSet の別名）。
var ErrCredentialNotSet = keymanager.ErrSyncCredentialNotSet

// IsCredentialNotSet は err が「認証情報が未登録」かを返す（呼び出し側の失敗分類 = auth_failed）。
func IsCredentialNotSet(err error) bool { return errors.Is(err, ErrCredentialNotSet) }
