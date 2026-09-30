# 利用手引き

> 本システムを**使う人**（システム担当者）と、その利用組織へ向けた手引き。
> 画面の操作で完結しないこと、つまり「**利用者と利用組織が何をするか**」を書く。
>
> 初回起動時に OS が出す警告の回避手順は配布物に同梱の `初回起動の手順.md` を見る（本群には載せていない）。

## アプリの入手先

**[GitHub Releases](https://github.com/howashoji/ReqWeave/releases/latest)**。
初回導入も更新も同じ場所から配られる。導入と初回起動の操作は配布物に同梱の `初回起動の手順.md` にある。
Windows で導入できる端末の条件は [windows-support.md](windows-support.md) を参照。

## 共同プロジェクトを使うなら、端末に git が要ります

**同期先を設定したプロジェクト**（複数人で 1 つのプロジェクトを進める使い方）では、
本システムは端末にインストールされている **git** を使って取り込み・反映を行います。
**git がインストールされていない端末では同期の操作だけができません**（アプリの他の機能は普通に使えます）。

| OS | 入手 |
|---|---|
| macOS | ターミナルで `git --version` を実行すると、未導入なら Xcode Command Line Tools の導入が案内されます。**管理者権限は要りません** |
| Windows | [Git for Windows](https://gitforwindows.org/) を導入します。インストール時に「現在のユーザーのみ」を選べば**管理者権限は要りません** |

**単独で使う場合（同期先を設定しないプロジェクト）は git は不要**です。要件・対話・成果物の生成・
エクスポート・受け渡しファイルのやり取りは、すべて git なしで動きます。

> 同期先が設定されているのに git が見つからないときは、アプリが「同期を利用できない理由と入手先」を
> 画面に示します。**作業そのものは止まりません**（その間の変更は git をインストールしたあとにまとめて反映できます）。

## 対象読者

**システム担当者**（本システムでプロジェクトを進める人）と、その**利用組織の管理者**。
ステークホルダー（回答モードの利用者）は対象外。回答モードの操作は画面内の案内で完結する。

## 構成

| 文書 | 内容 |
|---|---|
| [data-and-git.md](data-and-git.md) | プロジェクトデータの置き場所と、利用者自身の git 操作の可否 |
| [access-and-protection.md](access-and-protection.md) | 利用者 ID の前提（共用アカウントで使わない）・アプリの権限判定の限界・端末側と同期先側の保護 |
| [sync-routine.md](sync-routine.md) | 取り込みと反映の頻度の目安・同期先へ届かないときの進め方 |
| [windows-support.md](windows-support.md) | Windows で導入できる端末の条件（Smart App Control / SmartScreen ポリシー）と macOS との違い |
| [exchange-files.md](exchange-files.md) | 受け渡しファイル（`.rwvq` / `.rwva`）を開いたときの行き先と OS ごとの違い・回答モードの診断情報 |
| [codex-app-server.md](codex-app-server.md) | Codex App Server の選び方・2 つの認証方式・学習利用の停止・付け足される送信内容・プランの残量・配布物の大きさと初回起動 |

## この手引きで扱うこと

利用組織が判断を誤ると、本システムの側では補えないことがある。その事項と、書いてある場所の一覧。

| 扱うこと | 書いた場所 |
|---|---|
| 共用の OS アカウントで共同プロジェクトを使わないこと（利用者 ID の前提） | [access-and-protection.md](access-and-protection.md) 1 |
| アプリの権限判定でできること・できないこと | [access-and-protection.md](access-and-protection.md) 2 |
| 端末側の保護と同期先側のアクセス制御の併用 | [access-and-protection.md](access-and-protection.md) 3 |
| 取り込みと反映の頻度 | [sync-routine.md](sync-routine.md) |
| 署名なしの Windows 版を導入できる端末の範囲 | [windows-support.md](windows-support.md) |
| 担当者の作業中に受け渡しファイルを開いたときの動き（OS ごとの差） | [exchange-files.md](exchange-files.md) |
| 個人向けプランでの学習利用の停止方法と、組織での利用に勧める方式 | [codex-app-server.md](codex-app-server.md) 3・4 |
| ChatGPT のサインインだけに頼らない運用（切り替え先を常に持つ） | [codex-app-server.md](codex-app-server.md) 7 |
| Codex の同梱による配布物の大きさと、初回起動に数秒かかること | [codex-app-server.md](codex-app-server.md) 9 |
| Codex が送信に付け足す内容と、動作ログに残る応答のクッキー | [codex-app-server.md](codex-app-server.md) 5 |
| Codex App Server を選べるのは macOS 版だけであること | [codex-app-server.md](codex-app-server.md) 1 |

> Windows でのファイルの関連付けの登録は実機での確認が済んでいない。
> ダブルクリックで開けないときの回避策は [exchange-files.md](exchange-files.md) 4 にある。
