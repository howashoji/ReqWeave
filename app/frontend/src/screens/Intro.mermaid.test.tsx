import {render, waitFor} from '@testing-library/react'
import {beforeAll, expect, test} from 'vitest'
import {Markdown} from '../ui/Markdown'
import {INTRO_PAGES} from './Intro'

/*
 * 紹介スライドの図に文字が出ることを、実物の mermaid で確かめる。
 *
 * 2026-09-04、実機で 3 つの図がすべて「四角と線だけ」になった（DOMPurify が
 * mermaid の foreignObject を中身ごと落としていた）。Intro.test.tsx は
 * 文面の規約を見るだけで図の中身を見ておらず、素通りした。
 */

// jsdom は SVG の寸法計算を実装しないため、mermaid のレイアウトに最低限を補う。
beforeAll(async () => {
    // mermaid のモジュールは先に読み込んでおく（Markdown は図を描くたびに import するが、読み込み済みならそれが返る）。
    // 読み込みは依存を含めて重く、全テストの並列実行で CPU が混むと数秒かかる
    // （実測: 単独 約 0.2 秒 / 高負荷 3.6〜5.0 秒）。これを下の waitFor の待ち時間（5 秒）に含めると、
    // 描画の不具合ではなく読み込みの遅さで時間切れになる。読み込みはフックの待ち時間（15 秒）で待ち、
    // waitFor には描画そのものだけを待たせる。
    await import('mermaid')

    const proto = window.SVGElement.prototype as unknown as Record<string, unknown>
    proto.getBBox = function () {
        return {x: 0, y: 0, width: 100, height: 20}
    }
    proto.getComputedTextLength = function () {
        return 100
    }
})

/** 各ページの図に必ず出ていなければならない語（本文の mermaid 定義から採った）。 */
const REQUIRED: Record<string, string[]> = {
    'できること': ['対話', '質問票', '基本設計'],
    'チームでの進めかた': ['参加する', '取り込む', '反映する'],
    'ファイルの扱い': ['自分の作業コピー', '同期先', 'ほかの人のぶんの置き場'],
}

test.each(Object.keys(REQUIRED))('紹介スライド「%s」の図に文字が出る', async (title) => {
    const page = INTRO_PAGES.find((p) => p.title === title)
    expect(page, `ページ「${title}」が見つからない`).toBeDefined()
    expect(page!.body).toContain('```mermaid')

    const {container} = render(<Markdown source={page!.body} />)
    await waitFor(() => {
        expect(container.querySelector('svg')).not.toBeNull()
    })
    const text = container.querySelector('svg')!.textContent ?? ''
    for (const label of REQUIRED[title]) {
        expect(text).toContain(label)
    }
})

// 図を持つページが 3 つあること（本テストの対象が減っていないことの確認）。
test('図を持つページは 3 つ', () => {
    const withDiagram = INTRO_PAGES.filter((p) => p.body.includes('```mermaid'))
    expect(withDiagram.map((p) => p.title)).toEqual(Object.keys(REQUIRED))
})
