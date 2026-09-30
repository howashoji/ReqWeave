package updater

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

/*
 * dmg からの取り出し。
 *
 * macOS の配布物は dmg、Windows は zip。**資産の形式は拡張子で決める**
 * （マニフェストの `url` の末尾）。形式を取り違えたときは黙って失敗せず、
 * 利用者向けのエラー（原因＋次に取る行動の 1 文）へ倒す。
 *
 * 取り出しに `ditto` を使う理由: `.app` バンドルには実行権限・拡張属性・
 * **コード署名**が付いており、素朴なファイルコピーでは壊れる（壊れると Gatekeeper に拒否される）。
 * `ditto` はこれらを保ったまま複製する。
 */

// extractArchive は配布物を dest 配下へ取り出す。
//
// **形式は中身の先頭で判定する。拡張子で判定しない。**
// 取得した配布物は `download-*.part` という名前で保存される（Download を参照）ため、
// 拡張子を見ると macOS の dmg を zip として開こうとして必ず失敗する。
func extractArchive(archivePath, dest string) error {
	zipped, err := looksLikeZip(archivePath)
	if err != nil {
		return err
	}
	if zipped {
		return unzip(archivePath, dest)
	}
	return extractDMG(archivePath, dest)
}

// looksLikeZip は先頭が zip の署名（`PK`）かどうかを返す。
func looksLikeZip(archivePath string) (bool, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return false, &Error{Kind: KindMalformed,
			msg: "更新ファイルを開けないため、現行版のまま更新を中止しました。配布元の案内を確認してください", cause: err}
	}
	defer f.Close()
	head := make([]byte, 2)
	n, rerr := io.ReadFull(f, head)
	if rerr != nil && n < 2 {
		return false, &Error{Kind: KindMalformed,
			msg:   "更新ファイルの中身が想定と違うため、現行版のまま更新を中止しました。配布元の案内を確認してください",
			cause: fmt.Errorf("archive too short: %w", rerr)}
	}
	return head[0] == 'P' && head[1] == 'K', nil
}

// extractDMG は dmg を読み取り専用でマウントし、アプリ本体を dest へ取り出して切り離す。
func extractDMG(archivePath, dest string) (err error) {
	if runtime.GOOS != "darwin" {
		return &Error{Kind: KindMalformed,
			msg:   "更新ファイルの形式がこの OS 向けではないため、現行版のまま更新を中止しました。配布元の案内を確認してください",
			cause: fmt.Errorf("dmg is macOS only, running on %s", runtime.GOOS)}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return &Error{Kind: KindNotUserArea,
			msg: "更新の作業場所を用意できませんでした。空き容量を確認して再実行してください", cause: err}
	}

	// マウント先は一時ディレクトリを明示する。`/Volumes` 任せにすると同名ボリュームがあるとき
	// 別の場所（`ReqWeave 1` 等）へ付き、どこを読んでいるかが曖昧になる。
	mountPoint, err := os.MkdirTemp("", "reqweave-update-dmg-")
	if err != nil {
		return &Error{Kind: KindNotUserArea,
			msg: "更新の作業場所を用意できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	defer os.RemoveAll(mountPoint)

	// **1 回で諦めない**: 直前の操作の直後は「リソースが使用中です」で失敗しうる。
	if aerr := attachRetry(archivePath, mountPoint); aerr != nil {
		return &Error{Kind: KindMalformed,
			msg:   "更新ファイルを開けないため、現行版のまま更新を中止しました。配布元の案内を確認してください",
			cause: aerr}
	}
	defer func() {
		// 切り離しに失敗したらマウントが残る。取り出し自体が成功していても失敗として扱う
		// （残ったマウントは次回の更新で別の場所を読む原因になる）。
		//
		// **1 回で諦めない**（実測）: 直後は Spotlight のインデックス等でボリュームが
		// 使用中になり `hdiutil detach` が exit 16（Resource busy）で落ちることがある。
		// ここで失敗にすると、**取り出しは終わっているのに更新が失敗扱いになる**。
		if derr := detachRetry(mountPoint); derr != nil {
			if err == nil {
				err = &Error{Kind: KindNotUserArea,
					msg:   "更新の後片づけに失敗しました。アプリを再起動してからもう一度実行してください",
					cause: derr}
			}
		}
	}()

	src, err := appEntry(mountPoint)
	if err != nil {
		return err
	}
	// 署名・実行権限・拡張属性を保ったまま複製する（素朴なコピーでは署名が壊れる）。
	target := filepath.Join(dest, filepath.Base(src))
	if out, cerr := exec.Command("ditto", src, target).CombinedOutput(); cerr != nil {
		return &Error{Kind: KindNotUserArea,
			msg:   "更新ファイルを展開できませんでした。空き容量を確認して再実行してください",
			cause: fmt.Errorf("ditto: %w: %s", cerr, strings.TrimSpace(string(out)))}
	}
	return nil
}

// detachRetry はマウントを切り離す。間隔をあけて数回試し、最後は -force で外す（使用中で一時的に失敗するため）。
func detachRetry(mountPoint string) error {
	var last error
	for i := 0; i < detachAttempts; i++ {
		// **`-quiet` を付けない**。付けると hdiutil は失敗の理由すら出さず、
		// `Error.Detail()` が終了コードだけになって原因を追えない（実測: 2026-09-08 の検証ゲート）。
		args := []string{"detach", mountPoint}
		if i == detachAttempts-1 {
			// 最後の 1 回は強制。残すより外すを優先する（残ると次回の更新が別の場所を読む）。
			args = append(args, "-force")
		}
		out, err := exec.Command("hdiutil", args...).CombinedOutput()
		if err == nil {
			return nil
		}
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = "（hdiutil は理由を出力しなかった）"
		}
		last = fmt.Errorf("hdiutil detach: %w: %s", err, detail)
		time.Sleep(detachInterval)
	}
	return last
}

// テストから短縮できるよう var にしている（既定値は変えない）。
var (
	detachAttempts = 5
	detachInterval = 2 * time.Second
)

// attachRetry は dmg を読み取り専用でマウントする。間隔をあけて数回試す（使用中で一時的に失敗するため）。
func attachRetry(archivePath, mountPoint string) error {
	var last error
	for i := 0; i < detachAttempts; i++ {
		out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noverify",
			"-mountpoint", mountPoint, archivePath).CombinedOutput()
		if err == nil {
			return nil
		}
		adetail := strings.TrimSpace(string(out))
		if adetail == "" {
			adetail = "（hdiutil は理由を出力しなかった）"
		}
		last = fmt.Errorf("hdiutil attach: %w: %s", err, adetail)
		time.Sleep(detachInterval)
	}
	return last
}
