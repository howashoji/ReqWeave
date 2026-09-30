//go:build integration

// 結合テスト（OS セキュアストレージ実体・アプリの作成ファイル全走査）。
// 実行: make -C app test-integration
//
// 実行環境の OS セキュアストレージ（macOS Keychain / Windows 資格情報マネージャー）を実際に使う。
// 利用者の実エントリと衝突しないよう、プロバイダ ID にテスト専用の接頭辞と UUID を用い、
// 後始末で必ず削除する。実キーは使わない（ダミー値のみ）。

package keymanager_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// dummySecret は実キーではないテスト用の値（アプリのファイルを全文検索するときの検索対象）。
const dummySecret = "reqweave-dummy-secret-do-not-use-9f3a2b1c"

func testRef(t *testing.T) keymanager.Ref {
	t.Helper()
	ref := keymanager.Ref{ProviderID: keytest.Marker + uuid.NewString(), Label: "integration"}
	keytest.TrackKey(t, ref)
	return ref
}

// OS セキュアストレージへ直接保存され、読み出し・削除ができること。
func TestRealSecureStorageRoundTrip(t *testing.T) {
	m := keymanager.New()
	ref := testRef(t)
	t.Cleanup(func() { _ = m.Delete(ref) })

	if ok, err := m.Exists(ref); err != nil || ok {
		t.Fatalf("未登録なのに存在した: %v %v", ok, err)
	}
	if err := m.Register(ref, dummySecret); err != nil {
		t.Fatalf("OS セキュアストレージへ登録できません: %v", err)
	}
	got, err := m.SecretKey(context.Background(), ref)
	if err != nil {
		t.Fatalf("読み出せません: %v", err)
	}
	if got != dummySecret {
		t.Errorf("読み出した値が違う（マスクして表示）: %q", keymanager.MaskKey(got))
	}
	if ok, err := m.Exists(ref); err != nil || !ok {
		t.Errorf("登録済みなのに存在しない: %v %v", ok, err)
	}

	// エントリ削除後は「キー未設定」として振る舞う
	if err := m.Delete(ref); err != nil {
		t.Fatalf("削除できません: %v", err)
	}
	if _, err := m.SecretKey(context.Background(), ref); !errors.Is(err, keymanager.ErrKeyNotSet) {
		t.Errorf("削除後が「キー未設定」でない: %v", err)
	}
}

// アプリが作成・更新したファイルを全文検索して
// キー本体が平文・Base64 のいずれでも検出されないこと。
func TestKeyNeverAppearsInAppFiles(t *testing.T) {
	m := keymanager.New()
	ref := testRef(t)
	t.Cleanup(func() { _ = m.Delete(ref) })
	if err := m.Register(ref, dummySecret); err != nil {
		t.Fatalf("登録できません: %v", err)
	}

	base := t.TempDir()
	paths := projectstore.AppPaths{Base: filepath.Join(base, "appdata")}
	projectRoot := filepath.Join(base, "project")

	// 一連の操作: アプリ設定の保存 → プロジェクト作成 → 監査記録
	settings := projectstore.NewSettings()
	if err := settings.RegisterAuthor("k.sato@example.co.jp", "佐藤"); err != nil {
		t.Fatal(err)
	}
	settings.Providers = []projectstore.ProviderSetting{{
		Label: "本番用", Provider: "anthropic", Model: "claude-x",
		Effort: projectstore.EffortStandard, KeyRef: ref.String(),
	}}
	settings.DefaultProvider = "本番用"
	if err := projectstore.SaveSettings(paths, settings); err != nil {
		t.Fatal(err)
	}

	store, err := projectstore.CreateProject(projectRoot, projectstore.CreateOptions{
		TargetSystemName: "在庫管理システム",
		Author:           projectstore.Author{AuthorID: "k.sato@example.co.jp", DisplayName: "佐藤"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logger, err := auditlog.New(store, "k.sato@example.co.jp")
	if err != nil {
		t.Fatal(err)
	}
	rec := auditlog.AISendRecord{Provider: "anthropic", Model: "claude-x", Prompt: "テスト送信",
		Included: []string{"project.yaml"}}
	sendID, err := logger.RecordAISend(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordAIUsage(sendID, 1, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordChange(auditlog.ChangeRecord{Target: "DEC-001", Change: auditlog.ChangeCreated}); err != nil {
		t.Fatal(err)
	}

	needles := []string{
		dummySecret,
		base64.StdEncoding.EncodeToString([]byte(dummySecret)),
		base64.RawStdEncoding.EncodeToString([]byte(dummySecret)),
	}
	var checked int
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		rel, _ := filepath.Rel(base, p)
		for _, needle := range needles {
			if strings.Contains(string(b), needle) {
				t.Errorf("アプリが作成したファイルにキーが含まれる: %s", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("走査に失敗: %v", err)
	}
	if checked == 0 {
		t.Fatal("走査対象のファイルが 1 件もない（検査が成立していない）")
	}
	// 参照名は保持してよい（参照名からキー本体は復元できない）
	raw, err := os.ReadFile(paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), ref.String()) {
		t.Errorf("アプリ設定にキー参照名が保持されていない:\n%s", raw)
	}
}
