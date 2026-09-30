package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildZip は指定した項目だけを含む書庫を作る。
func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeZip(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dist.zip")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// macFat は fat（universal）の Mach-O ヘッダを持つ実行ファイルの中身を作る。
func macFat(arches byte) string {
	return string([]byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, arches}) + "rest of the binary"
}

// macThin は単一アーキテクチャ（64bit Mach-O）のヘッダ。
func macThin() string {
	return string([]byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01}) + "rest of the binary"
}

func goodMacEntries() map[string]string {
	return map[string]string{
		"ReqWeave.app/Contents/Info.plist":        "<plist/>",
		"ReqWeave.app/Contents/MacOS/ReqWeave":    macFat(2),
		"ReqWeave.app/Contents/Resources/LICENSE": testLicense,
		"ReqWeave.app/Contents/Resources/NOTICE":  testNotice,
		"初回起動の手順.md":                              "# ReqWeave — 初回起動の手順\n",
	}
}

func goodWinEntries() map[string]string {
	return map[string]string{
		"ReqWeave.exe": "binary",
		"初回起動の手順.md":   "# 手順\n",
		"LICENSE":      testLicense,
		"NOTICE":       testNotice,
	}
}

// 単一の配布物: 配布物は OS ごとに 1 個で、中身はアプリ本体と同梱の手順書だけ。
func TestCheckArchiveAcceptsExpectedLayout(t *testing.T) {
	problems, _, err := checkArchive(writeZip(t, buildZip(t, goodMacEntries())), "darwin", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("想定どおりの配布物で問題を報告した: %v", problems)
	}

	problems, _, err = checkArchive(writeZip(t, buildZip(t, goodWinEntries())), "windows", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("Windows 向けの配布物で問題を報告した: %v", problems)
	}
}

// 受け入れ条件: 同梱の手順書が無い配布物を検知すること。
func TestCheckArchiveDetectsMissingDoc(t *testing.T) {
	e := goodMacEntries()
	delete(e, "初回起動の手順.md")
	problems, _, err := checkArchive(writeZip(t, buildZip(t, e)), "darwin", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "同梱の手順書が無い") {
		t.Fatalf("手順書の欠落を検知していない: %v", problems)
	}
}

// アプリ本体が 0 件・2 件のときに検知すること（アップデータの選択条件と揃える）。
func TestCheckArchiveDetectsAppEntryCount(t *testing.T) {
	none := map[string]string{"初回起動の手順.md": "x"}
	problems, _, err := checkArchive(writeZip(t, buildZip(t, none)), "darwin", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "アプリ本体") {
		t.Fatalf("本体の欠落を検知していない: %v", problems)
	}

	two := goodMacEntries()
	two["ReqWeaveOld.app/Contents/Info.plist"] = "<plist/>"
	problems, _, err = checkArchive(writeZip(t, buildZip(t, two)), "darwin", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "想定外の項目") {
		t.Fatalf("余分な項目を検知していない: %v", problems)
	}
}

// 不要なファイル（.DS_Store / __MACOSX）を検知すること。
func TestCheckArchiveDetectsJunk(t *testing.T) {
	for _, junk := range []string{".DS_Store", "__MACOSX/._ReqWeave.app", "ReqWeave.app/Contents/.DS_Store"} {
		e := goodMacEntries()
		e[junk] = "x"
		problems, _, err := checkArchive(writeZip(t, buildZip(t, e)), "darwin", testLegalDir(t))
		if err != nil {
			t.Fatal(err)
		}
		if !containsSubstring(problems, "不要なファイル") {
			t.Errorf("%s を検知していない: %v", junk, problems)
		}
	}
}

// 受け入れ条件: Apple Silicon・Intel の双方に対応しているかを判定できること。
func TestUniversalCheck(t *testing.T) {
	// fat（2 アーキテクチャ）は合格。
	checked, ok, detail, err := universalCheck(writeZip(t, buildZip(t, goodMacEntries())))
	if err != nil {
		t.Fatal(err)
	}
	if !checked || !ok {
		t.Fatalf("universal binary を合格にしない: checked=%v ok=%v detail=%q", checked, ok, detail)
	}

	// 単一アーキテクチャは不合格。
	thin := goodMacEntries()
	thin["ReqWeave.app/Contents/MacOS/ReqWeave"] = macThin()
	checked, ok, detail, err = universalCheck(writeZip(t, buildZip(t, thin)))
	if err != nil {
		t.Fatal(err)
	}
	if !checked || ok {
		t.Fatalf("単一アーキテクチャを合格にした: checked=%v ok=%v detail=%q", checked, ok, detail)
	}

	// fat だがアーキテクチャが 1 つだけなら不合格。
	one := goodMacEntries()
	one["ReqWeave.app/Contents/MacOS/ReqWeave"] = macFat(1)
	_, ok, _, err = universalCheck(writeZip(t, buildZip(t, one)))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("1 アーキテクチャの fat を合格にした")
	}

	// 実行ファイルが無ければ判定できない。
	nobin := map[string]string{"初回起動の手順.md": "x"}
	checked, _, _, err = universalCheck(writeZip(t, buildZip(t, nobin)))
	if err != nil {
		t.Fatal(err)
	}
	if checked {
		t.Fatal("実行ファイルが無いのに判定したことになっている")
	}
}

func TestAppNameForRejectsUnknownOS(t *testing.T) {
	if _, err := appNameFor("linux"); err == nil {
		t.Error("対象外の OS を受け付けた")
	}
}

// 受け入れ条件: 同梱の手順書が実在し、macOS・Windows の双方について
// **現行の配布物で実際に通る手順**が書かれていること（管理者権限なしで、OS の警告を越えて起動できる手順）。
//
// 経緯: 本テストは 2 度、間違った手順を守り続けた。
//  1. macOS 14 までの「右クリック →『開く』」を固定していた（15 以降は廃止済み）。
//  2. 署名・公証を導入した後も「署名がありません」「管理者のユーザ名とパスワードが必要」を
//     固定し続けた。**配布物の実態が変わったら、手順書と本テストを同時に直す**。
//
// 現行（2026-09-04 に署名・公証済みの配布物を実機で確認した結果）:
// 隔離属性つきでも `spctl` は accepted（source=Notarized Developer ID）で、初回に出るのは
// 「インターネットからダウンロードされたアプリケーションです。開いてもよろしいですか？」の確認だけ。
// **管理者の資格情報は求められず、App Translocation も起きない**（承認後は元の場所から起動する）。
func TestBundledGuideContent(t *testing.T) {
	p := filepath.Join("..", "..", "build", "package", "初回起動の手順.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("同梱の手順書が存在しない: %v", err)
	}
	text := string(b)
	// **手順の本文**に操作が書かれていること（語がどこかに 1 回出るだけでは足りない）。
	for _, want := range []string{
		"## macOS の場合",
		"## Windows の場合",
		// 署名・公証済みであることと、管理者が不要であること（macOS 版は署名・公証して配る）。
		"Apple の公証（notarization）を受けています",
		"**管理者のユーザ名・パスワードは必要ありません。**",
		// 初回に出る確認とその押しかた（実機で確認した文言）。
		"インターネットからダウンロードされたアプリケーションです",
		"**「開く」** を押します",
		// Windows の SmartScreen 回避（Windows 版はコード署名をしない方針）。
		"**「詳細情報」** を押します",
		"**「実行」** を押します",
		// macOS の配布物は dmg。開いてからコピーする操作を書くこと。
		"macos.dmg` をダブルクリック**",
		"ドラッグしてコピー",
		"直接起動しないでください",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("手順書に %q の記載が無い", want)
		}
	}
	// **配布物の実態と食い違う記述を残さないこと**。署名・公証を導入した後にこれらが残っていると、
	// 利用者は「署名がないアプリ」と受け取り、要らない不安と手順（システム設定からの許可）へ誘導される。
	for _, obsolete := range []string{
		"開発元の署名がありません",
		"管理者のユーザ名とパスワードを入力",
		"「このまま開く」",  // 署名前の許可経路（もう出ない）
		"「ゴミ箱に入れる」", // 署名前の警告の既定ボタン（もう出ない）
		"**右クリック（または control キーを押しながらクリック）** して", // macOS 14 まで
	} {
		if strings.Contains(text, obsolete) {
			t.Errorf("署名・公証済みの配布物と食い違う記述が残っている: %q", obsolete)
		}
	}
	// OS の保護そのものを解除させる手順を書かないこと。
	for _, forbidden := range []string{"sudo", "xattr -d", "spctl --master-disable", "spctl --global-disable"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("OS の保護解除を要求する手順がある: %q", forbidden)
		}
	}
	// システム領域へ置かせないこと（自動更新がユーザー領域で動くため）。macOS・Windows の両方。
	if !strings.Contains(text, "Program Files") || !strings.Contains(text, "置かないでください") {
		t.Error("Windows でシステム領域へ置かない案内が無い")
	}
	if !strings.Contains(text, "`/Applications`）には置かないでください") {
		t.Error("macOS で /Applications へ置かない案内が無い（自動更新が成立しない）")
	}
	// 標準ユーザーでも必ず成功する置き場所を**主経路**として示すこと。
	macOS, _, found := strings.Cut(text, "## Windows の場合")
	if !found {
		t.Fatal("Windows の節が無く macOS の節を切り出せない")
	}
	home := strings.Index(macOS, "ホームフォルダの中の「アプリケーション」フォルダ")
	slash := strings.Index(macOS, "`/Applications`）には置かないでください")
	if home < 0 {
		t.Fatal("ホームフォルダ配下への配置案内が無い")
	}
	if slash >= 0 && slash < home {
		t.Error("/Applications の案内がホームフォルダ配下より先に出ている（標準ユーザーは主経路で必ず失敗する）")
	}
	if !strings.Contains(macOS, "名前を **`Applications`（半角・大文字始まり）** にします") {
		t.Error("~/Applications が無い場合の作り方が書かれていない（既定では存在しない）")
	}
	// 保存時に拡張子が変わる事象への対処（配信側の Content-Type 誤りで .man になった実測）。
	if !strings.Contains(text, "`.dmg` で終わっていない") {
		t.Error("拡張子が変わってしまったときの確認が無い")
	}
}

// dist と dist-windows が **互いの配布物を消さない**こと。
//
// リリースは macOS と Windows の配布物を同時に必要とする（更新マニフェストも
// 両 OS の資産を 1 つに列挙する）。どちらかの生成でもう一方が消えると、リリース作業が成立しない。
// 実際に両方を生成して確かめるのは重い（universal ビルド + Windows クロスビルドで数分）ため、
// **消し方の指定**を Makefile の記述として固定する。
func TestDistTargetsDoNotClobberEachOther(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)

	// 配布先ディレクトリごと消すと、もう一方の OS の配布物まで消える。
	// **レシピ行（タブ始まり）だけを見る**。コメントに同じ字面が出ても検知しない
	// （本テストを書いた際、注意書きのコメントを拾って誤検知した）。
	for i, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "\t") {
			continue
		}
		if strings.Contains(line, "rm -rf $(DIST_DIR)") {
			t.Errorf("dist 系が配布先ディレクトリごと削除している（もう一方の OS の配布物が消える）: %d 行目 %q", i+1, line)
		}
	}
	// それぞれ**自分の OS の書庫だけを、版を問わず**消していること。
	// 現行版の名前だけを消すと、版を上げたときに旧版の書庫が残り、e2e が
	// 「配布物が 1 個に定まりません（2 件）」で止まる（2026-09-10 に 0.1.0 → 0.1.1 で発生）。
	const (
		macClean = "mkdir -p $(DIST_DIR) && rm -f $(DIST_DIR)/ReqWeave-*-macos-unsigned.dmg"
		winClean = "mkdir -p $(DIST_DIR) && rm -f $(DIST_DIR)/ReqWeave-*-windows.zip"
	)
	for _, want := range []string{macClean, winClean} {
		if !strings.Contains(text, want) {
			t.Errorf("自分の配布物を版を問わず消す指定が無い: %q", want)
		}
	}
	// 消し方の字面ではなく**何に当たるか**で確かめる（パターンを誤って広げた・狭めたときに落とす）。
	patterns := map[string]string{
		"macOS":   "ReqWeave-*-macos-unsigned.dmg",
		"Windows": "ReqWeave-*-windows.zip",
	}
	cases := []struct {
		name string
		os   string // 当たるべき OS（"" はどちらにも当たってはならない）
	}{
		{"ReqWeave-0.1.0-macos-unsigned.dmg", "macOS"},
		{"ReqWeave-0.1.1-macos-unsigned.dmg", "macOS"}, // 版を問わない
		{"ReqWeave-0.1.0-windows.zip", "Windows"},
		{"ReqWeave-0.1.1-windows.zip", "Windows"},
		{"ReqWeave-0.1.1-macos.dmg", ""}, // リリース物と同名の残骸は別の行で消す
		{"ReqWeave.exe", ""},
	}
	for _, c := range cases {
		for osName, pat := range patterns {
			got, err := filepath.Match(pat, c.name)
			if err != nil {
				t.Fatal(err)
			}
			if want := osName == c.os; got != want {
				t.Errorf("%s の消し方 %q が %q に当たるか: %v（期待 %v。もう一方の OS の配布物を消す／旧版を残す）",
					osName, pat, c.name, got, want)
			}
		}
	}
	// 一時展開物の後始末は残っていること（書庫だけ残す）。
	for _, want := range []string{
		"rm -rf $(DMG_ROOT)", // dmg の材料置き場（作成後に消す）
		"cd $(DIST_DIR) && rm -f ReqWeave.exe *.md",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("一時展開物の後始末が無い: %q", want)
		}
	}
}

// Makefile の dist ターゲットが版番号の正本から配布物名を作ること。
func TestDistTargetsUseVersionSourceOfTruth(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{
		"APP_VERSION",
		// macOS は dmg。**ゲート用とリリース用で名前を分ける**。
		"ReqWeave-$(APP_VERSION)-macos-unsigned.dmg", // ゲート用（未署名）
		"ReqWeave-$(APP_VERSION)-macos.dmg",          // リリース用（署名・公証つき。名前は変えられない）
		"ReqWeave-$(APP_VERSION)-windows.zip",
		// dmg の作成。**/Applications へのリンクを入れない**ため、
		// 材料の置き場には .app と手順書だけを入れる。
		"hdiutil create -volname ReqWeave -srcfolder $(DMG_ROOT)",
		"-platform darwin/universal", // Apple Silicon・Intel の双方で動く
		"go run ./tools/distcheck",   // 生成のたびに中身を検査する
		// WebView2 未導入時にアプリ自身が取得しに行かない（通信先を増やさない）。
		// **実行される行**に指定があることを見る（説明のコメントに書いてあるだけでは足りない）。
		"build -trimpath -platform windows/amd64 -webview2 browser",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Makefile に %q が無い", want)
		}
	}
}

// 手で確認するための起動が、利用者の実データへ触れない手立てを持つこと。
//
// 普通に起動すると ~/Library/Application Support/… の実データ（利用者 ID・表示名・
// キー参照名）を読み書きし、初回起動の見え方を確かめられないうえ、画面に実データが出る。
func TestRunCleanTargetIsolatesHome(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "run-clean:") {
		t.Fatal("Makefile に run-clean ターゲットが無い（隔離起動の手立て）")
	}
	// 実行される行で $HOME を一時フォルダへ向けていること（説明のコメントでは足りない）。
	var recipe []string
	inRecipe := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "run-clean:") {
			inRecipe = true
			continue
		}
		if inRecipe {
			if !strings.HasPrefix(line, "\t") && strings.TrimSpace(line) != "" {
				break
			}
			recipe = append(recipe, line)
		}
	}
	joined := strings.Join(recipe, "\n")
	if !strings.Contains(joined, "mktemp -d") {
		t.Errorf("run-clean が一時フォルダを作っていない: %q", joined)
	}
	if !strings.Contains(joined, "HOME=") {
		t.Errorf("run-clean が $HOME を差し替えていない: %q", joined)
	}
	// $HOME を差し替えると macOS はログインキーチェーンを見失い、
	// シークレットキーを登録できない（初期設定が完了できず AI 機能を確認できない）。
	// Keychains だけは実環境へリンクしておくこと。
	if !strings.Contains(joined, "Library/Keychains") {
		t.Errorf("run-clean がキーチェーンを参照可能にしていない: %q", joined)
	}
	if !strings.Contains(joined, "ln -s") {
		t.Errorf("run-clean がキーチェーンへのリンクを張っていない: %q", joined)
	}
}

// 配布物を作るビルドが必ず -trimpath を付けること。
// 付け忘れるとビルドしたマシンの利用者名とディレクトリ構成が実行ファイルへ埋め込まれ、
// **配布物を取得した全員に見える**（2026-09-04 に macOS 1940 件・Windows 1085 件で実測）。
func TestBuildTargetsUseTrimpath(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	// 実行される行だけを見る（コメントに書いてあるだけでは足りない）。
	var recipes []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "\t") && strings.Contains(line, "$(WAILS) build") {
			recipes = append(recipes, strings.TrimSpace(line))
		}
	}
	if len(recipes) == 0 {
		t.Fatal("Makefile に wails build の行が無い（検査が空振りしている）")
	}
	for _, r := range recipes {
		if !strings.Contains(r, "-trimpath") {
			t.Errorf("wails build に -trimpath が無い: %q", r)
		}
	}
}

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
