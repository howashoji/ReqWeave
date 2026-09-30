//go:build darwin

package updater

import (
	"archive/zip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/*
 * dmg からの取り出し。
 *
 * **名前で形式を判定していないこと**を固定するのが本テストの主眼。
 * 取得した配布物は `download-*.part` という名前で保存される（Download）ため、
 * 拡張子で判定する実装だと本番の更新だけが失敗する（テストでは .dmg を直接渡すので気づけない）。
 * ここでは意図的に**拡張子のない名前**で渡す。
 */

// detail は診断用の詳細（`hdiutil` の実出力など）を添えた文字列を返す（失敗の原因を追えるように）。
//
// `updater.Error` は利用者向けの 1 文しか `Error()` で返さない（内部の生エラーを画面へ出さないため）。
// テストの失敗メッセージがその 1 文だけだと、**環境起因か実装起因かを切り分けられない**。
func detail(err error) string {
	var uerr *Error
	if errors.As(err, &uerr) {
		return uerr.Detail()
	}
	return err.Error()
}

// makeDMG は中身 1 件（`<name>.app`）の dmg を作って返す。
func makeDMG(t *testing.T, outPath, appName string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "dmgroot")
	bundle := filepath.Join(root, appName)
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "MacOS", "ReqWeave"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "初回起動の手順.md"), []byte("# 手順\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// hdiutil は出力先が .dmg で終わらないと拡張子を勝手に足すため、
	// いったん .dmg で作ってから目的の名前へ移す（本番の `download-*.part` を再現する）。
	made := outPath + ".dmg"
	// **`-quiet` を付けない**: 失敗の理由まで抑止されるため。
	out, err := exec.Command("hdiutil", "create", "-volname", "ReqWeaveTest", "-srcfolder", root,
		"-ov", "-format", "UDZO", made).CombinedOutput()
	if err != nil {
		t.Fatalf("テスト用の dmg を作れません: %v\n%s", err, out)
	}
	if err := os.Rename(made, outPath); err != nil {
		t.Fatal(err)
	}
	// `hdiutil create` は作成中にイメージを一時的にマウントする。その切り離しを
	// 取りこぼすとマウントが残り、次の実行を巻き込む（2026-09-08 に実測）。
	// 名前を変える前後の両方の名前で確認する（残骸は作成時の名前で記録される）。
	t.Cleanup(func() {
		sweepMountLeak(t, made)
		sweepMountLeak(t, outPath)
	})
	return outPath
}

// makeZIP は中身 1 件（`<name>.app/…`）の zip を作って返す。
func makeZIP(t *testing.T, outPath, appName string) string {
	t.Helper()
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	e, err := w.Create(appName + "/Contents/MacOS/ReqWeave")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write([]byte("#!/bin/sh\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return outPath
}

// 取得した配布物は拡張子を持たない名前で保存される。dmg・zip のどちらも中身で判別できること。
func TestExtractArchiveDetectsFormatWithoutExtension(t *testing.T) {
	t.Run("dmg", func(t *testing.T) {
		// 本番と同じ「拡張子のない名前」で渡す（Download が付ける名前と同形）。
		archive := makeDMG(t, filepath.Join(t.TempDir(), "download-123.part"), "ReqWeave.app")
		dest := filepath.Join(t.TempDir(), "extract")
		if err := extractArchive(archive, dest); err != nil {
			t.Fatalf("dmg を取り出せない: %v / 詳細: %s", err, detail(err))
		}
		app, err := appEntry(dest)
		if err != nil {
			t.Fatalf("取り出し先にアプリ本体が無い: %v", err)
		}
		if filepath.Base(app) != "ReqWeave.app" {
			t.Errorf("取り出したものが違う: %q", app)
		}
		// 実行権限が保たれていること（ditto を使う理由。素朴なコピーでは落ちる）。
		info, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "ReqWeave"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("実行権限が失われている: %v", info.Mode())
		}
	})

	t.Run("zip", func(t *testing.T) {
		archive := makeZIP(t, filepath.Join(t.TempDir(), "download-456.part"), "ReqWeave.app")
		dest := filepath.Join(t.TempDir(), "extract")
		if err := extractArchive(archive, dest); err != nil {
			t.Fatalf("zip を取り出せない: %v / 詳細: %s", err, detail(err))
		}
		if _, err := appEntry(dest); err != nil {
			t.Fatalf("取り出し先にアプリ本体が無い: %v", err)
		}
	})
}

// 配布物でも何でもないファイルは、利用者向けのエラーへ倒す（黙って失敗しない）。
func TestExtractArchiveRejectsUnknownFormat(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "download-789.part")
	if err := os.WriteFile(archive, []byte("これは配布物ではありません"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := extractArchive(archive, filepath.Join(t.TempDir(), "extract"))
	if err == nil {
		t.Fatal("配布物でないファイルが受理された")
	}
	var uerr *Error
	if !errors.As(err, &uerr) {
		t.Fatalf("利用者向けエラーになっていない: %T %v", err, err)
	}
	if uerr.Kind != KindMalformed {
		t.Errorf("種別が違う: %v", uerr.Kind)
	}
	if uerr.Error() == "" {
		t.Error("利用者向けの文言が空")
	}
}

// 切り離しの失敗は「何と言って落ちたか」まで残す（終了コードだけでは原因を追えない）。
//
// `hdiutil detach` に `-quiet` を付けると失敗の理由が出力されず、
// 診断の材料が終了コードだけになる（2026-09-08 の検証ゲートの偽赤の原因）。
func TestDetachRetryKeepsFailureReason(t *testing.T) {
	// 何もマウントされていない場所を切り離そうとして必ず失敗させる。
	notMounted := t.TempDir()
	saveAttempts, saveInterval := detachAttempts, detachInterval
	detachAttempts, detachInterval = 1, 10*time.Millisecond
	defer func() { detachAttempts, detachInterval = saveAttempts, saveInterval }()

	err := detachRetry(notMounted)
	if err == nil {
		t.Fatalf("マウントされていない %q の切り離しが成功扱いになった", notMounted)
	}
	if !strings.Contains(err.Error(), "hdiutil: detach failed") {
		t.Errorf("hdiutil の実出力が残っていない: %q", err.Error())
	}
}
