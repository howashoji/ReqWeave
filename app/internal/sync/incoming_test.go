package sync

// 取り込みで作業コピーへ入った内容の取り出し。
//
// 確かめること:
//   - 変更されたパスが取れる（派生インデックスの差分再構築の入力）
//   - 入ってきた**変更履歴レコード**が取れる（変更要約の入力）。単位は取り込みであり
//     日時ではない（相手が過去に書いた記録が、今回の取り込みで入る）

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// writeHistory は変更履歴（月別 × 作業者別 NDJSON）へ 1 件追記する。
func writeHistory(t *testing.T, root string, author projectstore.Author, rec auditlog.ChangeRecord) {
	t.Helper()
	store, err := projectstore.Open(root, author)
	if err != nil {
		t.Fatalf("作業コピーを開けない: %v", err)
	}
	defer store.Close()
	logger, err := auditlog.New(store, author.AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordChange(rec); err != nil {
		t.Fatalf("変更履歴を書けない: %v", err)
	}
}

// 取り込みで入った変更履歴レコードと変更パスが取れる。
func TestIncorporateReportsIncomingChanges(t *testing.T) {
	a, b, rootA, rootB, remote := setupShared(t)

	// A が**過去の日時**で記録を書いて反映する（日時で区切ると落ちる記録）。
	old := time.Now().UTC().Add(-72 * time.Hour)
	writeHistory(t, rootA, authorA, auditlog.ChangeRecord{At: old, Author: authorA.AuthorID,
		Target: "DEC-001", Change: auditlog.ChangeCreated, After: "在庫は受注確定時に引き当てる。"})
	writeProjectFile(t, rootA, "decisions/DEC-001.md", "---\nid: DEC-001\n---\n決定\n")
	mustPublish(t, a, rootA, remote, false)

	// B 自身の記録は「入ってきたもの」ではない（自分の作業コピーに元からある）。
	writeHistory(t, rootB, authorB, auditlog.ChangeRecord{At: time.Now().UTC(), Author: authorB.AuthorID,
		Target: "ISS-001", Change: auditlog.ChangeCreated, After: "B が起票"})

	res := mustIncorporate(t, b, rootB, remote)
	if res.Incoming == nil {
		t.Fatal("取り込みで入った内容が返っていない")
	}
	in := res.Incoming
	if in.NoStartingPoint || in.ID == "" || in.Since == "" {
		t.Fatalf("起点・現在の位置が取れていない: %+v", in)
	}
	if !containsPath(in.Paths, "decisions/DEC-001.md") {
		t.Errorf("変更パスに取り込んだレコードが無い: %v", in.Paths)
	}
	if len(in.Changes) != 1 {
		t.Fatalf("入ってきた変更履歴の件数が違う: %+v", in.Changes)
	}
	got := in.Changes[0]
	if got.Target != "DEC-001" || got.Author != authorA.AuthorID {
		t.Errorf("入ってきた変更履歴が違う: %+v", got)
	}
	if !got.At.Equal(old.Truncate(time.Second)) && got.At.Sub(old).Abs() > time.Second {
		t.Errorf("記録の日時が保たれていない（取り込みの日時に置き換わっている）: %v ← %v", got.At, old)
	}
	// 取り込む変更が無ければ空（画面は「変更なし」を示す）。
	again := mustIncorporate(t, b, rootB, remote)
	if again.Incoming == nil || !again.Incoming.IsEmpty() {
		t.Errorf("変更が無いのに入ってきた扱いになった: %+v", again.Incoming)
	}
}

// 起点が無い・別の作業コピーの位置を渡した場合は、全期間を対象にせず対象なしで返す。
func TestIncomingWithoutStartingPoint(t *testing.T) {
	a, _, rootA, _, remote := setupShared(t)
	ctx := context.Background()
	writeHistory(t, rootA, authorA, auditlog.ChangeRecord{At: time.Now().UTC(), Author: authorA.AuthorID,
		Target: "DEC-001", Change: auditlog.ChangeCreated, After: "決定"})
	mustPublish(t, a, rootA, remote, false)

	for _, since := range []string{"", "0123456789012345678901234567890123456789"} {
		in, err := a.Incoming(ctx, rootA, since)
		if err != nil {
			t.Fatalf("取得に失敗（since=%q）: %v", since, err)
		}
		if !in.NoStartingPoint || len(in.Changes) != 0 || len(in.Paths) != 0 {
			t.Errorf("起点が無いのに変更を返した（since=%q）: %+v", since, in)
		}
	}

	// 同期先を設定していない作業コピー（単独利用）でも失敗させない。
	solo := filepath.Join(t.TempDir(), "solo")
	newProject(t, solo, authorA)
	in, err := a.Incoming(ctx, solo, "")
	if err != nil {
		t.Fatalf("単独利用の作業コピーで失敗した: %v", err)
	}
	if !in.NoStartingPoint {
		t.Errorf("取り込みの無い作業コピーで起点があることになった: %+v", in)
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
