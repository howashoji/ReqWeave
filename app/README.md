# app — ReqWeave 実装

対話型の要件定義・基本設計支援クライアントアプリ **ReqWeave** のソースコード。
デスクトップアプリ（macOS / Windows）として動き、ここからアプリ本体と配布物をビルドする。

## 技術スタック

| 層 | 技術 |
|---|---|
| アプリ基盤 | Wails v2（v2.15.0） |
| バックエンド | Go 1.27 |
| フロントエンド | TypeScript + React 19 + Vite |
| ユニットテスト | Go 標準 `testing` / vitest + Testing Library |

## ディレクトリ構成

```
main.go               エントリポイント（ウィンドウ設定・バインディング登録）
internal/             モジュールごとの内部パッケージ
  binding/            公開バインディング層。フロントエンドからの唯一の入口
  dialogue/           対話エンジン
  aiprovider/         AIプロバイダ抽象化層。プロバイダ SDK を import してよい唯一の場所
  projectstore/       プロジェクトストア
  exchange/           受け渡しモジュール
  importer/           取り込みモジュール
  docgen/             ドキュメント生成
  keymanager/         キーマネージャ（OS セキュアストレージ直結）。同期先の認証情報も同じ機構・別サービス名で扱う。go-keyring を import してよい唯一の場所
  masking/            秘密情報のマスキングフィルタ
  aiprovider/adapter/codex/  Codex App Server アダプタ。**同梱の Codex を子プロセスとして起動してよい唯一の場所**
                      （起動設定の固定・起動後の検査・一時領域の後片づけを含む。結合テストは実バイナリを使う）
  sync/               同期モジュール。外部 `git` コマンドを呼んでよい唯一の場所
                      （取得・取り込み・反映・失敗分類・認証情報の受け渡し。三面マージは Merger、同期の記録は Recorder の受け口を通す）
  updater/            アップデータ
  auditlog/           監査ログ・利用量集計
  nfrcheck/           単一モジュールに閉じない非機能要件（性能・信頼性など）の横断検査（テストのみ）
tools/depcheck/       モジュール間の依存規則（どの層が何を import・起動してよいか）の機械検知。make lint から実行
tools/crashwriter/    強制終了されても保存中のデータが壊れないことの検証で、**外から強制終了される側**になるプロセス
tools/scalegen/       データ量が上限規模に達したプロジェクトを生成する（応答時間の実測用）
tools/codexcatalog/   Codex へ渡す「手元のモデル定義」を同梱する版の埋め込み定義から生成する
                      （版を上げるたびに作り直す。`go run ./tools/codexcatalog -codex <実行ファイル>`）
tools/codexfetch/     同梱する Codex を取得して照合し universal binary に組み立てる
                      （`make codex`。版・取得元・ハッシュの正本は internal/aiprovider/adapter/codex/bundle.json。
                      **ネットワークが要るのは取得の 1 回だけ**で、以後 build/codex/ を使い回す）
tools/noticegen/      リポジトリ直上の NOTICE（第三者のライセンス文）を依存から作る。`-check` を make lint から実行
                      （依存を変えたら `go run ./tools/noticegen` で作り直してコミットする）
frontend/             React + Vite。wailsjs/ は Wails が生成する（手で編集しない）
```

## 検証ゲート

ゲートは 6 段で、この順に通す。リポジトリのルートからは `make -C app <段>`、`app/` の中では `make <段>` で実行する。

| 段 | 内容 |
|---|---|
| `build` | `npm ci` と `wails build`。`build/bin/ReqWeave.app` を作る |
| `lint` | `gofmt` / `go vet` / 依存規則（`tools/depcheck`）/ アイコンの同期（`tools/iconcheck`）/ 版番号の一致（`tools/versioncheck`）/ ライセンス文の一致（`tools/noticegen -check`）/ TypeScript の型検査 |
| `test-unit` | `go test ./...` と vitest |
| `test-layout` | 実ブラウザでのレイアウト検査 |
| `test-integration` | `go test -tags integration -skip <時間で判定するテスト> ./...` の後に、時間で判定するテストだけを `-p 1` で単独に回す（Makefile の `TIMED_TESTS`） |
| `test-e2e` | 配布物（`make dist`）を実際に起動する確認 |

- **初回は `make codex` が走る**（`build` / `build-universal` の依存）。同梱する Codex を
  公式のリリースから取得し、SHA-256 で照合して `build/codex/codex`（universal・約 400MB）を作る。
  2 回目以降は何もしない（ネットワーク不要）。取得物は `build/codex/` に置き、git では追跡しない。
- 同梱により、配布物は 37.8MB → 215MB、アプリ本体は約 25MB → 444MB になる（2026-09-14 実測）。
  **初回起動だけ OS の走査で遅くなる**（6.937 秒。2 回目以降は 0.27 秒）ため、e2e の起動時間の基準は
  初回 15 秒・2 回目以降 5 秒に分けてある。
- `test-layout`: **実ブラウザ**（vitest browser mode + Playwright/Chromium）で描いて寸法を見る。
  jsdom はレイアウトを計算しないため、横のはみ出し・縦の押し出しは単体テストでは検出できない。対象は `frontend/src/**/*.layout.test.tsx` の数本のみ。
  **初回のみブラウザの取得が要る**: `cd frontend && npx playwright install chromium`（約 94MB）。
  無い環境では失敗する（黙って緑にしない）。**配布物には含まれない開発時のみの依存**。
- `test-integration`: `//go:build integration` のテスト。モックを使わず実ファイル・ビルド成果物を対象にする。
- `test-e2e`: ビルド済み配布物を実際に起動し、画面（DOM）到達までを確認する。
  GUI セッション上で実行すること（ヘッドレス環境では起動しない）。判定は起動マーカー
  `binding.ReadyMarker` の標準出力による。
- 実コマンドの正本は `Makefile`。

## 開発

```bash
make dev        # ホットリロード付き開発サーバ（wails dev）
make generate   # Go 側の公開バインディングを frontend/wailsjs/ へ再生成
```
