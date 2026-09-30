import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Documents} from './Documents'

const documentVersions = vi.hoisted(() => vi.fn())
const documentChapters = vi.hoisted(() => vi.fn())
const documentDiff = vi.hoisted(() => vi.fn())
const confirmCheck = vi.hoisted(() => vi.fn())
const confirmDocument = vi.hoisted(() => vi.fn())
const generateDocument = vi.hoisted(() => vi.fn())
const verifyDocument = vi.hoisted(() => vi.fn())
const moveToBasicDesign = vi.hoisted(() => vi.fn())
const checkDocumentReservation = vi.hoisted(() => vi.fn())
const workModeOptions = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    CheckDocumentReservation: checkDocumentReservation,
    WorkModeOptions: workModeOptions,
    DocumentVersions: documentVersions,
    DocumentChapters: documentChapters,
    DocumentDiff: documentDiff,
    ConfirmCheck: confirmCheck,
    ConfirmDocument: confirmDocument,
    GenerateDocument: generateDocument,
    VerifyDocument: verifyDocument,
    MoveToBasicDesign: moveToBasicDesign,
    PaneWidths: paneWidths,
    SetPaneWidths: setPaneWidths,
}))

const listeners = vi.hoisted(() => ({current: [] as Array<(ev: unknown) => void>}))
const paneWidths = vi.hoisted(() => vi.fn())
const setPaneWidths = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, cb: (ev: unknown) => void) => {
        listeners.current.push(cb)
        return () => {
            listeners.current = listeners.current.filter((f) => f !== cb)
        }
    },
}))

function emit(ev: Record<string, unknown>) {
    listeners.current.forEach((cb) => cb(ev))
}

const CHAPTERS = [
    {
        fileName: '00-index.md',
        chapter: 'index',
        title: '目次・表記規約',
        body: '# 目次\n\n- [業務背景](01-business-context.md)',
        covers: [],
    },
    {
        fileName: '06-functional-requirements.md',
        chapter: 'functional-requirements',
        title: '機能要件',
        body: '# 機能要件\n\n## FR-INV-001 在庫引当\n\n受注確定時に引き当てる。\n\n```mermaid\nflowchart TD\n  A --> B\n```',
        covers: ['FR-INV-001'],
    },
]

/**
 * 予約の選択を経て着手する。
 * 生成・差分再生成・確定は進め方を選ぶまで実行されない。
 */
async function startWork(label: string, mode = '排他（1 名で進める）') {
    fireEvent.click(screen.getByRole('button', {name: label}))
    fireEvent.click(await screen.findByRole('radio', {name: new RegExp(mode)}))
    fireEvent.click(screen.getByRole('button', {name: 'この進め方で始める'}))
}

describe('成果物プレビュー・版差分', () => {
    beforeEach(() => {
        paneWidths.mockReset().mockResolvedValue({versions: 220, checks: 360, min: 160, max: 640})
        setPaneWidths.mockReset().mockResolvedValue(undefined)
        listeners.current = []
        documentVersions.mockReset()
        documentVersions.mockResolvedValue([
            {version: 0, label: 'ドラフト', hasContent: true},
            {version: 1, label: '確定版 v1', confirmedAt: '2026-08-27T10:00:00Z', sourceCount: 3, hasContent: true},
        ])
        documentChapters.mockReset()
        documentChapters.mockResolvedValue(CHAPTERS)
        documentDiff.mockReset()
        documentDiff.mockResolvedValue([
            {
                fileName: '01-business-context.md',
                chapter: 'background',
                status: 'changed',
                added: 1,
                removed: 1,
                lines: [
                    {kind: 'equal', text: '# 業務背景'},
                    {kind: 'remove', text: '現状は Excel 台帳。'},
                    {kind: 'add', text: '現状は Excel 台帳と紙の受払簿。'},
                ],
            },
            {fileName: '02-scope.md', chapter: 'scope', status: 'unchanged', added: 0, removed: 0, lines: []},
        ])
        confirmCheck.mockReset()
        confirmCheck.mockResolvedValue({
            confirmable: true, draftRequirements: ['FR-INV-001'], blockingIssues: [], hasDraft: true,
        })
        confirmDocument.mockReset()
        confirmDocument.mockResolvedValue({version: 1, agreedRequirements: ['FR-INV-001'], canMoveToBasicDesign: true})
        generateDocument.mockReset()
        generateDocument.mockResolvedValue(undefined)
        // 予約の選択。既定では他メンバーの予約が無い状態。
        checkDocumentReservation.mockReset()
        checkDocumentReservation.mockResolvedValue({reserved: false})
        workModeOptions.mockReset()
        workModeOptions.mockResolvedValue([
            {mode: 'exclusive', label: '排他（1 名で進める）', hint: '自分だけで進めます。'},
            {mode: 'concurrent', label: '並行（同時に進める）', hint: '同時に進めます。'},
        ])
        verifyDocument.mockReset()
        moveToBasicDesign.mockReset()
        moveToBasicDesign.mockResolvedValue(undefined)
    })

    // 章を選んで本文がレンダリング表示される（Mermaid フェンスを含む）。
    it('章を選ぶと本文がレンダリングされる', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByRole('button', {name: '目次・表記規約'})

        fireEvent.click(screen.getByRole('button', {name: '機能要件'}))
        // 見出しが Markdown として描画される（生の "#" が残らない）。
        expect(await screen.findByRole('heading', {name: /FR-INV-001 在庫引当/})).toBeInTheDocument()
        expect(screen.getByText('受注確定時に引き当てる。')).toBeInTheDocument()
    })

    // 版一覧に確定版とドラフトが並ぶ。
    it('版一覧にドラフトと確定版が並ぶ', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        expect(await screen.findByRole('button', {name: 'ドラフト'})).toBeInTheDocument()
        expect(screen.getByRole('button', {name: '確定版 v1'})).toBeInTheDocument()
    })

    // 選んだ 2 版の差分が変更前後で表示される。
    it('版差分を変更前後で表示する', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByRole('button', {name: '確定版 v1'})

        fireEvent.click(screen.getByRole('button', {name: '差分'}))
        const panel = await screen.findByLabelText('版差分')
        await waitFor(() => expect(documentDiff).toHaveBeenCalledWith('requirements', 1, 0))

        expect(within(panel).getByText(/確定版 v1 → ドラフト/)).toBeInTheDocument()
        expect(within(panel).getByText(/変更 1 文書 \/ 全 2 文書/)).toBeInTheDocument()
        expect(within(panel).getByText(/現状は Excel 台帳。/)).toBeInTheDocument()
        expect(within(panel).getByText(/現状は Excel 台帳と紙の受払簿。/)).toBeInTheDocument()
    })

    // 確定前チェックの結果が一覧表示される。
    it('確定前チェックの結果を表示する', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        expect(await screen.findByText(/未合意の要件項目: 1 件/)).toBeInTheDocument()
        expect(screen.getByText(/ブロックする未決事項: 0 件/)).toBeInTheDocument()
        expect(screen.getByText(/成果物: ドラフトあり/)).toBeInTheDocument()
    })

    // 確定できない状態では操作が無効になり、理由が示される。
    it('確定できないときは理由を示して操作を無効にする', async () => {
        confirmCheck.mockResolvedValue({
            confirmable: false, draftRequirements: [], blockingIssues: ['ISS-001'], hasDraft: true,
            reason: '要件項目をブロックする未決事項が 1 件あります。決着させてから確定してください。',
        })
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/ブロックする未決事項が 1 件あります/)

        fireEvent.click(screen.getByRole('button', {name: '確定する'}))
        expect(confirmDocument).not.toHaveBeenCalled()
    })

    // 確定すると版が保存され、基本設計へ進める旨が示される。
    it('確定すると版番号と次の行動を示す', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        await startWork('確定する')
        await waitFor(() =>
            expect(confirmDocument).toHaveBeenCalledWith('requirements', {mode: 'exclusive', confirmed: false}),
        )
        expect(await screen.findByText(/確定版 v1 を保存しました。基本設計フェーズへ進めます。/)).toBeInTheDocument()
    })

    // 進め方を選ぶまで生成は始まらない。
    it('進め方を選ぶまで生成を始めない', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        fireEvent.click(screen.getByRole('button', {name: '生成する'}))
        expect(await screen.findByText('成果物の生成を始めます')).toBeInTheDocument()
        expect(generateDocument).not.toHaveBeenCalled()

        fireEvent.click(screen.getByRole('button', {name: 'やめる'}))
        expect(generateDocument).not.toHaveBeenCalled()
    })

    // 他メンバーの排他予約は警告のうえ確認を経れば着手できる。
    it('他メンバーの予約があるときは警告し、確認するまで着手しない', async () => {
        checkDocumentReservation.mockResolvedValue({
            reserved: true,
            holder: '佐藤',
            startedAt: '2026-09-02 10:00',
            warning: '要件定義書（documents-requirements）は 佐藤 さんが 1 名で進める予定です。それでも着手しますか。',
        })
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        fireEvent.click(screen.getByRole('button', {name: '生成する'}))
        expect(await screen.findByText(/佐藤 さんが 1 名で進める予定です/)).toBeInTheDocument()
        fireEvent.click(await screen.findByRole('radio', {name: /並行（同時に進める）/}))
        // 確認のチェックを入れるまでは着手できない
        expect(screen.getByRole('button', {name: 'この進め方で始める'})).toBeDisabled()

        fireEvent.click(screen.getByRole('checkbox', {name: /並行して着手します/}))
        fireEvent.click(screen.getByRole('button', {name: 'この進め方で始める'}))
        await waitFor(() =>
            expect(generateDocument).toHaveBeenCalledWith('requirements', false, {
                mode: 'concurrent',
                confirmed: true,
            }),
        )
    })

    // 基本設計フェーズへの移行を操作できる。
    it('基本設計フェーズへ移行できる', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        fireEvent.click(screen.getByRole('button', {name: '基本設計フェーズへ'}))
        await waitFor(() => expect(moveToBasicDesign).toHaveBeenCalled())
        expect(await screen.findByText(/基本設計フェーズへ移行しました/)).toBeInTheDocument()
    })

    // 生成の進行が章ごとに示され、完了で検証結果が出る。
    it('生成の進行と検証結果を示す', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        await startWork('生成する')
        await waitFor(() =>
            expect(generateDocument).toHaveBeenCalledWith('requirements', false, {
                mode: 'exclusive',
                confirmed: false,
            }),
        )

        emit({kind: 'chapter-start', index: 1, total: 12, title: '業務背景'})
        expect(await screen.findByText(/1\/12 業務背景 を生成中…/)).toBeInTheDocument()
        emit({kind: 'chapter-done', index: 2, total: 12, title: 'スコープ', reused: true})
        expect(await screen.findByText(/スコープ は変更がないため再利用しました/)).toBeInTheDocument()
        emit({kind: 'done', total: 12, errors: 1, warnings: 2})
        expect(await screen.findByText(/生成しました（検証: エラー 1 件 \/ 警告 2 件）/)).toBeInTheDocument()
    })

    // 差分再生成を操作できる。
    it('変更分だけ再生成を操作できる', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        await startWork('変更分だけ再生成', '並行（同時に進める）')
        await waitFor(() =>
            expect(generateDocument).toHaveBeenCalledWith('requirements', true, {
                mode: 'concurrent',
                confirmed: false,
            }),
        )
    })

    // 検証結果は項目名と区分つきで一覧される。
    it('検証結果を項目名と区分つきで一覧する', async () => {
        verifyDocument.mockResolvedValue({
            violations: [
                {check: 'V3', severity: 'error', target: 'ISS-001', message: 'ISS-001 が FR-INV-001 をブロックしたままです。'},
                {check: 'V5', severity: 'warning', file: '07.md', line: 3, target: '速い', message: '「速い」は曖昧語です。'},
            ],
        })
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        fireEvent.click(screen.getByRole('button', {name: '検証する'}))
        expect(await screen.findByText(/V3 ブロックする未決事項/)).toBeInTheDocument()
        expect(screen.getByText(/V5 曖昧語/)).toBeInTheDocument()
        expect(screen.getByText('エラー')).toBeInTheDocument()
        expect(screen.getByText('警告')).toBeInTheDocument()
    })

    // 生成の失敗は原因と次の行動を日本語 1 文で示す（内部コードを出さない）。
    it('生成の失敗を原因と次の行動で示す', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        emit({kind: 'error', errorClass: 'transient', message: 'HTTP 503 upstream'})
        const notice = await screen.findByText(/通信を確認して、もう一度お試しください/)
        expect(notice.textContent).not.toContain('503')
        expect(notice.textContent).toContain('既存の成果物は変更されていません')
    })

    /*
     * 文言の正本はバックエンドのエラーカタログ（分類 × コード）で、
     * 画面は届いた `userMessage` をそのまま出す。画面側にコード別の表を持たない。
     * 残量切れ（`plan_usage_exhausted`）では、待つ／切り替えるという取れる操作が分かること。
     */
    it('バックエンドが作った文言（userMessage）をそのまま表示する', async () => {
        render(<Documents kind="requirements" onBack={() => undefined} />)
        await screen.findByText(/未合意の要件項目/)

        emit({
            kind: 'error',
            errorClass: 'config',
            errorCode: 'plan_usage_exhausted',
            message: 'plan usage exhausted',
            userMessage:
                'ChatGPT のプランの利用枠を使い切りました。9月16日 8時00分 の回復を待つか、設定画面で認証方式または AIプロバイダを切り替えてください。',
        })

        const notice = await screen.findByText(/ChatGPT のプランの利用枠を使い切りました/)
        expect(notice.textContent).toContain('9月16日 8時00分 の回復を待つか')
        expect(notice.textContent).toContain('設定画面で認証方式または AIプロバイダを切り替えてください')
        // 分類ごとの既定文（キーとモデルの確認）へ倒さない
        expect(notice.textContent).not.toContain('キーとモデルを確認')
        expect(notice.textContent).not.toContain('plan_usage_exhausted')
    })
})
