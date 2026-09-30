import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled, clickEnabledBy, expectDisabledReason} from '../test/interact'
import {Dialogue} from './Dialogue'

const openDialogueProject = vi.hoisted(() => vi.fn())
const closeDialogueProject = vi.hoisted(() => vi.fn())
const startDialogueSession = vi.hoisted(() => vi.fn())
const resumeDialogueSession = vi.hoisted(() => vi.fn())
const pendingCandidates = vi.hoisted(() => vi.fn())
const askNextQuestion = vi.hoisted(() => vi.fn())
const sendAnswer = vi.hoisted(() => vi.fn())
const interruptDialogue = vi.hoisted(() => vi.fn())
const suspendDialogue = vi.hoisted(() => vi.fn())
const approveCandidates = vi.hoisted(() => vi.fn())
const dialogueUtterances = vi.hoisted(() => vi.fn())
const dialogueCompleteness = vi.hoisted(() => vi.fn())
const workflowGuide = vi.hoisted(() => vi.fn())
const usageStatusNow = vi.hoisted(() => vi.fn())
const usageDashboard = vi.hoisted(() => vi.fn())
const tokenUsage = vi.hoisted(() => vi.fn())
const currentPermission = vi.hoisted(() => vi.fn())
const changeSummary = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    OpenDialogueProject: openDialogueProject,
    CloseDialogueProject: closeDialogueProject,
    StartDialogueSession: startDialogueSession,
    ResumeDialogueSession: resumeDialogueSession,
    PendingCandidates: pendingCandidates,
    AskNextQuestion: askNextQuestion,
    SendAnswer: sendAnswer,
    InterruptDialogue: interruptDialogue,
    SuspendDialogue: suspendDialogue,
    ApproveDialogueCandidates: approveCandidates,
    DialogueUtterances: dialogueUtterances,
    DialogueCompleteness: dialogueCompleteness,
    WorkflowGuide: workflowGuide,
    UsageStatusNow: usageStatusNow,
    ChangeSummary: changeSummary,
    UsageDashboard: usageDashboard,
    TokenUsage: tokenUsage,
    SaveUsageReport: vi.fn(),
    CopyUsageReport: vi.fn(),
    ChooseUsageReportDestination: vi.fn(),
    CurrentPermission: currentPermission,
    SetUsageLimit: vi.fn(),
    ClearUsageLimit: vi.fn(),
    PaneWidths: paneWidths,
    SetPaneWidths: setPaneWidths,
    // 工程ガイドの行き先へ実際に移れることを確かめるため、決定・未決の画面が読む口も置く。
    Decisions: vi.fn(() => Promise.resolve([])),
    OpenIssues: vi.fn(() => Promise.resolve([])),
    ChangeHistory: vi.fn(() => Promise.resolve([])),
}))

// Wails イベントはテストから送れるようにフックを保持する。
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

/*
 * イベントを送り、受け取った画面の再描画を確定させてから戻る。
 *
 * act の外で送ると、React は再描画を後の順番へ予約するだけで戻る。候補パネルは送る前から
 * 「まだ候補はありません」の形で在るため、直後の `findByLabelText('抽出候補')` は古いパネルを
 * 即座に掴み、その中を `getBy…` で探すと候補がまだ無い。負荷が高いと待ち合わせのタイマーが
 * 予約済みの再描画より先に回り、そのときだけ落ちる（実測: 空のパネルを見て要素が見つからない）。
 */
function emit(ev: Record<string, unknown>) {
    act(() => {
        listeners.current.forEach((cb) => cb(ev))
    })
}

const COMPLETENESS = {
    chapters: [
        {chapterId: 'background', name: '業務背景', percent: 0, satisfied: 0, total: 2, openIssues: 0, state: 'untouched'},
        {chapterId: 'scope', name: 'スコープ', percent: 50, satisfied: 1, total: 2, openIssues: 1, state: 'with-open-issues'},
        {chapterId: 'users', name: '利用者', percent: 100, satisfied: 2, total: 2, openIssues: 0, state: 'settled'},
    ],
    confirmation: {confirmable: false, draftRequirements: ['FR-INV-001'], blockingIssues: []},
}

const CANDIDATES = {
    decisions: [{topic_key: 'background/current-state', body: 'Excel 台帳で管理している。', rationale: '回答で明示',
        evidence_refs: ['S-0001#utt-00002']}],
    open_issues: [{topic: '棚卸の頻度を決める', owner: '', due: '', needs_stakeholder: true,
        blocks_requirement_ids: [], evidence_refs: ['S-0001#utt-00002']}],
    // id_group / kind はバックエンドが抽出時に確定して入れる。画面は表示するだけ。
    requirement_updates: [{operation: 'create', chapter: 'functional-requirements', title: '在庫引当',
        body_after: '受注確定時に在庫を引き当てること。', acceptance_criteria: ['3 秒以内'],
        evidence_refs: [], missing_evidence: true, id_group: 'INV', kind: 'functional'}],
    term_candidates: [],
    contradictions: [],
}

// 工程ガイド。導出はバックエンドが行うため、画面は返り値をそのまま出す。
const GUIDE = {
    stageId: 'dialogue',
    stageLabel: '対話で要件を詰める',
    stageIndex: 2,
    stageTotal: 6,
    next: '対話を続けて、まだ埋まっていない章を詰めます。',
    target: 'dialogue',
    button: '対話へ移る',
    note: '自分では決められない未決事項は、質問票にして関係者へ聞けます。',
    noteTarget: 'questionnaires',
    noteButton: '質問票を開く',
    // 工程の並び。内容の正本はバックエンド（internal/guide）にあり、
    // ここはその形だけを写した最小の据え置き（説明文の検査は Go 側のテスト）。
    stages: [
        {
            id: 'imports',
            label: '資料を取り込む',
            purpose: '手元にある既存の資料を読み込ませ、そこから決定事項や要件の候補を出します。',
            screen: '資料取込',
            target: 'imports',
        },
        {
            id: 'dialogue',
            label: '対話で要件を詰める',
            purpose: 'AI の質問に答えていき、決定事項・未決事項・要件項目を書き出していきます。',
            screen: '対話',
            target: 'dialogue',
        },
    ],
}

describe('対話画面', () => {
    beforeEach(() => {
        paneWidths.mockReset().mockResolvedValue({versions: 220, checks: 360, min: 160, max: 640})
        setPaneWidths.mockReset().mockResolvedValue(undefined)
        listeners.current = []
        openDialogueProject.mockReset()
        openDialogueProject.mockResolvedValue({
            projectPath: '/tmp/proj', phase: 'requirements', targetName: '在庫管理システム', sessions: [],
        })
        closeDialogueProject.mockReset()
        closeDialogueProject.mockResolvedValue(undefined)
        startDialogueSession.mockReset()
        startDialogueSession.mockResolvedValue({id: 'S-0001', type: 'owner', phase: 'requirements', startedAt: ''})
        resumeDialogueSession.mockReset()
        pendingCandidates.mockReset()
        pendingCandidates.mockResolvedValue(null)
        askNextQuestion.mockReset()
        askNextQuestion.mockResolvedValue(true)
        sendAnswer.mockReset()
        sendAnswer.mockResolvedValue('utt-00002')
        interruptDialogue.mockReset()
        interruptDialogue.mockResolvedValue(undefined)
        suspendDialogue.mockReset()
        approveCandidates.mockReset()
        dialogueUtterances.mockReset()
        dialogueUtterances.mockResolvedValue([])
        dialogueCompleteness.mockReset()
        dialogueCompleteness.mockResolvedValue(COMPLETENESS)
        workflowGuide.mockReset()
        workflowGuide.mockResolvedValue(GUIDE)
        usageStatusNow.mockReset()
        // 既定は上限未設定（警告・停止のいずれも出ない状態）。
        usageStatusNow.mockResolvedValue({consumedTokens: 12345, missingRecords: 0, warnRatio: 0, level: 'none'})
        changeSummary.mockReset()
        changeSummary.mockResolvedValue({items: []})
        usageDashboard.mockReset()
        usageDashboard.mockResolvedValue({from: '', to: '', projects: [], markdown: '# AI 利用量'})
        tokenUsage.mockReset()
        tokenUsage.mockResolvedValue({
            path: '/tmp/proj', targetSystemName: '在庫管理システム', tokens: 4200, tokensIn: 4000,
            tokensOut: 200, tokensReasoning: 0, sends: 3, missingRecords: 0,
            lastUsedAt: '2026-08-20T01:30:00Z', limitTokens: null, consumptionRatio: null,
            byProvider: [], bySession: [], latest: null,
        })
        currentPermission.mockReset()
        currentPermission.mockResolvedValue({
            role: 'owner', roleLabel: 'オーナー', canEdit: true, canManageMembers: true,
            canManageUsageLimit: true, reason: '', manageReason: '', usageLimitReason: '',
        })
    })

    /*
     * 工程ガイド。
     *
     * 利用者報告「この画面からの業務と操作手順が全く見当がつかない」への対応。
     * 導出はバックエンドが行うため、画面は**返ってきた内容をそのまま出し、行き先へ移す**ことを確かめる。
     */
    it('いまの工程と次にやることを常時出し、行き先へ移れる', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await screen.findByLabelText('いまの工程と次にやること')

        expect(screen.getByText('2/6')).toBeInTheDocument()
        expect(screen.getByText('対話で要件を詰める')).toBeInTheDocument()
        // 対話の画面ではこの画面での次の一手が優先される（画面をまたぐ案内は引っ込む）。
        expect(await screen.findByText(/「次の質問」を押すと/)).toBeInTheDocument()
        expect(screen.queryByText(GUIDE.next)).toBeNull()

        // 補足の道（関係者に聞く）は、この画面で手が動いている間も引っ込めない。
        // ここにしか導線が無いため、消すとその道が画面から見えなくなる。
        expect(screen.getByText(GUIDE.note)).toBeInTheDocument()
        expect(screen.getByRole('button', {name: '質問票を開く'})).toBeEnabled()
    })

    it('工程ガイドが示した行き先へ、押して移動できる', async () => {
        workflowGuide.mockResolvedValue({
            ...GUIDE,
            stageId: 'dialogue',
            next: '2 件の未決事項が要件をブロックしています。',
            target: 'records',
            button: '決定・未決を開く',
            note: '',
            noteTarget: '',
            noteButton: '',
        })
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        // 対話の画面はこの画面での次の一手を出すため、行き先への操作は引っ込む。
        // 一覧の画面へ移ったあとは、画面をまたぐ案内が出る。
        fireEvent.click(await screen.findByRole('button', {name: '要件項目'}))
        fireEvent.click(await screen.findByRole('button', {name: '決定・未決を開く'}))
        expect(await screen.findByLabelText('決定事項・未決事項')).toBeInTheDocument()
    })

    it('知らない行き先には移動操作を出さない（押しても何も起きないボタンを作らない）', async () => {
        workflowGuide.mockResolvedValue({...GUIDE, target: 'unknown-screen', button: 'どこかへ移る', note: ''})
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: '要件項目'}))
        await screen.findByLabelText('いまの工程と次にやること')
        expect(screen.queryByRole('button', {name: 'どこかへ移る'})).toBeNull()
    })

    /*
     * 工程の全体像。
     *
     * **対話以外の画面からも 1 操作で開ける**ことと、
     * そこから各工程の画面へ移れることを、画面をまたいで確かめる。
     */
    it('対話以外の画面からも 1 操作で工程の全体像を開き、そこから工程の画面へ移れる', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        // まず別の画面（決定・未決）へ移る。
        fireEvent.click(await screen.findByRole('button', {name: '決定・未決'}))
        expect(await screen.findByLabelText('決定事項・未決事項')).toBeInTheDocument()

        // 1 操作（1 クリック）で全体像が開く。
        fireEvent.click(await screen.findByRole('button', {name: '工程の全体像'}))
        const overview = within(screen.getByRole('group', {name: '工程の全体像'}))
        expect(overview.getByText('資料を取り込む')).toBeInTheDocument()
        // 現在地はいま返っている工程（対話）。
        expect(overview.getByText('（いまここ）').closest('li')).toHaveTextContent('対話で要件を詰める')

        // 全体像から工程の画面へ移れる。
        fireEvent.click(overview.getByRole('button', {name: '資料取込を開く'}))
        // 資料取込の画面（左ペインの「取り込む」）が出る。
        expect(await screen.findByText('取り込む')).toBeInTheDocument()
        // 移ったあとは全体像を閉じる（一覧が手元に残らない）。
        expect(screen.queryByRole('group', {name: '工程の全体像'})).toBeNull()
    })

    it('工程ガイドが取れなくても対話は続けられる（手掛かりであり関門にしない）', async () => {
        workflowGuide.mockRejectedValue(new Error('取得できません'))
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await screen.findByLabelText('対話')
        expect(screen.queryByLabelText('いまの工程と次にやること')).toBeNull()
    })

    it('上部ナビの各ボタンは機能を示す 1 文をアプリ内の要素で示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        const nav = [
            '決定・未決', '要件項目', '対話履歴', '質問票', '回答取込', '資料取込', '進捗レポート',
            'メンバー', '同期', 'AI 利用量', '消費実績', '取り込んだ変更', '要件定義書', 'プロジェクト一覧へ戻る',
        ]
        for (const name of nav) {
            const button = screen.getByRole('button', {name})
            // ブラウザ標準の title に頼らない
            expect(button).not.toHaveAttribute('title')
            fireEvent.mouseOver(button)
            const tip = screen.getByRole('tooltip')
            // 1 文（句点で終わる）であり、ラベルの言い換えだけで終わらない
            expect(tip.textContent?.endsWith('。')).toBe(true)
            expect(tip.textContent?.length ?? 0).toBeGreaterThan(name.length)
            expect(button.getAttribute('aria-describedby')).toBe(tip.id)
            fireEvent.mouseOut(button)
            expect(screen.queryByRole('tooltip')).toBeNull()
        }
    })

    // 対話画面の共通メニュー → 消費実績 → AI 利用量ダッシュボード。
    it('共通メニューから消費実績を開き、ダッシュボードへ渡せる', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        await clickEnabled('消費実績')
        expect(await screen.findByLabelText('トークン消費実績')).toBeInTheDocument()
        // 開いているプロジェクトが対象（パスは渡さない）。
        await waitFor(() => expect(tokenUsage).toHaveBeenCalledWith({path: ''}))

        await clickEnabled('期間別・横断で見る（AI 利用量）')
        expect(await screen.findByLabelText('AI 利用量ダッシュボード')).toBeInTheDocument()
    })

    // 共通メニュー → ダッシュボード → 上限設定（プロジェクトを開いたまま）。
    it('共通メニューからダッシュボードを開き、オーナーは上限設定へ進める', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        await clickEnabled('AI 利用量')
        expect(await screen.findByLabelText('AI 利用量ダッシュボード')).toBeInTheDocument()
        await waitFor(() => expect(usageDashboard).toHaveBeenCalled())

        const toLimit = await waitFor(() => {
            const b = screen.getByRole('button', {name: '上限を設定する'})
            expect(b.hasAttribute('disabled')).toBe(false)
            return b
        })
        fireEvent.click(toLimit)
        expect(await screen.findByLabelText('トークン上限設定')).toBeInTheDocument()
        // 上限設定を開いてもプロジェクトは開いたまま（閉じると権限判定も設定もできなくなる）。
        expect(closeDialogueProject).not.toHaveBeenCalled()
    })

    // ステータスラインのトークン消費は実際の累計を表示する。
    it('ステータスラインに実際の累計トークン消費を表示する（上限設定時は「消費 / 上限」）', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(usageStatusNow).toHaveBeenCalled())

        // 上限未設定のときは累計のみ。
        expect(await screen.findByText('12,345')).toBeInTheDocument()

        usageStatusNow.mockResolvedValue({
            consumedTokens: 82000, missingRecords: 0, limitTokens: 100000, warnRatio: 0.8,
            remainingTokens: 18000, consumptionRatio: 0.82, level: 'warn',
        })
        // AI 呼び出しの完了時に読み直す（消費は呼び出しのたびに変わる）。
        await clickEnabled('次の質問')
        await waitFor(() => expect(askNextQuestion).toHaveBeenCalled())
        emit({kind: 'done', state: 'awaiting-answer'})

        expect(await screen.findByText('82,000 / 100,000')).toBeInTheDocument()
    })

    // 警告閾値以上では橙の警告バナーを出し、AI 呼び出しの開始操作は妨げない。
    it('警告閾値以上では残りトークン数を警告として表示し、送信操作は妨げない', async () => {
        usageStatusNow.mockResolvedValue({
            consumedTokens: 82000, missingRecords: 0, limitTokens: 100000, warnRatio: 0.8,
            remainingTokens: 18000, consumptionRatio: 0.82, level: 'warn',
        })
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)

        const banner = await screen.findByText(/上限まで残り 18,000 トークン/)
        expect(banner).toBeInTheDocument()
        // 警告中でも「次の質問」は押せる（操作を妨げない）。
        expect(screen.getByRole('button', {name: '次の質問'}).hasAttribute('disabled')).toBe(false)
    })

    // エラーカタログ「利用量上限系」: 上限到達は赤で理由・再開方法・導線を示す。
    it('上限到達では停止の理由と再開方法・ダッシュボードへの導線を表示する', async () => {
        usageStatusNow.mockResolvedValue({
            consumedTokens: 100000, missingRecords: 0, limitTokens: 100000, warnRatio: 0.8,
            remainingTokens: 0, consumptionRatio: 1, level: 'blocked',
        })
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)

        expect(await screen.findByText(/上限に達したため/)).toBeInTheDocument()
        expect(screen.getByText(/オーナーが上限を変更すると再開できます/)).toBeInTheDocument()

        fireEvent.click(screen.getByRole('button', {name: 'AI 利用量を確認する'}))
        expect(await screen.findByLabelText('AI 利用量ダッシュボード')).toBeInTheDocument()
    })

    // 警告・停止は「AI 呼び出しを伴う各画面」に出す（対話画面だけではない）。
    it('AI 呼び出しを伴う他の画面でも警告を表示し、伴わない画面では表示しない', async () => {
        usageStatusNow.mockResolvedValue({
            consumedTokens: 82000, missingRecords: 0, limitTokens: 100000, warnRatio: 0.8,
            remainingTokens: 18000, consumptionRatio: 0.82, level: 'warn',
        })
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await screen.findByText(/上限まで残り 18,000 トークン/)

        // 資料取込（資料の分析 = AI 呼び出しあり）へ移っても警告は出続ける。
        fireEvent.click(screen.getByRole('button', {name: '資料取込'}))
        expect(await screen.findByText(/上限まで残り 18,000 トークン/)).toBeInTheDocument()

        // メンバー管理（AI 呼び出しなし）では出さない。
        fireEvent.click(screen.getByRole('button', {name: 'メンバー'}))
        await waitFor(() => expect(screen.queryByText(/上限まで残り/)).toBeNull())
    })

    // 上限未設定なら警告・停止のいずれも出さない。
    it('上限未設定では警告も停止表示も出さない', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(usageStatusNow).toHaveBeenCalled())

        expect(screen.queryByText(/上限まで残り/)).toBeNull()
        expect(screen.queryByText(/上限に達したため/)).toBeNull()
    })

    // 何も動いていないのに「質問を作成中」と出さない。
    //
    // 新しいセッションを始めた直後、質問はまだ作られておらず生成も走っていない。
    // 「作成中」と出すと、利用者は待てば質問が出ると受け取り、いつまでも待つことになる
    // （2026-09-04 に実機で発生）。次に押す操作を示す。
    it('質問がまだ無いときは、進行中と偽らずに次の操作を示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        // 状態は「待機中」と正直に言い、次に押すものは工程ガイドが示す（次の一手は 1 か所に寄せた）。
        expect(await screen.findByText('待機中')).toBeInTheDocument()
        expect(screen.queryByText('質問を作成中')).toBeNull()
        expect(screen.getByLabelText('いまの工程と次にやること').textContent).toContain('「次の質問」を押すと')
        // 何も要求していないこと（勝手に AI を呼ばない）。
        expect(askNextQuestion).not.toHaveBeenCalled()
    })

    // 生成を要求している間は「質問を作成中」を出す（こちらは実際に進行中）。
    it('質問の生成中は作成中と示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        await clickEnabled('次の質問')
        await waitFor(() => expect(askNextQuestion).toHaveBeenCalledWith('S-0001'))

        expect(await screen.findByText('質問を作成中')).toBeInTheDocument()
    })

    // 応答は逐次追記で描画する。
    it('本文差分を受け取るたびに追記して表示する', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        await clickEnabled('次の質問')
        await waitFor(() => expect(askNextQuestion).toHaveBeenCalledWith('S-0001'))

        emit({kind: 'text', text: '現状の在庫管理は'})
        expect(await screen.findByText(/現状の在庫管理は/)).toBeInTheDocument()
        emit({kind: 'text', text: 'どのように行っていますか。'})
        expect(await screen.findByText(/現状の在庫管理はどのように行っていますか。/)).toBeInTheDocument()
    })

    // 応答開始までの待機中は状態表示を出す。
    it('応答を待っている間は待機中であることを示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        await clickEnabled('次の質問')
        expect(await screen.findByText('応答を待っています…')).toBeInTheDocument()

        emit({kind: 'text', text: '質問です。'})
        await waitFor(() => expect(screen.queryByText('応答を待っています…')).toBeNull())
    })

    // 送信操作の直後に状態表示へ切り替える（受理イベントを待たない）。
    it('送信すると即座に応答待ちの表示へ切り替わる', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        fireEvent.change(screen.getByLabelText('回答'), {target: {value: 'Excel 台帳です。'}})
        await clickEnabled('送信')

        // バインディングの応答を待たずに待機表示が出る。
        expect(await screen.findByText('応答を待っています…')).toBeInTheDocument()
        await waitFor(() => expect(sendAnswer).toHaveBeenCalledWith('S-0001', 'Excel 台帳です。'))
    })

    // 送信に失敗したら入力テキストを保持して再送できる。
    it('送信に失敗しても入力内容を失わない', async () => {
        sendAnswer.mockRejectedValue(new Error('接続できません'))
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        fireEvent.change(screen.getByLabelText('回答'), {target: {value: 'Excel 台帳です。'}})
        await clickEnabled('送信')

        await screen.findByText(/接続できません/)
        expect(screen.getByLabelText('回答')).toHaveValue('Excel 台帳です。')
    })

    // ストリーミング中は送信ボタンが中断ボタンに切り替わる。
    it('応答中は中断ボタンに切り替わり、中断すると履歴に残る', async () => {
        dialogueUtterances.mockResolvedValue([
            {id: 'utt-00001', speaker: 'agent', at: '2026-08-27T10:00:00Z', status: 'interrupted', body: '途中まで'},
        ])
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        await clickEnabled('次の質問')
        expect(await screen.findByRole('button', {name: '中断'})).toBeInTheDocument()
        expect(screen.queryByRole('button', {name: '送信'})).toBeNull()

        await clickEnabled('中断')
        await waitFor(() => expect(interruptDialogue).toHaveBeenCalled())

        emit({kind: 'done', state: 'suspended', interrupted: true})
        expect(await screen.findByText(/中断しました/)).toBeInTheDocument()
        // 中断発話は「中断」ラベル付きで履歴に残る。
        expect(await screen.findByText('中断')).toBeInTheDocument()
        expect(screen.getByRole('button', {name: '送信'})).toBeInTheDocument()
    })

    /*
     * 抽出待機中の可視化。
     *
     * 回答を送ってから候補が届くまでの見せ方（右ペインのスケルトン・回答を読めるままの沈み込み・
     * 1 サイクルの現在地・0 件や読み取り失敗のときの案内）を確かめる。
     * 「送ってみるまで何が起きるか分からない」を、待ち時間に**結果の型を先に見せる**ことで解く。
     */
    it('回答を送ると、右ペインに承認 / 破棄の輪郭を持つスケルトンが 2 枚出る', async () => {
        sendAnswer.mockResolvedValue('utt-2')
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        fireEvent.change(screen.getByLabelText('回答'), {target: {value: '受注確定時に引き当てます。'}})
        await clickEnabled('送信')
        await waitFor(() => expect(sendAnswer).toHaveBeenCalled())

        const panel = await screen.findByLabelText('抽出候補')
        expect(await within(panel).findByText('抽出候補（抽出中）')).toBeInTheDocument()
        // 「何をする場所か」を伝える輪郭。枚数は 2 枚に固定する。
        expect(within(panel).getAllByText('承認')).toHaveLength(2)
        expect(within(panel).getAllByText('破棄')).toHaveLength(2)
    })

    it('抽出中も回答は読めるまま（マスクしない）で、中断は押せる', async () => {
        sendAnswer.mockResolvedValue('utt-2')
        dialogueUtterances.mockResolvedValue([
            {id: 'utt-1', speaker: 'agent', at: '2026-09-08 10:00', body: '在庫はいつ引き当てますか。', status: 'done'},
        ])
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        fireEvent.change(screen.getByLabelText('回答'), {target: {value: '受注確定時に引き当てます。'}})
        await clickEnabled('送信')
        await waitFor(() => expect(sendAnswer).toHaveBeenCalled())

        // 本文は隠さない（送信直後は自分が書いた内容を読み返す場面が多い）。
        expect(await screen.findByText('在庫はいつ引き当てますか。')).toBeInTheDocument()
        // 入力は止め、待つ理由をその場に書く。
        const input = screen.getByLabelText('回答') as HTMLTextAreaElement
        await waitFor(() => expect(input).toBeDisabled())
        expect(input.placeholder).toBe('抽出が終わるまでお待ちください')
        // 中断は有効のまま（止める手段を奪わない）。
        expect(screen.getByRole('button', {name: '中断'})).toBeEnabled()
    })

    it('抽出中は 1 サイクルの現在地が「抽出」を指す', async () => {
        sendAnswer.mockResolvedValue('utt-2')
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await screen.findByLabelText('いまの工程と次にやること')
        // 送る前は「質問」。
        expect(screen.getByLabelText('いまのやり取りの段階: 質問')).toBeInTheDocument()

        fireEvent.change(screen.getByLabelText('回答'), {target: {value: '受注確定時に引き当てます。'}})
        await clickEnabled('送信')
        expect(await screen.findByLabelText('いまのやり取りの段階: 抽出')).toBeInTheDocument()

        // 候補が届いたら「承認」へ進む。
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})
        expect(await screen.findByLabelText('いまのやり取りの段階: 承認')).toBeInTheDocument()
    })

    it('抽出が 0 件で終わったら、スケルトンを畳んで出なかったことを言う', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({
            kind: 'candidates',
            state: 'awaiting-approval',
            extraction: {decisions: [], open_issues: [], requirement_updates: []},
        })
        const panel = await screen.findByLabelText('抽出候補')
        expect(within(panel).getByText('今回の回答からは候補が出ませんでした')).toBeInTheDocument()
        // 次に取る手を添える（行き止まりにしない）。
        expect(within(panel).getByText(/「次の質問」で先へ進むか/)).toBeInTheDocument()
        expect(within(panel).queryByText('抽出候補（抽出中）')).toBeNull()
    })

    it('抽出が読み取れなかったときは、何が起きたかと次の手を示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({kind: 'fallback', state: 'awaiting-approval', text: '（応答の生テキスト）'})
        const panel = await screen.findByLabelText('抽出候補')
        expect(within(panel).getByText(/形式どおりの分析結果が得られませんでした/)).toBeInTheDocument()
        expect(within(panel).getByText(/手で起票してください/)).toBeInTheDocument()
        // スケルトンを残して待たせない。
        expect(within(panel).queryByText('抽出候補（抽出中）')).toBeNull()
    })

    it('候補がまだ無いときは、何が起きると並ぶのかを書く（操作名を主語にしない）', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        const panel = await screen.findByLabelText('抽出候補')
        expect(within(panel).getByText('まだ候補はありません')).toBeInTheDocument()
        expect(
            within(panel).getByText(/回答を送信すると、ここに要件候補が並びます。/),
        ).toBeInTheDocument()
        expect(within(panel).getByText(/左の完成度に反映されます。/)).toBeInTheDocument()
    })

    /*
     * 反映の結果が伝わること。
     *
     * 利用者報告「選んだ内容で反映のボタンをクリックしても画面が変わらないので
     * 何の処理が行われたのかわからない」。原因は 2 つで、どちらもここで押さえる。
     */
    it('1 件も選んでいないうちは反映できず、理由を示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        const apply = within(panel).getByRole('button', {name: '選んだ内容で反映'})
        expectDisabledReason(apply, '候補ごとに「承認」か「破棄」を選んでください。')
        // 押しても何も起きない状態を作らない（バインディングは呼ばれない）。
        fireEvent.click(apply)
        expect(approveCandidates).not.toHaveBeenCalled()
    })

    it('選ぶと何件が反映されるかを押す前に示し、結果はその場の通知で伝える', async () => {
        approveCandidates.mockResolvedValue({
            applied: {decisionIds: ['DEC-001'], state: 'questioning'},
        })
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        const decision = within(panel).getByLabelText('決定事項候補 1').closest('article')!
        await clickEnabledBy(() => within(decision).getByRole('button', {name: '承認'}))

        // 押す前に、何が反映されるかが読める。
        expect(within(panel).getByText('承認 1 件 ／ 破棄 0 件')).toBeInTheDocument()

        await clickEnabled('選んだ内容で反映')
        await waitFor(() => expect(approveCandidates).toHaveBeenCalled())

        // 結果は一過性の通知で出す（中央ペイン上端のバナーは、操作した右ペインから見えない）。
        // 通知は数秒で自動で消えるため、現れたらすぐ確かめる（別の要素を待って時間を使わない）。
        const notice = await screen.findByText(/決定事項 1 件を記録しました/)
        expect(notice.closest('[role="status"]'), '反映の結果が通知として読み上げられる場所に出ていない').not.toBeNull()
        expect(screen.getByRole('button', {name: '通知を閉じる'})).toBeInTheDocument()
    })

    it('候補の承認を案内する文言が、実際のボタン名と一致している', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        const label = within(panel).getByRole('button', {name: /反映/}).textContent ?? ''
        expect(label).not.toBe('')
        // 案内が実在しないボタン名を指していない（押せない操作へ案内しない）。
        await waitFor(() =>
            expect(screen.getByLabelText('いまの工程と次にやること').textContent).toContain(label),
        )
    })

    // 抽出候補は 3 区分で、候補ごとに承認 / 破棄と根拠参照を持つ。
    it('抽出候補を 3 区分で表示し、承認した候補だけを反映する', async () => {
        approveCandidates.mockResolvedValue({decisionIds: ['DEC-001'], state: 'questioning'})
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        expect(within(panel).getByText('決定事項候補')).toBeInTheDocument()
        expect(within(panel).getByText('未決事項候補')).toBeInTheDocument()
        expect(within(panel).getByText('要件項目への反映案')).toBeInTheDocument()
        // 根拠となる発話への参照を表示する。
        expect(within(panel).getAllByText(/S-0001#utt-00002/).length).toBeGreaterThan(0)
        // 根拠が無い候補は、何が問題かを文で示す。
        // 「参照欠落」の語だけを出さない。要件項目は根拠なしでも記録できることも言う。
        const reqItem = within(panel).getByLabelText('要件項目の反映案 1').closest('article')!
        expect(within(reqItem).getByRole('note').textContent).toContain('根拠にした発言を特定できませんでした')
        expect(within(reqItem).getByRole('note').textContent).toContain('承認はできますが')
        expect(within(reqItem).getByRole('button', {name: '承認'})).toBeEnabled()

        // 決定事項候補だけ承認する（未決・要件は未選択のまま = 反映しない）。
        const decisionItem = within(panel).getByLabelText('決定事項候補 1').closest('article')!
        await clickEnabledBy(() => within(decisionItem).getByRole('button', {name: '承認'}))
        await clickEnabledBy(() => within(panel).getByRole('button', {name: '選んだ内容で反映'}))

        await waitFor(() => expect(approveCandidates).toHaveBeenCalled())
        const [, request] = approveCandidates.mock.calls[0]
        expect(request.decisions).toHaveLength(1)
        expect(request.openIssues).toHaveLength(0)
        expect(request.requirementUpdates).toHaveLength(0)
    })

    // 根拠を特定できない決定事項候補は、理由を文で示し、承認を選べなくする
    // （押してから失敗させない。決定事項は根拠が無いと記録できない）。
    it('根拠を特定できない決定事項候補は理由を示して承認を選べなくする', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({
            kind: 'candidates',
            state: 'awaiting-approval',
            extraction: {
                ...CANDIDATES,
                decisions: [{...CANDIDATES.decisions[0], evidence_refs: null, missing_evidence: true}],
            },
        })

        const panel = await screen.findByLabelText('抽出候補')
        const item = within(panel).getByLabelText('決定事項候補 1').closest('article')!
        expect(within(item).getByRole('note').textContent).toContain(
            'このままでは記録できないため、この候補は破棄してください。',
        )
        expect(within(item).queryByText('参照欠落')).not.toBeInTheDocument()
        expectDisabledReason(within(item).getByRole('button', {name: '承認'}), /根拠にした発言を特定できない/)
        // 破棄は選べる（候補を片づける手段は残す）。
        expect(within(item).getByRole('button', {name: '破棄'})).toBeEnabled()
    })

    // 反映の失敗は押したボタンのそばに、「Error: 」を付けずに出す。
    it('反映に失敗した理由を候補パネルの中の反映ボタンのそばに出す', async () => {
        approveCandidates.mockRejectedValue(
            new Error('未決事項候補「棚卸の頻度を決める」の「決める人」が空です。決める人を入れてから反映してください。'),
        )
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        const issueItem = within(panel).getByText('棚卸の頻度を決める').closest('article')!
        await clickEnabledBy(() => within(issueItem).getByRole('button', {name: '承認'}))
        // 決める人は押す前の確認で求められる。ここではバックエンドが拒んだ場合を見る。
        fireEvent.change(within(issueItem).getByLabelText('未決事項候補 1 の決める人'), {target: {value: '鈴木'}})
        await clickEnabledBy(() => within(panel).getByRole('button', {name: '選んだ内容で反映'}))

        const alert = await within(panel).findByRole('alert')
        expect(alert.textContent).toContain('反映できませんでした。何も記録していません。')
        expect(alert.textContent).toContain('「決める人」が空です')
        expect(alert.textContent).not.toContain('Error')
        // 反映ボタンの直前に置く（右ペインで押した利用者の視界に入る位置）。
        expect(alert.nextElementSibling?.className).toContain('rw-candidates__actions')
        // 候補は残り、直して押し直せる。
        expect(within(panel).getByText('棚卸の頻度を決める')).toBeInTheDocument()
    })

    // 新しい質問が末尾に出たら、履歴を最新の発話が見える位置へ送る。
    // 上へ戻って読んでいる間は引き戻さず、「次の質問」を押したら末尾へ戻す。
    // jsdom は高さを計算しないため、履歴の高さ（scrollHeight）だけを与えて配線を確かめる
    // （寸法での振る舞いは ui/useFollowLatest.layout.test.tsx が実ブラウザで確かめる）。
    it('新しい質問が届くと履歴を末尾へ送り、読み返し中は引き戻さず、次の質問で末尾へ戻る', async () => {
        const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight')
        Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
            configurable: true,
            get() {
                return (this as HTMLElement).classList.contains('rw-dialogue__stream') ? 1000 : 0
            },
        })
        try {
            askNextQuestion.mockResolvedValue(true)
            render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
            await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
            const history = await screen.findByLabelText('対話履歴')

            // 開いた直後から末尾に付いている → 受信中の本文で末尾へ送る。
            await act(async () => emit({kind: 'text', text: '次の論点について伺います。'}))
            expect(history.scrollTop, '新しい質問の本文が届いても末尾へ送られない').toBe(1000)

            // 利用者が上へ戻って読む → 続きの本文が届いても引き戻さない。
            await act(async () => {
                history.scrollTop = 0
                fireEvent.scroll(history)
            })
            await act(async () => emit({kind: 'text', text: '（続き）'}))
            expect(history.scrollTop, '読み返している途中で末尾へ引き戻された').toBe(0)

            // 応答が終わってから「次の質問」を押す → 末尾へ戻す（押した本人が見に行く）。
            await act(async () => emit({kind: 'done', state: 'awaiting-answer'}))
            await clickEnabled('次の質問')
            expect(history.scrollTop, '次の質問を押しても末尾へ戻らない').toBe(1000)
        } finally {
            if (height) {
                Object.defineProperty(HTMLElement.prototype, 'scrollHeight', height)
            } else {
                delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollHeight
            }
        }
    })

    // 新規の要件項目の ID は ReqWeave が付ける。利用者に入力させない。
    it('新規の要件項目は ID を入力させず、記録される ID の形を示して、そのまま反映できる', async () => {
        approveCandidates.mockResolvedValue({applied: {requirementIds: ['FR-INV-001'], state: 'questioning'}})
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        const reqItem = within(panel).getByLabelText('要件項目の反映案 1').closest('article')!
        // 入力欄は無い。記録される ID の形（連番は nnn）と、誰が付けるかを示す。
        expect(within(reqItem).queryByLabelText(/ID グループ/)).toBeNull()
        expect(reqItem.textContent).toContain('新規 FR-INV-nnn')
        expect(reqItem.textContent).toContain('ID は記録するときに ReqWeave が付けます')

        await clickEnabledBy(() => within(reqItem).getByRole('button', {name: '承認'}))
        // 何も入れなくても反映できる（押す前の入力不足にならない）。
        await clickEnabledBy(() => within(panel).getByRole('button', {name: '選んだ内容で反映'}))
        await waitFor(() => expect(approveCandidates).toHaveBeenCalled())
        const [, request] = approveCandidates.mock.calls[0]
        expect(request.requirementUpdates).toHaveLength(1)
        // 画面は ID グループを決め直さない（候補に確定済みの値をそのまま渡す）。
        expect(request.requirementUpdates[0].group).toBeUndefined()
        expect(request.requirementUpdates[0].candidate.id_group).toBe('INV')
    })

    // 非機能要件の章の項目は NFR- になる（種別も利用者に選ばせない）。
    it('非機能要件の章の新規項目は NFR の ID の形で示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({
            kind: 'candidates',
            state: 'awaiting-approval',
            extraction: {
                ...CANDIDATES,
                requirement_updates: [
                    {
                        ...CANDIDATES.requirement_updates[0],
                        chapter: 'non-functional-requirements',
                        title: '応答時間',
                        id_group: 'QUAL',
                        kind: 'non-functional',
                    },
                ],
            },
        })
        const panel = await screen.findByLabelText('抽出候補')
        const reqItem = within(panel).getByLabelText('要件項目の反映案 1').closest('article')!
        expect(reqItem.textContent).toContain('新規 NFR-QUAL-nnn')
    })

    // 未決事項の決める人も押す前に求める。
    it('決める人が空の未決事項候補を承認すると、反映を押す前に理由を示して押せなくする', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        const panel = await screen.findByLabelText('抽出候補')
        const issueItem = within(panel).getByText('棚卸の頻度を決める').closest('article')!
        await clickEnabledBy(() => within(issueItem).getByRole('button', {name: '承認'}))
        expectDisabledReason(
            within(panel).getByRole('button', {name: '選んだ内容で反映'}),
            '未決事項候補「棚卸の頻度を決める」の「決める人」を入れてください。',
        )
        expect(approveCandidates).not.toHaveBeenCalled()
    })

    // 編集して承認したときは編集後の本文が渡る。
    it('候補を編集して承認すると編集後の本文で反映する', async () => {
        approveCandidates.mockResolvedValue({decisionIds: ['DEC-001'], state: 'questioning'})
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        // 候補は既定で読むだけ。直すときは「編集」で入力欄へ切り替える
        const shown = await screen.findByLabelText('決定事項候補 1')
        expect(shown.tagName).toBe('P')
        const item = shown.closest('article')!
        await clickEnabledBy(() => within(item).getByRole('button', {name: '編集'}))
        const body = await screen.findByLabelText('決定事項候補 1')
        expect(body.tagName).toBe('TEXTAREA')
        fireEvent.change(body, {target: {value: 'Excel 台帳と紙の受払簿を併用している。'}})
        await clickEnabledBy(() => within(item).getByRole('button', {name: '承認'}))
        await clickEnabled('選んだ内容で反映')

        await waitFor(() => expect(approveCandidates).toHaveBeenCalled())
        const [, request] = approveCandidates.mock.calls[0]
        expect(request.decisions[0].candidate.body).toContain('紙の受払簿')
    })

    // 未決事項候補の承認には「誰が・いつまでに」の入力欄を出す。
    it('未決事項候補に決める人と期限の入力欄を出す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())
        emit({kind: 'candidates', state: 'awaiting-approval', extraction: CANDIDATES})

        expect(await screen.findByLabelText('未決事項候補 1 の決める人')).toBeInTheDocument()
        expect(screen.getByLabelText('未決事項候補 1 の期限')).toBeInTheDocument()
    })

    // 完成度は章観点ごとに数値と 3 状態を色と文字ラベルの併用で示す。
    it('完成度を章観点ごとに数値と状態ラベルで常設表示する', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        const panel = await screen.findByLabelText('完成度')

        expect(within(panel).getByText('業務背景')).toBeInTheDocument()
        expect(within(panel).getByText('0%')).toBeInTheDocument()
        expect(within(panel).getByText('未着手')).toBeInTheDocument()
        expect(within(panel).getByText('50%')).toBeInTheDocument()
        expect(within(panel).getByText('記載あり・未決あり')).toBeInTheDocument()
        expect(within(panel).getByText('100%')).toBeInTheDocument()
        expect(within(panel).getByText('記載あり・未決なし')).toBeInTheDocument()
        // 確定可否はバックエンドの算出値をそのまま示す（UI 側で再計算しない）。
        expect(within(panel).getByText(/未合意の要件項目が 1 件あります/)).toBeInTheDocument()
    })

    // 中断したセッションがあれば再開し、前回の質問を示す。
    it('既存セッションがあれば再開して案内を出す', async () => {
        openDialogueProject.mockResolvedValue({
            projectPath: '/tmp/proj', phase: 'requirements', targetName: '在庫管理システム',
            sessions: [{id: 'S-0001', type: 'owner', phase: 'requirements', startedAt: ''}],
        })
        resumeDialogueSession.mockResolvedValue({
            state: 'suspended',
            presentedQuestion: {topicKey: 'background/current-state', text: '現状は？', followUpIndex: 1, answered: false},
            openIssues: [],
            hasPendingCandidates: false,
        })
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)

        expect(await screen.findByText(/中断した対話を再開しました/)).toBeInTheDocument()
        expect(startDialogueSession).not.toHaveBeenCalled()
        expect(screen.getByText('中断中')).toBeInTheDocument()
    })

    // エラーは原因と次の行動を日本語 1 文で示す（内部コードを出さない）。
    it('エラーは原因と次の行動を日本語で示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({kind: 'error', state: 'suspended', errorClass: 'transient', message: 'HTTP 503 upstream'})
        const notice = await screen.findByText(/通信を確認して、もう一度お試しください/)
        expect(notice.textContent).not.toContain('503')
        expect(screen.getByText('中断中')).toBeInTheDocument()
    })

    /*
     * 文言の正本はバックエンドのエラーカタログ（分類 × コード）で、
     * 画面は届いた `userMessage` をそのまま出す（画面側にコード別の表を持たない）。
     * サインインが切れたときは、再サインインという取れる操作が分かること。
     */
    it('バックエンドが作った文言（userMessage）をそのまま表示する', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({
            kind: 'error',
            state: 'suspended',
            errorClass: 'config',
            errorCode: 'unauthorized',
            message: 'HTTP 401 unauthorized',
            userMessage:
                'ChatGPT のアカウントのサインインが切れました。設定画面でサインインし直してください。',
        })

        const notice = await screen.findByText(/ChatGPT のアカウントのサインインが切れました/)
        expect(notice.textContent).toContain('設定画面でサインインし直してください')
        // 分類ごとの既定文（キーとモデルの確認）へ倒さない
        expect(notice.textContent).not.toContain('キーとモデルを確認')
        expect(notice.textContent).not.toContain('401')
    })

    // 既決の論点だったときは作り直しを利用者に示し、表示中の本文を捨てる。
    it('既決の論点なら質問の作り直しを示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({kind: 'text', text: '決定済みの論点への質問'})
        expect(await screen.findByText(/決定済みの論点への質問/)).toBeInTheDocument()
        emit({kind: 'replaced'})
        await waitFor(() => expect(screen.queryByText(/決定済みの論点への質問/)).toBeNull())
        expect(screen.getByText(/質問を作り直しています/)).toBeInTheDocument()
    })

    // 形式どおりの分析結果が得られないときは応答原文を示して手動起票へ誘導する。
    it('分析結果を読み取れないときは応答原文と次の行動を示す', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        emit({kind: 'fallback', state: 'awaiting-approval', text: '候補はありません。'})
        await waitFor(() => expect(screen.getAllByText(/手動で起票してください/).length).toBeGreaterThan(0))
        expect(screen.getByText('候補はありません。')).toBeInTheDocument()
    })

    // 過去の発話に書き換え操作を設けない。
    it('過去の発話に編集操作を持たない', async () => {
        dialogueUtterances.mockResolvedValue([
            {id: 'utt-00001', speaker: 'agent', at: '2026-08-27T10:00:00Z', status: 'completed', body: '質問です。'},
            {id: 'utt-00002', speaker: 'user', at: '2026-08-27T10:01:00Z', status: 'completed', body: '回答です。'},
        ])
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)

        const history = await screen.findByLabelText('対話履歴')
        expect(await within(history).findByText('質問です。')).toBeInTheDocument()
        expect(within(history).queryByRole('textbox')).toBeNull()
        expect(within(history).queryByRole('button')).toBeNull()
    })
})
