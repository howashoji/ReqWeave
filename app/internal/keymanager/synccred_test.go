package keymanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newTestSyncCredentials() (*SyncCredentials, *fakeBackend) {
	f := newFake()
	return &SyncCredentials{service: SyncServiceName, backend: f}, f
}

// testSSHKey はテスト用に生成した ed25519 の秘密鍵（OpenSSH 形式）。passphrase が空なら平文。
func testSSHKey(t *testing.T, passphrase string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "reqweave-test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "reqweave-test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

const dummyToken = "dummy-sync-token-value-5678"

// 別のサービス名（シークレットキーと名前空間を分ける）。
func TestSyncServiceNameIsDistinct(t *testing.T) {
	if SyncServiceName == ServiceName {
		t.Fatalf("同期先の認証情報のサービス名がシークレットキーと同じ: %q", SyncServiceName)
	}
	if !strings.HasPrefix(SyncServiceName, ServiceName+".") {
		t.Errorf("サービス名が本システムの名前空間にない: %q", SyncServiceName)
	}
}

// 参照名は `<project_id>/<認証方式>`。
func TestSyncRefFormatAndParse(t *testing.T) {
	r := SyncRef{ProjectID: "01J9ABC", Kind: CredentialToken}
	if got := r.String(); got != "01J9ABC/token" {
		t.Errorf("参照名の形式が違う: %q", got)
	}
	parsed, err := ParseSyncRef("01J9ABC/token")
	if err != nil || parsed != r {
		t.Errorf("解釈結果が違う: %+v %v", parsed, err)
	}
	for _, s := range []string{"", "01J9ABC", "/token", "01J9ABC/", "01J9ABC/password", "a b/token", "a/b/token"} {
		if _, err := ParseSyncRef(s); err == nil {
			t.Errorf("%q は不正だが受理された", s)
		}
	}
}

// 公開操作は Register / Credential / Delete / Exists の 4 つ（キーマネージャと同型）。
func TestSyncCredentialRoundTrip(t *testing.T) {
	s, _ := newTestSyncCredentials()
	ref := SyncRef{ProjectID: "p1", Kind: CredentialToken}

	if ok, err := s.Exists(ref); err != nil || ok {
		t.Errorf("未登録なのに存在した: %v %v", ok, err)
	}
	if err := s.Register(ref, SyncCredential{Kind: CredentialToken, Username: " alice ", Secret: "  " + dummyToken + "\n"}); err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	got, err := s.Credential(context.Background(), ref)
	if err != nil {
		t.Fatalf("読み出しに失敗: %v", err)
	}
	if got.Secret != dummyToken || got.Username != "alice" || got.Kind != CredentialToken {
		t.Errorf("読み出した値が違う（前後の空白除去・利用者名・方式）: kind=%q user=%q secretLen=%d", got.Kind, got.Username, len(got.Secret))
	}
	if ok, err := s.Exists(ref); err != nil || !ok {
		t.Errorf("登録済みなのに存在しない: %v %v", ok, err)
	}

	// SSH 鍵も同じ保管庫に別の参照名で置ける
	key := testSSHKey(t, "")
	sshRef := SyncRef{ProjectID: "p2", Kind: CredentialSSHKey}
	if err := s.Register(sshRef, SyncCredential{Kind: CredentialSSHKey, Secret: key}); err != nil {
		t.Fatalf("SSH 鍵の登録に失敗: %v", err)
	}
	if got, _ := s.Credential(context.Background(), sshRef); got.Secret != strings.TrimSpace(key) {
		t.Error("SSH 鍵が読み出せない")
	}

	if err := s.Delete(ref); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	if _, err := s.Credential(context.Background(), ref); !errors.Is(err, ErrSyncCredentialNotSet) {
		t.Errorf("削除後が「未登録」でない: %v", err)
	}
	if err := s.Delete(ref); err != nil {
		t.Errorf("二重削除がエラーになった: %v", err)
	}
}

// OS 側で消えていた場合も「未登録」へ正規化する。
func TestSyncCredentialNormalizesNotFound(t *testing.T) {
	s, f := newTestSyncCredentials()
	ref := SyncRef{ProjectID: "p1", Kind: CredentialToken}
	if err := s.Register(ref, SyncCredential{Kind: CredentialToken, Secret: dummyToken}); err != nil {
		t.Fatal(err)
	}
	delete(f.items, SyncServiceName+"\x00"+ref.String())
	if _, err := s.Credential(context.Background(), ref); !errors.Is(err, ErrSyncCredentialNotSet) {
		t.Errorf("外部削除が「未登録」に正規化されない: %v", err)
	}
}

// 本システム以外が書いた値・壊れた値は「未登録」と区別して失敗させる（登録し直しの導線）。
func TestSyncCredentialRejectsForeignValue(t *testing.T) {
	s, f := newTestSyncCredentials()
	ref := SyncRef{ProjectID: "p1", Kind: CredentialToken}
	f.items[SyncServiceName+"\x00"+ref.String()] = "not-json-" + dummyToken
	_, err := s.Credential(context.Background(), ref)
	if err == nil || errors.Is(err, ErrSyncCredentialNotSet) {
		t.Errorf("形式違いの値が未登録扱い・成功扱いになった: %v", err)
	}
	if strings.Contains(err.Error(), dummyToken) {
		t.Errorf("エラーに値が含まれる: %v", err)
	}
	// 参照名の方式と保存値の方式が食い違う場合も同様
	f.items[SyncServiceName+"\x00"+ref.String()] = `{"kind":"ssh_key","secret":"x"}`
	if _, err := s.Credential(context.Background(), ref); err == nil || errors.Is(err, ErrSyncCredentialNotSet) {
		t.Errorf("方式不一致の値が受理された: %v", err)
	}
}

// 登録時の検証: 方式の一致・空値・パスフレーズ付き鍵・改行入りトークン・上限超過。
func TestSyncCredentialRegisterValidation(t *testing.T) {
	s, _ := newTestSyncCredentials()
	tokenRef := SyncRef{ProjectID: "p1", Kind: CredentialToken}
	sshRef := SyncRef{ProjectID: "p1", Kind: CredentialSSHKey}

	cases := map[string]struct {
		ref  SyncRef
		cred SyncCredential
	}{
		"空の値":            {tokenRef, SyncCredential{Kind: CredentialToken, Secret: " \n"}},
		"方式が参照名と不一致":     {tokenRef, SyncCredential{Kind: CredentialSSHKey, Secret: testSSHKey(t, "")}},
		"方式が値集合外":        {SyncRef{ProjectID: "p1", Kind: "password"}, SyncCredential{Kind: "password", Secret: "x"}},
		"改行入りトークン":       {tokenRef, SyncCredential{Kind: CredentialToken, Secret: "abc\ndef"}},
		"SSH 鍵として読めない":   {sshRef, SyncCredential{Kind: CredentialSSHKey, Secret: "this is not a key"}},
		"パスフレーズ付き SSH 鍵": {sshRef, SyncCredential{Kind: CredentialSSHKey, Secret: testSSHKey(t, "pass")}},
		"SSH 鍵に利用者名":     {sshRef, SyncCredential{Kind: CredentialSSHKey, Username: "git", Secret: testSSHKey(t, "")}},
		"上限超過":           {tokenRef, SyncCredential{Kind: CredentialToken, Secret: strings.Repeat("a", SecureStorageMaxBytes)}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := s.Register(c.ref, c.cred)
			if err == nil {
				t.Fatal("不正な登録が受理された")
			}
			if secret := strings.TrimSpace(c.cred.Secret); secret != "" && strings.Contains(err.Error(), secret) {
				t.Errorf("エラーに値が含まれる: %v", err)
			}
		})
	}
	if ok, _ := s.Exists(tokenRef); ok {
		t.Error("失敗した登録で値が保存された")
	}
	// パスフレーズ付きの鍵は原因を名指しする（登録時に弾く）
	err := s.Register(sshRef, SyncCredential{Kind: CredentialSSHKey, Secret: testSSHKey(t, "pass")})
	if err == nil || !strings.Contains(err.Error(), "パスフレーズ") {
		t.Errorf("パスフレーズ付き鍵の理由が示されない: %v", err)
	}
}

// エラーメッセージに値を含めない。
func TestSyncCredentialErrorsNeverContainSecret(t *testing.T) {
	s, f := newTestSyncCredentials()
	ref := SyncRef{ProjectID: "p1", Kind: CredentialToken}
	f.failSet = true
	err := s.Register(ref, SyncCredential{Kind: CredentialToken, Username: "alice", Secret: dummyToken})
	if err == nil {
		t.Fatal("失敗するはずの登録が成功した")
	}
	if strings.Contains(err.Error(), dummyToken) || strings.Contains(err.Error(), "alice") {
		t.Errorf("エラーメッセージに値が含まれる: %v", err)
	}
	f.failSet, f.failGet = false, true
	if _, err := s.Credential(context.Background(), ref); err == nil {
		t.Fatal("失敗するはずの読み出しが成功した")
	} else if strings.Contains(err.Error(), dummyToken) {
		t.Errorf("エラーメッセージに値が含まれる: %v", err)
	}
}

// 読み出しは context を尊重する。
func TestSyncCredentialRespectsContext(t *testing.T) {
	s, _ := newTestSyncCredentials()
	ref := SyncRef{ProjectID: "p1", Kind: CredentialToken}
	if err := s.Register(ref, SyncCredential{Kind: CredentialToken, Secret: dummyToken}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Credential(ctx, ref); err == nil {
		t.Error("キャンセル済みの context で読み出せてしまった")
	}
}

// シークレットキーの保管庫と同期先の保管庫は互いのエントリを見ない（別サービス名）。
func TestSyncCredentialsDoNotShareNamespaceWithKeys(t *testing.T) {
	f := newFake()
	keys := &Manager{service: ServiceName, backend: f}
	creds := &SyncCredentials{service: SyncServiceName, backend: f}
	if err := keys.Register(Ref{ProviderID: "p1", Label: "token"}, dummyKey); err != nil {
		t.Fatal(err)
	}
	// 同じアカウント名 "p1/token" でも、サービス名が違うため未登録
	if ok, err := creds.Exists(SyncRef{ProjectID: "p1", Kind: CredentialToken}); err != nil || ok {
		t.Errorf("シークレットキーのエントリが同期先の認証情報として見えた: %v %v", ok, err)
	}
}
