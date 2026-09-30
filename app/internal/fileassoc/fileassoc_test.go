package fileassoc

// 単体テスト（レジストリを触らない純関数の検査）。実行: make -C app test-unit
//
// 実書き込みは Windows でのみ動くため、ここでは「何を書くつもりか」を固定する。
// 実機での関連付けの成立は Windows の実機検証で確認する。

import (
	"strings"
	"testing"
)

// 受け入れ条件: Windows の関連付け登録が管理者権限を要さないこと。
// = 書き込み先が HKCU（利用者領域）に限られ、HKLM を含まないこと。
func TestWindowsEntriesStayInCurrentUserHive(t *testing.T) {
	entries := windowsEntries(`C:\Users\taro\Programs\ReqWeave\ReqWeave.exe`)
	if len(entries) == 0 {
		t.Fatal("登録項目が空")
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, `Software\Classes\`) {
			t.Errorf("HKCU 配下でないキー: %q", e.Key)
		}
		if strings.Contains(strings.ToUpper(e.Key), "HKEY_LOCAL_MACHINE") || strings.Contains(strings.ToUpper(e.Key), "HKLM") {
			t.Errorf("全ユーザー領域（管理者権限が要る）を指すキー: %q", e.Key)
		}
	}
}

// 受け入れ条件: .rwvq / .rwva の両方が登録され、別々の ProgID に割り当たること
// （発行用と返送用を別扱いにするため）。
func TestWindowsEntriesCoverBothExtensions(t *testing.T) {
	entries := windowsEntries(`C:\app\ReqWeave.exe`)
	progIDOf := map[string]string{}
	for _, e := range entries {
		switch e.Key {
		case `Software\Classes\.rwvq`:
			progIDOf[".rwvq"] = e.Value
		case `Software\Classes\.rwva`:
			progIDOf[".rwva"] = e.Value
		}
	}
	if progIDOf[".rwvq"] != ProgIDQuestionnaire {
		t.Errorf(".rwvq の ProgID が %q（期待 %q）", progIDOf[".rwvq"], ProgIDQuestionnaire)
	}
	if progIDOf[".rwva"] != ProgIDReturn {
		t.Errorf(".rwva の ProgID が %q（期待 %q）", progIDOf[".rwva"], ProgIDReturn)
	}
	if progIDOf[".rwvq"] == progIDOf[".rwva"] {
		t.Error("発行用と返送用が同じ ProgID になっている")
	}
}

// 起動コマンドは「実行ファイル」と「開くファイル」の双方を引用符で囲む。
// 空白を含むパス（Windows では普通にある）で壊れないこと。
func TestOpenCommandQuotesPathAndArgument(t *testing.T) {
	exe := `C:\Users\山田 太郎\Programs\ReqWeave\ReqWeave.exe`
	var commands []string
	for _, e := range windowsEntries(exe) {
		if strings.HasSuffix(e.Key, `\shell\open\command`) {
			commands = append(commands, e.Value)
		}
	}
	if len(commands) != len(associations) {
		t.Fatalf("起動コマンドが %d 件（期待 %d 件）", len(commands), len(associations))
	}
	for _, cmd := range commands {
		if !strings.HasPrefix(cmd, `"`+exe+`"`) {
			t.Errorf("実行ファイルが引用符で囲まれていない: %q", cmd)
		}
		if !strings.HasSuffix(cmd, `"%1"`) {
			t.Errorf("開くファイルの引数（\"%%1\"）が無い、または引用符で囲まれていない: %q", cmd)
		}
	}
}

// 既定アイコンは実行ファイルの 0 番の資源を指す（拡張子ごとの独自アイコンは持たない）。
func TestDefaultIconPointsAtExecutable(t *testing.T) {
	exe := `C:\app\ReqWeave.exe`
	found := 0
	for _, e := range windowsEntries(exe) {
		if strings.HasSuffix(e.Key, `\DefaultIcon`) {
			found++
			if e.Value != `"`+exe+`",0` {
				t.Errorf("既定アイコンの指定が %q", e.Value)
			}
		}
	}
	if found != len(associations) {
		t.Errorf("既定アイコンの項目が %d 件（期待 %d 件）", found, len(associations))
	}
}
