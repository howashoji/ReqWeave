import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {QuestionnaireManager} from './Questionnaires'

const listQuestionnaires = vi.hoisted(() => vi.fn())
const openIssues = vi.hoisted(() => vi.fn())
const roster = vi.hoisted(() => vi.fn())
const addStakeholder = vi.hoisted(() => vi.fn())
const generateQuestionDrafts = vi.hoisted(() => vi.fn())
const previewQuestionnaireIssue = vi.hoisted(() => vi.fn())
const issueQuestionnaire = vi.hoisted(() => vi.fn())
const reissueQuestionnaire = vi.hoisted(() => vi.fn())
const chooseIssueDestination = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    Questionnaires: listQuestionnaires,
    OpenIssues: openIssues,
    Roster: roster,
    AddStakeholder: addStakeholder,
    GenerateQuestionDrafts: generateQuestionDrafts,
    PreviewQuestionnaireIssue: previewQuestionnaireIssue,
    IssueQuestionnaire: issueQuestionnaire,
    ReissueQuestionnaire: reissueQuestionnaire,
    ChooseIssueDestination: chooseIssueDestination,
}))

const ISSUE = {
    id: 'ISS-001',
    topic: '在庫引当のタイミング',
    owner: '営業部長',
    status: 'open',
    overdue: false,
    questionnaireStatus: '未発行',
    evidence: ['S-0001#utt-00001'],
}

const STAKEHOLDER = {id: 'STK-001', name: '佐藤', org: '営業部', label: '佐藤（営業部）'}

const DRAFTS = {
    drafts: [
        {
            sourceIssue: 'ISS-001',
            text: '在庫の引き当ては、注文を受けた時点で行いますか。',
            background: '取り消しの扱いが変わります。',
            answerFormat: 'choice',
            choices: ['受注した時点で行う', '1日1回まとめて行う'],
        },
    ],
    fallback: false,
}

const PREVIEW = {
    questionnaireId: 'QS-001',
    addressee: '佐藤（営業部）',
    questionCount: 1,
    sourceIssues: ['ISS-001'],
    questionnaireMarkdown: '---\nid: QS-001\n---\n\n### q-01\n\n#### 質問\n\n在庫の引き当ては、注文を受けた時点で行いますか。\n',
    terms: [{name: '在庫引当', nameEn: 'stock-allocation', definition: '受注に対して在庫を確保すること。'}],
    excluded: ['担当者とAIの対話履歴', '要件項目の全文', 'パスコードそのもの'],
}

const ISSUED = {
    questionnaireId: 'QS-001',
    path: '/tmp/QS-001.rwvq',
    issuedAt: '2026-08-28T02:00:00Z',
    passcode: 'AbCdEfGh2345',
    passcodeNotice:
        'パスコードは質問票ファイルとは別の経路（電話・チャット等）で宛先へお伝えください。' +
        '本システムはパスコードを保存せず、この画面を閉じると再表示できません（忘れた場合は再発行になります）。',
    reissued: false,
}

/** 論点選択 → 宛先 → 質問文の段階まで進める（各テストの共通前段）。 */
async function advanceToQuestions() {
    fireEvent.click(await screen.findByRole('button', {name: '新しい質問票を作る'}))
    fireEvent.click(await screen.findByLabelText('ISS-001 を選ぶ'))
    fireEvent.click(screen.getByRole('button', {name: '次へ（宛先を決める）'}))
    fireEvent.click(await screen.findByLabelText('佐藤（営業部）'))
    fireEvent.click(screen.getByRole('button', {name: '次へ（質問文を作る）'}))
    // 質問文の段階に着くまで待つ（非同期の生成完了前に検証すると空振りする）。
    await screen.findByLabelText('質問文')
}

describe('質問票管理', () => {
    beforeEach(() => {
        vi.clearAllMocks()
        listQuestionnaires.mockResolvedValue([])
        openIssues.mockResolvedValue([ISSUE])
        roster.mockResolvedValue([STAKEHOLDER])
        generateQuestionDrafts.mockResolvedValue(DRAFTS)
        previewQuestionnaireIssue.mockResolvedValue(PREVIEW)
        issueQuestionnaire.mockResolvedValue(ISSUED)
        chooseIssueDestination.mockResolvedValue('/tmp/QS-001.rwvq')
    })

    // 一覧に宛先・発行日時・状態・経過日数が並ぶ。
    it('質問票一覧に宛先・状態・経過日数を表示する', async () => {
        listQuestionnaires.mockResolvedValue([
            {
                id: 'QS-001',
                addressee: '佐藤（営業部）',
                issuedAt: '2026-08-20T02:00:00Z',
                status: 'issued',
                statusLabel: '発行済み',
                elapsedDays: 8,
                sourceIssues: ['ISS-001'],
            },
        ])
        render(<QuestionnaireManager onBack={() => undefined} />)

        const row = await screen.findByRole('row', {name: /QS-001/})
        expect(within(row).getByText('佐藤（営業部）')).toBeTruthy()
        expect(within(row).getByText('発行済み')).toBeTruthy()
        expect(within(row).getByText('8 日')).toBeTruthy()
        // 発行日時は他画面と同じ表記（`YYYY-MM-DD HH:MM` = ui/format の formatLocal）。
        // 絶対値は実行環境のタイムゾーンで変わるため、ここでは表記だけを検証する
        // （値の変換そのものは formatLocal の単体テストが担う）。
        expect(within(row).getByText(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)).toBeTruthy()
    })

    // 名簿未登録の宛先は選べず、登録導線を経てから進める。
    it('名簿が空のときは宛先を選べず、登録してから次へ進める', async () => {
        roster.mockResolvedValue([])
        addStakeholder.mockResolvedValue(STAKEHOLDER)
        render(<QuestionnaireManager onBack={() => undefined} />)

        fireEvent.click(await screen.findByRole('button', {name: '新しい質問票を作る'}))
        fireEvent.click(await screen.findByLabelText('ISS-001 を選ぶ'))
        fireEvent.click(screen.getByRole('button', {name: '次へ（宛先を決める）'}))

        // 宛先が無い間は次へ進めず、理由が示される。
        const next = await screen.findByRole('button', {name: '次へ（質問文を作る）'})
        expectDisabledReason(next, /名簿/)
        expect(screen.getByText(/名簿に宛先がありません/)).toBeTruthy()

        // 登録導線を経ると宛先が決まり、先へ進める。
        fireEvent.change(screen.getByLabelText('氏名'), {target: {value: '佐藤'}})
        fireEvent.change(screen.getByLabelText('所属'), {target: {value: '営業部'}})
        fireEvent.click(screen.getByRole('button', {name: '名簿へ登録して宛先にする'}))

        await waitFor(() =>
            expect(
                screen.getByRole('button', {name: '次へ（質問文を作る）'}).hasAttribute('disabled'),
            ).toBe(false),
        )
        expect(addStakeholder).toHaveBeenCalledWith({name: '佐藤', org: '営業部'})
    })

    // プレビューに質問全文・同梱用語・「含まれないもの」が出て、確認操作なしに出力しない。
    it('プレビューを経ないと出力せず、プレビューに全文・用語・含まれないものを表示する', async () => {
        render(<QuestionnaireManager onBack={() => undefined} />)
        await advanceToQuestions()

        // 質問文の段階では出力操作が無い（確認操作なしに出力できない）。
        expect(screen.queryByRole('button', {name: 'この内容で発行する'})).toBeNull()
        expect(issueQuestionnaire).not.toHaveBeenCalled()

        fireEvent.click(await screen.findByRole('button', {name: '内容を確認する'}))

        expect(await screen.findByText(/在庫の引き当ては、注文を受けた時点で行いますか。/)).toBeTruthy()
        const terms = screen.getByLabelText('同梱する用語')
        expect(within(terms).getByText('在庫引当')).toBeTruthy()
        const excluded = screen.getByLabelText('含まれないもの')
        expect(within(excluded).getByText('担当者とAIの対話履歴')).toBeTruthy()
        expect(within(excluded).getByText('パスコードそのもの')).toBeTruthy()
        // プレビュー表示だけでは出力しない。
        expect(issueQuestionnaire).not.toHaveBeenCalled()
    })

    // パスコードは 1 回だけ提示し、離れると再表示しない。
    it('出力完了でパスコードと案内を提示し、閉じると再表示しない', async () => {
        render(<QuestionnaireManager onBack={() => undefined} />)
        await advanceToQuestions()
        fireEvent.click(await screen.findByRole('button', {name: '内容を確認する'}))
        fireEvent.click(await screen.findByRole('button', {name: 'この内容で発行する'}))

        expect(await screen.findByText('AbCdEfGh2345')).toBeTruthy()
        const notice = screen.getByRole('alert')
        expect(notice.textContent).toContain('別の経路')
        expect(notice.textContent).toContain('再表示できません')
        expect(notice.textContent).toContain('再発行')

        // 閉じて一覧へ戻るとパスコードは残らない。
        fireEvent.click(screen.getByRole('button', {name: 'パスコードを控えたので閉じる'}))
        await waitFor(() => expect(screen.queryByText('AbCdEfGh2345')).toBeNull())

        // 発行フローに入り直しても再表示されない。
        fireEvent.click(screen.getByRole('button', {name: '新しい質問票を作る'}))
        expect(screen.queryByText('AbCdEfGh2345')).toBeNull()
    })

    // AI 障害でも手入力で発行まで到達できる。
    it('AI 障害時はテンプレートを出して手入力で発行まで進める', async () => {
        generateQuestionDrafts.mockRejectedValue(new Error('AI が一時的に応答しませんでした。'))
        render(<QuestionnaireManager onBack={() => undefined} />)
        await advanceToQuestions()

        // 論点が転記され、案内が出る。
        const text = (await screen.findByLabelText('質問文')) as HTMLTextAreaElement
        expect(text.value).toBe('在庫引当のタイミング')
        expect(screen.getByText(/論点を転記しました/)).toBeTruthy()

        // 背景説明が空の間は先へ進めない（理由つき）。
        const confirm = screen.getByRole('button', {name: '内容を確認する'})
        expectDisabledReason(confirm, /背景説明/)

        fireEvent.change(text, {target: {value: '在庫の引き当てはいつ行いますか。'}})
        fireEvent.change(screen.getByLabelText('背景説明'), {target: {value: '取り消しの扱いが変わります。'}})

        await waitFor(() =>
            expect(screen.getByRole('button', {name: '内容を確認する'}).hasAttribute('disabled')).toBe(false),
        )
        fireEvent.click(screen.getByRole('button', {name: '内容を確認する'}))
        fireEvent.click(await screen.findByRole('button', {name: 'この内容で発行する'}))

        expect(await screen.findByText('AbCdEfGh2345')).toBeTruthy()
        // AI を呼べなくても発行までの経路が通っている。
        expect(issueQuestionnaire).toHaveBeenCalledTimes(1)
    })

    // 再発行は一覧から行い、引き継がれない旨の案内が出る。
    it('発行済みの質問票を再発行できる', async () => {
        listQuestionnaires.mockResolvedValue([
            {
                id: 'QS-001',
                addressee: '佐藤（営業部）',
                issuedAt: '2026-08-28T02:00:00Z',
                status: 'issued',
                statusLabel: '発行済み',
                elapsedDays: 0,
                sourceIssues: ['ISS-001'],
            },
        ])
        reissueQuestionnaire.mockResolvedValue({
            ...ISSUED,
            passcode: 'ZyXwVuTs6789',
            passcodeNotice: ISSUED.passcodeNotice + '再発行したため、前のファイルで宛先が入力していた回答途中のデータは引き継がれません。',
            reissued: true,
        })
        render(<QuestionnaireManager onBack={() => undefined} />)

        fireEvent.click(await screen.findByRole('button', {name: '再発行'}))

        expect(await screen.findByText('再発行しました')).toBeTruthy()
        expect(screen.getByText('ZyXwVuTs6789')).toBeTruthy()
        expect(screen.getByRole('alert').textContent).toContain('引き継がれません')
    })

    // 保存先を選ばずにダイアログを閉じたときは何も出力しない。
    it('保存先を選ばなければ発行しない', async () => {
        chooseIssueDestination.mockResolvedValue('')
        render(<QuestionnaireManager onBack={() => undefined} />)
        await advanceToQuestions()
        fireEvent.click(await screen.findByRole('button', {name: '内容を確認する'}))
        fireEvent.click(await screen.findByRole('button', {name: 'この内容で発行する'}))

        await waitFor(() => expect(chooseIssueDestination).toHaveBeenCalled())
        expect(issueQuestionnaire).not.toHaveBeenCalled()
        expect(screen.queryByText('AbCdEfGh2345')).toBeNull()
    })
})
