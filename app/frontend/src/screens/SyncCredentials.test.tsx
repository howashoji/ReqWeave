import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {SyncCredentials} from './SyncCredentials'

/**
 * 同期先の認証情報の画面テスト。
 *
 * **平文の再表示機能を持たない**こと、共有フォルダでは入力を求めないこと、接続確認の結果を
 * 区別して示すことを固定する。
 */

const syncCredentialView = vi.hoisted(() => vi.fn())
const registerSyncCredential = vi.hoisted(() => vi.fn())
const deleteSyncCredential = vi.hoisted(() => vi.fn())
const checkSyncConnection = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    SyncCredentialView: syncCredentialView,
    RegisterSyncCredential: registerSyncCredential,
    DeleteSyncCredential: deleteSyncCredential,
    CheckSyncConnection: checkSyncConnection,
}))

const KINDS = [
    {kind: 'ssh_key', label: 'SSH 鍵', hint: 'パスフレーズなしの秘密鍵の内容を貼り付けてください。'},
    {kind: 'token', label: 'アクセストークン', hint: 'https:// の同期先で使うトークンを入力してください。'},
]

const UNSET = {
    projectId: 'P-1',
    registered: false,
    kind: '',
    kindLabel: '',
    state: '未設定',
    masked: '—',
    kinds: KINDS,
}

describe('同期先の認証情報', () => {
    beforeEach(() => {
        syncCredentialView.mockReset()
        syncCredentialView.mockResolvedValue({...UNSET})
        registerSyncCredential.mockReset()
        registerSyncCredential.mockResolvedValue({...UNSET, registered: true, state: '登録済み'})
        deleteSyncCredential.mockReset()
        checkSyncConnection.mockReset()
    })

    // 登録できるが、平文を再表示しない。
    it('登録すると状態だけが変わり、入力値は画面に残らない', async () => {
        render(<SyncCredentials projectId="P-1" onBack={() => undefined} backLabel="設定へ戻る" />)
        expect(await screen.findByText('未設定')).toBeInTheDocument()

        const input = (await screen.findByLabelText('SSH 秘密鍵')) as HTMLTextAreaElement
        fireEvent.change(input, {target: {value: 'dummy-ssh-key-body-for-test'}})
        syncCredentialView.mockResolvedValue({
            ...UNSET,
            registered: true,
            kind: 'ssh_key',
            kindLabel: 'SSH 鍵',
            state: '登録済み',
            masked: '********',
        })
        fireEvent.click(screen.getByRole('button', {name: '登録する'}))
        await waitFor(() =>
            expect(registerSyncCredential).toHaveBeenCalledWith({
                projectId: 'P-1',
                kind: 'ssh_key',
                username: '',
                secret: 'dummy-ssh-key-body-for-test',
            }),
        )
        expect(await screen.findByText('登録済み')).toBeInTheDocument()
        // 入力欄は空に戻り、値の再表示経路が無い
        expect((screen.getByLabelText('SSH 秘密鍵') as HTMLTextAreaElement).value).toBe('')
        expect(screen.queryByText('dummy-ssh-key-body-for-test')).not.toBeInTheDocument()
    })

    // 共有フォルダの同期先では登録が要らない旨を示し、入力を求めない。
    it('共有フォルダでは入力を求めない', async () => {
        render(<SyncCredentials projectId="P-1" requiresCredential={false} onBack={() => undefined} backLabel="設定へ戻る" />)
        expect(await screen.findByText(/認証情報の登録は要りません/)).toBeInTheDocument()
        expect(screen.queryByRole('button', {name: '登録する'})).not.toBeInTheDocument()
    })

    // 接続確認は認証エラー / 到達不能 / その他を区別して示す。
    it('接続確認の結果を示す', async () => {
        checkSyncConnection.mockResolvedValue({ok: true, notice: '同期先へ接続できました。'})
        render(<SyncCredentials projectId="P-1" onBack={() => undefined} backLabel="設定へ戻る" />)
        fireEvent.click(await screen.findByRole('button', {name: '接続を確認する'}))
        expect(await screen.findByText('同期先へ接続できました。')).toBeInTheDocument()
    })

    // 未登録のときは削除を無効化して理由を示す。
    it('未登録では削除を無効化する', async () => {
        render(<SyncCredentials projectId="P-1" onBack={() => undefined} backLabel="設定へ戻る" />)
        const remove = await screen.findByRole('button', {name: '削除する'})
        expectDisabledReason(remove, '登録されていません。')
    })
})
