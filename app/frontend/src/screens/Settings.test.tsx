import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled, clickEnabledBy} from '../test/interact'
import {Settings} from './Settings'

const settingsView = vi.hoisted(() => vi.fn())
const registerKey = vi.hoisted(() => vi.fn())
const deleteKey = vi.hoisted(() => vi.fn())
const saveProviderConfig = vi.hoisted(() => vi.fn())
const setDisplayName = vi.hoisted(() => vi.fn())
const setTheme = vi.hoisted(() => vi.fn())
const setAITimeouts = vi.hoisted(() => vi.fn())
const appInfo = vi.hoisted(() => vi.fn())
const diagnosticsPreview = vi.hoisted(() => vi.fn())
const exportDiagnostics = vi.hoisted(() => vi.fn())
const codexSignInState = vi.hoisted(() => vi.fn())
const startCodexSignIn = vi.hoisted(() => vi.fn())
const reopenCodexSignInPage = vi.hoisted(() => vi.fn())
const cancelCodexSignIn = vi.hoisted(() => vi.fn())
const signOutCodex = vi.hoisted(() => vi.fn())
const codexPlanUsage = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    SettingsView: settingsView,
    RegisterKey: registerKey,
    DeleteKey: deleteKey,
    SaveProviderConfig: saveProviderConfig,
    SetDisplayName: setDisplayName,
    SetTheme: setTheme,
    SetAITimeouts: setAITimeouts,
    AppInfo: appInfo,
    DiagnosticsPreview: diagnosticsPreview,
    ExportDiagnostics: exportDiagnostics,
    CodexSignInState: codexSignInState,
    StartCodexSignIn: startCodexSignIn,
    ReopenCodexSignInPage: reopenCodexSignInPage,
    CancelCodexSignIn: cancelCodexSignIn,
    SignOutCodex: signOutCodex,
    CodexPlanUsage: codexPlanUsage,
}))

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, _cb: (ev: unknown) => void) => () => undefined,
}))

const DUMMY_KEY = 'dummy-key-for-settings-777'

const DIAGNOSTIC = {
    environment: 'アプリ: ReqWeave 0.1.0\nOS: macOS 26.6.2\nアーキテクチャ: arm64\n',
    items: [
        {name: 'environment.txt', bytes: 64},
        {name: 'app.log', bytes: 128},
    ],
    logText: '{"at":"2026-09-03T00:11:22Z","level":"error","event":"app.panic"}\n',
    truncated: false,
    totalBytes: 128,
}

const VIEW = {
    authorId: 'k.sato@example.co.jp',
    displayName: '佐藤',
    providers: [
        {
            label: '既定',
            providerId: 'anthropic',
            providerLabel: 'Anthropic（Claude）',
            model: 'claude-opus-5',
            effort: 'standard',
            effortLabel: '標準',
            keyState: '登録済み',
            keyMasked: '••••••••',
            isDefault: true,
            policyUrl: 'https://www.anthropic.com/legal/privacy',
        },
    ],
    options: [{id: 'anthropic', displayName: 'Anthropic（Claude）', policyUrl: 'https://www.anthropic.com/legal/privacy'}],
    efforts: [
        {id: 'low', label: '低', description: '軽量モデル', default: false},
        {id: 'standard', label: '標準', description: '標準の掘り下げ', default: true},
        {id: 'high', label: '高', description: '最大の掘り下げ', default: false},
    ],
    dataPolicyNotice: '組織ポリシーの確認は利用者の責務です。',
    aiReady: true,
    theme: 'dark',
    aiTimeouts: {
        connectSeconds: 10,
        responseSeconds: 300,
        minConnectSeconds: 5,
        maxConnectSeconds: 60,
        minResponseSeconds: 60,
        maxResponseSeconds: 900,
    },
}

describe('設定', () => {
    beforeEach(() => {
        settingsView.mockReset()
        registerKey.mockReset()
        deleteKey.mockReset()
        saveProviderConfig.mockReset()
        setDisplayName.mockReset()
        settingsView.mockResolvedValue(VIEW)
        registerKey.mockResolvedValue({ok: true})
        deleteKey.mockResolvedValue(undefined)
        saveProviderConfig.mockResolvedValue(undefined)
        setDisplayName.mockResolvedValue(undefined)
        setTheme.mockReset()
        setTheme.mockResolvedValue(undefined)
        setAITimeouts.mockReset()
        setAITimeouts.mockResolvedValue(undefined)
        appInfo.mockReset()
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '0.1.0'})
        diagnosticsPreview.mockReset()
        diagnosticsPreview.mockResolvedValue(DIAGNOSTIC)
        exportDiagnostics.mockReset()
        exportDiagnostics.mockResolvedValue('/tmp/ReqWeave-diagnostics.zip')
        document.documentElement.removeAttribute('data-theme')
    })

    // キーはマスク表示のみ・平文の再表示をしない
    it('キーはマスク表示で、入力はパスワード型', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('登録済み')

        expect(screen.getByText('••••••••')).toBeInTheDocument()
        expect(screen.getByLabelText('キーを更新')).toHaveAttribute('type', 'password')
        expect(document.body.textContent).not.toContain(DUMMY_KEY)
    })

    // 選択中のプロバイダに固有の注意喚起があれば併せて示す
    it('プロバイダ固有の注意喚起を、共通の注意喚起に加えて表示する', async () => {
        const notice = 'このプロバイダでは、端末の識別子が送信に付きます。'
        settingsView.mockResolvedValue({
            ...VIEW,
            providers: [{...VIEW.providers[0], notice}],
        })
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('登録済み')

        expect(screen.getByText(VIEW.dataPolicyNotice)).toBeInTheDocument()
        expect(screen.getByText(notice)).toBeInTheDocument()
    })

    // メールアドレス（利用者 ID）は表示のみ・表示名は変更できる
    it('メールアドレス（利用者 ID）は表示のみで、表示名は変更できる', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('k.sato@example.co.jp')

        expect(screen.queryByLabelText('メールアドレス（利用者 ID）')).toBeNull()
        expect(screen.getByText('メールアドレス（利用者 ID）')).toBeInTheDocument()
        expect(screen.getByText(/メールアドレスは変更できません/)).toBeInTheDocument()

        fireEvent.change(screen.getByLabelText('表示名'), {target: {value: '佐藤（営業）'}})
        fireEvent.click(screen.getByRole('button', {name: '表示名を保存'}))
        await waitFor(() => expect(setDisplayName).toHaveBeenCalledWith('佐藤（営業）'))
    })

    // 疎通確認に失敗したら変更前の設定を保持する旨を示す
    it('キー更新に失敗したら理由と「変更前のまま」であることを示す', async () => {
        registerKey.mockResolvedValue({
            ok: false,
            reason: '認証エラー',
            detail: 'キーが受け付けられませんでした。キーを確認して登録し直してください。',
        })
        render(<Settings onBack={() => undefined} />)
        await screen.findByLabelText('キーを更新')

        fireEvent.change(screen.getByLabelText('キーを更新'), {target: {value: DUMMY_KEY}})
        fireEvent.click(screen.getByRole('button', {name: '更新して疎通確認'}))

        await screen.findByText(/認証エラー/)
        expect(screen.getByText(/変更前の設定はそのままです/)).toBeInTheDocument()
    })

    // キー削除後は AI 機能がブロックされ、再登録への誘導が出る
    it('キーを削除すると AI 機能のブロックと再登録の誘導を示す', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('登録済み')

        settingsView.mockResolvedValue({
            ...VIEW,
            providers: [{...VIEW.providers[0], keyState: '未設定', keyMasked: '—'}],
            aiReady: false,
            aiBlockedReason: 'キーが未登録です。設定でキーを登録してください。',
        })
        fireEvent.click(screen.getByRole('button', {name: 'キーを削除'}))

        await screen.findByText('キーが未登録です。設定でキーを登録してください。')
        expect(deleteKey).toHaveBeenCalledWith('anthropic', '既定')
    })

    // モデル・エフォートの変更を保存できる
    it('モデルとエフォートを変更して保存できる', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByLabelText('モデル')

        fireEvent.change(screen.getByLabelText('モデル'), {target: {value: 'claude-haiku-4-5-20251001'}})
        fireEvent.change(screen.getByLabelText('エフォート'), {target: {value: 'high'}})
        fireEvent.click(screen.getByRole('button', {name: '設定を保存'}))

        await waitFor(() => expect(saveProviderConfig).toHaveBeenCalled())
        expect(saveProviderConfig).toHaveBeenCalledWith({
            label: '既定',
            providerId: 'anthropic',
            model: 'claude-haiku-4-5-20251001',
            effort: 'high',
            // 認証方式を持たないプロバイダはシークレットキー方式のまま保存する
            authMethod: 'secret_key',
            makeDefault: true,
        })
    })

    // 既定はダーク・手動切替・OS 追従なし。選択は端末ごとに保存する
    it('テーマを手動で切り替え、端末ごとの設定へ保存する', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('ダーク')

        expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
        fireEvent.click(screen.getByRole('button', {name: 'ライトへ'}))
        expect(document.documentElement.getAttribute('data-theme')).toBe('light')
        await waitFor(() => expect(setTheme).toHaveBeenCalledWith('light'))
        expect(screen.getByText(/この端末に保存され/)).toBeInTheDocument()
    })

    // 保存済みのテーマを画面に復元する
    it('保存済みのテーマを復元して表示する', async () => {
        settingsView.mockResolvedValue({...VIEW, theme: 'light'})
        render(<Settings onBack={() => undefined} />)

        await screen.findByText('ライト')
        expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    })

    // 保存に失敗したら表示を元へ戻す（画面と保存内容を食い違わせない）
    it('テーマの保存に失敗したら表示を元へ戻す', async () => {
        setTheme.mockRejectedValue(new Error('write failed'))
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('ダーク')

        fireEvent.click(screen.getByRole('button', {name: 'ライトへ'}))
        await screen.findByText('テーマを保存できませんでした。もう一度お試しください。')
        expect(document.documentElement.hasAttribute('data-theme')).toBe(false)
    })

    // AI 呼び出しの待ち時間を表示・変更できる（保存先は端末ごとのアプリ設定）
    it('AI 呼び出しの待ち時間を保存する', async () => {
        render(<Settings onBack={() => undefined} />)
        const connect = await screen.findByLabelText('接続の待ち時間（秒）')
        const response = screen.getByLabelText('応答の待ち時間（秒）')
        expect(connect).toHaveValue(10)
        expect(response).toHaveValue(300)
        expect(screen.getByText(/接続は 5〜60 秒、応答は 60〜900 秒の範囲/)).toBeInTheDocument()

        fireEvent.change(connect, {target: {value: '30'}})
        fireEvent.change(response, {target: {value: '120'}})
        fireEvent.click(screen.getByRole('button', {name: '待ち時間を保存'}))

        await waitFor(() => expect(setAITimeouts).toHaveBeenCalledWith(30, 120))
        await screen.findByText(/次の AI 呼び出しから適用されます/)
    })

    // 範囲外はバックエンドが拒否し、その理由をそのまま出す（範囲の正本はバックエンド）
    it('待ち時間の保存に失敗したら理由を表示する', async () => {
        setAITimeouts.mockRejectedValue(new Error('接続の待ち時間が範囲外です。5〜60 秒で指定してください。'))
        render(<Settings onBack={() => undefined} />)
        const connect = await screen.findByLabelText('接続の待ち時間（秒）')

        fireEvent.change(connect, {target: {value: '1'}})
        fireEvent.click(screen.getByRole('button', {name: '待ち時間を保存'}))

        await screen.findByText(/接続の待ち時間が範囲外です。5〜60 秒で指定してください。/)
    })

    // プロバイダのポリシー参照先と注意喚起
    it('データ利用ポリシーの注意喚起と参照先を示す', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('組織ポリシーの確認は利用者の責務です。')
        expect(screen.getByText('https://www.anthropic.com/legal/privacy')).toBeInTheDocument()
    })
    // 版番号（正本 = internal/appversion/VERSION）を設定画面に表示する
    it('アプリの版番号を表示する', async () => {
        render(<Settings onBack={() => undefined} />)

        expect(await screen.findByText('このアプリについて')).toBeInTheDocument()
        expect(await screen.findByText('0.1.0')).toBeInTheDocument()
    })

    // 版番号を取得できなくても設定画面の他の機能を止めない
    it('版番号を取得できないときは「—」を表示し、他の設定は使える', async () => {
        appInfo.mockRejectedValue(new Error('unavailable'))
        render(<Settings onBack={() => undefined} />)

        await screen.findByText('登録済み')
        const about = (await screen.findByText('このアプリについて')).closest('section')
        expect(about).not.toBeNull()
        expect(about!.textContent).toContain('—')
        expect(screen.getByLabelText('キーを更新')).toBeInTheDocument()
    })
    // 書き出す内容を確認してからでないと保存できない
    it('診断情報は内容を確認してからでないと保存できない', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('診断情報')

        const save = screen.getByRole('button', {name: 'ファイルに保存'})
        expect(save).toBeDisabled()

        fireEvent.click(screen.getByRole('button', {name: '書き出す内容を確認'}))
        await screen.findByLabelText('書き出す内容')

        expect(screen.getByRole('button', {name: 'ファイルに保存'})).toBeEnabled()
        expect(exportDiagnostics).not.toHaveBeenCalled()
    })

    // 含まれるものと本文を提示する
    it('診断情報の内容（含まれるものと本文）を提示する', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('診断情報')

        fireEvent.click(screen.getByRole('button', {name: '書き出す内容を確認'}))
        const body = await screen.findByLabelText('書き出す内容')

        expect(body.textContent).toContain('OS: macOS 26.6.2')
        expect(body.textContent).toContain('app.panic')
        expect(screen.getByText(/environment\.txt（64 バイト）/)).toBeInTheDocument()
        expect(screen.getByText(/app\.log（128 バイト）/)).toBeInTheDocument()
    })

    // 保存先を選んだら保存し、結果を知らせる
    it('確認後にファイルへ保存し、保存先を知らせる', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('診断情報')

        fireEvent.click(screen.getByRole('button', {name: '書き出す内容を確認'}))
        await screen.findByLabelText('書き出す内容')
        fireEvent.click(screen.getByRole('button', {name: 'ファイルに保存'}))

        await waitFor(() => expect(exportDiagnostics).toHaveBeenCalledTimes(1))
        expect(await screen.findByText(/診断情報を保存しました/)).toBeInTheDocument()
    })

    // 外部へ送らないこと・渡す相手は利用者が選ぶことを画面で明示する
    it('外部へ送らない旨を画面に明示する', async () => {
        render(<Settings onBack={() => undefined} />)

        const section = (await screen.findByText('診断情報')).closest('section')
        expect(section).not.toBeNull()
        expect(section!.textContent).toContain('自動では送られません')
        expect(section!.textContent).toContain('含みません')
    })
})

// 紹介スライドの再表示
describe('設定 — 紹介スライドの再表示', () => {
    beforeEach(() => {
        settingsView.mockReset()
        settingsView.mockResolvedValue(VIEW)
        appInfo.mockReset()
        appInfo.mockResolvedValue({name: 'ReqWeave', version: '0.1.0'})
        diagnosticsPreview.mockReset()
        diagnosticsPreview.mockResolvedValue(DIAGNOSTIC)
    })

    it('「紹介スライドをもう一度見る」で紹介スライドを開き、閉じると設定へ戻る', async () => {
        render(<Settings onBack={() => undefined} />)

        fireEvent.click(await screen.findByRole('button', {name: '紹介スライドをもう一度見る'}))
        expect(await screen.findByText('ReqWeave へようこそ')).toBeInTheDocument()
        // 再表示では設定へ戻る旨を示す（初回の自動表示とは文言が異なる）
        expect(screen.getByText(/設定画面へ戻ります/)).toBeInTheDocument()

        fireEvent.click(screen.getByRole('button', {name: 'スキップ'}))
        expect(await screen.findByText('はじめての方へ')).toBeInTheDocument()
    })

    // 再表示では intro_shown を変えない。設定画面から記録の更新を呼ばないことを
    // 実装の構造で担保する（呼び出しの有無をモックで数えるのではなく、経路そのものを持たせない）。
    it('設定画面は初回表示の記録（MarkIntroShown）を呼ぶ経路を持たない', async () => {
        const source = (await import('./Settings.tsx?raw')).default
        expect(source).toContain('紹介スライドをもう一度見る')
        expect(source).not.toContain('MarkIntroShown')
    })
})

/*
 * Codex App Server の認証方式・サインイン・残量。
 */
const CODEX_POLICY = 'https://openai.com/policies/api-data-usage-policies/'

const CODEX_PROVIDER_NOTICE =
    'Codex App Server では、本システムが送る内容に加えて、Codex 自身が' +
    '道具の定義文・利用中の OS の版と CPU の種類・端末ごとの識別子を送ります。'

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

/** サインイン方式で保存済みの設定（キーを持たないため状態は「—」で届く）。 */
const CODEX_VIEW = {
    ...VIEW,
    providers: [
        {
            label: '既定',
            providerId: 'codex',
            providerLabel: 'Codex App Server（OpenAI）',
            model: 'gpt-5-codex',
            effort: 'standard',
            effortLabel: '標準',
            authMethod: 'chatgpt_signin',
            authMethodLabel: 'ChatGPT のアカウントでのサインイン',
            keyState: '—',
            keyMasked: '—',
            isDefault: true,
            policyUrl: CODEX_POLICY,
            notice: CODEX_PROVIDER_NOTICE,
        },
    ],
    options: [
        {
            id: 'codex',
            displayName: 'Codex App Server（OpenAI）',
            policyUrl: CODEX_POLICY,
            notice: CODEX_PROVIDER_NOTICE,
            authMethods: CODEX_AUTH_METHODS,
        },
    ],
}

const SIGNED_IN = {
    available: true,
    state: 'signed_in',
    label: '既定',
    waitMinutes: 15,
    account: {email: 'k.sato@example.co.jp', planLabel: 'Plus', accountLabel: 'ChatGPT のアカウント'},
}

describe('設定 — Codex App Server の認証方式とサインイン', () => {
    beforeEach(() => {
        settingsView.mockReset().mockResolvedValue(CODEX_VIEW)
        saveProviderConfig.mockReset().mockResolvedValue(undefined)
        appInfo.mockReset().mockResolvedValue({name: 'ReqWeave', version: '0.1.0'})
        diagnosticsPreview.mockReset().mockResolvedValue(DIAGNOSTIC)
        codexSignInState.mockReset().mockResolvedValue(SIGNED_IN)
        signOutCodex.mockReset().mockResolvedValue(undefined)
        codexPlanUsage
            .mockReset()
            .mockResolvedValue({available: true, fetched: false, notice: 'まだ取得していません（AI を使うと表示されます）'})
    })

    // 認証方式を 2 つ持つプロバイダでだけ切り替えを出す
    it('認証方式の切り替えと、方式ごとの注意喚起を表示する', async () => {
        render(<Settings onBack={() => undefined} />)

        const select = await screen.findByLabelText('認証方式')
        expect(select).toHaveValue('chatgpt_signin')
        expect(screen.getByText(/ブラウザで OpenAI のサインインのページを開いて許可します/)).toBeInTheDocument()
        expect(screen.getByText(/現在の設定: ChatGPT のアカウントでのサインイン/)).toBeInTheDocument()
        // 切り替えとサインアウトは別であることを示す
        expect(screen.getByText(/認証情報を消すときは、別途サインアウトしてください/)).toBeInTheDocument()
    })

    // サインイン方式ではキーの状態欄に「—」を出し、入力欄を出さない
    it('サインイン方式ではキーの入力欄を出さず、状態に「—」を出す', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByLabelText('認証方式')

        const section = screen.getByText('シークレットキー').closest('section')
        expect(section).not.toBeNull()
        expect(section!.textContent).toContain('—')
        expect(screen.queryByLabelText('キーを更新')).toBeNull()
        expect(screen.queryByRole('button', {name: '更新して疎通確認'})).toBeNull()
        expect(screen.queryByRole('button', {name: 'キーを削除'})).toBeNull()
        expect(screen.getByText(/シークレットキーを使いません/)).toBeInTheDocument()
    })

    // サインインの状態（アカウント・プラン）を表示する
    it('サインインの状態としてアカウントとプランを表示する', async () => {
        render(<Settings onBack={() => undefined} />)

        expect(await screen.findByText('サインインしました。')).toBeInTheDocument()
        // 利用者 ID（作業者名の欄）と同じ文字列が出るため、サインインの欄に限って確かめる
        const panel = within(screen.getByLabelText('ChatGPT のアカウントでのサインイン'))
        expect(panel.getByText('k.sato@example.co.jp')).toBeInTheDocument()
        expect(panel.getByText('Plus')).toBeInTheDocument()
        expect(panel.getByText('サインイン済み')).toBeInTheDocument()
    })

    // サインアウトはアプリ内の確認ダイアログを経る（ネイティブ confirm を使わない）
    it('サインアウトは確認操作を経てから実行する', async () => {
        render(<Settings onBack={() => undefined} />)

        await clickEnabled('サインアウトする')
        // 確認の面が開いただけでは実行しない
        expect(signOutCodex).not.toHaveBeenCalled()
        const dialog = await screen.findByRole('dialog', {name: 'サインアウトの確認'})
        expect(within(dialog).getByText(/もう一度サインインするまで実行できません/)).toBeInTheDocument()

        await clickEnabledBy(() => within(dialog).getByRole('button', {name: 'サインアウトする'}))

        await waitFor(() => expect(signOutCodex).toHaveBeenCalledWith('既定'))
        expect(
            await screen.findByText(/サインアウトしました。AI を使う操作は、もう一度サインインするまで実行できません。/),
        ).toBeInTheDocument()
    })

    it('確認の面で「やめる」を押すとサインアウトしない', async () => {
        render(<Settings onBack={() => undefined} />)

        await clickEnabled('サインアウトする')
        const dialog = await screen.findByRole('dialog', {name: 'サインアウトの確認'})
        fireEvent.click(within(dialog).getByRole('button', {name: 'やめる'}))

        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        expect(signOutCodex).not.toHaveBeenCalled()
    })

    // 認証方式の切り替えは「設定を保存」で確定する
    it('認証方式をシークレットキー方式へ切り替えて保存できる', async () => {
        render(<Settings onBack={() => undefined} />)

        fireEvent.change(await screen.findByLabelText('認証方式'), {target: {value: 'secret_key'}})
        // 切り替えるとキーの入力欄が出る（サインインしたままでも切り替えられる）
        expect(await screen.findByLabelText('キーを更新')).toBeInTheDocument()

        await clickEnabled('設定を保存')

        await waitFor(() => expect(saveProviderConfig).toHaveBeenCalled())
        expect(saveProviderConfig).toHaveBeenCalledWith({
            label: '既定',
            providerId: 'codex',
            model: 'gpt-5-codex',
            effort: 'standard',
            authMethod: 'secret_key',
            makeDefault: true,
        })
    })

    // サインインしているときはプランの残量を出す（未取得の文言つき）
    it('プランの残量の欄を出し、まだ取得していないことを示す', async () => {
        render(<Settings onBack={() => undefined} />)

        expect(await screen.findByLabelText('ChatGPT のプランの残量')).toBeInTheDocument()
        expect(screen.getByText(/まだ取得していません/)).toBeInTheDocument()
        expect(screen.getByText(/この画面を開いても取りに行きません/)).toBeInTheDocument()
    })

    // シークレットキー方式では残量を出さない
    it('シークレットキー方式では残量の欄を出さない', async () => {
        settingsView.mockResolvedValue({
            ...CODEX_VIEW,
            providers: [
                {
                    ...CODEX_VIEW.providers[0],
                    authMethod: 'secret_key',
                    authMethodLabel: 'シークレットキー方式',
                    keyState: '登録済み',
                    keyMasked: '••••••••',
                },
            ],
        })
        codexPlanUsage.mockResolvedValue({available: false, fetched: false})
        render(<Settings onBack={() => undefined} />)

        await screen.findByLabelText('キーを更新')
        expect(screen.queryByLabelText('ChatGPT のプランの残量')).toBeNull()
        expect(screen.queryByText('サインインしました。')).toBeNull()
    })
})

// 認証方式を 1 つしか持たないプロバイダでは、認証方式の選択を画面へ出さない
describe('設定 — 認証方式を持たないプロバイダ', () => {
    beforeEach(() => {
        settingsView.mockReset().mockResolvedValue(VIEW)
        appInfo.mockReset().mockResolvedValue({name: 'ReqWeave', version: '0.1.0'})
        diagnosticsPreview.mockReset().mockResolvedValue(DIAGNOSTIC)
    })

    it('認証方式の選択もサインインの欄も出さない', async () => {
        render(<Settings onBack={() => undefined} />)
        await screen.findByText('登録済み')

        expect(screen.queryByLabelText('認証方式')).toBeNull()
        expect(screen.queryByText('ChatGPT のアカウント')).toBeNull()
        expect(screen.queryByRole('button', {name: 'サインアウトする'})).toBeNull()
        expect(screen.getByLabelText('キーを更新')).toBeInTheDocument()
    })
})
