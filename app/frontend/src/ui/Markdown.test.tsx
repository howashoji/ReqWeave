import {render, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Markdown} from './Markdown'

// mermaid は重いので描画そのものは差し替える。ここで確かめるのは「どのテーマで初期化したか」。
const initialize = vi.hoisted(() => vi.fn())
const renderDiagram = vi.hoisted(() => vi.fn(async () => ({svg: '<svg data-testid="diagram"></svg>'})))
vi.mock('mermaid', () => ({default: {initialize, render: renderDiagram}}))

const SOURCE = '# 見出し\n\n```mermaid\nflowchart LR\n  A --> B\n```'

describe('Markdown — 図の配色は画面テーマに追従する', () => {
    beforeEach(() => {
        initialize.mockClear()
        renderDiagram.mockClear()
        document.documentElement.removeAttribute('data-theme')
    })

    it('既定（ダーク）ではダークで初期化する', async () => {
        render(<Markdown source={SOURCE} />)

        await waitFor(() => expect(initialize).toHaveBeenCalled())
        expect(initialize.mock.calls[0][0]).toMatchObject({theme: 'dark'})
    })

    it('ライトテーマではライト側で初期化する（図だけがダークのまま残らない）', async () => {
        document.documentElement.setAttribute('data-theme', 'light')

        render(<Markdown source={SOURCE} />)

        await waitFor(() => expect(initialize).toHaveBeenCalled())
        expect(initialize.mock.calls[0][0]).toMatchObject({theme: 'default'})
    })

    it('外部から読み込まず、securityLevel は strict のまま（外部へ通信しない）', async () => {
        render(<Markdown source={SOURCE} />)

        await waitFor(() => expect(initialize).toHaveBeenCalled())
        expect(initialize.mock.calls[0][0]).toMatchObject({startOnLoad: false, securityLevel: 'strict'})
    })
})
