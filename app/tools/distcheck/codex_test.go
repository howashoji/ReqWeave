package main

// 同梱した Codex の検査の単体テスト。
//
// **合格の側**（定義どおりの同梱物が入っていること）は、実物が要るため `make dist` の distcheck が見る
// （出力例: 「同梱の Codex: 0.135.0-alpha.1（2 アーキテクチャ・定義と一致）」）。
// ここでは**不合格を検知できること**を確かめる（黙って通す形になっていないこと）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider/adapter/codex"
)

// fakeApp は検査対象の並びだけを持つアプリ本体を作る。
func fakeApp(t *testing.T, withCodex bool) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "ReqWeave.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !withCodex {
		return app
	}
	dir := filepath.Join(app, filepath.Dir(codex.BundledExecutableRelPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 定義と中身が違う実行ファイル（差し替えられた状態の再現）。
	if err := os.WriteFile(filepath.Join(app, codex.BundledExecutableRelPath), []byte("差し替えられた実体"), 0o755); err != nil {
		t.Fatal(err)
	}
	def, err := codex.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	for _, license := range def.Licenses {
		if err := os.WriteFile(filepath.Join(dir, license.Name), []byte("ライセンス表示のつもり"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

// 同梱物が無ければ不合格（作り忘れた配布物を配らない）。
func TestCheckBundledCodexDetectsMissing(t *testing.T) {
	problems, _ := checkBundledCodex(fakeApp(t, false), false)
	if !containsSubstring(problems, "同梱の Codex がありません") {
		t.Errorf("同梱物の不在を検知しない: %v", problems)
	}
}

// 中身が定義と違えば不合格（片方のアーキテクチャだけ差し替え・改ざんを検出する）。
func TestCheckBundledCodexDetectsTamperedBinary(t *testing.T) {
	problems, _ := checkBundledCodex(fakeApp(t, true), false)
	if len(problems) == 0 {
		t.Fatal("定義と違う同梱物が素通りした")
	}
	// 実行ファイルの照合（アーキテクチャを取り出せない = 定義と違う）とライセンス表示の照合の両方で挙がる。
	if !containsSubstring(problems, "同梱の Codex") {
		t.Errorf("実行ファイルの不一致を検知しない: %v", problems)
	}
	if !containsSubstring(problems, "LICENSE") {
		t.Errorf("ライセンス表示の不一致を検知しない: %v", problems)
	}
}

// 定義（bundle.json）に版・取得元・ハッシュ・ライセンス表示が揃っていること。
// 揃っていなければ検査そのものが成り立たない。
func TestBundleDefinitionIsComplete(t *testing.T) {
	def, err := codex.Bundle()
	if err != nil {
		t.Fatalf("同梱物の定義を読めない: %v", err)
	}
	if def.Version == "" || def.ReleaseTag == "" || !strings.HasPrefix(def.Source, "https://") {
		t.Errorf("版・取得元が揃っていない: %+v", def)
	}
	archs := map[string]bool{}
	for _, artifact := range def.Artifacts {
		archs[artifact.GOARCH] = true
		if len(artifact.ArchiveSHA256) != 64 || len(artifact.BinarySHA256) != 64 {
			t.Errorf("%s: SHA-256 が揃っていない", artifact.GOARCH)
		}
		if _, ok := lipoArch[artifact.GOARCH]; !ok {
			t.Errorf("%s: 想定外のアーキテクチャ", artifact.GOARCH)
		}
	}
	// macOS は両アーキテクチャを同梱する。
	for _, want := range []string{"arm64", "amd64"} {
		if !archs[want] {
			t.Errorf("同梱物の定義に %s がない", want)
		}
	}
	names := map[string]bool{}
	for _, license := range def.Licenses {
		names[license.Name] = true
		if len(license.SHA256) != 64 {
			t.Errorf("%s: SHA-256 が揃っていない", license.Name)
		}
	}
	// Apache-2.0 の 4 項（License の写しと NOTICE を配布物に添える）。
	for _, want := range []string{"LICENSE", "NOTICE"} {
		if !names[want] {
			t.Errorf("同梱物の定義に %s がない（Apache-2.0 の 4 項）", want)
		}
	}
}
