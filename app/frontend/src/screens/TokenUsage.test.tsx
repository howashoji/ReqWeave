import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {TokenUsage} from './TokenUsage'

const tokenUsage = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    TokenUsage: tokenUsage,
}))

const VIEW = {
    path: '/projects/inventory',
    targetSystemName: '在庫管理システム',
    tokens: 123456,
    tokensIn: 100000,
    tokensOut: 20000,
    tokensReasoning: 3456,
    sends: 42,
    missingRecords: 2,
    lastUsedAt: '2026-08-20T01:30:00Z',
    limitTokens: 200000,
    consumptionRatio: 0.61728,
    byProvider: [
        {key: 'anthropic', tokens: 100000, sends: 30, missing: 1},
        {key: 'openai', tokens: 23456, sends: 12, missing: 1},
    ],
    bySession: [
        {key: 'S-0001', tokens: 100000, sends: 30, missing: 0},
        {key: '', tokens: 23456, sends: 12, missing: 2},
    ],
    latest: {
        at: '2026-08-20T01:30:00Z',
        provider: 'openai',
        model: 'gpt-5',
        session: 'S-0002',
        hasTokens: true,
        tokensIn: 800,
        tokensOut: 200,
        tokensReasoning: 50,
        tokensTotal: 1050,
    },
}

describe('トークン消費実績', () => {
    beforeEach(() => {
        tokenUsage.mockReset().mockResolvedValue(VIEW)
    })

    // プロジェクト単位・対話セッション単位・プロバイダ別の累計を表示する。
    it('プロジェクト・プロバイダ別・対話セッション別の累計を表示する', async () => {
        render(<TokenUsage onBack={() => undefined} onOpenDashboard={() => undefined} />)

        await waitFor(() => expect(tokenUsage).toHaveBeenCalledWith({path: ''}))
        const totals = await screen.findByLabelText('プロジェクトの累計')
        expect(totals.textContent).toContain('在庫管理システム')
        expect(totals.textContent).toContain('123,456')
        expect(totals.textContent).toContain('200,000 / 61.7%')

        const providers = screen.getByRole('table', {name: 'プロバイダ別の累計'})
        expect(providers.textContent).toContain('anthropic')
        expect(providers.textContent).toContain('openai')
        const sessions = screen.getByRole('table', {name: '対話セッション別の累計'})
        expect(sessions.textContent).toContain('S-0001')
        // セッション文脈のない送信は生の空値ではなくラベルで示す（利用者に生の値を見せない）。
        expect(sessions.textContent).toContain('対話セッション外（取り込み分析など）')
    })

    // 直近 1 件は API 応答の実績値を表示する（推定値で代用しない）。
    it('直近の対話 1 件の消費値を表示する', async () => {
        render(<TokenUsage onBack={() => undefined} onOpenDashboard={() => undefined} />)

        const latest = await screen.findByLabelText('直近の対話 1 件')
        expect(latest.textContent).toContain('openai / gpt-5')
        expect(latest.textContent).toContain('S-0002')
        expect(latest.textContent).toContain('1,050')
        expect(latest.textContent).toContain('入力 800 / 出力 200 / 推論 50')
    })

    // 実績が欠測の送信は「実績なし」と示し、0 と区別する。
    it('直近 1 件の実績が欠測なら「実績なし」と示す（0 と区別する）', async () => {
        tokenUsage.mockResolvedValue({
            ...VIEW,
            latest: {...VIEW.latest, hasTokens: false, tokensIn: 0, tokensOut: 0, tokensReasoning: 0, tokensTotal: 0},
        })
        render(<TokenUsage onBack={() => undefined} onOpenDashboard={() => undefined} />)

        const latest = await screen.findByLabelText('直近の対話 1 件')
        expect(within(latest).getByText('実績なし')).toBeTruthy()
        // 欠測を 0 として表示しない。
        expect(latest.textContent).not.toContain('入力 0 / 出力 0')
    })

    it('AI をまだ利用していないときはその旨を示す', async () => {
        tokenUsage.mockResolvedValue({...VIEW, tokens: 0, sends: 0, missingRecords: 0, byProvider: [],
            bySession: [], latest: null, lastUsedAt: '', limitTokens: null, consumptionRatio: null})
        render(<TokenUsage onBack={() => undefined} onOpenDashboard={() => undefined} />)

        expect(await screen.findByText('このプロジェクトではまだ AI を利用していません。')).toBeTruthy()
        expect(screen.getAllByText('AI の送信はまだありません。').length).toBe(2)
    })

    // 期間別・作業者別・横断は AI 利用量ダッシュボードへ渡す。
    it('AI 利用量ダッシュボードへの導線がある', async () => {
        const onOpenDashboard = vi.fn()
        render(<TokenUsage onBack={() => undefined} onOpenDashboard={onOpenDashboard} />)
        await screen.findByLabelText('プロジェクトの累計')

        fireEvent.click(screen.getByRole('button', {name: '期間別・横断で見る（AI 利用量）'}))
        expect(onOpenDashboard).toHaveBeenCalledTimes(1)
    })

    it('対象プロジェクトを指定して参照できる', async () => {
        render(
            <TokenUsage
                projectPath="/projects/inventory"
                onBack={() => undefined}
                onOpenDashboard={() => undefined}
            />,
        )
        await waitFor(() => expect(tokenUsage).toHaveBeenCalledWith({path: '/projects/inventory'}))
    })

    it('取得に失敗したときは理由を表示する', async () => {
        tokenUsage.mockRejectedValue(
            new Error('このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。'),
        )
        render(<TokenUsage onBack={() => undefined} onOpenDashboard={() => undefined} />)

        expect(await screen.findByText(/メンバーに登録されていません/)).toBeTruthy()
        expect(screen.queryByLabelText('プロジェクトの累計')).toBeNull()
    })

    // 共同プロジェクトでは集計範囲を併記する。
    it('集計範囲の併記を表示する', async () => {
        tokenUsage.mockResolvedValue({
            ...VIEW,
            scopeNotice:
                '2026-09-02 10:00 に取り込んだ時点までの、全メンバーの利用量を集計しています。それ以降に他のメンバーが消費したぶんは、次に取り込むまで含まれません。',
            lastIncorporation: '2026-09-02 10:00',
        })
        render(<TokenUsage projectPath="" onBack={() => undefined} onOpenDashboard={() => undefined} />)
        expect(await screen.findByText(/取り込んだ時点までの、全メンバーの利用量を集計しています/)).toBeInTheDocument()
    })

    // 単独利用のプロジェクトでは併記しない（範囲の限定が起きないため）。
    it('単独利用のプロジェクトでは集計範囲を併記しない', async () => {
        render(<TokenUsage projectPath="" onBack={() => undefined} onOpenDashboard={() => undefined} />)
        await screen.findByLabelText('プロジェクトの累計')
        expect(screen.queryByText(/取り込んだ時点までの/)).not.toBeInTheDocument()
    })
})
