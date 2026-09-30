import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {EmptyState} from './EmptyState'

/*
 * 空状態。
 *
 * 「何が無いか」だけを書くと画面が行き止まりになる。対話画面・取り込み画面と
 * 同型の報告が続いたため、**次の一手か「利用者の操作では増えない理由」のどちらかを型で必須**にした。
 * ここではその 2 通りが実際に画面へ出ることを確かめる。
 */

describe('EmptyState — 空状態', () => {
    it('次の一手がある場合は、何が無いかと次に何をするかを両方出す', () => {
        render(
            <EmptyState
                message="決定事項はまだありません。"
                next="対話画面で「次の質問」に答え、抽出された候補を承認すると記録されます。"
            />,
        )
        expect(screen.getByText('決定事項はまだありません。')).toBeInTheDocument()
        expect(
            screen.getByText('対話画面で「次の質問」に答え、抽出された候補を承認すると記録されます。'),
        ).toBeInTheDocument()
    })

    it('次の操作が別の画面にあるときは、そこへ行く操作を押せる形で出す', () => {
        const onClick = vi.fn()
        render(
            <EmptyState
                message="決定事項はまだありません。"
                next="対話で答えると記録されます。"
                action={{label: '対話へ戻る', onClick}}
            />,
        )
        const button = screen.getByRole('button', {name: '対話へ戻る'})
        // 押せると分かる枠を持つ（文字だけのボタンにしない）。
        expect(button.getAttribute('data-variant')).toBe('secondary')
        fireEvent.click(button)
        expect(onClick).toHaveBeenCalledTimes(1)
    })

    it('利用者の操作では増えないものは、増える条件を説明する（操作を出さない）', () => {
        render(
            <EmptyState
                message="変更履歴はまだありません。"
                automatic="決定事項・未決事項・要件項目を記録するたびに、ここへ残ります。"
            />,
        )
        expect(
            screen.getByText('決定事項・未決事項・要件項目を記録するたびに、ここへ残ります。'),
        ).toBeInTheDocument()
        expect(screen.queryByRole('button')).toBeNull()
    })

    it('読み上げの live region にしない（画面に元からある説明のため）', () => {
        render(<EmptyState message="まだありません。" next="対話で答えると増えます。" />)
        expect(screen.queryByRole('status')).toBeNull()
    })
})
