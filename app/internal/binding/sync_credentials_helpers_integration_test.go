//go:build integration

package binding

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testSSHPrivateKey はテスト用に生成した ed25519 の秘密鍵（OpenSSH 形式・パスフレーズなし）。
func testSSHPrivateKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "reqweave-test")
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}
