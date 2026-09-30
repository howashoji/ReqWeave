import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {BackupRestore} from './BackupRestore'

const backupGenerations = vi.hoisted(() => vi.fn())
const backupProject = vi.hoisted(() => vi.fn())
const chooseBackupFile = vi.hoisted(() => vi.fn())
const chooseFolder = vi.hoisted(() => vi.fn())
const previewBackup = vi.hoisted(() => vi.fn())
const restoreBackup = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    BackupGenerations: backupGenerations,
    BackupProject: backupProject,
    ChooseBackupFile: chooseBackupFile,
    ChooseFolder: chooseFolder,
    PreviewBackup: previewBackup,
    RestoreBackup: restoreBackup,
}))

const PROJECT = '/work/inventory'
const GENERATIONS = [
    {path: '/backups/20260827T050000Z.zip', createdAt: '2026-08-27T05:00:00Z', sizeBytes: 2 * 1024 * 1024},
    {path: '/backups/20260826T050000Z.zip', createdAt: '2026-08-26T05:00:00Z', sizeBytes: 512},
]

describe('バックアップ・復元', () => {
    beforeEach(() => {
        backupGenerations.mockReset()
        backupProject.mockReset()
        chooseBackupFile.mockReset()
        chooseFolder.mockReset()
        previewBackup.mockReset()
        restoreBackup.mockReset()
        backupGenerations.mockResolvedValue(GENERATIONS)
        backupProject.mockResolvedValue('/tmp/backup.zip')
        chooseFolder.mockResolvedValue('/work/restored')
        restoreBackup.mockResolvedValue({targetSystemName: '在庫管理システム', available: true})
        previewBackup.mockResolvedValue({
            sourcePath: GENERATIONS[0].path,
            projectId: 'id-1',
            targetSystemName: '在庫管理システム',
        })
    })

    // 1 回の操作で単一ファイルへ出力する
    it('単一ファイルへのバックアップを実行できる', async () => {
        render(<BackupRestore projectPath={PROJECT} onBack={() => undefined} />)
        await screen.findByText(PROJECT)

        fireEvent.click(screen.getByRole('button', {name: '単一ファイルへ出力'}))
        await waitFor(() => expect(backupProject).toHaveBeenCalledWith(PROJECT))
        expect(await screen.findByText(/バックアップを出力しました/)).toBeInTheDocument()
    })

    it('プロジェクト未選択ではバックアップできない理由を示す', async () => {
        render(<BackupRestore projectPath="" onBack={() => undefined} />)
        const button = await screen.findByRole('button', {name: '単一ファイルへ出力'})
        expectDisabledReason(button, /プロジェクト一覧で対象を選んで/)
    })

    // 自動退避の世代一覧（日時表示）から復元できる
    it('自動退避の世代一覧を日時つきで表示し、選ぶと復元の確認へ進む', async () => {
        render(<BackupRestore projectPath={PROJECT} onBack={() => undefined} />)

        const list = await screen.findByLabelText('自動退避の世代')
        expect(list).toBeInTheDocument()
        expect(screen.getByText('2.0 MB')).toBeInTheDocument()
        const expected = new Date(GENERATIONS[0].createdAt)
        const pad = (n: number) => String(n).padStart(2, '0')
        const label = `${expected.getFullYear()}-${pad(expected.getMonth() + 1)}-${pad(expected.getDate())} ${pad(expected.getHours())}:${pad(expected.getMinutes())}`
        fireEvent.click(screen.getByText(label))

        await screen.findByText('復元の確認')
        expect(previewBackup).toHaveBeenCalledWith(GENERATIONS[0].path)
    })

    // 復元先を選ぶまで実行できない（既存フォルダを上書きしない）
    it('復元先を選ぶまで復元できない', async () => {
        render(<BackupRestore projectPath={PROJECT} onBack={() => undefined} />)
        await screen.findByLabelText('自動退避の世代')
        fireEvent.click(screen.getByRole('button', {name: 'バックアップファイルを選ぶ'}))
        chooseBackupFile.mockResolvedValue('/tmp/manual.zip')

        fireEvent.click(screen.getByText('2.0 MB'))
        await screen.findByText('復元の確認')

        const restore = screen.getByRole('button', {name: '復元する'})
        expectDisabledReason(restore, /復元先のフォルダを選んで/)

        fireEvent.click(screen.getByRole('button', {name: '復元先のフォルダを選ぶ'}))
        await screen.findByText('/work/restored')
        fireEvent.click(screen.getByRole('button', {name: '復元する'}))
        await waitFor(() => expect(restoreBackup).toHaveBeenCalledWith(GENERATIONS[0].path, '/work/restored', false))
    })

    // project_id が重複する場合は置き換え / 複製として保持を選ばせる
    it('同じプロジェクトが既にある場合は置き換えか複製かを選ばせる', async () => {
        previewBackup.mockResolvedValue({
            sourcePath: GENERATIONS[0].path,
            projectId: 'id-1',
            targetSystemName: '在庫管理システム',
            duplicatePath: '/work/inventory',
            notice: '同じプロジェクトが既にあります。置き換えるか、複製として保持するかを選んでください。',
        })
        render(<BackupRestore projectPath={PROJECT} onBack={() => undefined} />)
        await screen.findByLabelText('自動退避の世代')
        fireEvent.click(screen.getByText('2.0 MB'))

        await screen.findByText(/同じプロジェクトが既にあります/)
        fireEvent.click(screen.getByRole('button', {name: '復元先のフォルダを選ぶ'}))
        await screen.findByText('/work/restored')

        fireEvent.click(screen.getByRole('button', {name: '複製として保持する'}))
        await waitFor(() => expect(restoreBackup).toHaveBeenCalledWith(GENERATIONS[0].path, '/work/restored', true))
    })

    it('自動退避が無いときは作成されるタイミングを案内する', async () => {
        backupGenerations.mockResolvedValue([])
        render(<BackupRestore projectPath={PROJECT} onBack={() => undefined} />)
        expect(await screen.findByText('自動退避はまだありません。')).toBeInTheDocument()
        // 「何が無いか」だけで終わらせず、増える条件まで書く（空の状態の共通の書き方）。
        expect(
            screen.getByText('プロジェクトを閉じるたびに自動で作成され、直近 10 世代を保持します。'),
        ).toBeInTheDocument()
    })
})
