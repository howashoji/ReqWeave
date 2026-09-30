import {fireEvent, render, screen, within} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {describe, expect, it, vi} from 'vitest'
import {MergePanel} from './MergePanel'
import {binding} from '../../wailsjs/go/models'

const CONFLICTS = [
    {
        id: 'FR-INV-001',
        label: 'FR-INV-001',
        baselineBody: '受注確定時に在庫を引き当てること。',
        currentBody: '他メンバーの変更（条件を追記）。',
        currentHash: 'hash-current',
    },
    {
        id: 'term:在庫引当',
        label: '用語「在庫引当」',
        baselineBody: '在庫引当（Stock Allocation）: 受注に対して在庫を確保すること。',
        currentBody: '在庫引当（Stock Allocation）: 他メンバーが直した定義。',
        currentHash: 'hash-term',
    },
] as binding.ConflictView[]

const PROPOSALS = {
    'FR-INV-001': '出荷指示時に在庫を引き当てること。',
    'term:在庫引当': '自分の定義。',
}

describe('競合マージパネル', () => {
    // 三面（基準版 / 他メンバーの変更 / 自分の反映案）が並ぶ。
    it('三面を並べて表示する', () => {
        render(
            <MergePanel
                conflicts={CONFLICTS}
                notice="他のメンバーの変更と競合しています。"
                proposals={PROPOSALS}
                busy={false}
                onApprove={() => undefined}
                onDefer={() => undefined}
            />,
        )
        const item = screen.getByLabelText('競合 FR-INV-001')
        expect(within(item).getByText('基準版（あなたが候補を作った時点）')).toBeTruthy()
        expect(within(item).getByText('他メンバーの変更（現在の内容）')).toBeTruthy()
        expect(within(item).getByText('自分の反映案')).toBeTruthy()
        expect(within(item).getByText('受注確定時に在庫を引き当てること。')).toBeTruthy()
        expect(within(item).getByText('他メンバーの変更（条件を追記）。')).toBeTruthy()
        expect(within(item).getByText('出荷指示時に在庫を引き当てること。')).toBeTruthy()
        // 用語は内部キーではなく表示名で出す。
        expect(screen.getByLabelText('競合 用語「在庫引当」')).toBeTruthy()
    })

    // すべての対象の扱いを選ぶまで反映できない（承認なしに反映しない）。
    it('扱いを選ぶまで反映できない', () => {
        const onApprove = vi.fn()
        render(
            <MergePanel
                conflicts={CONFLICTS}
                proposals={PROPOSALS}
                busy={false}
                onApprove={onApprove}
                onDefer={() => undefined}
            />,
        )
        const apply = screen.getByRole('button', {name: 'この内容で反映する'})
        expectDisabledReason(apply, /扱いを選んで/)
        fireEvent.click(apply)
        expect(onApprove).not.toHaveBeenCalled()
    })

    // 「自分の案を通す」だけがマージ承認として渡り、未決事項化は対象として返る。
    it('選んだ扱いに応じてマージ承認と未決事項化を返す', () => {
        const onApprove = vi.fn()
        render(
            <MergePanel
                conflicts={CONFLICTS}
                proposals={PROPOSALS}
                busy={false}
                onApprove={onApprove}
                onDefer={() => undefined}
            />,
        )
        const req = screen.getByLabelText('競合 FR-INV-001')
        fireEvent.click(within(req).getByRole('radio', {name: '自分の反映案を通す'}))
        const term = screen.getByLabelText('競合 用語「在庫引当」')
        fireEvent.click(within(term).getByRole('radio', {name: '決めきれないので未決事項にする'}))

        fireEvent.click(screen.getByRole('button', {name: 'この内容で反映する'}))
        expect(onApprove).toHaveBeenCalledTimes(1)
        const [merges, openIssues] = onApprove.mock.calls[0]
        expect(merges).toHaveLength(1)
        expect(merges[0].id).toBe('FR-INV-001')
        expect(merges[0].baselineHash).toBe('hash-current')
        expect(openIssues).toEqual(['term:在庫引当'])
    })

    // 「他メンバーの変更を採用する」を選んだ対象は自分の案を反映しない（マージ承認に含めない）。
    it('他メンバーの変更を採用した対象は反映しない', () => {
        const onApprove = vi.fn()
        render(
            <MergePanel
                conflicts={[CONFLICTS[0]]}
                proposals={PROPOSALS}
                busy={false}
                onApprove={onApprove}
                onDefer={() => undefined}
            />,
        )
        fireEvent.click(screen.getByRole('radio', {name: /他メンバーの変更を採用する/}))
        fireEvent.click(screen.getByRole('button', {name: 'この内容で反映する'}))
        const [merges, openIssues] = onApprove.mock.calls[0]
        expect(merges).toHaveLength(0)
        expect(openIssues).toHaveLength(0)
    })

    // 保留すると反映しない（候補は残る）。
    it('保留を選ぶと反映しない', () => {
        const onApprove = vi.fn()
        const onDefer = vi.fn()
        render(
            <MergePanel
                conflicts={CONFLICTS}
                proposals={PROPOSALS}
                busy={false}
                onApprove={onApprove}
                onDefer={onDefer}
            />,
        )
        fireEvent.click(screen.getByRole('button', {name: '反映せず保留する'}))
        expect(onDefer).toHaveBeenCalledTimes(1)
        expect(onApprove).not.toHaveBeenCalled()
    })
})
