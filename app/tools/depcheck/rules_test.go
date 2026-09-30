package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// materialize は testdata/<name> 配下のフィクスチャを一時ディレクトリへ展開する。
// フィクスチャは末尾 ".fixture" を付けて保存してあり（go ツールに拾わせないため）、展開時に取り除く。
func materialize(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := t.TempDir()

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rel = strings.TrimSuffix(rel, ".fixture")
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("フィクスチャ %s の展開に失敗: %v", name, err)
	}
	return dst
}

func ruleIDs(vs []Violation) []string {
	ids := make([]string, 0, len(vs))
	for _, v := range vs {
		ids = append(ids, v.File+" "+v.RuleID)
	}
	sort.Strings(ids)
	return ids
}

func TestRunDetectsDependencyRuleViolations(t *testing.T) {
	root := materialize(t, "violations")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}

	want := []string{
		// 配布物（ビルド後の CSS）に残った外部ホスト参照
		"frontend/dist/assets/index.css external-asset",
		// フロントエンドの外部 CDN 読込
		"frontend/index.html external-asset",
		// フロントエンドからの直接通信・Node の fs 利用
		"frontend/src/Bad.tsx frontend-direct-io",
		"frontend/src/Bad.tsx frontend-node-fs",
		// アダプタからブラウザ・外部のプログラムで URL を開く違反。
		// Wails の BrowserOpenURL と OS の open の 2 形を拾う。
		"internal/aiprovider/adapter/codex/browser.go browser-open",
		"internal/aiprovider/adapter/codex/browser.go browser-open",
		// 証明書検証の無効化（抽象化層でも許されない）
		"internal/aiprovider/tls.go tls-verification",
		// ログ出力層以外からの動作ログの保存先への書き込み
		"internal/backup/logwrite.go logs-writer",
		// Codex アダプタ以外からの同梱 Codex の起動。
		// 実行ファイル名の字面と `app-server` の副命令の 2 形を拾う。
		"internal/binding/codexrun.go codex-invocation",
		"internal/binding/codexrun.go codex-invocation",
		// 同期モジュール以外からの git の起動。
		// LookPath("git") / CommandContext(..., "git") / 絶対パス指定の 3 形をすべて拾う。
		"internal/binding/gitrun.go git-invocation",
		"internal/binding/gitrun.go git-invocation",
		// 更新の取得先を updater 以外の層で直書き
		"internal/binding/update.go update-endpoint",
		// アダプタの StreamMessage 直接呼び出し（送信前の同意確認の迂回）
		"internal/dialogue/engine.go consent-gate-bypass",
		// 対話エンジンからのプロバイダ SDK import と外向き通信
		"internal/dialogue/engine.go outbound-network",
		"internal/dialogue/engine.go provider-sdk",
		// 進捗レポート・セッション統計からの AI 呼び出し依存。
		// 同じディレクトリの generate.go（成果物生成）は AI 呼び出しを伴うため対象外。
		"internal/docgen/report.go no-ai-in-report",
		// 取り込みモジュールからの外向き通信（取り込みはローカルで完結する）
		"internal/importer/extract.go outbound-network",
		// 同期モジュール以外での git ライブラリの import
		"internal/projectstore/gitlib.go git-library",
		// キーマネージャ以外からの OS セキュアストレージ直接操作
		"internal/projectstore/secrets.go secure-storage",
		// 同期モジュールから AIプロバイダ抽象化層への依存（同期は AI 呼び出しを伴わない）
		"internal/sync/aidep.go sync-no-ai",
	}
	gotIDs := ruleIDs(got)

	if len(gotIDs) != len(want) {
		t.Fatalf("検出件数 = %d, want %d\n検出: %v", len(gotIDs), len(want), gotIDs)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Errorf("検出[%d] = %q, want %q", i, gotIDs[i], want[i])
		}
	}
}

// 抽象化層とアップデータは例外として許可される（規則の Allow）。
func TestRunAllowsProviderSDKInAbstractionLayer(t *testing.T) {
	root := materialize(t, "violations")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}

	for _, v := range got {
		// TLS 検証の無効化とブラウザの起動は抽象化層でも例外にしない
		// （前者は通信先のなりすましを防ぐため、後者はアダプタが URL を返すだけのため）
		if v.RuleID == "tls-verification" || v.RuleID == "browser-open" {
			continue
		}
		if strings.HasPrefix(v.File, "internal/aiprovider/") {
			t.Errorf("AIプロバイダ抽象化層は例外のはずが違反として検出された: %s", v)
		}
	}
}

// ログ出力層は動作ログの保存先を参照してよい（動作ログの唯一の書き手）。
func TestRunAllowsLogsDirInAppLog(t *testing.T) {
	root := materialize(t, "clean")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}
	for _, v := range got {
		if v.RuleID == "logs-writer" {
			t.Errorf("ログ出力層の保存先参照が違反として検出された: %s", v)
		}
	}
	// フィクスチャが実際に検査対象の記述を持っていること（空振りで緑にしない）
	body, err := os.ReadFile(filepath.Join("testdata", "clean", "internal", "applog", "applog.go.fixture"))
	if err != nil {
		t.Fatalf("フィクスチャを読めない: %v", err)
	}
	if !strings.Contains(string(body), "LogsDir(") {
		t.Fatal("clean フィクスチャに LogsDir( の記述が無い（規則を素通りしているだけの可能性）")
	}
}

// 同期モジュールは git を起動してよい（唯一の許可先）。
// clean ツリーには internal/sync/git.go が LookPath("git") / CommandContext を持つ。
func TestRunAllowsGitInvocationInSyncModule(t *testing.T) {
	root := materialize(t, "clean")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}
	for _, v := range got {
		if v.RuleID == "git-invocation" {
			t.Errorf("同期モジュールの git 起動が違反として検出された: %s", v)
		}
	}
	// フィクスチャが実際に検査対象の記述を持っていること（空振りで緑にしない）
	fixture, err := os.ReadFile(filepath.Join(root, "internal", "sync", "git.go"))
	if err != nil {
		t.Fatalf("clean フィクスチャを読めない: %v", err)
	}
	for _, want := range []string{`exec.LookPath("git")`, "exec.CommandContext("} {
		if !strings.Contains(string(fixture), want) {
			t.Fatalf("clean フィクスチャに %q が無い（例外の検証になっていない）", want)
		}
	}
}

// Codex アダプタは同梱の Codex を起動してよい（唯一の許可先）。
func TestRunAllowsCodexInvocationInAdapter(t *testing.T) {
	root := materialize(t, "clean")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}
	for _, v := range got {
		if v.RuleID == "codex-invocation" {
			t.Errorf("Codex アダプタの起動が違反として検出された: %s", v)
		}
	}
	// フィクスチャが実際に検査対象の記述を持っていること（空振りで緑にしない）
	fixture, err := os.ReadFile(filepath.Join(root, "internal", "aiprovider", "adapter", "codex", "process.go"))
	if err != nil {
		t.Fatalf("clean フィクスチャを読めない: %v", err)
	}
	for _, want := range []string{`"codex", "codex"`, `"app-server"`} {
		if !strings.Contains(string(fixture), want) {
			t.Fatalf("clean フィクスチャに %q が無い（例外の検証になっていない）", want)
		}
	}
}

// 公開バインディング層は認可の URL を既定のブラウザで開いてよい（規則の Exempt）。
func TestRunAllowsBrowserOpenInBinding(t *testing.T) {
	root := materialize(t, "clean")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}
	for _, v := range got {
		if v.RuleID == "browser-open" {
			t.Errorf("バインディング層の URL 表示が違反として検出された: %s", v)
		}
	}
	// フィクスチャが実際に検査対象の記述を持っていること（空振りで緑にしない）
	fixture, err := os.ReadFile(filepath.Join(root, "internal", "binding", "openurl.go"))
	if err != nil {
		t.Fatalf("clean フィクスチャを読めない: %v", err)
	}
	if !strings.Contains(string(fixture), "BrowserOpenURL(") {
		t.Fatal("clean フィクスチャに BrowserOpenURL が無い（例外の検証になっていない）")
	}
}

func TestRunReportsNothingForCompliantTree(t *testing.T) {
	root := materialize(t, "clean")

	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("規則に適合したツリーで違反 %d 件を検出: %v", len(got), ruleIDs(got))
	}
}

// 実リポジトリ（app/）自身が依存規則に適合していることを検証する。
func TestRepositoryItselfIsCompliant(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("app/ の絶対パス解決に失敗: %v", err)
	}
	got, err := run(root)
	if err != nil {
		t.Fatalf("run() でエラー: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("app/ に依存規則違反 %d 件: %v", len(got), ruleIDs(got))
	}
}

// 回帰テスト。禁止対象の粒度（完全一致 / "/..." による配下すべて）を固定する
// （"net" の禁止が net/url まで巻き込んでいた不具合の再発防止）。
func TestMatchesAnyRespectsDenyGranularity(t *testing.T) {
	patterns := []string{"net", "net/http/...", "github.com/anthropics/anthropic-sdk-go/..."}

	cases := []struct {
		imp  string
		want bool
	}{
		{"net", true},               // 完全一致で禁止
		{"net/url", false},          // URL 解析。外向き通信ではない
		{"net/netip", false},        // アドレス表現。外向き通信ではない
		{"net/http", true},          // "/..." は基点自身も含む
		{"net/http/httputil", true}, // 配下も禁止
		{"net/smtp", false},         // 本パターン集には含めていない
		{"github.com/anthropics/anthropic-sdk-go", true},        // 基点
		{"github.com/anthropics/anthropic-sdk-go/option", true}, // 配下
		{"github.com/anthropics/anthropic-sdk-goodies", false},  // 前方一致だけで巻き込まない
	}

	for _, c := range cases {
		if got := matchesAny(c.imp, patterns); got != c.want {
			t.Errorf("matchesAny(%q) = %v, want %v", c.imp, got, c.want)
		}
	}
}
