import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {clickEnabled, expectDisabledReason} from '../test/interact'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {SetupWizard} from './SetupWizard'

// フロントエンドはバックエンドの公開バインディング経由でのみ機能を呼ぶ（依存規則）。
const setupState = vi.hoisted(() => vi.fn())
const registerKey = vi.hoisted(() => vi.fn())
const models = vi.hoisted(() => vi.fn())
const completeSetup = vi.hoisted(() => vi.fn())
const codexSignInState = vi.hoisted(() => vi.fn())
const startCodexSignIn = vi.hoisted(() => vi.fn())
const reopenCodexSignInPage = vi.hoisted(() => vi.fn())
const cancelCodexSignIn = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    SetupState: setupState,
    RegisterKey: registerKey,
    Models: models,
    CompleteSetup: completeSetup,
    CodexSignInState: codexSignInState,
    StartCodexSignIn: startCodexSignIn,
    ReopenCodexSignInPage: reopenCodexSignInPage,
    CancelCodexSignIn: cancelCodexSignIn,
}))

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, _cb: (ev: unknown) => void) => () => undefined,
}))

const STATE = {
    complete: false,
    authorId: '',
    displayName: '',
    suggestedDisplayName: '佐藤 一郎',
    providers: [
        {id: 'anthropic', displayName: 'Anthropic（Claude）', policyUrl: 'https://www.anthropic.com/legal/privacy'},
        {id: 'openai', displayName: 'OpenAI', policyUrl: 'https://openai.com/policies/api-data-usage-policies/'},
        {id: 'google', displayName: 'Google（Gemini）', policyUrl: 'https://ai.google.dev/gemini-api/terms'},
    ],
    efforts: [
        {id: 'low', label: '低', description: '軽量モデル・追問少なめ', default: false},
        {id: 'standard', label: '標準', description: '上位モデル・標準の掘り下げ', default: true},
        {id: 'high', label: '高', description: '上位モデル・最大の掘り下げ', default: false},
    ],
    dataPolicyNotice: '送信内容はプロバイダのポリシーに従って扱われます。組織ポリシーの確認は利用者の責務です。',
}

const MODELS = {
    models: [
        {id: 'claude-opus-5', displayName: 'Claude Opus 5', contextWindow: 1000000, maxOutput: 128000, tier: 'primary', recommended: true},
        {id: 'claude-haiku-4-5-20251001', displayName: 'Claude Haiku 4.5', contextWindow: 200000, maxOutput: 64000, tier: 'light', recommended: false},
    ],
    fromKnownList: false,
    notice: '',
}

const DUMMY_KEY = 'dummy-key-for-test-9999'

function next() {
    return screen.getByRole('button', {name: /次へ|設定を完了する/})
}

async function selectProvider() {
    await screen.findByText(/AI プロバイダを選んでください/)
    fireEvent.click(screen.getByRole('radio', {name: /Anthropic/}))
    fireEvent.click(next())
    await screen.findByLabelText('シークレットキー')
}

describe('初期設定ウィザード', () => {
    beforeEach(() => {
        setupState.mockReset()
        registerKey.mockReset()
        models.mockReset()
        completeSetup.mockReset()
        setupState.mockResolvedValue(STATE)
        models.mockResolvedValue(MODELS)
        registerKey.mockResolvedValue({ok: true})
        completeSetup.mockResolvedValue(undefined)
    })

    // プロバイダの選択と、プロバイダごとの利用ポリシーの注意喚起
    it('プロバイダを選ぶまで進めず、ポリシーの注意喚起と参照先を示す', async () => {
        render(<SetupWizard onComplete={() => undefined} />)
        await screen.findByText(/AI プロバイダを選んでください/)

        expect(screen.getByText(STATE.dataPolicyNotice)).toBeInTheDocument()
        expect(screen.getByText(/anthropic\.com\/legal\/privacy/)).toBeInTheDocument()
        expectDisabledReason(next(), /プロバイダを選んで/)

        fireEvent.click(screen.getByRole('radio', {name: /Anthropic/}))
        expect(next()).toBeEnabled()
    })

    // 10-external-interfaces 5.4: プロバイダ固有の注意喚起（付け足される送信内容など）も示す
    it('プロバイダ固有の注意喚起があれば、そのプロバイダの選択肢に表示する', async () => {
        const notice = 'このプロバイダでは、端末の識別子が送信に付きます。'
        setupState.mockResolvedValue({
            ...STATE,
            providers: [{...STATE.providers[0], notice}, STATE.providers[1]],
        })
        render(<SetupWizard onComplete={() => undefined} />)
        await screen.findByText(/AI プロバイダを選んでください/)

        expect(screen.getByText(notice)).toBeInTheDocument()
        // 注意喚起を持たないプロバイダには出さない（1 件だけ）
        expect(screen.getAllByText(notice)).toHaveLength(1)
    })

    // キーはマスク入力・平文の再表示をしない
    it('キー入力はパスワード型で、疎通確認の成功まで先へ進めない', async () => {
        render(<SetupWizard onComplete={() => undefined} />)
        await selectProvider()

        const input = screen.getByLabelText('シークレットキー')
        expect(input).toHaveAttribute('type', 'password')
        expect(next()).toBeDisabled()

        fireEvent.change(input, {target: {value: DUMMY_KEY}})
        fireEvent.click(screen.getByRole('button', {name: '登録して疎通確認'}))

        await screen.findByText(/疎通確認に成功しました/)
        expect(registerKey).toHaveBeenCalledWith('anthropic', '', DUMMY_KEY)
        expect(next()).toBeEnabled()
        // 成功後は入力欄にキーを残さない（画面へ再表示しない）
        expect((screen.getByLabelText('シークレットキー') as HTMLInputElement).value).toBe('')
        expect(document.body.textContent).not.toContain(DUMMY_KEY)
    })

    // 失敗理由を区別して示し、先へ進めない
    it('疎通確認に失敗したら理由を示し、先へ進めない', async () => {
        registerKey.mockResolvedValue({
            ok: false,
            reason: '認証エラー',
            detail: 'キーが受け付けられませんでした。キーを確認して登録し直してください。',
        })
        render(<SetupWizard onComplete={() => undefined} />)
        await selectProvider()

        fireEvent.change(screen.getByLabelText('シークレットキー'), {target: {value: DUMMY_KEY}})
        fireEvent.click(screen.getByRole('button', {name: '登録して疎通確認'}))

        await screen.findByText(/認証エラー/)
        expect(screen.getByText(/キーを確認して登録し直してください/)).toBeInTheDocument()
        expect(next()).toBeDisabled()
    })

    // 既知一覧への縮退を黙って行わない
    it('モデル一覧が既知一覧へ縮退したらその旨を示す', async () => {
        models.mockResolvedValue({
            models: MODELS.models,
            fromKnownList: true,
            notice: 'モデル一覧を取得できなかったため、アプリに登録済みの一覧を表示しています。',
        })
        render(<SetupWizard onComplete={() => undefined} />)
        await selectProvider()
        fireEvent.change(screen.getByLabelText('シークレットキー'), {target: {value: DUMMY_KEY}})
        fireEvent.click(screen.getByRole('button', {name: '登録して疎通確認'}))
        await screen.findByText(/疎通確認に成功しました/)
        fireEvent.click(next())

        await screen.findByText(/アプリに登録済みの一覧を表示しています/)
        expect(screen.getByRole('radio', {name: /Claude Opus 5（推奨）/})).toBeChecked()
    })

    // 既定は標準。最後まで進めると設定が保存される
    it('エフォートの既定は標準で、最後まで進めると設定を保存する', async () => {
        const onComplete = vi.fn()
        render(<SetupWizard onComplete={onComplete} />)
        await selectProvider()
        fireEvent.change(screen.getByLabelText('シークレットキー'), {target: {value: DUMMY_KEY}})
        fireEvent.click(screen.getByRole('button', {name: '登録して疎通確認'}))
        await screen.findByText(/疎通確認に成功しました/)

        fireEvent.click(next()) // → モデル
        await screen.findByRole('radio', {name: /Claude Opus 5/})
        fireEvent.click(next()) // → エフォート

        const standard = await screen.findByRole('radio', {name: /標準（既定）/})
        expect(standard).toBeChecked()
        expect(screen.getByText('上位モデル・標準の掘り下げ')).toBeInTheDocument()
        fireEvent.click(next()) // → 作業者名

        const authorInput = await screen.findByLabelText('メールアドレス（利用者 ID）')
        expect((screen.getByLabelText('表示名') as HTMLInputElement).value).toBe('佐藤 一郎')
        expect(next()).toBeDisabled() // メールアドレス未入力

        fireEvent.change(authorInput, {target: {value: 'k.sato@example.co.jp'}})
        expect(next()).toBeEnabled()
        fireEvent.click(next())

        await waitFor(() => expect(completeSetup).toHaveBeenCalled())
        expect(completeSetup).toHaveBeenCalledWith({
            providerId: 'anthropic',
            label: '',
            model: 'claude-opus-5',
            effort: 'standard',
            // 認証方式を持たないプロバイダはシークレットキー方式で保存する
            authMethod: 'secret_key',
            authorId: 'k.sato@example.co.jp',
            displayName: '佐藤 一郎',
        })
        await waitFor(() => expect(onComplete).toHaveBeenCalled())
    })

    // メールアドレス（利用者 ID）は登録後に本人が変更できないことを明示する
    it('メールアドレスが本人変更不可であることを示し、何を入れる欄かを補足する', async () => {
        render(<SetupWizard onComplete={() => undefined} />)
        await selectProvider()
        fireEvent.change(screen.getByLabelText('シークレットキー'), {target: {value: DUMMY_KEY}})
        fireEvent.click(screen.getByRole('button', {name: '登録して疎通確認'}))
        await screen.findByText(/疎通確認に成功しました/)
        fireEvent.click(next())
        await screen.findByRole('radio', {name: /Claude Opus 5/})
        fireEvent.click(next())
        await screen.findByRole('radio', {name: /標準（既定）/})
        fireEvent.click(next())

        await screen.findByLabelText('メールアドレス（利用者 ID）')
        expect(screen.getByText(/登録後に変更できません/)).toBeInTheDocument()
        // 何を入れる欄かを補足する（内部用語 UPN を画面に出さない）
        expect(screen.getByText('会社で使っているメールアドレスを入力してください。')).toBeInTheDocument()
    })
})

/*
 * Codex App Server の認証方式の選択とサインイン。
 *
 * 認証方式を 2 つ持つのは Codex App Server だけで、他のプロバイダでは選択そのものを出さない。
 * サインイン方式ではキー入力に代えてサインインの操作を出し、**成功をもって疎通確認の成功とする**。
 */
const CODEX_AUTH_METHODS = [
    {
        id: 'secret_key',
        label: 'シークレットキー方式',
        description: 'OpenAI のシークレットキーを登録して使います。',
        default: true,
    },
    {
        id: 'chatgpt_signin',
        label: 'ChatGPT のアカウントでのサインイン',
        description: 'ブラウザで OpenAI のサインインのページを開いて許可します。',
        default: false,
    },
]

const CODEX_STATE = {
    ...STATE,
    providers: [
        ...STATE.providers,
        {
            id: 'codex',
            displayName: 'Codex App Server（OpenAI）',
            policyUrl: 'https://openai.com/policies/api-data-usage-policies/',
            notice: 'Codex App Server では、本システムが送る内容に加えて、Codex 自身が道具の定義文を送ります。',
            authMethods: CODEX_AUTH_METHODS,
        },
    ],
}

const SIGNED_OUT = {available: true, state: 'signed_out', label: '', waitMinutes: 15}
const SIGNED_IN = {
    available: true,
    state: 'signed_in',
    label: '',
    waitMinutes: 15,
    account: {email: 'k.sato@example.co.jp', planLabel: 'Plus', accountLabel: 'ChatGPT のアカウント'},
}

async function selectCodex() {
    await screen.findByText(/AI プロバイダを選んでください/)
    fireEvent.click(screen.getByRole('radio', {name: /Codex App Server/}))
}

describe('初期設定ウィザード — Codex App Server の認証方式', () => {
    beforeEach(() => {
        setupState.mockReset().mockResolvedValue(CODEX_STATE)
        models.mockReset().mockResolvedValue(MODELS)
        registerKey.mockReset().mockResolvedValue({ok: true})
        completeSetup.mockReset().mockResolvedValue(undefined)
        codexSignInState.mockReset().mockResolvedValue(SIGNED_OUT)
        startCodexSignIn.mockReset().mockResolvedValue({...SIGNED_OUT, state: 'waiting'})
        cancelCodexSignIn.mockReset().mockResolvedValue(undefined)
        reopenCodexSignInPage.mockReset().mockResolvedValue(undefined)
    })

    // 認証方式の選択は Codex App Server を選んだときだけ出す
    it('Codex App Server を選んだときだけ認証方式の選択を出す', async () => {
        render(<SetupWizard onComplete={() => undefined} />)
        await screen.findByText(/AI プロバイダを選んでください/)

        fireEvent.click(screen.getByRole('radio', {name: /Anthropic/}))
        expect(screen.queryByRole('radio', {name: /ChatGPT のアカウントでのサインイン/})).toBeNull()

        await selectCodex()
        expect(screen.getByRole('radio', {name: /シークレットキー方式（既定）/})).toBeChecked()
        expect(screen.getByRole('radio', {name: /ChatGPT のアカウントでのサインイン/})).toBeInTheDocument()
        // 方式ごとのポリシーと注意喚起を示す
        expect(screen.getByText(/ブラウザで OpenAI のサインインのページを開いて許可します/)).toBeInTheDocument()
    })

    // サインイン方式ではキー入力欄を出さず、サインインするまで進めない
    it('サインイン方式ではキー入力欄を出さず、サインインするまで次へ進めない', async () => {
        render(<SetupWizard onComplete={() => undefined} />)
        await selectCodex()
        fireEvent.click(screen.getByRole('radio', {name: /ChatGPT のアカウントでのサインイン/}))
        fireEvent.click(next())

        expect(await screen.findByText('ChatGPT のアカウントでサインインする')).toBeInTheDocument()
        expect(screen.queryByLabelText('シークレットキー')).toBeNull()
        expect(screen.queryByRole('button', {name: '登録して疎通確認'})).toBeNull()
        expectDisabledReason(next(), /サインインしてください/)
    })

    // サインインの成功をもって疎通確認の成功とし、認証方式を設定へ保存する
    it('サインインに成功すると先へ進め、認証方式を保存する', async () => {
        const onComplete = vi.fn()
        codexSignInState.mockResolvedValue(SIGNED_IN)
        render(<SetupWizard onComplete={onComplete} />)
        await selectCodex()
        fireEvent.click(screen.getByRole('radio', {name: /ChatGPT のアカウントでのサインイン/}))
        fireEvent.click(next())

        expect(await screen.findByText('サインインしました。')).toBeInTheDocument()
        await clickEnabled(/次へ/) // → モデル
        await screen.findByRole('radio', {name: /Claude Opus 5/})
        fireEvent.click(next()) // → エフォート
        await screen.findByRole('radio', {name: /標準（既定）/})
        fireEvent.click(next()) // → 作業者名

        fireEvent.change(await screen.findByLabelText('メールアドレス（利用者 ID）'), {
            target: {value: 'k.sato@example.co.jp'},
        })
        await clickEnabled('設定を完了する')

        await waitFor(() => expect(completeSetup).toHaveBeenCalled())
        expect(completeSetup).toHaveBeenCalledWith({
            providerId: 'codex',
            label: '',
            model: 'claude-opus-5',
            effort: 'standard',
            authMethod: 'chatgpt_signin',
            authorId: 'k.sato@example.co.jp',
            displayName: '佐藤 一郎',
        })
        // キーは登録しない（認証情報は Codex App Server が保管する）
        expect(registerKey).not.toHaveBeenCalled()
    })
})
