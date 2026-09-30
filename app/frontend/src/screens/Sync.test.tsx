import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Sync} from './Sync'

/**
 * 同期パネルの画面テスト。
 *
 * 反映は事前提示と確認操作を経ること、閲覧権限では反映を無効化して理由を示すこと、
 * 到達できないときは原因＋次の行動を示して作業を続けられる旨を併記すること、
 * 競合は三面マージへ連結することを固定する。
 */

const syncStatus = vi.hoisted(() => vi.fn())
const syncLog = vi.hoisted(() => vi.fn())
const syncMergeOptions = vi.hoisted(() => vi.fn())
const incorporateSync = vi.hoisted(() => vi.fn())
const previewSyncPublish = vi.hoisted(() => vi.fn())
const publishSync = vi.hoisted(() => vi.fn())
const checkSyncConnection = vi.hoisted(() => vi.fn())
const createSyncMergeOpenIssue = vi.hoisted(() => vi.fn())
const reattachSync = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    SyncStatus: syncStatus,
    SyncLog: syncLog,
    SyncMergeOptions: syncMergeOptions,
    IncorporateSync: incorporateSync,
    PreviewSyncPublish: previewSyncPublish,
    PublishSync: publishSync,
    CheckSyncConnection: checkSyncConnection,
    CreateSyncMergeOpenIssue: createSyncMergeOpenIssue,
    ReattachSync: reattachSync,
}))

const listeners = vi.hoisted(() => ({current: [] as Array<(ev: unknown) => void>}))
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, cb: (ev: unknown) => void) => {
        listeners.current.push(cb)
        return () => {
            listeners.current = listeners.current.filter((f) => f !== cb)
        }
    },
}))

const STATUS = {
    configured: true,
    kindLabel: '共有フォルダ上のリポジトリ',
    location: '/Volumes/share/proj.git',
    available: true,
    canIncorporate: true,
    canPublish: true,
    lastSyncedAt: '2026-09-02 10:00',
    lastIncorporationAt: '2026-09-02 09:30',
    hasUnpublished: true,
    unpublishedSummary: '決定事項 1',
    credentialRequired: false,
    credentialRegistered: false,
    needsReattach: false,
    restorePending: false,
}

const CHOICES = [
    {choice: 'theirs', label: '相手を採る', hint: '相手の内容で置き換えます。'},
    {choice: 'ours', label: '自分を採る', hint: '自分の内容を残します。'},
    {choice: 'both', label: '両立させる', hint: '両方を踏まえた内容を入力してください。'},
    {choice: 'open-issue', label: '未決事項として起票する', hint: '未決事項を起票します。'},
]

function renderSync(onIncorporated = vi.fn()) {
    render(<Sync onBack={() => undefined} onIncorporated={onIncorporated} onOpenCredentials={() => undefined} />)
    return onIncorporated
}

describe('同期', () => {
    beforeEach(() => {
        listeners.current = []
        syncStatus.mockReset()
        syncStatus.mockResolvedValue({...STATUS})
        syncLog.mockReset()
        syncLog.mockResolvedValue({
            entries: [
                {
                    at: '2026-09-02 10:00',
                    author: '佐藤',
                    operation: '反映',
                    remote: '共有フォルダ上のリポジトリ（/Volumes/share/proj.git）',
                    summary: '要件項目 2',
                    result: '成功',
                },
            ],
            lastIncorporation: '2026-09-02 09:30',
        })
        syncMergeOptions.mockReset()
        syncMergeOptions.mockResolvedValue(CHOICES)
        incorporateSync.mockReset()
        previewSyncPublish.mockReset()
        publishSync.mockReset()
        reattachSync.mockReset()
        checkSyncConnection.mockReset()
        createSyncMergeOpenIssue.mockReset()
    })

    // 最後に同期した日時・未反映の変更の有無・同期の記録を示す。
    it('同期の状態と記録を示す', async () => {
        renderSync()
        expect(await screen.findByText('最後に同期した日時')).toBeInTheDocument()
        expect(screen.getAllByText('2026-09-02 10:00').length).toBeGreaterThan(0)
        const facts = screen.getByText('未反映の変更').closest('div') as HTMLElement
        expect(within(facts).getByText('あり')).toBeInTheDocument()
        expect(within(facts).getByText('決定事項 1')).toBeInTheDocument()
        const log = screen.getByRole('table', {name: '同期の記録'})
        expect(within(log).getByText('反映')).toBeInTheDocument()
        expect(within(log).getByText('成功')).toBeInTheDocument()
    })

    // 反映は「何が載るか」の提示と確認操作を経る。
    it('反映は事前提示と確認を経てから実行する', async () => {
        previewSyncPublish.mockResolvedValue({
            kindLabel: '共有フォルダ上のリポジトリ',
            location: '/Volumes/share/proj.git',
            items: [{categoryLabel: '決定事項', added: 1, modified: 0, removed: 0, total: 1}],
            total: 1,
            summary: '決定事項 1',
            firstPublish: false,
            notice: '次の内容を 共有フォルダ上のリポジトリ（/Volumes/share/proj.git）へ載せます。',
        })
        publishSync.mockResolvedValue({done: true, summary: '決定事項 1', notice: '同期先へ反映しました（決定事項 1）。'})
        renderSync()
        await screen.findByText('未反映の変更')

        fireEvent.click(screen.getByRole('button', {name: '反映する内容を確認する'}))
        expect(await screen.findByLabelText('反映する内容')).toBeInTheDocument()
        expect(publishSync).not.toHaveBeenCalled()

        fireEvent.click(screen.getByRole('button', {name: 'この内容で反映する'}))
        await waitFor(() => expect(publishSync).toHaveBeenCalled())
        expect(await screen.findByText(/同期先へ反映しました/)).toBeInTheDocument()
    })

    // 復元した作業コピーは、同期先から取り直してから同期する。
    it('復元した作業コピーでは取り直しの導線を出す', async () => {
        syncStatus.mockResolvedValue({
            ...STATUS,
            needsReattach: true,
            notice: 'この作業コピーはバックアップから復元されています。同期先から取り直してから、取り込み・反映を行ってください。',
        })
        reattachSync.mockResolvedValue({
            done: true,
            restorePending: true,
            summary: '決定事項 1',
            notice: '同期先から取り直しました。復元した内容には同期先と異なる変更があります（決定事項 1）。反映する内容を確認してから反映してください。',
        })
        renderSync()
        await screen.findByText('未反映の変更')
        expect(screen.getByText(/バックアップから復元されています/)).toBeInTheDocument()

        fireEvent.click(screen.getByRole('button', {name: '同期先から取り直す'}))
        await waitFor(() => expect(reattachSync).toHaveBeenCalled())
        expect(await screen.findByText(/同期先から取り直しました/)).toBeInTheDocument()
    })

    // 復元した内容の反映は、確認を経てから同期先へ書く（無確認で書かない）。
    it('復元した内容の反映は確認を添えて実行する', async () => {
        syncStatus.mockResolvedValue({...STATUS, restorePending: true})
        previewSyncPublish.mockResolvedValue({
            kindLabel: '共有フォルダ上のリポジトリ',
            location: '/Volumes/share/proj.git',
            items: [{categoryLabel: '決定事項', added: 0, modified: 1, removed: 0, total: 1}],
            total: 1,
            summary: '決定事項 1',
            firstPublish: false,
            needsReattach: false,
            restorePending: true,
            notice: 'バックアップから復元した内容を 共有フォルダ上のリポジトリ（/Volumes/share/proj.git）へ載せます。',
        })
        publishSync.mockResolvedValue({done: true, summary: '決定事項 1', notice: '同期先へ反映しました（決定事項 1）。'})
        renderSync()
        await screen.findByText('未反映の変更')

        fireEvent.click(screen.getByRole('button', {name: '反映する内容を確認する'}))
        expect(await screen.findByLabelText('反映する内容')).toBeInTheDocument()
        expect(screen.getByText(/古い内容へ戻ったように/)).toBeInTheDocument()
        expect(publishSync).not.toHaveBeenCalled()

        fireEvent.click(screen.getByRole('button', {name: 'この内容で反映する'}))
        await waitFor(() => expect(publishSync).toHaveBeenCalledWith(true))
    })

    // 閲覧権限では反映を無効化し、理由を示す（取り込みはできる）。
    it('閲覧権限では反映を無効化して理由を示す', async () => {
        syncStatus.mockResolvedValue({
            ...STATUS,
            canPublish: false,
            publishReason: '同期先への反映は編集権限が必要です（現在は閲覧）。オーナーに権限の変更を依頼してください。',
        })
        renderSync()
        await screen.findByText('未反映の変更')

        const publishButton = screen.getByRole('button', {name: '反映する内容を確認する'})
        expectDisabledReason(publishButton, /編集権限が必要です/)
        expect(screen.getByRole('button', {name: '取り込む'})).toBeEnabled()
    })

    // 到達できないときは原因＋次の行動を示し、作業を続けられる旨を併記する。
    it('到達できないときは原因と次の行動を示す', async () => {
        incorporateSync.mockResolvedValue({
            done: false,
            failure: {
                kindLabel: '同期先に接続できなかった',
                message:
                    '同期先に接続できません。接続が回復してから、もう一度取り込みしてください。作業はこのまま続けられます。作業コピーの内容は変わっていません。',
                detail: 'fatal: could not read from remote',
            },
            notice:
                '同期先に接続できません。接続が回復してから、もう一度取り込みしてください。作業はこのまま続けられます。作業コピーの内容は変わっていません。',
        })
        renderSync()
        await screen.findByText('未反映の変更')

        fireEvent.click(screen.getByRole('button', {name: '取り込む'}))
        expect(await screen.findByText(/作業はこのまま続けられます/)).toBeInTheDocument()
        // 詳細は折りたたみ（既定では出さない）
        expect(screen.queryByText(/could not read from remote/)).not.toBeInTheDocument()
        fireEvent.click(screen.getByRole('button', {name: '詳細を見る'}))
        expect(await screen.findByText(/could not read from remote/)).toBeInTheDocument()
    })

    // 競合は三面マージへ連結し、承認を添えて再実行する。
    it('競合したら三面マージを開き、承認を添えて取り込み直す', async () => {
        incorporateSync
            .mockResolvedValueOnce({
                done: false,
                conflicts: [
                    {
                        id: 'terms.yaml#在庫',
                        label: '用語「在庫」',
                        unitLabel: 'エントリ',
                        categoryLabel: '用語',
                        theirsAuthor: '鈴木',
                        base: '基準の定義',
                        theirs: 'B の定義',
                        ours: 'A の定義',
                    },
                ],
                notice: '他のメンバーと同じ対象が変更されています。取り込みは行っていません。',
            })
            .mockResolvedValueOnce({done: true, summary: '用語 1', conflictCount: 1, notice: '取り込みが完了しました（用語 1）。'})
        const onIncorporated = renderSync()
        await screen.findByText('未反映の変更')

        fireEvent.click(screen.getByRole('button', {name: '取り込む'}))
        expect(await screen.findByLabelText('取り込み時の競合の解決')).toBeInTheDocument()
        expect(screen.getByText('用語「在庫」')).toBeInTheDocument()
        expect(onIncorporated).not.toHaveBeenCalled()

        fireEvent.click(screen.getByRole('radio', {name: /相手を採る/}))
        fireEvent.click(screen.getByRole('button', {name: 'この内容で取り込む'}))
        await waitFor(() =>
            expect(incorporateSync).toHaveBeenLastCalledWith([
                {choice: 'theirs', merged: '', adopt: '', openIssueId: '', conflictId: 'terms.yaml#在庫'},
            ]),
        )
        expect(await screen.findByText(/取り込みが完了しました/)).toBeInTheDocument()
        expect(onIncorporated).toHaveBeenCalled()
    })

    // 進行状況を段階の表示名で示す（git の語を出さない）。
    it('進行状況を表示する', async () => {
        incorporateSync.mockImplementation(async () => {
            listeners.current.forEach((cb) => cb({stage: 'fetch', label: '同期先から受信しています'}))
            return {done: true, summary: '変更なし', notice: '取り込む変更はありませんでした。'}
        })
        renderSync()
        await screen.findByText('未反映の変更')
        fireEvent.click(screen.getByRole('button', {name: '取り込む'}))
        expect(await screen.findByText(/取り込む変更はありませんでした/)).toBeInTheDocument()
    })

    // 同期先が未設定なら実行の導線を出さず、理由を示す。
    it('同期先が未設定なら理由を示して操作を出さない', async () => {
        syncStatus.mockResolvedValue({
            configured: false,
            available: false,
            canIncorporate: false,
            canPublish: false,
            hasUnpublished: false,
            credentialRequired: false,
            credentialRegistered: false,
            notice: '同期先が設定されていません。オーナーが同期先を設定すると、他のメンバーと成果物を共有できます。',
        })
        renderSync()
        expect(await screen.findByText(/同期先が設定されていません/)).toBeInTheDocument()
        expect(screen.queryByRole('button', {name: '取り込む'})).not.toBeInTheDocument()
    })
})
