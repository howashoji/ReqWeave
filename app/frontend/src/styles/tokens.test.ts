import {describe, expect, it} from 'vitest'
// CSS を文字列として読み込む（Node の fs を使わない。フロントエンドは直接ファイルを読まない依存規則）
import tokensCss from './tokens.css?raw'
import {contrastRatio} from './contrast'

/** 本文テキストに求めるコントラスト比（WCAG AA）。 */
const AA_TEXT = 4.5

/** カラートークン 13 種（名前・順序ともに tokens.css と同じ）。 */
const COLOR_TOKENS = [
    'bg', 'bg-sub', 'surface', 'panel', 'border',
    'text', 'text-sub', 'muted', 'faint',
    'accent', 'warn', 'danger', 'info',
] as const

const BACKGROUNDS = ['bg', 'bg-sub', 'surface', 'panel'] as const
const STATE_COLORS = ['accent', 'warn', 'danger', 'info'] as const

type Tokens = Record<string, string>

/** tokens.css から 1 セレクタ分のカスタムプロパティを取り出す。 */
function block(selector: string): Tokens {
    const start = tokensCss.indexOf(selector)
    expect(start, `セレクタ ${selector} が tokens.css にありません`).toBeGreaterThanOrEqual(0)
    const open = tokensCss.indexOf('{', start)
    const close = tokensCss.indexOf('}', open)
    const body = tokensCss.slice(open + 1, close)

    const tokens: Tokens = {}
    for (const line of body.split('\n')) {
        const m = /^\s*--rw-([a-z0-9-]+)\s*:\s*([^;]+);/.exec(line)
        if (m) {
            tokens[m[1]] = m[2].trim()
        }
    }
    return tokens
}

const dark = block(':root {')
const light = block(":root[data-theme='light']")

const themes: Array<[string, Tokens]> = [['ダーク', dark], ['ライト', light]]

describe('カラートークン', () => {
    it.each(themes)('%s: 13 種のカラートークンがすべて 16 進 6 桁で定義されている', (_name, tokens) => {
        for (const token of COLOR_TOKENS) {
            expect(tokens[token], `--rw-${token} が未定義`).toMatch(/^#[0-9a-f]{6}$/i)
        }
    })

    it('ダークとライトは同じトークン集合を持つ（差し替えのみで実現する）', () => {
        // ライトはカラートークンのみを差し替える。タイポグラフィ・サイズ・形はダーク側の定義を継承する。
        const lightNames = Object.keys(light).sort()
        const darkColorNames = Object.keys(dark)
            .filter((n) => /^#/.test(dark[n]))
            .sort()
        expect(lightNames).toEqual(darkColorNames)
    })
})

describe('コントラスト比（本文テキストは 4.5:1 以上・両テーマ）', () => {
    it.each(themes)('%s: text / text-sub / muted はすべての背景に対して 4.5:1 以上', (_name, tokens) => {
        for (const fg of ['text', 'text-sub', 'muted'] as const) {
            for (const bg of BACKGROUNDS) {
                const ratio = contrastRatio(tokens[fg], tokens[bg])
                expect(ratio, `${fg} on ${bg} = ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(AA_TEXT)
            }
        }
    })

    it.each(themes)('%s: 状態色 4 色はすべての背景に対して 4.5:1 以上', (_name, tokens) => {
        for (const fg of STATE_COLORS) {
            for (const bg of BACKGROUNDS) {
                const ratio = contrastRatio(tokens[fg], tokens[bg])
                expect(ratio, `${fg} on ${bg} = ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(AA_TEXT)
            }
        }
    })

    it.each(themes)('%s: 状態チップは「状態色の文字 + 淡い同系背景」で 4.5:1 以上', (_name, tokens) => {
        for (const state of STATE_COLORS) {
            const chipBg = tokens[`${state}-chip-bg`]
            expect(chipBg, `--rw-${state}-chip-bg が未定義`).toMatch(/^#[0-9a-f]{6}$/i)
            const ratio = contrastRatio(tokens[state], chipBg)
            expect(ratio, `${state} on ${state}-chip-bg = ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(AA_TEXT)
        }
    })

    it.each(themes)('%s: プライマリ操作（緑塗り）の文字が 4.5:1 以上', (_name, tokens) => {
        const ratio = contrastRatio(tokens['on-accent'], tokens['accent'])
        expect(ratio, `on-accent on accent = ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(AA_TEXT)
    })
})

describe('faint の用途制限', () => {
    // faint は 2.2〜2.4:1 で本文基準を満たさない。区切り線・非テキストにのみ使う。
    // 見出し行の小ラベルのような小さな文字にも faint ではなく muted を使う。
    const cssFiles = import.meta.glob('../**/*.css', {eager: true, query: '?raw', import: 'default'})

    it('どの CSS も文字色に faint を使っていない', () => {
        const offenders: string[] = []
        for (const [path, source] of Object.entries(cssFiles)) {
            const text = source as string
            for (const line of text.split('\n')) {
                if (/(^|[^-])color\s*:\s*var\(\s*--rw-faint\s*\)/.test(line)) {
                    offenders.push(`${path}: ${line.trim()}`)
                }
            }
        }
        expect(offenders, 'faint を文字色に使っている箇所').toEqual([])
    })
})
