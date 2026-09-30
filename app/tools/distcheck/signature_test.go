package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 署名・公証の検査。
//
// **証明書を持たない環境でも回るテストにする**。Developer ID の署名は再現できないため、
// 「配布用の署名が無い状態」（未署名・ad-hoc）の判定を実物の codesign で確かめ、
// 署名済みの読み取り（署名者・hardened runtime）は `codesign --display` の出力の解釈で確かめる。

// makeApp は検査対象の .app を作る。adhoc=true なら実物の codesign で ad-hoc 署名を付ける。
func makeApp(t *testing.T, adhoc bool) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "ReqWeave.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 署名の対象は Mach-O でなければならないため、OS の実行ファイルを複製して使う。
	exe := filepath.Join(app, "Contents", "MacOS", "ReqWeave")
	if out, err := exec.Command("cp", "/bin/echo", exe).CombinedOutput(); err != nil {
		t.Fatalf("実行ファイルを用意できません: %v\n%s", err, out)
	}
	// **複製元は Apple が署名済み**（複製しても署名は実行ファイルの中に残る）。
	// 「未署名」の状態を作るには明示的に外す（外さないと未署名の判定を検証できない）。
	if out, err := exec.Command("codesign", "--remove-signature", exe).CombinedOutput(); err != nil {
		t.Fatalf("複製元の署名を外せません: %v\n%s", err, out)
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
	if adhoc {
		if out, err := exec.Command("codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
			t.Fatalf("ad-hoc 署名を付けられません: %v\n%s", err, out)
		}
	}
	return app
}

// `codesign --display` の実出力の書式（Developer ID + hardened runtime）を読めること。
func TestParseCodesignDisplayReadsDeveloperIDAndHardenedRuntime(t *testing.T) {
	out := `Executable=/Users/x/ReqWeave.app/Contents/MacOS/ReqWeave
Identifier=net.howashoji.reqweave
Format=app bundle with Mach-O universal (x86_64 arm64)
CodeDirectory v=20500 size=83887 flags=0x10000(runtime) hashes=2615+3 location=embedded
Signature size=9012
Timestamp=2026年9月3日 17:00:00
Info.plist entries=12
TeamIdentifier=A1B2C3D4E5
Runtime Version=15.0.0
Sealed Resources version=2 rules=13 files=2
Internal requirements count=1 size=200
Authority=Developer ID Application: Example Corp (A1B2C3D4E5)
Authority=Developer ID Certification Authority
Authority=Apple Root CA
`
	info := parseCodesignDisplay(out)
	if got := info.developerID(); got != "Developer ID Application: Example Corp (A1B2C3D4E5)" {
		t.Errorf("署名者を読めていない: %q", got)
	}
	if !info.hardened {
		t.Error("hardened runtime（flags=0x10000(runtime)）を読めていない。公証の必須条件の判定が効かない")
	}
	if info.adhoc {
		t.Error("Developer ID 署名を ad-hoc と誤判定した")
	}
}

// hardened runtime **なし**の署名を「あり」と読まないこと（読み違えると未公証の配布物が通る）。
func TestParseCodesignDisplayDetectsMissingHardenedRuntime(t *testing.T) {
	out := `Identifier=net.howashoji.reqweave
CodeDirectory v=20400 size=83887 flags=0x0(none) hashes=2615+3 location=embedded
Authority=Developer ID Application: Example Corp (A1B2C3D4E5)
`
	info := parseCodesignDisplay(out)
	if info.hardened {
		t.Error("hardened runtime が無いのに「あり」と判定した")
	}
}

// **ad-hoc 署名を「署名あり」と数えないこと**。Go / Wails の macOS ビルドは自動的に
// ad-hoc 署名を付けるため、ここを取り違えると未署名の配布物が署名済みとして通る。
func TestParseCodesignDisplayDetectsAdhoc(t *testing.T) {
	out := `Identifier=net.howashoji.reqweave
CodeDirectory v=20400 size=83887 flags=0x2(adhoc) hashes=2615+3 location=embedded
Signature=adhoc
TeamIdentifier=not set
`
	info := parseCodesignDisplay(out)
	if !info.adhoc {
		t.Error("ad-hoc 署名を検知しない")
	}
	if info.developerID() != "" {
		t.Error("ad-hoc 署名に Developer ID があると判定した")
	}
}

// make dist（require=false）: 配布用の署名が無くても**「未実施」と明示して**緑にする。
// 黙って飛ばさない（アイコンの検査の 2 段構成と同じ方針）。
func TestSignatureCheckReportsNotPerformedWhenNotRequired(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		adhoc      bool
	}{
		{name: "未署名", want: "署名なし", adhoc: false},
		{name: "ad-hoc署名", want: "ad-hoc 署名", adhoc: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := makeApp(t, tc.adhoc)
			problems, notes, err := checkSignature(app, filepath.Join(t.TempDir(), "none.dmg"), false)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 0 {
				t.Errorf("署名なしの配布物（make dist の正常な出力）を不合格にした: %v", problems)
			}
			if !containsSubstring(notes, "未実施") || !containsSubstring(notes, tc.want) {
				t.Errorf("未実施であることを明示していない: %v", notes)
			}
		})
	}
}

// make dist-release（require=true）: **署名なしの配布物を不合格にすること**（故障注入）。
func TestSignatureCheckRejectsUnsignedWhenRequired(t *testing.T) {
	app := makeApp(t, false)
	problems, _, err := checkSignature(app, filepath.Join(t.TempDir(), "none.dmg"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "Developer ID 署名がありません") {
		t.Errorf("署名なしの配布物を不合格にしない: %v", problems)
	}
	if !containsSubstring(problems, "署名なし") {
		t.Errorf("どの状態で落ちたのかが出力から分からない: %v", problems)
	}
}

// **ad-hoc 署名も不合格**。「署名が付いている」ことだけを見ると、Go / Wails が自動で付ける
// ad-hoc 署名で検査がすり抜ける。
func TestSignatureCheckRejectsAdhocWhenRequired(t *testing.T) {
	app := makeApp(t, true)
	problems, _, err := checkSignature(app, filepath.Join(t.TempDir(), "none.dmg"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "ad-hoc 署名") {
		t.Errorf("ad-hoc 署名を不合格にしない: %v", problems)
	}
}

// Makefile の dist-release が「署名 → 公証 → ステープル → 検査」を**この順で**行うこと。
// リリースは滅多に実行しないため、段の欠落を機械で押さえる。
func TestDistReleaseTargetSignsNotarizesAndStaples(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	// 段の内容。**hardened runtime とタイムスタンプは公証の必須条件**。
	wants := []string{
		"--options runtime",            // hardened runtime
		"--timestamp",                  // 署名のタイムスタンプ
		"notarytool submit",            // 公証への提出
		"--wait",                       // 結果を待つ
		"--keychain-profile",           // 資格情報を引数に置かない
		"stapler staple",               // チケットの添付
		"distcheck -require-signature", // 署名・公証の検査を必須にする
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("dist-release に %q が無い", want)
		}
	}
	// 公証の結果を**受理されたことまで**確かめること（submit の終了コードだけでは足りない）。
	if !strings.Contains(text, "status: Accepted") {
		t.Error("公証が受理されたことの確認が無い（notarytool の終了コードだけで判定している）")
	}
	// **アプリ本体と dmg の双方へステープルする**。アプリ本体に無いと、利用者が dmg から
	// 取り出した .app をオフラインで検証できない。
	staples := strings.Count(text, "stapler staple")
	if staples < 2 {
		t.Errorf("ステープルが %d か所しかない（アプリ本体と dmg の双方に要る）", staples)
	}

	// **再開点があること**（実測: 公証は数時間かかることがあり、dmg の公証で失敗したときに
	// ビルドと .app の公証をやり直すのは現実的でない）。
	if !strings.Contains(text, "dist-release-notarize:") {
		t.Error("公証の再開点（dist-release-notarize）が無い。失敗するとビルドからやり直しになる")
	}
	if !strings.Contains(recipeOf(text, "dist-release:"), "$(MAKE) dist-release-notarize") {
		t.Error("dist-release が再開点を呼んでいない（同じ段が 2 か所に書かれると片方だけ直る）")
	}

	// **公証の資格情報を先に確かめること**。画面ロック中は読めず、
	// 事前確認が無いと**ビルドと署名を終えたあとに失敗する**（実測 2 回）。
	if !strings.Contains(text, "define require_notary") {
		t.Error("公証の資格情報の事前確認（require_notary）が無い")
	}
	for _, target := range []string{"dist-release:", "dist-release-notarize:"} {
		if !strings.Contains(recipeOf(text, target), "$(call require_notary)") {
			t.Errorf("%s が公証の資格情報を事前確認していない", target)
		}
	}

	// **リリース物とゲート用の配布物が同じパスを共有しないこと**。
	// 共有すると、検証ゲートの test-e2e（dist に依存）が署名・公証済みの配布物を未署名で上書きする。
	// 公証は数時間かかることがあり、失うと再ビルド・再公証からやり直しになる。
	releaseRecipe := recipeOf(text, "dist-release:")
	if strings.Contains(releaseRecipe, "$(MAC_DMG)") {
		t.Error("dist-release がゲート用の配布物パス（MAC_DMG）へ書いている。test-e2e に上書きされる")
	}
	// **作りかけの名前で作り、全段が通ってから公開候補へ置き換えること**。
	// dist-release は置き換えを dist-release-verify へ委譲しているため、委譲の呼び出しと
	// 委譲先の置き換えの**両方**を見る（片方だけ見ると、置き換えが消えても気づけない）。
	if !strings.Contains(releaseRecipe, "$(MAC_RELEASE_TMP)") {
		t.Error("dist-release が作りかけの名前（MAC_RELEASE_TMP）で作っていない")
	}
	if strings.Contains(releaseRecipe, "rm -f $(MAC_RELEASE_DMG)") {
		t.Error("dist-release が開始時に公開候補を消している。失敗すると署名・公証済みの配布物を失う")
	}
	if !strings.Contains(releaseRecipe, "$(MAKE) dist-release-verify") {
		t.Error("dist-release が検査・置き換えの段（dist-release-verify）を呼んでいない")
	}
	verifyRecipe := recipeOf(text, "dist-release-verify:")
	if !strings.Contains(verifyRecipe, "mv -f $(MAC_RELEASE_TMP) $(MAC_RELEASE_DMG)") {
		t.Error("作りかけを公開候補（MAC_RELEASE_DMG）へ置き換える段が無い")
	}
	if !strings.Contains(verifyRecipe, "distcheck -require-signature") {
		t.Error("置き換えの前に署名・公証を検査していない")
	}
	if strings.Contains(recipeOf(text, "dist:"), "$(MAC_RELEASE_DMG)") {
		t.Error("dist（ゲート用）がリリース物のパスを触っている")
	}
	// **ゲート用とリリース用の配布物が同名でないこと**（2026-09-04 の配布事故）。
	// 置き場所を分けただけでは、コピー・アップロードの段で人が取り違える。
	if !strings.Contains(text, "-macos-unsigned.dmg") {
		t.Error("ゲート用の配布物に名前の印（-unsigned）が無い。リリース物と取り違える")
	}
	if strings.Contains(text, "MAC_DMG     := $(DIST_DIR)/ReqWeave-$(APP_VERSION)-macos.dmg") {
		t.Error("ゲート用とリリース用の配布物が同名になっている")
	}

	if strings.Contains(recipeOf(text, "clean:"), "build/release") {
		t.Error("clean がリリース物を消している。作り直しに数時間かかるため巻き添えで消さない")
	}

	// **make dist（開発・検証ゲート用）では署名・公証をしないこと**。
	// ここに混ぜると、検証ゲートが毎回ネットワークと数分を要求するようになる。
	distRecipe := recipeOf(text, "dist:")
	for _, forbidden := range []string{"codesign", "notarytool", "stapler"} {
		if strings.Contains(distRecipe, forbidden) {
			t.Errorf("dist（ゲート用）に %q がある。検証ゲートがネットワーク必須になる: %q", forbidden, distRecipe)
		}
	}
}

// recipeOf は Makefile から指定したターゲットのレシピ（タブ始まりの行）を切り出す。
func recipeOf(makefile, target string) string {
	lines := strings.Split(makefile, "\n")
	var out []string
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, target) {
			in = true
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(line, "\t") || strings.TrimSpace(line) == "" {
			out = append(out, line)
			continue
		}
		break
	}
	return strings.Join(out, "\n")
}
