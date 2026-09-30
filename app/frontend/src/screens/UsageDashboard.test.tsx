import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {clickEnabled, clickEnabledBy, expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {UsageDashboard} from './UsageDashboard'

const usageDashboard = vi.hoisted(() => vi.fn())
const saveUsageReport = vi.hoisted(() => vi.fn())
const copyUsageReport = vi.hoisted(() => vi.fn())
const chooseDestination = vi.hoisted(() => vi.fn())
const currentPermission = vi.hoisted(() => vi.fn())
const codexPlanUsage = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    UsageDashboard: usageDashboard,
    SaveUsageReport: saveUsageReport,
    CopyUsageReport: copyUsageReport,
    ChooseUsageReportDestination: chooseDestination,
    CurrentPermission: currentPermission,
    CodexPlanUsage: codexPlanUsage,
}))

/** 横断一覧 2 行（集計できた 1 行・集計不能 1 行）。 */
const CROSS = {
    from: '2026-08-01',
    to: '2026-08-31',
    markdown: '# AI 利用量',
    projects: [
        {
            path: '/projects/inventory',
            projectId: 'PRJ-0001',
            targetSystemName: '在庫管理システム',
            aggregated: true,
            tokens: 123456,
            missingRecords: 2,
            lastUsedAt: '2026-08-20T01:30:00Z',
            limitTokens: 200000,
            consumptionRatio: 0.61728,
        },
        {
            path: '/mnt/share/hr',
            projectId: '',
            targetSystemName: '人事システム',
            aggregated: false,
            notice: '共有フォルダへ接続できません。ネットワーク接続を確認してください。',
            tokens: 0,
            missingRecords: 0,
            limitTokens: null,
            consumptionRatio: null,
        },
    ],
}

const DETAIL = {
    ...CROSS,
    detail: {
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
        byProvider: [{key: 'anthropic', tokens: 120000, sends: 40, missing: 1}],
        bySession: [
            {key: 'S-0001', tokens: 100000, sends: 30, missing: 0},
            {key: '', tokens: 23456, sends: 12, missing: 2},
        ],
        byAuthor: [{key: 'U-0001', tokens: 123456, sends: 42, missing: 2}],
        sessions: {
            from: '2026-08-01T00:00:00Z',
            to: '2026-08-31T23:59:59Z',
            sessionsTotal: 5,
            sessionsByPhase: [
                {phase: 'requirements', count: 4},
                {phase: 'basic-design', count: 1},
            ],
            questionnairesIssued: 3,
            questionnairesImported: 2,
            importedFlows: [],
            elapsedDaysAverage: 4.5,
            decisionsApproved: 7,
            openIssuesResolved: 3,
            requirementsConfirmed: 11,
        },
    },
}

const OWNER = {
    role: 'owner',
    roleLabel: 'オーナー',
    canEdit: true,
    canManageMembers: true,
    canManageUsageLimit: true,
    reason: '',
    manageReason: '',
    usageLimitReason: '',
}

const VIEWER = {
    role: 'viewer',
    roleLabel: '閲覧',
    canEdit: false,
    canManageMembers: false,
    canManageUsageLimit: false,
    reason: 'この操作は編集権限が必要です。オーナーに権限の変更を依頼してください。',
    manageReason: 'メンバー管理はオーナー権限が必要です。オーナーに依頼してください。',
    usageLimitReason: 'AI 利用量上限の設定はオーナー権限が必要です。オーナーに依頼してください。',
}

describe('AI 利用量ダッシュボード', () => {
    beforeEach(() => {
        usageDashboard.mockReset().mockResolvedValue(CROSS)
        saveUsageReport.mockReset().mockResolvedValue('/tmp/ai-usage.md')
        copyUsageReport.mockReset().mockResolvedValue('# AI 利用量')
        chooseDestination.mockReset().mockResolvedValue('/tmp/ai-usage.md')
        currentPermission.mockReset().mockResolvedValue(OWNER)
        // 既定はシークレットキー方式（残量は届かない = 欄を出さない）。
        codexPlanUsage.mockReset().mockResolvedValue({available: false, fetched: false})
    })

    // 期間を指定して横断一覧（合計・直近利用日時・上限設定時の消費率）を表示する。
    it('期間を指定して横断一覧を表示する（合計・直近の利用・消費率が数値で読める）', async () => {
        render(<UsageDashboard onBack={() => undefined} />)

        // 開いた時点で当月を既定として集計する（読むだけの画面のため確認操作を挟まない）。
        await waitFor(() => expect(usageDashboard).toHaveBeenCalled())

        fireEvent.change(screen.getByLabelText('開始日'), {target: {value: '2026-08-01'}})
        fireEvent.change(screen.getByLabelText('終了日'), {target: {value: '2026-08-31'}})
        await clickEnabled('集計する')

        await waitFor(() =>
            expect(usageDashboard).toHaveBeenCalledWith({from: '2026-08-01', to: '2026-08-31', path: ''}),
        )

        const list = await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})
        const row = within(list).getByText('在庫管理システム').closest('[role="row"]')
        expect(row).not.toBeNull()
        // 数値そのものが読める（グラフのみの表現にしない）。
        // 列の並びは CROSS_COLUMNS（プロジェクト / 消費 / 直近 / 上限 / 消費率 / 実績なし）。
        const cells = within(row as HTMLElement).getAllByRole('cell').map((c) => c.textContent)
        expect(cells[1]).toBe('123,456')
        expect(cells[3]).toBe('200,000')
        expect(cells[4]).toBe('61.7%')
        // 欠測（トークン実績のない送信）を黙って落とさない。件数そのものを列で示す。
        expect(cells[5]).toBe('2')
        // 直近の利用日時はローカル表示（保存は UTC）。
        expect(cells[2]).not.toBe('—')
    })

    // 到達不能なプロジェクトは行として残し、理由を日本語で示す（黙って消さない）。
    it('集計不能のプロジェクトを行として残し、理由を表示する', async () => {
        render(<UsageDashboard onBack={() => undefined} />)

        const list = await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})
        const row = within(list).getByText('人事システム').closest('[role="row"]')
        expect(row?.textContent).toContain('集計不能')

        const notices = await screen.findByLabelText('集計できなかったプロジェクト')
        expect(notices.textContent).toContain('共有フォルダへ接続できません')
    })

    // 行選択で内訳（プロバイダ別・セッション別・作業者別）とセッション統計。
    it('行を選ぶと内訳とセッション統計を表示する', async () => {
        render(<UsageDashboard onBack={() => undefined} />)
        const list = await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})

        usageDashboard.mockResolvedValue(DETAIL)
        fireEvent.click(within(list).getByText('在庫管理システム'))

        await waitFor(() =>
            expect(usageDashboard).toHaveBeenLastCalledWith(
                expect.objectContaining({path: '/projects/inventory'}),
            ),
        )

        const detail = await screen.findByLabelText('プロジェクトの内訳')
        expect(detail.textContent).toContain('在庫管理システム の内訳')
        // 入力 / 出力 / 推論の内訳が数値で読める。
        expect(detail.textContent).toContain('100,000')
        expect(detail.textContent).toContain('3,456')

        const providers = within(detail).getByRole('table', {name: 'プロバイダ別'})
        expect(providers.textContent).toContain('anthropic')
        const sessions = within(detail).getByRole('table', {name: '対話セッション別'})
        expect(sessions.textContent).toContain('S-0001')
        // セッション文脈を持たない送信は生の空値を出さずラベルに写像する（利用者に生の値を見せない）。
        expect(sessions.textContent).toContain('対話セッション外（取り込み分析など）')
        expect(within(detail).getByRole('table', {name: '作業者別'}).textContent).toContain('U-0001')

        // セッション統計（フェーズ別・質問票・決定・未決・確定要件）。
        expect(detail.textContent).toContain('要件定義 4')
        expect(detail.textContent).toContain('基本設計 1')
        expect(detail.textContent).toContain('4.5')
        expect(detail.textContent).toContain('11')
    })

    // 共同プロジェクトの内訳には集計範囲を併記する。
    it('内訳に集計範囲の併記を表示する', async () => {
        render(<UsageDashboard onBack={() => undefined} />)
        const list = await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})

        usageDashboard.mockResolvedValue({
            ...DETAIL,
            detail: {
                ...DETAIL.detail,
                scopeNotice:
                    '2026-09-02 10:00 に取り込んだ時点までの、全メンバーの利用量を集計しています。それ以降に他のメンバーが消費したぶんは、次に取り込むまで含まれません。',
                lastIncorporation: '2026-09-02 10:00',
            },
        })
        fireEvent.click(within(list).getByText('在庫管理システム'))

        const detail = await screen.findByLabelText('プロジェクトの内訳')
        expect(detail.textContent).toContain('取り込んだ時点までの、全メンバーの利用量を集計しています')
    })

    // Markdown ファイル出力とクリップボードコピー。
    it('Markdown 保存とクリップボードコピーの成否をトーストで示す', async () => {
        render(<UsageDashboard onBack={() => undefined} />)
        await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})

        fireEvent.click(screen.getByRole('button', {name: 'Markdown で保存する'}))
        await waitFor(() => expect(saveUsageReport).toHaveBeenCalled())
        const saved = await screen.findByRole('status')
        expect(saved.textContent).toContain('AI 利用量を保存しました')
        await clickEnabledBy(() => within(saved).getByRole('button', {name: '通知を閉じる'}))

        copyUsageReport.mockRejectedValueOnce(
            new Error('クリップボードへコピーできませんでした。ファイルへ保存してください。'),
        )
        await clickEnabled('クリップボードへコピーする')
        await waitFor(() => expect(copyUsageReport).toHaveBeenCalled())
        // 失敗も同じ経路で示す（黙って成功したように見せない）。
        const failed = await screen.findByRole('status')
        expect(failed.textContent).toContain('クリップボードへコピーできませんでした')
        expect(failed.getAttribute('data-tone')).toBe('danger')
    })

    // 上限設定の導線はオーナーのみ。判定はバインディングの結果だけを使う。
    it('オーナーは上限設定へ進める', async () => {
        const open = vi.fn()
        render(<UsageDashboard onBack={() => undefined} onOpenUsageLimit={open} />)
        await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})

        const button = await waitFor(() => {
            const b = screen.getByRole('button', {name: '上限を設定する'})
            expect(b.hasAttribute('disabled')).toBe(false)
            return b
        })
        fireEvent.click(button)
        expect(open).toHaveBeenCalled()
    })

    it('オーナー以外では上限設定の導線が無効化され理由が表示される', async () => {
        const open = vi.fn()
        currentPermission.mockResolvedValue(VIEWER)
        render(<UsageDashboard onBack={() => undefined} onOpenUsageLimit={open} />)
        await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})

        const button = await waitFor(() => {
            const b = screen.getByRole('button', {name: '上限を設定する'})
            expect(b.hasAttribute('disabled')).toBe(true)
            return b
        })
        // 理由はバインディングが返した文言をそのまま示す（画面で作文しない）。
        expectDisabledReason(button, VIEWER.usageLimitReason)
        expect(screen.getByText(new RegExp(VIEWER.usageLimitReason))).toBeTruthy()
        fireEvent.click(button)
        expect(open).not.toHaveBeenCalled()
    })

    // 上限はプロジェクト単位の設定。開いていない経路では対象が定まらない。
    it('プロジェクトを開いていない経路では上限設定の導線が理由つきで無効になる', async () => {
        // プロジェクトを開いていないと権限判定そのものが取れない（バインディングがエラーを返す）。
        currentPermission.mockRejectedValue(new Error('プロジェクトが開かれていません。'))
        render(<UsageDashboard onBack={() => undefined} />)
        await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})

        const button = screen.getByRole('button', {name: '上限を設定する'})
        expectDisabledReason(button, 'プロジェクトを開いてから上限を設定してください。')
        // 横断一覧そのものはプロジェクトを開かなくても読める。
        expect(screen.getByRole('table', {name: 'プロジェクト横断の利用量'}).textContent).toContain(
            '在庫管理システム',
        )
    })

    // 閲覧権限でも参照・出力できる（AI を呼ばないためオフラインでも成立する）。
    it('閲覧権限でも一覧と出力操作が使える', async () => {
        currentPermission.mockResolvedValue(VIEWER)
        render(<UsageDashboard onBack={() => undefined} />)

        const list = await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})
        expect(list.textContent).toContain('在庫管理システム')
        expect(screen.getByRole('button', {name: 'Markdown で保存する'}).hasAttribute('disabled')).toBe(false)
        expect(screen.getByRole('button', {name: 'クリップボードへコピーする'}).hasAttribute('disabled')).toBe(
            false,
        )
    })

    // 失敗は「原因＋次の行動」の日本語で示し、内部コード値を出さない。
    it('集計に失敗したときは理由を表示し、一覧を空にする', async () => {
        usageDashboard.mockRejectedValue(
            new Error('利用者 ID が未登録です。設定で利用者 ID を登録してください。'),
        )
        render(<UsageDashboard onBack={() => undefined} />)

        expect(await screen.findByText(/利用者 ID が未登録です/)).toBeTruthy()
        expect(screen.getByText('この端末で開けるプロジェクトがありません。')).toBeTruthy()
    })
})

/*
 * ChatGPT のプランの残量。
 * サインインしているときだけ出し、**表示のための通信をしない**。
 * トークン上限の判定には使わない旨を添える。
 */
describe('AI 利用量ダッシュボード — ChatGPT のプランの残量', () => {
    beforeEach(() => {
        usageDashboard.mockReset().mockResolvedValue(CROSS)
        currentPermission.mockReset().mockResolvedValue(OWNER)
        codexPlanUsage.mockReset().mockResolvedValue({
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
            ],
        })
    })

    it('サインインしているときは残量を出し、上限の判定に使わない旨を添える', async () => {
        render(<UsageDashboard onBack={() => undefined} />)

        const panel = await screen.findByLabelText('ChatGPT のプランの残量')
        expect(within(panel).getByText('5 時間の利用枠')).toBeInTheDocument()
        expect(within(panel).getByText('42.0%')).toBeInTheDocument()
        expect(within(panel).getByText('2026-09-16 08:00')).toBeInTheDocument()
        expect(within(panel).getByText('2026-09-15 18:30')).toBeInTheDocument()
        expect(within(panel).getByText(/トークン上限の判定には使いません/)).toBeInTheDocument()
    })

    it('シークレットキー方式では残量の欄を出さない', async () => {
        codexPlanUsage.mockResolvedValue({available: false, fetched: false})
        render(<UsageDashboard onBack={() => undefined} />)

        await screen.findByRole('table', {name: 'プロジェクト横断の利用量'})
        expect(screen.queryByLabelText('ChatGPT のプランの残量')).toBeNull()
    })
})
