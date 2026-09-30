import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {DecisionsAndIssues, DialogueHistory, RequirementList} from './Records'

const decisions = vi.hoisted(() => vi.fn())
const openIssues = vi.hoisted(() => vi.fn())
const changeHistory = vi.hoisted(() => vi.fn())
const evidence = vi.hoisted(() => vi.fn())
const requirements = vi.hoisted(() => vi.fn())
const editRequirement = vi.hoisted(() => vi.fn())
const agreeRequirement = vi.hoisted(() => vi.fn())
const revertRequirement = vi.hoisted(() => vi.fn())
const dialogueSessions = vi.hoisted(() => vi.fn())
const dialogueUtterances = vi.hoisted(() => vi.fn())
const impact = vi.hoisted(() => vi.fn())
const searchDialogueUtterances = vi.hoisted(() => vi.fn())
const checkPhaseDocumentReservation = vi.hoisted(() => vi.fn())
const workModeOptions = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    CheckPhaseDocumentReservation: checkPhaseDocumentReservation,
    WorkModeOptions: workModeOptions,
    Decisions: decisions,
    OpenIssues: openIssues,
    ChangeHistory: changeHistory,
    Evidence: evidence,
    Requirements: requirements,
    EditRequirement: editRequirement,
    AgreeRequirement: agreeRequirement,
    RevertRequirement: revertRequirement,
    DialogueSessions: dialogueSessions,
    DialogueUtterances: dialogueUtterances,
    SearchDialogueUtterances: searchDialogueUtterances,
    Impact: impact,
}))

const DECISION = {
    id: 'DEC-001',
    topicKey: 'background/current-state',
    decidedAt: '2026-08-27T10:00:00Z',
    body: 'Excel 台帳で管理している。',
    evidence: ['S-0001#utt-00002'],
}

const ISSUE = {
    id: 'ISS-001',
    topic: '棚卸の頻度',
    owner: '営業部 佐藤',
    due: '2020-01-01',
    status: 'open',
    overdue: true,
    blocking: ['FR-INV-001'],
    questionnaireStatus: '未発行',
    evidence: ['S-0001#utt-00002'],
}

const REQUIREMENT = {
    id: 'FR-INV-001',
    title: '在庫引当',
    chapter: 'functional-requirements',
    kind: 'functional',
    priority: 'must',
    status: 'draft',
    body: '受注確定時に在庫を引き当てること。',
    acceptanceCriteria: ['3 秒以内'],
    decisions: ['DEC-001'],
    blockedBy: ['ISS-001'],
    missingEvidence: false,
}

describe('決定事項・未決事項一覧', () => {
    beforeEach(() => {
        decisions.mockReset()
        decisions.mockResolvedValue([DECISION])
        openIssues.mockReset()
        openIssues.mockResolvedValue([ISSUE])
        impact.mockReset()
        impact.mockResolvedValue({
            target: 'DEC-001',
            chapters: [{kind: 'requirements', fileName: '01-business-context.md', title: '業務背景'}],
            designChapters: [],
            requirements: ['FR-INV-001'],
            openIssues: ['ISS-001'],
        })
        changeHistory.mockReset()
        changeHistory.mockResolvedValue([
            {at: '2026-08-27T10:00:00Z', author: 'k.sato@example.co.jp', target: 'DEC-001', change: 'created'},
        ])
        evidence.mockReset()
    })

    // 本文・決定日・根拠を出し、本文を直接書き換える操作を置かない。
    it('決定事項を本文・決定日・根拠つきで表示し、編集操作を持たない', async () => {
        render(<DecisionsAndIssues onBack={() => undefined} />)
        await screen.findByText('Excel 台帳で管理している。')

        expect(screen.getAllByText(/DEC-001/).length).toBeGreaterThan(0)
        expect(screen.getAllByText(/2026-08-27T10:00:00Z/).length).toBeGreaterThan(0)
        // 本文を書き換える入力欄が無い（根拠ボタンは遡及表示のためのもの）。
        expect(screen.queryByRole('textbox')).toBeNull()
    })

    // 未決事項は決める人・期限・状態・ブロック対象・質問票状態を出し、期限超過を識別する。
    it('未決事項を決める人・期限・ブロック対象つきで表示し、期限超過を識別する', async () => {
        render(<DecisionsAndIssues onBack={() => undefined} />)
        await screen.findByText('棚卸の頻度')

        expect(screen.getByText(/決める人: 営業部 佐藤/)).toBeInTheDocument()
        expect(screen.getByText(/質問票: 未発行/)).toBeInTheDocument()
        expect(screen.getByText('期限超過')).toBeInTheDocument()
        expect(screen.getByText(/ブロック対象: FR-INV-001/)).toBeInTheDocument()
    })

    // 根拠から発話の前後文脈へ遡及できる。
    it('根拠から発話の前後文脈を開ける', async () => {
        evidence.mockResolvedValue({
            ref: 'S-0001#utt-00002',
            found: true,
            context: [
                {id: 'utt-00001', speaker: 'agent', at: '', status: 'completed', body: '現状は？'},
                {id: 'utt-00002', speaker: 'user', at: '', status: 'completed', body: 'Excel 台帳です。'},
            ],
        })
        render(<DecisionsAndIssues onBack={() => undefined} />)
        const refButtons = await screen.findAllByRole('button', {name: 'S-0001#utt-00002'})
        fireEvent.click(refButtons[0])

        const panel = await screen.findByLabelText('根拠')
        expect(within(panel).getByText(/現状は？/)).toBeInTheDocument()
        expect(within(panel).getByText(/Excel 台帳です。/)).toBeInTheDocument()
    })

    // 参照先が無い根拠は参照欠落として示す。
    it('参照先が無い根拠は参照欠落として示す', async () => {
        evidence.mockResolvedValue({ref: 'S-0001#utt-09999', found: false})
        render(<DecisionsAndIssues onBack={() => undefined} />)
        const refButtons = await screen.findAllByRole('button', {name: 'S-0001#utt-00002'})
        fireEvent.click(refButtons[0])

        expect(await screen.findByText(/参照欠落/)).toBeInTheDocument()
    })

    // 取り込み資料の根拠から原本の所在と該当箇所へ遡及できる。
    it('取り込み資料の根拠は原本の場所と該当箇所を表示する', async () => {
        evidence.mockResolvedValue({
            ref: 'IMP-001#L10-L12',
            found: true,
            import: {
                ref: 'IMP-001#L10-L12',
                importId: 'IMP-001',
                sourceName: '現行業務.docx',
                kind: 'material',
                format: 'docx',
                importedAt: '2026-08-27T10:00:00Z',
                sourcePath: 'imports/IMP-001/original.docx',
                startLine: 10,
                endLine: 12,
                excerpt: ['棚卸は月末に行う。', '差異は翌月へ繰り越す。'],
                contextStartLine: 9,
                context: [],
            },
        })
        render(<DecisionsAndIssues onBack={() => undefined} />)
        const refButtons = await screen.findAllByRole('button', {name: 'S-0001#utt-00002'})
        fireEvent.click(refButtons[0])

        const panel = await screen.findByLabelText('根拠')
        expect(within(panel).getByText(/現行業務.docx/)).toBeInTheDocument()
        expect(within(panel).getByText(/imports\/IMP-001\/original.docx/)).toBeInTheDocument()
        expect(within(panel).getByText(/棚卸は月末に行う。/)).toBeInTheDocument()
        // 参照欠落と取り違えない。
        expect(within(panel).queryByText(/参照欠落/)).not.toBeInTheDocument()
    })

    // 回答の根拠は質問と答えの対で表示する。
    it('回答の根拠は質問・回答・回答者・回答日時を表示する', async () => {
        evidence.mockResolvedValue({
            ref: 'QS-001#q-01',
            found: true,
            answer: {
                questionnaireId: 'QS-001',
                questionId: 'q-01',
                questionText: '在庫の引き当ては、注文を受けた時点で行いますか。',
                background: '取り消しの扱いが変わります。',
                sourceIssue: 'ISS-001',
                respondent: '佐藤',
                answeredAt: '2026-08-28T07:00:00Z',
                kind: 'answered',
                selected: ['受注した時点で行う'],
            },
        })
        render(<DecisionsAndIssues onBack={() => undefined} />)
        const refButtons = await screen.findAllByRole('button', {name: 'S-0001#utt-00002'})
        fireEvent.click(refButtons[0])

        const panel = await screen.findByLabelText('根拠')
        expect(within(panel).getByText(/在庫の引き当ては、注文を受けた時点で行いますか。/)).toBeInTheDocument()
        expect(within(panel).getByText(/受注した時点で行う/)).toBeInTheDocument()
        expect(within(panel).getByText(/佐藤/)).toBeInTheDocument()
        expect(within(panel).getByText(/ISS-001/)).toBeInTheDocument()
        expect(within(panel).queryByText(/参照欠落/)).not.toBeInTheDocument()
    })

    // 「不明」の回答も根拠として理由まで表示する。
    it('「不明」の回答は理由まで表示する', async () => {
        evidence.mockResolvedValue({
            ref: 'QS-001#q-02',
            found: true,
            answer: {
                questionnaireId: 'QS-001',
                questionId: 'q-02',
                questionText: '月末の締め作業で手作業になっている工程を教えてください。',
                respondent: '佐藤',
                answeredAt: '2026-08-28T07:00:00Z',
                kind: 'unknown',
                body: '経理部へ確認が必要です。',
            },
        })
        render(<DecisionsAndIssues onBack={() => undefined} />)
        const refButtons = await screen.findAllByRole('button', {name: 'S-0001#utt-00002'})
        fireEvent.click(refButtons[0])

        const panel = await screen.findByLabelText('根拠')
        expect(within(panel).getByText(/不明/)).toBeInTheDocument()
        expect(within(panel).getByText(/経理部へ確認が必要です。/)).toBeInTheDocument()
    })

    // 見つかったのに描画できない種別は、空にせず想定外として示す（以前に見出しだけ出て中身が空になった）。
    it('描画できない種別は空にせず想定外として示す', async () => {
        evidence.mockResolvedValue({ref: 'XX-001#y-01', found: true})
        render(<DecisionsAndIssues onBack={() => undefined} />)
        const refButtons = await screen.findAllByRole('button', {name: 'S-0001#utt-00002'})
        fireEvent.click(refButtons[0])

        const panel = await screen.findByLabelText('根拠')
        expect(within(panel).getByText(/対応する表示がありません/)).toBeInTheDocument()
    })

    // 決定事項から影響範囲を開ける。
    it('決定事項から影響範囲を開ける', async () => {
        render(<DecisionsAndIssues onBack={() => undefined} />)
        await screen.findByText('Excel 台帳で管理している。')

        fireEvent.click(screen.getByRole('button', {name: '影響範囲'}))
        const panel = await screen.findByLabelText('変更影響')
        await waitFor(() => expect(impact).toHaveBeenCalledWith('DEC-001'))

        expect(within(panel).getByText(/業務背景/)).toBeInTheDocument()
        expect(within(panel).getByText(/FR-INV-001/)).toBeInTheDocument()
        expect(within(panel).getByText(/ISS-001/)).toBeInTheDocument()
    })

    // 変更履歴を表示する。
    it('変更履歴を表示する', async () => {
        render(<DecisionsAndIssues onBack={() => undefined} />)
        expect(await screen.findByText(/k.sato@example.co.jp DEC-001 created/)).toBeInTheDocument()
    })
})

describe('要件項目一覧・詳細', () => {
    beforeEach(() => {
        requirements.mockReset()
        requirements.mockResolvedValue([REQUIREMENT])
        editRequirement.mockReset()
        editRequirement.mockResolvedValue(REQUIREMENT)
        agreeRequirement.mockReset()
        agreeRequirement.mockResolvedValue({...REQUIREMENT, status: 'agreed'})
        revertRequirement.mockReset()
        revertRequirement.mockResolvedValue({...REQUIREMENT, status: 'draft', revertedReason: '前提が変わったため'})
        // 差し戻しは進め方の選択を経る。
        checkPhaseDocumentReservation.mockReset()
        checkPhaseDocumentReservation.mockResolvedValue({reserved: false})
        workModeOptions.mockReset()
        workModeOptions.mockResolvedValue([
            {mode: 'exclusive', label: '排他（1 名で進める）', hint: '自分だけで進めます。'},
            {mode: 'concurrent', label: '並行（同時に進める）', hint: '同時に進めます。'},
        ])
    })

    it('一覧に ID・状態・ブロックする未決事項を出す', async () => {
        render(<RequirementList onBack={() => undefined} />)
        await screen.findByText('在庫引当')

        expect(screen.getByText('FR-INV-001')).toBeInTheDocument()
        expect(screen.getByText('ドラフト')).toBeInTheDocument()
        expect(screen.getByText('未決 1')).toBeInTheDocument()
    })

    // 手動編集ができる（AI 障害中も可）。
    it('要件項目を手動編集できる', async () => {
        render(<RequirementList onBack={() => undefined} />)
        fireEvent.click(await screen.findByText('在庫引当'))

        fireEvent.change(await screen.findByLabelText('要件名'), {target: {value: '在庫引当（改）'}})
        fireEvent.click(screen.getByRole('button', {name: '編集を保存'}))

        await waitFor(() => expect(editRequirement).toHaveBeenCalled())
        expect(editRequirement.mock.calls[0][0].title).toBe('在庫引当（改）')
    })

    // 差し戻しには理由が要る。
    it('差し戻しは理由を入力するまで実行できない', async () => {
        requirements.mockResolvedValue([{...REQUIREMENT, status: 'agreed'}])
        render(<RequirementList onBack={() => undefined} />)
        fireEvent.click(await screen.findByText('在庫引当'))

        const revertButton = await screen.findByRole('button', {name: '差し戻す'})
        fireEvent.click(revertButton)
        expect(revertRequirement).not.toHaveBeenCalled()

        fireEvent.change(screen.getByLabelText('差し戻しの理由'), {target: {value: '前提が変わったため'}})
        fireEvent.click(screen.getByRole('button', {name: '差し戻す'}))
        // 進め方を選ぶまでは実行しない
        fireEvent.click(await screen.findByRole('radio', {name: /排他（1 名で進める）/}))
        expect(revertRequirement).not.toHaveBeenCalled()
        fireEvent.click(screen.getByRole('button', {name: 'この進め方で始める'}))
        await waitFor(() =>
            expect(revertRequirement).toHaveBeenCalledWith('FR-INV-001', '前提が変わったため', {
                mode: 'exclusive',
                confirmed: false,
            }),
        )
    })

    // 根拠へたどれない項目を識別表示する。
    // 差し戻し操作の際に影響一覧が確認表示として出る。
    it('差し戻しのときに影響一覧を確認表示する', async () => {
        requirements.mockResolvedValue([{...REQUIREMENT, status: 'agreed'}])
        impact.mockResolvedValue({
            target: 'FR-INV-001',
            chapters: [{kind: 'requirements', fileName: '06-functional-requirements.md', title: '機能要件'}],
            designChapters: [{kind: 'basic-design', fileName: '01-architecture.md', title: '全体アーキテクチャ'}],
            requirements: [],
            openIssues: ['ISS-001'],
        })
        render(<RequirementList onBack={() => undefined} />)
        fireEvent.click(await screen.findByText('在庫引当'))

        fireEvent.change(await screen.findByLabelText('差し戻しの理由'), {target: {value: '前提が変わったため'}})
        fireEvent.click(screen.getByRole('button', {name: '差し戻す'}))
        fireEvent.click(await screen.findByRole('radio', {name: /排他（1 名で進める）/}))
        fireEvent.click(screen.getByRole('button', {name: 'この進め方で始める'}))

        const panel = await screen.findByLabelText('変更影響')
        expect(within(panel).getByText(/機能要件/)).toBeInTheDocument()
        expect(within(panel).getByText(/全体アーキテクチャ/)).toBeInTheDocument()
        await waitFor(() => expect(revertRequirement).toHaveBeenCalled())
    })

    // 要件項目の詳細から影響範囲を開ける。
    it('要件項目から影響範囲を開ける', async () => {
        render(<RequirementList onBack={() => undefined} />)
        fireEvent.click(await screen.findByText('在庫引当'))

        fireEvent.click(await screen.findByRole('button', {name: '影響範囲を見る'}))
        const panel = await screen.findByLabelText('変更影響')
        await waitFor(() => expect(impact).toHaveBeenCalledWith('FR-INV-001'))
        expect(within(panel).getByText(/FR-INV-001 の影響範囲/)).toBeInTheDocument()
    })

    it('根拠へたどれない要件項目を参照欠落として示す', async () => {
        requirements.mockResolvedValue([{...REQUIREMENT, decisions: [], missingEvidence: true}])
        render(<RequirementList onBack={() => undefined} />)
        fireEvent.click(await screen.findByText('在庫引当'))

        expect(await screen.findByText('参照欠落')).toBeInTheDocument()
    })
})

describe('対話履歴一覧', () => {
    beforeEach(() => {
        dialogueSessions.mockReset()
        dialogueSessions.mockResolvedValue([
            {id: 'S-0001', type: 'owner', phase: 'requirements', startedAt: '2026-08-27T10:00:00Z'},
            {id: 'S-0002', type: 'stakeholder', phase: 'basic-design', startedAt: '2026-08-27T11:00:00Z'},
        ])
        dialogueUtterances.mockReset()
        dialogueUtterances.mockResolvedValue([
            {id: 'utt-00001', speaker: 'agent', at: '2026-08-27T10:00:00Z', status: 'completed', body: '質問です。'},
            {id: 'utt-00002', speaker: 'agent', at: '2026-08-27T10:01:00Z', status: 'interrupted', body: '途中まで'},
        ])
        searchDialogueUtterances.mockReset()
        searchDialogueUtterances.mockResolvedValue({
            hits: [
                {
                    sessionId: 'S-0002',
                    phase: 'basic-design',
                    type: 'stakeholder',
                    id: 'utt-00007',
                    speaker: 'user',
                    at: '2026-08-27T11:05:00Z',
                    status: 'interrupted',
                    excerpt: '…在庫の締め処理は倉庫ごとに違います…',
                },
            ],
            total: 1,
            truncated: false,
            limit: 200,
        })
    })

    // フェーズ・種別で絞り込める。
    it('フェーズと種別で絞り込める', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        const list = await screen.findByLabelText('セッション一覧')
        expect(within(list).getAllByRole('button')).toHaveLength(2)

        fireEvent.change(screen.getByLabelText('種別で絞り込む'), {target: {value: 'owner'}})
        await waitFor(() => expect(within(list).getAllByRole('button')).toHaveLength(1))
        expect(within(list).getByText(/S-0001/)).toBeInTheDocument()
    })

    // 中断発話は「中断」ラベル付きで表示する。
    it('中断発話に中断ラベルを付けて表示する', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        const history = await screen.findByLabelText('発話履歴')

        expect(await within(history).findByText('質問です。')).toBeInTheDocument()
        expect(within(history).getByText('中断')).toBeInTheDocument()
    })

    // 参照専用で書き換え操作を持たない。
    it('発話履歴に書き換え操作を持たない', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        const history = await screen.findByLabelText('発話履歴')
        await within(history).findByText('質問です。')

        expect(within(history).queryByRole('textbox')).toBeNull()
        expect(within(history).queryByRole('button')).toBeNull()
    })
    // 語句で検索すると、件数と該当発話の一覧（抜粋つき）が出る。
    it('発話本文を検索して件数と該当発話の一覧を表示する', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')

        fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '在庫の締め処理'}})
        fireEvent.click(screen.getByRole('button', {name: '検索'}))

        const found = await screen.findByLabelText('検索結果')
        expect(within(found).getByText(/「在庫の締め処理」の検索結果 1 件/)).toBeInTheDocument()
        expect(within(found).getByText(/在庫の締め処理は倉庫ごとに違います/)).toBeInTheDocument()
        // 中断発話であることが結果に出る。
        expect(within(found).getByText('中断')).toBeInTheDocument()
    })

    // 検索は現在の絞り込み（フェーズ・種別）を伴って実行される。
    it('検索は現在の絞り込み条件を伴って実行される', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')

        fireEvent.change(screen.getByLabelText('フェーズで絞り込む'), {target: {value: 'basic-design'}})
        fireEvent.change(screen.getByLabelText('種別で絞り込む'), {target: {value: 'stakeholder'}})
        fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '在庫'}})
        fireEvent.click(screen.getByRole('button', {name: '検索'}))

        await screen.findByLabelText('検索結果')
        expect(searchDialogueUtterances).toHaveBeenCalledWith('在庫', 'basic-design', 'stakeholder')
    })

    // 0 件は空の一覧ではなく、その旨と検索条件を示す。
    it('該当 0 件のときは条件つきでその旨を示す', async () => {
        searchDialogueUtterances.mockResolvedValue({hits: [], total: 0, truncated: false, limit: 200})
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')

        fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '存在しない語句'}})
        fireEvent.click(screen.getByRole('button', {name: '検索'}))

        const found = await screen.findByLabelText('検索結果')
        expect(within(found).getByText('該当する発話はありません。')).toBeInTheDocument()
        expect(within(found).getByText(/「存在しない語句」の検索結果 0 件/)).toBeInTheDocument()
        expect(within(found).getByText(/フェーズ=すべて/)).toBeInTheDocument()
    })

    // 一覧を上限で切ったときは、切ったことを画面で示す（黙って落とさない）。
    it('上限で切ったときはその旨を示す', async () => {
        searchDialogueUtterances.mockResolvedValue({
            hits: [
                {
                    sessionId: 'S-0001',
                    phase: 'requirements',
                    type: 'owner',
                    id: 'utt-00001',
                    speaker: 'agent',
                    at: '2026-08-27T10:00:00Z',
                    status: 'completed',
                    excerpt: '在庫',
                },
            ],
            total: 4213,
            truncated: true,
            limit: 200,
        })
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')

        fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '在庫'}})
        fireEvent.click(screen.getByRole('button', {name: '検索'}))

        const found = await screen.findByLabelText('検索結果')
        expect(within(found).getByText(/検索結果 4213 件/)).toBeInTheDocument()
        expect(within(found).getByText(/先頭 200 件を表示しています/)).toBeInTheDocument()
    })

    // 結果の項目を選ぶと当該セッションの発話履歴が開く。
    it('検索結果を選ぶと当該セッションの発話履歴が開く', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')
        await waitFor(() => expect(dialogueUtterances).toHaveBeenLastCalledWith('S-0001'))

        fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '在庫'}})
        fireEvent.click(screen.getByRole('button', {name: '検索'}))
        const found = await screen.findByLabelText('検索結果一覧')
        fireEvent.click(within(found).getByRole('button', {name: /utt-00007/}))

        await waitFor(() => expect(dialogueUtterances).toHaveBeenLastCalledWith('S-0002'))
    })

    // 語句なしの検索はアプリ内の 1 文で拒否する（ネイティブダイアログを使わない）。
    it('語句なしで検索すると原因と次の行動を画面に示す', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')

        fireEvent.click(screen.getByRole('button', {name: '検索'}))

        expect(await screen.findByRole('alert')).toHaveTextContent(
            '探したい語句がありません。検索したい語句を入力してください。',
        )
        expect(searchDialogueUtterances).not.toHaveBeenCalled()
        expect(screen.queryByLabelText('検索結果')).toBeNull()
    })

    // 検索の経路にも書き換え操作を置かない。
    it('検索結果に書き換え操作を持たない', async () => {
        render(<DialogueHistory onBack={() => undefined} />)
        await screen.findByLabelText('セッション一覧')

        fireEvent.change(screen.getByLabelText('発話本文を検索する'), {target: {value: '在庫'}})
        fireEvent.click(screen.getByRole('button', {name: '検索'}))
        const found = await screen.findByLabelText('検索結果一覧')

        expect(within(found).queryByRole('textbox')).toBeNull()
        // 一覧の操作は「該当発話を開く」だけ（項目数と同数）。
        expect(within(found).getAllByRole('button')).toHaveLength(1)
    })
})
