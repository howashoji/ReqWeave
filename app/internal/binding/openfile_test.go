package binding

// 単体テスト（ファイル I/O なし）。実行: make -C app test-unit
//
// 受け渡しファイルを OS から受け取る経路の分岐を固定する。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// 受け入れ条件: 拡張子で「回答モードで開く / 取込導線へ / 開けない」を分けること。
func TestClassifyOpenFile(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		wantKind string
	}{
		{"発行用", "/tmp/QS-001.rwvq", OpenFileRespond},
		{"発行用（大文字）", "/tmp/QS-001.RWVQ", OpenFileRespond},
		{"返送用", "/tmp/QS-001-return.rwva", OpenFileImport},
		{"返送用（大文字混じり）", "/tmp/QS-001-return.RwvA", OpenFileImport},
		{"対応しない形式", "/tmp/議事録.docx", OpenFileUnsupported},
		{"拡張子なし", "/tmp/README", OpenFileUnsupported},
		{"紛らわしい名前（拡張子ではない）", "/tmp/QS-001.rwvq.txt", OpenFileUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOpenFile(tc.path)
			if got.Kind != tc.wantKind {
				t.Fatalf("区分が %q（期待 %q）", got.Kind, tc.wantKind)
			}
			if got.FileName == "" {
				t.Error("表示用のファイル名が空")
			}
			switch tc.wantKind {
			case OpenFileUnsupported:
				// 受け入れ条件: 原因と次の行動を示すこと。落ちないこと（例外にしない）。
				if got.Message == "" {
					t.Error("開けない理由の文言が空")
				}
				if !strings.Contains(got.Message, ".rwvq") || !strings.Contains(got.Message, ".rwva") {
					t.Errorf("次に取る行動（開ける種類）が示されていない: %q", got.Message)
				}
				if got.FilePath != "" {
					t.Errorf("開けないファイルのパスを画面へ渡している: %q", got.FilePath)
				}
			default:
				if got.FilePath != tc.path {
					t.Errorf("ファイルパスが %q（期待 %q）", got.FilePath, tc.path)
				}
				if got.Message != "" {
					t.Errorf("正常な受け取りでエラー文言が付いている: %q", got.Message)
				}
			}
		})
	}
}

// 起動引数からファイルを拾う際、実行ファイル自身とフラグを取り違えないこと。
func TestFileArg(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		want  string
		found bool
	}{
		{"引数なし", []string{"ReqWeave"}, "", false},
		{"ファイル 1 件", []string{"ReqWeave", "/tmp/QS-001.rwvq"}, "/tmp/QS-001.rwvq", true},
		{"フラグは飛ばす", []string{"ReqWeave", "--loglevel", "/tmp/QS-001.rwva"}, "/tmp/QS-001.rwva", true},
		{"フラグだけ", []string{"ReqWeave", "--debug"}, "", false},
		{"空文字は飛ばす", []string{"ReqWeave", "", "/tmp/QS-001.rwvq"}, "/tmp/QS-001.rwvq", true},
		{"実行ファイルだけを渡さない", []string{"/Applications/ReqWeave.app/Contents/MacOS/ReqWeave"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := fileArg(tc.args)
			if found != tc.found || got != tc.want {
				t.Fatalf("結果が (%q, %v)（期待 (%q, %v)）", got, found, tc.want, tc.found)
			}
		})
	}
}

// 受け入れ条件: macOS の open-file イベントで届いたパスで回答モードになること
// （起動引数だけに依存しないこと）。
func TestOpenFileEventDrivesRespondentModeWithoutArgs(t *testing.T) {
	saved := os.Args
	os.Args = []string{"ReqWeave"} // Finder のダブルクリックでは引数が付かない
	t.Cleanup(func() { os.Args = saved })

	a := &API{}
	if a.StartupMode().Mode != ModeOwner {
		t.Fatal("受け取る前から回答モードになっている")
	}
	a.HandleOpenFile("/tmp/QS-007.rwvq")

	got := a.StartupMode()
	if got.Mode != ModeRespondent {
		t.Fatalf("モードが %q（期待 %q）", got.Mode, ModeRespondent)
	}
	if got.FilePath != "/tmp/QS-007.rwvq" || got.FileName != "QS-007.rwvq" {
		t.Fatalf("開く対象が %+v", got)
	}
}

// 返送ファイルを open-file で受け取っても回答モードにはしない（担当者側の取込対象）。
// 画面へは取込導線として渡す。
func TestReturnFileGoesToImportNotRespondent(t *testing.T) {
	saved := os.Args
	os.Args = []string{"ReqWeave"}
	t.Cleanup(func() { os.Args = saved })

	a := &API{}
	a.HandleOpenFile("/tmp/QS-007-return.rwva")

	if mode := a.StartupMode(); mode.Mode != ModeOwner {
		t.Fatalf("返送ファイルで %q になった（期待 %q）", mode.Mode, ModeOwner)
	}
	pending := a.PendingOpenFile()
	if pending.Kind != OpenFileImport || pending.FilePath != "/tmp/QS-007-return.rwva" {
		t.Fatalf("取込導線へ渡っていない: %+v", pending)
	}
}

// Windows の関連付け起動（引数でパスが届く）でも同じ区分になること。
func TestPendingOpenFileFallsBackToArgs(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })

	os.Args = []string{"ReqWeave.exe", `C:\tmp\QS-009-return.rwva`}
	got := (&API{}).PendingOpenFile()
	if got.Kind != OpenFileImport {
		t.Fatalf("区分が %q（期待 %q）: %+v", got.Kind, OpenFileImport, got)
	}

	os.Args = []string{"ReqWeave.exe"}
	if none := (&API{}).PendingOpenFile(); none.Kind != "" {
		t.Fatalf("受け取っていないのに %+v", none)
	}
}

// open-file で受け取った内容が起動引数より優先されること
// （起動中のアプリへ別のファイルが届く経路 = macOS）。
func TestOpenFileEventWinsOverArgs(t *testing.T) {
	saved := os.Args
	os.Args = []string{"ReqWeave", "/tmp/QS-001.rwvq"}
	t.Cleanup(func() { os.Args = saved })

	a := &API{}
	a.HandleOpenFile("/tmp/QS-002.rwvq")
	if got := a.StartupMode(); got.FilePath != "/tmp/QS-002.rwvq" {
		t.Fatalf("後から受け取ったファイルが優先されていない: %+v", got)
	}
}

/*
 * プロジェクト（パッケージ）を受け取る経路。
 *
 * macOS では拡張子付きの**ディレクトリ**がダブルクリックで届く。
 * 拡張子だけを見て開こうとすると、開いた先で分かりにくく失敗するため、中身も見る。
 */

func TestClassifyOpenFileAcceptsProjectPackage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "在庫管理システム"+projectstore.ProjectFolderExt)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, projectstore.FileProject),
		[]byte("project_id: P-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ev := classifyOpenFile(dir)
	if ev.Kind != OpenFileProject {
		t.Fatalf("プロジェクトとして扱われない: %+v", ev)
	}
	if ev.FilePath != dir {
		t.Errorf("場所が渡らない: %+v", ev)
	}
}

func TestClassifyOpenFileRejectsLookalikeFolder(t *testing.T) {
	// 名前は合っているが中身がプロジェクトでない。開けない旨を、名前ではなく中身の話として言う。
	dir := filepath.Join(t.TempDir(), "紛らわしい"+projectstore.ProjectFolderExt)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	ev := classifyOpenFile(dir)
	if ev.Kind != OpenFileUnsupported {
		t.Fatalf("中身を見ずに受け入れている: %+v", ev)
	}
	if !strings.Contains(ev.Message, "プロジェクトデータではありません") {
		t.Errorf("理由が中身の話になっていない: %q", ev.Message)
	}
	// 生のパスを画面へ出さない。
	if strings.Contains(ev.Message, dir) {
		t.Errorf("メッセージにパスが入っている: %q", ev.Message)
	}
}
