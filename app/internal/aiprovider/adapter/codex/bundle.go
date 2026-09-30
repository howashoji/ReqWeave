// Package codex は Codex App Server 向けの ProviderAdapter 実装。
//
// 他のアダプタが HTTP の API を直接呼ぶのに対し、本アダプタは**同梱した Codex を子プロセスとして
// 起動し、標準入出力の JSON-RPC（1 行 1 メッセージ）で呼ぶ**。子プロセスの起動・停止・監視、
// JSON-RPC の送受信、要求と応答の写像、エラーの正規化、一時領域の後片づけは本パッケージに閉じる
// （同梱の Codex を起動してよいのは本パッケージだけ。機械検査は tools/depcheck の codex-invocation）。
//
// 本パッケージの挙動・数値（抑止設定が効くこと・要求本文に載るもの・終了にかかる時間・
// 誤りの届き方など）は同梱する版（bundle.json の version）の実バイナリで実測したものであり、
// 版を上げるときは同じ確認をやり直してから配布する（抑止設定が黙って効かなくなる・送信内容が増えるのを防ぐため）。
package codex

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// bundleJSON は同梱物の定義。
//
// 版は本システムの版ごとに 1 つへ固定する（確認済みの版だけを配るため）。取得元とアーキテクチャごとの
// SHA-256 をここに置き、**取得時**（`make codex` = tools/codexfetch）と
// **配布物の作成時**（tools/distcheck）に照合する。実行時は起動後の検査（guard.go の検査 a）が、
// 実行中の Codex の版とこの版を照合する。
//
//go:embed bundle.json
var bundleJSON []byte

// BundleArtifact は同梱する実行ファイル 1 つ（アーキテクチャごと）。
type BundleArtifact struct {
	// GOARCH は Go のアーキテクチャ名（arm64 / amd64）。
	GOARCH string `json:"goarch"`
	// Target は取得元の命名（aarch64-apple-darwin など）。
	Target string `json:"target"`
	// Asset は配布物（書庫）の名前。
	Asset string `json:"asset"`
	// ArchiveSHA256 は書庫の SHA-256。
	ArchiveSHA256 string `json:"archive_sha256"`
	// Binary は書庫を展開して出てくる実行ファイルの名前。
	Binary string `json:"binary"`
	// BinarySHA256 は展開した実行ファイルの SHA-256。
	BinarySHA256 string `json:"binary_sha256"`
}

// BundleLicense は同梱物に添えるライセンス表示（Apache License 2.0 の 4 項）。
type BundleLicense struct {
	// Name は置き場所での名前（LICENSE / NOTICE）。
	Name string `json:"name"`
	// Path は取得元のリポジトリ内の場所。
	Path string `json:"path"`
	// SHA256 は取得物の SHA-256。
	SHA256 string `json:"sha256"`
}

// BundleDefinition は同梱物の定義（リポジトリ内の 1 ファイル）。
type BundleDefinition struct {
	// Version は実行中の Codex と照合する版（起動後の検査 a）。
	Version string `json:"version"`
	// ReleaseTag は取得元のリリースの名前。
	ReleaseTag string `json:"release_tag"`
	// Source は取得元の所在（人が確かめるための参照先）。
	Source string `json:"source"`
	// Artifacts はアーキテクチャごとの取得物。
	Artifacts []BundleArtifact `json:"artifacts"`
	// Licenses は同梱物に添えるライセンス表示（Apache-2.0 の 4 項）。
	Licenses []BundleLicense `json:"licenses"`
}

var (
	bundleOnce sync.Once
	bundle     BundleDefinition
	bundleErr  error
)

// Bundle は同梱物の定義を返す（取得・照合を行う道具が使う）。
func Bundle() (BundleDefinition, error) {
	bundleOnce.Do(func() {
		if err := json.Unmarshal(bundleJSON, &bundle); err != nil {
			bundleErr = fmt.Errorf("同梱物の定義を解釈できません: %w", err)
			return
		}
		if bundle.Version == "" {
			bundleErr = fmt.Errorf("同梱物の定義に版がありません")
			return
		}
		if len(bundle.Artifacts) == 0 {
			bundleErr = fmt.Errorf("同梱物の定義に取得物がありません")
			return
		}
		if len(bundle.Licenses) == 0 {
			bundleErr = fmt.Errorf("同梱物の定義にライセンス表示がありません")
		}
	})
	return bundle, bundleErr
}

// bundledVersion は同梱する Codex の版（例 "0.135.0-alpha.1"）。
func bundledVersion() (string, error) {
	def, err := Bundle()
	if err != nil {
		return "", err
	}
	return def.Version, nil
}

// BundledExecutableRelPath はアプリ本体の中での同梱物の置き場所（macOS）。
//
// 配布物の検査（tools/distcheck）が同じ場所を見る。
const BundledExecutableRelPath = "Contents/Resources/codex/codex"

// executablePath は同梱した Codex の実行ファイルの場所を返す。
//
// **場所はアダプタが決める**（設定・環境変数から変えられる経路を設けない。利用者の端末の Codex は使わない）。
// macOS ではアプリ本体の内部（`ReqWeave.app/Contents/Resources/codex/codex`）に収める。
//
// パッケージ内のテストのみが差し替える（実バイナリでの結合テストで同梱前の実行ファイルを指すため）。
var executablePath = bundledExecutablePath

// bundledExecutablePath はアプリ本体の内部に収めた Codex の実行ファイルを解決する。
func bundledExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("本システムの実行ファイルの場所を特定できません: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	// macOS のアプリバンドル: <App>.app/Contents/MacOS/<exe> → <App>.app/Contents/Resources/codex/codex
	if filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		path := filepath.Join(filepath.Dir(filepath.Dir(dir)), BundledExecutableRelPath)
		if _, err := os.Stat(path); err != nil {
			return "", errNotBundled
		}
		return path, nil
	}
	return "", errNotBundled
}

// errNotBundled は同梱の Codex が見つからない状態（開発中のビルド・配布物でない実行ファイル）。
var errNotBundled = fmt.Errorf("Codex App Server の本体が見つかりません")
