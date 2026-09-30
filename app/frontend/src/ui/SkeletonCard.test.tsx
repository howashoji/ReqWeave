import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {SkeletonCard} from './SkeletonCard'

/*
 * 抽出中のスケルトンカード。
 *
 * ねらいは「ここが承認と破棄をする場所だ」を**形で**伝えること。
 * 中身の棒だけでは「読む場所」に見えるため、操作の輪郭まで描いているかを確かめる。
 */

describe('SkeletonCard — 抽出中の型見せ', () => {
    it('承認 / 破棄の輪郭を描く（何をする場所かを形で伝える）', () => {
        render(<SkeletonCard />)
        const card = document.querySelector('.rw-skeleton')
        expect(card).not.toBeNull()
        expect(card!.textContent).toContain('承認')
        expect(card!.textContent).toContain('破棄')
    })

    it('押せないものをフォーカス順・読み上げに入れない', () => {
        render(<SkeletonCard />)
        expect(document.querySelector('.rw-skeleton')).toHaveAttribute('aria-hidden', 'true')
        // 輪郭はボタンではない（Tab で止まらない）。
        expect(screen.queryAllByRole('button')).toHaveLength(0)
        expect(document.querySelectorAll('.rw-skeleton button')).toHaveLength(0)
    })
})
