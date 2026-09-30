//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

/*
 * マウントの残骸を持ち越さない。
 *
 * 2026-09-08 の検証ゲートの偽赤は、**切り離しに失敗したマウントが残り、
 * それが次の実行を巻き込む**（同じ dmg が二重にマウントされる）ことで自己増殖していた。
 * 実測では `hdiutil create` が内部で行う切り離しの取りこぼしと、
 * 検査側の切り離し失敗の 2 系統の残骸が居座っていた。
 *
 * テストが作った dmg のマウントは、テストが責任を持って外す。
 * **黙って消さない**: 何を外したかは必ず出力する（消えたこと自体が診断の材料になる）。
 */

// sweepMountLeak は imagePath のマウントが残っていれば切り離す。
// 残っていなければ何もしない（無音）。
func sweepMountLeak(t *testing.T, imagePath string) {
	t.Helper()
	for _, m := range mountsOfImagePath(t, imagePath) {
		desc := fmt.Sprintf("%s（%s）", strings.Join(m.devices, " "), m.imagePath)
		var failed []string
		for _, dev := range m.devices {
			if out, err := exec.Command("hdiutil", "detach", dev, "-force").CombinedOutput(); err != nil {
				// 親 device を外すと子は一緒に消えるため、消えていれば成功とみなす。
				if len(mountsOfImagePath(t, imagePath)) == 0 {
					failed = nil
					break
				}
				failed = append(failed, fmt.Sprintf("%s: %v: %s", dev, err, strings.TrimSpace(string(out))))
			}
		}
		report := "マウントの残骸を切り離しました: " + desc
		if len(failed) > 0 {
			report = "マウントの残骸を切り離せませんでした: " + desc + ": " + strings.Join(failed, " / ")
		}
		t.Logf("%s", report)
		fmt.Fprintf(os.Stderr, "mount-leak: %s\n", report)
	}
}

// mountedImageInfo は現在マウントされているディスクイメージ 1 件。
type mountedImageInfo struct {
	imagePath string
	devices   []string
}

// mountsOfImagePath は imagePath に対応する現在のマウントを返す。
//
// 判定は実体（inode）で行い、実体が既に消えている残骸はパスで拾う
// （`hdiutil create` の中間ファイルは作った後に名前を変えるため、実体で引けない）。
// plist は `plutil` で JSON へ変換して読む（macOS 標準。外部依存を増やさない）。
func mountsOfImagePath(t *testing.T, imagePath string) []mountedImageInfo {
	t.Helper()
	plist, err := exec.Command("hdiutil", "info", "-plist").Output()
	if err != nil {
		t.Fatalf("マウント一覧を読めません（hdiutil info）: %v", err)
	}
	conv := exec.Command("plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = bytes.NewReader(plist)
	raw, err := conv.Output()
	if err != nil {
		t.Fatalf("マウント一覧を読めません（plutil）: %v", err)
	}
	var doc struct {
		Images []struct {
			ImagePath string `json:"image-path"`
			Entities  []struct {
				DevEntry string `json:"dev-entry"`
			} `json:"system-entities"`
		} `json:"images"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("マウント一覧を解釈できません: %v", err)
	}
	want, statErr := os.Stat(imagePath)
	abs, aerr := filepath.Abs(imagePath)
	if aerr != nil {
		abs = imagePath
	}
	var hit []mountedImageInfo
	for _, img := range doc.Images {
		if img.ImagePath == "" {
			continue
		}
		same := false
		if statErr == nil {
			if got, gerr := os.Stat(img.ImagePath); gerr == nil && os.SameFile(want, got) {
				same = true
			}
		}
		if !same && filepath.Clean(img.ImagePath) != filepath.Clean(abs) {
			continue
		}
		m := mountedImageInfo{imagePath: img.ImagePath}
		for _, e := range img.Entities {
			if e.DevEntry != "" {
				m.devices = append(m.devices, e.DevEntry)
			}
		}
		hit = append(hit, m)
	}
	return hit
}
