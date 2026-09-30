//go:build integration

package keymanager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
)

const dummySyncToken = "reqweave-dummy-sync-token-do-not-use-7c1d9e"

// 同期先の認証情報が OS セキュアストレージへ
// 直接保存され（別サービス名）、削除後は「未登録」として振る舞うこと。
func TestRealSecureStorageSyncCredentialRoundTrip(t *testing.T) {
	s := keymanager.NewSyncCredentials()
	ref := keymanager.SyncRef{ProjectID: keytest.Marker + uuid.NewString(), Kind: keymanager.CredentialToken}
	keytest.TrackSync(t, ref)

	if ok, err := s.Exists(ref); err != nil || ok {
		t.Fatalf("未登録なのに存在した: %v %v", ok, err)
	}
	if err := s.Register(ref, keymanager.SyncCredential{Kind: keymanager.CredentialToken, Username: "alice", Secret: dummySyncToken}); err != nil {
		t.Fatalf("OS セキュアストレージへ登録できません: %v", err)
	}
	got, err := s.Credential(context.Background(), ref)
	if err != nil {
		t.Fatalf("読み出せません: %v", err)
	}
	if got.Secret != dummySyncToken || got.Username != "alice" || got.Kind != keymanager.CredentialToken {
		t.Errorf("読み出した値が違う（値は伏せる）: kind=%q user=%q", got.Kind, got.Username)
	}

	// 同じアカウント名でもシークレットキーの保管庫からは見えない（別サービス名）
	keys := keymanager.New()
	if ok, err := keys.Exists(keymanager.Ref{ProviderID: ref.ProjectID, Label: string(ref.Kind)}); err != nil || ok {
		t.Errorf("同期先の認証情報がシークレットキーとして見えた: %v %v", ok, err)
	}

	if err := s.Delete(ref); err != nil {
		t.Fatalf("削除できません: %v", err)
	}
	if _, err := s.Credential(context.Background(), ref); !errors.Is(err, keymanager.ErrSyncCredentialNotSet) {
		t.Errorf("削除後が「未登録」でない: %v", err)
	}
}
