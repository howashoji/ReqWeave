import {render, screen} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {AppShell} from './AppShell'
import {DocumentWorkspace} from './DocumentWorkspace'
import {Overlay} from './Overlay'

/*
 * レイアウトの崩れを**実描画で**確かめる。
 *
 * jsdom は幅も高さも 0 のままなので、次の 2 つは単体テストでは検出できず、実機で人が見つけていた:
 *
 * - **横のはみ出し**（右ペインを縮めると入力欄が枠から出る）
 * - **縦の押し出し**（行を 1 つ足したら本体が縦を失い、上部ナビが画面外へ出た /
 *   重ねる面を流れへ挿し込んで作業領域が下がった）
 *
 * ここでは**実際に描かれた寸法**を見る。CSS の書き方ではなく結果を見るので、
 * 「まだ知らない崩れ方」も捕まえられる。
 */

const paneWidths = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    PaneWidths: paneWidths,
    SetPaneWidths: vi.fn(() => Promise.resolve()),
}))

/** 折り返せない長い文字列（ID・パスなど、実際にはみ出しの原因になる形）。 */
const LONG_ID = 'FR-INVENTORY-ALLOCATION-0001-VERY-LONG-IDENTIFIER-THAT-DOES-NOT-WRAP'

function overflowX(el: HTMLElement): number {
    return el.scrollWidth - el.clientWidth
}

beforeEach(() => {
    // 許容範囲の下限（160px）。**いちばん狭い状態**で見る。
    paneWidths.mockReset().mockResolvedValue({versions: 160, checks: 160, min: 160, max: 640})
    document.documentElement.style.height = '100%'
    document.body.style.height = '100%'
})

describe('レイアウト（実描画）', () => {
    it('3 ペインを下限まで狭めても、横にはみ出さない', async () => {
        render(
            <div style={{height: '600px'}}>
                <DocumentWorkspace
                    versionsPane={<p className="rw-mono">{LONG_ID}</p>}
                    documentPane={<p className="rw-mono">{LONG_ID}</p>}
                    checksPane={
                        <label>
                            ID グループ
                            <input defaultValue={LONG_ID} />
                        </label>
                    }
                    primaryAction={{label: '確定する', onClick: () => undefined}}
                />
            </div>,
        )
        // 幅はバインディングから届く（届くまでは既定値）。下限が反映されるまで待つ。
        const resizer = await screen.findByRole('separator', {name: '左のペインの幅'})
        await vi.waitFor(() => expect(resizer.getAttribute('aria-valuenow')).toBe('160'))

        const workspace = document.querySelector('.rw-doc') as HTMLElement
        expect(overflowX(workspace), '3 ペインの外枠が横にはみ出している').toBeLessThanOrEqual(0)
        for (const selector of ['.rw-doc__versions', '.rw-doc__body', '.rw-doc__checks']) {
            const pane = document.querySelector(selector) as HTMLElement
            expect(pane, `${selector} が無い`).not.toBeNull()
            expect(overflowX(pane), `${selector} が横にはみ出している`).toBeLessThanOrEqual(0)
        }
    })

    it('行が増えても本体は縦を失わず、上部ナビが画面内に留まる', () => {
        render(
            <div id="window" style={{height: '600px'}}>
                <AppShell
                    breadcrumb={['reqweave', '在庫管理システム', '要件定義']}
                    nav={
                        <>
                            {Array.from({length: 14}, (_, i) => (
                                <button key={i} type="button">
                                    ナビ {i + 1}
                                </button>
                            ))}
                        </>
                    }
                    completeness={{sections: [{label: '業務背景', percent: 50}]}}
                    counts={{decided: 7, open: 0, candidates: 5}}
                    effort="標準"
                    model=""
                    usage={{consumed: 29946, limit: null}}
                    guide={{
                        stageLabel: '資料を取り込む',
                        stageIndex: 1,
                        stageTotal: 6,
                        next: '資料を分析します。',
                        cycleStep: '承認',
                    }}
                >
                    {/* 中身が長い画面（一覧が伸びる状況） */}
                    {Array.from({length: 200}, (_, i) => (
                        <p key={i}>行 {i + 1}</p>
                    ))}
                </AppShell>
            </div>,
        )
        const window600 = document.querySelector('#window') as HTMLElement
        const shell = document.querySelector('.rw-shell') as HTMLElement
        const header = document.querySelector('.rw-shell__header') as HTMLElement
        const main = document.querySelector('.rw-shell__main') as HTMLElement
        const status = document.querySelector('.rw-statusline') as HTMLElement
        const frame = window600.getBoundingClientRect()

        // 本体は縦の余りを取り、その中でスクロールする。
        expect(main.clientHeight, '本体の高さが無い（縦を他の行に取られている）').toBeGreaterThan(100)
        expect(main.scrollHeight, '本体の中身が本体より高くない（検査が空振りしている）').toBeGreaterThan(
            main.clientHeight,
        )
        // **シェルは窓に収まる**（内容が長くてもシェルごと伸びない。以前の不具合の症状）。
        expect(
            shell.getBoundingClientRect().height,
            'シェルが窓より高い（中身の量でシェルごと伸びている）',
        ).toBeLessThanOrEqual(frame.height + 1)
        // 上部ナビと下端の状態行は窓の中に留まる（押せなくならない）。
        for (const [name, el] of [
            ['上部ナビ', header],
            ['状態行', status],
        ] as const) {
            const box = el.getBoundingClientRect()
            expect(box.top, `${name} が窓の上へ出ている`).toBeGreaterThanOrEqual(frame.top - 1)
            expect(box.bottom, `${name} が窓の下へ出ている`).toBeLessThanOrEqual(frame.bottom + 1)
        }
    })

    it('重ねる面を開いても作業領域は動かない', () => {
        function Screen({open}: {open: boolean}) {
            return (
                <div style={{height: '600px'}}>
                    <AppShell
                        breadcrumb={['reqweave']}
                        nav={<button type="button">ナビ</button>}
                        completeness={{sections: [{label: '業務背景', percent: 50}]}}
                        counts={{decided: 1, open: 0, candidates: 0}}
                        effort="標準"
                        model=""
                        usage={{consumed: 0, limit: null}}
                    >
                        {open ? (
                            // 工程ガイドと同じ置き方（開く操作の真下へ重ねる）で確かめる。
                            <Overlay label="工程の全体像" anchored onClose={() => undefined}>
                                {Array.from({length: 6}, (_, i) => (
                                    <p key={i}>工程 {i + 1}</p>
                                ))}
                            </Overlay>
                        ) : null}
                        {/* 余白の相殺で位置が動かないよう、余白を持たない要素で測る。 */}
                        <div id="work">作業領域の先頭</div>
                    </AppShell>
                </div>
            )
        }
        const {rerender} = render(<Screen open={false} />)
        const before = (document.querySelector('#work') as HTMLElement).getBoundingClientRect().top

        rerender(<Screen open />)
        expect(screen.getByRole('group', {name: '工程の全体像'})).toBeInTheDocument()
        const after = (document.querySelector('#work') as HTMLElement).getBoundingClientRect().top
        expect(after, '重ねる面が作業領域を押し下げている').toBe(before)
    })
})
