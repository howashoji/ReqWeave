import {fireEvent, render, screen} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {describe, expect, it, vi} from 'vitest'
import {Intro, INTRO_PAGES} from './Intro'

// 図の配色（テーマ追従）と mermaid の初期化条件は Markdown 側の責務（Markdown.test.tsx）。
// ここでは「図が同梱の描画経路に載っていること」と画面の操作を確かめる。

/** 「次へ」を n 回押して n ページ進む。 */
function forward(times: number) {
    for (let i = 0; i < times; i += 1) {
        fireEvent.click(screen.getByRole('button', {name: '次へ'}))
    }
}

describe('紹介スライド', () => {
    it('4 区分（できること・はじめに決めること・チームでの進めかた・ファイルの扱い）を含む', () => {
        const titles = INTRO_PAGES.map((p) => p.title)
        expect(titles).toEqual([
            'ReqWeave へようこそ',
            'できること',
            'はじめに決めること',
            'チームでの進めかた',
            'ファイルの扱い',
        ])
    })

    it('「チームでの進めかた」と「ファイルの扱い」は図を含む', () => {
        for (const title of ['チームでの進めかた', 'ファイルの扱い']) {
            const page = INTRO_PAGES.find((p) => p.title === title)
            expect(page?.body).toContain('```mermaid')
        }
    })

    it('端末間の排他は「予約」と書き、「ロック」は同一端末の保護だけを指す（用語集の定義どおり）', () => {
        const files = INTRO_PAGES.find((p) => p.title === 'ファイルの扱い')?.body ?? ''
        expect(files).toContain('**予約**')
        expect(files).toMatch(/「ロック」は、同じ端末で同じプロジェクトを別のウィンドウから開いたときの保護だけ/)
    })

    it('予約が反映して初めて伝わること・同期しないと相手の変更が入らないことを述べる', () => {
        const files = INTRO_PAGES.find((p) => p.title === 'ファイルの扱い')?.body ?? ''
        const team = INTRO_PAGES.find((p) => p.title === 'チームでの進めかた')?.body ?? ''
        expect(files).toContain('同期先へ反映して、相手が取り込んではじめて相手に伝わります')
        expect(team).toContain('取り込みと反映をしない限り、自分の変更は相手に見えず、相手の変更も自分に入りません')
    })

    it('git の語・内部用語を出さない', () => {
        const all = INTRO_PAGES.map((p) => `${p.title}\n${p.body}`).join('\n')
        for (const word of ['git', 'クローン', 'プル', 'プッシュ', 'コンフリクト', 'マージ', 'ブランチ', 'リポジトリ']) {
            expect(all.toLowerCase()).not.toContain(word.toLowerCase())
        }
    })

    it('順に送ると最後のページで「はじめる」になり、押すと終了を通知する', () => {
        const onFinish = vi.fn()
        render(<Intro onFinish={onFinish} />)

        expect(screen.getByLabelText('進捗')).toHaveTextContent(`1 / ${INTRO_PAGES.length}`)
        forward(INTRO_PAGES.length - 1)
        expect(screen.getByLabelText('進捗')).toHaveTextContent(`${INTRO_PAGES.length} / ${INTRO_PAGES.length}`)
        expect(screen.queryByRole('button', {name: 'スキップ'})).toBeNull()

        fireEvent.click(screen.getByRole('button', {name: 'はじめる'}))
        expect(onFinish).toHaveBeenCalledTimes(1)
    })

    it('途中で「スキップ」を押しても終了を通知する', () => {
        const onFinish = vi.fn()
        render(<Intro onFinish={onFinish} />)

        forward(1)
        fireEvent.click(screen.getByRole('button', {name: 'スキップ'}))
        expect(onFinish).toHaveBeenCalledTimes(1)
    })

    it('1 ページ目は「戻る」を無効化し、理由を示す（ほかの画面の無効化と同じ方式）', () => {
        render(<Intro onFinish={vi.fn()} />)

        const back = screen.getByRole('button', {name: '戻る'})
        expectDisabledReason(back, '最初の項目のため戻れません。')
    })

    it('2 ページ目からは「戻る」で前のページへ戻れる', () => {
        render(<Intro onFinish={vi.fn()} />)

        forward(1)
        fireEvent.click(screen.getByRole('button', {name: '戻る'}))
        expect(screen.getByLabelText('進捗')).toHaveTextContent(`1 / ${INTRO_PAGES.length}`)
    })

    it('キーボードで移動・スキップできる（→ / ← / Esc）', () => {
        const onFinish = vi.fn()
        render(<Intro onFinish={onFinish} />)

        fireEvent.keyDown(window, {key: 'ArrowRight'})
        expect(screen.getByLabelText('進捗')).toHaveTextContent(`2 / ${INTRO_PAGES.length}`)
        fireEvent.keyDown(window, {key: 'ArrowLeft'})
        expect(screen.getByLabelText('進捗')).toHaveTextContent(`1 / ${INTRO_PAGES.length}`)
        fireEvent.keyDown(window, {key: 'Escape'})
        expect(onFinish).toHaveBeenCalledTimes(1)
    })

    it('再表示では最終ボタンが「閉じる」になり、設定へ戻る旨を示す', () => {
        render(<Intro onFinish={vi.fn()} reviewing />)

        expect(screen.getByText(/設定画面へ戻ります/)).toBeInTheDocument()
        forward(INTRO_PAGES.length - 1)
        expect(screen.getByRole('button', {name: '閉じる'})).toBeInTheDocument()
    })

    it('図は同梱の描画経路に載せ、外部へ通信しない', () => {
        // fetch が無い実行環境でも「呼ばれないこと」を確かめられるように、必ず差し替えてから描く。
        const fetchSpy = vi.fn()
        const original = (globalThis as {fetch?: unknown}).fetch
        ;(globalThis as {fetch?: unknown}).fetch = fetchSpy

        const {container} = render(<Intro onFinish={vi.fn()} />)
        forward(3) // 「チームでの進めかた」= 図のあるページ

        // 図は img/外部リソースではなく、同梱の描画経路（mermaid）の描画先として置かれる。
        expect(container.querySelector('.rw-markdown__mermaid')).not.toBeNull()
        expect(container.querySelector('img')).toBeNull()
        expect(fetchSpy).not.toHaveBeenCalled()
        ;(globalThis as {fetch?: unknown}).fetch = original
    })
})
