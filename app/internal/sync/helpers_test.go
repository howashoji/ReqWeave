package sync

import "github.com/howashoji/ReqWeave/app/internal/keymanager"

// テスト用の別名（ops_test.go の CredentialProvider 実装で keymanager の型を短く参照する）。
type keymanagerSyncCredential = keymanager.SyncCredential

var errCredentialNotSetForTest = keymanager.ErrSyncCredentialNotSet
