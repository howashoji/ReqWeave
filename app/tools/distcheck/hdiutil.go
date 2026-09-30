package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// hdiutil の呼び出しと、マウントの残骸の後始末。
//
// **`hdiutil` の失敗に `-quiet` で蓋をしない**。2026-09-08 の検証ゲートの偽赤では
// `detach` に `-quiet` が付いていたため、失敗メッセージが
// 「配布物を切り離せません（…）: 」で終端し、**hdiutil が何と言って落ちたかが残らなかった**
// （実測: `-quiet` は stderr のメッセージごと消す。exit code だけが残る）。
// 診断の材料が無いまま再試行で隠すと、本当の異常まで隠れる。
//
// あわせて、**切り離しに失敗したときにマウントを残さない**。残ったマウントは
// 次回の実行を巻き込み（同じ dmg が二重にマウントされる）、失敗が自己増殖する。

// hdiutilRun は hdiutil を実行する。失敗したときは
// 「実行した引数・終了コード・実出力」を必ず含む error を返す
// （出力が空でもその旨を書く＝**空の詳細を作らない**）。
func hdiutilRun(args ...string) ([]byte, error) {
	out, err := exec.Command("hdiutil", args...).CombinedOutput()
	if err == nil {
		return out, nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = "（hdiutil は理由を出力しなかった）"
	}
	return out, fmt.Errorf("hdiutil %s: %w: %s", strings.Join(args, " "), err, detail)
}

// mountedImage は現在マウントされているディスクイメージ 1 件。
type mountedImage struct {
	ImagePath   string   // マウント元（dmg）のパス。実体が既に消えていることもある
	Devices     []string // /dev/diskN。切り離しはここへ指定するのが確実
	MountPoints []string // マウント先（付いていない device もある）
}

// mountedImages は `hdiutil info` から現在のマウント一覧を読む。
//
// plist を JSON へ変換してから読む（`plutil` は macOS の標準コマンド。
// plist パーサを外部依存として増やさない）。テキスト出力の見た目に依存しない。
func mountedImages() ([]mountedImage, error) {
	plist, err := hdiutilRun("info", "-plist")
	if err != nil {
		return nil, err
	}
	conv := exec.Command("plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = bytes.NewReader(plist)
	var stderr bytes.Buffer
	conv.Stderr = &stderr
	raw, err := conv.Output()
	if err != nil {
		return nil, fmt.Errorf("マウント一覧を読めません（plutil）: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var doc struct {
		Images []struct {
			ImagePath string `json:"image-path"`
			Entities  []struct {
				DevEntry   string `json:"dev-entry"`
				MountPoint string `json:"mount-point"`
			} `json:"system-entities"`
		} `json:"images"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("マウント一覧を解釈できません: %w", err)
	}
	images := make([]mountedImage, 0, len(doc.Images))
	for _, img := range doc.Images {
		m := mountedImage{ImagePath: img.ImagePath}
		for _, e := range img.Entities {
			if e.DevEntry != "" {
				m.Devices = append(m.Devices, e.DevEntry)
			}
			if e.MountPoint != "" {
				m.MountPoints = append(m.MountPoints, e.MountPoint)
			}
		}
		images = append(images, m)
	}
	return images, nil
}

// mountsOfImage は指定の dmg が今マウントされているものだけを返す。
//
// 判定は**実体（inode）**で行う。パス文字列だけで見ると、
// 作った後に名前を変えた場合やシンボリックリンク経由の場合に取りこぼす。
// 実体が既に消えている残骸（一時ディレクトリごと消えた場合）はパスで拾う。
func mountsOfImage(dmgPath string) ([]mountedImage, error) {
	all, err := mountedImages()
	if err != nil {
		return nil, err
	}
	want, statErr := os.Stat(dmgPath)
	abs, aerr := filepath.Abs(dmgPath)
	if aerr != nil {
		abs = dmgPath
	}
	var hit []mountedImage
	for _, m := range all {
		if m.ImagePath == "" {
			continue
		}
		if statErr == nil {
			if got, err := os.Stat(m.ImagePath); err == nil && os.SameFile(want, got) {
				hit = append(hit, m)
				continue
			}
		}
		if filepath.Clean(m.ImagePath) == filepath.Clean(abs) {
			hit = append(hit, m)
		}
	}
	return hit, nil
}

// マウントが消えるまで待つ上限と間隔。
//
// `hdiutil detach` が成功で返っても、`hdiutil info` からその image の device が
// 消えるまでには**わずかな遅れがある**（切り離しの完了は detach の終了と同期しない）。
// 直後に一覧を見ると「マウント先の無い device が残っている」状態が見えることがある。
// 上限を超えて残っていれば本当の取り残しなので、待つのはここまでにする。
var (
	mountDrainTimeout  = 10 * time.Second
	mountDrainInterval = 200 * time.Millisecond
)

// waitMountsDrained は dmg のマウントが無くなるまで待ち、最後に見えた一覧を返す。
//
// 空のスライスが返れば消えている。**上限を過ぎても残っていればそのまま返す**
// （待てば必ず消えると仮定しない = 本当の取り残しを緑にしない）。
func waitMountsDrained(dmgPath string) ([]mountedImage, error) {
	deadline := time.Now().Add(mountDrainTimeout)
	for {
		left, err := mountsOfImage(dmgPath)
		if err != nil || len(left) == 0 || !time.Now().Before(deadline) {
			return left, err
		}
		time.Sleep(mountDrainInterval)
	}
}

// detachDevices は device 指定でマウントを強制的に外す。
//
// マウント先のパスからは外せない状態（マウント先のディレクトリが既に消えている等）でも、
// device が分かっていれば外せる。**残すより外すを優先する**（残骸が次回を巻き込むため）。
func detachDevices(m mountedImage) error {
	var errs []string
	for _, dev := range m.Devices {
		// 子 device（/dev/disk20s1 等）は親を外せば一緒に外れる。親から順に試し、
		// 既に外れているものの「見つからない」は成功として扱う。
		if _, err := hdiutilRun("detach", dev, "-force"); err != nil {
			if again, ferr := mountsOfImage(m.ImagePath); ferr == nil && len(again) == 0 {
				return nil // 実際には外れている（親を外した副作用で子が消えた）
			}
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(errs, " / "))
}

// sweepStaleMounts は「これから使う dmg が既にマウントされている」残骸を切り離す。
//
// **黙って消さない**。何を切り離したか（device とマウント元）を log へ必ず残す。
// 残骸が無ければ何もしない（無音）。
func sweepStaleMounts(dmgPath string, log func(string)) {
	stale, err := mountsOfImage(dmgPath)
	if err != nil {
		log(fmt.Sprintf("前回のマウント残骸を確認できませんでした（検査は続行）: %v", err))
		return
	}
	for _, m := range stale {
		desc := fmt.Sprintf("%s（%s）", strings.Join(m.Devices, " "), m.ImagePath)
		if derr := detachDevices(m); derr != nil {
			log(fmt.Sprintf("前回のマウント残骸を切り離せませんでした: %s: %v", desc, derr))
			continue
		}
		log(fmt.Sprintf("前回のマウント残骸を切り離しました: %s", desc))
	}
}
