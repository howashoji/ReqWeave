package updater

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stagingDirName は取得・展開の作業場所（置換対象と同じ親ディレクトリに置く）。
//
// 別のボリュームに置くと最後の rename がボリュームをまたいで失敗するため、
// 置換対象の隣に作る。先頭のドットで通常の一覧から隠す。
const stagingDirName = ".reqweave-update"

// downloadTimeout は配布物 1 件の取得の上限時間。
// マニフェスト取得（DefaultTimeout）より長い。配布物は数十 MB になりうるため。
const downloadTimeout = 10 * time.Minute

// Applied は適用の結果。
type Applied struct {
	// Version は適用した版。
	Version string
	// TargetPath は置換した対象のパス。
	TargetPath string
	// RestartRequired は再起動が必要か。適用が成功した場合は常に true。
	//
	// **本パッケージは再起動しない**。利用者の操作で再起動する（無断で再起動しない）。
	RestartRequired bool
}

// Applier は配布物の取得・検証・適用（更新の流れの (3)〜(4)）を行う。
type Applier struct {
	client *http.Client
	// target は置換対象。macOS は `ReqWeave.app` ディレクトリ、Windows は実行ファイル。
	target string
	// home は「ユーザー領域」の境界。target がこの配下に無ければ適用しない。
	home string
	// beforeSwap は rename 直前のフック。**テストのみが差し替える**
	// （中断時に現行版が壊れないことを確かめるため）。
	beforeSwap func() error
}

// NewApplier は置換対象を指定して適用器を作る。
//
// 対象がユーザー領域（ホームフォルダ配下）に無い場合は誤りを返す。
// 管理者権限を要する場所（/Applications 直下・Program Files 等）へは書き込まない
// （管理者権限なしで更新が完了するようにするため）。
func NewApplier(target string) (*Applier, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &Error{Kind: KindNetwork,
			msg: "更新を適用できませんでした。アプリを再起動して再実行してください", cause: err}
	}
	return newApplier(target, home)
}

func newApplier(target, home string) (*Applier, error) {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("置換対象のパスを解決できません: %w", err)
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("ホームフォルダのパスを解決できません: %w", err)
	}
	if !underDir(absHome, absTarget) {
		return nil, &Error{Kind: KindNotUserArea,
			msg:   "このアプリの場所では自動更新できません。配布元から最新版を入手してください",
			cause: fmt.Errorf("target %q is outside user area %q", absTarget, absHome)}
	}
	return &Applier{
		client: &http.Client{Timeout: downloadTimeout},
		target: absTarget,
		home:   absHome,
	}, nil
}

// underDir は child が dir と同じか配下にあるかを返す。
func underDir(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// stagingDir は作業場所（置換対象の隣）を返す。
func (a *Applier) stagingDir() string {
	return filepath.Join(filepath.Dir(a.target), stagingDirName)
}

// Download は配布物を作業場所へ取得し、完全性を検証したうえでそのパスを返す。
//
// 検証に不合格なら一時ファイルを消して誤りを返す（現行版には触れない）。
// ctx の取り消しで中断でき、中断時も一時ファイルを残さない。
func (a *Applier) Download(ctx context.Context, asset Asset) (path string, err error) {
	staging := a.stagingDir()
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", &Error{Kind: KindNotUserArea,
			msg: "更新の作業場所を用意できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	tmp, err := os.CreateTemp(staging, "download-*.part")
	if err != nil {
		return "", &Error{Kind: KindNotUserArea,
			msg: "更新の作業場所を用意できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			// 中断・失敗時は一時ファイルを残さない。
			_ = os.Remove(tmpName)
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", &Error{Kind: KindNetwork,
			msg: "更新ファイルを取得できませんでした。通信を確認して再実行してください", cause: err}
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", &Error{Kind: KindNetwork,
			msg: "更新ファイルを取得できませんでした。通信を確認して再実行してください", cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &Error{Kind: KindNetwork,
			msg:   "更新ファイルを取得できませんでした。しばらく待って再実行してください",
			cause: fmt.Errorf("status %d", resp.StatusCode)}
	}
	// マニフェスト記載のサイズ +1 までしか読まない（過大な応答で容量を食わないため）。
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, asset.Size+1)); err != nil {
		return "", &Error{Kind: KindNetwork,
			msg: "更新ファイルを取得できませんでした。通信を確認して再実行してください", cause: err}
	}
	if err := tmp.Sync(); err != nil {
		return "", &Error{Kind: KindNetwork,
			msg: "更新ファイルを保存できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	if err := tmp.Close(); err != nil {
		return "", &Error{Kind: KindNetwork,
			msg: "更新ファイルを保存できませんでした。空き容量を確認して再実行してください", cause: err}
	}

	// 取得したものを読み直して検証する（サイズ → SHA-256 の順に落ちる）。
	f, err := os.Open(tmpName)
	if err != nil {
		return "", &Error{Kind: KindNetwork,
			msg: "更新ファイルを読み込めませんでした。もう一度実行してください", cause: err}
	}
	verifyErr := VerifyAssetStream(f, asset)
	_ = f.Close()
	if verifyErr != nil {
		return "", verifyErr
	}
	committed = true
	return tmpName, nil
}

// Apply は検証済みの配布物（macOS は dmg、Windows は zip）を取り出し、置換対象と差し替える。
//
// 手順: 展開 → 現行版を退避（rename）→ 新版を対象名へ rename → 退避を削除。
// 途中で失敗したら退避を戻すため、現行版が消えた状態は残らない（保存処理の原子性と同じ方針）。
func (a *Applier) Apply(archivePath, version string) (Applied, error) {
	staging := a.stagingDir()
	extractDir := filepath.Join(staging, "extract")
	if err := os.RemoveAll(extractDir); err != nil {
		return Applied{}, &Error{Kind: KindNotUserArea,
			msg: "更新の作業場所を用意できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	if err := extractArchive(archivePath, extractDir); err != nil {
		return Applied{}, err
	}
	newPath, err := appEntry(extractDir)
	if err != nil {
		return Applied{}, err
	}

	backup := a.target + ".previous"
	_ = os.RemoveAll(backup) // 前回の残骸があれば消す

	if a.beforeSwap != nil {
		if err := a.beforeSwap(); err != nil {
			// 差し替え直前の中断。現行版には一切触れていない。
			return Applied{}, &Error{Kind: KindNetwork,
				msg: "更新を適用できませんでした。もう一度実行してください", cause: err}
		}
	}

	targetExisted := true
	if _, err := os.Lstat(a.target); os.IsNotExist(err) {
		targetExisted = false
	}
	if targetExisted {
		if err := os.Rename(a.target, backup); err != nil {
			return Applied{}, &Error{Kind: KindNotUserArea,
				msg: "更新を適用できませんでした。アプリを終了してから再実行してください", cause: err}
		}
	}
	if err := os.Rename(newPath, a.target); err != nil {
		if targetExisted {
			_ = os.Rename(backup, a.target) // 現行版を戻す
		}
		return Applied{}, &Error{Kind: KindNotUserArea,
			msg: "更新を適用できませんでした。アプリを終了してから再実行してください", cause: err}
	}
	_ = os.RemoveAll(backup)
	_ = os.RemoveAll(staging)

	return Applied{Version: version, TargetPath: a.target, RestartRequired: true}, nil
}

// Cleanup は作業場所を消す（中止・失敗後の後始末）。
func (a *Applier) Cleanup() error {
	if err := os.RemoveAll(a.stagingDir()); err != nil {
		return fmt.Errorf("更新の作業場所を片づけられません: %w", err)
	}
	return nil
}

// appEntry は展開先からアプリ本体（macOS は `.app`、Windows は `.exe`）を 1 件だけ選ぶ。
//
// 配布物には OS 警告の回避手順書を同梱する（初回起動時に OS が出す警告への対処を、利用者が手元で確かめられるように）。
// 「中身は 1 件だけ」を要求すると同梱物と両立しないため、**アプリ本体を選ぶ**方式にした。
// 本体が 0 件・2 件以上なら適用しない（想定と違う書庫を黙って適用しない）。
func appEntry(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", &Error{Kind: KindMalformed,
			msg: "更新ファイルの中身を読み取れないため、現行版のまま更新を中止しました。配布元の案内を確認してください", cause: err}
	}
	var found []string
	for _, e := range entries {
		if isAppBundleName(e.Name()) {
			found = append(found, e.Name())
		}
	}
	if len(found) != 1 {
		return "", &Error{Kind: KindMalformed,
			msg:   "更新ファイルの中身が想定と違うため、現行版のまま更新を中止しました。配布元の案内を確認してください",
			cause: fmt.Errorf("expected exactly 1 app entry, got %d of %d entries", len(found), len(entries))}
	}
	return filepath.Join(dir, found[0]), nil
}

// isAppBundleName はアプリ本体らしい名前かを返す。
// macOS は `.app` バンドル（ディレクトリ）、Windows は `.exe`。
func isAppBundleName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".app") || strings.HasSuffix(lower, ".exe")
}

// unzip は zip を dest 配下へ展開する。
//
// 書庫内のパスが dest の外を指す（`../` を含む・絶対パス）場合は展開しない。
// シンボリックリンクも展開しない（いずれも置換対象の外を書き換える経路になるため）。
func unzip(archivePath, dest string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return &Error{Kind: KindMalformed, msg: "更新ファイルを開けないため、現行版のまま更新を中止しました。配布元の案内を確認してください", cause: err}
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return &Error{Kind: KindNotUserArea,
			msg: "更新の作業場所を用意できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	for _, f := range r.File {
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !underDir(dest, target) {
			return &Error{Kind: KindMalformed,
				msg:   "更新ファイルの中身が想定と違うため、現行版のまま更新を中止しました。配布元の案内を確認してください",
				cause: fmt.Errorf("entry escapes destination: %q", f.Name)}
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return &Error{Kind: KindMalformed,
				msg:   "更新ファイルの中身が想定と違うため、現行版のまま更新を中止しました。配布元の案内を確認してください",
				cause: fmt.Errorf("symlink entry: %q", f.Name)}
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return &Error{Kind: KindNotUserArea, msg: "更新ファイルを展開できませんでした。空き容量を確認して再実行してください", cause: err}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return &Error{Kind: KindNotUserArea, msg: "更新ファイルを展開できませんでした。空き容量を確認して再実行してください", cause: err}
		}
		if err := writeZipEntry(f, target, mode); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string, mode os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return &Error{Kind: KindMalformed, msg: "更新ファイルを展開できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	defer rc.Close()

	// 実行権限は保つ（アプリ本体の実行ファイルが実行できなくなるため）。
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &Error{Kind: KindNotUserArea, msg: "更新ファイルを展開できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil {
		return &Error{Kind: KindMalformed, msg: "更新ファイルを展開できませんでした。空き容量を確認して再実行してください", cause: err}
	}
	return nil
}
