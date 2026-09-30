package main

// 単体テスト（アイコンの同期検査）。実行: make -C app test-unit
//
// 実リポジトリのアイコンではなく、**一時ディレクトリに作った小さな複製**を対象にする
// （検査ロジックの回帰を、実アイコンの内容から独立に固定するため）。

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePNG は size×size の PNG を書く。
func writePNG(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeICO は指定サイズのディレクトリだけを持つ最小の ICO を書く（画像本体は検査対象外）。
func writeICO(t *testing.T, path string, sizes []int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(sizes)))
	for _, s := range sizes {
		b := byte(s)
		if s == 256 {
			b = 0
		}
		entry := make([]byte, 16)
		entry[0], entry[1] = b, b
		buf.Write(entry)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// newIconTree は整合の取れたアイコン一式（SVG / PNG / ICO / manifest）を作る。
func newIconTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	svg := filepath.Join(root, svgRel)
	if err := os.MkdirAll(filepath.Dir(svg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svg, []byte("<svg/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(root, pngRel), 1024)
	writeICO(t, filepath.Join(root, icoRel), []int{16, 32, 48, 64, 128, 256})
	writeManifest(t, root)
	return root
}

// writeManifest は現在のファイル内容から manifest を書き直す（--build 相当）。
func writeManifest(t *testing.T, root string) {
	t.Helper()
	body := fmt.Sprintf(`{"svg":%q,"png":%q,"ico":%q,"png_size":1024,"ico_sizes":[16,32,48,64,128,256]}`,
		sha(t, filepath.Join(root, svgRel)), sha(t, filepath.Join(root, pngRel)),
		sha(t, filepath.Join(root, icoRel)))
	if err := os.WriteFile(filepath.Join(root, manifestRel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func check(t *testing.T, root string) []string {
	t.Helper()
	problems, err := CheckManifest(root)
	if err != nil {
		t.Fatalf("検査に失敗: %v", err)
	}
	return problems
}

// 整合が取れていれば指摘なし（検査が常に何か言う状態にしない）。
func TestConsistentIconTreeHasNoProblems(t *testing.T) {
	if problems := check(t, newIconTree(t)); len(problems) != 0 {
		t.Fatalf("整合しているのに指摘が出た: %v", problems)
	}
}

// 本検査の主目的: 原本・生成物のどれか 1 つだけを差し替えた状態を検出する。
func TestDetectsEachArtifactEditedAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(t *testing.T, root string)
		want   string
	}{
		"原本 SVG だけを書き換えた": {
			mutate: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, svgRel), []byte("<svg><rect/></svg>\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: svgRel,
		},
		"PNG だけを差し替えた": {
			mutate: func(t *testing.T, root string) { writePNG(t, filepath.Join(root, pngRel), 512) },
			want:   pngRel,
		},
		"ICO だけを差し替えた": {
			mutate: func(t *testing.T, root string) {
				writeICO(t, filepath.Join(root, icoRel), []int{32, 64, 128, 256})
			},
			want: icoRel,
		},
	} {
		root := newIconTree(t)
		tc.mutate(t, root)
		problems := check(t, root)
		if len(problems) == 0 {
			t.Errorf("%s: 検出されなかった", name)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: 対象ファイルが示されていない: %v", name, problems)
		}
	}
}

// PNG の寸法が縮むと配布物のアイコンが荒れるため、ハッシュだけでなく寸法も見る。
func TestDetectsWrongPNGSize(t *testing.T) {
	root := newIconTree(t)
	writePNG(t, filepath.Join(root, pngRel), 512)
	writeManifest(t, root) // ハッシュは合わせたうえで寸法だけが違う状態にする
	problems := check(t, root)
	if len(problems) != 1 || !strings.Contains(problems[0], "512×512") {
		t.Fatalf("寸法の違いが検出されない: %v", problems)
	}
}

// 16px を落とすとタスクバーで潰れるため、サイズ構成も見る。
func TestDetectsMissingICOSize(t *testing.T) {
	root := newIconTree(t)
	writeICO(t, filepath.Join(root, icoRel), []int{32, 48, 64, 128, 256})
	writeManifest(t, root)
	problems := check(t, root)
	if len(problems) != 1 || !strings.Contains(problems[0], "サイズ構成") {
		t.Fatalf("サイズ構成の違いが検出されない: %v", problems)
	}
}

// manifest が無い・壊れている場合は「指摘なし」ではなくエラーにする（黙って緑にしない）。
func TestMissingOrBrokenManifestIsAnError(t *testing.T) {
	root := newIconTree(t)
	if err := os.Remove(filepath.Join(root, manifestRel)); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckManifest(root); err == nil {
		t.Error("manifest が無いのにエラーにならなかった")
	}
	if err := os.WriteFile(filepath.Join(root, manifestRel), []byte("{壊れ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckManifest(root); err == nil {
		t.Error("manifest が壊れているのにエラーにならなかった")
	}
}

// 実リポジトリのアイコン一式が整合していること（検査対象の実在確認を兼ねる）。
func TestRepositoryIconsAreConsistent(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	problems, err := CheckManifest(root)
	if err != nil {
		t.Fatalf("実リポジトリの検査に失敗: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("実リポジトリのアイコンが食い違っている: %v", problems)
	}
}
