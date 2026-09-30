package sync

// 復元した作業コピーの取り直し。
//
// 自動退避の zip は `.git/` を含めない。そこから復元した作業コピーが
//   - 同期先から管理情報を取り直せること（復元した内容を書き換えない）
//   - 取り直しの直後は、利用者の確認なしに同期先へ書かないこと
//   - 取り込みは確認なしで行えること（同期先へ書かないため）
// を、実際の git と共有フォルダ相当の bare リポジトリで確かめる。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// recordSync は同期の記録を 1 件書く（作業コピーが「同期したことがある」状態を作る）。
func recordSync(t *testing.T, root string, author projectstore.Author) {
	t.Helper()
	store, err := projectstore.Open(root, author)
	if err != nil {
		t.Fatalf("記録のためにプロジェクトを開けない: %v", err)
	}
	defer store.Close()
	logger, err := auditlog.New(store, author.AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordSync(auditlog.SyncRecord{
		At: time.Now().UTC(), Author: author.AuthorID, Op: "publish",
		RemoteKind: "folder", Result: "ok",
	}); err != nil {
		t.Fatalf("同期の記録を書けない: %v", err)
	}
}

// dropGitDir は自動退避からの復元を模す（同期の管理情報だけが無い作業コピー）。
func dropGitDir(t *testing.T, root string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, gitDirName)); err != nil {
		t.Fatal(err)
	}
}

// remoteBranchSHA は同期先の当該ブランチの位置を返す（同期先が書き換わっていないことの確認用）。
func remoteBranchSHA(t *testing.T, c *Client, remote Remote, branch string) string {
	t.Helper()
	out, err := c.git.run(context.Background(), filepath.Dir(remote.Location), nil, nil,
		"--git-dir", remote.Location, "show-ref", "--hash", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return out
}

// 自動退避から復元した作業コピー（`.git/` が無い）は取り直しの対象。
// まだ一度も同期していない作業コピーは対象外（通常の初回反映で管理情報を作る）。
func TestNeedsReattachOnlyForSyncedCopyWithoutManagementInfo(t *testing.T) {
	_, _, _, rootB, _ := setupShared(t)
	recordSync(t, rootB, authorB)
	if NeedsReattach(rootB) {
		t.Error("管理情報がある作業コピーが取り直しの対象になった")
	}
	dropGitDir(t, rootB)
	if !NeedsReattach(rootB) {
		t.Error("復元した作業コピー（同期の記録あり・管理情報なし）が取り直しの対象にならない")
	}

	// 一度も同期していない作業コピー（記録が無い）は対象外
	fresh := filepath.Join(t.TempDir(), "fresh", "proj")
	newProject(t, fresh, authorA)
	if NeedsReattach(fresh) {
		t.Error("まだ同期していない作業コピーが取り直しの対象になった")
	}
}

// 取り直しは同期先から管理情報を再構成し、復元した内容を
// 「自分のブランチへの未反映の変更」として載せる。同期先へは書かない。
func TestReattachRebuildsManagementInfoWithoutWritingRemote(t *testing.T) {
	_, b, rootA, rootB, remote := setupShared(t)
	writeProjectFile(t, rootB, "requirements/FR-INV-010.md", "---\nid: FR-INV-010\n---\n在庫を数える\n")
	mustPublish(t, b, rootB, remote, false)
	recordSync(t, rootB, authorB)
	before := remoteBranchSHA(t, b, remote, BranchName(authorB.AuthorID))
	if before == "" {
		t.Fatal("同期先に自分のブランチが無い（前提が崩れている）")
	}

	// 自動退避からの復元を模す: 管理情報だけが無く、内容はそのまま。
	// 復元後に加えた変更（未反映）も含む。
	dropGitDir(t, rootB)
	writeProjectFile(t, rootB, "decisions/DEC-010.md", "---\nid: DEC-010\n---\n復元後の決定\n")

	result, err := b.Reattach(context.Background(), rootB, remote, "")
	if err != nil {
		t.Fatalf("取り直しに失敗: %v", err)
	}
	if !result.Reconstructed || !result.RestorePending {
		t.Errorf("取り直しの結果が違う: %+v", result)
	}
	if result.Summary.Total() == 0 {
		t.Error("復元後の変更が未反映として数えられていない")
	}
	// 作業ツリー（復元した内容）は変わらない
	if got := readProjectFile(t, rootB, "requirements/FR-INV-010.md"); got == "" {
		t.Error("取り直しで復元した内容が失われた")
	}
	if got := readProjectFile(t, rootB, "decisions/DEC-010.md"); got == "" {
		t.Error("取り直しで復元後の変更が失われた")
	}
	// 同期先は書き換わっていない
	if after := remoteBranchSHA(t, b, remote, BranchName(authorB.AuthorID)); after != before {
		t.Errorf("取り直しで同期先が書き換わった: %q → %q", before, after)
	}
	// A 側の内容が消えていない（同期先の位置から索引を合わせている）
	if _, err := os.Stat(filepath.Join(rootA, projectstore.FileProject)); err != nil {
		t.Fatal(err)
	}

	// 取り直しは記録に残る（取得として）
	if pending, err := b.RestorePending(context.Background(), rootB); err != nil || !pending {
		t.Errorf("反映の確認待ちになっていない: %v %v", pending, err)
	}
}

// 復元した内容は、利用者の確認なしに同期先へ書かない。
// 確認を添えた反映は通り、以後は確認を求めない。
func TestPublishAfterRestoreRequiresAcknowledgement(t *testing.T) {
	_, b, _, rootB, remote := setupShared(t)
	writeProjectFile(t, rootB, "requirements/FR-INV-011.md", "---\nid: FR-INV-011\n---\n入出庫を記録する\n")
	mustPublish(t, b, rootB, remote, false)
	recordSync(t, rootB, authorB)
	before := remoteBranchSHA(t, b, remote, BranchName(authorB.AuthorID))

	dropGitDir(t, rootB)
	writeProjectFile(t, rootB, "requirements/FR-INV-011.md", "---\nid: FR-INV-011\n---\n入出庫を記録する（復元後に修正）\n")

	// 確認なしの反映は中止し、同期先を書き換えない（取り直しは内部で済ませる）
	_, err := b.Publish(context.Background(), rootB, remote, "", PublishOptions{})
	expectFailure(t, err, FailRestoreUnconfirmed)
	if after := remoteBranchSHA(t, b, remote, BranchName(authorB.AuthorID)); after != before {
		t.Errorf("確認なしの反映で同期先が書き換わった: %q → %q", before, after)
	}
	// 再実行しても通らない（目印は反映の成功まで残る）
	_, err = b.Publish(context.Background(), rootB, remote, "", PublishOptions{})
	expectFailure(t, err, FailRestoreUnconfirmed)

	// 事前提示は「復元した内容を載せようとしている」ことを示す
	preview, err := b.PreviewPublish(context.Background(), rootB, remote)
	if err != nil {
		t.Fatalf("事前提示に失敗: %v", err)
	}
	if !preview.RestorePending {
		t.Error("事前提示に復元の確認待ちが出ていない")
	}
	if preview.Summary.Total() == 0 {
		t.Error("事前提示に載る内容が数えられていない")
	}

	// 確認を添えると反映でき、以後は確認を求めない
	if _, err := b.Publish(context.Background(), rootB, remote, "", PublishOptions{AcknowledgeRestore: true}); err != nil {
		t.Fatalf("確認つきの反映に失敗: %v", err)
	}
	if after := remoteBranchSHA(t, b, remote, BranchName(authorB.AuthorID)); after == before || after == "" {
		t.Errorf("確認つきの反映が同期先へ載っていない: %q → %q", before, after)
	}
	if pending, err := b.RestorePending(context.Background(), rootB); err != nil || pending {
		t.Errorf("反映後も確認待ちが残っている: %v %v", pending, err)
	}
}

// 取り込みは同期先へ書かないため、復元直後でも確認を求めずに実行できる。
// 復元した内容と他メンバーの変更の双方が残る。
func TestIncorporateReattachesRestoredCopy(t *testing.T) {
	a, b, rootA, rootB, remote := setupShared(t)
	recordSync(t, rootB, authorB)
	writeProjectFile(t, rootA, "decisions/DEC-020.md", "---\nid: DEC-020\n---\nA の決定\n")
	mustPublish(t, a, rootA, remote, false)

	dropGitDir(t, rootB)
	writeProjectFile(t, rootB, "requirements/FR-INV-020.md", "---\nid: FR-INV-020\n---\nB の復元後の要件\n")

	result := mustIncorporate(t, b, rootB, remote)
	if result == nil {
		t.Fatal("取り込みの結果が無い")
	}
	if got := readProjectFile(t, rootB, "decisions/DEC-020.md"); got == "" {
		t.Error("取り込みで他メンバーの変更が入っていない")
	}
	if got := readProjectFile(t, rootB, "requirements/FR-INV-020.md"); got == "" {
		t.Error("取り込みで復元した作業コピーの変更が失われた")
	}
}

// 事前提示は同期先へ接続しないため、取り直しの前は
// 「先に取り直しが要る」ことだけを示す（管理情報を作らない）。
func TestPreviewPublishAsksForReattachBeforeTouchingWorkCopy(t *testing.T) {
	_, b, _, rootB, remote := setupShared(t)
	recordSync(t, rootB, authorB)
	dropGitDir(t, rootB)

	preview, err := b.PreviewPublish(context.Background(), rootB, remote)
	if err != nil {
		t.Fatalf("事前提示に失敗: %v", err)
	}
	if !preview.NeedsReattach {
		t.Error("事前提示が取り直しの必要を示していない")
	}
	if _, err := os.Stat(filepath.Join(rootB, gitDirName)); !os.IsNotExist(err) {
		t.Error("事前提示が管理情報を作ってしまった（同期先へ接続しない約束を破っている）")
	}
}

// 退避より後に同期先へ載った内容を、古い退避からの復元が
// 「削除」として反映しない（同期先にあって復元先に無いファイルは取り直しで戻る）。
func TestReattachDoesNotTurnNewerRemoteContentIntoDeletions(t *testing.T) {
	_, b, _, rootB, remote := setupShared(t)
	writeProjectFile(t, rootB, "requirements/FR-INV-030.md", "---\nid: FR-INV-030\n---\n退避に含まれる要件\n")
	mustPublish(t, b, rootB, remote, false)
	// 退避の後に反映した内容（復元した作業コピーには入っていない）
	writeProjectFile(t, rootB, "requirements/FR-INV-031.md", "---\nid: FR-INV-031\n---\n退避より後の要件\n")
	mustPublish(t, b, rootB, remote, false)
	recordSync(t, rootB, authorB)

	// 古い退避からの復元を模す: 管理情報が無く、後から足した要件も入っていない。
	dropGitDir(t, rootB)
	if err := os.Remove(filepath.Join(rootB, "requirements", "FR-INV-031.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := b.Reattach(context.Background(), rootB, remote, ""); err != nil {
		t.Fatalf("取り直しに失敗: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootB, "requirements", "FR-INV-031.md")); err != nil {
		t.Errorf("退避より後に反映した内容が戻っていない: %v", err)
	}
	preview, err := b.PreviewPublish(context.Background(), rootB, remote)
	if err != nil {
		t.Fatalf("事前提示に失敗: %v", err)
	}
	for cat, counts := range preview.Summary {
		if counts.Removed != 0 {
			t.Errorf("復元によって削除が反映されようとしている（%s: %d 件）", cat, counts.Removed)
		}
	}
}
