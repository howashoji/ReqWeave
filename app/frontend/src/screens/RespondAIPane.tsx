import {useCallback, useEffect, useRef, useState} from 'react'
import {
    CancelRespondAIMessage,
    DisableRespondAI,
    EnableRespondAI,
    RegisterRespondAIKey,
    RespondAIAnswerDraft,
    RespondAIDialogue,
    RespondAIProviders,
    RespondAIScope,
    SendRespondAIMessage,
    StartRespondAISignIn,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Button, CodexSignIn, Utterance, errorText, useFollowLatest} from '../ui'
import './RespondAIPane.css'

/**
 * 回答モードの AI 対話ペイン（回答画面の下部に置く任意の機能。AI を使わなくても回答できる）。
 *
 * ペインは 2 つの状態を同じ場所で入れ替える（**画面も遷移も増やさない**）:
 *
 *   未有効 … AIプロバイダの選択・認証方式の選択・注意喚起・**送信範囲の事前表示**・
 *            キーの入力またはサインイン（設定画面のサインインと同じ部品を使う）。
 *   有効   … 対話履歴・入力欄・送信・中断・「この内容を回答欄へ入れる」。
 *
 * **閉じる操作はここに置かない**。開閉はペインの直上（回答欄の下）の 1 つの従属操作が担う
 * （開くときは「AI と相談する」、開いているときは「相談を閉じる」と 1 つの操作で切り替えるため。
 * 同じラベルの操作を 2 つ並べない）。
 *
 * **統制表示（モデル・エフォート・トークン消費・完成度）を一切出さない**（ウィザード・回答系の画面では出さない）。
 * **強調（緑塗り）はフッターの「次の質問へ」だけ**なので、ここの操作はすべて枠線・テキストにする
 * （主役の操作を 1 つに絞る）。文言（送信範囲の一覧・注意喚起）は**バックエンドが持つものをそのまま出す**
 * （画面に一覧を作らない = 二重管理の禁止）。
 */

/** 回答欄へ入れる入れ方（黙って上書きしない）。 */
const DRAFT_REPLACE = 'replace'
const DRAFT_APPEND = 'append'

/**
 * 対話の途中経過（バックエンドの binding.RespondAIStreamEvent）。
 *
 * Wails のバインディング生成はメソッドの引数・戻り値の型だけを出力するため、
 * イベントで届く型はここで定義する（フィールド名はバックエンドの JSON タグと一致させる）。
 */
type RespondAIEvent = {
    kind: 'text' | 'done' | 'error'
    text?: string
    message?: string
    interrupted?: boolean
}

/** シークレットキー方式の識別（画面には出さず分岐にだけ使う）。 */
const AUTH_SECRET_KEY = 'secret_key'
const AUTH_SIGNIN = 'chatgpt_signin'

export function RespondAIPane({
    questionID,
    canApplyToAnswer,
    onApplyDraft,
}: {
    /** いま表示している質問（移す先はこの 1 問だけ）。 */
    questionID: string
    /** この質問が自由記述欄を持つか（選択肢だけの質問へは入れない）。 */
    canApplyToAnswer: boolean
    /** 下書きを回答欄へ渡す。保存は回答欄側の通常の経路で行う。 */
    onApplyDraft: (draft: binding.RespondAnswerDraftView) => void
}) {
    const [view, setView] = useState<binding.RespondAIDialogueView | null>(null)
    const [providers, setProviders] = useState<binding.ProviderOption[]>([])
    const [providerID, setProviderID] = useState('')
    const [authMethod, setAuthMethod] = useState(AUTH_SECRET_KEY)
    const [scope, setScope] = useState<binding.RespondAIScopeView | null>(null)
    const [confirmed, setConfirmed] = useState(false)
    const [key, setKey] = useState('')
    const [input, setInput] = useState('')
    const [streaming, setStreaming] = useState('')
    const [error, setError] = useState('')
    const [busy, setBusy] = useState(false)

    const historyRef = useRef<HTMLDivElement | null>(null)
    const utterances = view?.utterances ?? []
    const follow = useFollowLatest(historyRef, [utterances.length, streaming])

    const reload = useCallback(() => {
        return Promise.resolve()
            .then(() => RespondAIDialogue())
            .then((next) => setView(next))
            .catch((err: unknown) => setError(errorText(err)))
    }, [])

    useEffect(() => {
        void reload()
        void Promise.resolve()
            .then(() => RespondAIProviders())
            .then((list) => {
                setProviders(list)
                setProviderID((current) => (current === '' && list.length > 0 ? list[0].id : current))
            })
            .catch((err: unknown) => setError(errorText(err)))
    }, [reload])

    // 選んだプロバイダが変わったら、送信範囲の事前表示と確認をやり直す（送信先ごとに同意を取るため）。
    useEffect(() => {
        if (providerID === '') {
            return
        }
        setConfirmed(false)
        const options = providers.find((p) => p.id === providerID)?.authMethods ?? []
        setAuthMethod(options.find((m) => m.default)?.id ?? AUTH_SECRET_KEY)
        void Promise.resolve()
            .then(() => RespondAIScope(providerID))
            .then((next) => setScope(next))
            .catch((err: unknown) => setError(errorText(err)))
    }, [providerID, providers])

    // 応答の途中経過（バックエンドからのイベント）。届いた分をその場に出す。
    useEffect(() => {
        const off = EventsOn('respondai:event', (ev: RespondAIEvent) => {
            if (ev.kind === 'text') {
                setStreaming((prev) => prev + (ev.text ?? ''))
                return
            }
            setStreaming('')
        })
        return () => off()
    }, [])

    const enable = () => {
        setBusy(true)
        setError('')
        return Promise.resolve()
            .then(() => EnableRespondAI(providerID, authMethod, confirmed))
            .then((next) => setView(next))
            .catch((err: unknown) => setError(errorText(err)))
            .finally(() => setBusy(false))
    }

    const registerKey = () => {
        setBusy(true)
        setError('')
        return Promise.resolve()
            .then(() => RegisterRespondAIKey(providerID, key, confirmed))
            .then((result) => {
                // キー本体は画面に残さない。
                setKey('')
                if (!result.ok) {
                    setError(result.detail ? result.detail : 'キーを確認できませんでした。')
                    return undefined
                }
                return enable()
            })
            .catch((err: unknown) => setError(errorText(err)))
            .finally(() => setBusy(false))
    }

    const send = () => {
        const body = input
        setBusy(true)
        setError('')
        setInput('')
        setStreaming('')
        follow()
        return Promise.resolve()
            .then(() => SendRespondAIMessage(body))
            .then((next) => setView(next))
            .catch((err: unknown) => {
                setError(errorText(err))
                // 送った内容は履歴に残っているため、画面の状態を取り直す。
                return reload()
            })
            .finally(() => {
                setStreaming('')
                setBusy(false)
            })
    }

    const apply = (utteranceID: string, mode: string) => {
        setError('')
        return Promise.resolve()
            .then(() => RespondAIAnswerDraft(questionID, utteranceID, mode))
            .then((draft) => onApplyDraft(draft))
            .catch((err: unknown) => setError(errorText(err)))
    }

    const disable = () => {
        setError('')
        return Promise.resolve()
            .then(() => DisableRespondAI())
            .then((next) => setView(next))
            .catch((err: unknown) => setError(errorText(err)))
    }

    /*
     * 状態が届くまでは何も名乗らない。届く前に「未有効」を描くと、
     * 有効なのに一瞬だけ有効化の画面が見える（CodexSignIn と同じ理由）。
     */
    if (!view) {
        return (
            <div className="rw-respond-ai">
                <p className="rw-respond-ai__hint">読み込んでいます…</p>
            </div>
        )
    }

    const provider = providers.find((p) => p.id === providerID)
    const authMethods = provider?.authMethods ?? []
    const signIn = authMethod === AUTH_SIGNIN

    return (
        <div className="rw-respond-ai">
            <header className="rw-respond-ai__head">
                <h2 className="rw-respond-ai__title">AI と相談する</h2>
            </header>

            {view.enabled ? (
                <>
                    <div className="rw-respond-ai__history" ref={historyRef} aria-label="相談のやり取り">
                        {utterances.length === 0 && streaming === '' ? (
                            <p className="rw-respond-ai__hint">
                                いまの質問について、分からないところを書いてみてください。
                                回答するかどうかを決めるのはご自身です。
                            </p>
                        ) : null}
                        {utterances.map((u) => (
                            <div key={u.id} className="rw-respond-ai__turn">
                                <Utterance
                                    speaker={u.isAgent ? 'ai' : 'human'}
                                    name={u.speakerLabel}
                                    timestamp={u.at}
                                    body={u.body}
                                    interrupted={u.interrupted}
                                />
                                {u.canApply ? (
                                    <div className="rw-respond-ai__apply">
                                        <Button
                                            onClick={() => void apply(u.id, DRAFT_REPLACE)}
                                            disabledReason={
                                                canApplyToAnswer
                                                    ? undefined
                                                    : 'この質問は選択肢から選ぶ形式です。選ぶのはご自身で行ってください。'
                                            }
                                        >
                                            この内容を回答欄へ入れる
                                        </Button>
                                        <Button
                                            onClick={() => void apply(u.id, DRAFT_APPEND)}
                                            disabledReason={
                                                canApplyToAnswer
                                                    ? undefined
                                                    : 'この質問は選択肢から選ぶ形式です。選ぶのはご自身で行ってください。'
                                            }
                                        >
                                            回答欄の末尾へ足す
                                        </Button>
                                    </div>
                                ) : null}
                            </div>
                        ))}
                        {streaming !== '' ? (
                            <Utterance speaker="ai" name="AI" timestamp="受信中" body={streaming} />
                        ) : null}
                    </div>

                    <div className="rw-respond-ai__compose">
                        <label className="rw-respond-ai__field">
                            <span>相談したいこと</span>
                            <textarea
                                value={input}
                                onChange={(e) => setInput(e.target.value)}
                                rows={2}
                                aria-label="相談したいこと"
                            />
                        </label>
                        <div className="rw-respond-ai__actions">
                            <Button
                                onClick={() => void send()}
                                disabledReason={
                                    view.running
                                        ? '応答を受け取っています。'
                                        : input.trim() === ''
                                          ? '相談したい内容を入力してください。'
                                          : busy
                                            ? '送信しています。'
                                            : undefined
                                }
                            >
                                送信
                            </Button>
                            {busy || view.running ? (
                                <Button variant="secondary" onClick={() => void CancelRespondAIMessage()}>
                                    中断
                                </Button>
                            ) : null}
                            <Button variant="secondary" onClick={() => void disable()}>
                                AI との相談をやめる
                            </Button>
                        </div>
                    </div>
                </>
            ) : (
                <div className="rw-respond-ai__setup">
                    <p className="rw-respond-ai__hint">
                        ご自身の AI のキー、または ChatGPT のアカウントで、質問の内容を AI に相談できます。
                        使わなくても、すべての質問に回答して返送できます。
                    </p>

                    <label className="rw-respond-ai__field">
                        <span>使う AI</span>
                        <select value={providerID} onChange={(e) => setProviderID(e.target.value)} aria-label="使う AI">
                            {providers.map((p) => (
                                <option key={p.id} value={p.id}>
                                    {p.displayName}
                                </option>
                            ))}
                        </select>
                    </label>

                    {authMethods.length > 1 ? (
                        <fieldset className="rw-respond-ai__fieldset">
                            <legend>つなぎ方</legend>
                            {authMethods.map((m) => (
                                <div key={m.id}>
                                    {/* 説明はラベルの外に置く（読み上げ名を方式の名前だけに保つ）。 */}
                                    <label className="rw-respond-ai__choice">
                                        <input
                                            type="radio"
                                            name="respond-ai-auth"
                                            checked={authMethod === m.id}
                                            onChange={() => setAuthMethod(m.id)}
                                        />
                                        <span>{m.label}</span>
                                    </label>
                                    {m.description ? (
                                        <p className="rw-respond-ai__hint">{m.description}</p>
                                    ) : null}
                                </div>
                            ))}
                        </fieldset>
                    ) : null}

                    {scope ? (
                        <section className="rw-respond-ai__scope" aria-label="AI に送られる内容">
                            <h3 className="rw-respond-ai__subtitle">AI に送られる内容</h3>
                            <p className="rw-respond-ai__hint">{scope.noRecordNotice}</p>
                            <p className="rw-respond-ai__scope-label">送るもの</p>
                            <ul>
                                {scope.sent.map((line) => (
                                    <li key={line}>{line}</li>
                                ))}
                            </ul>
                            <p className="rw-respond-ai__scope-label">送らないもの</p>
                            <ul>
                                {scope.notSent.map((line) => (
                                    <li key={line}>{line}</li>
                                ))}
                            </ul>
                            {scope.notice ? <p className="rw-respond-ai__hint">{scope.notice}</p> : null}
                            {scope.policyUrl ? (
                                <p className="rw-respond-ai__hint">
                                    データ利用ポリシー: <span className="rw-mono">{scope.policyUrl}</span>
                                </p>
                            ) : null}
                            <label className="rw-respond-ai__choice">
                                <input
                                    type="checkbox"
                                    checked={confirmed}
                                    onChange={(e) => setConfirmed(e.target.checked)}
                                />
                                <span>上の内容を確認しました</span>
                            </label>
                        </section>
                    ) : null}

                    {signIn && !confirmed ? (
                        <p className="rw-respond-ai__hint">
                            上の内容を確認すると、サインインへ進めます。
                        </p>
                    ) : null}
                    {signIn && confirmed ? (
                        <CodexSignIn
                            label=""
                            methodDescription={authMethods.find((m) => m.id === authMethod)?.description}
                            providerNotice={scope?.notice}
                            policyUrl={scope?.policyUrl}
                            start={() => StartRespondAISignIn(confirmed)}
                            onChange={(state) => {
                                if (state === 'signed_in') {
                                    void enable()
                                }
                            }}
                        />
                    ) : null}
                    {signIn ? null : (
                        <>
                            <label className="rw-respond-ai__field">
                                <span>シークレットキー</span>
                                <input
                                    type="password"
                                    value={key}
                                    onChange={(e) => setKey(e.target.value)}
                                    autoComplete="off"
                                    aria-label="シークレットキー"
                                />
                            </label>
                            <div className="rw-respond-ai__actions">
                                <Button
                                    onClick={() => void registerKey()}
                                    disabledReason={
                                        !confirmed
                                            ? '送られる内容を確認してからお進みください。'
                                            : key.trim() === ''
                                              ? 'シークレットキーを入力してください。'
                                              : busy
                                                ? '確認しています。'
                                                : undefined
                                    }
                                >
                                    キーを登録して相談をはじめる
                                </Button>
                            </div>
                        </>
                    )}
                </div>
            )}

            {error ? (
                <p className="rw-respond-ai__error" role="alert">
                    {error}
                </p>
            ) : null}
        </div>
    )
}
