import {useCallback, useEffect, useState} from 'react'
import {Button, Markdown, Wizard} from '../ui'
import './Intro.css'

/*
 * 紹介スライド。
 *
 * 初回起動時に初期設定ウィザードより前に自動表示し、設定から再表示できる。
 * 画面型はウィザード・回答系。**設定値を登録しない**点で初期設定ウィザードと別画面。
 *
 * AI 呼び出し・ネットワーク通信・プロジェクトデータの読み書きを行わない（初回起動の直後、設定の前でも読めるように）。
 * 本文は Markdown、図は mermaid で、成果物プレビューと同じ描画経路を使う
 * （ライブラリは同梱済み。閲覧中に外部から読み込まない）。
 *
 * 文言の規約: 内部用語・生のコード値・git の語を出さない。
 * 端末間の排他は「予約」、同一端末の保護のみ「ロック」と書く（用語集）。
 */

type Page = {title: string; body: string}

export const INTRO_PAGES: Page[] = [
    {
        title: 'ReqWeave へようこそ',
        body: `本システムは、AI エージェントとの対話で**要件定義と基本設計**を進め、
開発 AI（Claude Code 等）へそのまま渡せる精度の成果物を作るためのアプリです。

この案内では、次の 4 つを順に説明します。

- できること
- はじめに決めること（初期設定）
- チームでの進めかた
- ファイルの扱い

「スキップ」でいつでも飛ばせます。あとから設定画面の「紹介スライドをもう一度見る」で読み直せます。`,
    },
    {
        title: 'できること',
        body: `- **対話で要件を引き出す** — 質問に答えていくと、決定事項・未決事項・要件項目が整理されていきます。
- **わからないことを関係者へ聞く** — 未決事項から質問票を作り、ファイルで渡して回答を取り込めます。
- **成果物ドキュメントを作る** — 整理した内容から要件定義書を生成し、版を確定できます。
- **基本設計へ進む** — 要件を確定すると、同じ対話の形で基本設計を進められます。
- **開発 AI へ渡す** — 成果物一式を、開発 AI が読める形で書き出せます。

\`\`\`mermaid
flowchart LR
    A["対話"] --> B["決定・未決事項・要件項目の整理"]
    B --> C["要件定義書の生成と確定"]
    C --> D["基本設計"]
    D --> E["開発 AI 向けの書き出し"]
    B -. わからないこと .-> Q["質問票"]
    Q -. 回答 .-> B
\`\`\``,
    },
    {
        title: 'はじめに決めること',
        body: `次の画面で、次の項目を登録します。あとから設定画面で変更できます。

| 項目 | 内容 |
| --- | --- |
| AI プロバイダ | 次の画面に出る一覧から選びます |
| シークレットキー | 選んだプロバイダのキーを登録します |
| モデル | 対話に使うモデルを選びます |
| エフォート | 掘り下げの深さを 低 / 標準 / 高 から選びます（既定は標準） |
| 作業者名 | メールアドレス（利用者 ID）と表示名を登録します |

- **シークレットキーは、お使いの OS のセキュアストレージにだけ保管します。** 設定ファイルにも成果物にも書き込まれません。登録したあとに元の文字列を画面へ戻すことはできません。
- **メールアドレス（利用者 ID）は、登録したあとご自身では変更できません。** 複数人で進めるときに「誰の作業か」を記録するために使います（訂正が要るときはプロジェクトのオーナーが行います）。
- エフォートは AI の使用量に影響します。迷ったら「標準」のままで始めてください。`,
    },
    {
        title: 'チームでの進めかた',
        body: `複数人で 1 つのプロジェクトを進めるときの流れです。一人で使うときは、この手順は要りません。

\`\`\`mermaid
flowchart LR
    J["参加する"] --> W["自分の作業コピーで作業する"]
    W --> I["取り込む（ほかの人の変更を取り入れる）"]
    I --> P["反映する（自分の変更を送り出す）"]
    P --> W
\`\`\`

- 作業はいつも **自分の端末にある「作業コピー」** に対して行います。**同期先へつながらない場所でも、参照・対話・要件の追加・版の確定まですべてできます。**
- **取り込みと反映をしない限り、自分の変更は相手に見えず、相手の変更も自分に入りません。**
- 作業を始める前に**取り込み**、区切りがついたら**反映する** — これを日常の習慣にしてください。間隔が空くほど、あとで突き合わせる量が増えます。`,
    },
    {
        title: 'ファイルの扱い',
        body: `\`\`\`mermaid
flowchart LR
    MY["自分の作業コピー<br/>（自分の端末）"] -- 反映 --> B1["自分のぶんの置き場"]
    B2["ほかの人のぶんの置き場"] -- 取り込み --> MY
    subgraph SYNC["同期先"]
      B1
      B2
    end
\`\`\`

- **作業コピー**はプロジェクトの完全な写しです。反映すると、同期先の **自分のぶんの置き場** へ送られます。ほかの人の変更との突き合わせは、**取り込む側**で行います。
- **予約**は「この範囲は自分が進めます」という宣言です。**同期先へ反映して、相手が取り込んではじめて相手に伝わります。** 相手の編集を機械的に止めるものではありません。
- **「ロック」は、同じ端末で同じプロジェクトを別のウィンドウから開いたときの保護だけ**を指します。人と人の間の調整は「予約」です。
- 同じところを二人が変えていたときは、**もとの内容・相手の内容・自分の内容の 3 つを並べて表示します。どれを採るかをご自身が選んで承認するまで、勝手に上書きされることはありません。**`,
    },
]

/**
 * @param onFinish 表示を終えたとき（最後まで送った・スキップした・再表示を閉じた）に呼ぶ。
 * @param reviewing 設定画面からの再表示なら true。初回の自動表示と文言・最終ボタンを変える。
 */
export function Intro({onFinish, reviewing = false}: {onFinish: () => void; reviewing?: boolean}) {
    const [page, setPage] = useState(0)
    const last = page === INTRO_PAGES.length - 1

    const back = useCallback(() => setPage((n) => Math.max(0, n - 1)), [])
    const next = useCallback(() => {
        setPage((n) => {
            if (n < INTRO_PAGES.length - 1) {
                return n + 1
            }
            onFinish()
            return n
        })
    }, [onFinish])

    // キーボード操作。ネイティブダイアログは使わない（テーマに追従せず、自動テストもできないため）。
    useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'ArrowRight') {
                next()
            } else if (event.key === 'ArrowLeft') {
                back()
            } else if (event.key === 'Escape') {
                onFinish()
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [back, next, onFinish])

    const current = INTRO_PAGES[page]
    return (
        <Wizard
            title={current.title}
            step={page + 1}
            total={INTRO_PAGES.length}
            onBack={page === 0 ? undefined : back}
            onNext={next}
            nextLabel={last ? (reviewing ? '閉じる' : 'はじめる') : '次へ'}
            extraAction={
                last ? undefined : (
                    <Button variant="quiet" onClick={onFinish}>
                        スキップ
                    </Button>
                )
            }
            persistenceNotice={
                reviewing
                    ? '設定画面へ戻ります。初期設定の内容は変わりません。'
                    : 'この案内はあとから設定画面の「紹介スライドをもう一度見る」で読み直せます。'
            }
        >
            <div className="rw-intro">
                <Markdown source={current.body} />
            </div>
        </Wizard>
    )
}
