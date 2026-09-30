package updater

import (
	"path/filepath"
	"strings"
	"testing"
)

// 受け入れ条件: 置換対象がユーザー領域の外にあるとき適用器を作らないこと。
// システム領域（/Applications 直下・Program Files 等）へ書き込む経路を持たない（管理者権限を要さない）。
func TestNewApplierRejectsTargetOutsideUserArea(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "users", "someone")

	outside := []string{
		filepath.Join(string(filepath.Separator), "Applications", "ReqWeave.app"),
		filepath.Join(string(filepath.Separator), "usr", "local", "bin", "ReqWeave"),
		filepath.Join(string(filepath.Separator), "users", "someone-else", "ReqWeave.app"),
		// 相対パスの上りで抜けようとする経路
		filepath.Join(home, "Applications", "..", "..", "ReqWeave.app"),
	}
	for _, target := range outside {
		if _, err := newApplier(target, home); err == nil {
			t.Errorf("ユーザー領域の外を対象にできてしまった: %s", target)
		} else if k, _ := KindOf(err); k != KindNotUserArea {
			t.Errorf("%s: Kind = %q, want %q", target, k, KindNotUserArea)
		}
	}

	inside := filepath.Join(home, "Applications", "ReqWeave.app")
	a, err := newApplier(inside, home)
	if err != nil {
		t.Fatalf("ユーザー領域内の対象を拒否した: %v", err)
	}
	// 作業場所は置換対象の隣（最後の rename がボリュームをまたがないため）。
	if want := filepath.Join(home, "Applications", stagingDirName); a.stagingDir() != want {
		t.Fatalf("作業場所 = %q, want %q", a.stagingDir(), want)
	}
	if !strings.HasPrefix(a.stagingDir(), home) {
		t.Fatalf("作業場所がユーザー領域の外: %s", a.stagingDir())
	}
	if a.client.Timeout <= 0 {
		t.Fatal("取得のタイムアウトが設定されていない")
	}
}

func TestUnderDir(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "a", "b")
	cases := map[string]bool{
		filepath.Join(root, "c"):      true,
		root:                          true,
		filepath.Join(root, "c", "d"): true,
		filepath.Join(string(filepath.Separator), "a"):       false,
		filepath.Join(string(filepath.Separator), "a", "bb"): false,
		filepath.Join(root, "..", "x"):                       false,
	}
	for child, want := range cases {
		if got := underDir(root, child); got != want {
			t.Errorf("underDir(%q, %q) = %v, want %v", root, child, got, want)
		}
	}
}
