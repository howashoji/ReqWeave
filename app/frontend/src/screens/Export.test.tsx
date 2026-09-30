import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Export} from './Export'

const documentVersions = vi.hoisted(() => vi.fn())
const exportDocuments = vi.hoisted(() => vi.fn())
const chooseFolder = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    DocumentVersions: documentVersions,
    ExportDocuments: exportDocuments,
    ChooseFolder: chooseFolder,
}))

const VIOLATIONS = [
    {check: 'V3', severity: 'error', target: 'ISS-001', message: 'ISS-001 が FR-INV-001 をブロックしたままです。'},
    {check: 'V5', severity: 'warning', file: '07.md', line: 3, target: '速い', message: '「速い」は曖昧語です。'},
]

describe('エクスポート', () => {
    beforeEach(() => {
        documentVersions.mockReset()
        documentVersions.mockImplementation((kind: string) =>
            Promise.resolve(
                kind === 'requirements'
                    ? [
                          {version: 0, label: 'ドラフト', hasContent: true},
                          {version: 1, label: '確定版 v1', hasContent: true},
                      ]
                    : [],
            ),
        )
        exportDocuments.mockReset()
        chooseFolder.mockReset()
        chooseFolder.mockResolvedValue('/tmp/export')
    })

    // 対象と出力先を指定して実行できる。
    it('対象と出力先を指定して実行する', async () => {
        exportDocuments.mockResolvedValue({
            files: ['CLAUDE.md', '10-requirements/00-index.md'],
            verification: {violations: []},
            exported: true,
        })
        render(<Export onBack={() => undefined} />)
        await screen.findByLabelText('要件定義書')

        fireEvent.click(screen.getByRole('button', {name: 'フォルダを選ぶ'}))
        await waitFor(() => expect(screen.getByLabelText('出力先')).toHaveValue('/tmp/export'))

        fireEvent.click(screen.getByRole('button', {name: 'エクスポート'}))
        await waitFor(() => expect(exportDocuments).toHaveBeenCalled())
        const req = exportDocuments.mock.calls[0][0]
        expect(req.destination).toBe('/tmp/export')
        expect(req.acceptWarnings).toBe(false)
    })

    // 違反ゼロなら検証合格を結果とともに表示する。
    it('違反ゼロなら検証合格と出力ファイル一覧を示す', async () => {
        exportDocuments.mockResolvedValue({
            files: ['CLAUDE.md', 'feedback-template.md', '10-requirements/00-index.md'],
            verification: {violations: []},
            exported: true,
        })
        render(<Export onBack={() => undefined} />)
        await screen.findByLabelText('要件定義書')
        fireEvent.change(screen.getByLabelText('出力先'), {target: {value: '/tmp/export'}})
        fireEvent.click(screen.getByRole('button', {name: 'エクスポート'}))

        expect(await screen.findByText(/検証合格。3 ファイルを出力しました。/)).toBeInTheDocument()
        const files = await screen.findByLabelText('出力ファイル')
        expect(within(files).getByText('CLAUDE.md')).toBeInTheDocument()
        expect(within(files).getByText('feedback-template.md')).toBeInTheDocument()
        expect(within(files).getByText('検証合格')).toBeInTheDocument()
    })

    // 違反があるとき一覧と 2 つの選択肢を示す。
    it('違反があるとき一覧と「修正に戻る／警告付きでエクスポート」を示す', async () => {
        exportDocuments.mockResolvedValue({
            files: [],
            verification: {violations: VIOLATIONS},
            exported: false,
        })
        const onBack = vi.fn()
        render(<Export onBack={onBack} />)
        await screen.findByLabelText('要件定義書')
        fireEvent.change(screen.getByLabelText('出力先'), {target: {value: '/tmp/export'}})
        fireEvent.click(screen.getByRole('button', {name: 'エクスポート'}))

        const panel = await screen.findByLabelText('整合性検証の結果')
        expect(within(panel).getByText(/エラー 1 件 \/ 警告 1 件/)).toBeInTheDocument()
        expect(within(panel).getByText(/V3 ブロックする未決事項/)).toBeInTheDocument()
        expect(within(panel).getByText(/V5 曖昧語/)).toBeInTheDocument()
        expect(within(panel).getByText(/07.md:3/)).toBeInTheDocument()

        // 修正に戻る。
        fireEvent.click(within(panel).getByRole('button', {name: '修正に戻る'}))
        expect(onBack).toHaveBeenCalled()

        // 警告付きでエクスポート。
        exportDocuments.mockResolvedValue({
            files: ['CLAUDE.md', 'export-report.md'],
            verification: {violations: VIOLATIONS},
            exported: true,
        })
        fireEvent.click(within(panel).getByRole('button', {name: '警告付きでエクスポート'}))
        await waitFor(() => expect(exportDocuments.mock.calls[1][0].acceptWarnings).toBe(true))
        expect(await screen.findByText(/警告付きで出力しました（2 ファイル）/)).toBeInTheDocument()
        expect(screen.getByText(/CLAUDE.md と export-report.md に記載/)).toBeInTheDocument()
    })

    // 出力先の未指定では実行できない。
    it('出力先を指定するまで実行できない', async () => {
        render(<Export onBack={() => undefined} />)
        await screen.findByLabelText('要件定義書')

        fireEvent.click(screen.getByRole('button', {name: 'エクスポート'}))
        expect(exportDocuments).not.toHaveBeenCalled()
    })

    // 失敗は原因と次の行動を日本語で示す（内部コードを出さない）。
    it('出力に失敗したら理由を日本語で示す', async () => {
        exportDocuments.mockRejectedValue(
            new Error('出力先が既に存在します（/tmp/export）。別のフォルダを指定してください。'),
        )
        render(<Export onBack={() => undefined} />)
        await screen.findByLabelText('要件定義書')
        fireEvent.change(screen.getByLabelText('出力先'), {target: {value: '/tmp/export'}})
        fireEvent.click(screen.getByRole('button', {name: 'エクスポート'}))

        expect(await screen.findByText(/別のフォルダを指定してください/)).toBeInTheDocument()
    })

    // 基本設計書が未生成なら同梱を選べない。
    it('基本設計書が未生成なら同梱を選べない', async () => {
        render(<Export onBack={() => undefined} />)
        const checkbox = await screen.findByLabelText('基本設計書を同梱する')
        expect(checkbox).toBeDisabled()
        expect(
            screen.getByText(/まだ生成されていません。成果物の画面で基本設計を生成すると選べます。/),
        ).toBeInTheDocument()
    })
})
