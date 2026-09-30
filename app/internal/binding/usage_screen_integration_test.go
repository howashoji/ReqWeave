//go:build integration

// 結合テスト（AI 利用量ダッシュボードのバインディング）。

package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// usageDashboardPeriod は本テスト群で使う期間（ローカル暦日）。
func usageDashboardPeriod() UsageReportRequest {
	return UsageReportRequest{From: "2026-08-01", To: "2026-08-31"}
}

// newUsageDashboardAPI は初期設定済みの API と、消費実績のあるプロジェクトを用意する。
func newUsageDashboardAPI(t *testing.T) (*API, string) {
	t.Helper()
	a, root := newUsageLimitAPI(t)
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	appendConsumption(t, root, "k.sato@example.co.jp", at, 300, "d-1")
	appendConsumption(t, root, "t.suzuki@example.co.jp", at.Add(time.Hour), 200, "d-2")
	registerRecentProject(t, a, root)
	return a, root
}

// registerRecentProject はプロジェクトを既知プロジェクト一覧へ登録する（横断一覧の範囲は既知プロジェクト一覧）。
//
// 既存フォルダを一覧へ追加する公開経路がまだ無いため、テストではアプリ設定へ直接登録する。
func registerRecentProject(t *testing.T, a *API, root string) {
	t.Helper()
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.AddRecentProject(root, 0)
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatalf("既知プロジェクト一覧へ登録できない: %v", err)
	}
}

// 期間指定で横断一覧と単体詳細（3 軸の内訳）が返る。
func TestUsageDashboardListsAndDetails(t *testing.T) {
	a, root := newUsageDashboardAPI(t)

	req := usageDashboardPeriod()
	req.Path = root
	view, err := a.UsageDashboard(req)
	if err != nil {
		t.Fatalf("ダッシュボードを取得できない: %v", err)
	}
	if len(view.Projects) != 1 || view.Projects[0].Path != root || view.Projects[0].Tokens != 500 {
		t.Fatalf("横断一覧が違う: %+v", view.Projects)
	}
	d := view.Detail
	if d == nil {
		t.Fatal("単体詳細が返っていない")
	}
	if d.Tokens != 500 || d.Sends != 2 {
		t.Errorf("単体の合計が違う: %+v", d)
	}
	if len(d.ByProvider) != 1 || d.ByProvider[0].Key != "anthropic" || d.ByProvider[0].Tokens != 500 {
		t.Errorf("プロバイダ別の内訳が違う: %+v", d.ByProvider)
	}
	if len(d.ByAuthor) != 2 {
		t.Errorf("作業者別の内訳が違う: %+v", d.ByAuthor)
	}
	if len(d.BySession) != 1 || d.BySession[0].Key != "S-0001" {
		t.Errorf("対話セッション別の内訳が違う: %+v", d.BySession)
	}
}

// Markdown に期間・合計・3 軸の内訳・セッション統計・欠測件数が数値で入る。
func TestUsageDashboardMarkdownContainsNumbers(t *testing.T) {
	a, root := newUsageDashboardAPI(t)
	// 実績を持たない送信（欠測）を 1 件足す。
	appendSendWithoutUsage(t, root, "k.sato@example.co.jp", time.Date(2026, 8, 12, 9, 0, 0, 0, time.Local))

	req := usageDashboardPeriod()
	req.Path = root
	view, err := a.UsageDashboard(req)
	if err != nil {
		t.Fatalf("ダッシュボードを取得できない: %v", err)
	}
	for _, want := range []string{
		"2026-08-01", "2026-08-31", // 期間
		"500",                                    // トークン合計
		"### プロバイダ別", "### 対話セッション別", "### 作業者別", // 3 軸の内訳
		"## 3. セッション統計", "対話セッション数", "質問票", "承認された決定事項", "確定した要件項目",
		"うちトークン実績なし: 1 件", // 欠測件数
	} {
		if !strings.Contains(view.Markdown, want) {
			t.Errorf("Markdown に %q が無い:\n%s", want, view.Markdown)
		}
	}
}

// ファイル保存とクリップボードコピーが同一の本文になる。
func TestUsageReportSaveMatchesView(t *testing.T) {
	a, root := newUsageDashboardAPI(t)
	req := usageDashboardPeriod()
	req.Path = root

	view, err := a.UsageDashboard(req)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), UsageReportFileName)
	if _, err := a.SaveUsageReport(req, dst); err != nil {
		t.Fatalf("保存できない: %v", err)
	}
	saved, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	// 出力日時の行だけは生成時刻に依存するため、本文の骨格で突き合わせる。
	if trimGeneratedAt(string(saved)) != trimGeneratedAt(view.Markdown) {
		t.Errorf("保存した本文が表示内容と違う:\n--- 保存 ---\n%s\n--- 表示 ---\n%s", saved, view.Markdown)
	}
}

func trimGeneratedAt(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "- 出力日時:") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// 閲覧権限でも参照・出力できる（編集権限を要求しない）。
func TestUsageDashboardAllowedForViewer(t *testing.T) {
	a, root := newUsageDashboardAPI(t)
	demoteToViewer(t, root, "k.sato@example.co.jp")

	req := usageDashboardPeriod()
	req.Path = root
	view, err := a.UsageDashboard(req)
	if err != nil {
		t.Fatalf("閲覧権限でダッシュボードを取得できない: %v", err)
	}
	if view.Detail == nil || view.Detail.Tokens != 500 {
		t.Errorf("閲覧権限で単体詳細が取得できない: %+v", view.Detail)
	}
	if view.Markdown == "" {
		t.Error("閲覧権限で Markdown が生成できない")
	}
}

// 期間はローカル暦日で解釈する（開始日 0:00 〜 終了日 23:59:59）。
func TestUsageDashboardPeriodIsLocalCalendarDay(t *testing.T) {
	a, root := newUsageLimitAPI(t)
	registerRecentProject(t, a, root)
	// 8/1 0:00（ローカル）と 7/31 23:59（ローカル）に 1 件ずつ。
	appendConsumption(t, root, "k.sato@example.co.jp",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local), 10, "in-1")
	appendConsumption(t, root, "k.sato@example.co.jp",
		time.Date(2026, 7, 31, 23, 59, 0, 0, time.Local), 999, "out-1")
	appendConsumption(t, root, "k.sato@example.co.jp",
		time.Date(2026, 8, 31, 23, 59, 0, 0, time.Local), 20, "in-2")
	appendConsumption(t, root, "k.sato@example.co.jp",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), 888, "out-2")

	req := usageDashboardPeriod()
	req.Path = root
	view, err := a.UsageDashboard(req)
	if err != nil {
		t.Fatal(err)
	}
	if view.Detail.Tokens != 30 {
		t.Errorf("ローカル暦日の境界が違う: got %d, want 30（8/1 0:00 と 8/31 23:59 のみ）", view.Detail.Tokens)
	}
}

// 期間の指定が無い・逆転しているときは理由つきで拒否する。
func TestUsageDashboardRejectsInvalidPeriod(t *testing.T) {
	a, _ := newUsageDashboardAPI(t)
	for name, req := range map[string]UsageReportRequest{
		"開始日なし": {From: "", To: "2026-08-31"},
		"終了日なし": {From: "2026-08-01", To: ""},
		"逆転":    {From: "2026-08-31", To: "2026-08-01"},
	} {
		if _, err := a.UsageDashboard(req); err == nil {
			t.Errorf("%s: 拒否されなかった", name)
		}
	}
}

// AI 呼び出しを行わない（プロバイダへ到達できない状態でも成立する）。
func TestUsageDashboardWorksOffline(t *testing.T) {
	a, root := newUsageDashboardAPI(t)
	// 疎通確認済みの設定を消し、AI が使えない状態にする。
	settings, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Providers = nil
	settings.DefaultProvider = ""
	if err := projectstore.SaveSettings(a.paths, settings); err != nil {
		t.Fatal(err)
	}
	if err := a.requireAIReady(); err == nil {
		t.Fatal("AI が使える状態のままでは検証にならない")
	}

	req := usageDashboardPeriod()
	req.Path = root
	view, err := a.UsageDashboard(req)
	if err != nil {
		t.Fatalf("AI が使えない状態でダッシュボードを取得できない: %v", err)
	}
	if view.Detail == nil || view.Markdown == "" {
		t.Error("AI が使えない状態で内容が組み立たない")
	}
}

// 表示のみではプロジェクトデータが変化しない。
func TestUsageDashboardDoesNotChangeProjectData(t *testing.T) {
	a, root := newUsageDashboardAPI(t)
	before := snapshotProjectFiles(t, root)

	req := usageDashboardPeriod()
	req.Path = root
	if _, err := a.UsageDashboard(req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CopyUsageReport(req); err == nil {
		// Wails ランタイム無しではコピーできない。内容の生成までは通ること自体が確認対象。
		t.Log("クリップボードが使える環境")
	}
	after := snapshotProjectFiles(t, root)
	if len(before) != len(after) {
		t.Fatalf("ファイル数が変わった: %d → %d", len(before), len(after))
	}
	for path, hash := range before {
		if after[path] != hash {
			t.Errorf("表示のみでファイルが変わった: %s", path)
		}
	}
}
