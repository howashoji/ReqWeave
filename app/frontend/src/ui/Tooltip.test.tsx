import {act, fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {Button, Tooltip} from './index'

/*
 * ツールチップ。
 *
 * 「アプリ内の要素で描く」「マウス専用にしない」「無効化の理由を優先する」を機械検証する。
 * ブラウザ標準の `title` に頼っていると、ここで検証する項目が一つも書けない（アプリ内の要素で描く理由そのもの）。
 */

const TIP = '対話から抽出した決定事項と未決事項を一覧で確認します。'

describe('Tooltip', () => {
    it('既定では表示しない', () => {
        render(
            <Tooltip text={TIP}>
                <button type="button">決定・未決</button>
            </Tooltip>,
        )
        expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('ポインタを重ねるとアプリ内の要素で表示し、離すと消える', () => {
        render(
            <Tooltip text={TIP}>
                <button type="button">決定・未決</button>
            </Tooltip>,
        )
        const target = screen.getByRole('button', {name: '決定・未決'})

        fireEvent.mouseOver(target)
        const tip = screen.getByRole('tooltip')
        expect(tip).toHaveTextContent(TIP)
        // 表示は DOM の要素であり、ブラウザ任せの title ではない
        expect(target).not.toHaveAttribute('title')

        fireEvent.mouseOut(target)
        expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('キーボードのフォーカスでも表示し、外れると消える', () => {
        render(
            <Tooltip text={TIP}>
                <button type="button">決定・未決</button>
            </Tooltip>,
        )
        const target = screen.getByRole('button', {name: '決定・未決'})

        // 実際に焦点を当てる（合成イベントの発火ではなく、キーボード移動と同じ経路を通す）
        act(() => target.focus())
        expect(document.activeElement).toBe(target)
        expect(screen.getByRole('tooltip')).toHaveTextContent(TIP)

        act(() => target.blur())
        expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('表示中は対象要素の aria-describedby がツールチップを指す（読み上げと視覚表示の一致）', () => {
        render(
            <Tooltip text={TIP}>
                <button type="button">決定・未決</button>
            </Tooltip>,
        )
        const target = screen.getByRole('button', {name: '決定・未決'})
        expect(target).not.toHaveAttribute('aria-describedby')

        fireEvent.mouseOver(target)
        const tip = screen.getByRole('tooltip')
        expect(target.getAttribute('aria-describedby')).toBe(tip.id)
        expect(tip.id).not.toBe('')
    })

    it('Esc で閉じる', () => {
        render(
            <Tooltip text={TIP}>
                <button type="button">決定・未決</button>
            </Tooltip>,
        )
        const target = screen.getByRole('button', {name: '決定・未決'})
        act(() => target.focus())
        expect(screen.getByRole('tooltip')).toBeInTheDocument()

        fireEvent.keyDown(target, {key: 'Escape'})
        expect(screen.queryByRole('tooltip')).toBeNull()
    })

    it('画面右端からはみ出すときだけ右揃えへ倒す（読めない吹き出しを出さない）', () => {
        const {unmount} = render(
            <Tooltip text={TIP}>
                <button type="button">新規作成</button>
            </Tooltip>,
        )
        // 既定（はみ出さない）は左揃えのまま
        fireEvent.mouseOver(screen.getByRole('button', {name: '新規作成'}))
        expect(screen.getByRole('tooltip').className).not.toContain('rw-tooltip__bubble--end')
        unmount()

        // 右端からはみ出す状況を作る（jsdom は実寸を持たないため測定値を差し替える）
        const rect = vi
            .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
            .mockReturnValue({right: window.innerWidth + 40} as DOMRect)
        try {
            render(
                <Tooltip text={TIP}>
                    <button type="button">新規作成</button>
                </Tooltip>,
            )
            fireEvent.mouseOver(screen.getByRole('button', {name: '新規作成'}))
            expect(screen.getByRole('tooltip').className).toContain('rw-tooltip__bubble--end')
        } finally {
            rect.mockRestore()
        }
    })

    it('表示中でも対象の操作を妨げない（操作の代わりにしない）', () => {
        const onClick = vi.fn()
        render(
            <Tooltip text={TIP}>
                <button type="button" onClick={onClick}>
                    決定・未決
                </button>
            </Tooltip>,
        )
        const target = screen.getByRole('button', {name: '決定・未決'})
        fireEvent.mouseOver(target)
        fireEvent.click(target)
        expect(onClick).toHaveBeenCalledTimes(1)
    })
})

describe('Button のツールチップ', () => {
    it('tooltip を渡したボタンは title 属性を使わずアプリ内の要素で描く', () => {
        render(<Button tooltip={TIP}>決定・未決</Button>)
        const target = screen.getByRole('button', {name: '決定・未決'})
        expect(target).not.toHaveAttribute('title')

        fireEvent.mouseOver(target)
        expect(screen.getByRole('tooltip')).toHaveTextContent(TIP)
    })

    it('無効化されているときは理由だけを出し、機能説明と二重に出さない', () => {
        const reason = '一覧からプロジェクトを選んでください。'
        render(
            <Button tooltip="選んだプロジェクトのトークン消費の実績を期間ごとに確認します。" disabledReason={reason}>
                トークン消費実績
            </Button>,
        )
        const target = screen.getByRole('button', {name: 'トークン消費実績'})
        expect(target).toBeDisabled()

        fireEvent.mouseOver(target)
        const tip = screen.getByRole('tooltip')
        expect(tip).toHaveTextContent(reason)
        expect(tip.textContent).not.toContain('トークン消費の実績')
    })

    // 無効化の理由はツールチップの段階適用（まず上部ナビから）の対象外で、tooltip を渡していないボタンでも
    // アプリ内の要素で描く。`title` に載せると、無効化された要素は
    // ポインタ事象を発しないため理由が画面のどこにも出ない（実際にそれで見逃した）。
    it('tooltip を渡していないボタンでも、無効化の理由はアプリ内の要素で描く', () => {
        const reason = '一覧からプロジェクトを選んでください。'
        render(<Button disabledReason={reason}>トークン消費実績</Button>)
        const target = screen.getByRole('button', {name: 'トークン消費実績'})
        expect(target).not.toHaveAttribute('title')

        fireEvent.mouseOver(target)
        expect(screen.getByRole('tooltip')).toHaveTextContent(reason)
    })

    it('無効化されていないボタンに tooltip を渡さなければ、何も描かない', () => {
        render(<Button>トークン消費実績</Button>)
        const target = screen.getByRole('button', {name: 'トークン消費実績'})
        expect(target).toBeEnabled()

        fireEvent.mouseOver(target)
        expect(screen.queryByRole('tooltip')).toBeNull()
    })
})
