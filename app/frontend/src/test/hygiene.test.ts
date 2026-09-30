import {describe, expect, it} from 'vitest'

/**
 * テストの書き方の規約。
 *
 * 検証ゲートを不定期に落とす原因は、実装ではなく**テストの待ち方**であることが多い。
 * 実測した 2 つの型を機械的に塞ぐ:
 *
 * 1. `waitFor(() => expect(<モック>).toHaveBeenCalled…)` は**呼ばれたこと**までしか待たない。
 *    その解決を受けた再描画（処理中の解除・一覧の到着）はまだ起きていないことがある。
 *    その状態で次のボタンを押すと、**無効なボタンへの click は黙って捨てられ**、
 *    後続の待ちが時間切れになる（実装に欠陥がなくてもゲートが赤くなる = 偽赤）。
 * 2. 対策は `clickEnabled` / `clickEnabledBy`（`src/test/interact.ts`）。
 *    「押せるようになるまで待ってから押す」を 1 か所に寄せてある。
 */

const tests = import.meta.glob('../**/*.test.tsx', {eager: true, query: '?raw', import: 'default'}) as Record<
    string,
    string
>

/** it(...) ブロックの単位で本文を切り出す（入れ子の describe は見ない）。 */
function itBlocks(text: string): string[] {
    const out: string[] = []
    const marks = [...text.matchAll(/\n\s{4}it\(/g)].map((m) => m.index ?? 0)
    for (let i = 0; i < marks.length; i += 1) {
        out.push(text.slice(marks[i], marks[i + 1] ?? text.length))
    }
    return out
}

describe('テストの待ち方の規約', () => {
    it('検査対象のテストを実際に読み込めている（空振りで緑にならないこと）', () => {
        expect(Object.keys(tests).length).toBeGreaterThanOrEqual(20)
    })

    it('「呼ばれたことだけを待った」あとにボタンを直接押さない（clickEnabled を使う）', () => {
        const waited = /waitFor\(\s*\(\)\s*=>\s*\n?\s*expect\([^)]*\)\.toHaveBeenCalled/
        const rawClick = /fireEvent\.click\([^\n]*getByRole\([^\n]*button/
        const offenders: string[] = []
        for (const [path, text] of Object.entries(tests)) {
            for (const block of itBlocks(text)) {
                const at = block.search(waited)
                if (at < 0) {
                    continue
                }
                const rest = block.slice(at)
                const hit = rest.match(rawClick)
                if (hit) {
                    offenders.push(`${path}: ${hit[0].trim()}`)
                }
            }
        }
        expect(
            offenders,
            'モックが呼ばれた直後の再描画を待たずにボタンを押している（clickEnabled / clickEnabledBy を使うこと）',
        ).toEqual([])
    })
})
