import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {UpdateNotice} from './UpdateNotice'
import type {binding} from '../../wailsjs/go/models'

function state(over: Partial<binding.UpdateState>): binding.UpdateState {
    return {status: 'idle', currentVersion: '0.1.0', ...over} as binding.UpdateState
}

const noop = () => undefined

describe('更新通知', () => {
    it('idle では何も出さない', () => {
        const {container} = render(
            <UpdateNotice state={state({status: 'idle'})} onApply={noop} onDefer={noop} onCancel={noop} onRestart={noop} />,
        )
        expect(container).toBeEmptyDOMElement()
    })

    it('state が無いときも何も出さない', () => {
        const {container} = render(
            <UpdateNotice state={null} onApply={noop} onDefer={noop} onCancel={noop} onRestart={noop} />,
        )
        expect(container).toBeEmptyDOMElement()
    })

    // 受け入れ条件: 新版検知時に通知が出て「更新する / 後で」を選べること
    it('新版があると現在の版と新しい版を示し、更新するか後でを選べる', () => {
        const onApply = vi.fn()
        const onDefer = vi.fn()
        render(
            <UpdateNotice
                state={state({status: 'available', newVersion: '0.2.0'})}
                onApply={onApply}
                onDefer={onDefer}
                onCancel={noop}
                onRestart={noop}
            />,
        )
        expect(screen.getByText('新しい版が公開されています')).toBeInTheDocument()
        expect(screen.getByText('0.1.0')).toBeInTheDocument()
        expect(screen.getByText('0.2.0')).toBeInTheDocument()

        fireEvent.click(screen.getByRole('button', {name: '更新する'}))
        expect(onApply).toHaveBeenCalledTimes(1)

        fireEvent.click(screen.getByRole('button', {name: '後で'}))
        expect(onDefer).toHaveBeenCalledTimes(1)
    })

    // 受け入れ条件: 同意操作の前に取得が始まらない（通知の時点で取得系の操作を呼ばない）
    it('通知を出しただけでは更新の実行を呼ばない', () => {
        const onApply = vi.fn()
        render(
            <UpdateNotice
                state={state({status: 'available', newVersion: '0.2.0'})}
                onApply={onApply}
                onDefer={noop}
                onCancel={noop}
                onRestart={noop}
            />,
        )
        expect(onApply).not.toHaveBeenCalled()
    })

    // 受け入れ条件: 進行状態（確認中 / ダウンロード中 / 検証中 / 再起動待ち）が表示されること
    it.each([
        ['checking', '新しい版を確認しています'],
        ['downloading', '更新ファイルを取得しています'],
        ['verifying', '更新ファイルを確認しています'],
        ['restart_required', '更新の準備ができました'],
    ])('進行状態 %s を表示する', (status, title) => {
        render(
            <UpdateNotice
                state={state({status: status as binding.UpdateState['status'], newVersion: '0.2.0'})}
                onApply={noop}
                onDefer={noop}
                onCancel={noop}
                onRestart={noop}
            />,
        )
        expect(screen.getByText(title)).toBeInTheDocument()
    })

    it('取得中は中止できる', () => {
        const onCancel = vi.fn()
        render(
            <UpdateNotice
                state={state({status: 'downloading', newVersion: '0.2.0'})}
                onApply={noop}
                onDefer={noop}
                onCancel={onCancel}
                onRestart={noop}
            />,
        )
        fireEvent.click(screen.getByRole('button', {name: '中止する'}))
        expect(onCancel).toHaveBeenCalledTimes(1)
    })

    // 受け入れ条件: 自動で再起動しない（利用者の操作で再起動する）
    it('適用後は再起動を促し、押したときだけ再起動を呼ぶ', () => {
        const onRestart = vi.fn()
        render(
            <UpdateNotice
                state={state({status: 'restart_required', newVersion: '0.2.0'})}
                onApply={noop}
                onDefer={noop}
                onCancel={noop}
                onRestart={onRestart}
            />,
        )
        expect(onRestart).not.toHaveBeenCalled()
        fireEvent.click(screen.getByRole('button', {name: '再起動する'}))
        expect(onRestart).toHaveBeenCalledTimes(1)
    })

    // 受け入れ条件: 失敗時は原因＋次の行動を示し、現行版のまま使える旨を出す
    it('失敗時はバインディングの文言と、現行版のまま使える旨を出す', () => {
        render(
            <UpdateNotice
                state={state({
                    status: 'failed',
                    message: '更新ファイルの発行元を確認できないため、現行版のまま更新を中止しました。時間をおいて再実行してください。',
                })}
                onApply={noop}
                onDefer={noop}
                onCancel={noop}
                onRestart={noop}
            />,
        )
        expect(screen.getByText('更新できませんでした')).toBeInTheDocument()
        expect(screen.getByText(/発行元を確認できない/)).toBeInTheDocument()
        expect(screen.getByText(/のまま使い続けられます/)).toBeInTheDocument()
    })

    // 生のコード値を画面に出さない（利用者に内部の値を見せない）
    it('状態のコード値を画面に出さない', () => {
        for (const status of ['checking', 'available', 'downloading', 'verifying', 'restart_required', 'failed']) {
            const {container, unmount} = render(
                <UpdateNotice
                    state={state({status: status as binding.UpdateState['status'], newVersion: '0.2.0', message: 'x。'})}
                    onApply={noop}
                    onDefer={noop}
                    onCancel={noop}
                    onRestart={noop}
                />,
            )
            expect(container.textContent).not.toContain(status)
            unmount()
        }
    })

    // 未知の状態でも生のコード値を出さず、落ちない
    it('未知の状態でも生のコード値を出さない', () => {
        render(
            <UpdateNotice
                state={state({status: 'some_new_status' as binding.UpdateState['status']})}
                onApply={noop}
                onDefer={noop}
                onCancel={noop}
                onRestart={noop}
            />,
        )
        expect(screen.getByText('更新の状態を確認できません')).toBeInTheDocument()
        expect(document.body.textContent).not.toContain('some_new_status')
    })
})
