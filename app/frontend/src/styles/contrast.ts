/*
 * WCAG 2.1 の相対輝度とコントラスト比（配色が WCAG AA を満たすことの検証に使う）。
 * 定義: https://www.w3.org/TR/WCAG21/#dfn-relative-luminance
 */

/** #rrggbb を 0-255 の三値へ。 */
export function parseHex(hex: string): [number, number, number] {
    const m = /^#([0-9a-f]{6})$/i.exec(hex.trim())
    if (!m) {
        throw new Error(`16 進 6 桁の色ではありません: ${hex}`)
    }
    const v = m[1]
    return [parseInt(v.slice(0, 2), 16), parseInt(v.slice(2, 4), 16), parseInt(v.slice(4, 6), 16)]
}

function channel(value: number): number {
    const c = value / 255
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
}

/** 相対輝度。 */
export function relativeLuminance(hex: string): number {
    const [r, g, b] = parseHex(hex)
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

/** コントラスト比（1〜21）。 */
export function contrastRatio(a: string, b: string): number {
    const la = relativeLuminance(a)
    const lb = relativeLuminance(b)
    const hi = Math.max(la, lb)
    const lo = Math.min(la, lb)
    return (hi + 0.05) / (lo + 0.05)
}
