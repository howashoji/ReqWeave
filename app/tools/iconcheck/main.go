// iconcheck はアプリアイコンの原本（SVG）と生成物（PNG / ICO）の同期を検査する。
// `make lint` から実行され、食い違いがあれば非ゼロ終了する。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "iconcheck: 対象ディレクトリを解決できません: %v\n", err)
		os.Exit(2)
	}

	problems, err := CheckManifest(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "iconcheck: 検査に失敗しました: %v\n%s\n", err, regenHint)
		os.Exit(2)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "✘ アイコンの原本と生成物が食い違っています %d 件:\n",
			len(problems))
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintf(os.Stderr, "  %s\n", regenHint)
		os.Exit(1)
	}
	fmt.Println("iconcheck: 原本と生成物のハッシュ・寸法・サイズ構成が一致しています")

	// 再描画の突き合わせは道具（librsvg / Pillow）がある環境でだけ行う。
	// 無い環境では未実施であることを明示する（黙って緑にしない）。
	if out, ok := runRenderCheck(abs); !ok {
		fmt.Println("iconcheck: 再描画の突き合わせは未実施（librsvg / Pillow が無い環境。" +
			"アイコンを変更したら道具のある環境で python3 app/build/icon/gen_appicon.py --check を通すこと）")
	} else {
		fmt.Print(out)
	}
}

// runRenderCheck は gen_appicon.py --check を実行する。
// 道具が無くて実行できない場合は ok=false を返す（失敗ではない）。実行できて不一致なら終了する。
func runRenderCheck(root string) (string, bool) {
	if _, err := exec.LookPath("rsvg-convert"); err != nil {
		return "", false
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return "", false
	}
	script := filepath.Join(root, "build", "icon", "gen_appicon.py")
	if _, err := os.Stat(script); err != nil {
		return "", false
	}
	// Pillow の有無を先に確かめる（無いだけの失敗を「不一致」と誤報しない）
	if err := exec.Command(python, "-c", "import PIL").Run(); err != nil {
		return "", false
	}
	out, err := exec.Command(python, script, "--check").CombinedOutput()
	if err != nil {
		fmt.Fprint(os.Stderr, string(out))
		fmt.Fprintf(os.Stderr, "iconcheck: 再描画の突き合わせで不一致を検出しました\n")
		os.Exit(1)
	}
	return string(out), true
}
