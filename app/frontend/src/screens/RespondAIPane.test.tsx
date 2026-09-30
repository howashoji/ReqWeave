import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled, expectDisabledReason} from '../test/interact'
import {RespondAIPane} from './RespondAIPane'

/*
 * 回答モードの AI 対話ペイン（回答画面の下部）。
 *
 * 画面が担う受け入れ条件:
 *   - 未有効の状態で**送信範囲の事前表示**を出し、確認するまで有効化・キー登録へ進めないこと
 *     （何が AI へ送られるかを知ったうえで使い始めるため）
 *   - 送信範囲の文言を**画面が持たない**こと（バックエンドの一覧をそのまま出す）
 *   - **モデル・エフォートの選択欄も値も出さないこと**（回答者に選ばせない）
 *   - AI の応答だけに「回答欄へ入れる」を出し、押すのは本人で、入るのは**下書き**であること
 *     （アプリが自動で回答欄へ書き込まない）
 *   - 選択肢だけの質問では移す操作を無効化し、理由を示すこと
 */

const respondAIDialogue = vi.hoisted(() => vi.fn())
const respondAIProviders = vi.hoisted(() => vi.fn())
const respondAIScope = vi.hoisted(() => vi.fn())
const registerRespondAIKey = vi.hoisted(() => vi.fn())
const enableRespondAI = vi.hoisted(() => vi.fn())
const disableRespondAI = vi.hoisted(() => vi.fn())
const sendRespondAIMessage = vi.hoisted(() => vi.fn())
const cancelRespondAIMessage = vi.hoisted(() => vi.fn())
const respondAIAnswerDraft = vi.hoisted(() => vi.fn())
const startRespondAISignIn = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    RespondAIDialogue: respondAIDialogue,
    RespondAIProviders: respondAIProviders,
    RespondAIScope: respondAIScope,
    RegisterRespondAIKey: registerRespondAIKey,
    EnableRespondAI: enableRespondAI,
    DisableRespondAI: disableRespondAI,
    SendRespondAIMessage: sendRespondAIMessage,
    CancelRespondAIMessage: cancelRespondAIMessage,
    RespondAIAnswerDraft: respondAIAnswerDraft,
    StartRespondAISignIn: startRespondAISignIn,
    CodexSignInState: vi.fn(() => Promise.resolve({available: true, state: 'signed_out', waitMinutes: 15})),
    StartCodexSignIn: vi.fn(),
    ReopenCodexSignInPage: vi.fn(),
    CancelCodexSignIn: vi.fn(),
}))

const listeners = vi.hoisted(() => ({current: [] as Array<(ev: unknown) => void>}))
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, cb: (ev: unknown) => void) => {
        listeners.current.push(cb)
        return () => {
            listeners.current = listeners.current.filter((f) => f !== cb)
        }
    },
}))

/** 送信範囲の一覧はバックエンドが持つ（画面に一覧を作らない = 二重管理の禁止）。 */
const SCOPE = {
    sent: ['この質問票に入っている全ての質問', 'あなたがこれまでに入力した回答'],
    notSent: ['あなたの氏名・所属', 'AI を使うためのキーやサインインの情報'],
    notice: 'Codex App Server では、Codex 自身が道具の定義文を送ります。',
    policyUrl: 'https://example.invalid/policy',
    noRecordNotice: 'この画面では、送った内容の記録は残りません。',
}

const PROVIDERS = [
    {id: 'anthropic', displayName: 'Anthropic（Claude）', policyUrl: 'https://example.invalid/a'},
    {
        id: 'codex',
        displayName: 'Codex App Server（OpenAI）',
        policyUrl: 'https://example.invalid/c',
        authMethods: [
            {id: 'secret_key', label: 'シークレットキー方式', description: 'キーを登録します。', default: true},
            {id: 'chatgpt_signin', label: 'ChatGPT のアカウントでのサインイン', description: 'ブラウザで許可します。'},
        ],
    },
]

const DISABLED = {enabled: false, canSignOut: false, running: false, utterances: []}

const ENABLED = {
    enabled: true,
    providerId: 'anthropic',
    providerLabel: 'Anthropic（Claude）',
    authMethod: 'secret_key',
    authMethodLabel: 'シークレットキー方式',
    canSignOut: false,
    running: false,
    utterances: [
        {
            id: 'utt-00001',
            speakerLabel: 'あなた',
            isAgent: false,
            at: '2026-09-16 12:00',
            body: '2 つめの質問が分かりません。',
            canApply: false,
        },
        {
            id: 'utt-00002',
            speakerLabel: 'AI',
            isAgent: true,
            at: '2026-09-16 12:01',
            body: '手作業になっている工程を名前と担当者で挙げると書きやすくなります。',
            canApply: true,
        },
    ],
}

function renderPane(props: Partial<Parameters<typeof RespondAIPane>[0]> = {}) {
    const onApplyDraft = vi.fn()
    render(
        <RespondAIPane
            questionID="q-03"
            canApplyToAnswer
            onApplyDraft={onApplyDraft}
            {...props}
        />,
    )
    return {onApplyDraft}
}

beforeEach(() => {
    vi.clearAllMocks()
    listeners.current = []
    respondAIDialogue.mockResolvedValue(DISABLED)
    respondAIProviders.mockResolvedValue(PROVIDERS)
    respondAIScope.mockResolvedValue(SCOPE)
    registerRespondAIKey.mockResolvedValue({ok: true})
    enableRespondAI.mockResolvedValue(ENABLED)
    disableRespondAI.mockResolvedValue(DISABLED)
})

describe('回答モードの AI 対話ペイン（回答画面の下部）', () => {
    it('未有効では送信範囲を示し、確認するまでキー登録へ進めない', async () => {
        renderPane()

        // 一覧はバックエンドの文言をそのまま出す（画面に一覧を作らない）。
        for (const line of [...SCOPE.sent, ...SCOPE.notSent, SCOPE.noRecordNotice]) {
            expect(await screen.findByText(line)).toBeTruthy()
        }

        fireEvent.change(await screen.findByLabelText('シークレットキー'), {target: {value: 'sk-dummy'}})
        expectDisabledReason(
            screen.getByRole('button', {name: 'キーを登録して相談をはじめる'}),
            /送られる内容を確認/,
        )
        expect(registerRespondAIKey).not.toHaveBeenCalled()

        fireEvent.click(screen.getByLabelText('上の内容を確認しました'))
        await clickEnabled('キーを登録して相談をはじめる')
        await waitFor(() => expect(registerRespondAIKey).toHaveBeenCalledWith('anthropic', 'sk-dummy', true))
        // 確認を経てから有効化まで進む。
        await waitFor(() => expect(enableRespondAI).toHaveBeenCalledWith('anthropic', 'secret_key', true))
    })

    it('モデル・エフォートの選択欄も選択中の値も出さない', async () => {
        renderPane()
        await screen.findByLabelText('使う AI')
        for (const word of ['モデル', 'エフォート', '推論', '残量']) {
            expect(screen.queryByText(new RegExp(word))).toBeNull()
        }
        // 有効な状態でも出さない。
        respondAIDialogue.mockResolvedValue(ENABLED)
        renderPane()
        await screen.findByLabelText('相談したいこと')
        for (const word of ['モデル', 'エフォート', '推論', '残量']) {
            expect(screen.queryByText(new RegExp(word))).toBeNull()
        }
    })

    it('AI の応答だけに回答欄へ移す操作を出し、入るのは下書きである', async () => {
        respondAIDialogue.mockResolvedValue(ENABLED)
        respondAIAnswerDraft.mockResolvedValue({
            questionId: 'q-03',
            freeText: '手作業になっている工程を名前と担当者で挙げると書きやすくなります。',
            notice: '回答欄へ下書きとして入れました。',
        })
        const {onApplyDraft} = renderPane()

        const applies = await screen.findAllByRole('button', {name: 'この内容を回答欄へ入れる'})
        // 本人の発話には出さない（移せるのは AI の応答だけ）。
        expect(applies).toHaveLength(1)

        fireEvent.click(applies[0])
        await waitFor(() => expect(respondAIAnswerDraft).toHaveBeenCalledWith('q-03', 'utt-00002', 'replace'))
        await waitFor(() => expect(onApplyDraft).toHaveBeenCalled())
        // 画面が保存を呼ばない（保存は回答欄側の通常の経路）。
        expect(onApplyDraft.mock.calls[0][0].freeText).toContain('名前と担当者')

        await clickEnabled('回答欄の末尾へ足す')
        await waitFor(() => expect(respondAIAnswerDraft).toHaveBeenCalledWith('q-03', 'utt-00002', 'append'))
    })

    it('選択肢だけの質問では移す操作を無効化し、理由を示す', async () => {
        respondAIDialogue.mockResolvedValue(ENABLED)
        renderPane({canApplyToAnswer: false})

        const apply = await screen.findByRole('button', {name: 'この内容を回答欄へ入れる'})
        expectDisabledReason(apply, /選ぶのはご自身/)
        expect(respondAIAnswerDraft).not.toHaveBeenCalled()
    })

    it('応答の途中経過を受け取り、送信のたびに履歴を取り直す', async () => {
        respondAIDialogue.mockResolvedValue(ENABLED)
        sendRespondAIMessage.mockResolvedValue({
            ...ENABLED,
            utterances: [
                ...ENABLED.utterances,
                {id: 'utt-00003', speakerLabel: 'あなた', isAgent: false, at: '12:02', body: 'ありがとう。', canApply: false},
            ],
        })
        renderPane()

        fireEvent.change(await screen.findByLabelText('相談したいこと'), {target: {value: 'ありがとう。'}})
        await clickEnabled('送信')
        await waitFor(() => expect(sendRespondAIMessage).toHaveBeenCalledWith('ありがとう。'))
        expect(await screen.findByText('ありがとう。')).toBeTruthy()
    })

    it('失敗の文言をそのまま出し、履歴を取り直す（回答モードの様式）', async () => {
        respondAIDialogue.mockResolvedValue(ENABLED)
        sendRespondAIMessage.mockRejectedValue('この操作は現在使えません。システム担当者へ連絡してください。')
        renderPane()

        fireEvent.change(await screen.findByLabelText('相談したいこと'), {target: {value: '教えてください。'}})
        await clickEnabled('送信')
        expect(await screen.findByRole('alert')).toHaveTextContent('システム担当者へ連絡してください')
        // 送った内容は残っているため、画面の状態を取り直す（初回 + 取り直しの 2 回）。
        await waitFor(() => expect(respondAIDialogue.mock.calls.length).toBeGreaterThanOrEqual(2))
    })
})

describe('Codex App Server を回答モードで選ぶ', () => {
    /** Codex を選ぶ（認証方式の選択が現れる）。 */
    async function chooseCodex() {
        const select = await screen.findByLabelText('使う AI')
        fireEvent.change(select, {target: {value: 'codex'}})
        return await screen.findByLabelText('ChatGPT のアカウントでのサインイン')
    }

    it('認証方式を 2 つとも選べ、担当者モードと同じ注意喚起を出す', async () => {
        renderPane()
        await chooseCodex()

        // 2 つとも選べる。
        expect(screen.getByLabelText('シークレットキー方式')).toBeTruthy()
        // 注意喚起: Codex が付け足す内容・方式ごとの説明・ポリシーの参照先。
        expect(screen.getByText(SCOPE.notice)).toBeTruthy()
        expect(screen.getByText(SCOPE.policyUrl)).toBeTruthy()
        expect(screen.getByText('ブラウザで許可します。')).toBeTruthy()
        // プランの残量は出さない（回答モードには表示先が無い）。
        expect(screen.queryByText(/残量/)).toBeNull()
    })

    it('送信範囲を確認するまでサインインへ進めない', async () => {
        renderPane()
        const signIn = await chooseCodex()
        fireEvent.click(signIn)

        expect(await screen.findByText('上の内容を確認すると、サインインへ進めます。')).toBeTruthy()
        expect(screen.queryByRole('button', {name: 'サインイン'})).toBeNull()
        expect(startRespondAISignIn).not.toHaveBeenCalled()

        fireEvent.click(screen.getByLabelText('上の内容を確認しました'))
        expect(await screen.findByRole('button', {name: 'サインイン'})).toBeTruthy()
    })

    // プロバイダを変えたら確認をやり直す（別のプロバイダへ送る同意にはならない）。
    it('プロバイダを変えると送信範囲の確認をやり直す', async () => {
        renderPane()
        fireEvent.click(await screen.findByLabelText('上の内容を確認しました'))
        expect((screen.getByLabelText('上の内容を確認しました') as HTMLInputElement).checked).toBe(true)

        await chooseCodex()
        await waitFor(() =>
            expect((screen.getByLabelText('上の内容を確認しました') as HTMLInputElement).checked).toBe(false),
        )
        expect(respondAIScope).toHaveBeenCalledWith('codex')
    })
})
