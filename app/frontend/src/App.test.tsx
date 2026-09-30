import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {clickEnabled} from './test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import App from './App'

// フロントエンドはバックエンドの公開バインディング経由でのみ機能を呼ぶ（依存規則）。
// そのためテストでもバインディングのみをモックする。
const appInfo = vi.hoisted(() => vi.fn())
// 初期設定は完了済み・プロジェクトは 1 件ある状態で担当者モードの画面を検証する
// （初期設定の経路は SetupWizard.test.tsx、一覧の経路は ProjectList.test.tsx）
const setupState = vi.hoisted(() => vi.fn())
const projects = vi.hoisted(() => vi.fn())
const domainPresets = vi.hoisted(() => vi.fn())
const storedTheme = vi.hoisted(() => vi.fn())
const openDialogueProject = vi.hoisted(() => vi.fn())
const startDialogueSession = vi.hoisted(() => vi.fn())
const dialogueUtterances = vi.hoisted(() => vi.fn())
const dialogueCompleteness = vi.hoisted(() => vi.fn())
const usageDashboard = vi.hoisted(() => vi.fn())
const pendingOpenFile = vi.hoisted(() => vi.fn())
const introShown = vi.hoisted(() => vi.fn())
const markIntroShown = vi.hoisted(() => vi.fn())
const validateReturnFile = vi.hoisted(() => vi.fn())
const eventsOn = vi.hoisted(() =>
    vi.fn((_name: string, _handler: (...args: unknown[]) => void) => () => undefined),
)
vi.mock('../wailsjs/go/binding/API', () => ({
    AppInfo: appInfo,
    SetupState: setupState,
    Projects: projects,
    DomainPresets: domainPresets,
    Theme: storedTheme,
    ChooseFolder: vi.fn(),
    CreateProject: vi.fn(),
    DeletePreview: vi.fn(),
    DeleteProject: vi.fn(),
    OpenDialogueProject: openDialogueProject,
    CloseDialogueProject: vi.fn(),
    StartDialogueSession: startDialogueSession,
    ResumeDialogueSession: vi.fn(),
    PendingCandidates: vi.fn(),
    AskNextQuestion: vi.fn(),
    SendAnswer: vi.fn(),
    InterruptDialogue: vi.fn(),
    SuspendDialogue: vi.fn(),
    ApproveDialogueCandidates: vi.fn(),
    DialogueUtterances: dialogueUtterances,
    DialogueCompleteness: dialogueCompleteness,
    UsageDashboard: usageDashboard,
    SaveUsageReport: vi.fn(),
    CopyUsageReport: vi.fn(),
    ChooseUsageReportDestination: vi.fn(),
    CurrentPermission: vi.fn(),
    PendingOpenFile: pendingOpenFile,
    IntroShown: introShown,
    MarkIntroShown: markIntroShown,
    ChooseReturnFile: vi.fn(),
    ValidateReturnFile: validateReturnFile,
    ImportReturnFile: vi.fn(),
    AnalyzeImportedAnswers: vi.fn(),
    ApproveImportDiff: vi.fn(),
}))
vi.mock('../wailsjs/runtime/runtime', () => ({EventsOn: eventsOn}))

const PROJECT = {
    path: '/tmp/proj',
    projectId: '3f2a1c8e-9d4b-4f6a-8c2e-7b1d5a9f0e33',
    targetSystemName: '在庫管理システム',
    phase: 'requirements',
    phaseLabel: '要件定義',
    updatedAt: '2026-08-27T05:00:00Z',
    role: 'owner',
    roleLabel: 'オーナー',
    available: true,
}

/** プロジェクト一覧から 1 件開いて担当者モードへ入る */
async function openProject() {
    await screen.findByText('在庫管理システム')
    fireEvent.click(screen.getByText('在庫管理システム'))
    fireEvent.click(screen.getByRole('button', {name: '開く'}))
}

describe('App', () => {
    beforeEach(() => {
        appInfo.mockReset()
        setupState.mockReset()
        setupState.mockResolvedValue({complete: true, providers: [], efforts: []})
        projects.mockReset()
        projects.mockResolvedValue([PROJECT])
        domainPresets.mockReset()
        domainPresets.mockResolvedValue([])
        storedTheme.mockReset()
        storedTheme.mockResolvedValue('dark')
        openDialogueProject.mockReset()
        openDialogueProject.mockResolvedValue({
            projectPath: '/tmp/proj',
            phase: 'requirements',
            targetName: '在庫管理システム',
            sessions: [],
        })
        startDialogueSession.mockReset()
        startDialogueSession.mockResolvedValue({id: 'S-0001', type: 'owner', phase: 'requirements', startedAt: ''})
        dialogueUtterances.mockReset()
        dialogueUtterances.mockResolvedValue([])
        dialogueCompleteness.mockReset()
        dialogueCompleteness.mockResolvedValue({
            chapters: [{chapterId: 'background', name: '業務背景', percent: 0, satisfied: 0, total: 2,
                openIssues: 0, state: 'untouched'}],
            confirmation: {confirmable: false},
        })
        usageDashboard.mockReset()
        usageDashboard.mockResolvedValue({from: '', to: '', projects: [], markdown: '# AI 利用量'})
        pendingOpenFile.mockReset()
        pendingOpenFile.mockResolvedValue({kind: ''})
        introShown.mockReset()
        // 既定は「表示済み」。紹介スライドの経路は専用のテストで確かめる。
        introShown.mockResolvedValue(true)
        markIntroShown.mockReset()
        markIntroShown.mockResolvedValue(undefined)
        validateReturnFile.mockReset()
        eventsOn.mockClear()
        document.documentElement.removeAttribute('data-theme')
    })

    it('初期設定が未完了なら初期設定ウィザードへ入る', async () => {
        setupState.mockResolvedValue({
            complete: false,
            providers: [{id: 'anthropic', displayName: 'Anthropic（Claude）', policyUrl: 'https://example.test/policy'}],
            efforts: [{id: 'standard', label: '標準', description: '標準の掘り下げ', default: true}],
            dataPolicyNotice: '組織ポリシーの確認は利用者の責務です。',
            suggestedDisplayName: '佐藤',
            authorId: '',
            displayName: '',
        })
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)

        expect(await screen.findByText(/AI プロバイダを選んでください/)).toBeInTheDocument()
        expect(screen.queryByLabelText('プロジェクト')).toBeNull()
    })

    it('初期設定が未完了で紹介スライドが未表示なら、初期設定ウィザードより前に紹介スライドを出す', async () => {
        setupState.mockResolvedValue({complete: false, providers: [], efforts: []})
        introShown.mockResolvedValue(false)
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)

        expect(await screen.findByText('ReqWeave へようこそ')).toBeInTheDocument()
        expect(screen.queryByText(/AI プロバイダを選んでください/)).toBeNull()
    })

    it('紹介スライドをスキップすると表示済みとして記録し、初期設定ウィザードへ進む', async () => {
        setupState.mockResolvedValue({
            complete: false,
            providers: [{id: 'anthropic', displayName: 'Anthropic（Claude）', policyUrl: 'https://example.test/policy'}],
            efforts: [{id: 'standard', label: '標準', description: '標準の掘り下げ', default: true}],
            dataPolicyNotice: '組織ポリシーの確認は利用者の責務です。',
            suggestedDisplayName: '佐藤',
            authorId: '',
            displayName: '',
        })
        introShown.mockResolvedValue(false)
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)

        fireEvent.click(await screen.findByRole('button', {name: 'スキップ'}))

        expect(await screen.findByText(/AI プロバイダを選んでください/)).toBeInTheDocument()
        await waitFor(() => expect(markIntroShown).toHaveBeenCalledTimes(1))
    })

    it('紹介スライドが表示済みなら初期設定ウィザードへ直接入る', async () => {
        setupState.mockResolvedValue({
            complete: false,
            providers: [{id: 'anthropic', displayName: 'Anthropic（Claude）', policyUrl: 'https://example.test/policy'}],
            efforts: [{id: 'standard', label: '標準', description: '標準の掘り下げ', default: true}],
            dataPolicyNotice: '組織ポリシーの確認は利用者の責務です。',
            suggestedDisplayName: '佐藤',
            authorId: '',
            displayName: '',
        })
        introShown.mockResolvedValue(true)
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)

        expect(await screen.findByText(/AI プロバイダを選んでください/)).toBeInTheDocument()
        expect(screen.queryByText('ReqWeave へようこそ')).toBeNull()
        expect(markIntroShown).not.toHaveBeenCalled()
    })

    it('初期設定が完了していればプロジェクト一覧を表示する', async () => {
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)

        expect(await screen.findByText('在庫管理システム')).toBeInTheDocument()
        expect(screen.getByLabelText('現在地')).toHaveTextContent('reqweave / プロジェクト')
    })

    // 遷移: プロジェクト一覧 → AI 利用量ダッシュボード（プロジェクトを開かずに参照できる）。
    it('プロジェクトを開かずに AI 利用量ダッシュボードを開ける', async () => {
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)
        await screen.findByText('在庫管理システム')
        fireEvent.click(screen.getByRole('button', {name: 'AI 利用量'}))

        expect(await screen.findByLabelText('AI 利用量ダッシュボード')).toBeInTheDocument()
        expect(screen.getByLabelText('現在地')).toHaveTextContent('reqweave / AI 利用量')
        await waitFor(() => expect(usageDashboard).toHaveBeenCalled())

        await clickEnabled('プロジェクト一覧へ戻る')
        expect(await screen.findByText('在庫管理システム')).toBeInTheDocument()
    })

    it('バインディングが同期的に例外を投げても画面が壊れず日本語の案内を出す', async () => {
        // Wails ランタイム未注入時、生成コードは Promise を返さずその場で TypeError を投げる
        appInfo.mockImplementation(() => {
            throw new TypeError("Cannot read properties of undefined (reading 'binding')")
        })

        render(<App />)
        await openProject()

        const alert = await screen.findByRole('alert')
        expect(alert).toHaveTextContent('アプリ情報を取得できませんでした。アプリを再起動してください。')
        expect(alert.textContent).not.toContain('TypeError')
    })

    it('バインディング呼び出しが失敗したときは原因と次の行動を日本語で示す', async () => {
        appInfo.mockRejectedValue(new Error('binding unavailable'))

        render(<App />)
        await openProject()

        const alert = await screen.findByRole('alert')
        // 内部用語・生のコード値を利用者向け画面に出さない
        expect(alert).toHaveTextContent('アプリ情報を取得できませんでした。アプリを再起動してください。')
        expect(alert.textContent).not.toContain('binding unavailable')
    })

    it('プロジェクトを開くと対話画面で 3 層構造を出す', async () => {
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)
        await openProject()

        expect(await screen.findByLabelText('現在地')).toHaveTextContent('reqweave / 在庫管理システム / 要件定義')
        expect(screen.getByLabelText('プロジェクトの状態')).toBeInTheDocument()
        expect(screen.getByLabelText('対話')).toBeInTheDocument()
    })

    it('起動時に保存済みのテーマを適用する', async () => {
        storedTheme.mockResolvedValue('light')
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})

        render(<App />)
        await screen.findByText('在庫管理システム')

        await waitFor(() => expect(document.documentElement.getAttribute('data-theme')).toBe('light'))
    })

    describe('OS から受け渡しファイルを受け取ったとき', () => {
        beforeEach(() => {
            appInfo.mockResolvedValue({name: 'ReqWeave', version: '1.2.3'})
        })

        it('質問票（.rwvq）は回答モードで開き、パスコードの入力を求める', async () => {
            pendingOpenFile.mockResolvedValue({
                kind: 'respond',
                filePath: '/tmp/QS-001.rwvq',
                fileName: 'QS-001.rwvq',
            })

            render(<App />)

            expect(await screen.findByLabelText('パスコード')).toBeTruthy()
            // 回答モードは担当者モードの画面を出さない。
            expect(screen.queryByText('在庫管理システム')).toBeNull()
        })

        it('返送ファイル（.rwva）は回答モードにせず、取込の案内を出す', async () => {
            pendingOpenFile.mockResolvedValue({
                kind: 'import',
                filePath: '/tmp/QS-001-return.rwva',
                fileName: 'QS-001-return.rwva',
            })

            render(<App />)

            expect(await screen.findByText('返送ファイルを取り込むには、対象のプロジェクトを開いてください。')).toBeTruthy()
            expect(screen.queryByLabelText('パスコード')).toBeNull()
        })

        it('返送ファイルはプロジェクトを開くと回答取込へ渡り、選択ダイアログを経ずに検証される', async () => {
            pendingOpenFile.mockResolvedValue({
                kind: 'import',
                filePath: '/tmp/QS-001-return.rwva',
                fileName: 'QS-001-return.rwva',
            })
            validateReturnFile.mockResolvedValue({questionnaireId: 'QS-001', addressee: '佐藤', respondent: '佐藤'})

            render(<App />)
            await openProject()

            await waitFor(() => expect(validateReturnFile).toHaveBeenCalledWith('/tmp/QS-001-return.rwva'))
        })

        it('対応しない種類のファイルは理由を示し、アプリは動き続ける', async () => {
            pendingOpenFile.mockResolvedValue({
                kind: 'unsupported',
                fileName: '議事録.docx',
                message:
                    'このファイルは ReqWeave で開ける種類ではありません。質問票ファイル（.rwvq）または返送ファイル（.rwva）を開いてください。',
            })

            render(<App />)

            expect(await screen.findByRole('alert')).toHaveTextContent('ReqWeave で開ける種類ではありません')
            // 一覧はそのまま使える（落ちない・行き止まりにしない）。
            expect(await screen.findByText('在庫管理システム')).toBeTruthy()
        })

        it('起動中に届いた質問票（open-file イベント）でも回答モードへ入る', async () => {
            render(<App />)
            await screen.findByText('在庫管理システム')

            const registered = eventsOn.mock.calls.find((call) => call[0] === 'openfile:event')
            expect(registered).toBeTruthy()
            await act(async () => {
                registered?.[1]({kind: 'respond', filePath: '/tmp/QS-002.rwvq', fileName: 'QS-002.rwvq'})
            })

            expect(await screen.findByLabelText('パスコード')).toBeTruthy()
        })
    })
})
