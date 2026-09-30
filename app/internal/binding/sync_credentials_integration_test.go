//go:build integration

package binding

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
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// dummySyncToken は実トークンではないテスト用の値（出力ファイルの全文検索の検索語）。
const dummySyncToken = "reqweave-dummy-sync-token-do-not-use-3e8b71"

func newSyncCredentialAPI(t *testing.T) (*API, string) {
	t.Helper()
	a := &API{paths: projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}}
	projectID := keytest.Marker + uuid.NewString()
	// テストが中断されても OS のセキュアストレージに項目を残さない。
	for _, k := range []keymanager.CredentialKind{keymanager.CredentialSSHKey, keymanager.CredentialToken} {
		keytest.TrackSync(t, keymanager.SyncRef{ProjectID: projectID, Kind: k})
	}
	return a, projectID
}

// 登録・更新・削除が OS セキュアストレージにのみ保管され、
// 画面へは登録済みの有無とマスク表記だけが返り、アプリ設定には参照名のみが残ること。
func TestSyncCredentialRegisterUpdateDelete(t *testing.T) {
	a, projectID := newSyncCredentialAPI(t)

	view, err := a.SyncCredentialView(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Registered || view.State != "未設定" || view.Kind != "" || len(view.Kinds) != 2 {
		t.Errorf("未登録の表示が違う: %+v", view)
	}

	view, err = a.RegisterSyncCredential(RegisterSyncCredentialRequest{
		ProjectID: projectID, Kind: "token", Username: "alice", Secret: dummySyncToken})
	if err != nil {
		t.Fatalf("登録に失敗: %v", err)
	}
	if !view.Registered || view.Kind != "token" || view.KindLabel != "アクセストークン" || view.State != "登録済み" || view.Masked != "••••••••" {
		t.Errorf("登録後の表示が違う: %+v", view)
	}

	// アプリ設定には参照名のみ（値・利用者名は書かれない）
	raw, err := readFile(a.paths.SettingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"`+projectID+`": "`+projectID+`/token"`) {
		t.Errorf("参照名が保存されていない:\n%s", raw)
	}
	if strings.Contains(raw, dummySyncToken) || strings.Contains(raw, "alice") {
		t.Error("アプリ設定に認証情報の値・利用者名が書かれた")
	}
	// 保存は常に現行の形式バージョンで行う（テストの期待値は版が上がるたびに書き換えない）
	if !strings.Contains(raw, `"settings_version": "`+projectstore.CurrentSettingsVersion+`"`) {
		t.Errorf("アプリ設定の形式バージョンが現行（%s）でない:\n%s", projectstore.CurrentSettingsVersion, raw)
	}

	// 保管庫からは同期モジュールだけが値を読める
	got, err := a.syncCredentials().Credential(context.Background(), keymanager.SyncRef{ProjectID: projectID, Kind: keymanager.CredentialToken})
	if err != nil || got.Secret != dummySyncToken || got.Username != "alice" {
		t.Errorf("保管庫の値が違う（値は伏せる）: err=%v user=%q", err, got.Username)
	}

	// 方式を変えて更新すると古いエントリは消える（複製を残さない）
	key := testSSHPrivateKey(t)
	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{ProjectID: projectID, Kind: "ssh_key", Secret: key}); err != nil {
		t.Fatalf("更新に失敗: %v", err)
	}
	if ok, _ := a.syncCredentials().Exists(keymanager.SyncRef{ProjectID: projectID, Kind: keymanager.CredentialToken}); ok {
		t.Error("方式を変えた後も古いエントリが残っている")
	}
	view, _ = a.SyncCredentialView(projectID)
	if !view.Registered || view.Kind != "ssh_key" || view.KindLabel != "SSH 鍵" {
		t.Errorf("更新後の表示が違う: %+v", view)
	}

	// 削除で「未登録」へ戻る
	if err := a.DeleteSyncCredential(projectID); err != nil {
		t.Fatalf("削除に失敗: %v", err)
	}
	view, _ = a.SyncCredentialView(projectID)
	if view.Registered || view.State != "未設定" {
		t.Errorf("削除後の表示が違う: %+v", view)
	}
	raw, _ = readFile(a.paths.SettingsFile())
	if strings.Contains(raw, projectID) {
		t.Errorf("削除後も参照名が残っている:\n%s", raw)
	}
	if _, err := a.syncCredentials().Credential(context.Background(), keymanager.SyncRef{ProjectID: projectID, Kind: keymanager.CredentialSSHKey}); !errors.Is(err, keymanager.ErrSyncCredentialNotSet) {
		t.Errorf("削除後の保管庫が「未登録」でない: %v", err)
	}
	if err := a.DeleteSyncCredential(projectID); err != nil {
		t.Errorf("二重削除がエラー: %v", err)
	}
}

// 不正な値（パスフレーズ付き鍵・改行入りトークン）は保存前に拒否し、変更前の状態を保つ（AI のシークレットキーの登録と同じ方針）。
func TestSyncCredentialRegisterKeepsPreviousOnFailure(t *testing.T) {
	a, projectID := newSyncCredentialAPI(t)
	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{ProjectID: projectID, Kind: "token", Secret: dummySyncToken}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{ProjectID: projectID, Kind: "ssh_key", Secret: "not a key"}); err == nil {
		t.Fatal("不正な鍵が受理された")
	}
	view, _ := a.SyncCredentialView(projectID)
	if !view.Registered || view.Kind != "token" {
		t.Errorf("失敗した更新で変更前の登録が失われた: %+v", view)
	}
}

// 登録 → アプリ設定の保存 → 受け渡し資材の作成と後片づけ
// を経て、アプリが作成・更新した全ファイルを平文・Base64 で全文検索して検出 0 件であること。
// （同期の一巡 = 取得 → 取り込み → 反映 → 認証失敗 を通した検査は sync_credential_leak_integration_test.go が行う）
func TestSyncCredentialNeverAppearsInAppFiles(t *testing.T) {
	a, projectID := newSyncCredentialAPI(t)
	tmp := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}
	key := testSSHPrivateKey(t)

	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{ProjectID: projectID, Kind: "token", Username: "alice", Secret: dummySyncToken}); err != nil {
		t.Fatal(err)
	}
	cred, err := a.syncCredentials().Credential(context.Background(), keymanager.SyncRef{ProjectID: projectID, Kind: keymanager.CredentialToken})
	if err != nil {
		t.Fatal(err)
	}
	h, err := syncmod.PrepareHandoff(cred, "https://git.example.co.jp/team/proj.git")
	if err != nil {
		t.Fatal(err)
	}
	// 受け渡し中は一時ファイルにだけ値がある（0600）。完了後は残らない
	if err := h.Cleanup(); err != nil {
		t.Fatal(err)
	}

	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{ProjectID: projectID, Kind: "ssh_key", Secret: key}); err != nil {
		t.Fatal(err)
	}
	cred, err = a.syncCredentials().Credential(context.Background(), keymanager.SyncRef{ProjectID: projectID, Kind: keymanager.CredentialSSHKey})
	if err != nil {
		t.Fatal(err)
	}
	h, err = syncmod.PrepareHandoff(cred, "git@git.example.co.jp:team/proj.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Cleanup(); err != nil {
		t.Fatal(err)
	}

	var needles []string
	for _, secret := range []string{dummySyncToken, strings.TrimSpace(key)} {
		needles = append(needles, secret,
			base64.StdEncoding.EncodeToString([]byte(secret)),
			base64.RawStdEncoding.EncodeToString([]byte(secret)))
	}
	var checked int
	for _, root := range []string{a.paths.Base, tmp} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
			for _, needle := range needles {
				if strings.Contains(string(b), needle) {
					t.Errorf("アプリが作成したファイルに認証情報が含まれる: %s", p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("走査に失敗: %v", err)
		}
	}
	if checked == 0 {
		t.Fatal("走査対象のファイルが 1 件もない（検査が成立していない）")
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "reqweave-sync-*")); len(left) != 0 {
		t.Errorf("受け渡しの一時領域が残っている: %v", left)
	}
}
