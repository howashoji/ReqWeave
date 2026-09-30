//go:build integration

// 結合テスト（同期の一巡を通しても同期先の認証情報をどこにも出力しない）。
// 実行: make -C app test-integration
//
// シークレットキーの非出力の検証（一連の操作 → アプリが出力した全ファイルの全文検索）を、同期先の認証情報へ広げる。
// 一巡は 取得 → 取り込み → 反映 → 意図的な認証失敗。
// 画面が呼ぶバインディングだけを使い、終了後に作業コピー・同期先・アプリ設定領域・
// 同期モジュールの設定領域・一時領域を、平文と Base64（標準／パディングなし）で走査する。

package binding

import (
	"context"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// leakToken / leakSSHKey は実在しないテスト用の値（走査の検索語）。
const leakToken = "reqweave-dummy-roundtrip-token-do-not-use-5a2f8d"

// 取得 → 取り込み → 反映 → 認証失敗 の一巡の後、認証情報がどのファイルにも現れないこと。
func TestSyncRoundTripNeverLeaksCredential(t *testing.T) {
	// 受け渡しの一時ファイル（0600）を走査対象へ入れるため、一時領域をテスト配下へ寄せる。
	tmp := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}

	env := newSyncEnv(t)
	a := env.a
	projectID := a.session.store.Project().ProjectID
	if projectID == "" {
		t.Fatal("プロジェクト ID が空（検証の前提が成立していない）")
	}
	sshKey := testSSHPrivateKey(t)
	// テストが中断されても OS のセキュアストレージに項目を残さない。プロジェクト ID はアプリの採番のため
	// 参照名に印を入れられない。**登録より前に**記録する。
	for _, k := range []keymanager.CredentialKind{keymanager.CredentialSSHKey, keymanager.CredentialToken} {
		keytest.TrackGeneratedSync(t, keymanager.SyncRef{ProjectID: projectID, Kind: k})
	}

	// 認証情報を登録する（同期先の設定画面の経路）。
	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{
		ProjectID: projectID, Kind: "token", Username: "alice", Secret: leakToken}); err != nil {
		t.Fatalf("認証情報の登録に失敗: %v", err)
	}

	// --- 一巡 1: 取り込み（B の変更を A が取り込む） ---
	env.publishFromB(t, "decisions/DEC-901.md", "---\nid: DEC-901\n---\nB の決定\n")
	if view, err := a.IncorporateSync(nil); err != nil || !view.Done || len(view.Conflicts) != 0 {
		t.Fatalf("取り込みに失敗: %v %+v", err, view)
	}

	// --- 一巡 2: 反映（A の変更を同期先へ） ---
	writeRecordFile(t, env.rootA, "decisions/DEC-902.md", "---\nid: DEC-902\n---\nA の決定\n")
	if view, err := a.PublishSync(false); err != nil || !view.Done {
		t.Fatalf("反映に失敗: %v %+v", err, view)
	}

	// --- 一巡 3: 意図的な認証失敗 ---
	// 到達できない https の同期先へ接続を試し、登録済みの認証情報が git へ受け渡される経路を通す。
	// 失敗すること自体が期待であり、成功したらテストの前提が崩れている。
	if view, err := a.CheckSyncConnection(projectstore.SyncKindGitInternal, "https://127.0.0.1:1/team/proj.git"); err != nil || view.OK || view.Failure == nil {
		t.Fatalf("到達できない同期先への接続が失敗として返らない（前提が崩れている）: %v %+v", err, view)
	}

	// 方式を SSH 鍵へ切り替えて、同じ経路をもう一度通す（鍵素材の受け渡しも走査対象に含める）。
	if _, err := a.RegisterSyncCredential(RegisterSyncCredentialRequest{
		ProjectID: projectID, Kind: "ssh_key", Secret: sshKey}); err != nil {
		t.Fatalf("SSH 鍵の登録に失敗: %v", err)
	}
	if view, err := a.CheckSyncConnection(projectstore.SyncKindGitInternal, "ssh://git@127.0.0.1:1/team/proj.git"); err != nil || view.OK || view.Failure == nil {
		t.Fatalf("到達できない同期先への接続が失敗として返らない（前提が崩れている）: %v %+v", err, view)
	}

	// --- 走査 ---
	needles := credentialNeedles(t, leakToken, strings.TrimSpace(sshKey))
	roots := []struct {
		label string
		root  string
		// requireFiles は「走査対象が 0 件なら検査が成立していない」とみなす場所。
		// 一時領域は受け渡しの資材を後片づけした結果として空になるのが正常なため対象外にする。
		requireFiles bool
	}{
		{"作業コピー（A）", env.rootA, true},
		{"作業コピー（B）", env.rootB, true},
		{"同期先", env.remote.Location, true},
		{"アプリ設定領域", a.paths.Base, true},
		{"同期モジュールの設定領域", a.paths.SyncDir(), true},
		{"一時領域（受け渡しの資材）", tmp, false},
	}
	total := 0
	for _, r := range roots {
		total += scanForNeedles(t, r.label, r.root, needles, r.requireFiles)
	}
	if total == 0 {
		t.Fatal("走査対象のファイルが 1 件もない（検査が成立していない）")
	}
	// 一巡の成果が実在すること（空のツリーを走査して緑にしない）
	for _, c := range []struct{ label, root, rel string }{
		{"作業コピー（A）", env.rootA, "decisions/DEC-901.md"}, // B から取り込んだ決定
		{"作業コピー（A）", env.rootA, "decisions/DEC-902.md"}, // A 自身の決定（反映済み）
		{"作業コピー（B）", env.rootB, "decisions/DEC-901.md"}, // B 自身の決定
	} {
		if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(c.rel))); err != nil {
			t.Errorf("%s に一巡の成果（%s）が無い: %v", c.label, c.rel, err)
		}
	}
	// 受け渡しの一時ファイルは一巡の後に残らない
	if left, _ := filepath.Glob(filepath.Join(tmp, "reqweave-sync-*")); len(left) != 0 {
		t.Errorf("受け渡しの一時領域が残っている: %v", left)
	}
	// 保管庫からは読める（走査で 0 件だったのが「そもそも登録されていない」ためでないこと）
	got, err := a.syncCredentials().Credential(context.Background(),
		keymanager.SyncRef{ProjectID: projectID, Kind: keymanager.CredentialSSHKey})
	if err != nil || strings.TrimSpace(got.Secret) != strings.TrimSpace(sshKey) {
		t.Errorf("保管庫から認証情報を読めない（検証の前提が崩れている）: err=%v", err)
	}
}

// credentialNeedles は検索語（平文・Base64 標準・Base64 パディングなし）を作る。
func credentialNeedles(t *testing.T, secrets ...string) []string {
	t.Helper()
	var needles []string
	for _, s := range secrets {
		if strings.TrimSpace(s) == "" {
			t.Fatal("検索語が空（検証にならない）")
		}
		needles = append(needles, s,
			base64.StdEncoding.EncodeToString([]byte(s)),
			base64.RawStdEncoding.EncodeToString([]byte(s)))
	}
	return needles
}

// scanForNeedles は root 配下の全ファイルを走査し、検索語を含むものを報告する。走査したファイル数を返す。
// requireFiles が真なら、走査対象が 0 件であること自体を検査の不成立として報告する。
func scanForNeedles(t *testing.T, label, root string, needles []string, requireFiles bool) int {
	t.Helper()
	checked := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		for _, needle := range needles {
			if strings.Contains(string(b), needle) {
				rel, relErr := filepath.Rel(root, p)
				if relErr != nil {
					rel = p
				}
				t.Errorf("%s のファイルに認証情報が含まれる: %s", label, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s の走査に失敗: %v", label, err)
	}
	if requireFiles && checked == 0 {
		t.Errorf("%s に走査対象のファイルが 1 件もない（検査が成立していない）: %s", label, root)
	}
	return checked
}
