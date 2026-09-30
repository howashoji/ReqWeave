//go:build integration

// 実ファイル I/O と HTTP サーバを使う（モジュール間・実ファイル I/O を扱う結合テスト）。

package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// makeAppZip は「アプリ本体 1 個」だけを含む zip を作る（macOS の .app 相当のディレクトリ）。
func makeAppZip(t *testing.T, appName, marker string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := map[string]string{
		appName + "/Contents/Info.plist":     "<plist>" + marker + "</plist>",
		appName + "/Contents/MacOS/ReqWeave": "binary-" + marker,
	}
	for name, content := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func assetFor(t *testing.T, url string, data []byte) Asset {
	t.Helper()
	sum := sha256.Sum256(data)
	return Asset{OS: "darwin", Arch: ArchUniversal, URL: url,
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

// installedApp は home 配下に現行版のアプリを置き、その場所を返す。
func installedApp(t *testing.T, home, appName, marker string) string {
	t.Helper()
	target := filepath.Join(home, "Applications", appName)
	if err := os.MkdirAll(filepath.Join(target, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(target, "Contents", "Info.plist"), "<plist>"+marker+"</plist>")
	write(t, filepath.Join(target, "Contents", "MacOS", "ReqWeave"), "binary-"+marker)
	return target
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("読めない（%s）: %v", path, err)
	}
	return string(b)
}

// treeHash は対象配下の全ファイルのパスと内容から要約を作る（適用前後の不変を確かめる）。
func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		h.Write([]byte(rel))
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write(b)
		return nil
	})
	if err != nil {
		t.Fatalf("走査に失敗（%s）: %v", root, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func serveBytes(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 受け入れ条件: 検証に合格した場合にのみ置換が行われ、適用後は新版になっていること。
func TestDownloadAndApplyReplacesTarget(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	zipped := makeAppZip(t, "ReqWeave.app", "v2")
	srv := serveBytes(t, zipped)

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	asset := assetFor(t, srv.URL, zipped)

	path, err := a.Download(context.Background(), asset)
	if err != nil {
		t.Fatalf("取得に失敗: %v", err)
	}
	applied, err := a.Apply(path, "0.2.0")
	if err != nil {
		t.Fatalf("適用に失敗: %v", err)
	}
	if !applied.RestartRequired {
		t.Error("再起動が必要と返っていない")
	}
	if applied.Version != "0.2.0" || applied.TargetPath != target {
		t.Errorf("結果が想定外: %+v", applied)
	}
	if got := read(t, filepath.Join(target, "Contents", "MacOS", "ReqWeave")); got != "binary-v2" {
		t.Fatalf("置換されていない: %q", got)
	}
	// 実行権限が残っていること（残らないとアプリが起動しない）。
	info, err := os.Stat(filepath.Join(target, "Contents", "MacOS", "ReqWeave"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("実行権限が落ちている: %v", info.Mode())
	}
	// 作業場所と退避が残っていないこと。
	if _, err := os.Stat(a.stagingDir()); !os.IsNotExist(err) {
		t.Error("作業場所が残っている")
	}
	if _, err := os.Stat(target + ".previous"); !os.IsNotExist(err) {
		t.Error("退避した現行版が残っている")
	}
}

// 受け入れ条件: 検証不合格時に一時ファイルが削除され、現行版のファイルが変化しないこと。
func TestDownloadRejectsTamperedAssetAndLeavesTargetIntact(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)

	zipped := makeAppZip(t, "ReqWeave.app", "v2")
	asset := assetFor(t, "", zipped)

	tampered := append([]byte{}, zipped...)
	tampered[len(tampered)/2] ^= 0x01 // 1 バイト改変（長さは変えない）
	srv := serveBytes(t, tampered)
	asset.URL = srv.URL

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Download(context.Background(), asset); err == nil {
		t.Fatal("改変した配布物が受理された")
	} else if k, _ := KindOf(err); k != KindHashMismatch {
		t.Fatalf("Kind = %q, want %q", k, KindHashMismatch)
	}
	if after := treeHash(t, target); after != before {
		t.Fatal("現行版が変化した")
	}
	assertNoLeftovers(t, a)
}

// 受け入れ条件: サイズがマニフェスト記載値と一致しない場合に適用しないこと。
func TestDownloadRejectsSizeMismatch(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)
	zipped := makeAppZip(t, "ReqWeave.app", "v2")

	for name, body := range map[string][]byte{
		"短い": zipped[:len(zipped)-1],
		"長い": append(append([]byte{}, zipped...), 'x'),
	} {
		srv := serveBytes(t, body)
		asset := assetFor(t, srv.URL, zipped) // 記載は元の zip のサイズ・ハッシュ
		a, err := newApplier(target, home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Download(context.Background(), asset); err == nil {
			t.Errorf("%s配布物が受理された", name)
		} else if k, _ := KindOf(err); k != KindSizeMismatch {
			t.Errorf("%s: Kind = %q, want %q", name, k, KindSizeMismatch)
		}
		assertNoLeftovers(t, a)
	}
	if after := treeHash(t, target); after != before {
		t.Fatal("現行版が変化した")
	}
}

// 受け入れ条件: 中止操作ができ、中止後に一時ファイルが残らないこと。
func TestDownloadCancelLeavesNoTemporaryFiles(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-release // 続きを返さない
	}))
	defer func() { close(release); srv.Close() }()

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel() // 利用者による中止に相当
	}()
	if _, err := a.Download(ctx, assetFor(t, srv.URL, []byte("full distribution bytes"))); err == nil {
		t.Fatal("中止したのに成功した")
	}
	assertNoLeftovers(t, a)
	if after := treeHash(t, target); after != before {
		t.Fatal("現行版が変化した")
	}
}

// 受け入れ条件: rename 直前で中断させた場合に現行版が読み出せること。
func TestApplyInterruptedBeforeSwapKeepsCurrentVersion(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)
	zipped := makeAppZip(t, "ReqWeave.app", "v2")
	srv := serveBytes(t, zipped)

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.Download(context.Background(), assetFor(t, srv.URL, zipped))
	if err != nil {
		t.Fatal(err)
	}
	a.beforeSwap = func() error { return errors.New("中断（電源断・強制終了に相当）") }

	if _, err := a.Apply(path, "0.2.0"); err == nil {
		t.Fatal("中断したのに成功した")
	}
	if after := treeHash(t, target); after != before {
		t.Fatal("中断で現行版が壊れた")
	}
	if got := read(t, filepath.Join(target, "Contents", "MacOS", "ReqWeave")); got != "binary-v1" {
		t.Fatalf("現行版が読み出せない: %q", got)
	}
}

// 差し替えに失敗したら現行版を戻すこと（新版を対象名へ rename できない場合）。
func TestApplyRestoresCurrentVersionWhenSwapFails(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)
	zipped := makeAppZip(t, "ReqWeave.app", "v2")
	srv := serveBytes(t, zipped)

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.Download(context.Background(), assetFor(t, srv.URL, zipped))
	if err != nil {
		t.Fatal(err)
	}
	// 展開後・退避後に新版が消えた状況を作る（rename が失敗する）。
	a.beforeSwap = func() error {
		return os.RemoveAll(filepath.Join(a.stagingDir(), "extract"))
	}
	if _, err := a.Apply(path, "0.2.0"); err == nil {
		t.Fatal("新版が無いのに成功した")
	}
	if after := treeHash(t, target); after != before {
		t.Fatal("差し替え失敗後に現行版が復元されていない")
	}
}

// zip の中身が対象の外を指す場合に展開しないこと（zip slip）。
func TestApplyRejectsArchiveEscapingDestination(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("../../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("escaped")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	srv := serveBytes(t, buf.Bytes())

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.Download(context.Background(), assetFor(t, srv.URL, buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(path, "0.2.0"); err == nil {
		t.Fatal("対象の外を指す書庫が展開された")
	}
	// 脱出先は展開先からの相対で決まるため、ホーム配下を全走査して不在を確かめる
	//（1 か所だけ見ると、脱出先が想定と違ったときに素通りする）。
	if found := findFile(t, home, "escaped.txt"); found != "" {
		t.Fatalf("展開先の外にファイルが作られた: %s", found)
	}
	if after := treeHash(t, target); after != before {
		t.Fatal("現行版が変化した")
	}
}

// 配布物に同梱した手順書があってもアプリ本体を選んで適用できること
// （配布物には OS 警告の回避手順書を同梱する）。
func TestApplyPicksAppBundleAlongsideBundledDocs(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := map[string]string{
		"ReqWeave.app/Contents/Info.plist":     "<plist>v2</plist>",
		"ReqWeave.app/Contents/MacOS/ReqWeave": "binary-v2",
		"初回起動の手順.md":                           "# 初回起動の手順\n",
	}
	for name, content := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	srv := serveBytes(t, buf.Bytes())

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.Download(context.Background(), assetFor(t, srv.URL, buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(path, "0.2.0"); err != nil {
		t.Fatalf("同梱物があるだけで適用に失敗した: %v", err)
	}
	if got := read(t, filepath.Join(target, "Contents", "MacOS", "ReqWeave")); got != "binary-v2" {
		t.Fatalf("置換されていない: %q", got)
	}
	// 同梱物は置換対象の外へ展開されない（作業場所ごと消える）。
	if found := findFile(t, home, "初回起動の手順.md"); found != "" {
		t.Fatalf("同梱物が残っている: %s", found)
	}
}

// 書庫の中にアプリ本体が無い、または 2 件以上ある場合は適用しない。
func TestApplyRejectsUnexpectedArchiveLayout(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)

	layouts := map[string][]string{
		"アプリ本体が無い":   {"README.txt", "docs/guide.md"},
		"アプリ本体が 2 件": {"ReqWeave.app/Contents/Info.plist", "ReqWeaveOld.app/Contents/Info.plist"},
	}
	for name, names := range layouts {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for _, n := range names {
			f, err := w.Create(n)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		srv := serveBytes(t, buf.Bytes())

		a, err := newApplier(target, home)
		if err != nil {
			t.Fatal(err)
		}
		path, err := a.Download(context.Background(), assetFor(t, srv.URL, buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Apply(path, "0.2.0"); err == nil {
			t.Errorf("%s の書庫が適用された", name)
		}
		if after := treeHash(t, target); after != before {
			t.Fatalf("%s: 現行版が変化した", name)
		}
	}
}

// HTTP エラーでも現行版に触れないこと。
func TestDownloadHTTPErrorLeavesTargetIntact(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")
	before := treeHash(t, target)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Download(context.Background(), assetFor(t, srv.URL, []byte("x"))); err == nil {
		t.Fatal("HTTP エラーで成功した")
	} else if k, _ := KindOf(err); k != KindNetwork {
		t.Fatalf("Kind = %q, want %q", k, KindNetwork)
	}
	if after := treeHash(t, target); after != before {
		t.Fatal("現行版が変化した")
	}
	assertNoLeftovers(t, a)
}

// findFile は root 配下から name のファイルを探し、見つかった絶対パスを返す（無ければ空文字）。
func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == name {
			found = p
		}
		return nil
	})
	if err != nil {
		t.Fatalf("走査に失敗（%s）: %v", root, err)
	}
	return found
}

// assertNoLeftovers は作業場所に取得途中のファイルが残っていないことを確かめる。
func assertNoLeftovers(t *testing.T, a *Applier) {
	t.Helper()
	entries, err := os.ReadDir(a.stagingDir())
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("取得途中のファイルが残っている: %s", e.Name())
		}
	}
}

// 取得は「マニフェスト記載のサイズ +1」で打ち切る。
// 上限が無いと、記載を大きく超える応答をいったん全部ディスクへ書いてから捨てることになる
// （サイズ検査だけでは最後に落とせても、その前に容量を食ってしまう）。
func TestDownloadStopsReadingBeyondDeclaredSize(t *testing.T) {
	home := t.TempDir()
	target := installedApp(t, home, "ReqWeave.app", "v1")

	const declared = 1024
	const oversize = 16 << 20 // 記載サイズを大きく超える応答

	var mu sync.Mutex
	written := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 64<<10)
		for sent := 0; sent < oversize; sent += len(chunk) {
			n, err := w.Write(chunk)
			mu.Lock()
			written += n
			mu.Unlock()
			if err != nil {
				return // 相手が読むのをやめた
			}
		}
	}))
	defer srv.Close()

	asset := Asset{OS: "darwin", Arch: ArchUniversal, URL: srv.URL,
		SHA256: strings.Repeat("0", 64), Size: declared}

	a, err := newApplier(target, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Download(context.Background(), asset); err == nil {
		t.Fatal("記載サイズと違う応答が受理された")
	} else if k, _ := KindOf(err); k != KindSizeMismatch {
		t.Fatalf("Kind = %q, want %q", k, KindSizeMismatch)
	}

	mu.Lock()
	got := written
	mu.Unlock()
	// 上限が効いていれば、記載サイズ + ソケットバッファ程度で打ち切られる。
	// 上限が無ければ 16 MiB を全部書き切ってしまう。
	if got >= 4<<20 {
		t.Fatalf("応答を %d バイト読み込んだ（記載は %d バイト。上限が効いていない）", got, declared)
	}
	assertNoLeftovers(t, a)
}
