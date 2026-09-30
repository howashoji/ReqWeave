package projectstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
 * プロジェクトフォルダの名前付け。
 *
 * 期待値は「macOS / Windows の双方で有効なフォルダ名になること」から導いた。
 * 名前は表示上の手掛かりであり、**識別の根拠にはしない**（判定は project.yaml の存在）。
 */

func TestProjectFolderNameAddsExtension(t *testing.T) {
	got := ProjectFolderName("在庫管理システム")
	if want := "在庫管理システム" + ProjectFolderExt; got != want {
		t.Fatalf("フォルダ名が違う: got %q want %q", got, want)
	}
}

func TestProjectFolderNameReplacesUnusableCharacters(t *testing.T) {
	// Windows が禁じる文字とパス区切りは、そのままだと保存できない。
	for _, in := range []string{`A/B`, `A\B`, `A:B`, `A*B`, `A?B`, `A"B`, `A<B`, `A>B`, `A|B`} {
		got := ProjectFolderName(in)
		if strings.ContainsAny(strings.TrimSuffix(got, ProjectFolderExt), `<>:"/\|?*`) {
			t.Fatalf("使えない文字が残っている: %q → %q", in, got)
		}
	}
}

func TestProjectFolderNameDropsTrailingDotAndSpace(t *testing.T) {
	// Windows は末尾の `.` と空白を保存できない（作った直後に開けなくなる）。
	for _, in := range []string{"在庫管理.", "在庫管理 ", "在庫管理. "} {
		got := ProjectFolderName(in)
		base := strings.TrimSuffix(got, ProjectFolderExt)
		if strings.HasSuffix(base, ".") || strings.HasSuffix(base, " ") {
			t.Fatalf("末尾の . / 空白が残っている: %q → %q", in, got)
		}
	}
}

func TestProjectFolderNameNeverEmpty(t *testing.T) {
	// 名前が付けられずに作成が止まる状態を作らない。
	for _, in := range []string{"", "   ", "///", "..."} {
		got := ProjectFolderName(in)
		if got == ProjectFolderExt {
			t.Fatalf("名前が空になっている: %q → %q", in, got)
		}
	}
}

func TestProjectFolderNameLimitsLength(t *testing.T) {
	// パス長の上限に、プロジェクト内の深いパスを足しても収まるようにする。
	// **文字数**で数える（日本語の名前を極端に短くしない）。
	long := strings.Repeat("在", 200)
	base := strings.TrimSuffix(ProjectFolderName(long), ProjectFolderExt)
	if n := len([]rune(base)); n != folderNameMaxRunes {
		t.Fatalf("長さの上限が効いていない: %d 文字", n)
	}
}

func TestUniqueProjectPathAvoidsOverwriting(t *testing.T) {
	parent := t.TempDir()

	first, err := UniqueProjectPath(parent, "在庫管理システム")
	if err != nil {
		t.Fatalf("パスを作れない: %v", err)
	}
	if filepath.Base(first) != "在庫管理システム"+ProjectFolderExt {
		t.Fatalf("1 件目の名前が違う: %q", filepath.Base(first))
	}
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatalf("作成できない: %v", err)
	}

	second, err := UniqueProjectPath(parent, "在庫管理システム")
	if err != nil {
		t.Fatalf("パスを作れない: %v", err)
	}
	if second == first {
		t.Fatal("既存を上書きするパスを返している")
	}
	if filepath.Base(second) != "在庫管理システム 2"+ProjectFolderExt {
		t.Fatalf("2 件目の名前が違う: %q", filepath.Base(second))
	}
}
