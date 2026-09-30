import {render, screen} from '@testing-library/react'
import {useEffect, useState} from 'react'
import {describe, expect, it} from 'vitest'

/*
 * findBy* / waitFor の待ち時間が既定（1000ms）のままでないことを、**振る舞いで**確かめる。
 *
 * 設定値そのものを読んで比べると、設定を消しても既定値と比べ直すだけで気づけない。
 * ここでは「既定では間に合わない遅さ」で描画される要素を findBy* が待ち切れることを確かめる。
 * 遅延は setup.ts の 5000ms より十分小さく、既定の 1000ms より確実に大きい値にする。
 */

const DELAY_MS = 2000

function LateContent() {
    const [shown, setShown] = useState(false)
    useEffect(() => {
        const timer = setTimeout(() => setShown(true), DELAY_MS)
        return () => clearTimeout(timer)
    }, [])
    return <div>{shown ? '遅れて現れる内容' : '読み込んでいます…'}</div>
}

describe('テストの待ち時間（並列負荷での偽赤を防ぐ）', () => {
    it('既定（1000ms）では間に合わない描画を findBy* が待ち切れる', async () => {
        render(<LateContent />)

        expect(screen.getByText('読み込んでいます…')).toBeInTheDocument()
        expect(await screen.findByText('遅れて現れる内容')).toBeInTheDocument()
    })
})
