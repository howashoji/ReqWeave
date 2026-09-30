import {fireEvent, render, screen, within} from '@testing-library/react'
import {useState} from 'react'
import {describe, expect, it, vi} from 'vitest'
import {binding} from '../../wailsjs/go/models'
import {SyncMergePanel} from './SyncMergePanel'

/**
 * 取り込み時の三面マージの画面テスト。
 *
 * **既定の選択を置かない**こと（承認なしに進ませない）と、対象ごとに基準版 / 相手 / 自分の 3 面が
 * 判別できること、中止しても作業コピーが変わらない旨を示すことを固定する。
 */

const CHOICES = [
    {choice: 'theirs', label: '相手を採る', hint: '相手の内容で置き換えます。'},
    {choice: 'ours', label: '自分を採る', hint: '自分の内容を残します。'},
    {choice: 'both', label: '両立させる', hint: '両方を踏まえた内容を入力してください。'},
    {choice: 'open-issue', label: '未決事項として起票する', hint: '未決事項を起票します。'},
] as binding.SyncChoiceOption[]

const CONFLICTS = [
    {
        id: 'terms.yaml#在庫',
        label: '用語「在庫」',
        unitLabel: 'エントリ',
        categoryLabel: '用語',
        theirsAuthor: '鈴木',
        base: '基準の定義',
        theirs: 'B の定義',
        ours: 'A の定義',
    },
    {
        id: 'decisions/DEC-010.md',
        label: 'DEC-010',
        unitLabel: 'レコード',
        categoryLabel: '決定事項',
        theirsAuthor: '鈴木',
        base: '',
        theirs: 'B の決定',
        ours: 'A の決定',
    },
] as binding.SyncConflictView[]

/** 承認の保持は親（同期パネル）が行うため、テストでも同じ形で包む。 */
function Harness({onApply, onCancel}: {onApply?: (r: Record<string, binding.SyncResolutionInput>) => void; onCancel?: () => void}) {
    const [resolutions, setResolutions] = useState<Record<string, binding.SyncResolutionInput>>({})
    return (
        <SyncMergePanel
            conflicts={CONFLICTS}
            choices={CHOICES}
            notice="他のメンバーと同じ対象が変更されています。取り込みは行っていません（作業コピーの内容は変わっていません）。"
            busy={false}
            resolutions={resolutions}
            onChange={(id, resolution) => setResolutions((prev) => ({...prev, [id]: resolution}))}
            onCreateOpenIssue={() => undefined}
            onApply={() => onApply?.(resolutions)}
            onCancel={() => onCancel?.()}
        />
    )
}

describe('取り込み時の三面マージ', () => {
    // 基準版 / 相手 / 自分の 3 面と 4 択を対象ごとに示す。
    it('対象ごとに三面と 4 択を示す', () => {
        render(<Harness />)
        const item = screen.getByLabelText('競合 用語「在庫」')
        expect(within(item).getByText('基準版（分かれる前）')).toBeInTheDocument()
        expect(within(item).getByText('鈴木の内容')).toBeInTheDocument()
        expect(within(item).getByText('自分の内容')).toBeInTheDocument()
        expect(within(item).getByText('B の定義')).toBeInTheDocument()
        expect(within(item).getByText('A の定義')).toBeInTheDocument()
        for (const choice of CHOICES) {
            expect(within(item).getByRole('radio', {name: new RegExp(choice.label)})).toBeInTheDocument()
        }
        // 片側が存在しない面は空欄にせず、その旨を示す
        const record = screen.getByLabelText('競合 DEC-010')
        expect(within(record).getByText('（内容なし。削除されています）')).toBeInTheDocument()
    })

    // 既定の選択を置かず、全対象に選択が揃うまで進めない。
    it('既定の選択を持たず、全対象を選ぶまで実行できない', () => {
        const onApply = vi.fn()
        render(<Harness onApply={onApply} />)
        for (const radio of screen.getAllByRole('radio')) {
            expect(radio).not.toBeChecked()
        }
        const apply = screen.getByRole('button', {name: 'この内容で取り込む'})
        expect(apply).toBeDisabled()

        const item = screen.getByLabelText('競合 用語「在庫」')
        fireEvent.click(within(item).getByRole('radio', {name: /相手を採る/}))
        expect(screen.getByRole('button', {name: 'この内容で取り込む'})).toBeDisabled()

        const record = screen.getByLabelText('競合 DEC-010')
        fireEvent.click(within(record).getByRole('radio', {name: /自分を採る/}))
        const enabled = screen.getByRole('button', {name: 'この内容で取り込む'})
        expect(enabled).toBeEnabled()
        fireEvent.click(enabled)
        expect(onApply).toHaveBeenCalledWith({
            'terms.yaml#在庫': {choice: 'theirs', merged: '', adopt: '', openIssueId: ''},
            'decisions/DEC-010.md': {choice: 'ours', merged: '', adopt: '', openIssueId: ''},
        })
    })

    // 「両立させる」は統合後の内容の入力を伴う（空のままでは進めない）。
    it('両立させるは内容を入力するまで承認として成立しない', () => {
        render(<Harness />)
        const item = screen.getByLabelText('競合 用語「在庫」')
        const record = screen.getByLabelText('競合 DEC-010')
        fireEvent.click(within(record).getByRole('radio', {name: /自分を採る/}))
        fireEvent.click(within(item).getByRole('radio', {name: /両立させる/}))
        expect(screen.getByRole('button', {name: 'この内容で取り込む'})).toBeDisabled()

        fireEvent.change(screen.getByLabelText('用語「在庫」 の統合後の内容'), {
            target: {value: 'A と B を踏まえた定義'},
        })
        expect(screen.getByRole('button', {name: 'この内容で取り込む'})).toBeEnabled()
    })

    // 中止しても作業コピーは変わらない旨を明示する。
    it('中止の導線で作業コピーが変わらない旨を示す', () => {
        const onCancel = vi.fn()
        render(<Harness onCancel={onCancel} />)
        const cancel = screen.getByRole('button', {name: '取り込みをやめる（作業コピーは変わりません）'})
        fireEvent.click(cancel)
        expect(onCancel).toHaveBeenCalled()
    })
})
