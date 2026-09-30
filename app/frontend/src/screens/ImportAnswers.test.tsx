import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {ImportAnswers, resolvedIssuesFor} from './ImportAnswers'

const chooseReturnFile = vi.hoisted(() => vi.fn())
const validateReturnFile = vi.hoisted(() => vi.fn())
const importReturnFile = vi.hoisted(() => vi.fn())
const analyzeImportedAnswers = vi.hoisted(() => vi.fn())
const approveImportDiff = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    ChooseReturnFile: chooseReturnFile,
    ValidateReturnFile: validateReturnFile,
    ImportReturnFile: importReturnFile,
    AnalyzeImportedAnswers: analyzeImportedAnswers,
    ApproveImportDiff: approveImportDiff,
}))

const REVIEW = {
    questionnaireId: 'QS-001',
    addressee: '佐藤（営業部）',
    respondent: '佐藤',
    answeredAt: '2026-08-28T06:00:00Z',
    matches: [
        {
            questionId: 'q-01',
            questionText: '在庫の引き当ては、注文を受けた時点で行いますか。',
            sourceIssue: 'ISS-001',
            kind: 'answered',
            selected: ['受注した時点で行う'],
            answered: true,
        },
        {
            questionId: 'q-02',
            questionText: '月末の締め作業で手作業になっている工程を教えてください。',
            sourceIssue: 'ISS-002',
            kind: 'unknown',
            body: '理由: 全体像を把握していません\n確認先: 経理部 田中',
            answered: true,
        },
    ],
    warnings: [],
    sessionCount: 0,
}

const IMPORTED = {questionnaireId: 'QS-001', status: 'answered', statusLabel: '回答済み'}

const ANALYSIS = {
    questionnaireId: 'QS-001',
    extraction: {
        decisions: [
            {
                topic_key: 'functional/scope',
                body: '在庫引当は受注確定時に即時で行う。',
                rationale: '回答のとおり',
                evidence_refs: ['QS-001#q-01'],
            },
        ],
        open_issues: [],
        requirement_updates: [
            {
                operation: 'update',
                target_id: 'FR-INV-001',
                chapter: 'functional/scope',
                title: '在庫引当のタイミング',
                body_after: '受注確定時に即時で在庫を引き当てる。',
                evidence_refs: ['QS-001#q-01'],
            },
        ],
        term_candidates: [],
        contradictions: [],
    },
    requirementDiffs: [
        {
            operation: 'update',
            targetId: 'FR-INV-001',
            title: '在庫引当のタイミング',
            bodyBefore: '受注確定時に日次で在庫を引き当てる。',
            bodyAfter: '受注確定時に即時で在庫を引き当てる。',
        },
    ],
    contradictions: [
        {
            decisionId: 'DEC-001',
            decisionBody: '在庫引当は日次バッチで行う。',
            description: '既存決定は日次バッチだが、回答は即時引当を求めている',
            evidenceRefs: ['QS-001#q-01'],
        },
    ],
    unknownAnswers: [
        {
            questionId: 'q-02',
            answerRef: 'QS-001#q-02',
            sourceIssueId: 'ISS-002',
            answeredAt: '2026-08-28T06:00:00Z',
            reason: '理由: 全体像を把握していません\n確認先: 経理部 田中',
            ownerCandidate: '経理部 田中',
            currentOwner: '情報システム部',
        },
    ],
    sourceIssueIds: ['ISS-001', 'ISS-002'],
    fallback: false,
}

const APPLIED = {
    decisionIds: ['DEC-002'],
    resolvedIssueIds: ['ISS-001'],
    unblockedRequirementIds: ['FR-INV-001'],
    questionnaireId: 'QS-001',
    questionnaireStatus: 'imported',
    state: '',
}

/** ファイル選択 → 検証 → 取込 → 分析まで進める。 */
async function importAndAnalyze() {
    fireEvent.click(await screen.findByRole('button', {name: '返送ファイルを選ぶ'}))
    fireEvent.click(await screen.findByRole('button', {name: 'この回答を取り込む'}))
    await screen.findByText('反映する内容')
}

describe('回答取込', () => {
    beforeEach(() => {
        vi.clearAllMocks()
        chooseReturnFile.mockResolvedValue('/tmp/QS-001-return.rwva')
        validateReturnFile.mockResolvedValue(REVIEW)
        importReturnFile.mockResolvedValue(IMPORTED)
        analyzeImportedAnswers.mockResolvedValue(ANALYSIS)
        approveImportDiff.mockResolvedValue({applied: APPLIED})
    })

    // 検証不合格は原因と次の行動を 1 文で示し、取込を実行しない。
    it('検証で止まったら理由を示し、取込を実行しない', async () => {
        validateReturnFile.mockRejectedValue(
            new Error('別のプロジェクト宛のファイルです。対象のプロジェクトを開いてから取り込んでください'),
        )
        render(<ImportAnswers onBack={() => undefined} />)

        fireEvent.click(await screen.findByRole('button', {name: '返送ファイルを選ぶ'}))

        expect(await screen.findByText(/別のプロジェクト宛のファイルです/)).toBeTruthy()
        expect(screen.queryByRole('button', {name: 'この回答を取り込む'})).toBeNull()
        expect(importReturnFile).not.toHaveBeenCalled()
    })

    // 確認を要する警告は確認操作を経ないと取り込めない。
    it('確認が必要な警告は確認するまで取り込めない', async () => {
        validateReturnFile.mockResolvedValue({
            ...REVIEW,
            warnings: [
                {
                    kind: 'content-modified',
                    message: '返送ファイルの質問部が発行時と違います。内容を確認してから取り込んでください。',
                    before: '日次で引き当てる',
                    after: '即時で引き当てる',
                },
            ],
        })
        render(<ImportAnswers onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: '返送ファイルを選ぶ'}))

        const button = await screen.findByRole('button', {name: 'この回答を取り込む'})
        expectDisabledReason(button, /確認/)
        expect(screen.getByText('質問部が発行時と違います')).toBeTruthy()

        fireEvent.click(screen.getByLabelText('内容を確認しました'))
        await waitFor(() =>
            expect(screen.getByRole('button', {name: 'この回答を取り込む'}).hasAttribute('disabled')).toBe(false),
        )
        fireEvent.click(screen.getByRole('button', {name: 'この回答を取り込む'}))
        await waitFor(() => expect(importReturnFile).toHaveBeenCalledTimes(1))
        expect(importReturnFile.mock.calls[0][0].AcceptModifiedContent).toBe(true)
    })

    // 質問・回答・発行元の対応と反映差分が 1 画面で完結し、承認で遷移しない。
    it('対応表示と差分承認が同じ画面で完結する', async () => {
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        // 質問・回答・発行元未決事項の対応。
        const matches = screen.getByLabelText('質問と回答の対応')
        expect(within(matches).getByText(/q-01 ／ 発行元 ISS-001/)).toBeTruthy()
        expect(within(matches).getByText('在庫の引き当ては、注文を受けた時点で行いますか。')).toBeTruthy()
        expect(within(matches).getByText(/受注した時点で行う/)).toBeTruthy()

        // 反映差分（変更前後の対比）。
        expect(screen.getByText('受注確定時に日次で在庫を引き当てる。')).toBeTruthy()
        expect((screen.getByLabelText('変更後の本文 1') as HTMLTextAreaElement).value).toBe(
            '受注確定時に即時で在庫を引き当てる。',
        )

        // 承認は同じ画面で行われ、対応表示が残ったままになる（画面遷移しない）。
        fireEvent.click(within(screen.getByLabelText('反映差分')).getAllByRole('button', {name: '承認'})[0])
        fireEvent.click(screen.getByRole('button', {name: '承認した内容を反映する'}))

        expect(await screen.findByText('反映しました')).toBeTruthy()
        expect(screen.getByLabelText('質問と回答の対応')).toBeTruthy()
        expect(screen.getByText(/記録した決定事項: DEC-002/)).toBeTruthy()
    })

    // 根拠を特定できない決着案は理由を文で示し、承認を選べなくする（対話画面の抽出候補パネルと同じ規則）。
    it('根拠を特定できない決着案は理由を示し、承認を選べなくする', async () => {
        analyzeImportedAnswers.mockResolvedValue({
            ...ANALYSIS,
            extraction: {
                ...ANALYSIS.extraction,
                decisions: [{...ANALYSIS.extraction.decisions[0], evidence_refs: [], missing_evidence: true}],
            },
        })
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        const diff = screen.getByLabelText('反映差分')
        const item = within(diff).getByLabelText('決着案 1').closest('article') as HTMLElement
        expect(within(item).getByRole('note').textContent).toContain(
            '根拠にした回答を特定できませんでした。このままでは記録できないため、この候補は破棄してください。',
        )
        expect(within(item).queryByText(/根拠: なし/)).toBeNull()
        expectDisabledReason(within(item).getByRole('button', {name: '承認'}), /回答を特定できない/)
    })

    // 反映の失敗は、押した反映ボタンのすぐ上に「Error: 」を付けずに出す。
    it('反映に失敗した理由を反映ボタンのすぐ上に出す', async () => {
        approveImportDiff.mockRejectedValue(
            new Error('要件項目の反映案「在庫引当のタイミング」の本文が空です。本文を書いてから反映してください。'),
        )
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        const diff = screen.getByLabelText('反映差分')
        fireEvent.click(within(diff).getAllByRole('button', {name: '承認'})[0])
        fireEvent.click(screen.getByRole('button', {name: '承認した内容を反映する'}))

        const alert = await within(diff).findByText(/反映できませんでした。何も記録していません。/)
        expect(alert.getAttribute('role')).toBe('alert')
        expect(alert.textContent).toContain('本文が空です')
        expect(alert.textContent).not.toContain('Error')
        expect(alert.nextElementSibling?.className).toContain('rw-import__actions')
    })

    // 破棄した差分は反映されない（承認したものだけを送る）。
    it('破棄した差分は反映に含めない', async () => {
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        const diff = screen.getByLabelText('反映差分')
        // 決着案は承認、要件項目の反映案は破棄。
        fireEvent.click(within(diff).getAllByRole('button', {name: '承認'})[0])
        fireEvent.click(within(diff).getAllByRole('button', {name: '破棄'})[1])
        fireEvent.click(screen.getByRole('button', {name: '承認した内容を反映する'}))

        await waitFor(() => expect(approveImportDiff).toHaveBeenCalledTimes(1))
        const [, request] = approveImportDiff.mock.calls[0]
        expect(request.decisions).toHaveLength(1)
        expect(request.requirementUpdates).toHaveLength(0)
        // 決着させる未決事項は根拠の回答から辿る。
        expect(request.decisions[0].resolvesIssueIds).toEqual(['ISS-001'])
        // 「不明」の経過は未決事項へ追記される。
        expect(request.unknownNotes).toEqual([
            expect.objectContaining({issueId: 'ISS-002', answerRef: 'QS-001#q-02'}),
        ])
        // 確認先は承認していないので owner を変えない。
        expect(request.ownerUpdates).toEqual([])
    })

    // 矛盾は該当 DEC の ID・本文とともに示す。
    it('矛盾を DEC の ID と本文つきで指摘する', async () => {
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        expect(screen.getByText(/DEC-001/)).toBeTruthy()
        expect(screen.getByText('既存決定は日次バッチだが、回答は即時引当を求めている')).toBeTruthy()
        expect(screen.getByText('在庫引当は日次バッチで行う。')).toBeTruthy()
    })

    // AI 障害でも突き合わせ表示と手動編集で取込を完了できる。
    it('AI 障害時も突き合わせ表示と手動編集で取込を完了できる', async () => {
        analyzeImportedAnswers.mockResolvedValue({
            questionnaireId: 'QS-001',
            extraction: {
                decisions: [],
                open_issues: [],
                requirement_updates: [],
                term_candidates: [],
                contradictions: [],
            },
            unknownAnswers: ANALYSIS.unknownAnswers,
            sourceIssueIds: ['ISS-001', 'ISS-002'],
            fallback: true,
            notice: 'AI が一時的に応答しませんでした。回答と未決事項の突き合わせは表示できます。',
        })
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        // 突き合わせ表示は残る。
        expect(screen.getByLabelText('質問と回答の対応')).toBeTruthy()
        expect(screen.getByText(/AI が一時的に応答しませんでした/)).toBeTruthy()

        // 候補が無くても承認操作で取込を完了できる（「不明」の経過追記と状態遷移）。
        fireEvent.click(screen.getByRole('button', {name: '承認した内容を反映する'}))
        await waitFor(() => expect(approveImportDiff).toHaveBeenCalledTimes(1))
        expect(await screen.findByText('反映しました')).toBeTruthy()
    })

    // 「不明」の確認先は承認したときだけ owner の変更として送る。
    it('確認先を承認したときだけ決める人を変える', async () => {
        render(<ImportAnswers onBack={() => undefined} />)
        await importAndAnalyze()

        fireEvent.click(screen.getByLabelText(/決める人を「経理部 田中」に変える/))
        fireEvent.click(screen.getByRole('button', {name: '承認した内容を反映する'}))

        await waitFor(() => expect(approveImportDiff).toHaveBeenCalledTimes(1))
        const [, request] = approveImportDiff.mock.calls[0]
        expect(request.ownerUpdates).toEqual([{issueId: 'ISS-002', owner: '経理部 田中'}])
    })
})

describe('根拠から発行元未決事項を引く', () => {
    it('回答参照から質問を辿って発行元を求める', () => {
        expect(resolvedIssuesFor(['QS-001#q-01', 'QS-001#q-02'], REVIEW.matches)).toEqual(['ISS-001', 'ISS-002'])
    })

    it('質問票に無い参照は無視する', () => {
        expect(resolvedIssuesFor(['QS-001#q-09', 'S-0001#utt-00001'], REVIEW.matches)).toEqual([])
    })

    it('同じ発行元を重複させない', () => {
        expect(resolvedIssuesFor(['QS-001#q-01', 'QS-001#q-01'], REVIEW.matches)).toEqual(['ISS-001'])
    })

    it('突合結果が無いときは空を返す', () => {
        expect(resolvedIssuesFor(['QS-001#q-01'], undefined)).toEqual([])
    })
})

describe('OS のファイル関連付けから渡された返送ファイル', () => {
    beforeEach(() => {
        chooseReturnFile.mockReset()
        validateReturnFile.mockReset()
    })

    it('選択ダイアログを経ずに、渡されたファイルをそのまま検証する', async () => {
        validateReturnFile.mockResolvedValue(REVIEW)
        render(<ImportAnswers onBack={() => undefined} initialFile="/tmp/QS-001-return.rwva" />)

        await waitFor(() => expect(validateReturnFile).toHaveBeenCalledWith('/tmp/QS-001-return.rwva'))
        expect(chooseReturnFile).not.toHaveBeenCalled()
        expect(await screen.findByText('QS-001')).toBeTruthy()
    })

    it('渡されていないときは検証を始めない（従来どおり選択から）', async () => {
        render(<ImportAnswers onBack={() => undefined} />)
        await waitFor(() => expect(screen.getByRole('button', {name: '返送ファイルを選ぶ'})).toBeTruthy())
        expect(validateReturnFile).not.toHaveBeenCalled()
    })
})
