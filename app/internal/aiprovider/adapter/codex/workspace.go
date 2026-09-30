package codex

// 本ファイルは Codex の一時領域。
//
//	<キャッシュ領域>/net.howashoji.reqweave/codex/       … 一時領域（子プロセスの終了時に**フォルダごと**消す）
//	                                        home/        … CODEX_HOME と CODEX_SQLITE_HOME
//	                                        work/        … スレッドの作業フォルダ（空のまま）
//	                                        model_catalog.json … 手元のモデル定義（catalog.go）
//	<キャッシュ領域>/net.howashoji.reqweave/codex.lock   … 同一端末の多重プロセスの排他
//
// **場所は固定する**（毎回同じパス）。keyring に置かれるサインインの項目の名前が CODEX_HOME の
// 場所で決まるため、場所を変えるとサインインが毎回失われる（実測）。
// フォルダごと消して同じ場所へ作り直してもサインインは続く（実測）。
//
// 排他は**ロックファイルに対する OS のロック**で取る。プロセスが異常終了しても OS が解放するため、
// 次の起動で残留ロックを解く手当てが要らない（プロジェクトの同一端末の多重プロセス保護と同じ「プロセスの生死で判定できる」方式）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/howashoji/ReqWeave/app/internal/projectstore"
)

// errWorkspaceBusy は同じ端末の別のウィンドウ（本システムの 2 つ目のプロセス）が一時領域を使っている状態。
//
// **自動再試行の対象にしない**（相手のウィンドウが閉じるまで解消しない）。
var errWorkspaceBusy = errors.New("別のウィンドウが Codex App Server を使っています")

// workspacePaths は一時領域の場所（固定）。
type workspacePaths struct {
	root string
	lock string
}

// workspaceLocation は一時領域の場所を返す（macOS = ~/Library/Caches/<AppID>/codex/）。
//
// Windows での場所は Windows 実機での検証の後に定めるため、ここでは
// OS 標準のキャッシュ領域を用いる（Windows 版では Codex App Server を選べない）。
func workspaceLocation() (workspacePaths, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return workspacePaths{}, fmt.Errorf("一時領域の場所を特定できません: %w", err)
	}
	base := filepath.Join(cache, projectstore.AppID)
	return workspacePaths{
		root: filepath.Join(base, "codex"),
		lock: filepath.Join(base, "codex.lock"),
	}, nil
}

// workspaceLocationFn は場所の解決（パッケージ内のテストのみが差し替える）。
var workspaceLocationFn = workspaceLocation

func (p workspacePaths) home() string    { return filepath.Join(p.root, "home") }
func (p workspacePaths) work() string    { return filepath.Join(p.root, "work") }
func (p workspacePaths) catalog() string { return filepath.Join(p.root, "model_catalog.json") }

// workspace は排他を取った一時領域。release まで同じ端末の他プロセスは使えない。
type workspace struct {
	workspacePaths
	lockFile *os.File
}

// acquireWorkspace は排他を取り、一時領域を作り直して手元のモデル定義を書き出す。
//
// 前回の残り（異常終了など）はここで**フォルダごと**消える。
func acquireWorkspace() (*workspace, error) {
	paths, err := workspaceLocationFn()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(paths.lock), 0o700); err != nil {
		return nil, fmt.Errorf("一時領域の親フォルダを作れません: %w", err)
	}
	file, err := lockExclusive(paths.lock)
	if err != nil {
		return nil, err
	}
	ws := &workspace{workspacePaths: paths, lockFile: file}
	if err := ws.prepare(); err != nil {
		_ = ws.release()
		return nil, err
	}
	return ws, nil
}

// prepare は一時領域を作り直す（残っていたものは消える）。
func (w *workspace) prepare() error {
	if err := os.RemoveAll(w.root); err != nil {
		return fmt.Errorf("一時領域を作り直せません: %w", err)
	}
	for _, dir := range []string{w.home(), w.work()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("一時領域を作れません: %w", err)
		}
	}
	// 手元のモデル定義（catalog.go）。埋め込みの生成物をそのまま書き出す。
	if err := os.WriteFile(w.catalog(), modelCatalogJSON, 0o600); err != nil {
		return fmt.Errorf("モデル定義を書き出せません: %w", err)
	}
	return nil
}

// release は一時領域をフォルダごと消して排他を解く。
func (w *workspace) release() error {
	removeErr := os.RemoveAll(w.root)
	if w.lockFile != nil {
		_ = unlockAndClose(w.lockFile)
		w.lockFile = nil
	}
	if removeErr != nil {
		return fmt.Errorf("一時領域を削除できません: %w", removeErr)
	}
	return nil
}

// removeLeftoverWorkspace は本システムの起動時に、他のプロセスが使っていなければ残りを削除する
// （異常終了への備え）。使用中なら何もしない（相手の一時領域を壊さない）。
func removeLeftoverWorkspace() error {
	paths, err := workspaceLocationFn()
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(paths.lock), 0o700); err != nil {
		return fmt.Errorf("一時領域の親フォルダを作れません: %w", err)
	}
	file, err := lockExclusive(paths.lock)
	if errors.Is(err, errWorkspaceBusy) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unlockAndClose(file) }()
	if err := os.RemoveAll(paths.root); err != nil {
		return fmt.Errorf("一時領域を削除できません: %w", err)
	}
	return nil
}

// supportedOS は Codex App Server を提供する OS か（Windows 版は Windows 実機での検証が済むまで提供しない）。
func supportedOS() bool { return runtime.GOOS != "windows" }
