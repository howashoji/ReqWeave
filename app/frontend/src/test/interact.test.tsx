import {fireEvent, render, screen} from '@testing-library/react'
import {useEffect, useState} from 'react'
import {describe, expect, it, vi} from 'vitest'
import {clickEnabled} from './interact'

/*
 * clickEnabled が「押し損ね」を防ぐことを振る舞いで確かめる。
 * 無効なボタンへの click は黙って無視されるため、素の fireEvent.click では押せないことも併せて示す。
 */

const DELAY_MS = 300

function LateButton({onPress}: {onPress: () => void}) {
    const [ready, setReady] = useState(false)
    useEffect(() => {
        const timer = setTimeout(() => setReady(true), DELAY_MS)
        return () => clearTimeout(timer)
    }, [])
    return (
        <button type="button" disabled={!ready} onClick={onPress}>
            実行
        </button>
    )
}

describe('clickEnabled（押し損ねによる偽赤を防ぐ）', () => {
    it('素の click は無効なボタンでは黙って無視される（防ぎたい事象）', () => {
        const onPress = vi.fn()
        render(<LateButton onPress={onPress} />)

        fireEvent.click(screen.getByRole('button', {name: '実行'}))

        expect(onPress).not.toHaveBeenCalled()
    })

    it('有効になるまで待ってから押す', async () => {
        const onPress = vi.fn()
        render(<LateButton onPress={onPress} />)

        await clickEnabled('実行')

        expect(onPress).toHaveBeenCalledTimes(1)
    })
})
