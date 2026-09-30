import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {binding} from '../../wailsjs/go/models'
import {IDRangeNotice} from './IDRangeNotice'

/**
 * ID の番号帯の残量の警告。
 *
 * 「残りが少ない」と「使い切った」を色以外の手掛かり（見出しと本文）で区別し、
 * 次に取る行動（同期）へ導くこと、番号帯を使わないプロジェクトでは何も出さないことを固定する。
 */

const WARNINGS = [
    {
        kindLabel: '要件項目',
        remaining: 12,
        exhausted: false,
        message:
            '要件項目の番号の残りが 12 件です。同期（取り込み・反映）を実行すると新しい番号がまとめて確保されます。',
    },
    {
        kindLabel: '決定事項',
        remaining: 0,
        exhausted: true,
        message: '決定事項の番号を使い切ったため、新しく作成できません。同期（取り込み・反映）を実行してください。',
    },
] as binding.IDRangeWarningView[]

describe('番号帯の残量の警告', () => {
    it('残りが少ない種別と使い切った種別を区別して示す', () => {
        render(<IDRangeNotice warnings={WARNINGS} />)
        expect(screen.getByText('要件項目の番号の残りが少なくなっています（残り 12 件）')).toBeInTheDocument()
        expect(screen.getByText('決定事項の番号を使い切りました')).toBeInTheDocument()
        expect(screen.getByText(/新しい番号がまとめて確保されます/)).toBeInTheDocument()
        expect(screen.getByText(/新しく作成できません/)).toBeInTheDocument()
    })

    it('同期への導線を出し、閉じた種別は出し直さない', () => {
        const onOpenSync = vi.fn()
        render(<IDRangeNotice warnings={[WARNINGS[0]]} onOpenSync={onOpenSync} />)
        fireEvent.click(screen.getByRole('button', {name: '同期を開く'}))
        expect(onOpenSync).toHaveBeenCalled()

        fireEvent.click(screen.getByRole('button', {name: '閉じる'}))
        expect(screen.queryByText(/要件項目の番号の残りが少なく/)).not.toBeInTheDocument()
    })

    it('警告が無ければ何も出さない（番号帯を使わないプロジェクト）', () => {
        const {container} = render(<IDRangeNotice warnings={[]} />)
        expect(container).toBeEmptyDOMElement()
        const empty = render(<IDRangeNotice warnings={null} />)
        expect(empty.container).toBeEmptyDOMElement()
    })
})
