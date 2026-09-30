package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/howashoji/ReqWeave/app/internal/appversion"
)

// 更新確認の取得先（GitHub Releases のパブリックリポジトリ）。
//
// **定数として 1 か所に置く**。実行時・設定ファイル・環境変数から差し替える経路は設けない
// （AI の送信先のエンドポイントを定数にしているのと同じ構造的担保）。更新のための通信は
// 許可した外向きの経路の 1 つであり、ここ以外から外へ出てはならない。
const (
	// ReleasesOwner / ReleasesRepo は配布元のリポジトリ。
	//
	// **ソースを公開しているリポジトリの Releases**（配布物と更新マニフェストは、ソースと同じタグの Release に付ける）。
	// `releases/latest/download` は匿名の HTTPS GET で解決される経路のため、配布元は公開でなければならない。
	// **この値を変えると、それより前の版のアプリが更新を見失う**（取得先が URL に埋め込まれるため）。
	ReleasesOwner = "howashoji"
	ReleasesRepo  = "ReqWeave"

	// ManifestURL は更新マニフェストの固定 URL。
	// GitHub Releases の latest/download は「最新リリースの同名資産」へ解決される静的な経路で、
	// API キーも認証も要らない（更新に要る通信を「HTTPS の静的ファイル 2 点（マニフェストと配布物）」に限る前提を満たす）。
	ManifestURL = "https://github.com/" + ReleasesOwner + "/" + ReleasesRepo +
		"/releases/latest/download/update-manifest.json"
)

// maxManifestBytes は更新マニフェストとして読み込む上限。
// 巨大な応答で起動時のメモリ・時間を食わないための防御（正常なマニフェストは数 KB）。
const maxManifestBytes = 1 << 20 // 1 MiB

// DefaultTimeout は更新確認 1 回の上限時間。
// 起動時に走るため、遅い回線でもアプリの利用開始を待たせない値にする。
const DefaultTimeout = 10 * time.Second

// Result は更新確認の結果。
type Result struct {
	// Available は現行版より新しい版が公開されているか。
	Available bool
	// Version は公開されている版番号（Available が false のときは現行版と同じか古い版）。
	Version string
	// Asset は現在の OS / アーキテクチャ向けの配布物（Available が true のときのみ意味を持つ）。
	Asset Asset
	// Manifest は検証に合格した更新マニフェスト。
	Manifest Manifest
}

// Checker は更新確認（更新の流れの (1)・(2)）を行う。
type Checker struct {
	// client は HTTP クライアント。既定は TLS 検証を有効にした標準の実装。
	client *http.Client
	// manifestURL は取得先。既定は ManifestURL 定数。
	// **テストのみが差し替える**（同一パッケージ内からしか触れない非公開フィールド）。
	manifestURL string
	// keys は署名検証に使う信頼公開鍵。
	keys KeySet
	// current は現行版。
	current appversion.Semver
	// goos / goarch は配布物の選択に使う実行環境。
	goos, goarch string
}

// NewChecker はアプリ埋め込みの信頼鍵と正本の版番号で更新確認器を作る。
func NewChecker() (*Checker, error) {
	keys, err := TrustedKeys()
	if err != nil {
		return nil, err
	}
	current, err := appversion.Current()
	if err != nil {
		return nil, err
	}
	return &Checker{
		client:      &http.Client{Timeout: DefaultTimeout},
		manifestURL: ManifestURL,
		keys:        keys,
		current:     current,
		goos:        runtime.GOOS,
		goarch:      runtime.GOARCH,
	}, nil
}

// Check は更新マニフェストを取得・検証し、新版の有無を返す。
//
// 失敗は誤りとして返すだけで、呼び出し側の処理を止めない。
// 起動時の更新確認が失敗してもアプリの利用は妨げない（求められるのは
// 「新版の存在が通知されること」であり、確認に失敗しても起動を止める理由は無い）。
func (c *Checker) Check(ctx context.Context) (Result, error) {
	raw, err := c.fetchManifest(ctx)
	if err != nil {
		return Result{}, err
	}
	m, err := VerifyManifest(raw, c.keys)
	if err != nil {
		return Result{}, err
	}
	latest, err := appversion.ParseSemver(m.Version)
	if err != nil {
		// ここへは来ない（ParseManifest が版番号を検査済み）が、
		// 検査を外した改修で素通りしないよう明示的に誤りへ倒す。
		return Result{}, &Error{Kind: KindMalformed, msg: "更新情報の内容が正しくないため、現行版のまま更新を中止しました。時間をおいて再実行してください", cause: err}
	}
	if latest.Compare(c.current) <= 0 {
		// 同版・旧版では「更新あり」にしない（差し戻し配信での意図しない巻き戻りを防ぐ）。
		return Result{Available: false, Version: m.Version, Manifest: m}, nil
	}
	asset, ok := m.Asset(c.goos, c.goarch)
	if !ok {
		return Result{}, &Error{Kind: KindNoAsset,
			msg:   "この環境向けの更新が公開されていません。配布元の案内を確認してください",
			cause: fmt.Errorf("no asset for %s/%s", c.goos, c.goarch)}
	}
	return Result{Available: true, Version: m.Version, Asset: asset, Manifest: m}, nil
}

// fetchManifest は更新マニフェストを取得する。
func (c *Checker) fetchManifest(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.manifestURL, nil)
	if err != nil {
		return nil, &Error{Kind: KindNetwork,
			msg: "更新の確認に失敗しました。しばらく待って再実行してください", cause: err}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, &Error{Kind: KindNetwork,
			msg: "更新の確認に失敗しました。通信を確認して再実行してください", cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Kind: KindNetwork,
			msg:   "更新の確認に失敗しました。しばらく待って再実行してください",
			cause: fmt.Errorf("status %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, &Error{Kind: KindNetwork,
			msg: "更新の確認に失敗しました。通信を確認して再実行してください", cause: err}
	}
	if len(raw) > maxManifestBytes {
		return nil, &Error{Kind: KindMalformed,
			msg:   "更新情報を読み取れないため、現行版のまま更新を中止しました。時間をおいて再実行してください",
			cause: fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)}
	}
	return raw, nil
}
