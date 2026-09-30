//go:build integration

// AI 利用量・セッション統計を参照する流れの統合検証。公開バインディング経由で、手順 1〜5
// （1. 期間の指定、2. 開けるプロジェクトの横断一覧、3. 単体の内訳とセッション統計、4. Markdown 出力、
// 5. オーナーによる上限・警告閾値の設定）と、例外の 2 つ（AI に到達できないとき・オーナー以外が
// 上限を変えようとしたとき）を実証する。
//
// オーナーと閲覧権限の 2 者を**別々の利用者 ID**で用意し、権限差（上限の設定はオーナーのみ）を実証する。
// ダッシュボードは AI 呼び出しを伴わないため、プロバイダへ到達できない状態でも成立することを確かめる。

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/auditlog"
	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// uc18Period は本テストで使う対象期間（消費レコードを置く日を含む暦日）。
const (
	uc18From = "2026-08-01"
	uc18To   = "2026-08-31"
)

// uc18Fixture は利用量の参照の登場人物（オーナー端末・閲覧権限の別端末）と対象プロジェクト。
type uc18Fixture struct {
	owner  *API
	viewer *API
	root   string
	stub   *usageStub
}

// newUC18Fixture はオーナーが開いたプロジェクトへ閲覧権限のメンバーを 1 名追加し、
// 期間内の消費実績（2 名・2 プロバイダ・欠測 1 件）を置いた状態を作る。
func newUC18Fixture(t *testing.T) *uc18Fixture {
	t.Helper()
	stub := &usageStub{streamingStub: streamingStub{scripts: []string{dialogueQuestion}}, tokensIn: 1}
	owner, root := openWithUsageLimit(t, stub)

	viewer := newTerminal(t, &streamingStub{}, "y.suzuki@example.co.jp", "鈴木")
	if _, err := owner.AddMember(MemberRequest{
		AuthorID: "y.suzuki@example.co.jp", DisplayName: "鈴木", Role: projectstore.RoleViewer}); err != nil {
		t.Fatalf("閲覧権限のメンバーを追加できない: %v", err)
	}

	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	appendSend(t, root, "k.sato@example.co.jp", "uc18-1", at, "anthropic", "S-0001", intPtr(300))
	appendSend(t, root, "y.suzuki@example.co.jp", "uc18-2", at.Add(time.Hour), "openai", "S-0002", intPtr(200))
	// トークン実績を返さなかった送信（欠測）。合計へは 0 で入り、件数だけが別に数えられる。
	appendSend(t, root, "k.sato@example.co.jp", "uc18-3", at.Add(2*time.Hour), "anthropic", "", nil)

	registerRecentProject(t, owner, root)
	registerRecentProject(t, viewer, root)
	return &uc18Fixture{owner: owner, viewer: viewer, root: root, stub: stub}
}

// 手順 1〜5: 期間指定 → 横断一覧 → 単体の内訳とセッション統計 → Markdown 出力 →
// オーナーによる上限設定（変更履歴に記録される）。
func TestUC18UsageDashboardBasicFlow(t *testing.T) {
	f := newUC18Fixture(t)

	// 1〜2. 期間を指定すると、開けるプロジェクトの横断一覧が返る。
	list, err := f.owner.UsageDashboard(UsageReportRequest{From: uc18From, To: uc18To})
	if err != nil {
		t.Fatalf("横断一覧を取得できない: %v", err)
	}
	row := findUC18Row(t, list.Projects, f.root)
	if !row.Aggregated || row.Tokens != 500 {
		t.Fatalf("横断一覧の合計が違う: %+v", row)
	}
	if row.MissingRecords != 1 {
		t.Errorf("実績なしの送信件数が違う: %+v", row)
	}
	if row.LastUsedAt == "" {
		t.Error("直近の利用日時が空")
	}

	// 3. プロジェクトを選ぶと単体の内訳（プロバイダ別・対話セッション別・作業者別）と
	//    セッション統計が返る。
	detail, err := f.owner.UsageDashboard(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root})
	if err != nil {
		t.Fatalf("単体詳細を取得できない: %v", err)
	}
	d := detail.Detail
	if d == nil {
		t.Fatal("単体詳細が返っていない")
	}
	if len(d.ByProvider) != 2 || len(d.ByAuthor) != 2 {
		t.Errorf("内訳の軸が揃っていない: provider=%+v author=%+v", d.ByProvider, d.ByAuthor)
	}
	// セッション文脈を持たない送信は空キーの行として残る（黙って落とさない）。
	if !hasBucketKey(d.BySession, "") || !hasBucketKey(d.BySession, "S-0001") {
		t.Errorf("対話セッション別の内訳が違う: %+v", d.BySession)
	}
	if d.Sessions.SessionsTotal < 0 {
		t.Errorf("セッション統計が組み立たない: %+v", d.Sessions)
	}

	// 4. 表示内容を Markdown ファイルへ出力する。保存とコピーは同一の本文（バインディングが
	//    組み立てた view.Markdown）を使うため、保存物と表示内容の一致で両経路を確かめる。
	dst := filepath.Join(t.TempDir(), UsageReportFileName)
	if _, err := f.owner.SaveUsageReport(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root}, dst); err != nil {
		t.Fatalf("Markdown を保存できない: %v", err)
	}
	saved, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != detail.Markdown {
		t.Error("保存した本文が表示内容と一致しない（出力経路で内容が変わっている）")
	}
	for _, want := range []string{"# AI 利用量", "プロジェクト横断", "セッション統計"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("出力に %q が無い", want)
		}
	}

	// 5. オーナーが上限・警告閾値を設定する。
	ratio := 0.5
	status, err := f.owner.SetUsageLimit(UsageLimitRequest{TokensMax: 1000, WarnRatio: &ratio})
	if err != nil {
		t.Fatalf("オーナーが上限を設定できない: %v", err)
	}
	if status.LimitTokens == nil || *status.LimitTokens != 1000 || status.WarnRatio != 0.5 {
		t.Fatalf("設定した上限が反映されていない: %+v", status)
	}

	// 事後条件: 上限設定の変更が変更履歴へ記録されている（誰がいつ変えたかを追えるように）。
	history, err := f.owner.ChangeHistory()
	if err != nil {
		t.Fatalf("変更履歴を読めない: %v", err)
	}
	var found *ChangeHistoryView
	for i := range history {
		if history[i].Change == auditlog.ChangeUsageLimitChanged {
			found = &history[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("上限設定の変更が変更履歴に無い: %+v", history)
	}
	if found.Before != "未設定" || !strings.Contains(found.After, "1000") {
		t.Errorf("変更履歴の前後の値が違う: %+v", found)
	}
	if found.Author != "k.sato@example.co.jp" {
		t.Errorf("変更履歴の作業者が違う: %+v", found)
	}

	// 上限設定後は横断一覧・単体詳細に消費率が出る（上限が無いときは出さない）。
	after, err := f.owner.UsageDashboard(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root})
	if err != nil {
		t.Fatal(err)
	}
	if after.Detail.ConsumptionRatio == nil || *after.Detail.ConsumptionRatio != 0.5 {
		t.Errorf("消費率が返っていない: %+v", after.Detail.ConsumptionRatio)
	}
}

// 例外（AI に到達できない）: AI 呼び出しを伴わないため、プロバイダへ到達できない状態でも手順 1〜4 が成立する。
func TestUC18WorksWithoutProviderAccess(t *testing.T) {
	f := newUC18Fixture(t)

	// 疎通確認済みの設定を消し、AI が使えない状態にする（API キー未設定と同じ状態）。
	settings, err := f.owner.settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Providers = nil
	settings.DefaultProvider = ""
	if err := projectstore.SaveSettings(f.owner.paths, settings); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.requireAIReady(); err == nil {
		t.Fatal("AI が使える状態のままでは検証にならない")
	}

	view, err := f.owner.UsageDashboard(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root})
	if err != nil {
		t.Fatalf("AI が使えない状態で横断一覧・単体の内訳を表示できない: %v", err)
	}
	if view.Detail == nil || view.Markdown == "" {
		t.Fatal("AI が使えない状態で表示内容が組み立たない")
	}
	dst := filepath.Join(t.TempDir(), UsageReportFileName)
	if _, err := f.owner.SaveUsageReport(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root}, dst); err != nil {
		t.Fatalf("AI が使えない状態で Markdown 出力ができない: %v", err)
	}
	// 消費実績の画面も同じ条件で成立する。
	if _, err := f.owner.TokenUsage(TokenUsageRequest{}); err != nil {
		t.Errorf("AI が使えない状態で消費実績を参照できない: %v", err)
	}
}

// 例外（オーナー以外の上限設定）: 拒否され、オーナーの操作である旨が返る。
// 参照（手順 1〜4）は閲覧権限でも成立する（参照に必要な権限は閲覧で足りる）。
func TestUC18ViewerCanReadButCannotChangeLimit(t *testing.T) {
	f := newUC18Fixture(t)
	if _, err := f.viewer.OpenDialogueProject(f.root); err != nil {
		t.Fatalf("閲覧権限のメンバーがプロジェクトを開けない: %v", err)
	}
	t.Cleanup(func() { _ = f.viewer.CloseDialogueProject() })

	// 参照はできる。
	view, err := f.viewer.UsageDashboard(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root})
	if err != nil {
		t.Fatalf("閲覧権限でダッシュボードを参照できない: %v", err)
	}
	if view.Detail == nil || view.Detail.Tokens != 500 {
		t.Errorf("閲覧権限の参照結果が違う: %+v", view.Detail)
	}
	if _, err := f.viewer.TokenUsage(TokenUsageRequest{}); err != nil {
		t.Errorf("閲覧権限で消費実績を参照できない: %v", err)
	}

	// 上限の設定・解除はできない（理由にオーナーの操作である旨が含まれる）。
	for name, err := range map[string]error{
		"設定": secondError(f.viewer.SetUsageLimit(UsageLimitRequest{TokensMax: 1000})),
		"解除": secondError(f.viewer.ClearUsageLimit()),
	} {
		if err == nil {
			t.Fatalf("閲覧権限の上限%sが拒否されなかった", name)
		}
		if !strings.Contains(err.Error(), "オーナー") {
			t.Errorf("上限%sの拒否理由にオーナーの操作である旨が無い: %v", name, err)
		}
	}

	// 権限の判定結果も画面へ渡る（画面側で権限判定をしないため）。
	perm, err := f.viewer.CurrentPermission()
	if err != nil {
		t.Fatal(err)
	}
	if perm.CanManageUsageLimit || perm.UsageLimitReason == "" {
		t.Errorf("閲覧権限の判定結果が違う: %+v", perm)
	}
}

// 上限到達後は AI を使う操作を開始できず、AI を使わない操作は続けられる。
func TestUC18BlockedAtLimitKeepsNonAIOperations(t *testing.T) {
	f := newUC18Fixture(t)
	if _, err := f.owner.SetUsageLimit(UsageLimitRequest{TokensMax: 100}); err != nil {
		t.Fatalf("上限を設定できない: %v", err)
	}

	sess, err := f.owner.StartDialogueSession()
	if err != nil {
		t.Fatal(err)
	}
	before := f.stub.calls
	if _, err := f.owner.AskNextQuestion(sess.ID); err == nil {
		t.Fatal("上限到達なのに AI 呼び出しが開始された")
	} else {
		for _, want := range []string{"上限に達した", "オーナーが上限を変更すると再開できます", "AI を使わない操作"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("エラーカタログ「利用量上限系」の要素が無い（%q）: %v", want, err)
			}
		}
	}
	if f.stub.calls != before {
		t.Errorf("上限到達なのにプロバイダへ送信された: %d → %d", before, f.stub.calls)
	}

	// AI を使わない操作（ダッシュボード・消費実績・進捗レポート）は続けられる。
	if _, err := f.owner.UsageDashboard(UsageReportRequest{From: uc18From, To: uc18To, Path: f.root}); err != nil {
		t.Errorf("上限到達でダッシュボードが使えない: %v", err)
	}
	if _, err := f.owner.TokenUsage(TokenUsageRequest{}); err != nil {
		t.Errorf("上限到達で消費実績が使えない: %v", err)
	}
	if _, err := f.owner.ProgressReport(ProgressReportRequest{From: uc18From, To: uc18To}); err != nil {
		t.Errorf("上限到達で進捗レポートが作れない: %v", err)
	}
}

// 事後条件: 表示のみの流れではプロジェクトデータが変化しない。
func TestUC18ReadOnlyFlowDoesNotChangeProjectData(t *testing.T) {
	f := newUC18Fixture(t)
	before := snapshotProjectFiles(t, f.root)

	req := UsageReportRequest{From: uc18From, To: uc18To, Path: f.root}
	if _, err := f.owner.UsageDashboard(req); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.TokenUsage(TokenUsageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.UsageStatusNow(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.SaveUsageReport(req, filepath.Join(t.TempDir(), UsageReportFileName)); err != nil {
		t.Fatal(err)
	}

	after := snapshotProjectFiles(t, f.root)
	if len(after) != len(before) {
		t.Fatalf("ファイル数が変わった: %d → %d", len(before), len(after))
	}
	for rel, sum := range before {
		if after[rel] != sum {
			t.Errorf("表示のみでファイルが変化した: %s", rel)
		}
	}
}

// findUC18Row は横断一覧から対象プロジェクトの行を取り出す。
func findUC18Row(t *testing.T, rows []ProjectUsageRow, root string) ProjectUsageRow {
	t.Helper()
	for _, r := range rows {
		if r.Path == root {
			return r
		}
	}
	t.Fatalf("横断一覧に対象プロジェクトが無い: %+v", rows)
	return ProjectUsageRow{}
}

func hasBucketKey(buckets []UsageBucketView, key string) bool {
	for _, b := range buckets {
		if b.Key == key {
			return true
		}
	}
	return false
}

// secondError は (値, error) を返す呼び出しから error だけを取り出す（表形式の検証で使う）。
func secondError(_ UsageStatus, err error) error { return err }
