//go:build integration

// 結合テスト（実ファイル I/O）。実行: make -C app test-integration

package exchange_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/exchange"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// projectstore.Store が受け渡しモジュールの入口を満たす（結線の型検査）。
var _ exchange.ProjectKeyStore = (*projectstore.Store)(nil)

func openTestProject(t *testing.T, keys *projectstore.ExchangeKeys) *projectstore.Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	s, err := projectstore.CreateProject(root, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
		ExchangeKeys:     keys,
	})
	if err != nil {
		t.Fatalf("プロジェクト作成に失敗: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 交換鍵を導入する前の版で作成した既存プロジェクト（exchange-keys.yaml なし）でも、
// 受け渡し操作の前に鍵ペアが生成される。
func TestEnsureProjectKeyPairOnLegacyProject(t *testing.T) {
	s := openTestProject(t, nil)
	keysPath := filepath.Join(s.Root(), projectstore.FileExchangeKeys)
	if _, err := os.Stat(keysPath); !os.IsNotExist(err) {
		t.Fatalf("前提が崩れています（鍵ファイルが既にある）: %v", err)
	}

	kp, err := exchange.EnsureProjectKeyPair(s)
	if err != nil {
		t.Fatalf("遅延生成に失敗: %v", err)
	}
	if _, err := os.Stat(keysPath); err != nil {
		t.Fatalf("鍵ファイルが作られていません: %v", err)
	}

	// 2 回目は再生成しない（発行済み質問票の返送を復号できなくなるため）。
	again, err := exchange.EnsureProjectKeyPair(s)
	if err != nil {
		t.Fatalf("2 回目の取得に失敗: %v", err)
	}
	if !bytes.Equal(kp.Public, again.Public) || !bytes.Equal(kp.Private, again.Private) {
		t.Fatalf("2 回目で鍵が変わりました（再生成されています）")
	}

	// 生成された鍵で返送方向の往復ができる。
	sealed, err := exchange.SealForRecipient([]byte("回答本文"), kp.Public)
	if err != nil {
		t.Fatalf("返送暗号化に失敗: %v", err)
	}
	got, err := exchange.OpenSealed(sealed, again)
	if err != nil {
		t.Fatalf("返送復号に失敗: %v", err)
	}
	if string(got) != "回答本文" {
		t.Fatalf("復号結果が一致しません: %q", string(got))
	}
}

// 別プロジェクトの鍵では復号できない（取り込み時の宛先違い検出）。
func TestProjectKeysAreProjectScoped(t *testing.T) {
	a := openTestProject(t, nil)
	b := openTestProject(t, nil)

	kpA, err := exchange.EnsureProjectKeyPair(a)
	if err != nil {
		t.Fatalf("A の鍵生成に失敗: %v", err)
	}
	kpB, err := exchange.EnsureProjectKeyPair(b)
	if err != nil {
		t.Fatalf("B の鍵生成に失敗: %v", err)
	}
	if bytes.Equal(kpA.Public, kpB.Public) {
		t.Fatalf("別プロジェクトで同じ鍵が生成されました")
	}

	sealed, err := exchange.SealForRecipient([]byte("A 宛の回答"), kpA.Public)
	if err != nil {
		t.Fatalf("暗号化に失敗: %v", err)
	}
	if _, err := exchange.OpenSealed(sealed, kpB); err == nil {
		t.Fatalf("別プロジェクトの鍵で復号できてしまいました")
	}
}
