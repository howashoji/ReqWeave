import {useCallback, useEffect, useRef, useState} from 'react'
import {
    CancelCodexSignIn,
    CodexSignInState,
    ReopenCodexSignInPage,
    StartCodexSignIn,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Button} from './Button'
import {Chip} from './Chip'
import type {StateTone} from './state'
import {errorText} from './errorText'
import './CodexSignIn.css'

/**
 * ChatGPT のアカウントでのサインイン。
 *
 * 初期設定ウィザードと設定の**どちらからも同じ画面**を使う
 * （文言・操作は同じで、終わった後の戻り先だけが違う）。画面ごとに作り込まない。
 *
 * 4 つの状態を順にたどる:
 *
 *   1. 説明 … 開く前に、Codex が送信に付け足す内容・動作ログに残る応答のクッキー・
 *      この認証方式に適用されるデータ利用ポリシーの参照先を示す（何が送られ何が残るかを、始める前に利用者が判断できるように）。
 *      **文言はバックエンドが持つ**（認証方式の説明・プロバイダの注意喚起）ため、
 *      ここでは受け取って出すだけにする（二重管理の禁止）。
 *   2. 待機 … 既定のブラウザが開く。**待っている間も他の画面を使える**ため、完了は
 *      Wails のイベント（`codex:signin`）で受け取る。
 *   3. 成功 … アカウント（メールアドレス）とプラン。個人向けプラン・取得できないときは
 *      学習利用の注意喚起と停止方法（`account.trainingNotice`。空なら出さない）。
 *   4. 失敗 … 原因＋次の行動の 1 文（`message`。文言はバックエンドのエラーカタログが持つ）。**設定は変わらない**。
 *
 * **回答モードの AI 対話ペインでも本画面を共用する**。
 * 違いは「始める呼び出し」だけで（回答モードは送信範囲の事前表示の確認を経る必要がある）、
 * 表示・文言・取り消し・待機の扱いは同じにする。**画面ごとに作り込まない**。
 *
 * **メールアドレスは表示するだけ**で、保存も記録もしない（バックエンドも保存しない）。
 * プラン種別・アカウントの種類のラベルはバックエンドが確定済みで、未知は「不明」で届く
 * （生のコード値を画面へ出さない）。
 */

/** サインインの状態（バックエンドの binding.SignInState と対応。**画面へは出さない**）。 */
const SIGN_IN_STATES = ['signed_out', 'waiting', 'signed_in', 'failed'] as const

type SignInState = (typeof SIGN_IN_STATES)[number]

/** 状態チップのラベル。型で網羅を強制し、未知の値は「状態不明」に倒す。 */
const STATE_LABELS: Record<SignInState, string> = {
    signed_out: '未サインイン',
    waiting: 'サインインの完了待ち',
    signed_in: 'サインイン済み',
    failed: 'サインインできていません',
}

const SIGN_IN_TONES: Record<SignInState, StateTone> = {
    signed_out: 'info',
    waiting: 'warn',
    signed_in: 'accent',
    failed: 'danger',
}

/** 既知の状態だけを返す（未知は null = 「状態不明」として扱う）。 */
function knownState(state: string): SignInState | null {
    return (SIGN_IN_STATES as readonly string[]).includes(state) ? (state as SignInState) : null
}

export function CodexSignIn({
    label,
    methodDescription,
    providerNotice,
    policyUrl,
    onChange,
    start: startSignIn,
}: {
    /** 対象のプロバイダ設定の表示名（初期設定では空文字＝既定の設定）。 */
    label: string
    /** 認証方式の説明（バックエンドの AuthMethodOption.description）。 */
    methodDescription?: string
    /** プロバイダ固有の注意喚起（Codex が送信に付け足す内容）。 */
    providerNotice?: string
    /** データ利用ポリシーの参照先。 */
    policyUrl?: string
    /** 状態が変わったことを親へ知らせる（ウィザードの「次へ」・設定の再読込に使う）。 */
    onChange?: (state: string) => void
    /**
     * サインインを始める呼び出し。省略時は担当者モードの経路（`StartCodexSignIn(label)`）。
     * 回答モードは事前表示の確認を伴う経路（`StartRespondAISignIn`）を渡す。
     */
    start?: () => Promise<binding.SignInView>
}) {
    const [view, setView] = useState<binding.SignInView | null>(null)
    const [error, setError] = useState('')
    const [busy, setBusy] = useState(false)

    /*
     * 状態の取得は**通信を起こさない**（保持している値を返すだけ = CodexSignInState）。
     * 画面を開くたびに子プロセスを起こさないため、ここでは CodexAccount を呼ばない。
     */
    const reload = useCallback(() => {
        return Promise.resolve()
            .then(() => CodexSignInState())
            .then((next) => setView(next))
            .catch((err: unknown) => setError(errorText(err)))
    }, [])

    useEffect(() => {
        void reload()
    }, [reload])

    // 完了の通知（上の 2. 待機）。他の画面を見ていても結果が届く。
    useEffect(() => {
        const off = EventsOn('codex:signin', (ev: binding.SignInView) => {
            setError('')
            setView(ev)
        })
        return () => off()
    }, [])

    /*
     * 親への通知は ref 越しに行う。onChange をそのまま依存に入れると、親が無名関数を
     * 渡している場合に描画のたびに通知が走る（状態が変わっていないのに親を更新する）。
     */
    const notify = useRef(onChange)
    notify.current = onChange
    const state = view ? view.state : ''
    useEffect(() => {
        if (state) {
            notify.current?.(state)
        }
    }, [state])

    const start = useCallback(() => {
        setBusy(true)
        setError('')
        return Promise.resolve()
            .then(() => (startSignIn ? startSignIn() : StartCodexSignIn(label)))
            .then((next) => setView(next))
            .catch((err: unknown) => setError(errorText(err)))
            .finally(() => setBusy(false))
    }, [label, startSignIn])

    const reopen = useCallback(() => {
        setError('')
        return Promise.resolve()
            .then(() => ReopenCodexSignInPage())
            .catch((err: unknown) => setError(errorText(err)))
    }, [])

    const cancel = useCallback(() => {
        setError('')
        return Promise.resolve()
            .then(() => CancelCodexSignIn())
            .then(() => reload())
            .catch((err: unknown) => setError(errorText(err)))
    }, [reload])

    /*
     * 読み込みが終わるまでは状態を名乗らない。
     * 状態が届く前に「未サインイン」等を描くと、**一瞬だけ違う状態が見える**
     * （押せてはいけない操作が押せる・テストが読み込み前の描画で緑になる）。
     */
    if (!view) {
        return (
            <section className="rw-signin" aria-label="ChatGPT のアカウントでのサインイン">
                {error ? (
                    <p className="rw-signin__notice" data-tone="danger" role="status">
                        {error}
                    </p>
                ) : (
                    <p className="rw-signin__hint">サインインの状態を読み込んでいます…</p>
                )}
            </section>
        )
    }

    // 使えない環境ではサインインの操作を出さず、代わりの方式を案内する。
    if (!view.available) {
        return (
            <section className="rw-signin" aria-label="ChatGPT のアカウントでのサインイン">
                <p className="rw-signin__hint">
                    この端末では ChatGPT のアカウントでのサインインを使えません。シークレットキー方式を選んでください。
                </p>
            </section>
        )
    }

    const current = knownState(state)
    const account = view?.account
    const waitMinutes = view?.waitMinutes ?? 0

    return (
        <section className="rw-signin" aria-label="ChatGPT のアカウントでのサインイン">
            <p className="rw-signin__state">
                <Chip tone={current ? SIGN_IN_TONES[current] : 'warn'}>
                    {current ? STATE_LABELS[current] : '状態不明'}
                </Chip>
            </p>

            {current === 'waiting' ? (
                <>
                    <p className="rw-signin__lead" role="status">
                        ブラウザでサインインを完了してください。
                    </p>
                    <p className="rw-signin__hint">
                        最大 {waitMinutes} 分待ちます。待っている間もほかの画面を使えます（完了するとこの画面に結果が出ます）。
                    </p>
                    <div className="rw-signin__actions">
                        <Button onClick={() => void reopen()}>ブラウザをもう一度開く</Button>
                        <Button onClick={() => void cancel()}>取り消す</Button>
                    </div>
                </>
            ) : null}

            {current === 'signed_in' ? (
                <>
                    <p className="rw-signin__lead" role="status">
                        サインインしました。
                    </p>
                    <dl className="rw-signin__figures">
                        <div>
                            <dt>アカウント</dt>
                            <dd className="rw-mono">{account?.email ? account.email : '—'}</dd>
                        </div>
                        <div>
                            <dt>プラン</dt>
                            <dd>{account?.planLabel ? account.planLabel : '不明'}</dd>
                        </div>
                        <div>
                            <dt>認証の種類</dt>
                            <dd>{account?.accountLabel ? account.accountLabel : '不明'}</dd>
                        </div>
                    </dl>
                    {account?.trainingNotice ? (
                        <p className="rw-signin__notice" data-tone="warn" role="status">
                            {account.trainingNotice}
                        </p>
                    ) : null}
                </>
            ) : null}

            {current === 'signed_in' || current === 'waiting' ? null : (
                <>
                    {current === 'failed' ? (
                        <>
                            <p className="rw-signin__notice" data-tone="danger" role="status">
                                {view?.message
                                    ? view.message
                                    : 'サインインできませんでした。もう一度サインインしてください。'}
                            </p>
                            <p className="rw-signin__hint">設定は変わっていません。そのまま使い続けられます。</p>
                        </>
                    ) : (
                        <>
                            <p className="rw-signin__lead">
                                ブラウザで OpenAI のサインインのページを開きます。サインインして許可すると、この画面に結果が表示されます。
                            </p>
                            {methodDescription ? <p className="rw-signin__hint">{methodDescription}</p> : null}
                            {providerNotice ? <p className="rw-signin__hint">{providerNotice}</p> : null}
                            {policyUrl ? (
                                <p className="rw-signin__hint">
                                    データ利用ポリシー: <span className="rw-mono">{policyUrl}</span>
                                </p>
                            ) : null}
                        </>
                    )}
                    <div className="rw-signin__actions">
                        <Button
                            onClick={() => void start()}
                            disabledReason={busy ? 'サインインの準備中です。' : undefined}
                        >
                            {current === 'failed' ? 'もう一度サインイン' : 'サインイン'}
                        </Button>
                    </div>
                </>
            )}

            {error ? (
                <p className="rw-signin__notice" data-tone="danger" role="status">
                    {error}
                </p>
            ) : null}
        </section>
    )
}
