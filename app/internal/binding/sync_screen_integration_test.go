//go:build integration

// 結合テスト（同期系の画面 = 同期・同期先の設定・同期先からの取得・競合の解決）。
// 実行: make -C app test-integration
//
// 2 端末（A = 佐藤 / B = 鈴木）を実際の同期先（共有フォルダ上の bare リポジトリ）でつなぎ、
// 画面が呼ぶバインディングだけを使って取り込み・反映・三面マージを通す。

package binding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// syncEnv は同期の検証に使う 2 端末の環境。
type syncEnv struct {
	a      *API
	rootA  string
	rootB  string
	remote syncmod.Remote
	client *syncmod.Client // B 側（対向の作業者）
}

// newSyncEnv は A（佐藤・オーナー）のプロジェクトへ同期先を設定し、B（鈴木・編集）を参加させる。
func newSyncEnv(t *testing.T) *syncEnv {
	t.Helper()
	stub := &streamingStub{}
	a, rootA := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(rootA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if _, err := a.AddMember(MemberRequest{
		AuthorID: suzukiID, DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	if client, err := a.syncClient(a.session); err != nil {
		t.Fatal(err)
	} else if av := client.Availability(); !av.Available {
		// 前提が揃わないときは skip せず fail させる（黙って飛ばすと検証していないのに緑になる）
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}

	location := filepath.Join(t.TempDir(), "share", "proj.git")
	if err := os.MkdirAll(filepath.Dir(location), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetSyncRemote(SetSyncRemoteRequest{
		Kind: projectstore.SyncKindFolder, Location: location}); err != nil {
		t.Fatalf("同期先を設定できない: %v", err)
	}
	// オーナーの初回反映で同期先の実体を作る
	if result, err := a.PublishSync(false); err != nil || !result.Done {
		t.Fatalf("初回の反映に失敗: %v %+v", err, result)
	}

	authorB := projectstore.Author{AuthorID: suzukiID, DisplayName: "鈴木"}
	remote := syncmod.Remote{Kind: projectstore.SyncKindFolder, Location: location}
	clientB, err := syncmod.New(syncmod.Options{Author: authorB,
		ConfigDir: filepath.Join(t.TempDir(), "syncB"), Recorder: syncRecorder{author: authorB}})
	if err != nil {
		t.Fatal(err)
	}
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	if _, err := clientB.Clone(context.Background(), syncmod.CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("B の取得に失敗: %v", err)
	}
	return &syncEnv{a: a, rootA: rootA, rootB: rootB, remote: remote, client: clientB}
}

// publishFromB は B 側で 1 ファイル書いて同期先へ反映する。
func (e *syncEnv) publishFromB(t *testing.T, rel, content string) {
	t.Helper()
	writeRecordFile(t, e.rootB, rel, content)
	if _, err := e.client.Publish(context.Background(), e.rootB, e.remote, "", syncmod.PublishOptions{}); err != nil {
		t.Fatalf("B の反映に失敗: %v", err)
	}
}

// 取り込みで他メンバーの変更が入り、結果が区分ごとの件数で示される。
func TestIncorporateSyncBringsInOtherMembersChanges(t *testing.T) {
	env := newSyncEnv(t)
	env.publishFromB(t, "decisions/DEC-900.md", "---\nid: DEC-900\n---\nB の決定\n")

	result, err := env.a.IncorporateSync(nil)
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	if !result.Done || result.Failure != nil || len(result.Conflicts) != 0 {
		t.Fatalf("取り込みが完了していない: %+v", result)
	}
	if !strings.Contains(result.Summary, "決定事項") {
		t.Errorf("取り込んだ区分が示されない: %q", result.Summary)
	}
	if _, err := os.Stat(filepath.Join(env.rootA, "decisions", "DEC-900.md")); err != nil {
		t.Errorf("取り込んだ内容が作業コピーに無い: %v", err)
	}
	// git の語を利用者向けの文言へ出さない（利用者は git を知らなくても同期できる）
	if strings.Contains(result.Notice, "git") || strings.Contains(result.Notice, "merge") {
		t.Errorf("利用者向けの文言に内部用語が出た: %q", result.Notice)
	}
}

// 同期の記録そのものを「未反映の変更」に数えない（同期のたびに指標が点灯しない）。
func TestSyncStatusIgnoresSyncLogItself(t *testing.T) {
	env := newSyncEnv(t)
	// 取り込む変更が無い取り込み（記録だけが増える）
	result, err := env.a.IncorporateSync(nil)
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	if !result.Done {
		t.Fatalf("取り込みが完了していない: %+v", result)
	}
	status, err := env.a.SyncStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.HasUnpublished {
		t.Fatalf("同期の記録だけで未反映の変更ありになった: %+v", status)
	}
	// 利用者の変更は従来どおり点灯する
	writeRecordFile(t, env.rootA, "decisions/DEC-902.md", "---\nid: DEC-902\n---\nA の決定\n")
	status, err = env.a.SyncStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.HasUnpublished || !strings.Contains(status.UnpublishedSummary, "決定事項") {
		t.Fatalf("利用者の変更が未反映として出ない: %+v", status)
	}
	// 反映の事前提示は実際に同期先へ載る内容（同期の記録を含む）を数える
	preview, err := env.a.PreviewSyncPublish()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Summary, "監査記録") {
		t.Errorf("事前提示から同期の記録が落ちた: %+v", preview)
	}
}

// 反映の前に何が載るかを提示し、確認を経て反映する。
func TestPreviewAndPublishSync(t *testing.T) {
	env := newSyncEnv(t)
	writeRecordFile(t, env.rootA, "decisions/DEC-901.md", "---\nid: DEC-901\n---\nA の決定\n")

	preview, err := env.a.PreviewSyncPublish()
	if err != nil {
		t.Fatalf("事前提示に失敗: %v", err)
	}
	if preview.Total == 0 || !strings.Contains(preview.Summary, "決定事項") {
		t.Fatalf("反映の対象が提示されない: %+v", preview)
	}
	if !strings.Contains(preview.Notice, preview.Location) {
		t.Errorf("同期先の所在が提示されない: %q", preview.Notice)
	}
	// 事前提示は同期先の内容を変えない（提示だけでは反映されない）
	if status, err := env.a.SyncStatus(); err != nil {
		t.Fatal(err)
	} else if !status.HasUnpublished {
		t.Errorf("事前提示だけで反映済みになった: %+v", status)
	}

	if result, err := env.a.PublishSync(false); err != nil || !result.Done {
		t.Fatalf("反映に失敗: %v %+v", err, result)
	}
	status, err := env.a.SyncStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.HasUnpublished {
		t.Errorf("反映後も未反映の変更が残っている: %+v", status)
	}
	if status.LastSyncedAt == "" {
		t.Errorf("最後に同期した日時が出ない: %+v", status)
	}
}

// 競合は承認するまで統合されず、作業コピーは取り込み前のまま。承認を添えて再実行すると統合される。
func TestIncorporateSyncRequiresApprovalForConflicts(t *testing.T) {
	env := newSyncEnv(t)
	// 双方が同じ用語エントリを別の内容へ変える（エントリ単位の競合）
	const termsB = "terms:\n  - name: 在庫\n    definition: B の定義\n"
	const termsA = "terms:\n  - name: 在庫\n    definition: A の定義\n"
	env.publishFromB(t, "terms.yaml", termsB)
	writeRecordFile(t, env.rootA, "terms.yaml", termsA)

	result, err := env.a.IncorporateSync(nil)
	if err != nil {
		t.Fatalf("取り込みでエラーになった（競合は結果として返すべき）: %v", err)
	}
	if result.Done || len(result.Conflicts) == 0 {
		t.Fatalf("競合が提示されない: %+v", result)
	}
	conflict := result.Conflicts[0]
	if conflict.Theirs == "" || conflict.Ours == "" || conflict.UnitLabel == "" {
		t.Errorf("三面の材料が揃っていない: %+v", conflict)
	}
	if conflict.TheirsAuthor != "鈴木" {
		t.Errorf("相手の作業者名が表示名でない: %+v", conflict)
	}
	// 承認しない間は作業コピーが変わらない（後勝ち上書きをしない）
	if got, err := readFile(filepath.Join(env.rootA, "terms.yaml")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(got, "A の定義") {
		t.Fatalf("承認前に作業コピーが書き換わった: %q", got)
	}

	// 承認（相手を採る）を添えて再実行する
	approved := []SyncResolutionInput{{ConflictID: conflict.ID, Choice: string(syncmod.ChoiceTheirs)}}
	applied, err := env.a.IncorporateSync(approved)
	if err != nil {
		t.Fatalf("承認つきの取り込みに失敗: %v", err)
	}
	if !applied.Done || applied.ConflictCount == 0 {
		t.Fatalf("承認どおりに統合されていない: %+v", applied)
	}
	if got, err := readFile(filepath.Join(env.rootA, "terms.yaml")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(got, "B の定義") {
		t.Errorf("選んだ内容が反映されていない: %q", got)
	}
}

// 選択が欠けた承認では統合しない（既定の選択を置かない）。
func TestIncorporateSyncRejectsIncompleteApproval(t *testing.T) {
	env := newSyncEnv(t)
	env.publishFromB(t, "terms.yaml", "terms:\n  - name: 在庫\n    definition: B の定義\n")
	writeRecordFile(t, env.rootA, "terms.yaml", "terms:\n  - name: 在庫\n    definition: A の定義\n")

	first, err := env.a.IncorporateSync(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Conflicts) == 0 {
		t.Fatal("競合が提示されない")
	}
	// 空の選択（未承認）を添えても統合しない
	blank := []SyncResolutionInput{{ConflictID: first.Conflicts[0].ID, Choice: ""}}
	again, err := env.a.IncorporateSync(blank)
	if err != nil {
		t.Fatal(err)
	}
	if again.Done {
		t.Fatalf("未承認のまま統合された: %+v", again)
	}
	if got, err := readFile(filepath.Join(env.rootA, "terms.yaml")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(got, "A の定義") {
		t.Errorf("未承認で作業コピーが書き換わった: %q", got)
	}
}

// 競合の解決で「未決事項として起票する」は両方の内容を含む未決事項を作り、暫定採用の側で統合する。
func TestSyncMergeOpenIssueKeepsBothContents(t *testing.T) {
	env := newSyncEnv(t)
	env.publishFromB(t, "terms.yaml", "terms:\n  - name: 在庫\n    definition: B の定義\n")
	writeRecordFile(t, env.rootA, "terms.yaml", "terms:\n  - name: 在庫\n    definition: A の定義\n")

	first, err := env.a.IncorporateSync(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Conflicts) == 0 {
		t.Fatal("競合が提示されない")
	}
	conflict := first.Conflicts[0]
	issue, err := env.a.CreateSyncMergeOpenIssue(CreateSyncMergeOpenIssueRequest{
		ConflictID: conflict.ID, Label: conflict.Label, TheirsAuthor: conflict.TheirsAuthor,
		Theirs: conflict.Theirs, Ours: conflict.Ours,
	})
	if err != nil {
		t.Fatalf("未決事項を起票できない: %v", err)
	}
	body, err := readFile(filepath.Join(env.rootA, "open-issues", issue.OpenIssueID+".md"))
	if err != nil {
		t.Fatalf("起票した未決事項が無い: %v", err)
	}
	if !strings.Contains(body, "B の定義") || !strings.Contains(body, "A の定義") {
		t.Errorf("両方の内容が本文に残っていない: %q", body)
	}

	applied, err := env.a.IncorporateSync([]SyncResolutionInput{{
		ConflictID: conflict.ID, Choice: string(syncmod.ChoiceOpenIssue),
		Adopt: string(syncmod.ChoiceOurs), OpenIssueID: issue.OpenIssueID,
	}})
	if err != nil {
		t.Fatalf("承認つきの取り込みに失敗: %v", err)
	}
	if !applied.Done {
		t.Fatalf("起票を添えた承認で統合されない: %+v", applied)
	}
	if got, err := readFile(filepath.Join(env.rootA, "terms.yaml")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(got, "A の定義") {
		t.Errorf("暫定採用した側が残っていない: %q", got)
	}
}

// 閲覧権限では反映を無効化して理由を示す（取り込みはできる）。
func TestSyncStatusDisablesPublishForViewer(t *testing.T) {
	env := newSyncEnv(t)
	// 自分（佐藤）を閲覧権限へ落とす（オーナーが 2 人目に移譲してから降格する経路の代わりに直接書く）
	demoteToViewer(t, env.rootA, satoID)

	status, err := env.a.SyncStatus()
	if err != nil {
		t.Fatalf("同期の状態を取得できない: %v", err)
	}
	if status.CanPublish {
		t.Fatalf("閲覧権限で反映が有効になっている: %+v", status)
	}
	if status.PublishReason == "" {
		t.Errorf("反映できない理由が示されない: %+v", status)
	}
	if !status.CanIncorporate {
		t.Errorf("閲覧権限で取り込みまで無効になっている: %+v", status)
	}
	if _, err := env.a.PublishSync(false); err == nil {
		t.Error("閲覧権限で反映が実行できた")
	}
}

// 外部 Git サーバは明示同意なしに保存しない（成果物が社外のサーバへ出るため）。設定はオーナーのみ。
func TestSetSyncRemoteRequiresConsentAndOwner(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	if _, err := a.SetSyncRemote(SetSyncRemoteRequest{
		Kind: projectstore.SyncKindGitExternal, Location: "https://git.example.com/x.git"}); err == nil {
		t.Fatal("同意なしに外部 Git サーバを保存できた")
	}
	// 資格情報を含む URL は受け付けない（認証情報は所在に書かず、設定で別に登録する）
	if _, err := a.SetSyncRemote(SetSyncRemoteRequest{
		Kind: projectstore.SyncKindGitExternal, Location: "https://user:pass@git.example.com/x.git",
		ExternalConsent: true}); err == nil {
		t.Fatal("認証情報を含む所在を保存できた")
	}
	if _, err := a.SetSyncRemote(SetSyncRemoteRequest{
		Kind: projectstore.SyncKindGitExternal, Location: "https://git.example.com/x.git",
		ExternalConsent: true}); err != nil {
		t.Fatalf("同意つきの保存に失敗: %v", err)
	}

	view, err := a.SyncRemote()
	if err != nil {
		t.Fatal(err)
	}
	if !view.Configured || view.Kind != projectstore.SyncKindGitExternal {
		t.Fatalf("設定が保存されていない: %+v", view)
	}
	if !view.CanManage {
		t.Fatalf("オーナーが設定できない扱いになっている: %+v", view)
	}

	demoteToViewer(t, root, satoID)
	view, err = a.SyncRemote()
	if err != nil {
		t.Fatal(err)
	}
	if view.CanManage || view.ManageReason == "" {
		t.Fatalf("閲覧権限で同期先を変更できる扱いになっている: %+v", view)
	}
	if _, err := a.SetSyncRemote(SetSyncRemoteRequest{
		Kind: projectstore.SyncKindFolder, Location: filepath.Join(t.TempDir(), "x.git")}); err == nil {
		t.Error("閲覧権限で同期先を変更できた")
	}
}

// 同期先から取得して一覧へ加え、重複取得は拒否する。
func TestCloneSyncProjectJoinsAndRejectsDuplicate(t *testing.T) {
	env := newSyncEnv(t)
	// 取得（参加）は**別の端末**で起きる操作。A はその作業コピーを既に持っているため、
	// A から取得すると「既にこの端末にある」で正しく拒まれる（開いた時点で一覧に載るため）。
	// 参加する側の端末を別に用意する（アプリ設定を別に持つ = 端末が違うことと同じ）。
	joiner, _ := newDialogueAPI(t, &streamingStub{})
	dest := filepath.Join(t.TempDir(), "joined", "proj")

	joined, err := joiner.CloneSyncProject(CloneSyncProjectRequest{
		Kind: projectstore.SyncKindFolder, Location: env.remote.Location, Dest: dest})
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	if !joined.Done || joined.Project == nil {
		t.Fatalf("取得できていない: %+v", joined)
	}
	if _, err := os.Stat(filepath.Join(dest, projectstore.FileProject)); err != nil {
		t.Fatalf("作業コピーが作られていない: %v", err)
	}
	list, err := joiner.Projects()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		if p.Path == dest {
			found = true
			if !p.SyncConfigured || p.SyncKindLabel == "" {
				t.Errorf("一覧に同期先の情報が出ない: %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("取得したプロジェクトが一覧に無い: %+v", list)
	}

	// 同じプロジェクトの 2 度目の取得は拒否する
	dup := filepath.Join(t.TempDir(), "dup", "proj")
	again, err := joiner.CloneSyncProject(CloneSyncProjectRequest{
		Kind: projectstore.SyncKindFolder, Location: env.remote.Location, Dest: dup})
	if err != nil {
		t.Fatalf("取得でエラーになった（失敗は結果として返すべき）: %v", err)
	}
	if again.Done || again.Failure == nil {
		t.Fatalf("重複取得が拒否されない: %+v", again)
	}
	if !strings.Contains(again.Notice, "既にこの端末") {
		t.Errorf("重複の理由が示されない: %q", again.Notice)
	}
	if _, err := os.Stat(dup); err == nil {
		t.Error("失敗したのに作成先が作られた")
	}
}
