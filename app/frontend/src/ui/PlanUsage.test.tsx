import {render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {PlanUsage} from './PlanUsage'

/*
 * ChatGPT のプランの残量。
 *
 *   - サインインしているときだけ出す（シークレットキー方式では欄ごと出さない）
 *   - 枠ごとの使用率・枠の長さ・次の回復時刻と、受け取った時刻を出す
 *   - まだ受け取っていないときは黙って空欄にせず、その旨と次の行動を出す
 *   - **表示のための通信をしない**（保持している値を返す口だけを呼ぶ）
 */

const codexPlanUsage = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({CodexPlanUsage: codexPlanUsage}))

// 受け取った時刻・回復時刻は UTC の ISO 8601 で届き、表示はローカル（JST 固定 = vite.config.ts）。
const USAGE = {
    available: true,
    fetched: true,
    planLabel: 'Plus',
    receivedAt: '2026-09-15T09:30:00Z',
    windows: [
        {
            label: '5 時間の利用枠',
            usedPercent: 42,
            windowDurationMins: 300,
            windowLabel: '5 時間',
            resetsAt: '2026-09-15T23:00:00Z',
        },
        {
            label: '週の利用枠',
            usedPercent: 8,
            windowDurationMins: 10080,
            windowLabel: '7 日',
            resetsAt: '2026-09-20T15:00:00Z',
        },
    ],
}

describe('ChatGPT のプランの残量', () => {
    beforeEach(() => {
        codexPlanUsage.mockReset().mockResolvedValue(USAGE)
    })

    it('枠ごとの使用率・枠の長さ・次の回復時刻と、受け取った時刻を表示する', async () => {
        render(<PlanUsage label="既定" />)

        const rows = await screen.findAllByRole('row')
        // 1 行目は見出し。枠は届いた順に出す。
        const first = within(rows[1])
        expect(first.getByText('5 時間の利用枠')).toBeInTheDocument()
        expect(first.getByText('42.0%')).toBeInTheDocument()
        expect(first.getByText('5 時間')).toBeInTheDocument()
        expect(first.getByText('2026-09-16 08:00')).toBeInTheDocument()

        const second = within(rows[2])
        expect(second.getByText('週の利用枠')).toBeInTheDocument()
        expect(second.getByText('8.0%')).toBeInTheDocument()
        expect(second.getByText('7 日')).toBeInTheDocument()

        expect(screen.getByText('Plus')).toBeInTheDocument()
        expect(screen.getByText('2026-09-15 18:30')).toBeInTheDocument()
        expect(codexPlanUsage).toHaveBeenCalledWith('既定')
    })

    // 閲覧の操作で通信を起こさない旨を画面でも示す
    it('直近の AI 呼び出しで受け取った値であり、開いても取りに行かないことを示す', async () => {
        render(<PlanUsage label="既定" />)

        expect(
            await screen.findByText(/この画面を開いても取りに行きません/),
        ).toBeInTheDocument()
    })

    // トークン上限の判定には使わない旨を添えられる
    it('画面ごとの補足（上限の判定に使わない旨）を添えられる', async () => {
        render(<PlanUsage label="" note="この残量はプロジェクトのトークン上限の判定には使いません。" />)

        expect(await screen.findByText(/トークン上限の判定には使いません/)).toBeInTheDocument()
    })

    // まだ一度も受け取っていないときの文言（黙って空欄にしない）
    it('まだ取得していないときは、その旨と次の行動を示す', async () => {
        codexPlanUsage.mockResolvedValue({
            available: true,
            fetched: false,
            notice: 'まだ取得していません（AI を使うと表示されます）',
        })
        render(<PlanUsage label="既定" />)

        expect(await screen.findByText(/まだ取得していません/)).toBeInTheDocument()
        expect(screen.getByText(/AI を使うと表示されます/)).toBeInTheDocument()
        expect(screen.queryByRole('row')).toBeNull()
    })

    // シークレットキー方式では表示しない（値が届かないため）
    it('サインイン方式でないときは残量の欄ごと出さない', async () => {
        codexPlanUsage.mockResolvedValue({available: false, fetched: false})
        render(<PlanUsage label="既定" />)

        // 取得の解決を待ってから不在を確かめる（描画前に見て「無い」と判定しないため）
        await waitFor(() => expect(codexPlanUsage).toHaveBeenCalled())
        expect(screen.queryByLabelText('ChatGPT のプランの残量')).toBeNull()
    })

    // 取得できなくても画面の他の機能を止めない（残量は参考情報）
    it('残量を取得できないときは何も出さず、画面を止めない', async () => {
        codexPlanUsage.mockRejectedValue(new Error('unavailable'))
        render(<PlanUsage label="既定" />)

        await waitFor(() => expect(codexPlanUsage).toHaveBeenCalled())
        expect(screen.queryByLabelText('ChatGPT のプランの残量')).toBeNull()
    })

    /*
     * 「閲覧で通信を起こさない」の構造的な担保: 画面が呼ぶのは「保持している値を返す口」だけ。
     * 呼び出し回数を数えるのではなく、**取得を起こす口への経路を持たない**ことを固定する
     * （経路が無ければ、あとから誤って呼ばれることもない）。
     */
    it('残量の取得を起こす口（CodexAccount）を呼ぶ経路を持たない', async () => {
        const source = (await import('./PlanUsage.tsx?raw')).default
        // 検出したい語は**分割して組み立てる**。この検査自体もフロントエンドのソースであり、
        // 直接通信の依存規則検査（tools/depcheck の frontend-direct-io）の対象に
        // 入るため、そのままの並びで書くと検査自身に当たって赤くなる。
        const directCall = 'fetch' + '('
        expect(source).toContain('CodexPlanUsage')
        expect(source).not.toContain('CodexAccount')
        expect(source).not.toContain(directCall)
    })
})
