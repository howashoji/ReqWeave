import {render, screen} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {FlyingCard, prefersReducedMotion} from './FlyingCard'

/*
 * 到着の演出。
 *
 * 演出は因果関係を伝えるための飾りであり、**無くても機能は損なわれない**ことを確かめる。
 */

const RECT = {left: 10, top: 20, width: 300, height: 40} as DOMRect

afterEach(() => {
    vi.unstubAllGlobals()
})

describe('FlyingCard — 到着の演出', () => {
    it('読み上げ・フォーカス順に入らない（装飾のため）', () => {
        const {container} = render(<FlyingCard from={RECT} to={RECT} onDone={vi.fn()} />)
        const el = container.querySelector('.rw-fly')
        expect(el).not.toBeNull()
        expect(el).toHaveAttribute('aria-hidden', 'true')
        expect(screen.queryAllByRole('button')).toHaveLength(0)
    })

    it('動きを減らす設定を読み取る', () => {
        vi.stubGlobal('matchMedia', (q: string) => ({matches: q.includes('reduce'), media: q}))
        expect(prefersReducedMotion()).toBe(true)

        vi.stubGlobal('matchMedia', (q: string) => ({matches: false, media: q}))
        expect(prefersReducedMotion()).toBe(false)
    })

    it('matchMedia を持たない環境では動きを止めない（判定できないことで機能を落とさない）', () => {
        vi.stubGlobal('matchMedia', undefined)
        expect(prefersReducedMotion()).toBe(false)
    })
})
