import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {Button} from './Button'
import {Overlay} from './Overlay'

/*
 * 重ねて出す面（どの画面の型でも共通）。
 *
 * 「一時的に見るもの・答えるもの」を通常の流れへ挿し込むと、開いた瞬間に作業領域が
 * そのぶん下へ押し出される（実機で起きた）。ここでは 2 種類の面の**振る舞いの違い**
 * ——背後を操作させるか・どう閉じるか・フォーカスが外へ出るか——を確かめる。
 */

describe('Overlay — 重ねて出す面', () => {
    it('popover は背後の操作を妨げない面として出し、外側を押すと閉じる', () => {
        const onClose = vi.fn()
        const {container} = render(
            <Overlay label="工程の全体像" onClose={onClose}>
                <p>本文</p>
            </Overlay>,
        )
        // 参照のための面なので dialog にはしない（背後は操作できる）。
        const panel = screen.getByRole('group', {name: '工程の全体像'})
        expect(panel).not.toHaveAttribute('aria-modal')
        // 開いた直後の読み上げ位置を面へ移す。
        expect(document.activeElement).toBe(panel)

        const outside = container.querySelector('.rw-overlay__outside')
        expect(outside).not.toBeNull()
        // 背後を暗くしない（覆い隠すのは modal だけ）。
        expect(outside).not.toHaveClass('rw-overlay__outside--modal')
        fireEvent.click(outside as Element)
        expect(onClose).toHaveBeenCalledTimes(1)
    })

    it('modal は背後を操作させない面として出す（暗くする・dialog として読み上げる）', () => {
        const onClose = vi.fn()
        const {container} = render(
            <Overlay label="成果物の生成の進め方" variant="modal" onClose={onClose}>
                <Button onClick={vi.fn()}>この進め方で始める</Button>
            </Overlay>,
        )
        const panel = screen.getByRole('dialog', {name: '成果物の生成の進め方'})
        expect(panel).toHaveAttribute('aria-modal', 'true')
        expect(container.querySelector('.rw-overlay__outside--modal')).not.toBeNull()
    })

    it('modal のフォーカスは面の中を回る（背後の要素へ Tab で抜けない）', () => {
        render(
            <>
                <button type="button">背後のボタン</button>
                <Overlay label="要件項目の差し戻しの進め方" variant="modal" onClose={vi.fn()}>
                    <Button onClick={vi.fn()}>この進め方で始める</Button>
                    <Button variant="quiet" onClick={vi.fn()}>
                        やめる
                    </Button>
                </Overlay>
            </>,
        )
        const start = screen.getByRole('button', {name: 'この進め方で始める'})
        const cancel = screen.getByRole('button', {name: 'やめる'})
        const panel = screen.getByRole('dialog')

        // 末尾で Tab すると先頭へ戻る。
        cancel.focus()
        fireEvent.keyDown(panel, {key: 'Tab'})
        expect(document.activeElement).toBe(start)

        // 先頭で Shift+Tab すると末尾へ回る。
        fireEvent.keyDown(panel, {key: 'Tab', shiftKey: true})
        expect(document.activeElement).toBe(cancel)
    })

    it('どちらも Esc で閉じられる（ネイティブのダイアログを使わない）', () => {
        const closePopover = vi.fn()
        const {unmount} = render(
            <Overlay label="根拠" onClose={closePopover}>
                <p>本文</p>
            </Overlay>,
        )
        fireEvent.keyDown(window, {key: 'Escape'})
        expect(closePopover).toHaveBeenCalledTimes(1)
        unmount()

        const cancelModal = vi.fn()
        render(
            <Overlay label="成果物の生成の進め方" variant="modal" onClose={cancelModal}>
                <p>本文</p>
            </Overlay>,
        )
        fireEvent.keyDown(window, {key: 'Escape'})
        expect(cancelModal).toHaveBeenCalledTimes(1)
    })
})
