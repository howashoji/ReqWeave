//go:build integration

// 統合検証: プロジェクトを共同で扱うときの基本の流れと例外の流れを、
// **共有フォルダ 2 端末相当**（同一のプロジェクトフォルダを 2 つの API インスタンスから操作）で実証する。
//
// AI プロバイダはスタブ（外部 API を呼ばない）。メンバー照合・予約・マージに AI 呼び出しは伴わない。

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/dialogue"
	"github.com/howashoji/ReqWeave/app/internal/keymanager"
	"github.com/howashoji/ReqWeave/app/internal/keymanager/keytest"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// newTerminal は別端末相当の API を作る（アプリ設定領域を分け、別の利用者 ID を登録する）。
func newTerminal(t *testing.T, stub *streamingStub, authorID, displayName string) *API {
	t.Helper()
	paths := projectstore.AppPaths{Base: filepath.Join(t.TempDir(), projectstore.AppID)}
	label := keytest.Marker + uuid.NewString()
	a := &API{
		paths: paths,
		keys:  keymanager.New(),
		newAdapter: func(id aiprovider.ProviderID, keys aiprovider.KeyProvider, ref aiprovider.KeyRef,
			opts aiprovider.AdapterOptions) (aiprovider.Adapter, error) {
			stub.stubAdapter.id = id
			stub.stubAdapter.keys = keys
			stub.stubAdapter.ref = ref
			return stub, nil
		},
	}
	// テストが中断されてもキーチェーンに項目を残さない。
	keytest.TrackKey(t, keymanager.Ref{ProviderID: "anthropic", Label: label})
	if _, err := a.RegisterKey("anthropic", label, dummyKey); err != nil {
		t.Fatal(err)
	}
	if err := a.CompleteSetup(SetupRequest{
		ProviderID: "anthropic", Label: label, Model: "claude-opus-5", Effort: "standard",
		AuthorID: authorID, DisplayName: displayName,
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// 共同作業の基本の流れ: メンバー追加 → 参加 → 変更要約 → 並行作業（予約と作業状況）→
// 予約の解除。メンバー外は開けず、他メンバーが予約した対象へは警告のうえ確認操作で着手できる。
func TestUC17SharedProjectBasicFlow(t *testing.T) {
	stubA := &streamingStub{}
	ownerAPI, root := newDialogueAPI(t, stubA)
	if _, err := ownerAPI.OpenDialogueProject(root); err != nil {
		t.Fatalf("オーナーが開けない: %v", err)
	}

	// 2a. メンバー一覧にない利用者は開けない（内容が表示されず、登録依頼が案内される）。
	stubB := &streamingStub{}
	memberAPI := newTerminal(t, stubB, "y.suzuki@example.co.jp", "鈴木")
	if _, err := memberAPI.OpenDialogueProject(root); err == nil {
		t.Fatal("メンバー外の利用者が共有プロジェクトを開けた")
	} else if !strings.Contains(err.Error(), "オーナーに登録を依頼") {
		t.Errorf("登録依頼の案内が無い: %v", err)
	}

	// 1. オーナーがメンバーを追加する。
	if _, err := ownerAPI.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatalf("メンバーを追加できない: %v", err)
	}

	// 2. メンバーが開く（利用者 ID で照合され、権限に応じた操作範囲になる）。
	if _, err := memberAPI.OpenDialogueProject(root); err != nil {
		t.Fatalf("メンバーが開けない: %v", err)
	}
	perm, err := memberAPI.CurrentPermission()
	if err != nil {
		t.Fatal(err)
	}
	if perm.RoleLabel != "編集" || !perm.CanEdit || perm.CanManageMembers {
		t.Fatalf("権限の判定が違う: %+v", perm)
	}

	// 4. 並行作業: 異なる対象を別々のメンバーが同時に進められる。
	if _, err := ownerAPI.StartWork(projectstore.ReservationDocumentsRequirements,
		projectstore.ReservationExclusive, false); err != nil {
		t.Fatalf("オーナーが要件定義書を予約できない: %v", err)
	}
	if _, err := memberAPI.StartWork(projectstore.ReservationTerms,
		projectstore.ReservationConcurrent, false); err != nil {
		t.Fatalf("メンバーが用語集の作業を開始できない: %v", err)
	}

	// 4a. 他メンバーが排他で予約した対象は、予約者の作業者名・予約日時つきで警告される（禁止しない）。
	list, err := memberAPI.Reservations()
	if err != nil {
		t.Fatal(err)
	}
	var reserved *ReservationView
	for i := range list.Items {
		if list.Items[i].Target == projectstore.ReservationDocumentsRequirements {
			reserved = &list.Items[i]
		}
	}
	if reserved == nil || reserved.Author != "佐藤" || reserved.StartedAt == "" ||
		reserved.ModeLabel != "排他（1 名で進める）" || reserved.Self {
		t.Fatalf("予約者の作業者名・着手日時・進め方が見えない: %+v", list)
	}
	if !strings.Contains(list.Notice, "最後に取り込んだ時点") {
		t.Errorf("最後に取り込んだ時点の情報である旨が示されない: %q", list.Notice)
	}
	check, err := memberAPI.CheckReservation(projectstore.ReservationDocumentsRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Reserved || !strings.Contains(check.Warning, "佐藤") ||
		!strings.Contains(check.Warning, "それでも着手しますか") {
		t.Fatalf("着手前の警告が組み立てられていない: %+v", check)
	}
	if _, err := memberAPI.StartWork(projectstore.ReservationDocumentsRequirements,
		projectstore.ReservationExclusive, false); err == nil {
		t.Fatal("警告の確認なしに予約対象へ着手できた")
	}
	// 確認操作を経れば着手できる（予約は警告であって、機械的に禁止しない）。着手は並行として記録される。
	mine, err := memberAPI.StartWork(projectstore.ReservationDocumentsRequirements,
		projectstore.ReservationExclusive, true)
	if err != nil {
		t.Fatalf("確認操作を経ても着手できない（禁止しないこと）: %v", err)
	}
	if mine.Mode != projectstore.ReservationConcurrent || !mine.Self {
		t.Errorf("重ねての着手が並行として記録されない: %+v", mine)
	}
	if _, err := memberAPI.Requirements(); err != nil {
		t.Errorf("予約された対象でも参照できるはず: %v", err)
	}
	// 予約されていない対象は警告なしに着手できる（受け入れ条件 4）。
	if c, err := memberAPI.CheckReservation(projectstore.ReservationRoster); err != nil || c.Reserved {
		t.Errorf("予約の無い対象で警告が出た: %+v %v", c, err)
	}

	// 6. 作業の完了時に予約を解除する（解除は変更履歴に残る）。
	if err := memberAPI.ReleaseReservation(projectstore.ReservationTerms, "", false); err != nil {
		t.Fatalf("本人の解除に失敗: %v", err)
	}
	if err := memberAPI.ReleaseReservation(projectstore.ReservationDocumentsRequirements, "", false); err != nil {
		t.Fatal(err)
	}
	if err := ownerAPI.ReleaseReservation(projectstore.ReservationDocumentsRequirements, "", false); err != nil {
		t.Fatal(err)
	}
	after, err := memberAPI.Reservations()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 0 {
		t.Errorf("解除後も作業状況が残る: %+v", after.Items)
	}
	if rec := changeOf(t, root, projectstore.ReservationTerms, auditlog.ChangeReservationSet); rec == nil {
		t.Error("予約の設定が変更履歴に無い")
	}
	if rec := changeOf(t, root, projectstore.ReservationTerms, auditlog.ChangeReservationReleased); rec == nil {
		t.Error("予約の解除が変更履歴に無い")
	}
	if err := memberAPI.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}

	// 3. 再び開いたときの変更要約。**単位は取り込み**（他メンバーの変更は取り込みで入るため）であり、
	// 同期先を設定していない作業コピーでは取り込みが存在しないため対象なしになる。
	// 取り込みで入った変更が提示されることの検証は
	// collab_events_integration_test.go の TestChangeSummaryOfIncorporation（2 端末の実同期）。
	if _, err := ownerAPI.AddMember(MemberRequest{
		AuthorID: "y.tanaka@example.co.jp", DisplayName: "田中", Role: projectstore.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	if _, err := memberAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memberAPI.CloseDialogueProject() })
	summary, err := memberAPI.ChangeSummary()
	if err != nil {
		t.Fatal(err)
	}
	if !summary.NoIncorporation || len(summary.Items) != 0 || summary.Notice == "" {
		t.Fatalf("取り込みの無い作業コピーで対象なしになっていない: %+v", summary)
	}
}

// オーナー不在時、編集権限のメンバーが確認操作を経てオーナーを引き継げる。
func TestUC17OwnerTakeover(t *testing.T) {
	stubA := &streamingStub{}
	ownerAPI, root := newDialogueAPI(t, stubA)
	if _, err := ownerAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerAPI.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	if err := ownerAPI.CloseDialogueProject(); err != nil {
		t.Fatal(err)
	}

	stubB := &streamingStub{}
	memberAPI := newTerminal(t, stubB, "y.suzuki@example.co.jp", "鈴木")
	if _, err := memberAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memberAPI.CloseDialogueProject() })

	// 確認操作なしでは実行されない。
	if _, err := memberAPI.TakeOverOwner(false); err == nil {
		t.Fatal("確認なしで引き継ぎが実行された")
	}
	taken, err := memberAPI.TakeOverOwner(true)
	if err != nil {
		t.Fatalf("引き継ぎに失敗: %v", err)
	}
	if taken.RoleLabel != "オーナー" {
		t.Fatalf("引き継げていない: %+v", taken)
	}
	if rec := changeOf(t, root, "y.suzuki@example.co.jp", auditlog.ChangeOwnerTakeover); rec == nil {
		t.Error("引き継ぎが変更履歴に無い")
	}
	// 引き継いだ側はメンバー管理ができる。
	perm, err := memberAPI.CurrentPermission()
	if err != nil {
		t.Fatal(err)
	}
	if !perm.CanManageMembers {
		t.Errorf("引き継ぎ後もメンバー管理ができない: %+v", perm)
	}
}

// 他メンバーの予約は確認操作を経てのみ解除でき、解除が変更履歴に残る。
func TestUC17ReleaseOtherMembersReservation(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	if _, err := a.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	// 他メンバーが予約したまま長期間戻らない状態を模す（予約は時間で失効しない）。
	store := storeOf(t, a)
	old := projectstore.Reservation{
		Target: projectstore.ReservationDocumentsRequirements, Mode: projectstore.ReservationExclusive,
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木",
		StartedAt: time.Now().UTC().Add(-14 * 24 * time.Hour),
	}
	if err := store.SaveReservations(&projectstore.Reservations{
		Reservations: []projectstore.Reservation{old}}); err != nil {
		t.Fatal(err)
	}

	list, err := a.Reservations()
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Author != "鈴木" || list.Items[0].Self {
		t.Fatalf("他メンバーの予約が示されない: %+v", list)
	}
	if err := a.ReleaseReservation(projectstore.ReservationDocumentsRequirements,
		"y.suzuki@example.co.jp", false); err == nil {
		t.Fatal("確認なしで他メンバーの予約が解除された")
	}
	if err := a.ReleaseReservation(projectstore.ReservationDocumentsRequirements,
		"y.suzuki@example.co.jp", true); err != nil {
		t.Fatalf("確認つきの解除に失敗: %v", err)
	}
	rec := changeOf(t, root, projectstore.ReservationDocumentsRequirements, auditlog.ChangeReservationReleased)
	if rec == nil {
		t.Fatal("解除が変更履歴に無い")
	}
	if rec.Author != "k.sato@example.co.jp" || !strings.Contains(rec.Before, "鈴木") ||
		!strings.Contains(rec.After, "佐藤") {
		t.Errorf("他メンバーによる解除であることが記録されていない: %+v", *rec)
	}
	// 解除後は自分が予約できる。
	if _, err := a.StartWork(projectstore.ReservationDocumentsRequirements,
		projectstore.ReservationExclusive, false); err != nil {
		t.Fatalf("解除後に予約できない: %v", err)
	}
}

// 同一端末の別ウィンドウが処理中のロックは、確認操作を経て解除できる（作業者名を出さない）。
func TestUC17ReleaseWindowLock(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	writeStaleWindowLock(t, root, projectstore.LockDocumentsRequirements)

	locks, err := a.WindowLocks()
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || !locks[0].Stale || locks[0].Self {
		t.Fatalf("残留したロックが示されない: %+v", locks)
	}
	if locks[0].TargetLabel != "要件定義書" || locks[0].AcquiredAt == "" {
		t.Errorf("対象・取得日時が出ていない: %+v", locks[0])
	}
	if err := a.ReleaseWindowLock(projectstore.LockDocumentsRequirements, false); err == nil {
		t.Fatal("確認なしで解除された")
	}
	if err := a.ReleaseWindowLock(projectstore.LockDocumentsRequirements, true); err != nil {
		t.Fatalf("解除に失敗: %v", err)
	}
	lock, err := storeOf(t, a).AcquireLock(projectstore.LockDocumentsRequirements)
	if err != nil {
		t.Fatalf("解除後に取得できない: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// writeStaleWindowLock は別ウィンドウが残した残留ロックを作る（異常終了の再現。作業者情報を持たない）。
func writeStaleWindowLock(t *testing.T, root, target string) {
	t.Helper()
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	body := "app_instance_id: 00000000-0000-0000-0000-000000000000\n" +
		"acquired_at: " + old + "\nheartbeat_at: " + old + "\n"
	dir := filepath.Join(root, "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, target+".lock"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 並行する反映の競合を検知し、承認しなければ未マージのまま保持する。
// 他メンバーが承認した内容が、以後の質問生成の文脈へ反映される。
func TestUC17ConcurrentRecordUpdateAndMerge(t *testing.T) {
	stubA := &streamingStub{}
	ownerAPI, root := newDialogueAPI(t, stubA)
	if _, err := ownerAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ownerAPI.CloseDialogueProject() })
	ownerStore := storeOf(t, ownerAPI)
	if _, err := ownerStore.CreateRequirement("INV", projectstore.Requirement{Title: "在庫引当",
		Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
		Priority: projectstore.PriorityMust, Status: projectstore.RequirementDraft,
		Body: "受注確定時に在庫を引き当てること。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerAPI.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}

	// 端末 A: 取り込み分析で「同じ要件項目を書き換える候補」を得る（基準版はこの時点で採られる）。
	stubA.scripts = []string{`{
	  "decisions": [], "open_issues": [],
	  "requirement_updates": [{"operation": "update", "target_id": "FR-INV-001",
	    "chapter": "functional-requirements", "title": "在庫引当",
	    "body_after": "端末 A の反映案（出荷指示時）。", "evidence_refs": ["IMP-001#L1-L1"]}],
	  "term_candidates": [], "contradictions": [], "perspective_candidates": []
	}`}
	view, err := ownerAPI.ImportClipboardText("引当の起点を見直す。", "material")
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := ownerAPI.AnalyzeImport(view.ID, true)
	if err != nil {
		t.Fatal(err)
	}

	// 端末 B: 同じ要件項目を先に更新する（別端末の並行作業）。
	stubB := &streamingStub{}
	memberAPI := newTerminal(t, stubB, "y.suzuki@example.co.jp", "鈴木")
	if _, err := memberAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memberAPI.CloseDialogueProject() })
	if _, err := memberAPI.EditRequirement(EditRequirementRequest{
		ID: "FR-INV-001", Title: "在庫引当", Body: "端末 B の変更（引当単位を明記）。"}); err != nil {
		t.Fatalf("端末 B の更新に失敗: %v", err)
	}

	// 端末 A の反映は競合として中断される（何も書き換わらない = 5a）。
	req := dialogue.FeedbackApproval{MaterialApproval: dialogue.MaterialApproval{
		ApprovalRequest: dialogue.ApprovalRequest{
			RequirementUpdates: []dialogue.RequirementApproval{
				{Candidate: analysis.Extraction.RequirementUpdates[0]}},
		},
	}}
	outcome, err := ownerAPI.ApproveImportCandidates(view.ID, req)
	if err != nil {
		t.Fatalf("競合がエラーになった（結果で返すこと）: %v", err)
	}
	if len(outcome.Conflicts) != 1 {
		t.Fatalf("競合が検知されない: %+v", outcome)
	}
	current, err := ownerStore.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.Body, "端末 B の変更") {
		t.Fatalf("承認していないのに端末 A の案が反映された: %+v", current)
	}

	// 本人がマージを承認すると反映され、merge-applied が記録される。
	c := outcome.Conflicts[0]
	req.Merges = []dialogue.MergeResolution{{ID: c.ID, BaselineHash: c.CurrentHash, Reference: view.ID}}
	merged, err := ownerAPI.ApproveImportCandidates(view.ID, req)
	if err != nil {
		t.Fatalf("マージ承認後の反映に失敗: %v", err)
	}
	if merged.Applied == nil || len(merged.Applied.RequirementIDs) != 1 {
		t.Fatalf("マージ承認後の結果が違う: %+v", merged)
	}
	if rec := changeOf(t, root, "FR-INV-001", auditlog.ChangeMergeApplied); rec == nil {
		t.Error("merge-applied が記録されていない")
	}

	// 端末 B から見ても反映後の内容になっている（共有レコードは 1 つ）。
	fromB, err := memberAPI.Requirements()
	if err != nil {
		t.Fatal(err)
	}
	var seen *RequirementView
	for i := range fromB {
		if fromB[i].ID == "FR-INV-001" {
			seen = &fromB[i]
		}
	}
	if seen == nil || !strings.Contains(seen.Body, "端末 A の反映案") {
		t.Errorf("他メンバーの反映が見えない: %+v", seen)
	}
}

// 閲覧権限は編集操作ができず、参照・進捗レポート・監査情報の参照はできる。
func TestUC17ViewerScope(t *testing.T) {
	stub := &streamingStub{}
	a, root := newDialogueAPI(t, stub)
	if _, err := a.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseDialogueProject() })
	demoteToViewer(t, root, "k.sato@example.co.jp")

	// 編集操作は拒否される。
	if _, err := a.ImportClipboardText("資料", "material"); err == nil {
		t.Error("閲覧権限で取り込みができた")
	}
	if _, err := a.AddPerspective(PerspectiveRequest{Name: "観点", Summary: "要旨"}); err == nil {
		t.Error("閲覧権限で観点を追加できた")
	}
	if _, err := a.AddMember(MemberRequest{
		AuthorID: "x@example.co.jp", DisplayName: "X", Role: projectstore.RoleViewer}); err == nil {
		t.Error("閲覧権限でメンバーを追加できた")
	}

	// 参照・進捗レポート・監査情報は可能。
	if _, err := a.Requirements(); err != nil {
		t.Errorf("閲覧権限で要件項目を参照できない: %v", err)
	}
	if _, err := a.ProgressReport(ProgressReportRequest{From: today(), To: today()}); err != nil {
		t.Errorf("閲覧権限で進捗レポートを生成できない: %v", err)
	}
	if _, err := a.ChangeHistory(); err != nil {
		t.Errorf("閲覧権限で変更履歴を参照できない: %v", err)
	}
	perm, err := a.CurrentPermission()
	if err != nil {
		t.Fatal(err)
	}
	if perm.CanEdit || perm.Reason == "" {
		t.Errorf("閲覧権限であることと理由が示されない: %+v", perm)
	}
}

// 異なる文書への 2 端末同時保存で双方の保存が失われない。
func TestUC17ConcurrentWritesToDifferentRecords(t *testing.T) {
	stubA := &streamingStub{}
	ownerAPI, root := newDialogueAPI(t, stubA)
	if _, err := ownerAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ownerAPI.CloseDialogueProject() })
	ownerStore := storeOf(t, ownerAPI)
	for _, title := range []string{"在庫引当", "棚卸"} {
		if _, err := ownerStore.CreateRequirement("INV", projectstore.Requirement{Title: title,
			Chapter: "functional-requirements", Kind: projectstore.RequirementFunctional,
			Priority: projectstore.PriorityMust, Status: projectstore.RequirementDraft,
			Body: title + "の要件。", Evidence: []string{"S-0001#utt-00001"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ownerAPI.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleEditor}); err != nil {
		t.Fatal(err)
	}
	stubB := &streamingStub{}
	memberAPI := newTerminal(t, stubB, "y.suzuki@example.co.jp", "鈴木")
	if _, err := memberAPI.OpenDialogueProject(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memberAPI.CloseDialogueProject() })

	done := make(chan error, 2)
	go func() {
		_, err := ownerAPI.EditRequirement(EditRequirementRequest{
			ID: "FR-INV-001", Title: "在庫引当", Body: "端末 A の保存。"})
		done <- err
	}()
	go func() {
		_, err := memberAPI.EditRequirement(EditRequirementRequest{
			ID: "FR-INV-002", Title: "棚卸", Body: "端末 B の保存。"})
		done <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("同時保存に失敗: %v", err)
		}
	}

	first, err := ownerStore.LoadRequirement("FR-INV-001")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ownerStore.LoadRequirement("FR-INV-002")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.Body, "端末 A の保存") || !strings.Contains(second.Body, "端末 B の保存") {
		t.Errorf("双方の保存が残っていない: %q / %q", first.Body, second.Body)
	}
}
