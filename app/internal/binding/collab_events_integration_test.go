//go:build integration

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

// changesOf は対象・種別に一致する変更履歴をすべて返す。
func changesOf(t *testing.T, root, kind string) []auditlog.ChangeRecord {
	t.Helper()
	all, err := auditlog.ReadChanges(root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var out []auditlog.ChangeRecord
	for _, c := range all {
		if c.Change == kind {
			out = append(out, c)
		}
	}
	return out
}

// 開く・閉じるが記録され、端末紐づけは初回だけ記録される。
func TestCollabOpenCloseAndAuthorBinding(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatalf("プロジェクトを開けない: %v", err)
	}
	opened := changesOf(t, root, auditlog.ChangeProjectOpened)
	if len(opened) != 1 {
		t.Fatalf("開いた記録が違う: %+v", opened)
	}
	if opened[0].Author != "k.sato@example.co.jp" || opened[0].After != "佐藤" {
		t.Errorf("作業者が記録されていない: %+v", opened[0])
	}

	// 端末紐づけは初回のみ（OS ユーザー名つき）。
	bindings := changesOf(t, root, auditlog.ChangeAuthorBinding)
	if len(bindings) != 1 {
		t.Fatalf("端末紐づけの記録が違う: %+v", bindings)
	}
	osUser := bindings[0].OSUser
	if osUser == "" {
		t.Fatalf("OS ユーザー名が記録されていない: %+v", bindings[0])
	}
	if osUser != strings.ToLower(osUser) {
		t.Errorf("OS ユーザー名が正規化されていない: %q", osUser)
	}

	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
	closed := changesOf(t, root, auditlog.ChangeProjectClosed)
	if len(closed) != 1 {
		t.Fatalf("閉じた記録が違う: %+v", closed)
	}

	// 2 回目の開閉では紐づけを再記録しない（開閉の記録は増える）。
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
	if got := changesOf(t, root, auditlog.ChangeAuthorBinding); len(got) != 1 {
		t.Errorf("端末紐づけが再記録された: %+v", got)
	}
	if got := changesOf(t, root, auditlog.ChangeProjectOpened); len(got) != 2 {
		t.Errorf("2 回目の開いた記録が無い: %+v", got)
	}
	if got := changesOf(t, root, auditlog.ChangeProjectClosed); len(got) != 2 {
		t.Errorf("2 回目の閉じた記録が無い: %+v", got)
	}

	// OS ユーザー名を含むのは author-binding だけ（端末固有の情報を書いてよい例外を広げない）。
	all, err := auditlog.ReadChanges(root, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.Change != auditlog.ChangeAuthorBinding && c.OSUser != "" {
			t.Errorf("author-binding 以外に OS ユーザー名が含まれる: %+v", c)
		}
	}
}

// 別の OS ユーザー名の紐づけが既にある状態では、自分の端末の紐づけが新たに記録される。
func TestCollabAuthorBindingPerTerminal(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)

	// 別端末（別の OS ユーザー名）での紐づけが履歴にある状態を作る。
	store, err := a.openProject(root)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := auditlog.New(store, "k.sato@example.co.jp")
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.RecordChange(auditlog.ChangeRecord{
		At: time.Now().UTC().Add(-time.Hour), Author: "k.sato@example.co.jp",
		Target: store.Project().ProjectID, Change: auditlog.ChangeAuthorBinding,
		After: "佐藤", OSUser: "other-terminal-user",
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	bindings := changesOf(t, root, auditlog.ChangeAuthorBinding)
	if len(bindings) != 2 {
		t.Fatalf("別端末の紐づけが記録されていない: %+v", bindings)
	}
}

// テスト内の作業者（newDialogueAPI が登録する A と、同期で対向に置く B）。
const (
	satoID   = "k.sato@example.co.jp"
	suzukiID = "y.suzuki@example.co.jp"
)

// **取り込みで自分の作業コピーへ入った**他の作業者の変更を区分別に提示する。
//
// 2 端末（A = 佐藤 / B = 鈴木）を実際の同期先（共有フォルダ相当の bare リポジトリ）でつなぎ、
// B の変更を A が取り込んで要約を得るところまでを通す。日時ではなく**取り込みが単位**であることを、
// 「取り込む前は対象なし → 取り込んだ後に提示 → 確認したら次は対象なし」で確かめる。
func TestChangeSummaryOfIncorporation(t *testing.T) {
	stub := &streamingStub{}
	a, rootA := newDialogueAPI(t, stub)
	ctx := context.Background()

	// A のプロジェクトへ B をメンバー登録する。
	if _, err := a.OpenDialogueProject(rootA); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddMember(MemberRequest{
		AuthorID: suzukiID, DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	sessionA := a.session

	// 同期先へ反映し、B が取得する。
	clientA, err := a.syncClient(sessionA)
	if err != nil {
		t.Fatal(err)
	}
	if av := clientA.Availability(); !av.Available {
		t.Fatalf("この環境に git が無いため検証できない: %s", av.Reason)
	}
	remote := syncmod.Remote{Kind: syncmod.RemoteFolder,
		Location: filepath.Join(t.TempDir(), "share", "proj.git")}
	if err := os.MkdirAll(filepath.Dir(remote.Location), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := clientA.Publish(ctx, rootA, remote, "", syncmod.PublishOptions{CreateIfAbsent: true}); err != nil {
		t.Fatalf("反映に失敗: %v", err)
	}
	authorB := projectstore.Author{AuthorID: suzukiID, DisplayName: "鈴木"}
	configB := filepath.Join(t.TempDir(), "syncB")
	clientB, err := syncmod.New(syncmod.Options{Author: authorB, ConfigDir: configB})
	if err != nil {
		t.Fatal(err)
	}
	rootB := filepath.Join(t.TempDir(), "B", "proj")
	if _, err := clientB.Clone(ctx, syncmod.CloneOptions{Remote: remote, Dest: rootB}); err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	storeB, err := projectstore.Open(rootB, authorB)
	if err != nil {
		t.Fatal(err)
	}
	// B も番号帯の確保を配線する（反映の直前に確保される）。
	clientB, err = syncmod.New(syncmod.Options{Author: authorB, ConfigDir: configB,
		Ranges: projectstore.IDRangeReserver{Store: storeB}})
	if err != nil {
		t.Fatal(err)
	}
	// B が自分の番号帯を確保する（反映して初めて有効になる）。
	if _, err := clientB.Publish(ctx, rootB, remote, "", syncmod.PublishOptions{}); err != nil {
		t.Fatalf("B の初回反映に失敗: %v", err)
	}

	// まだ取り込んでいないので対象なし（**過去の全変更を「前回以降」として見せない**）。
	first, err := a.ChangeSummary()
	if err != nil {
		t.Fatal(err)
	}
	if !first.NoIncorporation || len(first.Items) != 0 || first.Notice == "" {
		t.Fatalf("取り込み前の扱いが違う: %+v", first)
	}
	// 確認済みの位置を記録する（以後はここからの差分になる）。
	if err := a.AcknowledgeChangeSummary(); err != nil {
		t.Fatalf("確認済みの位置を記録できない: %v", err)
	}
	settings, err := projectstore.LoadSettings(a.paths)
	if err != nil {
		t.Fatal(err)
	}
	recent, ok := settings.RecentProject(rootA)
	if !ok || recent.AcknowledgedIncorporation == "" || recent.AcknowledgedAt.IsZero() {
		t.Fatalf("確認済みの位置がアプリ設定へ保存されていない: %+v %v", recent, ok)
	}

	// B が変更し（変更履歴つき）、反映する。
	loggerB, err := auditlog.New(storeB, suzukiID)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-24 * time.Hour) // **昨日**書いた記録が今日の取り込みで入る
	recB := func(at time.Time, target, kind, before, after string) {
		t.Helper()
		if err := loggerB.RecordChange(auditlog.ChangeRecord{
			At: at, Author: suzukiID, Target: target, Change: kind, Before: before, After: after}); err != nil {
			t.Fatal(err)
		}
	}
	recB(base.Add(time.Minute), "DEC-001", auditlog.ChangeCreated, "", "在庫は受注確定時に引き当てる。")
	recB(base.Add(2*time.Minute), "ISS-001", auditlog.ChangeStatusChanged, "open", "resolved")
	recB(base.Add(3*time.Minute), "requirements/v1", auditlog.ChangeStatusChanged, "", "確定")
	recB(base.Add(4*time.Minute), "y.tanaka@example.co.jp", auditlog.ChangeMemberAdded, "", "田中（編集）")
	recB(base.Add(5*time.Minute), projectstore.ReservationTerms, auditlog.ChangeReservationSet, "", "鈴木: 排他")
	// 変更要約の区分に無い変更（対象外）。
	recB(base.Add(6*time.Minute), storeB.Project().ProjectID, auditlog.ChangeProjectOpened, "", "鈴木")
	decB, err := storeB.CreateDecision(projectstore.Decision{TopicKey: "scope/warehouse",
		Body: "対象は国内倉庫のみとする。", Evidence: []string{"S-0001#utt-00001"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientB.Publish(ctx, rootB, remote, "", syncmod.PublishOptions{}); err != nil {
		t.Fatalf("B の反映に失敗: %v", err)
	}
	_ = storeB.Close()

	// A も自分で変更しておく（自分の変更は要約に出さない）。
	a.recordChange(sessionA, auditlog.ChangeRecord{At: time.Now().UTC(), Author: satoID,
		Target: "DEC-900", Change: auditlog.ChangeCreated, After: "自分が決めた内容"})

	// A が取り込む。
	result, err := clientA.Incorporate(ctx, rootA, remote, "")
	if err != nil {
		t.Fatalf("取り込みに失敗: %v", err)
	}
	if result.Incoming == nil || len(result.Incoming.Paths) == 0 {
		t.Fatalf("取り込みで入った内容が取れていない: %+v", result)
	}

	got, err := a.ChangeSummary()
	if err != nil {
		t.Fatalf("変更要約を取得できない: %v", err)
	}
	if got.NoIncorporation || len(got.Items) == 0 {
		t.Fatalf("取り込んだ変更が提示されない: %+v", got)
	}
	counts := got.Counts
	if counts["decisions"] != 1 || counts["open-issues"] != 1 ||
		counts["confirmations"] != 1 || counts["members"] != 1 || counts["reservations"] != 1 {
		t.Fatalf("区分ごとの件数が違う: %+v", counts)
	}
	for _, item := range got.Items {
		if item.Target == "DEC-900" {
			t.Errorf("自分の変更が含まれた: %+v", item)
		}
		if item.Target == sessionA.store.Project().ProjectID {
			t.Errorf("区分に無い変更（プロジェクトを開いた記録）が含まれた: %+v", item)
		}
		if item.Author != "鈴木" {
			t.Errorf("作業者が表示名になっていない: %+v", item)
		}
		if item.KindLabel == "" || item.At == "" {
			t.Errorf("表示用の区分・日時が無い: %+v", item)
		}
		switch item.Kind {
		case "decisions", "open-issues", "requirements", "questionnaires", "confirmations", "members", "reservations":
		default:
			t.Errorf("区分に無い変更が要約へ入った: %+v", item)
		}
	}
	// 内容の 1 行が before / after から組み立てられる。
	var resolved *ChangeSummaryItemView
	for i := range got.Items {
		if got.Items[i].Target == "ISS-001" {
			resolved = &got.Items[i]
		}
	}
	if resolved == nil || resolved.Summary != "open → resolved" {
		t.Errorf("変更内容の 1 行が違う: %+v", resolved)
	}

	// 取り込みで入ったファイルだけを派生インデックスへ反映する（全再構築しない）。
	a.applyIncorporationToIndex(sessionA, result.Incoming)
	ix, err := projectstore.LoadRecordIndex(a.paths, sessionA.store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.Hash(decB.ID); !ok {
		t.Errorf("取り込んだレコード（%s）が索引に無い: %v", decB.ID, ix.IDs())
	}

	// 確認すると次からは対象なしになる（同じ変更を繰り返し見せない = 取り込む変更が無い旨を示す）。
	if err := a.AcknowledgeChangeSummary(); err != nil {
		t.Fatal(err)
	}
	after, err := a.ChangeSummary()
	if err != nil {
		t.Fatal(err)
	}
	if after.NoIncorporation || len(after.Items) != 0 || after.Notice == "" {
		t.Fatalf("確認後も同じ変更が提示される: %+v", after)
	}
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
}

// 要約の生成でプロジェクトデータは変化しない（要約を保存しない）。
func TestChangeSummaryDoesNotWrite(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })

	before := hashProjectTree(t, root)
	if _, err := a.ChangeSummary(); err != nil {
		t.Fatal(err)
	}
	if diff := diffTrees(before, hashProjectTree(t, root)); len(diff) != 0 {
		t.Errorf("変更要約の生成でプロジェクトデータが変化した: %v", diff)
	}
	if stub.calls != 0 {
		t.Errorf("変更要約の生成で AI プロバイダを呼んだ: %d 回", stub.calls)
	}
}

// プロジェクト一覧に自分の権限と作業状況（予約）が出る。
func TestProjectListShowsRoleAndWorking(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if _, err := a.StartWork(projectstore.ReservationDocumentsRequirements,
		projectstore.ReservationExclusive, false); err != nil {
		t.Fatal(err)
	}

	// 一覧の 1 行はプロジェクトを開かずに組み立てる（遅延読込）。
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	author, _ := settings.Author()
	row := a.summarize(root, author)
	target := &row
	if target.RoleLabel != "オーナー" {
		t.Errorf("自分の権限が出ていない: %+v", target)
	}
	if len(target.Working) != 1 || target.Working[0] != "佐藤（排他）" {
		t.Errorf("作業状況が出ていない: %+v", target.Working)
	}

	// 解除すると作業状況から消える。
	if err := a.ReleaseReservation(projectstore.ReservationDocumentsRequirements, "", false); err != nil {
		t.Fatal(err)
	}
	if after := a.summarize(root, author); len(after.Working) != 0 {
		t.Errorf("解除後も作業状況に残っている: %+v", after.Working)
	}

	// 読めない reservations.yaml では作業状況を出さない（誤った状況を出さない）。
	if err := os.WriteFile(filepath.Join(root, projectstore.FileReservations), []byte("broken: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if broken := a.summarize(root, author); len(broken.Working) != 0 {
		t.Errorf("壊れた予約ファイルで作業状況が出た: %+v", broken.Working)
	}
}
