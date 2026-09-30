package updater

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/appversion"
)

// newTestChecker は httptest のサーバを向いた確認器を作る。
// 差し替えるのは非公開フィールドで、パッケージ外からは触れない。
func newTestChecker(t *testing.T, url string, current string, keys KeySet) *Checker {
	t.Helper()
	sv, err := appversion.ParseSemver(current)
	if err != nil {
		t.Fatal(err)
	}
	return &Checker{
		client:      &http.Client{Timeout: 2 * time.Second},
		manifestURL: url,
		keys:        keys,
		current:     sv,
		goos:        "darwin",
		goarch:      "arm64",
	}
}

// serveManifest はマニフェストを返す httptest サーバを立てる。
func serveManifest(t *testing.T, raw []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func manifestFor(t *testing.T, version string, priv ed25519.PrivateKey, keyID string) []byte {
	t.Helper()
	m := baseManifest(t, []byte("distribution bytes "+version))
	m.Version = version
	return signed(t, m, signer{keyID, priv})
}

// 受け入れ条件: 現行版より新しいときだけ「更新あり」を返すこと。
func TestCheckReportsUpdateOnlyWhenNewer(t *testing.T) {
	pub, priv := testKey(t, 0x71)
	keys, err := NewKeySet(pub)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		published string
		current   string
		want      bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.0", "0.1.0", false}, // 同版
		{"0.1.0", "0.2.0", false}, // 旧版（差し戻し配信で巻き戻さない）
		{"1.0.0", "0.99.99", true},
	}
	for _, c := range cases {
		srv := serveManifest(t, manifestFor(t, c.published, priv, pub.ID))
		res, err := newTestChecker(t, srv.URL, c.current, keys).Check(context.Background())
		if err != nil {
			t.Fatalf("published=%s current=%s: %v", c.published, c.current, err)
		}
		if res.Available != c.want {
			t.Errorf("published=%s current=%s: Available = %v, want %v",
				c.published, c.current, res.Available, c.want)
		}
		if res.Version != c.published {
			t.Errorf("published=%s: Version = %q", c.published, res.Version)
		}
	}
}

// 受け入れ条件: 版番号の比較が数値順であること（文字列比較なら 0.10.0 < 0.9.0 になる）。
func TestCheckComparesVersionsNumerically(t *testing.T) {
	pub, priv := testKey(t, 0x72)
	keys, _ := NewKeySet(pub)
	cases := []struct {
		published, current string
		want               bool
	}{
		{"0.10.0", "0.9.0", true},  // 文字列比較なら "0.10.0" < "0.9.0" で false になる
		{"0.9.0", "0.10.0", false}, // 同上の裏
		{"10.0.0", "9.0.0", true},  // 桁上がり
		{"1.2.10", "1.2.9", true},  // patch の桁上がり
	}
	for _, c := range cases {
		srv := serveManifest(t, manifestFor(t, c.published, priv, pub.ID))
		res, err := newTestChecker(t, srv.URL, c.current, keys).Check(context.Background())
		if err != nil {
			t.Fatalf("published=%s current=%s: %v", c.published, c.current, err)
		}
		if res.Available != c.want {
			t.Errorf("published=%s current=%s: Available = %v, want %v（数値順で比較していない）",
				c.published, c.current, res.Available, c.want)
		}
	}
}

// 受け入れ条件: semver として解釈できない版番号では「更新あり」を返さず誤りにすること。
func TestCheckRejectsUnparseableVersion(t *testing.T) {
	pub, priv := testKey(t, 0x73)
	keys, _ := NewKeySet(pub)
	for _, bad := range []string{"v0.2.0", "0.2", "latest", "0.2.0-rc1"} {
		m := baseManifest(t, []byte("d"))
		m.Version = bad
		srv := serveManifest(t, signed(t, m, signer{pub.ID, priv}))
		res, err := newTestChecker(t, srv.URL, "0.1.0", keys).Check(context.Background())
		if err == nil {
			t.Errorf("版番号 %q が受理された（Available=%v）", bad, res.Available)
			continue
		}
		if res.Available {
			t.Errorf("版番号 %q で Available=true になった", bad)
		}
	}
}

// 受け入れ条件: 署名が信頼できないマニフェストで「更新あり」を返さないこと。
func TestCheckRejectsUntrustedManifest(t *testing.T) {
	trustedPub, _ := testKey(t, 0x74)
	attackerPub, attackerPriv := testKey(t, 0x75)
	keys, _ := NewKeySet(trustedPub)

	srv := serveManifest(t, manifestFor(t, "9.9.9", attackerPriv, attackerPub.ID))
	res, err := newTestChecker(t, srv.URL, "0.1.0", keys).Check(context.Background())
	if err == nil {
		t.Fatal("信頼できない署名のマニフェストが受理された")
	}
	if res.Available {
		t.Fatal("信頼できない署名で Available=true になった")
	}
	if k, _ := KindOf(err); k != KindUntrusted {
		t.Fatalf("Kind = %q, want %q", k, KindUntrusted)
	}
}

// 受け入れ条件: 通信断・404・不正 JSON・タイムアウトのいずれでも誤りを返すだけで、
// 呼び出し側の処理を止めない（panic しない・Available=false）。
func TestCheckFailuresAreRecoverable(t *testing.T) {
	pub, _ := testKey(t, 0x76)
	keys, _ := NewKeySet(pub)

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer notFound.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer broken.Close()

	// 上限超過は「切り詰めても JSON として通ってしまう」形で試す。
	// 正しいマニフェストの後ろに空白を詰めると、上限で切っても JSON としては読めるため、
	// 長さの検査が無ければ受理されてしまう（切り詰めた先に何があるか誰も見ていない状態で通る）。
	padPub, padPriv := testKey(t, 0x7f)
	padded := append(manifestFor(t, "9.9.9", padPriv, padPub.ID),
		[]byte(strings.Repeat(" ", maxManifestBytes))...)
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(padded)
	}))
	defer huge.Close()

	// 接続できないサーバ（起動直後に閉じる）。
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	paddedKeys, err := NewKeySet(padPub)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		url  string
		keys KeySet
		kind Kind
	}{
		"HTTP 404": {notFound.URL, keys, KindNetwork},
		"不正 JSON":  {broken.URL, keys, KindMalformed},
		// 署名は信頼鍵で通る。落ちる理由は長さの検査だけであることを固定する。
		"応答が大きすぎる": {huge.URL, paddedKeys, KindMalformed},
		"接続できない":   {deadURL, keys, KindNetwork},
	}
	for name, c := range cases {
		res, err := newTestChecker(t, c.url, "0.1.0", c.keys).Check(context.Background())
		if err == nil {
			t.Errorf("%s: 誤りを返さなかった", name)
			continue
		}
		if res.Available {
			t.Errorf("%s: 失敗したのに Available=true", name)
		}
		if k, _ := KindOf(err); k != c.kind {
			t.Errorf("%s: Kind = %q, want %q（%v）", name, k, c.kind, err)
		}
	}
}

// 受け入れ条件: 更新確認に時間の上限があり、上限超過で打ち切られること。
func TestCheckTimesOut(t *testing.T) {
	pub, _ := testKey(t, 0x77)
	keys, _ := NewKeySet(pub)

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // 応答を返さない
	}))
	defer func() { close(block); srv.Close() }()

	c := newTestChecker(t, srv.URL, "0.1.0", keys)
	c.client = &http.Client{Timeout: 150 * time.Millisecond}

	start := time.Now()
	_, err := c.Check(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("応答が返らないのに誤りにならなかった")
	}
	if k, _ := KindOf(err); k != KindNetwork {
		t.Fatalf("Kind = %q, want %q", k, KindNetwork)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("打ち切りに %v かかった（上限が効いていない）", elapsed)
	}
}

// context の取り消しで打ち切れること（起動時の確認をアプリ終了で中断できる）。
func TestCheckHonorsContextCancel(t *testing.T) {
	pub, _ := testKey(t, 0x78)
	keys, _ := NewKeySet(pub)

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := newTestChecker(t, srv.URL, "0.1.0", keys).Check(ctx); err == nil {
		t.Fatal("context を取り消しても誤りにならなかった")
	}
}

// 受け入れ条件: この環境向けの配布物が無いとき「更新あり」ではなく専用の誤りを返すこと。
func TestCheckReportsMissingAssetForPlatform(t *testing.T) {
	pub, priv := testKey(t, 0x79)
	keys, _ := NewKeySet(pub)

	m := baseManifest(t, []byte("d"))
	m.Version = "0.2.0"
	m.Assets = m.Assets[1:] // windows/amd64 のみ残す
	srv := serveManifest(t, signed(t, m, signer{pub.ID, priv}))

	c := newTestChecker(t, srv.URL, "0.1.0", keys) // goos=darwin
	res, err := c.Check(context.Background())
	if err == nil {
		t.Fatal("対象外の環境で誤りにならなかった")
	}
	if res.Available {
		t.Fatal("配布物が無いのに Available=true")
	}
	if k, _ := KindOf(err); k != KindNoAsset {
		t.Fatalf("Kind = %q, want %q", k, KindNoAsset)
	}
}

// 更新あり時に、この環境向けの配布物が選ばれること（macOS は universal）。
func TestCheckSelectsAssetForPlatform(t *testing.T) {
	pub, priv := testKey(t, 0x7a)
	keys, _ := NewKeySet(pub)
	srv := serveManifest(t, manifestFor(t, "0.2.0", priv, pub.ID))

	res, err := newTestChecker(t, srv.URL, "0.1.0", keys).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatal("更新ありにならなかった")
	}
	if res.Asset.OS != "darwin" || res.Asset.Arch != ArchUniversal {
		t.Fatalf("選ばれた配布物 = %s", res.Asset.ID())
	}
	if !strings.HasPrefix(res.Asset.URL, "https://") {
		t.Fatalf("配布物 URL が https でない: %q", res.Asset.URL)
	}
}

// 受け入れ条件: 取得先が定数として 1 か所に定義され、実行時に差し替える公開経路が無いこと。
func TestManifestURLIsAConstantOverHTTPS(t *testing.T) {
	if !strings.HasPrefix(ManifestURL, "https://github.com/") {
		t.Fatalf("ManifestURL = %q（https の GitHub でない）", ManifestURL)
	}
	if !strings.Contains(ManifestURL, ReleasesOwner+"/"+ReleasesRepo+"/releases/") {
		t.Fatalf("ManifestURL = %q（Releases の経路でない）", ManifestURL)
	}
	// 配布先はソースを公開しているリポジトリの Releases。変えると、それより前の版のアプリが更新を見失うので、値そのものを固定する。
	const want = "https://github.com/howashoji/ReqWeave/releases/latest/download/update-manifest.json"
	if ManifestURL != want {
		t.Fatalf("ManifestURL = %q、期待は %q（配布先を変えるなら、旧版の利用者への影響を確かめてからこの値も直す）", ManifestURL, want)
	}
	// 既定の確認器は必ず定数を向く。
	c, err := NewChecker()
	if err != nil {
		t.Fatal(err)
	}
	if c.manifestURL != ManifestURL {
		t.Fatalf("既定の取得先 = %q, want %q", c.manifestURL, ManifestURL)
	}
	if c.client.Timeout <= 0 {
		t.Fatal("既定のクライアントにタイムアウトが無い")
	}
}

// 既定の確認器は正本の版番号と埋め込み鍵を使う。
func TestNewCheckerUsesSourceOfTruth(t *testing.T) {
	c, err := NewChecker()
	if err != nil {
		t.Fatal(err)
	}
	want, err := appversion.Current()
	if err != nil {
		t.Fatal(err)
	}
	if c.current != want {
		t.Fatalf("現行版 = %v, want %v", c.current, want)
	}
	embedded, err := TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if c.keys.Len() != embedded.Len() {
		t.Fatalf("鍵数 = %d, want %d", c.keys.Len(), embedded.Len())
	}
}

// 応答が正しくてもマニフェストが JSON として空なら誤りになる（素通りしない）。
func TestCheckRejectsEmptyBody(t *testing.T) {
	pub, _ := testKey(t, 0x7b)
	keys, _ := NewKeySet(pub)
	srv := serveManifest(t, []byte("{}"))
	if _, err := newTestChecker(t, srv.URL, "0.1.0", keys).Check(context.Background()); err == nil {
		t.Fatal("空のマニフェストが受理された")
	}
}
