import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled} from '../test/interact'
import {CodexSignIn} from './CodexSignIn'

/*
 * ChatGPT のアカウントでのサインイン。
 *
 * 受け入れ条件のうち画面が担うもの:
 *   - 4 つの状態（説明 → 待機 → 成功 / 失敗）をたどれること
 *   - 待機は「最大 15 分」と分かり、ブラウザの再オープンと取り消しができること
 *   - 成功でアカウント・プランを示し、個人向けプラン（取得できない場合を含む）では
 *     学習利用の注意喚起と停止方法を併せて出すこと
 *   - 失敗は原因＋次の行動の 1 文で、**設定は変わらない**ことを示すこと
 *   - プラン種別・アカウント種別の未知の値が「不明」になり、生のコード値が画面へ出ないこと
 */

const codexSignInState = vi.hoisted(() => vi.fn())
const startCodexSignIn = vi.hoisted(() => vi.fn())
const reopenCodexSignInPage = vi.hoisted(() => vi.fn())
const cancelCodexSignIn = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    CodexSignInState: codexSignInState,
    StartCodexSignIn: startCodexSignIn,
    ReopenCodexSignInPage: reopenCodexSignInPage,
    CancelCodexSignIn: cancelCodexSignIn,
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

/** 完了の通知（`codex:signin`）を届ける。待っている間も他の画面を使えるため、結果はイベントで来る。 */
function emit(ev: Record<string, unknown>) {
    listeners.current.forEach((cb) => cb(ev))
}

const SIGNED_OUT = {available: true, state: 'signed_out', label: '既定', waitMinutes: 15}
const WAITING = {available: true, state: 'waiting', label: '既定', waitMinutes: 15}

/** 個人向けプラン。学習利用の注意喚起と停止方法はバックエンドの文言をそのまま出す。 */
const TRAINING_NOTICE =
    'このプランでは、送信内容が OpenAI のモデルの学習に使われることがあります。' +
    'ChatGPT の設定「データコントロール」の「Improve the model for everyone」をオフにするか、' +
    'プライバシーポータルで「Do not train on my content」を選ぶと停止できます（どちらか一方で足ります）。'

const METHOD_DESCRIPTION =
    'ブラウザで OpenAI のサインインのページを開いて許可します。' +
    'Codex App Server の動作ログには呼び出し先の応答のクッキーが残りますが、' +
    'これは一時領域にあり、本システムの終了時と次回の起動時に消えます。'

const PROVIDER_NOTICE =
    'Codex App Server では、本システムが送る内容に加えて、Codex 自身が' +
    '道具の定義文・利用中の OS の版と CPU の種類・端末ごとの識別子を送ります。'

function renderPanel() {
    render(
        <CodexSignIn
            label="既定"
            methodDescription={METHOD_DESCRIPTION}
            providerNotice={PROVIDER_NOTICE}
            policyUrl="https://openai.com/policies/api-data-usage-policies/"
        />,
    )
}

describe('ChatGPT のアカウントでのサインイン', () => {
    beforeEach(() => {
        listeners.current = []
        codexSignInState.mockReset().mockResolvedValue(SIGNED_OUT)
        startCodexSignIn.mockReset().mockResolvedValue(WAITING)
        reopenCodexSignInPage.mockReset().mockResolvedValue(undefined)
        cancelCodexSignIn.mockReset().mockResolvedValue(undefined)
    })

    // 1. 説明: 開く前に付け足される送信内容・動作ログのクッキー・ポリシーの参照先を示す
    it('説明では、付け足される送信内容・動作ログのクッキー・ポリシーの参照先を示す', async () => {
        renderPanel()

        expect(
            await screen.findByText(
                /ブラウザで OpenAI のサインインのページを開きます。サインインして許可すると、この画面に結果が表示されます。/,
            ),
        ).toBeInTheDocument()
        expect(screen.getByText(/動作ログには呼び出し先の応答のクッキーが残ります/)).toBeInTheDocument()
        expect(screen.getByText(/一時領域にあり、本システムの終了時と次回の起動時に消えます/)).toBeInTheDocument()
        expect(screen.getByText(/道具の定義文・利用中の OS の版と CPU の種類・端末ごとの識別子/)).toBeInTheDocument()
        expect(screen.getByText(/openai\.com\/policies\/api-data-usage-policies/)).toBeInTheDocument()
        expect(screen.getByRole('button', {name: 'サインイン'})).toBeEnabled()
    })

    // 2. 待機: 最大 15 分・他の画面を使える・再オープンと取り消し
    it('サインインを押すと待機になり、最大 15 分待つことと 2 つの操作を示す', async () => {
        renderPanel()
        await screen.findByRole('button', {name: 'サインイン'})

        await clickEnabled('サインイン')

        expect(await screen.findByText('ブラウザでサインインを完了してください。')).toBeInTheDocument()
        expect(startCodexSignIn).toHaveBeenCalledWith('既定')
        expect(screen.getByText(/最大 15 分待ちます/)).toBeInTheDocument()
        expect(screen.getByText(/待っている間もほかの画面を使えます/)).toBeInTheDocument()
        expect(screen.getByRole('button', {name: 'ブラウザをもう一度開く'})).toBeEnabled()
        expect(screen.getByRole('button', {name: '取り消す'})).toBeEnabled()
        // キーの入力欄をどこにも出さない（この方式ではキーを使わない）
        expect(screen.queryByLabelText('シークレットキー')).toBeNull()
    })

    it('「ブラウザをもう一度開く」で認可のページを開き直す', async () => {
        codexSignInState.mockResolvedValue(WAITING)
        renderPanel()

        await clickEnabled('ブラウザをもう一度開く')
        await waitFor(() => expect(reopenCodexSignInPage).toHaveBeenCalledTimes(1))
    })

    it('「取り消す」で待ち受けを取り消し、未サインインへ戻る', async () => {
        codexSignInState.mockResolvedValue(WAITING)
        renderPanel()
        await screen.findByText('ブラウザでサインインを完了してください。')
        codexSignInState.mockResolvedValue(SIGNED_OUT)

        await clickEnabled('取り消す')

        expect(await screen.findByRole('button', {name: 'サインイン'})).toBeEnabled()
        expect(cancelCodexSignIn).toHaveBeenCalledTimes(1)
        expect(screen.getByText('未サインイン')).toBeInTheDocument()
    })

    // 3. 成功: 待っている間に他の画面を見ていても、完了は通知で届く
    it('待機中に届いた完了の通知で、アカウントとプランを表示する', async () => {
        codexSignInState.mockResolvedValue(WAITING)
        renderPanel()
        await screen.findByText('ブラウザでサインインを完了してください。')

        emit({
            available: true,
            state: 'signed_in',
            label: '既定',
            waitMinutes: 15,
            account: {
                email: 'k.sato@example.co.jp',
                planLabel: 'Plus',
                accountLabel: 'ChatGPT のアカウント',
                trainingNotice: TRAINING_NOTICE,
            },
        })

        expect(await screen.findByText('サインインしました。')).toBeInTheDocument()
        expect(screen.getByText('k.sato@example.co.jp')).toBeInTheDocument()
        expect(screen.getByText('Plus')).toBeInTheDocument()
        expect(screen.getByText('ChatGPT のアカウント')).toBeInTheDocument()
    })

    // 個人向けプランでは学習利用の注意喚起と停止方法を併せて示す
    it('個人向けプランでは学習利用の注意喚起と停止方法を示す', async () => {
        codexSignInState.mockResolvedValue({
            available: true,
            state: 'signed_in',
            label: '既定',
            account: {
                email: 'k.sato@example.co.jp',
                planLabel: 'Free',
                accountLabel: 'ChatGPT のアカウント',
                trainingNotice: TRAINING_NOTICE,
            },
        })
        renderPanel()

        const notice = await screen.findByText(/モデルの学習に使われることがあります/)
        expect(notice.textContent).toContain('Improve the model for everyone')
        expect(notice.textContent).toContain('Do not train on my content')
    })

    // 法人向けプランは既定で学習に使われないため、注意喚起を出さない（空で届く）
    it('学習利用の注意喚起が無いプランでは注意喚起を出さない', async () => {
        codexSignInState.mockResolvedValue({
            available: true,
            state: 'signed_in',
            label: '既定',
            account: {
                email: 'k.sato@example.co.jp',
                planLabel: 'Enterprise',
                accountLabel: 'ChatGPT のアカウント',
            },
        })
        renderPanel()

        await screen.findByText('サインインしました。')
        expect(screen.queryByText(/モデルの学習に使われることがあります/)).toBeNull()
    })

    // プラン種別が取得できないときは「不明」に倒し、生のコード値を出さない
    it('プランを取得できないときは「不明」と表示し、生のコード値を出さない', async () => {
        codexSignInState.mockResolvedValue({
            available: true,
            state: 'signed_in',
            label: '既定',
            account: {planLabel: '不明', accountLabel: '不明', trainingNotice: TRAINING_NOTICE},
        })
        renderPanel()

        await screen.findByText('サインインしました。')
        expect(screen.getAllByText('不明')).toHaveLength(2)
        expect(document.body.textContent).not.toContain('prolite')
        // メールアドレスが届かないときは空欄にせず「—」を出す
        expect(screen.getByText('—')).toBeInTheDocument()
    })

    // 4. 失敗: 原因＋次の行動の 1 文と、設定が変わらないこと
    it('失敗は原因と次の行動の 1 文を示し、設定が変わらないことを伝える', async () => {
        codexSignInState.mockResolvedValue({
            available: true,
            state: 'failed',
            label: '既定',
            message: 'サインインが完了しませんでした。もう一度サインインしてください。',
        })
        renderPanel()

        expect(
            await screen.findByText('サインインが完了しませんでした。もう一度サインインしてください。'),
        ).toBeInTheDocument()
        expect(screen.getByText(/設定は変わっていません/)).toBeInTheDocument()
        expect(screen.getByRole('button', {name: 'もう一度サインイン'})).toBeEnabled()
    })

    // 保存値が未知の列挙値でも、生のコード値を画面に出さない
    it('未知の状態は「状態不明」に倒し、生のコード値を画面へ出さない', async () => {
        codexSignInState.mockResolvedValue({available: true, state: 'revoked_by_provider', label: '既定'})
        renderPanel()

        expect(await screen.findByText('状態不明')).toBeInTheDocument()
        expect(document.body.textContent).not.toContain('revoked_by_provider')
        // 行き止まりにしない（もう一度サインインできる）
        expect(screen.getByRole('button', {name: 'サインイン'})).toBeEnabled()
    })

    // 使えない環境ではサインインの操作を出さず、代わりの方式を案内する
    it('サインインを使えない環境では操作を出さず、別の方式を案内する', async () => {
        codexSignInState.mockResolvedValue({available: false, state: 'signed_out'})
        renderPanel()

        expect(await screen.findByText(/シークレットキー方式を選んでください/)).toBeInTheDocument()
        expect(screen.queryByRole('button', {name: 'サインイン'})).toBeNull()
    })

    // バインディングのエラーは errorText を通して出す（「Error: 」を出さない）
    it('サインインを開始できないときは理由を日本語 1 文で示す', async () => {
        startCodexSignIn.mockRejectedValue(
            new Error('サインインを始められませんでした。しばらく待ってから、もう一度サインインしてください。'),
        )
        renderPanel()
        await screen.findByRole('button', {name: 'サインイン'})

        await clickEnabled('サインイン')

        const notice = await screen.findByText(/サインインを始められませんでした/)
        expect(notice.textContent).not.toContain('Error:')
    })
})
