import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {SyncRemote} from './SyncRemote'

/**
 * 同期先の設定の画面テスト。
 *
 * 外部 Git サーバは明示表示と同意操作を経ること、オーナー以外は無効化＋理由表示に
 * なること（ほかの管理画面と同じ方式）を固定する。
 */

const syncRemote = vi.hoisted(() => vi.fn())
const setSyncRemote = vi.hoisted(() => vi.fn())
const checkSyncConnection = vi.hoisted(() => vi.fn())
const chooseFolder = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    SyncRemote: syncRemote,
    SetSyncRemote: setSyncRemote,
    CheckSyncConnection: checkSyncConnection,
    ChooseFolder: chooseFolder,
}))

const KINDS = [
    {
        kind: 'folder',
        label: '共有フォルダ上のリポジトリ',
        hint: '共有フォルダ上のフォルダのパスを指定します。',
        requiresConsent: false,
        requiresCredential: false,
    },
    {
        kind: 'git_internal',
        label: '社内 Git サーバ',
        hint: 'https:// または ssh:// の URL で指定します。',
        requiresConsent: false,
        requiresCredential: true,
    },
    {
        kind: 'git_external',
        label: '外部 Git サーバ',
        hint: 'https:// または ssh:// の URL で指定します。',
        requiresConsent: true,
        consentText:
            '外部 Git サーバを同期先にすると、要件定義データが利用組織の外部が運用するホストへ保管されます。',
        requiresCredential: true,
    },
]

const VIEW = {
    configured: false,
    kind: 'folder',
    kinds: KINDS,
    canManage: true,
    credential: {projectId: 'P-1', registered: false, kind: '', kindLabel: '', state: '未設定', masked: '—', kinds: []},
    notice: '同期先はまだ設定されていません。',
}

function renderRemote() {
    render(<SyncRemote onBack={() => undefined} onOpenCredentials={() => undefined} />)
}

describe('同期先の設定', () => {
    beforeEach(() => {
        syncRemote.mockReset()
        syncRemote.mockResolvedValue({...VIEW})
        setSyncRemote.mockReset()
        setSyncRemote.mockResolvedValue({...VIEW, configured: true, kind: 'folder', location: '/share/proj.git'})
        checkSyncConnection.mockReset()
        chooseFolder.mockReset()
    })

    // 種別を選び、所在を入力して保存できる（既定は共有フォルダ）。
    it('共有フォルダの同期先を保存できる', async () => {
        renderRemote()
        expect(await screen.findByRole('radio', {name: /共有フォルダ上のリポジトリ/})).toBeChecked()

        fireEvent.change(screen.getByLabelText('同期先の所在'), {target: {value: '/share/proj.git'}})
        fireEvent.click(screen.getByRole('button', {name: 'この同期先を保存する'}))
        await waitFor(() =>
            expect(setSyncRemote).toHaveBeenCalledWith({
                kind: 'folder',
                location: '/share/proj.git',
                externalConsent: false,
            }),
        )
        expect(await screen.findByText(/反映して初めて他のメンバーへ及びます/)).toBeInTheDocument()
    })

    // 外部 Git サーバは明示表示と同意なしに保存しない。
    it('外部 Git サーバは同意するまで保存できない', async () => {
        renderRemote()
        fireEvent.click(await screen.findByRole('radio', {name: /外部 Git サーバ/}))
        fireEvent.change(screen.getByLabelText('同期先の所在'), {
            target: {value: 'https://git.example.com/proj.git'},
        })
        expect(screen.getByText(/利用組織の外部が運用するホストへ保管されます/)).toBeInTheDocument()

        const save = screen.getByRole('button', {name: 'この同期先を保存する'})
        expect(save).toBeDisabled()
        expect(setSyncRemote).not.toHaveBeenCalled()

        fireEvent.click(screen.getByRole('checkbox', {name: '上記の内容に同意します'}))
        fireEvent.click(screen.getByRole('button', {name: 'この同期先を保存する'}))
        await waitFor(() =>
            expect(setSyncRemote).toHaveBeenCalledWith({
                kind: 'git_external',
                location: 'https://git.example.com/proj.git',
                externalConsent: true,
            }),
        )
    })

    // オーナー以外は無効化して理由を示す（操作を隠さない）。
    it('オーナー以外は無効化して理由を示す', async () => {
        syncRemote.mockResolvedValue({
            ...VIEW,
            canManage: false,
            manageReason: '同期先の設定はオーナー権限が必要です（現在は編集）。オーナーに権限の変更を依頼してください。',
        })
        renderRemote()
        expect(await screen.findByText(/同期先の設定はオーナー権限が必要です/)).toBeInTheDocument()
        expect(screen.getByRole('radio', {name: /共有フォルダ上のリポジトリ/})).toBeDisabled()
        expect(screen.getByLabelText('同期先の所在')).toBeDisabled()
        expect(screen.getByRole('button', {name: 'この同期先を保存する'})).toBeDisabled()
    })

    // 接続確認は結果を区別して示す（生のエラー文言は折りたたみ）。
    it('接続確認の結果を示す', async () => {
        checkSyncConnection.mockResolvedValue({
            ok: false,
            failure: {
                kindLabel: '認証できなかった',
                message: '同期先の認証に失敗しました。設定で認証情報を登録し直してください。作業コピーの内容は変わっていません。',
                detail: 'fatal: Authentication failed',
            },
            notice: '同期先の認証に失敗しました。設定で認証情報を登録し直してください。作業コピーの内容は変わっていません。',
        })
        renderRemote()
        fireEvent.change(await screen.findByLabelText('同期先の所在'), {target: {value: '/share/proj.git'}})
        fireEvent.click(screen.getByRole('button', {name: '接続を確認する'}))
        expect(await screen.findByText(/同期先の認証に失敗しました/)).toBeInTheDocument()
        expect(screen.getByText(/Authentication failed/)).toBeInTheDocument()
    })
})
