package main

import (
	"os/user"
	"strings"
	"testing"
)

// 実行ファイルにビルド環境の絶対パスが混入していたら不合格にすること。
// （2026-09-04 に macOS 1940 件・Windows 1085 件の混入を手で見つけた。機械で止める）
func TestCheckLeaksDetectsBuildPathsInExecutable(t *testing.T) {
	cases := []struct {
		name    string
		entries map[string]string
		goos    string
		want    string
	}{
		{
			name: "macOS の実行ファイルに /Users/ が残っている",
			entries: map[string]string{
				"ReqWeave.app/Contents/Info.plist":     "<plist/>",
				"ReqWeave.app/Contents/MacOS/ReqWeave": macFat(2) + "/Users/someone/go/src/reqweave/main.go",
				"初回起動の手順.md":                           "# 手順\n",
			},
			goos: "darwin",
			want: `"/Users/"`,
		},
		{
			name: "Windows の実行ファイルに \\Users\\ が残っている",
			entries: map[string]string{
				"ReqWeave.exe": `binary C:\Users\builder\src\reqweave\main.go`,
				"初回起動の手順.md":   "# 手順\n",
			},
			goos: "windows",
			want: `"\\Users\\"`,
		},
		{
			name: "Linux ホストでビルドして /home/ が残っている",
			entries: map[string]string{
				"ReqWeave.exe": "binary /home/builder/src/reqweave/main.go",
				"初回起動の手順.md":   "# 手順\n",
			},
			goos: "windows",
			want: `"/home/"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems, _, err := checkArchive(writeZip(t, buildZip(t, tc.entries)), tc.goos, testLegalDir(t))
			if err != nil {
				t.Fatal(err)
			}
			if !containsSubstring(problems, "混入している") {
				t.Fatalf("実行ファイルへのビルドパス混入を検知していない: %v", problems)
			}
			if !containsSubstring(problems, tc.want) {
				t.Fatalf("検出したパターン %s が報告に含まれない: %v", tc.want, problems)
			}
			if !containsSubstring(problems, "-trimpath") {
				t.Fatalf("直し方（-trimpath）が報告に含まれない: %v", problems)
			}
		})
	}
}

// 同梱の手順書が持つ「説明のための例」を誤検出しないこと。
// 手順書は C:\Users\<あなたのユーザー名>\… のような一般形を正しい記述として含む。
func TestCheckLeaksAllowsPlaceholderPathsInGuide(t *testing.T) {
	e := goodMacEntries()
	e["初回起動の手順.md"] = "# 手順\n\n" +
		`1. ダウンロードした dmg を開き、ReqWeave.app を「アプリケーション」へドラッグします。` + "\n" +
		`2. Windows では C:\Users\<あなたのユーザー名>\AppData\Local\ReqWeave に置かれます。` + "\n" +
		`3. macOS の設定は /Users/<あなたのユーザー名>/Library/Application Support に作られます。` + "\n"
	problems, _, err := checkArchive(writeZip(t, buildZip(t, e)), "darwin", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("手順書の説明のための例を混入として報告した: %v", problems)
	}
}

// 設定・資格情報・鍵のファイルが同梱されていたら不合格にすること（配布物は初期状態）。
func TestCheckLeaksDetectsBundledSecrets(t *testing.T) {
	cases := []struct {
		entry string
		want  string
	}{
		{"ReqWeave.app/Contents/Resources/settings.json", "設定・資格情報のファイル"},
		{"ReqWeave.app/Contents/Resources/.env", "設定・資格情報のファイル"},
		{"ReqWeave.app/Contents/Resources/AuthKey.p8", "鍵・証明書のファイル"},
		{"ReqWeave.app/Contents/Resources/developer.pem", "鍵・証明書のファイル"},
	}
	for _, tc := range cases {
		t.Run(tc.entry, func(t *testing.T) {
			e := goodMacEntries()
			e[tc.entry] = "x"
			problems, _, err := checkArchive(writeZip(t, buildZip(t, e)), "darwin", testLegalDir(t))
			if err != nil {
				t.Fatal(err)
			}
			if !containsSubstring(problems, tc.want) {
				t.Fatalf("%s の同梱を検知していない: %v", tc.entry, problems)
			}
		})
	}
}

// ビルドしたマシンの利用者のホームのパスは、実行ファイル以外に混入していても検知すること。
func TestCheckLeaksDetectsBuilderHomePathInAnyFile(t *testing.T) {
	u := currentUserHomeMarkerForTest(t)
	e := goodMacEntries()
	e["ReqWeave.app/Contents/Resources/build-info.txt"] = "built from " + u + "/src/reqweave\n"
	problems, _, err := checkArchive(writeZip(t, buildZip(t, e)), "darwin", testLegalDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(problems, "利用者のホームのパス") {
		t.Fatalf("実行ファイル以外への利用者ホームのパス混入を検知していない: %v", problems)
	}
	if !strings.Contains(strings.Join(problems, "\n"), u) {
		t.Fatalf("報告に該当のパス %q が含まれない: %v", u, problems)
	}
}

// currentUserHomeMarkerForTest は checkLeaks が照合する「自分のホームのパス」を返す。
// 取得できない環境では検査自体が「未実施」になるため、テストも成立しない。
func currentUserHomeMarkerForTest(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil || u.Username == "" {
		t.Fatalf("ビルドしたマシンの利用者名を取得できません: %v", err)
	}
	return "/Users/" + u.Username
}
