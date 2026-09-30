import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {clickEnabled, expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Respond} from './Respond'

const openQuestionnaireFile = vi.hoisted(() => vi.fn())
const saveAnswer = vi.hoisted(() => vi.fn())
const finalizeAnswers = vi.hoisted(() => vi.fn())
const chooseReturnDestination = vi.hoisted(() => vi.fn())
const respondAIDialogue = vi.hoisted(() => vi.fn())
const respondAIProviders = vi.hoisted(() => vi.fn())
const respondAIScope = vi.hoisted(() => vi.fn())
const signOutRespondAI = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    OpenQuestionnaireFile: openQuestionnaireFile,
    SaveAnswer: saveAnswer,
    FinalizeAnswers: finalizeAnswers,
    ChooseReturnDestination: chooseReturnDestination,
    RespondAIDialogue: respondAIDialogue,
    RespondAIProviders: respondAIProviders,
    RespondAIScope: respondAIScope,
    SignOutRespondAI: signOutRespondAI,
    RegisterRespondAIKey: vi.fn(),
    EnableRespondAI: vi.fn(),
    DisableRespondAI: vi.fn(),
    SendRespondAIMessage: vi.fn(),
    CancelRespondAIMessage: vi.fn(),
    RespondAIAnswerDraft: vi.fn(),
    StartRespondAISignIn: vi.fn(),
    CodexSignInState: vi.fn(() => Promise.resolve({available: false, state: 'signed_out'})),
}))

vi.mock('../../wailsjs/runtime/runtime', () => ({EventsOn: () => () => undefined}))

/** AI 対話（任意機能）の既定の状態。未有効・サインアウトの導線なし。 */
const AI_DISABLED = {enabled: false, canSignOut: false, running: false, utterances: []}

const QUESTIONS = [
    {
        id: 'q-01',
        text: '在庫の引き当ては、注文を受けた時点で行いますか。',
        background: '取り消しの扱いが変わります。',
        answerFormat: 'choice',
        choices: ['受注した時点で行う', '1日1回まとめて行う'],
    },
    {
        id: 'q-02',
        text: '関係する部署をすべて選んでください。',
        background: '確認先の範囲を決めます。',
        answerFormat: 'multi_choice',
        choices: ['営業部', '物流部', '経理部'],
    },
    {
        id: 'q-03',
        text: '月末の締め作業で手作業になっている工程を教えてください。',
        background: '対象範囲の判断に使います。',
        answerFormat: 'free',
    },
    {
        id: 'q-04',
        text: '在庫の単位はどれですか。',
        background: '数え方をそろえます。',
        answerFormat: 'choice_with_free',
        choices: ['ケース', '本'],
    },
]

const OPENED = {
    questionnaireId: 'QS-001',
    addressee: '佐藤（営業部）',
    questions: QUESTIONS,
    status: 'answering',
    restored: false,
}

/** パスコードを入れて質問票を開く。 */
async function openWithPasscode(code = 'AbCdEfGh2345') {
    fireEvent.change(await screen.findByLabelText('パスコード'), {target: {value: code}})
    fireEvent.click(screen.getByRole('button', {name: '質問をひらく'}))
}

describe('回答モード', () => {
    beforeEach(() => {
        vi.clearAllMocks()
        openQuestionnaireFile.mockResolvedValue(OPENED)
        saveAnswer.mockResolvedValue({answered: 1, total: 4})
        chooseReturnDestination.mockResolvedValue('/tmp/QS-001-return.rwva')
        respondAIDialogue.mockResolvedValue(AI_DISABLED)
        respondAIProviders.mockResolvedValue([])
        respondAIScope.mockResolvedValue({sent: [], notSent: [], noRecordNotice: ''})
        finalizeAnswers.mockResolvedValue({
            path: '/tmp/QS-001-return.rwva',
            notice: 'このファイルを担当者へ返送してください。',
        })
    })

    // パスコード一致まで質問票の内容が画面に出ない。
    it('パスコードが一致するまで質問を表示しない', async () => {
        openQuestionnaireFile.mockRejectedValue(
            new Error('パスコードが違います。システム担当者へ連絡してください（担当者は新しいパスコードで再発行できます）'),
        )
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)

        // 開く前は質問文が一切出ない。
        expect(screen.queryByText(QUESTIONS[0].text)).toBeNull()

        await openWithPasscode('WrongPasscode99')

        const alert = await screen.findByRole('alert')
        expect(alert.textContent).toContain('システム担当者へ連絡してください')
        expect(screen.queryByText(QUESTIONS[0].text)).toBeNull()
        // 試行したパスコードは画面に残さない（入力欄は password 型で、本文へ出さない）。
        expect(alert.textContent).not.toContain('WrongPasscode99')
        expect(document.body.textContent).not.toContain('WrongPasscode99')
    })

    // 「不明」を選ぶと理由・確認先の欄が出て、未記入でも回答済みになる。
    it('「わからない」を選ぶと理由欄が出て、未記入でも回答済みになる', async () => {
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        expect(screen.queryByLabelText(/わからない理由・確認先/)).toBeNull()
        fireEvent.click(screen.getByLabelText('わからない'))

        expect(await screen.findByLabelText(/わからない理由・確認先/)).toBeTruthy()
        await waitFor(() => expect(saveAnswer).toHaveBeenCalled())
        expect(saveAnswer.mock.calls.at(-1)?.[0]).toMatchObject({questionId: 'q-01', kind: 'unknown', body: ''})

        // 理由が未記入でも回答済みとして数える（残り 3 件になる）。
        for (let i = 0; i < 3; i++) {
            await clickEnabled('次の質問へ')
            await screen.findByText(QUESTIONS[i + 1].text)
        }
        await clickEnabled('回答を確認する')
        const confirm = await screen.findByRole('button', {name: 'この内容で確定して返送ファイルを作る'})
        expectDisabledReason(confirm, /未回答の質問が 3 件/)
    })

    // answer_format の 4 種すべてが対応する入力 UI で出る。
    it('4 種の回答形式それぞれに対応した入力欄を出す', async () => {
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        // choice = ラジオ
        const single = screen.getByLabelText('受注した時点で行う') as HTMLInputElement
        expect(single.type).toBe('radio')
        expect(screen.getByText('当てはまるものを 1 つ選んでください。')).toBeTruthy()
        fireEvent.click(single)
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))

        // multi_choice = チェックボックス
        const multi = (await screen.findByLabelText('営業部')) as HTMLInputElement
        expect(multi.type).toBe('checkbox')
        expect(screen.getByText('当てはまるものをすべて選んでください。')).toBeTruthy()
        fireEvent.click(multi)
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))

        // free = 自由記述
        const free = await screen.findByLabelText('回答')
        expect(free.tagName).toBe('TEXTAREA')
        fireEvent.change(free, {target: {value: '請求書の突合です。'}})
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))

        // choice_with_free = 選択 + 補足
        expect((await screen.findByLabelText('ケース')).getAttribute('type')).toBe('radio')
        expect(screen.getByLabelText('補足（任意）').tagName).toBe('TEXTAREA')
        expect(screen.getByText('当てはまるものを 1 つ選び、必要なら補足をご記入ください。')).toBeTruthy()
    })

    // 全質問に回答するまで確定操作が有効にならない。
    it('未回答が残る間は確定できない', async () => {
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        // 1 問だけ答えて回答一覧へ進む。
        fireEvent.click(screen.getByLabelText('受注した時点で行う'))
        for (let i = 0; i < 3; i++) {
            fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))
            await screen.findByText(QUESTIONS[i + 1].text)
        }
        fireEvent.click(screen.getByRole('button', {name: '回答を確認する'}))

        const confirm = await screen.findByRole('button', {name: 'この内容で確定して返送ファイルを作る'})
        expectDisabledReason(confirm, /未回答の質問が 3 件/)
        expect(finalizeAnswers).not.toHaveBeenCalled()

        // 一覧の「この質問に戻って直す」から未回答を直せる（回答一覧から個別の修正へ戻る）。
        const review = screen.getByLabelText('回答一覧')
        fireEvent.click(within(review).getAllByRole('button', {name: 'この質問に戻って直す'})[1])
        await screen.findByText(QUESTIONS[1].text)

        // 残りを「わからない」で埋めると確定できる（「不明」も回答）。
        fireEvent.click(screen.getByLabelText('わからない'))
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))
        await screen.findByText(QUESTIONS[2].text)
        fireEvent.click(screen.getByLabelText('わからない'))
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))
        await screen.findByText(QUESTIONS[3].text)
        fireEvent.click(screen.getByLabelText('わからない'))
        fireEvent.click(screen.getByRole('button', {name: '回答を確認する'}))

        const enabled = await screen.findByRole('button', {name: 'この内容で確定して返送ファイルを作る'})
        await waitFor(() => expect(enabled.hasAttribute('disabled')).toBe(false))
    })

    // 開き直すと入力済み回答が復元される。
    it('中断して開き直すと入力済み回答が復元される', async () => {
        openQuestionnaireFile.mockResolvedValue({
            ...OPENED,
            restored: true,
            answers: [
                {questionId: 'q-01', kind: 'answered', selected: ['受注した時点で行う']},
                {questionId: 'q-03', kind: 'unknown', body: '理由: 把握していません'},
            ],
        })
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()

        expect(await screen.findByText(/前回の続きから再開しました/)).toBeTruthy()
        expect((screen.getByLabelText('受注した時点で行う') as HTMLInputElement).checked).toBe(true)

        // 3 問目の「わからない」と理由も復元されている。
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))
        await screen.findByText(QUESTIONS[1].text)
        fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))
        await screen.findByText(QUESTIONS[2].text)
        expect((screen.getByLabelText('わからない') as HTMLInputElement).checked).toBe(true)
        expect((screen.getByLabelText(/わからない理由・確認先/) as HTMLTextAreaElement).value).toBe(
            '理由: 把握していません',
        )
    })

    // 確定で返送ファイルを出力し、返送手順を案内する。
    it('確定すると返送ファイルを出力して返送手順を案内する', async () => {
        openQuestionnaireFile.mockResolvedValue({
            ...OPENED,
            answers: QUESTIONS.map((q) => ({questionId: q.id, kind: 'unknown', body: ''})),
        })
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        for (let i = 0; i < 3; i++) {
            fireEvent.click(screen.getByRole('button', {name: '次の質問へ'}))
            await screen.findByText(QUESTIONS[i + 1].text)
        }
        fireEvent.click(screen.getByRole('button', {name: '回答を確認する'}))
        fireEvent.click(await screen.findByRole('button', {name: 'この内容で確定して返送ファイルを作る'}))

        expect(await screen.findByText('このファイルを担当者へ返送してください。')).toBeTruthy()
        expect(screen.getByText('/tmp/QS-001-return.rwva')).toBeTruthy()
        expect(finalizeAnswers).toHaveBeenCalledWith('/tmp/QS-001-return.rwva')
    })

    // 回答モードから担当者モードへの導線を持たない。
    it('担当者モードへの導線を出さない', async () => {
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        for (const label of ['プロジェクト一覧へ', '決定・未決', '要件項目', '設定', '質問票', '回答取込']) {
            expect(screen.queryByRole('button', {name: label})).toBeNull()
        }
    })

    // 保存に失敗したら担当者への連絡を案内する（回答者は自分で直す手段を持たない）。
    it('保存に失敗したら担当者へ連絡する案内を出す', async () => {
        saveAnswer.mockRejectedValue(new Error('作業データを保存できません。'))
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        fireEvent.click(screen.getByLabelText('受注した時点で行う'))

        const alert = await screen.findByRole('alert')
        expect(alert.textContent).toContain('システム担当者へ連絡してください')
    })

    /*
     * AI 対話ペインは**既定は閉**で、回答欄の下の従属操作で開閉する。
     * 開いても質問・回答欄・フッターの主操作は消えない（押し下げない）。
     */
    it('AI 対話ペインは既定で閉じており、回答欄の下の操作で開閉する', async () => {
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        // 既定は閉（ペインの中身が無い）。
        expect(screen.queryByLabelText('AI との相談')).toBeNull()
        expect(respondAIProviders).not.toHaveBeenCalled()

        await clickEnabled('AI と相談する')
        expect(await screen.findByLabelText('AI との相談')).toBeTruthy()
        // 開いても質問・回答・フッターの主操作は残る（押し下げない）。
        expect(screen.getByText(QUESTIONS[0].text)).toBeTruthy()
        expect(screen.getByText(QUESTIONS[0].background)).toBeTruthy()
        expect(screen.getByRole('button', {name: '次の質問へ'})).toBeTruthy()

        await clickEnabled('相談を閉じる')
        await waitFor(() => expect(screen.queryByLabelText('AI との相談')).toBeNull())
    })

    /*
     * 構造化回答 UI だけで回答を完了できる
     * （ペインを一度も開かずに全質問へ回答して確定できる）。
     */
    it('ペインを一度も開かずに回答を確定できる', async () => {
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(QUESTIONS[0].text)

        fireEvent.click(screen.getByLabelText('受注した時点で行う'))
        await clickEnabled('次の質問へ')
        fireEvent.click(await screen.findByLabelText('営業部'))
        await clickEnabled('次の質問へ')
        fireEvent.change(await screen.findByLabelText('回答'), {target: {value: '請求書の突合。'}})
        await clickEnabled('次の質問へ')
        fireEvent.click(await screen.findByLabelText('ケース'))
        await clickEnabled('回答を確認する')
        await clickEnabled('この内容で確定して返送ファイルを作る')

        expect(await screen.findByText('このファイルを担当者へ返送してください。')).toBeTruthy()
        // AI の機能へ一度も触れていない。
        expect(respondAIProviders).not.toHaveBeenCalled()
        expect(signOutRespondAI).not.toHaveBeenCalled()
    })

    /*
     * サインアウトの導線は
     * 「ChatGPT のアカウントでのサインイン」で有効にしたときだけ返送の画面に出す。
     */
    it('サインイン方式で有効にしたときだけ返送画面にサインアウトの導線を出す', async () => {
        openQuestionnaireFile.mockResolvedValue({...OPENED, status: 'answered'})
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await screen.findByText(/回答は確定済みです/)
        await waitFor(() => expect(respondAIDialogue).toHaveBeenCalled())
        expect(screen.queryByRole('button', {name: 'サインアウトする'})).toBeNull()

        // サインイン方式で有効にしている場合だけ出す。
        respondAIDialogue.mockResolvedValue({...AI_DISABLED, enabled: true, canSignOut: true})
        signOutRespondAI.mockResolvedValue({...AI_DISABLED, canSignOut: false})
        render(<Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />)
        await openWithPasscode()
        await clickEnabled('サインアウトする')
        await waitFor(() => expect(signOutRespondAI).toHaveBeenCalled())
        expect(await screen.findByText('サインアウトしました。')).toBeTruthy()
    })
})