# ReqWeave（レクウィーブ）

AI と対話しながら要件定義書と基本設計書をつくるデスクトップアプリです。
macOS と Windows 11 で動きます。

要件定義は聞くことと書き残すことの繰り返しです。何を聞いたか、誰が何を決めたか、どこがまだ決まっていないか。
これを人の記憶と議事録に頼ると、聞き漏れや「決めたはず」がどうしても出てきます。

ReqWeave では AI が質問を組み立て、答えから決定事項と未決事項を拾って文書に落とします。
担当者の仕事は、答えて、確かめて、直すこと。

できあがった文書は Claude Code などの開発 AI にそのまま渡せる形で書き出せます。

## できること

- **対話で要件を詰める。** AI が次に聞くべきことを質問し、答えから決定事項・未決事項の候補を抜き出します。候補は担当者が直してから確定します。
- **答えられない質問は関係者に回す。** 質問票をファイルで渡すと、相手は同じアプリの「回答モード」で答えます。返ってきた回答は、何が変わるかを確かめてから取り込めます。
- **要件定義書と基本設計書を生成する。** 用語集も対話の中で育っていきます。版を残し、変わったところだけを作り直せます。
- **開発 AI 向けに書き出す。** 成果物一式に、読む順番や ID の決まりを書いた導入ファイル（CLAUDE.md に当たるもの）を付けて出力します。

このほか既存資料の取り込み、進捗レポート、開発 AI からの指摘の取り込み、複数人での共同作業（git のリポジトリを同期先にする）、AI の利用量の集計もあります。

## 使える AI

Anthropic・OpenAI・Google の API に対応。それぞれのシークレットキーを登録して使います。
キーは OS のキーチェーン（Windows は資格情報マネージャー）に保存し、ファイルやログには書きません。

macOS 版では OpenAI の Codex App Server も選べます。こちらはシークレットキーのほか ChatGPT のアカウントでのサインインでも使えます。
Codex は配布物に同梱していて、応答の生成にだけ使います。コマンドの実行やファイルの書き換えはさせません。

## 入手する

配布物はこのリポジトリの [Releases](https://github.com/howashoji/ReqWeave/releases/latest) に置いています。
アプリの自動更新も同じ場所から取りに行きます。

| OS | ファイル | 初回の起動 |
| -- | -- | -- |
| macOS（Apple Silicon・Intel） | `ReqWeave-<版>-macos.dmg` | 署名と Apple の公証を受けています。確認のダイアログが 1 回出るだけです |
| Windows 11（64bit） | `ReqWeave-<版>-windows.zip` | 署名はありません。SmartScreen の警告が出たら「詳細情報」から「実行」を選びます |

どちらも管理者権限なしでインストールできます。手順は配布物に同梱の `初回起動の手順.md` にあります。
macOS では `ReqWeave.app` をホームフォルダの「アプリケーション」に置いてください。Mac 全体の `/Applications` に置くと、自動更新ができません。

Windows で Smart App Control が有効な端末にはインストールできません。条件は [Windows で導入できる条件](docs/user-guide/windows-support.md) にまとめています。

## 使い方の手引き

[docs/user-guide](docs/user-guide/README.md) にあります。データの置き場所、共同作業の進め方、受け渡しファイルの扱い、Codex App Server の使い方など。

## ソースからビルドする

macOS で開発しています。必要なのは Go 1.27 以降、Node.js 24 以降、[Wails CLI](https://wails.io/) v2.15.0 です。

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
make -C app build     # app/build/bin/ReqWeave.app ができる
make -C app dev       # 開発モードで起動する
```

初回の `make -C app build` は同梱する Codex を公式のリリースから取ってきます（約 400MB）。2 回目からは手元のものを使うので通信はしません。

テストの段の一覧と、それぞれに要る環境は [CONTRIBUTING.md](CONTRIBUTING.md) に書いています。

## 不具合の報告と要望

Issues へどうぞ。セキュリティの問題だけは Issues に書かず、[SECURITY.md](SECURITY.md) の方法で知らせてください。

版ごとの変更は [CHANGELOG.md](CHANGELOG.md) にまとめています。

## ライセンス

[MIT License](LICENSE)。Copyright (c) 2026 HOWA SHOJI K.K.

使っているライブラリとフォントのライセンスはまとめて [NOTICE](NOTICE) に載せました。
