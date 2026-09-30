//go:build integration

package codex

// シークレットキー方式で、Codex が作るファイルにキーが残らないことの実測。
//
// 「Codex のファイルに認証情報が残らない」（シークレットキー方式）を、
// **ダミーのキー**で自動化した範囲で確かめる。見るのは次の 2 時点:
//
//   - 本システムの終了前（一時領域が残っている間）… 一時領域配下の全ファイルを平文・Base64 で探す
//   - 終了後 … 一時領域がフォルダごと消え、その親にも残りが無いこと
//
// **自動化の範囲**: 一時領域と利用者の `~/.codex`（Codex の既定の設定場所）まで。
// 端末の全ファイルの検索は手作業の検証で行う。
// キーの値は出力に書かない（見つかった場合もファイル名と件数だけを示す）。

import (
	"bytes"
	"context"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/howashoji/ReqWeave/app/internal/aiprovider"
)

func TestRealCodexLeavesNoSecretKeyInFiles(t *testing.T) {
	env := newRealCodexEnv(t)
	adapter := NewWithOptions(stubKeys{key: dummyKey}, "codex/結合テスト", aiprovider.AdapterOptions{})

	ch, err := adapter.StreamMessage(context.Background(), realCodexRequest())
	if err != nil {
		t.Fatalf("送信を開始できない: %v", err)
	}
	if last := lastEvent(t, collect(t, ch)); last.Kind != aiprovider.EventDone {
		t.Fatalf("正常終了していない: %+v（エラー: %+v）", last, last.Err)
	}

	root := filepath.Join(env.base, "codex")
	// 前提の確認: Codex が一時領域に実際にファイルを作っている（空を探して 0 件にしない）。
	if n := countFiles(t, root); n == 0 {
		t.Fatal("一時領域にファイルが 1 件も無い（前提が崩れている）")
	}

	// 終了前（一時領域が残っている間）
	if hits := filesContainingKey(t, root); len(hits) != 0 {
		t.Errorf("一時領域のファイルにキーが残っている: %v", hits)
	}
	home := os.Getenv("HOME")
	if home != "" {
		if hits := filesContainingKey(t, filepath.Join(home, ".codex")); len(hits) != 0 {
			t.Errorf("利用者の ~/.codex にキーが残っている: %v", hits)
		}
	}

	// 終了後
	if err := procManager.stopCurrent("テストの確認"); err != nil {
		t.Fatalf("子プロセスを止められない: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("一時領域が残っている: %v", err)
	}
	if hits := filesContainingKey(t, env.base); len(hits) != 0 {
		t.Errorf("終了後にキーを含むファイルが残っている: %v", hits)
	}
	if home != "" {
		if hits := filesContainingKey(t, filepath.Join(home, ".codex")); len(hits) != 0 {
			t.Errorf("終了後に利用者の ~/.codex にキーが残っている: %v", hits)
		}
	}
}

// filesContainingKey は配下のファイルからキーの平文・Base64 を探し、**見つかったファイルの名前だけ**を返す。
func filesContainingKey(t *testing.T, root string) []string {
	t.Helper()
	needles := keyNeedles(dummyKey)
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil // 読めないものは飛ばす（実行中に消えるファイルがある）
		}
		for _, needle := range needles {
			if bytes.Contains(body, needle) {
				hits = append(hits, filepath.Base(path))
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ファイルを走査できない（%s）: %v", root, err)
	}
	return hits
}

// keyNeedles はキーの平文と、Base64（標準・URL 用）の**位置に依らない**断片を返す。
//
// Base64 は前に何バイトあるか（3 で割った余り）で並びが変わるため、ずれ 0・1・2 の 3 通りを作り、
// 前後の文字（前のバイト・後のバイトと混ざる部分）を落とした中央だけを探す。
func keyNeedles(key string) [][]byte {
	needles := [][]byte{[]byte(key)}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		for shift := 0; shift < 3; shift++ {
			encoded := strings.TrimRight(enc.EncodeToString([]byte(strings.Repeat("\x00", shift)+key)), "=")
			if len(encoded) <= 8 {
				continue
			}
			needles = append(needles, []byte(encoded[4:len(encoded)-4]))
		}
	}
	return needles
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}
