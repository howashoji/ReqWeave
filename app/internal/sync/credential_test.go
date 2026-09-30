package sync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"golang.org/x/crypto/ssh"
)

const dummyToken = "dummy-sync-token-value-5678:with/special chars"

func testSSHKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "reqweave-test")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(pem.EncodeToMemory(block)))
}

// isolateTempDir は一時領域をテストごとに隔離する（掃除の検証と後片づけの検証のため）。
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, dir)
	}
	return dir
}

func handoffDirs(t *testing.T, tmp string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(tmp, handoffPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// secretNeedles は平文と Base64 表現（認証情報が漏れていないかを全文検索する対象）。
func secretNeedles(secret string) []string {
	return []string{secret, base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawStdEncoding.EncodeToString([]byte(secret))}
}

func assertNoSecret(t *testing.T, label string, values []string, secret string) {
	t.Helper()
	for _, v := range values {
		for _, needle := range secretNeedles(secret) {
			if strings.Contains(v, needle) {
				t.Errorf("%s に認証情報が含まれる: %s", label, v)
			}
		}
	}
}

// SSH 鍵は 0600 の一時ファイルへ書き、GIT_SSH_COMMAND で参照させる。環境変数に鍵を置かない。
func TestPrepareHandoffSSHKey(t *testing.T) {
	tmp := isolateTempDir(t)
	key := testSSHKey(t)
	h, err := PrepareHandoff(keymanager.SyncCredential{Kind: keymanager.CredentialSSHKey, Secret: key}, "git@git.example.co.jp:team/proj.git")
	if err != nil {
		t.Fatalf("受け渡しの準備に失敗: %v", err)
	}
	t.Cleanup(func() { _ = h.Cleanup() })

	dirs := handoffDirs(t, tmp)
	if len(dirs) != 1 {
		t.Fatalf("一時領域が 1 つでない: %v", dirs)
	}
	keyFile := filepath.Join(dirs[0], "id")
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("鍵ファイルが無い: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Errorf("鍵ファイルの権限が 0600 でない: %o", info.Mode().Perm())
		}
		dinfo, _ := os.Stat(dirs[0])
		if dinfo.Mode().Perm() != 0o700 {
			t.Errorf("一時領域の権限が 0700 でない: %o", dinfo.Mode().Perm())
		}
	}
	if b, _ := os.ReadFile(keyFile); strings.TrimSpace(string(b)) != key {
		t.Error("鍵ファイルの内容が登録した鍵と違う")
	}

	env := h.Env()
	var sshCmd string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "GIT_SSH_COMMAND="); ok {
			sshCmd = v
		}
	}
	if sshCmd == "" {
		t.Fatalf("GIT_SSH_COMMAND が無い: %v", env)
	}
	if !strings.Contains(sshCmd, filepath.ToSlash(keyFile)) {
		t.Errorf("GIT_SSH_COMMAND が鍵ファイルを参照していない: %q", sshCmd)
	}
	for _, opt := range []string{"-o IdentitiesOnly=yes", "-o BatchMode=yes", "-o StrictHostKeyChecking=accept-new"} {
		if !strings.Contains(sshCmd, opt) {
			t.Errorf("GIT_SSH_COMMAND に %s が無い: %q", opt, sshCmd)
		}
	}
	if !contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("端末への問い合わせが閉じられていない: %v", env)
	}
	assertNoSecret(t, "環境変数", env, key)
	assertNoSecret(t, "引数", h.ConfigArgs(), key)
	if len(h.ConfigArgs()) != 0 {
		t.Errorf("SSH 鍵で -c 引数が付いた: %v", h.ConfigArgs())
	}

	// 完了後は一時ファイルが残らない（二重呼び出しも安全）
	if err := h.Cleanup(); err != nil {
		t.Fatalf("後片づけに失敗: %v", err)
	}
	if err := h.Cleanup(); err != nil {
		t.Errorf("二重の後片づけがエラー: %v", err)
	}
	if left := handoffDirs(t, tmp); len(left) != 0 {
		t.Errorf("一時領域が残った: %v", left)
	}
}

// トークンは 0600 の一時ファイルを保存先にした資格情報ヘルパで渡す。引数・環境変数にトークンを置かない。
func TestPrepareHandoffToken(t *testing.T) {
	tmp := isolateTempDir(t)
	cred := keymanager.SyncCredential{Kind: keymanager.CredentialToken, Username: "alice@example.co.jp", Secret: dummyToken}
	h, err := PrepareHandoff(cred, "https://git.example.co.jp:8443/team/proj.git")
	if err != nil {
		t.Fatalf("受け渡しの準備に失敗: %v", err)
	}
	t.Cleanup(func() { _ = h.Cleanup() })

	dirs := handoffDirs(t, tmp)
	if len(dirs) != 1 {
		t.Fatalf("一時領域が 1 つでない: %v", dirs)
	}
	storeFile := filepath.Join(dirs[0], "credentials")
	info, err := os.Stat(storeFile)
	if err != nil {
		t.Fatalf("資格情報ファイルが無い: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("資格情報ファイルの権限が 0600 でない: %o", info.Mode().Perm())
	}
	b, _ := os.ReadFile(storeFile)
	want := "https://alice%40example.co.jp:dummy-sync-token-value-5678%3Awith%2Fspecial%20chars@git.example.co.jp:8443\n"
	if string(b) != want {
		t.Errorf("credential-store の保存形式が違う:\n got %q\nwant %q", b, want)
	}

	args := h.ConfigArgs()
	wantArgs := []string{"-c", "credential.helper=", "-c", "credential.helper=store --file='" + filepath.ToSlash(storeFile) + "'"}
	if strings.Join(args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Errorf("-c 引数が違う:\n got %q\nwant %q", args, wantArgs)
	}
	assertNoSecret(t, "引数", args, dummyToken)
	assertNoSecret(t, "環境変数", h.Env(), dummyToken)
	if !contains(h.Env(), "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("端末への問い合わせが閉じられていない: %v", h.Env())
	}

	if err := h.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if left := handoffDirs(t, tmp); len(left) != 0 {
		t.Errorf("一時領域が残った: %v", left)
	}
}

// 利用者名が空のときは既定値を補う（実装時決定）。
func TestPrepareHandoffTokenDefaultUsername(t *testing.T) {
	tmp := isolateTempDir(t)
	h, err := PrepareHandoff(keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: "tok"}, "https://github.com/x/y.git")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Cleanup()
	b, _ := os.ReadFile(filepath.Join(handoffDirs(t, tmp)[0], "credentials"))
	if string(b) != "https://git:tok@github.com\n" {
		t.Errorf("既定の利用者名が補われていない: %q", b)
	}
}

// 認証方式と同期先の形式が合わないとき・URL に認証情報が含まれるときは拒否し、一時ファイルを残さない。
func TestPrepareHandoffRejectsMismatch(t *testing.T) {
	tmp := isolateTempDir(t)
	key := testSSHKey(t)
	cases := map[string]struct {
		cred   keymanager.SyncCredential
		remote string
	}{
		"トークンを http:// へ":   {keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: dummyToken}, "http://git.example.co.jp/p.git"},
		"トークンを ssh:// へ":    {keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: dummyToken}, "ssh://git@host/p.git"},
		"トークンを scp 形式へ":     {keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: dummyToken}, "git@host:p.git"},
		"トークンを共有フォルダへ":      {keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: dummyToken}, "/Volumes/share/p.git"},
		"URL に認証情報が含まれる":    {keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: dummyToken}, "https://alice:pw@host/p.git"},
		"SSH 鍵を https:// へ": {keymanager.SyncCredential{Kind: keymanager.CredentialSSHKey, Secret: key}, "https://host/p.git"},
		"空の値":               {keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: ""}, "https://host/p.git"},
		"方式が値集合外":           {keymanager.SyncCredential{Kind: "password", Secret: "x"}, "https://host/p.git"},
		"SSH 鍵として読めない":      {keymanager.SyncCredential{Kind: keymanager.CredentialSSHKey, Secret: "nope"}, "git@host:p.git"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, err := PrepareHandoff(c.cred, c.remote)
			if err == nil {
				h.Cleanup()
				t.Fatal("不整合な受け渡しが受理された")
			}
			if h != nil {
				t.Error("失敗時に nil でない資材が返った")
			}
			if strings.Contains(err.Error(), dummyToken) || strings.Contains(err.Error(), key) {
				t.Errorf("エラーに認証情報が含まれる: %v", err)
			}
			if left := handoffDirs(t, tmp); len(left) != 0 {
				t.Errorf("失敗時に一時領域が残った: %v", left)
			}
		})
	}
}

// 共有フォルダの同期先では認証情報を要しない。
func TestNoCredential(t *testing.T) {
	h := NoCredential()
	if len(h.ConfigArgs()) != 0 {
		t.Errorf("認証なしで -c 引数が付いた: %v", h.ConfigArgs())
	}
	if !contains(h.Env(), "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("端末への問い合わせが閉じられていない: %v", h.Env())
	}
	if err := h.Cleanup(); err != nil {
		t.Errorf("後片づけがエラー: %v", err)
	}
	if err := (*Handoff)(nil).Cleanup(); err != nil {
		t.Errorf("nil の後片づけがエラー: %v", err)
	}
}

// 異常終了で残った古い一時領域は次回の受け渡し時に掃除する。進行中（新しい）ものは消さない。
func TestPrepareHandoffSweepsStaleDirs(t *testing.T) {
	tmp := isolateTempDir(t)
	stale := filepath.Join(tmp, handoffPrefix+"stale")
	fresh := filepath.Join(tmp, handoffPrefix+"fresh")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "id"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleHandoffAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	h, err := PrepareHandoff(keymanager.SyncCredential{Kind: keymanager.CredentialToken, Secret: "tok"}, "https://host/p.git")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Cleanup()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("古い一時領域が掃除されていない")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("進行中（新しい）の一時領域が消された")
	}
}

// 引用符を含む値も壊れない（シェル経由で参照させるための保護）。
func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/tmp/a b":   "'/tmp/a b'",
		"/tmp/it's":  `'/tmp/it'\''s'`,
		"C:/Users/x": "'C:/Users/x'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPercentEncode(t *testing.T) {
	if got := percentEncode("a-b._~Z9"); got != "a-b._~Z9" {
		t.Errorf("非予約文字が符号化された: %q", got)
	}
	if got := percentEncode("a:b@c/d e%"); got != "a%3Ab%40c%2Fd%20e%25" {
		t.Errorf("符号化が違う: %q", got)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
