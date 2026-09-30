package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dmg の検査。
//
// zip と違って中身を直接読めないため、実際に dmg を作ってマウントし、
// **想定どおりの中身を通し、想定外を落とす**ことを確かめる。

// buildDMG は指定した中身の dmg を作って返す。
func buildDMG(t *testing.T, entries map[string]string, symlinks map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, kind := range entries {
		p := filepath.Join(root, name)
		switch kind {
		case "app":
			if err := os.MkdirAll(filepath.Join(p, "Contents", "MacOS"), 0o755); err != nil {
				t.Fatal(err)
			}
			// Universal Binary の判定に通る実体を置く（自前で Mach-O を作らず、
			// 実際にビルド済みのものがあればそれを使う。無ければ判定は落ちる想定）。
			if err := os.WriteFile(filepath.Join(p, "Contents", "MacOS", "ReqWeave"), []byte("dummy"), 0o755); err != nil {
				t.Fatal(err)
			}
			// Info.plist が無いと codesign が「バンドルとして解釈できない」で落ち、
			// 署名の検査が「未署名」ではなく異常として扱われる。
			plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>ReqWeave</string>
<key>CFBundleIdentifier</key><string>net.howashoji.reqweave.test</string>
</dict></plist>
`
			if err := os.WriteFile(filepath.Join(p, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
				t.Fatal(err)
			}
			// ライセンス表示（原本は testLegalDir と同じ中身）。
			if err := os.MkdirAll(filepath.Join(p, "Contents", "Resources"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"LICENSE": testLicense, "NOTICE": testNotice} {
				if err := os.WriteFile(filepath.Join(p, "Contents", "Resources", name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		case "file":
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, target := range symlinks {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "test.dmg")
	// **`-quiet` を付けない**: 失敗したときの理由まで抑止され、
	// 下の t.Fatalf が終了コードだけになる。
	if o, err := exec.Command("hdiutil", "create", "-volname", "ReqWeaveTest", "-srcfolder", root,
		"-ov", "-format", "UDZO", out).CombinedOutput(); err != nil {
		t.Fatalf("テスト用の dmg を作れません: %v\n%s", err, o)
	}
	return out
}

// 想定どおりの中身（アプリ本体 1 件 + 同梱の手順書）は中身の検査を通る。
func TestCheckDMGAcceptsExpectedLayout(t *testing.T) {
	dmg := buildDMG(t, map[string]string{
		"ReqWeave.app": "app",
		"初回起動の手順.md":   "file",
	}, nil)
	problems, _, err := checkDMG(dmg, "ReqWeave.app", testLegalDir(t), false)
	if err != nil {
		t.Fatal(err)
	}
	// 実体はダミーのため、実行ファイルそのものを見る検査（Universal Binary・同梱の Codex）は落ちる。
	// ここで見たいのは**中身の並び**（アプリ本体 1 個 + 手順書）であり、それ以外の問題が出ないこと。
	// 同梱の Codex の検査そのものは codex_test.go、実物での合格は make dist（distcheck の出力）で見る。
	allowed := map[string]bool{
		"実行ファイルが見つからず、対応アーキテクチャを確認できません": true,
	}
	for _, p := range problems {
		if allowed[p] || strings.HasPrefix(p, "同梱の Codex がありません") {
			continue
		}
		t.Errorf("想定どおりの中身が問題として挙がった: %q", p)
	}
}

// 手順書が無い dmg は不合格。
func TestCheckDMGDetectsMissingDoc(t *testing.T) {
	dmg := buildDMG(t, map[string]string{"ReqWeave.app": "app"}, nil)
	problems, _, err := checkDMG(dmg, "ReqWeave.app", testLegalDir(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "同梱の手順書が無い") {
		t.Errorf("手順書の不在を検知しない: %v", problems)
	}
}

// アプリ本体が無い dmg は不合格。
func TestCheckDMGDetectsMissingApp(t *testing.T) {
	dmg := buildDMG(t, map[string]string{"初回起動の手順.md": "file"}, nil)
	problems, _, err := checkDMG(dmg, "ReqWeave.app", testLegalDir(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "アプリ本体") {
		t.Errorf("アプリ本体の不在を検知しない: %v", problems)
	}
}

// **/Applications へのシンボリックリンクを置かない**。
// 一般的な dmg は入れるが、本アプリはホームフォルダ配下に置かないと自動更新ができないため、
// 入れると同梱の手順書と矛盾した誘導になる。
func TestCheckDMGRejectsApplicationsSymlink(t *testing.T) {
	dmg := buildDMG(t, map[string]string{
		"ReqWeave.app": "app",
		"初回起動の手順.md":   "file",
	}, map[string]string{"Applications": "/Applications"})
	problems, _, err := checkDMG(dmg, "ReqWeave.app", testLegalDir(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "シンボリックリンクが含まれる") {
		t.Errorf("/Applications へのリンクを検知しない: %v", problems)
	}
}

// 想定外の項目が入っていたら不合格。
func TestCheckDMGDetectsJunk(t *testing.T) {
	dmg := buildDMG(t, map[string]string{
		"ReqWeave.app": "app",
		"初回起動の手順.md":   "file",
		"メモ.txt":       "file",
	}, nil)
	problems, _, err := checkDMG(dmg, "ReqWeave.app", testLegalDir(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "想定外の項目") {
		t.Errorf("想定外の項目を検知しない: %v", problems)
	}
}

// dmg でも、アプリ本体の中のライセンス表示を原本と突き合わせる（原本だけ新しくなった = 配布物が古い）。
func TestCheckDMGDetectsStaleLegalFile(t *testing.T) {
	dmg := buildDMG(t, map[string]string{
		"ReqWeave.app": "app",
		"初回起動の手順.md":   "file",
	}, nil)
	legal := testLegalDir(t)
	if err := os.WriteFile(filepath.Join(legal, "NOTICE"), []byte("依存を足した後の NOTICE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, _, err := checkDMG(dmg, "ReqWeave.app", legal, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "ReqWeave.app/Contents/Resources/NOTICE がリポジトリ直上の NOTICE と違います") {
		t.Errorf("古い NOTICE を検知しない: %v", problems)
	}
	if containsSubstring(problems, `"LICENSE"`) {
		t.Errorf("一致している LICENSE を問題にした: %v", problems)
	}
}

// ad-hoc 署名の後に同梱物を足して署名をやり直さなかったアプリは、未署名の配布物でも不合格にする。
func TestCheckDMGDetectsBrokenAdhocSeal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	app := filepath.Join(root, "ReqWeave.app")
	for _, d := range []string{"Contents/MacOS", "Contents/Resources"} {
		if err := os.MkdirAll(filepath.Join(app, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// ad-hoc 署名できる実体（テストのバイナリ自身を写す）。
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "ReqWeave"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>ReqWeave</string>
<key>CFBundleIdentifier</key><string>net.howashoji.reqweave.test</string>
</dict></plist>
`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("ad-hoc 署名できません: %v\n%s", err, out)
	}
	// 署名の後に同梱物を足す（やり直さない）。
	for name, body := range map[string]string{"LICENSE": testLicense, "NOTICE": testNotice} {
		if err := os.WriteFile(filepath.Join(app, "Contents", "Resources", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "初回起動の手順.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dmg := filepath.Join(t.TempDir(), "adhoc.dmg")
	if o, err := exec.Command("hdiutil", "create", "-volname", "ReqWeaveTest", "-srcfolder", root,
		"-ov", "-format", "UDZO", dmg).CombinedOutput(); err != nil {
		t.Fatalf("テスト用の dmg を作れません: %v\n%s", err, o)
	}
	problems, _, err := checkDMG(dmg, "ReqWeave.app", testLegalDir(t), false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "ad-hoc 署名が壊れています") {
		t.Errorf("署名の後に足した同梱物を検知しない: %v", problems)
	}
}
