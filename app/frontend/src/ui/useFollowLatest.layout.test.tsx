import {act, render} from '@testing-library/react'
import {useRef, useState} from 'react'
import {describe, expect, it} from 'vitest'
import {useFollowLatest} from './useFollowLatest'

/*
 * 履歴を最新の発話が見える位置に保つことを**実描画で**確かめる。
 * jsdom は高さも scrollTop も計算しないため、実ブラウザで寸法を見る。
 */

let api: {add: (n: number) => void; follow: () => void; el: () => HTMLDivElement}

function History() {
    const ref = useRef<HTMLDivElement | null>(null)
    const [items, setItems] = useState(() => Array.from({length: 30}, (_, i) => `発話 ${i + 1}`))
    const follow = useFollowLatest(ref, [items])
    api = {
        add: (n) => setItems((prev) => [...prev, ...Array.from({length: n}, (_, i) => `追加 ${prev.length + i + 1}`)]),
        follow,
        el: () => ref.current!,
    }
    return (
        <div ref={ref} data-testid="history" style={{height: '200px', overflowY: 'auto'}}>
            {items.map((t) => (
                <p key={t} style={{margin: 0, height: '24px'}}>
                    {t}
                </p>
            ))}
        </div>
    )
}

const atBottom = (el: HTMLElement) => el.scrollHeight - el.scrollTop - el.clientHeight <= 1

describe('useFollowLatest（実描画）', () => {
    it('開いた直後は最新の発話が見えている', () => {
        render(<History />)
        const el = api.el()
        expect(el.scrollHeight, '内容が高さを超えていない（検査が空振り）').toBeGreaterThan(el.clientHeight)
        expect(atBottom(el), '開いた直後に末尾が見えていない').toBe(true)
    })

    it('末尾を見ているときは、発話が増えると末尾へ付いていく', async () => {
        render(<History />)
        await act(async () => api.add(3))
        expect(atBottom(api.el()), '新しい発話が見える位置へ送られていない').toBe(true)
    })

    it('上へ戻って読んでいる間は送らず、利用者の操作（follow）で末尾へ戻る', async () => {
        render(<History />)
        const el = api.el()
        // 利用者が先頭まで戻って過去の発話を読む。
        await act(async () => {
            el.scrollTop = 0
            el.dispatchEvent(new Event('scroll'))
        })
        await act(async () => api.add(3))
        expect(el.scrollTop, '読んでいる途中で末尾へ引き戻された').toBe(0)

        // 「次の質問」を押した（follow）→ 末尾へ送り、以後も付いていく。
        await act(async () => api.follow())
        expect(atBottom(el), 'follow で末尾へ戻らない').toBe(true)
        await act(async () => api.add(2))
        expect(atBottom(el), 'follow のあと、増えた発話へ付いていかない').toBe(true)
    })
})
