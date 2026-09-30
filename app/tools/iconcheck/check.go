package main

// アイコンの原本（SVG）と生成物（PNG / ICO）の同期検査。
//
// 原本から生成物を作り直すには librsvg と Pillow が要るが、**それらを検証ゲートの必須依存にしない**
// （配布物・アプリのビルドには不要な依存であり、道具の無い環境の lint を壊す）。
// そこで検査を 2 段に分ける:
//
//   1. manifest 照合（本ファイル。Go の標準ライブラリのみ）— 常に実行する。
//      原本・PNG・ICO のハッシュと、PNG の寸法・ICO のサイズ構成を突き合わせ、
//      「どれか 1 つだけを差し替えた」状態を検出する。
//   2. 再描画の突き合わせ（gen_appicon.py --check）— 道具がある環境でだけ実行する。
//      無い環境では「未実施」と明示する（黙って緑にしない）。

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
)

// Manifest は gen_appicon.py --build が書き出す記録（icon.manifest.json）。
type Manifest struct {
	SVG      string `json:"svg"`
	PNG      string `json:"png"`
	ICO      string `json:"ico"`
	PNGSize  int    `json:"png_size"`
	ICOSizes []int  `json:"ico_sizes"`
}

// 対象ファイル（app/ からの相対パス）。
const (
	svgRel      = "build/icon/appicon.svg"
	pngRel      = "build/appicon.png"
	icoRel      = "build/windows/icon.ico"
	manifestRel = "build/icon/icon.manifest.json"
)

// regenHint は失敗時に示す次の行動（原因＋次の行動の 1 文にする）。
const regenHint = "→ python3 app/build/icon/gen_appicon.py --build で作り直してください。"

// CheckManifest は manifest と実ファイルの食い違いを返す（空なら一致）。
func CheckManifest(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, manifestRel))
	if err != nil {
		return nil, fmt.Errorf("%s を読み込めません: %w", manifestRel, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s を解釈できません: %w", manifestRel, err)
	}

	var problems []string
	for _, f := range []struct {
		rel  string
		want string
	}{{svgRel, m.SVG}, {pngRel, m.PNG}, {icoRel, m.ICO}} {
		if want := f.want; want == "" {
			problems = append(problems, fmt.Sprintf("%s のハッシュが manifest にありません", f.rel))
			continue
		}
		got, err := sha256File(filepath.Join(root, f.rel))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s を読み込めません: %v", f.rel, err))
			continue
		}
		if got != f.want {
			problems = append(problems, fmt.Sprintf(
				"%s が原本と対応していません（manifest 記録 %s… / 実ファイル %s…）",
				f.rel, f.want[:8], got[:8]))
		}
	}

	// PNG の寸法（Wails が .icns を作る元。小さすぎると配布物のアイコンが荒れる）
	if size, err := pngSize(filepath.Join(root, pngRel)); err != nil {
		problems = append(problems, fmt.Sprintf("%s の寸法を読めません: %v", pngRel, err))
	} else if m.PNGSize > 0 && (size[0] != m.PNGSize || size[1] != m.PNGSize) {
		problems = append(problems, fmt.Sprintf("%s の寸法が %d×%d です（期待 %d×%d）",
			pngRel, size[0], size[1], m.PNGSize, m.PNGSize))
	}

	// ICO のサイズ構成（16px まで持たないとタスクバーで潰れる）
	if sizes, err := icoSizes(filepath.Join(root, icoRel)); err != nil {
		problems = append(problems, fmt.Sprintf("%s を解釈できません: %v", icoRel, err))
	} else if len(m.ICOSizes) > 0 && !sameInts(sizes, m.ICOSizes) {
		problems = append(problems, fmt.Sprintf("%s のサイズ構成が %v です（期待 %v）",
			icoRel, sizes, m.ICOSizes))
	}
	return problems, nil
}

func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func pngSize(path string) ([2]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return [2]int{}, err
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return [2]int{}, err
	}
	return [2]int{cfg.Width, cfg.Height}, nil
}

// icoSizes は ICO のディレクトリを読み、含まれる画像の幅を昇順で返す。
// 形式: 6 バイトのヘッダ（予約 0 / 種別 1 / 画像数）+ 画像数 × 16 バイトのエントリ。
// エントリ先頭の幅・高さは 1 バイトで、0 は 256 を意味する。
func icoSizes(path string) ([]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 6 {
		return nil, fmt.Errorf("ファイルが短すぎます（%d バイト）", len(b))
	}
	if binary.LittleEndian.Uint16(b[0:2]) != 0 || binary.LittleEndian.Uint16(b[2:4]) != 1 {
		return nil, fmt.Errorf("ICO のヘッダではありません")
	}
	count := int(binary.LittleEndian.Uint16(b[4:6]))
	if count == 0 {
		return nil, fmt.Errorf("画像が 1 つも入っていません")
	}
	if len(b) < 6+count*16 {
		return nil, fmt.Errorf("ディレクトリが欠けています（画像数 %d）", count)
	}
	sizes := make([]int, 0, count)
	for i := 0; i < count; i++ {
		w := int(b[6+i*16])
		if w == 0 {
			w = 256
		}
		sizes = append(sizes, w)
	}
	sort.Ints(sizes)
	return sizes, nil
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]int(nil), a...), append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
