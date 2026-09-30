import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {UsageLimit} from './UsageLimit'

const usageStatusNow = vi.hoisted(() => vi.fn())
const setUsageLimit = vi.hoisted(() => vi.fn())
const clearUsageLimit = vi.hoisted(() => vi.fn())
const currentPermission = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    UsageStatusNow: usageStatusNow,
    SetUsageLimit: setUsageLimit,
    ClearUsageLimit: clearUsageLimit,
    CurrentPermission: currentPermission,
}))

const NO_LIMIT = {consumedTokens: 12000, missingRecords: 0, warnRatio: 0, level: 'none'}
const WITH_LIMIT = {
    consumedTokens: 12000,
    missingRecords: 0,
    limitTokens: 100000,
    warnRatio: 0.8,
    remainingTokens: 88000,
    consumptionRatio: 0.12,
    level: 'none',
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
const EDITOR = {
    role: 'editor',
    roleLabel: '編集',
    canEdit: true,
    canManageMembers: false,
    canManageUsageLimit: false,
    reason: '',
    manageReason: 'メンバー管理はオーナー権限が必要です。オーナーに依頼してください。',
    usageLimitReason: 'AI 利用量上限の設定はオーナー権限が必要です。オーナーに依頼してください。',
}

describe('トークン上限設定', () => {
    beforeEach(() => {
        usageStatusNow.mockReset().mockResolvedValue(NO_LIMIT)
        setUsageLimit.mockReset().mockResolvedValue(WITH_LIMIT)
        clearUsageLimit.mockReset().mockResolvedValue(NO_LIMIT)
        currentPermission.mockReset().mockResolvedValue(OWNER)
    })

    // オーナーは上限値と警告閾値を設定できる。
    it('オーナーは上限値と警告閾値を設定できる', async () => {
        render(<UsageLimit onBack={() => undefined} />)
        await waitFor(() => expect(usageStatusNow).toHaveBeenCalled())
        // 未設定のときは「未設定」と示し、警告閾値は既定 80% を初期値にする。
        expect(await screen.findByText('未設定')).toBeTruthy()
        expect((screen.getByLabelText('警告閾値（%）') as HTMLInputElement).value).toBe('80')

        fireEvent.change(screen.getByLabelText('上限値（トークン）'), {target: {value: '100000'}})
        fireEvent.change(screen.getByLabelText('警告閾値（%）'), {target: {value: '80'}})
        fireEvent.click(await screen.findByRole('button', {name: '上限を設定する'}))

        await waitFor(() => expect(setUsageLimit).toHaveBeenCalledWith({tokensMax: 100000, warnRatio: 0.8}))
        // 保存後は保存済みの値が表示へ反映される（入力値の見た目だけを変えない）。
        expect(await screen.findByText('100,000')).toBeTruthy()
        expect((await screen.findByRole('status')).textContent).toContain('トークン上限を保存しました')
    })

    it('設定済みなら変更・解除ができる', async () => {
        usageStatusNow.mockResolvedValue(WITH_LIMIT)
        render(<UsageLimit onBack={() => undefined} />)

        const change = await screen.findByRole('button', {name: '上限を変更する'})
        expect((screen.getByLabelText('上限値（トークン）') as HTMLInputElement).value).toBe('100000')
        expect(change.hasAttribute('disabled')).toBe(false)

        fireEvent.click(screen.getByRole('button', {name: '上限を解除する'}))
        await waitFor(() => expect(clearUsageLimit).toHaveBeenCalled())
        expect((await screen.findByRole('status')).textContent).toContain('トークン上限を解除しました')
        // 解除後は「未設定」に戻り、解除ボタンは理由つきで無効になる。
        expect(await screen.findByText('未設定')).toBeTruthy()
        const cleared = screen.getByRole('button', {name: '上限を解除する'})
        expectDisabledReason(cleared, '上限は設定されていません。')
    })

    // 編集・閲覧権限では無効化して理由を示す（ほかの管理画面と同じ方式）。
    it('オーナー以外では操作が無効化され理由が表示される', async () => {
        currentPermission.mockResolvedValue(EDITOR)
        usageStatusNow.mockResolvedValue(WITH_LIMIT)
        render(<UsageLimit onBack={() => undefined} />)
        await screen.findByText('100,000')

        const change = await waitFor(() => {
            const b = screen.getByRole('button', {name: '上限を変更する'})
            expect(b.hasAttribute('disabled')).toBe(true)
            return b
        })
        expectDisabledReason(change, EDITOR.usageLimitReason)
        expect(screen.getByRole('button', {name: '上限を解除する'}).hasAttribute('disabled')).toBe(true)
        expect((screen.getByLabelText('上限値（トークン）') as HTMLInputElement).disabled).toBe(true)
        expect(screen.getByText(new RegExp(EDITOR.usageLimitReason))).toBeTruthy()

        fireEvent.click(change)
        expect(setUsageLimit).not.toHaveBeenCalled()
    })

    // 検証エラーは原因＋次の行動の日本語 1 文をそのまま示す（内部コード値を出さない）。
    it('保存に失敗したときは理由を示し、表示値を書き換えない', async () => {
        usageStatusNow.mockResolvedValue(WITH_LIMIT)
        setUsageLimit.mockRejectedValue(
            new Error('トークン上限は 1 以上の数値で入力してください（入力値: 0）。'),
        )
        render(<UsageLimit onBack={() => undefined} />)
        await screen.findByText('100,000')

        fireEvent.change(screen.getByLabelText('上限値（トークン）'), {target: {value: '0'}})
        fireEvent.click(screen.getByRole('button', {name: '上限を変更する'}))

        const toast = await screen.findByRole('status')
        expect(toast.textContent).toContain('トークン上限は 1 以上の数値で入力してください')
        expect(toast.getAttribute('data-tone')).toBe('danger')
        // 保存されていないので表示中の上限は変わらない。
        expect(screen.getByText('100,000')).toBeTruthy()
    })

    // 状態は色だけでなく文字でも区別する。生のコード値は出さない。
    it('状態を日本語ラベルで示す（生のコード値を出さない）', async () => {
        usageStatusNow.mockResolvedValue({...WITH_LIMIT, consumedTokens: 100000, remainingTokens: 0,
            consumptionRatio: 1, level: 'blocked'})
        render(<UsageLimit onBack={() => undefined} />)

        // 待つ対象と確かめる対象を一致させる（ラベル「状態」は初期描画で出るため、
        // それを待っても値の到着は待てない）。
        const value = await screen.findByText('上限に到達')
        const block = value.closest('div') as HTMLElement
        expect(within(block).getByText('状態')).toBeTruthy()
        expect(screen.queryByText('blocked')).toBeNull()
    })

    it('現在の状態を取得できないときは理由を表示する', async () => {
        usageStatusNow.mockRejectedValue(new Error('プロジェクトが開かれていません。'))
        render(<UsageLimit onBack={() => undefined} />)

        expect(await screen.findByText(/プロジェクトが開かれていません/)).toBeTruthy()
    })
})
