import {render, screen, waitFor} from '@testing-library/react'
import {beforeAll, expect, test} from 'vitest'
import DOMPurify from 'dompurify'
import {Markdown} from './Markdown'

/*
 * mermaid の実物を使った描画テスト。
 *
 * Markdown.test.tsx は mermaid をモックしており、**サニタイズ後に文字が残るか**を見ていない。
 * そのため「DOMPurify が foreignObject を中身ごと落として、図の文字が全部消える」不具合を
 * 素通りさせた（2026-09-04 に利用者の実機で発覚。四角と線だけが描かれた）。
 * ここでは mermaid を差し替えず、実際に描いた SVG をサニタイズに通して文字を確かめる。
 */

// jsdom は SVG の寸法計算（getBBox / getComputedTextLength）を実装しないため、
// mermaid のレイアウト計算が落ちる。描画そのものは本題ではないので最小限を補う。
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
    proto.getScreenCTM = function () {
        return {a: 1, b: 0, c: 0, d: 1, e: 0, f: 0, inverse: () => ({a: 1, b: 0, c: 0, d: 1, e: 0, f: 0})}
    }
})

const SOURCE = [
    '```mermaid',
    'flowchart LR',
    '    A["対話"] --> B["決定・未決事項の整理"]',
    '    B -. わからないこと .-> Q["質問票"]',
    '```',
].join('\n')

// 受け入れ条件: ノード・エッジのラベルがサニタイズ後の SVG に文字として残ること。
test('図のラベルがサニタイズ後も文字として残る', async () => {
    const {container} = render(<Markdown source={SOURCE} />)

    await waitFor(() => {
        expect(container.querySelector('svg')).not.toBeNull()
    })
    const svg = container.querySelector('svg')!
    const text = svg.textContent ?? ''

    for (const label of ['対話', '決定・未決事項の整理', '質問票', 'わからないこと']) {
        expect(text).toContain(label)
    }
    // 描画に失敗したときの退避表示（記述をそのまま出す）になっていないこと。
    expect(screen.queryByText('一部の図を描画できませんでした。記述をそのまま表示しています。')).toBeNull()
    expect(container.querySelector('.rw-markdown__mermaid--raw')).toBeNull()
})

// <br/> を含むラベルが改行して表示されること（紹介スライド「ファイルの扱い」で使っている）。
test('ラベル中の <br/> が改行として扱われる', async () => {
    const source = [
        '```mermaid',
        'flowchart LR',
        '    MY["自分の作業コピー<br/>（自分の端末）"] --> B1["自分のぶんの置き場"]',
        '```',
    ].join('\n')
    const {container} = render(<Markdown source={source} />)

    await waitFor(() => {
        expect(container.querySelector('svg')).not.toBeNull()
    })
    const text = container.querySelector('svg')!.textContent ?? ''
    expect(text).toContain('自分の作業コピー')
    expect(text).toContain('（自分の端末）')
    // <br/> がそのまま文字として出ていないこと。
    expect(text).not.toContain('<br/>')
})

/*
 * なぜ htmlLabels を無効にしたのかを、サニタイザ側の実挙動で固定する。
 * DOMPurify は foreignObject を **中身ごと** 落とす（svgDisallowed と DEFAULT_FORBID_CONTENTS の双方）。
 * したがって「ラベルを HTML で描く」設定に戻すと、必ず文字が消える。
 */
test('DOMPurify は foreignObject の中身を残さない（htmlLabels を使えない理由）', () => {
    const svg =
        '<svg xmlns="http://www.w3.org/2000/svg"><g>' +
        '<foreignObject width="100" height="40"><div xmlns="http://www.w3.org/1999/xhtml">' +
        '<span class="nodeLabel">消えるラベル</span></div></foreignObject></g></svg>'
    expect(DOMPurify.sanitize(svg)).not.toContain('消えるラベル')
})
