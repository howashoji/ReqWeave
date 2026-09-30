import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {PreviousRunNotice} from './PreviousRunNotice'

describe('前回の異常終了の案内', () => {
    it('前回が正常終了なら何も出さない', () => {
        const {container} = render(<PreviousRunNotice incomplete={false} onOpenSettings={() => undefined} />)
        expect(container).toBeEmptyDOMElement()
    })

    it('異常終了していたら案内と設定への導線を出す', () => {
        const onOpenSettings = vi.fn()
        render(<PreviousRunNotice incomplete onOpenSettings={onOpenSettings} />)

        expect(screen.getByText('前回は正常に終了していません')).toBeInTheDocument()
        // 操作を止めない旨（保存済みの内容はそのまま開ける）を本文で示す。
        expect(screen.getByText(/保存済みの内容はそのまま開けます/)).toBeInTheDocument()

        fireEvent.click(screen.getByRole('button', {name: '設定を開く'}))
        expect(onOpenSettings).toHaveBeenCalledTimes(1)
    })

    it('閉じたら出し直さない', () => {
        const {container} = render(<PreviousRunNotice incomplete onOpenSettings={() => undefined} />)

        fireEvent.click(screen.getByRole('button', {name: '閉じる'}))
        expect(container).toBeEmptyDOMElement()
    })

    it('あとで見るでも閉じる（バナーは閉じる導線を必ず持つ）', () => {
        const {container} = render(<PreviousRunNotice incomplete onOpenSettings={() => undefined} />)

        fireEvent.click(screen.getByRole('button', {name: 'あとで見る'}))
        expect(container).toBeEmptyDOMElement()
    })
})
