import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {GuideStrip} from './GuideStrip'

/*
 * 工程ガイド。
 *
 * 利用者報告「この画面からの業務と操作手順が全く見当がつかない」への対応。
 * ここでは「いまの工程」と「次にやること」が出ること、そして
 * **画面の中でやることがあるときはそちらを優先する**ことを確かめる。
 */

const BASE = {
    stageLabel: '対話で要件を詰める',
    stageIndex: 2,
    stageTotal: 6,
    next: '対話を続けて、まだ埋まっていない章を詰めます。',
}

describe('GuideStrip — 工程ガイド', () => {
    it('いまの工程を「何合目か」の数と名前で出す', () => {
        render(<GuideStrip {...BASE} />)
        expect(screen.getByText('2/6')).toBeInTheDocument()
        expect(screen.getByText('対話で要件を詰める')).toBeInTheDocument()
        expect(screen.getByText(BASE.next)).toBeInTheDocument()
    })

    it('次の工程が別の画面にあるときは、そこへ移る操作を押せる形で出す', () => {
        const onClick = vi.fn()
        render(<GuideStrip {...BASE} action={{label: '対話へ移る', onClick}} />)
        fireEvent.click(screen.getByRole('button', {name: '対話へ移る'}))
        expect(onClick).toHaveBeenCalledTimes(1)
    })

    it('ほかにも取れる道は補足として添える', () => {
        const onClick = vi.fn()
        render(
            <GuideStrip
                {...BASE}
                note="自分では決められない未決事項は、質問票にして関係者へ聞けます。"
                noteAction={{label: '質問票を開く', onClick}}
            />,
        )
        expect(screen.getByText(/質問票にして関係者へ聞けます/)).toBeInTheDocument()
        fireEvent.click(screen.getByRole('button', {name: '質問票を開く'}))
        expect(onClick).toHaveBeenCalledTimes(1)
    })

    /*
     * いま開いている画面でやることがあるなら、そこから目を離させない。
     * 別画面への誘いを同時に出すと、手を動かす先が 2 つになって迷う。
     * ただし「ほかにも取れる道」は引っ込めない（引っ込めるとその道が画面から消える）。
     */
    it('画面の中でやることがあるときは、それを出して別画面への誘いを引っ込める', () => {
        render(
            <GuideStrip
                {...BASE}
                hint="中央の「上の内容を AI プロバイダへ送信することに同意します」にチェックを入れます。"
                action={{label: '対話へ移る', onClick: vi.fn()}}
                note="質問票にして関係者へ聞けます。"
                noteAction={{label: '質問票を開く', onClick: vi.fn()}}
            />,
        )
        expect(screen.getByText(/「上の内容を AI プロバイダへ送信することに同意します」/)).toBeInTheDocument()
        expect(screen.queryByText(BASE.next)).toBeNull()
        expect(screen.queryByRole('button', {name: '対話へ移る'})).toBeNull()
        // 「関係者に聞く」道は残す（ここにしか導線が無い）。
        expect(screen.getByRole('button', {name: '質問票を開く'})).toBeInTheDocument()
        // 工程そのものは出したままにする（いま何をしている最中かは見えている）。
        expect(screen.getByText('対話で要件を詰める')).toBeInTheDocument()
    })
})

/*
 * 工程の全体像。
 *
 * 工程ガイドはいまの 1 段しか示さないため、「この先に何があるのか」が見えなかった。
 * ここでは **1 操作で全体像が開くこと**・**現在地が分かること**・**各工程の画面へ移れること**を確かめる。
 * 工程の説明そのものはバックエンドが持つ（写しの検査は internal/guide のテスト）。
 */
const STAGES = [
    {
        id: 'imports',
        label: '資料を取り込む',
        purpose: '手元にある既存の資料を読み込ませ、そこから決定事項や要件の候補を出します。',
        screen: '資料取込',
        target: 'imports',
    },
    {
        id: 'dialogue',
        label: '対話で要件を詰める',
        purpose: 'AI の質問に答えていき、決定事項・未決事項・要件項目を書き出していきます。',
        screen: '対話',
        target: 'dialogue',
    },
    {
        id: 'confirm',
        label: '要件定義を確定する',
        purpose: '確定前の確認結果を見て、内容に合意できたら版として確定します。',
        screen: '要件定義書',
        target: 'documents',
    },
]

describe('GuideStrip — 工程の全体像', () => {
    it('工程の並びを渡していないときは、全体像を開く操作を出さない', () => {
        render(<GuideStrip {...BASE} />)
        expect(screen.queryByRole('button', {name: '工程の全体像'})).toBeNull()
    })

    it('1 操作で全体像が開き、各工程の目的と使う画面が並ぶ', () => {
        render(<GuideStrip {...BASE} stages={STAGES} stageId="dialogue" onMove={() => vi.fn()} />)
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        // 工程名はストリップ側にも出るため、全体像の中だけを見る。
        const overview = within(screen.getByRole('group', {name: '工程の全体像'}))
        for (const stage of STAGES) {
            expect(overview.getByText(stage.label)).toBeInTheDocument()
            expect(overview.getByText(stage.purpose)).toBeInTheDocument()
            expect(overview.getByText(`使う画面: ${stage.screen}`)).toBeInTheDocument()
        }
    })

    it('現在地を色だけでなく語で示す', () => {
        render(<GuideStrip {...BASE} stages={STAGES} stageId="dialogue" onMove={() => vi.fn()} />)
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        const here = screen.getByText('（いまここ）')
        expect(here).toBeInTheDocument()
        // 印が付くのは 1 行だけ（いまの工程の行）。
        expect(here.closest('li')).toHaveTextContent('対話で要件を詰める')
        expect(screen.getAllByText('（いまここ）')).toHaveLength(1)
    })

    it('工程の画面へ移れる。移ったら全体像は閉じる（一覧が手元に残らない）', () => {
        const moved = vi.fn()
        render(
            <GuideStrip {...BASE} stages={STAGES} stageId="dialogue" onMove={(target) => () => moved(target)} />,
        )
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        fireEvent.click(screen.getByRole('button', {name: '資料取込を開く'}))
        expect(moved).toHaveBeenCalledWith('imports')
        expect(screen.queryByText('使う画面: 資料取込')).toBeNull()
    })

    it('移れない行き先には操作を出さない（押しても何も起きないボタンを作らない）', () => {
        render(
            <GuideStrip
                {...BASE}
                stages={STAGES}
                stageId="dialogue"
                onMove={(target) => (target === 'imports' ? vi.fn() : undefined)}
            />,
        )
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        const overview = within(screen.getByRole('group', {name: '工程の全体像'}))
        expect(overview.getByRole('button', {name: '資料取込を開く'})).toBeInTheDocument()
        expect(overview.queryByRole('button', {name: '対話を開く'})).toBeNull()
        // 行そのものは出す（何をする工程かは読める）。
        expect(overview.getByText('対話で要件を詰める')).toBeInTheDocument()
    })

    it('Esc で閉じられる（ネイティブのダイアログを使わない）', () => {
        render(<GuideStrip {...BASE} stages={STAGES} stageId="dialogue" onMove={() => vi.fn()} />)
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        expect(screen.getByText('使う画面: 対話')).toBeInTheDocument()
        fireEvent.keyDown(window, {key: 'Escape'})
        expect(screen.queryByText('使う画面: 対話')).toBeNull()
    })

    // 以前は開いた瞬間に作業領域が押し下げられ、ウィンドウを広げないと見えなかった。
    it('外側を押しても閉じる（読み終えて作業へ戻る道を「閉じる」だけにしない）', () => {
        const {container} = render(
            <GuideStrip {...BASE} stages={STAGES} stageId="dialogue" onMove={() => vi.fn()} />,
        )
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        const outside = container.querySelector('.rw-overlay__outside')
        expect(outside, '外側を押すための面が無い').not.toBeNull()
        fireEvent.click(outside as Element)
        expect(screen.queryByRole('group', {name: '工程の全体像'})).toBeNull()
    })

    it('順序を強制しないことを明記する（上から順に進める決まりだと読ませない）', () => {
        render(<GuideStrip {...BASE} stages={STAGES} stageId="dialogue" onMove={() => vi.fn()} />)
        fireEvent.click(screen.getByRole('button', {name: '工程の全体像'}))
        expect(screen.getByText(/上から順に進める決まりはありません/)).toBeInTheDocument()
    })
})
