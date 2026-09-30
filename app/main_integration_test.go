//go:build integration

// 結合テスト（フロントエンドのビルド成果物 × Go の埋め込み）。
// 単体テストと違いモックを使わず、実際に配布物へ入る frontend/dist の実体を対象にする。
// 実行: make -C app test-integration

package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

const distRoot = "frontend/dist"

// assetRefRe は index.html が参照する資産（src= / href=）を取り出す。
var assetRefRe = regexp.MustCompile(`(?:src|href)\s*=\s*"([^"]+)"`)

// 配布物に入る資産は Go の埋め込み FS 経由でのみ提供される。
// frontend/dist が未ビルド・埋め込みパスの誤りがあればここで落ちる。
func TestEmbeddedAssetsContainBuiltFrontend(t *testing.T) {
	index, err := assets.ReadFile(distRoot + "/index.html")
	if err != nil {
		t.Fatalf("埋め込み資産に %s/index.html がありません: %v（frontend のビルド前か embed パスの誤り）", distRoot, err)
	}
	if len(index) == 0 {
		t.Fatalf("%s/index.html が空です", distRoot)
	}
	if !strings.Contains(string(index), "<div id=\"root\"></div>") {
		t.Errorf("index.html に React のマウント点 <div id=\"root\"></div> がありません:\n%s", index)
	}
}

// index.html が参照する資産がすべて埋め込み FS に存在し、外部ホストを参照しないこと
// （オフラインで全機能が動くように。depcheck の静的検査と対で、
// 実際に埋め込まれたバイト列に対して検証する）。
func TestEmbeddedIndexReferencesOnlyEmbeddedAssets(t *testing.T) {
	index, err := assets.ReadFile(distRoot + "/index.html")
	if err != nil {
		t.Fatalf("埋め込み資産の読み出しに失敗: %v", err)
	}
	refs := assetRefRe.FindAllStringSubmatch(string(index), -1)
	if len(refs) == 0 {
		t.Fatalf("index.html に資産参照（src= / href=）が 1 件もありません:\n%s", index)
	}
	for _, m := range refs {
		ref := m[1]
		switch {
		case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"), strings.HasPrefix(ref, "//"):
			t.Errorf("配布物が外部ホストを参照しています: %q", ref)
			continue
		case strings.HasPrefix(ref, "data:"):
			continue
		}
		p := distRoot + "/" + strings.TrimPrefix(ref, "/")
		if _, err := fs.Stat(assets, p); err != nil {
			t.Errorf("index.html が参照する %q が埋め込み FS にありません（解決先 %s）: %v", ref, p, err)
		}
	}
}
