package projectstore

import (
	"strings"
	"testing"
)

/*
 * クラウド同期領域の検知。
 *
 * **当て推量である**ことを踏まえ、検知したときに作業を止めないこと・
 * 誤検知しやすい形（名前の一部が一致するだけ）で拾わないことを確かめる。
 */

func TestCloudSyncedLocationDetectsKnownProviders(t *testing.T) {
	cases := map[string]string{
		// macOS の新しい形（File Provider）。実機で確認した並び。
		"/Users/x/Library/CloudStorage/Box-Box/仕事/proj":                   "Box",
		"/Users/x/Library/CloudStorage/Dropbox/proj":                      "Dropbox",
		"/Users/x/Library/CloudStorage/GoogleDrive-name@example.com/proj": "Google ドライブ",
		"/Users/x/Library/CloudStorage/OneDrive-Contoso/proj":             "OneDrive",
		"/Users/x/Library/Mobile Documents/com~apple~CloudDocs/proj":      "iCloud Drive",
		// 従来の置き場所（両 OS）。
		"/Users/x/Dropbox/proj":            "Dropbox",
		"/Users/x/Google Drive/proj":       "Google ドライブ",
		"C:/Users/x/OneDrive - Contoso/pj": "OneDrive",
	}
	for path, want := range cases {
		got, ok := CloudSyncedLocation(path)
		if !ok || got != want {
			t.Errorf("%s: got %q ok=%v want %q", path, got, ok, want)
		}
	}
}

func TestCloudSyncedLocationIgnoresOrdinaryPaths(t *testing.T) {
	// 名前の一部がたまたま一致するだけのものを拾わない（誤検知で毎回注意が出ると読まれなくなる）。
	for _, path := range []string{
		"/Users/x/Documents/proj",
		"/Users/x/Boxing/proj",      // Box で始まるが別語
		"/Users/x/DropboxLike/proj", // Dropbox で始まるが別語
		"/Users/x/work/OneDriveOld", // OneDrive で始まるが別語
		"/Users/x/Mobile Documents", // Library の下でなければ iCloud と見なさない
	} {
		if label, ok := CloudSyncedLocation(path); ok {
			t.Errorf("%s を誤検知した: %q", path, label)
		}
	}
}

func TestCloudSyncWarningExplainsWhatToWatchFor(t *testing.T) {
	msg := CloudSyncWarning("/Users/x/Library/CloudStorage/Box-Box/proj")
	if msg == "" {
		t.Fatal("注意が出ない")
	}
	// 禁じずに、何に気をつければよいかを言う（利用者向けの文言の様式）。
	for _, want := range []string{"Box", "作業はできます", "別の端末"} {
		if !strings.Contains(msg, want) {
			t.Errorf("文言に %q が無い: %q", want, msg)
		}
	}
	if CloudSyncWarning("/Users/x/Documents/proj") != "" {
		t.Error("検知していないのに注意が出ている")
	}
}
