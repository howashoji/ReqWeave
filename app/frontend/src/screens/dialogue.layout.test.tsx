import {render} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {AppShell} from '../ui/AppShell'
import './Dialogue.css'

/*
 * 対話画面の 2 ペインを**実描画で**確かめる。
 *
 * 0.1.2 の実機で、右ペイン（候補）を反映ボタンまで下へ送ると、ページ全体が縦に送られて
 * 左の対話ペインが画面外へ出て空白になった（利用者のスクリーンショット）。jsdom は高さを
 * 計算しないため単体テストでは見えない。ここでは Dialogue.css の規則を当てた**静的な骨組み**を
 * 実ブラウザで描き、寸法で判定する（Dialogue コンポーネント自体は描かない。多数のバインディングに
 * 依存するため、ここで見たいのは CSS の規則が作る結果）。
 */

vi.mock('../../wailsjs/go/binding/API', () => ({
    PaneWidths: vi.fn(() => Promise.resolve({versions: 220, checks: 360, min: 160, max: 640})),
    SetPaneWidths: vi.fn(() => Promise.resolve()),
}))

beforeEach(() => {
    document.documentElement.style.height = '100%'
    document.body.style.height = '100%'
})

/** 対話画面の骨組み（Dialogue.tsx と同じクラス構成。右ペインは画面の高さを超える量を置く）。 */
function renderDialogueFrame() {
    render(
        <div style={{height: '700px', width: '1200px'}}>
            <AppShell
                breadcrumb={['reqweave', 'ReqWeave Project', '要件定義']}
                nav={<button type="button">決定・未決</button>}
                completeness={{sections: [{label: '業務背景', percent: 50}]}}
                counts={{decided: 7, open: 0, candidates: 3}}
                effort="標準"
                model=""
                usage={{consumed: 40029, limit: null}}
            >
                <div className="rw-dialogue">
                    <section className="rw-dialogue__main" aria-label="対話">
                        <p className="rw-dialogue__state">承認待ち</p>
                        <div className="rw-dialogue__stream" aria-label="対話履歴">
                            {Array.from({length: 40}, (_, i) => (
                                <p key={i}>発話 {i + 1}: 回答の本文がここに入ります。</p>
                            ))}
                        </div>
                        <div className="rw-dialogue__composer">
                            <textarea aria-label="回答" rows={4} />
                        </div>
                    </section>
                    <aside className="rw-dialogue__side" aria-label="候補と完成度">
                        {Array.from({length: 30}, (_, i) => (
                            <p key={i}>候補 {i + 1}: 候補の本文がここに入ります。</p>
                        ))}
                        <button type="button">選んだ内容で反映</button>
                    </aside>
                </div>
            </AppShell>
        </div>,
    )
    const q = (selector: string) => {
        const el = document.querySelector(selector) as HTMLElement | null
        expect(el, `${selector} が無い`).not.toBeNull()
        return el!
    }
    return {
        shellMain: q('.rw-shell__main'),
        left: q('.rw-dialogue__main'),
        stream: q('.rw-dialogue__stream'),
        composer: q('.rw-dialogue__composer'),
        side: q('.rw-dialogue__side'),
    }
}

describe('対話画面の 2 ペイン（実描画）', () => {
    it('右ペインを最下部まで送っても、左の対話ペインは画面内に見えたままで、ページ全体は送られない', () => {
        const {shellMain, left, stream, composer, side} = renderDialogueFrame()

        // 前提: 右ペインの内容は画面本体の高さを超えている（空振りで緑にしない）。
        // ペイン自身の高さと比べない: 旧 CSS ではペインが内容の長さまで伸びるため、その比較だと
        // 欠陥そのものが「空振り」の文言で落ち、確かめたい下の検査まで届かない（caveat-testing）。
        expect(side.scrollHeight, '右ペインの内容が画面の高さを超えていない（検査が空振り）').toBeGreaterThan(
            shellMain.clientHeight,
        )

        // 右ペインはペインの中で送られ、画面本体（ページ）は縦に送られない。
        side.scrollTop = side.scrollHeight
        expect(side.scrollTop, '右ペインがペインの中でスクロールしない').toBeGreaterThan(0)
        expect(shellMain.scrollHeight - shellMain.clientHeight, '画面本体が縦に送られている').toBeLessThanOrEqual(1)

        // 左の対話ペインと回答欄は、画面本体の見えている範囲に収まっている。
        const view = shellMain.getBoundingClientRect()
        const leftBox = left.getBoundingClientRect()
        expect(leftBox.top, '左ペインの上端が画面外').toBeGreaterThanOrEqual(view.top - 1)
        expect(leftBox.bottom, '左ペインの下端が画面外').toBeLessThanOrEqual(view.bottom + 1)
        const composerBox = composer.getBoundingClientRect()
        expect(composerBox.bottom, '回答欄が画面外').toBeLessThanOrEqual(view.bottom + 1)

        // 履歴は左ペインの中でスクロールする。
        expect(stream.scrollHeight, '履歴が左ペインの中でスクロールしない').toBeGreaterThan(stream.clientHeight)
    })
})
