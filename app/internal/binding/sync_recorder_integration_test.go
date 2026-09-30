//go:build integration

// 結合テスト（同期の記録と、利用量の集計範囲の併記）。実行: make -C app test-integration

package binding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
	syncmod "github.com/howashoji/ReqWeave/app/internal/sync"
)

// 取得・取り込み・反映を記録し、失敗も残す。参照は作業コピー内で完結する。
func TestSyncLogRecordsOperations(t *testing.T) {
	stub := &streamingStub{}
	a, rootA := newDialogueAPI(t, stub)
	ctx := context.Background()

	if _, err := a.OpenDialogueProject(rootA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if _, err := a.AddMember(MemberRequest{
		AuthorID: suzukiID, DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	session := a.session
	client, err := a.syncClient(session)
	if err != nil {
		t.Fatal(err)
	}
	if av := client.Availability(); !av.Available {
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}
	remote := syncmod.Remote{Kind: syncmod.RemoteFolder,
		Location: filepath.Join(t.TempDir(), "share", "proj.git")}
	if err := os.MkdirAll(filepath.Dir(remote.Location), 0o755); err != nil {
		t.Fatal(err)
	}
	// 同期先を project.yaml へ設定する（同期先があるときだけ集計範囲を併記する）
	if err := session.store.UpdateProject(func(p *projectstore.Project) error {
		p.Sync = &projectstore.SyncSetting{Kind: remote.Kind, Location: remote.Location}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 1. 反映（成功）
	if _, err := client.Publish(ctx, rootA, remote, "", syncmod.PublishOptions{CreateIfAbsent: true}); err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}
	// 2. 反映（失敗 = 同期先を隠して到達できなくする）
	hidden := remote.Location + ".hidden"
	if err := os.Rename(remote.Location, hidden); err != nil {
		t.Fatal(err)
	}
	writeRecordFile(t, rootA, "decisions/DEC-500.md", "---\nid: DEC-500\n---\n決定\n")
	if _, err := client.Publish(ctx, rootA, remote, "", syncmod.PublishOptions{}); err == nil {
		t.Fatal("到達できない反映が成功した")
	}
	if err := os.Rename(hidden, remote.Location); err != nil {
		t.Fatal(err)
	}
	// 3. B が取得して反映 → A が取り込む
	authorB := projectstore.Author{AuthorID: suzukiID, DisplayName: "鈴木"}
	clientB, err := syncmod.New(syncmod.Options{Author: authorB, ConfigDir: filepath.Join(t.TempDir(), "syncB"),
		Recorder: syncRecorder{author: authorB}})
	if err != nil {
		t.Fatal(err)
	}
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	if _, err := clientB.Clone(ctx, syncmod.CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	writeRecordFile(t, rootB, "decisions/DEC-501.md", "---\nid: DEC-501\n---\nB の決定\n")
	if _, err := clientB.Publish(ctx, rootB, remote, "", syncmod.PublishOptions{}); err != nil {
		t.Fatalf("B の反映に失敗: %v", err)
	}
	if _, err := client.Incorporate(ctx, rootA, remote, ""); err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}

	// 記録の実体（audit/sync-log/ の月別 × 作業者別ファイル）
	records, err := auditlog.ReadSyncRecords(rootA, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("同期の記録を読めない: %v", err)
	}
	// `audit/` は同期の対象のため、取り込むと**他メンバーの同期の記録も届く**。
	// 自分が行った操作の記録は自分の作業コピーで作られたものだけを見る。
	ops := map[string][]string{}
	others := 0
	for _, rec := range records {
		if rec.Author == satoID {
			ops[rec.Op] = append(ops[rec.Op], rec.Result)
		} else {
			others++
		}
		if rec.RemoteKind != syncmod.RemoteFolder || rec.RemoteLocation != remote.Location {
			t.Errorf("同期先が記録されていない: %+v", rec)
		}
	}
	if others == 0 {
		t.Error("取り込みで他メンバーの同期の記録が届いていない（audit/ は同期対象）")
	}
	if len(ops["publish"]) != 2 || ops["publish"][0] != "ok" || ops["publish"][1] != "failed" {
		t.Errorf("反映の記録が違う（成功と失敗の両方が残ること）: %+v", ops["publish"])
	}
	if len(ops["incorporate"]) != 1 || ops["incorporate"][0] != "ok" {
		t.Errorf("取り込みの記録が違う: %+v", ops["incorporate"])
	}
	if len(ops["check"]) != 0 {
		t.Errorf("接続確認は記録の対象外（内容を変えない操作のため）: %+v", ops["check"])
	}
	// 失敗の記録には失敗種別が残る
	var failed *auditlog.SyncRecord
	for i := range records {
		if records[i].Result == "failed" {
			failed = &records[i]
		}
	}
	if failed == nil || failed.Failure == "" {
		t.Fatalf("失敗種別が記録されていない: %+v", failed)
	}
	// 取得は取得先（B の作業コピー）へ記録される
	bRecords, err := auditlog.ReadSyncRecords(rootB, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	foundClone := false
	for _, rec := range bRecords {
		if rec.Op == "clone" && rec.Result == "ok" && rec.Author == suzukiID {
			foundClone = true
		}
	}
	if !foundClone {
		t.Errorf("取得が取得先へ記録されていない: %+v", bRecords)
	}

	// 参照: 新しい順・表示名・日本語ラベル。git の語を出さない。
	view, err := a.SyncLog()
	if err != nil {
		t.Fatalf("同期の記録を参照できない: %v", err)
	}
	if len(view.Entries) != len(records) || view.Notice != "" {
		t.Fatalf("一覧の件数が違う: %d / %d", len(view.Entries), len(records))
	}
	if view.Entries[0].Operation != "取り込み" {
		t.Errorf("新しい順になっていない: %+v", view.Entries[0])
	}
	if view.LastIncorporation == "" {
		t.Error("最後に取り込んだ日時が取れていない")
	}
	for _, e := range view.Entries {
		if e.Author != "佐藤" && e.Author != "鈴木" {
			t.Errorf("作業者が表示名になっていない: %+v", e)
		}
		if e.Result == "" || strings.Contains(e.Result, "ok") || strings.Contains(e.Result, "failed") {
			t.Errorf("結果が生のコード値のまま: %+v", e)
		}
		if !strings.Contains(e.Remote, "共有フォルダ") {
			t.Errorf("同期先の種別が表示名になっていない: %+v", e)
		}
		for _, word := range []string{"commit", "branch", "push", "fetch", "merge", "git "} {
			if strings.Contains(strings.ToLower(e.Operation+e.Result+e.Failure+e.Summary), word) {
				t.Errorf("git の語が表示に含まれる（%s）: %+v", word, e)
			}
		}
	}

	// 利用量の集計範囲に「取り込み時点まで」を併記する。
	usage, err := a.TokenUsage(TokenUsageRequest{Path: rootA})
	if err != nil {
		t.Fatal(err)
	}
	if usage.LastIncorporation != view.LastIncorporation || usage.ScopeNotice == "" {
		t.Fatalf("集計範囲の併記が無い: %+v", usage)
	}
	if !strings.Contains(usage.ScopeNotice, "取り込") || !strings.Contains(usage.ScopeNotice, "含まれません") {
		t.Errorf("集計範囲の説明が足りない: %q", usage.ScopeNotice)
	}
}

// 同期先が未設定（単独利用）のプロジェクトでは、記録が無い旨を示し集計範囲の併記もしない。
func TestSyncLogAndUsageScopeForSoloProject(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	view, err := a.SyncLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 0 || view.Notice == "" || view.LastIncorporation != "" {
		t.Fatalf("記録が無いときの提示が違う: %+v", view)
	}
	usage, err := a.TokenUsage(TokenUsageRequest{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if usage.ScopeNotice != "" || usage.LastIncorporation != "" {
		t.Errorf("単独利用のプロジェクトに集計範囲の併記が出た: %+v", usage)
	}
}

// writeRecordFile は作業コピーへ 1 ファイル書く（同期の対象になる内容を作るため）。
func writeRecordFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
