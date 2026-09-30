import {cleanup, fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Dialogue} from './Dialogue'
import {ProjectList} from './ProjectList'
import {QuestionnaireManager} from './Questionnaires'
import {DecisionsAndIssues, DialogueHistory, RequirementList} from './Records'

/*
 * 上限規模での画面応答の基準のうち**描画側**を実ブラウザで計測する。
 *
 * ---- なぜ 2 か所で測るのか ------------------------------------------------
 *
 * 画面の応答は「バインディング層より下（ディスク I/O・解釈）」＋「描画」からなる。
 * アプリの webview を自動操作する手段が無いため（e2e は DOM 到達までで操作はできない）、
 * **2 つを別の段で測り、それぞれに配分した持ち分を課す**。持ち分の合計は基準値と一致する。
 *
 *	基準（画面応答）          バインディング層               本テスト（描画）
 *	(1) 開く→対話画面 3,000ms   2,000ms                        1,000ms
 *	(2) 画面遷移・一覧 1,000ms     660ms                          340ms
 *	(3) 履歴の走査     2,000ms   1,330ms                          670ms
 *
 * バインディング層側は `internal/binding/scale_response_integration_test.go`。
 *
 * ---- 計測が空振りにならないための約束 --------------------------------------
 *
 * 件数はデータ量の**上限値そのもの**を使い（LIMITS）、
 * 各計測は「上限件数ぶんが実際に DOM へ出たこと」を待ってから止める。
 * 描画されていない状態で時間を止めると、速く見えるだけの数字になる。
 *
 * jsdom ではなく実ブラウザで測る理由: jsdom はレイアウトを計算しないため、
 * **上限件数の描画コストが出ない**（幅も高さも 0 のまま返る）。
 *
 * ---- 検索について ----------------------------------------------------------
 *
 * 基準(3)は「対話履歴のスクロール・検索結果表示」で、この「検索結果表示」に
 * 対応する機能は **対話履歴の全文検索**である
 * （全文検索を入れる前は、実装されている絞り込みを基準(3)の対象として測っていた）。
 * ここでは絞り込み・スクロールに加えて**検索結果の描画**を測る。
 * 検索結果は上限（SEARCH_LIMIT）で切られるため、描画側の上限規模は 200 件である。
 */

/** データ量の上限目安（件数のみ。総量はバインディング層側で扱う）。 */
const LIMITS = {
    sessions: 100,
    utterances: 1000,
    requirements: 1000,
    decisions: 500,
    openIssues: 500,
    questionnaires: 100,
    projects: 50,
}

/** 持ち分（上の表の「本テスト（描画）」列。単位はミリ秒）。 */
const BUDGET = {
    openDialogue: 1000,
    screenSwitch: 340,
    historyBrowse: 670,
}

/** 試行回数（5 回計測して全て基準を満たすこと）。 */
const TRIALS = 5

// ---- バインディングの差し替え（描画だけを測るため、取得は即座に解決させる）----

const projects = vi.hoisted(() => vi.fn())
const migratableProjects = vi.hoisted(() => vi.fn())
const domainPresets = vi.hoisted(() => vi.fn())
const syncKindOptions = vi.hoisted(() => vi.fn())
const projectFolderNameFor = vi.hoisted(() => vi.fn())
const requirements = vi.hoisted(() => vi.fn())
const decisions = vi.hoisted(() => vi.fn())
const openIssues = vi.hoisted(() => vi.fn())
const changeHistory = vi.hoisted(() => vi.fn())
const dialogueSessions = vi.hoisted(() => vi.fn())
const dialogueUtterances = vi.hoisted(() => vi.fn())
const searchDialogueUtterances = vi.hoisted(() => vi.fn())
const questionnaires = vi.hoisted(() => vi.fn())
const roster = vi.hoisted(() => vi.fn())
const openDialogueProject = vi.hoisted(() => vi.fn())
const resumeDialogueSession = vi.hoisted(() => vi.fn())
const pendingCandidates = vi.hoisted(() => vi.fn())
const dialogueCompleteness = vi.hoisted(() => vi.fn())
const workflowGuide = vi.hoisted(() => vi.fn())
const changeSummary = vi.hoisted(() => vi.fn())
const idRangeWarnings = vi.hoisted(() => vi.fn())
const usageStatusNow = vi.hoisted(() => vi.fn())

// 実ブラウザでは**存在しない名前を import した時点で失敗する**（jsdom と違い後回しにならない）。
// 対話画面は多くの画面を取り込むため、生成された定義を土台にして必要な口だけ差し替える
// （差し替えない口は呼ばれない経路にある。呼ばれれば window.go が無いことで即座に失敗する
// ＝ 取りこぼしを黙って通さない）。
vi.mock('../../wailsjs/go/binding/API', async (importOriginal) => ({
    ...(await importOriginal<Record<string, unknown>>()),
    // プロジェクト一覧
    Projects: projects,
    MigratableProjects: migratableProjects,
    DomainPresets: domainPresets,
    SyncKindOptions: syncKindOptions,
    ProjectFolderNameFor: projectFolderNameFor,
    // 決定・未決 / 要件項目 / 対話履歴
    Requirements: requirements,
    Decisions: decisions,
    OpenIssues: openIssues,
    ChangeHistory: changeHistory,
    DialogueSessions: dialogueSessions,
    DialogueUtterances: dialogueUtterances,
    SearchDialogueUtterances: searchDialogueUtterances,
    // 質問票
    Questionnaires: questionnaires,
    Roster: roster,
    // 対話画面
    OpenDialogueProject: openDialogueProject,
    CloseDialogueProject: vi.fn(() => Promise.resolve()),
    ResumeDialogueSession: resumeDialogueSession,
    PendingCandidates: pendingCandidates,
    StartDialogueSession: vi.fn(),
    DialogueCompleteness: dialogueCompleteness,
    WorkflowGuide: workflowGuide,
    ChangeSummary: changeSummary,
    IDRangeWarnings: idRangeWarnings,
    UsageStatusNow: usageStatusNow,
    AskNextQuestion: vi.fn(() => Promise.resolve(false)),
    SendAnswer: vi.fn(),
    InterruptDialogue: vi.fn(),
    SuspendDialogue: vi.fn(),
    ApproveDialogueCandidates: vi.fn(),
    AcknowledgeChangeSummary: vi.fn(() => Promise.resolve()),
}))

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: () => () => undefined,
}))

// ---- 上限規模のデータ（バインディング層側の生成器と同じ形・同じ量）--------------

/** 1 発話の本文の長さ。tools/scalegen の utteranceBodyBytes と揃える。 */
const UTTERANCE_BODY_LENGTH = 1200

function longBody(head: string): string {
    const filler = '現状の在庫管理は拠点ごとの Excel 台帳で行っており、締め時刻の違いが引当のずれを生んでいる。'
    let body = head
    while (body.length < UTTERANCE_BODY_LENGTH) {
        body += filler
    }
    return body
}

function utteranceList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        id: `utt-${String(i + 1).padStart(5, '0')}`,
        speaker: i % 2 === 0 ? 'agent' : 'user',
        at: '2026-09-10T09:00:00Z',
        status: (i + 1) % 97 === 0 ? 'interrupted' : 'completed',
        body: longBody(`S-0100 の ${i + 1} 番目の発話。`),
    }))
}

/** 検索結果の上限（projectstore.DefaultUtteranceSearchLimit と揃える）。 */
const SEARCH_LIMIT = 200

/** 該当発話 n 件（抜粋は前後 40 文字 + 該当語 = 実装の excerptAround と同じ長さ）。 */
function hitList(n: number) {
    const around = '現状の在庫管理は拠点ごとの Excel 台帳で行っており、締め時刻の違いが引当のずれを生ん'
    return Array.from({length: n}, (_, i) => ({
        sessionId: `S-${String((i % LIMITS.sessions) + 1).padStart(4, '0')}`,
        phase: (i + 1) % 3 === 0 ? 'basic-design' : 'requirements',
        type: (i + 1) % 4 === 0 ? 'stakeholder' : 'owner',
        id: `utt-${String(i + 1).padStart(5, '0')}`,
        speaker: i % 2 === 0 ? 'agent' : 'user',
        at: '2026-09-10T09:00:00Z',
        status: (i + 1) % 97 === 0 ? 'interrupted' : 'completed',
        excerpt: `…${around}在庫管理${around}…`,
    }))
}

function sessionList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        id: `S-${String(i + 1).padStart(4, '0')}`,
        type: (i + 1) % 4 === 0 ? 'stakeholder' : 'owner',
        phase: (i + 1) % 3 === 0 ? 'basic-design' : 'requirements',
        startedAt: '2026-09-10T09:00:00Z',
        author: 'k.sato@example.co.jp',
    }))
}

function requirementList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        id: `FR-INV-${String(i + 1).padStart(3, '0')}`,
        title: `在庫引当の要件 ${String(i + 1).padStart(4, '0')}`,
        chapter: 'functional-requirements',
        kind: 'functional',
        priority: 'must',
        status: 'draft',
        body: `拠点 ${i + 1} の在庫引当を本システムで行えること。`,
        acceptanceCriteria: ['引当済み在庫が二重に引当されないこと', '一覧へ 1 秒以内に反映されること'],
        evidence: ['S-0001#utt-00002'],
        missingEvidence: false,
    }))
}

function decisionList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        id: `DEC-${String(i + 1).padStart(3, '0')}`,
        topicKey: `background/topic-${i + 1}`,
        decidedAt: '2026-09-10T09:00:00Z',
        body: `論点 ${i + 1} について現行の運用を維持する。`,
        evidence: ['S-0001#utt-00002'],
    }))
}

function issueList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        id: `ISS-${String(i + 1).padStart(3, '0')}`,
        topic: `拠点 ${i + 1} の棚卸の締め時刻`,
        owner: '情報システム部',
        due: '2026-12-31',
        status: 'open',
        overdue: false,
        questionnaireStatus: 'none',
        evidence: ['S-0001#utt-00002'],
    }))
}

function questionnaireList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        id: `QS-${String(i + 1).padStart(3, '0')}`,
        addressee: '回答者（検証用）',
        issuedAt: '2026-09-10T09:00:00Z',
        status: 'issued',
        statusLabel: '発行済み',
        elapsedDays: 3,
        sourceIssues: ['ISS-001'],
    }))
}

function projectList(n: number) {
    return Array.from({length: n}, (_, i) => ({
        path: `/Users/example/Projects/併走プロジェクト${i + 1}.rwv`,
        projectId: `P-${i + 1}`,
        targetSystemName: `併走プロジェクト ${i + 1}`,
        phase: 'requirements',
        phaseLabel: '要件定義',
        updatedAt: '2026-09-10T09:00:00Z',
        role: 'owner',
        roleLabel: 'オーナー',
        available: true,
        syncConfigured: false,
        hasUnpublished: false,
    }))
}

// ---- 計測 ------------------------------------------------------------------

/**
 * 描画が落ち着くまで待つ。
 *
 * レイアウトを強制的に計算させたうえで 2 フレーム待つ（描画の完了を待たずに時間を止めると、
 * DOM を作った時間しか測っていないことになる）。この待ちぶんは計測値に**上乗せ**されるため、
 * 判定は実際より厳しい側へ倒れる。
 */
async function settled(): Promise<void> {
    document.body.getBoundingClientRect()
    await new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
    })
}

/**
 * step を TRIALS 回計測し、全試行の実測値を出したうえで最大値を持ち分と突き合わせる。
 *
 * prepare は**計測に含めない**下ごしらえ（既に開いている画面での操作を測るときに使う）。
 * 試行ごとに前の描画を捨てる（残したまま測ると次の試行の描画量が積み上がる）。
 */
async function measureSteps(
    label: string,
    budget: number,
    prepare: () => Promise<void>,
    step: () => Promise<void>,
): Promise<void> {
    const samples: number[] = []
    for (let i = 0; i < TRIALS; i += 1) {
        cleanup()
        await prepare()
        const started = performance.now()
        await step()
        await settled()
        samples.push(performance.now() - started)
    }
    const worst = Math.max(...samples)
    // 実測値を必ず残す（バインディング層側の実測と合計を突き合わせるため）。
    console.log(
        `画面応答（描画） ${label}: ${samples.map((d) => `${d.toFixed(0)}ms`).join(' ')}` +
            `（最大 ${worst.toFixed(0)}ms / 持ち分 ${budget}ms）`,
    )
    expect(worst, `${label} が持ち分 ${budget}ms を超えた（上限規模での画面応答の基準）`).toBeLessThanOrEqual(budget)
}

/** 画面を開いてから描き終わるまでを測る（下ごしらえの無い measureSteps）。 */
async function measure(label: string, budget: number, step: () => Promise<void>): Promise<void> {
    await measureSteps(label, budget, async () => undefined, step)
}

/**
 * 一覧（DataList = role="table"）に期待件数の行が出るまで待つ。
 *
 * 行数は見出し行を含むため expected + 1 で待つ（DataList の構造 = ui/DataList.tsx）。
 */
async function waitForTableRows(caption: string, expected: number): Promise<void> {
    const table = await screen.findByRole('table', {name: caption}, {timeout: 10000})
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(expected + 1), {
        interval: 5,
        timeout: 10000,
    })
}

/** 素の一覧（ul）に期待件数の項目が出るまで待つ。 */
async function waitForListItems(expected: number): Promise<void> {
    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(expected), {
        interval: 5,
        timeout: 10000,
    })
}

/** 発話が期待件数ぶん描かれるまで待つ（発話行は article = ui/Utterance.tsx）。 */
async function waitForUtterances(expected: number): Promise<void> {
    await waitFor(() => expect(screen.getAllByRole('article')).toHaveLength(expected), {
        interval: 5,
        timeout: 10000,
    })
}

/**
 * 対話履歴の発話が期待件数ぶん描かれるまで待つ。
 *
 * 対話履歴の発話行は対話画面の発話行（ui/Utterance.tsx）とは別の素の段落なので、
 * 「発話履歴」の入れ物の子の数で数える。
 */
async function waitForHistoryUtterances(expected: number): Promise<void> {
    const history = await screen.findByLabelText('発話履歴', {}, {timeout: 10000})
    await waitFor(() => expect(history.children).toHaveLength(expected), {interval: 5, timeout: 10000})
}

/** 検索結果一覧に期待件数の項目が出るまで待つ。 */
async function waitForSearchHits(expected: number): Promise<void> {
    const found = await screen.findByLabelText('検索結果一覧', {}, {timeout: 10000})
    await waitFor(() => expect(within(found).getAllByRole('listitem')).toHaveLength(expected), {
        interval: 5,
        timeout: 10000,
    })
}

/** el から上へたどって、実際に縦スクロールできる要素を返す（無ければ documentElement）。 */
function scrollableAncestor(el: HTMLElement): HTMLElement {
    for (let node: HTMLElement | null = el; node; node = node.parentElement) {
        if (node.scrollHeight - node.clientHeight > 1) {
            return node
        }
    }
    return document.documentElement
}

beforeEach(() => {
    searchDialogueUtterances.mockReset().mockResolvedValue({
        hits: hitList(SEARCH_LIMIT),
        // 上限規模では 1 語が全発話に当たり得る（バインディング層の計測と同じ最悪ケース）。
        total: LIMITS.sessions * LIMITS.utterances,
        truncated: true,
        limit: SEARCH_LIMIT,
    })
    projects.mockReset().mockResolvedValue(projectList(LIMITS.projects))
    migratableProjects.mockReset().mockResolvedValue([])
    domainPresets.mockReset().mockResolvedValue([])
    syncKindOptions.mockReset().mockResolvedValue([])
    projectFolderNameFor.mockReset().mockResolvedValue('')
    requirements.mockReset().mockResolvedValue(requirementList(LIMITS.requirements))
    decisions.mockReset().mockResolvedValue(decisionList(LIMITS.decisions))
    openIssues.mockReset().mockResolvedValue(issueList(LIMITS.openIssues))
    changeHistory.mockReset().mockResolvedValue([])
    dialogueSessions.mockReset().mockResolvedValue(sessionList(LIMITS.sessions))
    dialogueUtterances.mockReset().mockResolvedValue(utteranceList(LIMITS.utterances))
    questionnaires.mockReset().mockResolvedValue(questionnaireList(LIMITS.questionnaires))
    roster.mockReset().mockResolvedValue([])
    openDialogueProject.mockReset().mockResolvedValue({
        projectPath: '/tmp/scale',
        projectId: 'P-1',
        phase: 'requirements',
        targetName: '在庫管理システム（上限規模の検証用）',
        sessions: sessionList(LIMITS.sessions),
    })
    resumeDialogueSession.mockReset().mockResolvedValue({state: 'awaiting-answer'})
    pendingCandidates.mockReset().mockResolvedValue(null)
    dialogueCompleteness.mockReset().mockResolvedValue({chapters: [], confirmation: {confirmable: false}})
    workflowGuide.mockReset().mockResolvedValue(null)
    changeSummary.mockReset().mockResolvedValue({items: []})
    idRangeWarnings.mockReset().mockResolvedValue([])
    usageStatusNow.mockReset().mockResolvedValue({level: 'none'})
})

describe('上限規模での画面応答（描画側）', () => {
    it('(1) 対話画面が発話 1,000 件を持ったまま持ち分内で描き終わる', async () => {
        await measure('(1) 対話画面（発話 1,000 件）', BUDGET.openDialogue, async () => {
            render(<Dialogue projectPath="/tmp/scale" onBack={() => undefined} />)
            await waitForUtterances(LIMITS.utterances)
        })
    })

    it('(2) 要件項目一覧が 1,000 件を持ち分内で描き終わる', async () => {
        await measure('(2) 要件項目一覧（1,000 件）', BUDGET.screenSwitch, async () => {
            render(<RequirementList onBack={() => undefined} />)
            await waitForTableRows('要件項目', LIMITS.requirements)
        })
    })

    it('(2) 決定事項・未決事項が 500 件ずつを持ち分内で描き終わる', async () => {
        await measure('(2) 決定事項・未決事項（500+500 件）', BUDGET.screenSwitch, async () => {
            render(<DecisionsAndIssues onBack={() => undefined} />)
            // 決定・未決の 2 つの ul（変更履歴は 0 件）。合計で待つ。
            await waitForListItems(LIMITS.decisions + LIMITS.openIssues)
        })
    })

    it('(2) 質問票一覧が 100 件を持ち分内で描き終わる', async () => {
        await measure('(2) 質問票一覧（100 件）', BUDGET.screenSwitch, async () => {
            render(<QuestionnaireManager onBack={() => undefined} />)
            await waitForTableRows('質問票一覧', LIMITS.questionnaires)
        })
    })

    it('(2) プロジェクト一覧が 50 件を持ち分内で描き終わる', async () => {
        await measure('(2) プロジェクト一覧（50 件）', BUDGET.screenSwitch, async () => {
            render(
                <ProjectList
                    onOpen={() => undefined}
                    onOpenSettings={() => undefined}
                    onOpenBackup={() => undefined}
                    onOpenUsage={() => undefined}
                    onOpenTokenUsage={() => undefined}
                />,
            )
            await waitForTableRows('プロジェクト', LIMITS.projects)
        })
    })

    it('(3) 対話履歴がセッション 100 件・発話 1,000 件を持ち分内で描き終わる', async () => {
        await measure('(3) 対話履歴（セッション 100 件・発話 1,000 件）', BUDGET.historyBrowse, async () => {
            render(<DialogueHistory onBack={() => undefined} />)
            await waitForListItems(LIMITS.sessions)
            await waitForHistoryUtterances(LIMITS.utterances)
        })
    })

    it('(3) 発話 1,000 件の履歴を末尾までスクロールしても持ち分内で描き終わる', async () => {
        let scroller: HTMLElement = document.documentElement

        await measureSteps(
            '(3) 履歴のスクロール（先頭→末尾）',
            BUDGET.historyBrowse,
            async () => {
                render(<DialogueHistory onBack={() => undefined} />)
                await waitForHistoryUtterances(LIMITS.utterances)
                const history = await screen.findByLabelText('発話履歴')
                scroller = scrollableAncestor(history)
                // 上限規模で本当にスクロールが要る高さになっていることを確かめる（空振り防止）。
                expect(scroller.scrollHeight).toBeGreaterThan(scroller.clientHeight * 5)
                scroller.scrollTop = 0
                await settled()
            },
            async () => {
                scroller.scrollTop = scroller.scrollHeight
                // 反映は同期。ここから settled() までにレイアウトの再計算が入る。
                expect(scroller.scrollTop).toBeGreaterThan(0)
            },
        )
    })

    it('(3) 対話履歴の絞り込み結果が持ち分内で描き終わる', async () => {
        // 生成データのフェーズは 3 件ごとに基本設計（tools/scalegen と同じ規則）。
        const requirementsOnly = LIMITS.sessions - Math.floor(LIMITS.sessions / 3)

        await measureSteps(
            '(3) 対話履歴の絞り込み（フェーズ）',
            BUDGET.historyBrowse,
            async () => {
                render(<DialogueHistory onBack={() => undefined} />)
                await waitForListItems(LIMITS.sessions)
                await waitForHistoryUtterances(LIMITS.utterances)
            },
            async () => {
                fireEvent.change(screen.getByLabelText('フェーズで絞り込む'), {
                    target: {value: 'requirements'},
                })
                await waitForListItems(requirementsOnly)
            },
        )
    })

    it('(3) 対話履歴の検索結果（上限 200 件）が持ち分内で描き終わる', async () => {
        await measureSteps(
            '(3) 対話履歴の全文検索（結果 200 件の描画）',
            BUDGET.historyBrowse,
            async () => {
                render(<DialogueHistory onBack={() => undefined} />)
                await waitForListItems(LIMITS.sessions)
                await waitForHistoryUtterances(LIMITS.utterances)
            },
            async () => {
                fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '在庫管理'}})
                fireEvent.click(screen.getByRole('button', {name: '検索'}))
                await waitForSearchHits(SEARCH_LIMIT)
                // 空振り防止: 総数と「切った」ことが実際に描かれていること。
                const found = await screen.findByLabelText('検索結果')
                expect(within(found).getByText(/検索結果 100000 件/)).toBeInTheDocument()
                expect(within(found).getByText(/先頭 200 件を表示しています/)).toBeInTheDocument()
            },
        )
    })
})
