package binding

// 単体テスト（ファイル I/O なし）。実行: make -C app test-unit

import (
	"os"
	"testing"
)

// 起動経路で動作モードが決まる（受け渡しファイルを開く = 回答モード）。
func TestStartupMode(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantMode string
		wantFile string
	}{
		{"通常起動", []string{"reqweave"}, ModeOwner, ""},
		{"発行用ファイルを開く", []string{"reqweave", "/tmp/QS-001.rwvq"}, ModeRespondent, "/tmp/QS-001.rwvq"},
		{"拡張子が大文字", []string{"reqweave", "/tmp/QS-001.RWVQ"}, ModeRespondent, "/tmp/QS-001.RWVQ"},
		// 返送ファイルは担当者側の取込対象であり、回答モードでは開かない。
		{"返送ファイル", []string{"reqweave", "/tmp/QS-001-return.rwva"}, ModeOwner, ""},
		{"関係ない引数", []string{"reqweave", "--debug"}, ModeOwner, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saved := os.Args
			os.Args = tc.args
			t.Cleanup(func() { os.Args = saved })

			got := (&API{}).StartupMode()
			if got.Mode != tc.wantMode {
				t.Fatalf("モードが %q です（期待 %q）", got.Mode, tc.wantMode)
			}
			if got.FilePath != tc.wantFile {
				t.Fatalf("ファイルパスが %q です（期待 %q）", got.FilePath, tc.wantFile)
			}
			if tc.wantMode == ModeRespondent && got.FileName == "" {
				t.Fatalf("表示用のファイル名が空です: %+v", got)
			}
		})
	}
}
