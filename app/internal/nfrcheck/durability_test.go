package nfrcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// クラッシュ後の復元と、保存処理の原子性の**構造的な担保**。
//
// 「電源断でも直前の発話まで残る」ことの本来の確認は電源断そのものを起こすしかなく、
// 自動テストでは再現できない（同一プロセスから読み直す限り、OS のページキャッシュが
// 応えてしまうため、fsync を外しても振る舞いが変わらない。fsync を外して実際に確かめた）。
//
// そこで**書き込み経路が同期を呼んでいること自体**を固定する。振る舞いの検証ではないが、
// 「うっかり外した」変更は捕まえられる。実機での確認は macOS・Windows の実機検証で行う。
func TestPersistencePathsCallSync(t *testing.T) {
	root := repoAppRoot(t)
	cases := map[string]struct {
		file string
		fn   string
	}{
		"発話の追記（発話を 1 件ごとに保存する）": {
			file: filepath.Join("internal", "projectstore", "session.go"), fn: "AppendUtterance"},
		"原子的置換（途中で落ちても前の版か新しい版のどちらかが残る）": {
			file: filepath.Join("internal", "projectstore", "atomic.go"), fn: "WriteFileAtomic"},
	}
	for name, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.file))
		if err != nil {
			t.Errorf("%s: %s を読めない: %v", name, c.file, err)
			continue
		}
		body, ok := functionBody(string(b), c.fn)
		if !ok {
			t.Errorf("%s: %s に関数 %s が無い", name, c.file, c.fn)
			continue
		}
		if !strings.Contains(body, ".Sync()") {
			t.Errorf("%s: %s が同期（Sync）を呼んでいない。"+
				"強制終了で直前の保存が失われうる", name, c.fn)
		}
	}
}

// functionBody は関数宣言から次の行頭 "}" までを返す（波括弧の対応は追わない粗い切り出し）。
func functionBody(source, name string) (string, bool) {
	marker := ") " + name + "("
	idx := strings.Index(source, marker)
	if idx < 0 {
		// レシーバの無い関数
		marker = "func " + name + "("
		idx = strings.Index(source, marker)
		if idx < 0 {
			return "", false
		}
	}
	rest := source[idx:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		return rest, true
	}
	return rest[:end], true
}
