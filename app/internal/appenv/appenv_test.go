package appenv

// 実行環境の識別（診断情報に添える OS の種別と版）の単体テスト。

import (
	"runtime"
	"strings"
	"testing"
)

func TestOSName(t *testing.T) {
	got := OSName()
	if got == "" {
		t.Fatal("OS の種別が空")
	}
	// 配布対象（macOS / Windows）は利用者が知る表記にする。
	want := map[string]string{"darwin": "macOS", "windows": "Windows", "linux": "Linux"}
	if expected, ok := want[runtime.GOOS]; ok && got != expected {
		t.Errorf("OSName() = %q（期待 %q）", got, expected)
	}
}

func TestArch(t *testing.T) {
	if Arch() != runtime.GOARCH {
		t.Errorf("Arch() = %q（期待 %q）", Arch(), runtime.GOARCH)
	}
}

// 配布対象 OS では版を取得できること（取得できないと診断情報の項目が欠ける）。
// 配布対象外（開発用の Linux 等）では空を返す規定であり、偽の値で埋めない。
func TestOSVersion(t *testing.T) {
	got := OSVersion()
	switch runtime.GOOS {
	case "darwin", "windows":
		if got == "" {
			t.Fatalf("%s で OS の版を取得できない", runtime.GOOS)
		}
		if !strings.ContainsAny(got, "0123456789") {
			t.Errorf("OS の版が版番号に見えない: %q", got)
		}
	default:
		if got != "" {
			t.Errorf("配布対象外の OS で版を返した: %q", got)
		}
	}
}

// 端末を特定できる値を返さないこと（診断情報に個人・端末を特定する情報を残さない）。
func TestOSVersionHasNoTerminalIdentity(t *testing.T) {
	for _, v := range []string{OSName(), OSVersion(), Arch()} {
		if strings.Contains(v, "/") || strings.Contains(v, `\`) {
			t.Errorf("パスらしき値を返した: %q", v)
		}
	}
}
