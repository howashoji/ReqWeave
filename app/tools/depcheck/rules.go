package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Violation は依存規則違反 1 件。
type Violation struct {
	File   string // リポジトリ app/ からの相対パス
	Line   int
	RuleID string
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", v.File, v.Line, v.RuleID, v.Detail)
}

// goImportRule は「あるパッケージ群だけに import を許す」形の規則。
// Allow に列挙したディレクトリ配下のパッケージ以外が Deny の import を持てば違反とする。
type goImportRule struct {
	ID string
	// Deny は禁止する import パス。末尾 "/..." は「そのパッケージとその配下すべて」を意味し、
	// それ以外は完全一致のみを禁止する（"net" が net/url に波及しないようにするため）。
	Deny   []string
	Allow  []string // app/ からの相対ディレクトリ。この配下のみ Deny を import してよい
	Reason string
}

// goRules は依存規則のうち、import 関係で機械検知できるもの。
var goRules = []goImportRule{
	{
		ID: "provider-sdk",
		Deny: []string{
			"github.com/anthropics/anthropic-sdk-go/...",
			"github.com/openai/openai-go/...",
			"google.golang.org/genai/...",
			"github.com/google/generative-ai-go/...",
			"github.com/sashabaranov/go-openai/...",
		},
		Allow:  []string{"internal/aiprovider"},
		Reason: "個別プロバイダ SDK を import してよいのは AIプロバイダ抽象化層のみ（プロバイダの追加・差し替えを抽象化層の中で閉じるため）",
	},
	{
		ID:   "outbound-network",
		Deny: []string{"net", "net/http/...", "net/rpc/...", "net/smtp"},
		// tools/codexfetch は**配布物に入らないビルド時の道具**（同梱する Codex を取得して照合する）。
		// 本規則が守るのは「動いている本システムの通信経路」であり、ビルド時の取得は含まない。
		Allow:  []string{"internal/aiprovider", "internal/updater", "tools/codexfetch"},
		Reason: "外部通信は AIプロバイダ API と配布チャネルの 2 経路のみ（取り込みのテキスト抽出もローカルで完結させる。ビルド時に同梱物を取得する tools/codexfetch は配布物に入らないため対象外）",
	},
	{
		ID:     "secure-storage",
		Deny:   []string{"github.com/zalando/go-keyring/..."},
		Allow:  []string{"internal/keymanager"},
		Reason: "OS セキュアストレージへ直接触れてよいのはキーマネージャのみ（シークレットキー・同期先の認証情報の保管場所を 1 か所に限定する）",
	},
	{
		ID: "git-library",
		Deny: []string{
			"github.com/go-git/go-git/...",
			"gopkg.in/src-d/go-git.v4/...",
			"github.com/src-d/go-git/...",
			"github.com/libgit2/git2go/...",
		},
		Allow:  []string{"internal/sync"},
		Reason: "git の実装（ライブラリ）を import してよいのは同期モジュールのみ（git の概念を上位の層へ漏らさない）",
	},
}

// goDirRule は「特定のディレクトリからの import を禁じる」形の規則（goImportRule の逆向き）。
// 層全体への許可ではなく、ある層が持ってはいけない依存を機械検知するために使う。
type goDirRule struct {
	ID string
	// Paths は app/ からの相対パス。ディレクトリ（配下すべて）とファイル（そのファイルのみ）を書ける。
	Paths []string
	// Deny は禁止する import パス（末尾 "/..." の意味は goImportRule と同じ）。
	Deny   []string
	Reason string
}

// goDirRules は「この層はこれを import しない」形の依存規則。
var goDirRules = []goDirRule{
	{
		ID: "no-ai-in-report",
		// 成果物生成（generate.go）は AI 呼び出しを伴うため対象外。レコードからの機械組立てで
		// 完結すべきファイルだけを列挙する。
		Paths: []string{
			"internal/docgen/report.go",
			"internal/docgen/report_feedback.go",
			"internal/docgen/stats.go",
		},
		Deny:   []string{"github.com/howashoji/ReqWeave/app/internal/aiprovider", "github.com/howashoji/ReqWeave/app/internal/aiprovider/..."},
		Reason: "進捗レポート・セッション統計は既存レコードからの機械組立てのみで AI 呼び出しを行わない",
	},
	{
		ID:     "sync-no-ai",
		Paths:  []string{"internal/sync"},
		Deny:   []string{"github.com/howashoji/ReqWeave/app/internal/aiprovider", "github.com/howashoji/ReqWeave/app/internal/aiprovider/..."},
		Reason: "同期モジュールは AIプロバイダ抽象化層へ依存しない（同期は AI 呼び出しを伴わない）",
	},
}

// goTextRules は Go ソースに対する行単位の禁止パターン。
// import 関係では検知できない「識別子の不在」を静的に確認する。
var goTextRules = []textRule{
	{
		ID:      "tls-verification",
		Pattern: regexp.MustCompile(`\bInsecureSkipVerify\b`),
		Reason:  "証明書検証を無効化する設定・コードパスを持たない（通信先のなりすましを防ぐため）",
	},
	{
		ID: "update-endpoint",
		// 更新の取得先は internal/updater の定数 1 か所に閉じる（通信先を 1 か所で把握できるように）。
		// 他の層が Releases の URL を直書きすると、通信経路の一覧が定数から読めなくなる。
		// モジュールパス（github.com/howashoji/ReqWeave/app/...）に当たらないよう
		// リリース配布物の経路そのものだけを拾う。
		Pattern: regexp.MustCompile(`ReqWeave/releases/|api\.github\.com`),
		Exempt:  []string{"internal/updater", "tools/depcheck"},
		Reason:  "更新の取得先は internal/updater の定数に限る（通信先の一覧を定数から読めなくしない）",
	},
	{
		ID:      "consent-gate-bypass",
		Pattern: regexp.MustCompile(`\.StreamMessage\s*\(`),
		Exempt:  []string{"internal/aiprovider"},
		Reason:  "アダプタの StreamMessage を層外から直接呼ばない（送信前の同意確認を置いた StreamRetrying を迂回させないため）",
	},
	{
		ID: "logs-writer",
		// 動作ログの保存先（AppPaths.LogsDir）を参照してよいのはログ出力層（internal/applog）だけ。
		// 他の層が logs/ を直接開くと、マスキングフィルタを通らない書き込みと
		// ローテーションの対象外のファイルが生まれる。
		// 定義元（projectstore）は保存先の宣言そのものなので例外。
		Pattern: regexp.MustCompile(`\bLogsDir\s*\(`),
		Exempt:  []string{"internal/applog", "internal/projectstore"},
		Reason:  "動作ログの保存先へ書いてよいのはログ出力層（internal/applog）のみ（マスキングとローテーションを迂回させない）",
	},
	{
		ID: "codex-invocation",
		// 同梱の Codex を起動する記述（実行ファイル名か `app-server` の副命令が字面で現れる形）を拾う。
		// 「os/exec を import してよい層」を限る書き方は採らない（配布物の適用 = アップデータ、
		// 外部の URL を開く = バインディング層が既にプロセスを起動しているため）。
		// 起動設定の固定と起動後の検査を通らない経路を静的に塞ぐのが目的。
		Pattern: regexp.MustCompile(`exec\.(?:LookPath|Command|CommandContext)\s*\([^)]*"(?:[^"]*[/\\])?codex(?:\.exe)?"|"app-server"`),
		Exempt:  []string{"internal/aiprovider/adapter/codex", "tools/codexcatalog", "tools/codexfetch"},
		Reason:  "同梱の Codex を起動してよいのは AIプロバイダ抽象化層の Codex アダプタのみ（起動設定の固定と起動後の検査を通すため）",
	},
	{
		ID: "browser-open",
		// 既定のブラウザ・外部のプログラムで URL を開く記述を拾う
		// （Wails の BrowserOpenURL / OS の open・xdg-open・rundll32 の起動）。
		// **アダプタは認可の URL を返すだけ**で、外部のプログラムを起動しない。開くのはバインディング層。
		Pattern: regexp.MustCompile(`\bBrowserOpenURL\s*\(|exec\.(?:LookPath|Command|CommandContext)\s*\([^)]*"(?:[^"]*[/\\])?(?:open|xdg-open|rundll32(?:\.exe)?)"`),
		Exempt:  []string{"internal/binding"},
		Reason:  "既定のブラウザ・外部のプログラムで URL を開いてよいのは公開バインディング層のみ（アダプタは URL を返すだけ）",
	},
	{
		ID: "git-invocation",
		// git を外部コマンドとして起動する記述（実行ファイル名を字面で持つ形）を拾う。
		// 実行ファイルの解決は LookPath("git") か絶対パス指定のいずれかを通るため、
		// 「"git" という実行ファイル名がソースに現れる exec 呼び出し」を同期モジュール外で禁止すれば、
		// 他層から git を起動する経路は静的に塞がる。
		// 例: exec.LookPath("git") / exec.Command("git", ...) / exec.CommandContext(ctx, "/usr/bin/git", ...)
		Pattern: regexp.MustCompile(`exec\.(?:LookPath|Command|CommandContext)\s*\([^)]*"(?:[^"]*[/\\])?git(?:\.exe)?"`),
		Exempt:  []string{"internal/sync"},
		Reason:  "git を起動してよいのは同期モジュールのみ（git の概念を上位の層へ漏らさない）",
	},
}

// exempts は当該ファイルが規則の適用外かを返す（Exempt 配下のディレクトリ）。
func (r textRule) exempts(rel string) bool {
	for _, dir := range r.Exempt {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// CheckGoText は Go ソースを行単位で走査して禁止識別子を検出する。
func CheckGoText(root string) ([]Violation, error) {
	var violations []Violation
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// 規則そのものを定義する本ツールは対象外（禁止識別子を文字列として保持するため）
		if strings.HasPrefix(rel, "tools/depcheck/") {
			return nil
		}
		lines, err := readLines(p)
		if err != nil {
			return err
		}
		for i, line := range lines {
			for _, rule := range goTextRules {
				if rule.exempts(rel) {
					continue
				}
				if rule.Pattern.MatchString(line) {
					violations = append(violations, Violation{
						File:   rel,
						Line:   i + 1,
						RuleID: rule.ID,
						Detail: fmt.Sprintf("%s — %s", strings.TrimSpace(line), rule.Reason),
					})
				}
			}
		}
		return nil
	})
	return violations, err
}

// textRule は行単位の禁止パターン（Go ソース・フロントエンド資産の双方で使う）。
type textRule struct {
	ID      string
	Pattern *regexp.Regexp
	Reason  string
	// Exempt は規則の適用外とするディレクトリ（app/ からの相対パスの接頭辞）。
	// 「その層の中でだけ許す」形の規則に使う。
	Exempt []string
}

var frontendRules = []textRule{
	{
		ID:      "frontend-direct-io",
		Pattern: regexp.MustCompile(`\b(fetch\s*\(|XMLHttpRequest|WebSocket\s*\(|EventSource\s*\(|navigator\.sendBeacon)`),
		Reason:  "フロントエンドはバックエンドの公開バインディング経由でのみ機能を呼ぶ（直接の通信・ファイル I/O をしない）",
	},
	{
		ID:      "frontend-node-fs",
		Pattern: regexp.MustCompile(`(?:from\s+|require\s*\(\s*)['"](?:node:)?(?:fs|child_process|net|http|https)['"]`),
		Reason:  "フロントエンドから OS のファイル I/O・通信 API を直接使わない（バックエンドの公開バインディングを経由する）",
	},
	externalAssetRule,
}

// externalAssetRule はソースと配布物の両方で使う。
var externalAssetRule = textRule{
	ID:      "external-asset",
	Pattern: regexp.MustCompile(`(?:src|href)\s*=\s*['"]https?://|@import\s+(?:url\()?['"]?https?://|url\(\s*['"]?https?://|(?:from|import\()\s*['"]https?://`),
	Reason:  "フォント・アイコン・JS/CSS は配布物に同梱し、外部 CDN から読み込まない（通信先を AI プロバイダと配布元に限るため）",
}

// skipDirs は走査対象から外すディレクトリ名。
var skipDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".gates":       true,
	"wailsjs":      true, // Wails が生成する
	"testdata":     true, // 規則自体のテスト用フィクスチャ
}

// CheckGo は root（app/ 相当）配下の Go ファイルを走査して import 規則違反を返す。
func CheckGo(root string) ([]Violation, error) {
	var violations []Violation
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// 依存規則そのものを定義する本ツールは対象外（禁止 import 文字列を保持するため）
		if strings.HasPrefix(rel, "tools/depcheck/") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		dir := path.Dir(rel)
		for _, spec := range file.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: import パスを解釈できません: %s", rel, spec.Path.Value)
			}
			for _, rule := range goDirRules {
				if !withinAny(dir, rule.Paths) && !withinAny(rel, rule.Paths) {
					continue
				}
				if !matchesAny(imp, rule.Deny) {
					continue
				}
				violations = append(violations, Violation{
					File:   rel,
					Line:   fset.Position(spec.Pos()).Line,
					RuleID: rule.ID,
					Detail: fmt.Sprintf("%q を import しています — %s", imp, rule.Reason),
				})
			}
			for _, rule := range goRules {
				if !matchesAny(imp, rule.Deny) || withinAny(dir, rule.Allow) {
					continue
				}
				violations = append(violations, Violation{
					File:   rel,
					Line:   fset.Position(spec.Pos()).Line,
					RuleID: rule.ID,
					Detail: fmt.Sprintf("%q を import しています — %s", imp, rule.Reason),
				})
			}
		}
		return nil
	})
	return violations, err
}

// CheckBundledAssets は配布物に取り込まれる最終成果物（frontend/dist）を走査し、
// 外部ホスト参照が残っていないことを確認する。
//
// frontend/src の検査だけでは、npm パッケージ由来の CSS（フォント等）が外部 URL を
// 持ち込んだ場合に見逃す。実際に配る CSS / HTML を見るのが最終的な担保になる。
// 対象が存在しない（未ビルド）ときは何も報告しない。
func CheckBundledAssets(root string, targets []string) ([]Violation, error) {
	var violations []Violation
	for _, target := range targets {
		full := filepath.Join(root, filepath.FromSlash(target))
		if _, err := os.Stat(full); err != nil {
			continue
		}
		err := filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			// 最小化された JS には任意の文字列が入るため、CSS と HTML のみを見る
			switch filepath.Ext(d.Name()) {
			case ".css", ".html":
			default:
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			lines, err := readLines(p)
			if err != nil {
				return err
			}
			for i, line := range lines {
				if externalAssetRule.Pattern.MatchString(line) {
					violations = append(violations, Violation{
						File:   rel,
						Line:   i + 1,
						RuleID: externalAssetRule.ID,
						Detail: fmt.Sprintf("配布物に外部ホスト参照が残っています — %s", externalAssetRule.Reason),
					})
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return violations, nil
}

// CheckFrontend は root 配下のフロントエンド資産を走査して禁止パターンを返す。
func CheckFrontend(root string, targets []string) ([]Violation, error) {
	var violations []Violation
	for _, target := range targets {
		full := filepath.Join(root, filepath.FromSlash(target))
		err := filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			switch filepath.Ext(d.Name()) {
			case ".ts", ".tsx", ".js", ".jsx", ".css", ".html":
			default:
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			// テストはバインディングをモックするため、直接 I/O 検査の対象外にはしない。
			// （モックはバインディングモジュールに対して行うため本規則には触れない）
			src, err := readLines(p)
			if err != nil {
				return err
			}
			for i, line := range src {
				for _, rule := range frontendRules {
					if rule.Pattern.MatchString(line) {
						violations = append(violations, Violation{
							File:   rel,
							Line:   i + 1,
							RuleID: rule.ID,
							Detail: fmt.Sprintf("%s — %s", strings.TrimSpace(line), rule.Reason),
						})
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return violations, nil
}

func matchesAny(imp string, patterns []string) bool {
	for _, p := range patterns {
		if base, ok := strings.CutSuffix(p, "/..."); ok {
			if imp == base || strings.HasPrefix(imp, base+"/") {
				return true
			}
			continue
		}
		if imp == p {
			return true
		}
	}
	return false
}

func withinAny(dir string, allowed []string) bool {
	for _, a := range allowed {
		if dir == a || strings.HasPrefix(dir, a+"/") {
			return true
		}
	}
	return false
}

func readLines(p string) ([]string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(b), "\n"), nil
}
