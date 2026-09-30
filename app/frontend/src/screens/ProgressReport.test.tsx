import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled, expectDisabledReason} from '../test/interact'
import {ProgressReport} from './ProgressReport'

const progressReport = vi.hoisted(() => vi.fn())
const saveProgressReport = vi.hoisted(() => vi.fn())
const copyProgressReport = vi.hoisted(() => vi.fn())
const chooseDestination = vi.hoisted(() => vi.fn())
const currentPermission = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/binding/API', () => ({
    ProgressReport: progressReport,
    SaveProgressReport: saveProgressReport,
    CopyProgressReport: copyProgressReport,
    ChooseProgressReportDestination: chooseDestination,
    CurrentPermission: currentPermission,
}))

const MARKDOWN = [
    '# 進捗レポート: 在庫管理システム',
    '',
    '## 1. 要約',
    '',
    '対象期間に承認した決定事項は 2 件です。',
    '',
    '## 2. 決定事項',
    '',
    '| ID | 決定内容 | 決定日 |',
    '|---|---|---|',
    '| DEC-002 | 対象は国内倉庫のみとする。 | 2026-08-05 |',
].join('\n')

describe('進捗レポート画面', () => {
    beforeEach(() => {
        progressReport.mockReset().mockResolvedValue({markdown: MARKDOWN, from: '2026-08-01', to: '2026-08-31'})
        saveProgressReport.mockReset().mockResolvedValue('/tmp/progress-report.md')
        copyProgressReport.mockReset().mockResolvedValue(MARKDOWN)
        chooseDestination.mockReset().mockResolvedValue('/tmp/progress-report.md')
        currentPermission
            .mockReset()
            .mockResolvedValue({role: 'editor', roleLabel: '編集', canEdit: true, reason: ''})
    })

    // 期間を指定して生成し、要約先頭の単一様式をプレビューする。
    it('期間を指定して生成し、要約を先頭にしたプレビューを表示する', async () => {
        render(<ProgressReport onBack={() => undefined} />)

        fireEvent.change(screen.getByLabelText('開始日'), {target: {value: '2026-08-01'}})
        fireEvent.change(screen.getByLabelText('終了日'), {target: {value: '2026-08-31'}})
        fireEvent.click(screen.getByRole('button', {name: 'レポートを作成する'}))

        await waitFor(() =>
            expect(progressReport).toHaveBeenCalledWith({from: '2026-08-01', to: '2026-08-31'}),
        )
        const preview = await screen.findByLabelText('レポートのプレビュー')
        expect(preview.textContent).toContain('進捗レポート: 在庫管理システム')
        expect(preview.textContent).toContain('1. 要約')
        // 要約が詳細より先に出る（上長報告向けの単一様式）。
        expect((preview.textContent ?? '').indexOf('1. 要約')).toBeLessThan(
            (preview.textContent ?? '').indexOf('2. 決定事項'),
        )
    })

    // Markdown ファイル出力とクリップボードコピー。
    it('生成後に Markdown 保存とクリップボードコピーができる', async () => {
        render(<ProgressReport onBack={() => undefined} />)

        // 生成前は出力できない（理由を示して無効化する）。
        const saveBefore = screen.getByRole('button', {name: 'Markdown で保存する'})
        expectDisabledReason(saveBefore, /先にレポートを作成してください/)

        fireEvent.click(screen.getByRole('button', {name: 'レポートを作成する'}))
        await screen.findByLabelText('レポートのプレビュー')

        fireEvent.click(screen.getByRole('button', {name: 'Markdown で保存する'}))
        await waitFor(() => expect(saveProgressReport).toHaveBeenCalled())
        expect(await screen.findByText(/進捗レポートを保存しました/)).toBeTruthy()

        await clickEnabled('クリップボードへコピーする')
        await waitFor(() => expect(copyProgressReport).toHaveBeenCalled())
        expect(await screen.findByText(/クリップボードへコピーしました/)).toBeTruthy()
    })

    // 進捗レポートは閲覧権限でも作成・出力できる（取り込みと権限が異なる）。
    it('閲覧権限でも作成・出力できる', async () => {
        currentPermission.mockResolvedValue({
            role: 'viewer',
            roleLabel: '閲覧',
            canEdit: false,
            reason: 'この操作は編集権限が必要です（現在は閲覧）。',
        })
        render(<ProgressReport onBack={() => undefined} />)
        expect(await screen.findByText(/閲覧権限でも進捗レポートは作成・出力できます/)).toBeTruthy()

        const build = screen.getByRole('button', {name: 'レポートを作成する'})
        expect(build.hasAttribute('disabled')).toBe(false)
        fireEvent.click(build)
        await waitFor(() => expect(progressReport).toHaveBeenCalled())

        await clickEnabled('クリップボードへコピーする')
        await waitFor(() => expect(copyProgressReport).toHaveBeenCalled())
    })

    // 失敗は原因＋次の行動の 1 文で示す。ネイティブ alert は使わない。
    it('生成に失敗したら理由を画面に出し、古いプレビューを残さない', async () => {
        render(<ProgressReport onBack={() => undefined} />)

        // 一度成功させてプレビューを出す。
        fireEvent.click(screen.getByRole('button', {name: 'レポートを作成する'}))
        await screen.findByLabelText('レポートのプレビュー')

        // 次の生成が失敗したら、古い内容を残さず理由だけを示す（古い期間の内容を見せない）。
        progressReport.mockRejectedValue(new Error('期間は YYYY-MM-DD の形式で指定してください'))
        fireEvent.change(screen.getByLabelText('開始日'), {target: {value: '2026-09-01'}})
        fireEvent.click(screen.getByRole('button', {name: 'レポートを作成する'}))

        expect(await screen.findByText(/YYYY-MM-DD/)).toBeTruthy()
        await waitFor(() => expect(screen.queryByLabelText('レポートのプレビュー')).toBeNull())
    })

    // 期間の既定値は当月（ローカル暦日）。
    it('期間の既定値が当月になっている', async () => {
        render(<ProgressReport onBack={() => undefined} />)
        await screen.findByText(/権限:/) // 権限取得の解決を待ってから検証する
        const now = new Date()
        const pad = (n: number) => String(n).padStart(2, '0')
        const first = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-01`
        const last = new Date(now.getFullYear(), now.getMonth() + 1, 0)
        expect((screen.getByLabelText('開始日') as HTMLInputElement).value).toBe(first)
        expect((screen.getByLabelText('終了日') as HTMLInputElement).value).toBe(
            `${last.getFullYear()}-${pad(last.getMonth() + 1)}-${pad(last.getDate())}`,
        )
    })
})
