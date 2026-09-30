package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture は VERSION と wails.json を持つ一時ディレクトリを作る。
func fixture(t *testing.T, version, productVersion string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(versionPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, versionPath), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wails := `{"name":"ReqWeave","info":{"productVersion":"` + productVersion + `"}}`
	if err := os.WriteFile(filepath.Join(root, wailsPath), []byte(wails), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCheckPassesWhenVersionsMatch(t *testing.T) {
	problems, err := check(fixture(t, "1.2.3", "1.2.3"))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("一致しているのに問題を報告した: %v", problems)
	}
}

// 不一致の注入で lint が失敗すること。
func TestCheckDetectsMismatch(t *testing.T) {
	problems, err := check(fixture(t, "1.2.3", "1.2.4"))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("問題 %d 件、期待 1 件: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "productVersion") || !strings.Contains(problems[0], "1.2.3") {
		t.Fatalf("問題の文言に正本と複製の値が出ていない: %q", problems[0])
	}
}

func TestCheckDetectsEmptyProductVersion(t *testing.T) {
	problems, err := check(fixture(t, "1.2.3", ""))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "空または不在") {
		t.Fatalf("空の productVersion を検知していない: %v", problems)
	}
}

// 形式不正のとき lint が失敗すること。
func TestCheckDetectsInvalidSemver(t *testing.T) {
	for _, bad := range []string{"1.2", "v1.2.3", "1.2.3-rc1", "0.0.0-dev", "01.2.3", "a.b.c"} {
		problems, err := check(fixture(t, bad, bad))
		if err != nil {
			t.Fatalf("check(%q): %v", bad, err)
		}
		// 形式不正は正本側の問題として 1 件出る（複製とは一致しているため不一致は出ない）。
		if len(problems) != 1 || !strings.Contains(problems[0], versionPath) {
			t.Errorf("形式不正 %q を検知していない: %v", bad, problems)
		}
	}
}

func TestCheckFailsWhenSourceOfTruthMissing(t *testing.T) {
	root := t.TempDir()
	if _, err := check(root); err == nil {
		t.Fatal("正本が無いのに誤りを返さなかった")
	}
}

// 実リポジトリの正本と wails.json が一致していること（検査そのものの現物確認）。
func TestRepositoryVersionsMatch(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	problems, err := check(root)
	if err != nil {
		t.Fatalf("check(app/): %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("リポジトリの版番号が食い違っている: %v", problems)
	}
}
