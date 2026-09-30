package binding

import (
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/appversion"
)

func TestAppInfoReturnsAppNameAndVersion(t *testing.T) {
	// アプリ名は ReqWeave。画面表示・ウィンドウタイトルの正本。
	if AppName != "ReqWeave" {
		t.Fatalf("AppName = %q, want %q", AppName, "ReqWeave")
	}

	original := Version
	t.Cleanup(func() { Version = original })
	Version = "1.2.3"

	got := New().AppInfo()

	if got.Name != "ReqWeave" {
		t.Errorf("AppInfo().Name = %q, want %q", got.Name, "ReqWeave")
	}
	if got.Version != "1.2.3" {
		t.Errorf("AppInfo().Version = %q, want %q", got.Version, "1.2.3")
	}
}

// 実行時の版番号は正本（internal/appversion/VERSION）と一致する。
// ビルドフラグを与えないテスト実行でも一致すること（埋め込みで解決するため）。
func TestVersionMatchesSourceOfTruth(t *testing.T) {
	if Version != appversion.Version() {
		t.Fatalf("binding.Version = %q, 正本 = %q", Version, appversion.Version())
	}
	if _, err := appversion.ParseSemver(Version); err != nil {
		t.Fatalf("binding.Version = %q が major.minor.patch 形式ではない: %v", Version, err)
	}
}
