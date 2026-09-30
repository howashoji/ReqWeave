import {useCallback, useEffect, useState} from 'react'
import {CompleteSetup, Models, RegisterKey, SetupState} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Button, CodexSignIn, Wizard, errorText} from '../ui'
import './SetupWizard.css'

/**
 * 初期設定ウィザード。
 *
 * プロバイダ（と認証方式）→ キー登録と疎通確認 / サインイン → モデル → エフォート → 作業者名 の順次進行。
 * 担当者向け統制表示（完成度・消費量・モデル等）は出さない（ウィザード・回答系の画面では出さない）。
 * キー本体はバックエンドへ渡すだけで、画面へ再表示する経路を持たない（画面からキーが読み取られないように）。
 *
 * **認証方式の選択は、2 つ以上の方式を持つプロバイダでだけ出す**（選べないものを選ばせない）。
 * 方式ごとのポリシーと注意喚起はバックエンドが文言を持ち、画面は選択中の方式のものを示す。
 * ChatGPT のアカウントでのサインインを選んだときは、キー入力に代えてサインインの操作を出し、
 * **サインインの成功をもって疎通確認の成功とする**。
 */

const TOTAL_STEPS = 5

/** シークレットキー方式（バックエンドの既定）。 */
const AUTH_SECRET_KEY = 'secret_key'
/** ChatGPT のアカウントでのサインイン。 */
const AUTH_CHATGPT_SIGNIN = 'chatgpt_signin'

function stepTitle(step: number, signIn: boolean): string {
    switch (step) {
        case 1:
            return 'AI プロバイダを選ぶ'
        case 2:
            return signIn ? 'ChatGPT のアカウントでサインインする' : 'シークレットキーを登録する'
        case 3:
            return 'モデルを選ぶ'
        case 4:
            return 'エフォートを選ぶ'
        default:
            return '作業者名を登録する'
    }
}

type Props = {
    /** 初期設定が完了したときに呼ばれる（プロジェクト一覧へ進む） */
    onComplete: () => void
}

export function SetupWizard({onComplete}: Props) {
    const [state, setState] = useState<binding.SetupState | null>(null)
    const [loadError, setLoadError] = useState('')
    const [step, setStep] = useState(1)

    const [providerId, setProviderId] = useState('')
    // 認証方式（未選択はシークレットキー方式）。選択を出すのは 2 つ以上持つプロバイダだけ。
    const [authMethod, setAuthMethod] = useState(AUTH_SECRET_KEY)
    // サインインの状態。成功をもって疎通確認の成功とする。
    const [signInState, setSignInState] = useState('')
    const [keyInput, setKeyInput] = useState('')
    const [verifying, setVerifying] = useState(false)
    const [verified, setVerified] = useState(false)
    const [verifyMessage, setVerifyMessage] = useState('')
    const [verifyTone, setVerifyTone] = useState<'accent' | 'danger'>('accent')

    const [models, setModels] = useState<binding.ModelList | null>(null)
    const [modelId, setModelId] = useState('')
    const [modelsError, setModelsError] = useState('')

    const [effort, setEffort] = useState('')
    const [authorId, setAuthorId] = useState('')
    const [displayName, setDisplayName] = useState('')
    const [saveError, setSaveError] = useState('')

    useEffect(() => {
        SetupState()
            .then((s) => {
                setState(s)
                setDisplayName(s.displayName || s.suggestedDisplayName || '')
                setAuthorId(s.authorId || '')
                const fallback = s.efforts.find((e) => e.default)
                setEffort(fallback ? fallback.id : '')
            })
            .catch((err: unknown) => setLoadError(errorText(err)))
    }, [])

    const loadModels = useCallback(async () => {
        setModelsError('')
        try {
            const list = await Models(providerId, '', authMethod)
            setModels(list)
            const recommended = list.models.find((m) => m.recommended) ?? list.models[0]
            setModelId((current) => current || (recommended ? recommended.id : ''))
        } catch (err: unknown) {
            setModelsError(errorText(err))
        }
    }, [providerId, authMethod])

    const verify = useCallback(async () => {
        setVerifying(true)
        setVerifyMessage('')
        try {
            const result = await RegisterKey(providerId, '', keyInput)
            setVerified(result.ok)
            setVerifyTone(result.ok ? 'accent' : 'danger')
            setVerifyMessage(
                result.ok ? '疎通確認に成功しました。キーは OS の安全な保管領域に保存しました。' : `${result.reason}: ${result.detail}`,
            )
            if (result.ok) {
                setKeyInput('')
            }
        } catch (err: unknown) {
            setVerified(false)
            setVerifyTone('danger')
            setVerifyMessage(errorText(err))
        } finally {
            setVerifying(false)
        }
    }, [providerId, keyInput])

    const finish = useCallback(async () => {
        setSaveError('')
        try {
            await CompleteSetup({
                providerId,
                label: '',
                model: modelId,
                effort,
                authMethod,
                authorId,
                displayName,
            } as binding.SetupRequest)
            onComplete()
        } catch (err: unknown) {
            setSaveError(errorText(err))
        }
    }, [providerId, modelId, effort, authMethod, authorId, displayName, onComplete])

    if (loadError) {
        return (
            <div className="rw-wizard">
                <main className="rw-wizard__body">
                    <p className="rw-setup__result" data-tone="danger">
                        設定を読み込めませんでした。アプリを再起動してください。
                    </p>
                </main>
            </div>
        )
    }
    if (!state) {
        return (
            <div className="rw-wizard">
                <main className="rw-wizard__body">
                    <p className="rw-setup__lead">読み込んでいます…</p>
                </main>
            </div>
        )
    }

    const selectedProvider = state.providers.find((p) => p.id === providerId)
    // 認証方式を 2 つ以上持つプロバイダでだけ選択を出す。
    const authMethods = selectedProvider?.authMethods ?? []
    const showAuthMethods = authMethods.length > 1
    const usesSignIn = authMethod === AUTH_CHATGPT_SIGNIN
    const selectedMethod = authMethods.find((m) => m.id === authMethod)

    const nextDisabledReason = (() => {
        switch (step) {
            case 1:
                return providerId ? undefined : 'AI プロバイダを選んでください。'
            case 2:
                if (usesSignIn) {
                    return signInState === 'signed_in'
                        ? undefined
                        : 'ChatGPT のアカウントでサインインしてください。'
                }
                return verified ? undefined : 'キーを登録して疎通確認を成功させてください。'
            case 3:
                return modelId ? undefined : 'モデルを選んでください。'
            case 4:
                return effort ? undefined : 'エフォートを選んでください。'
            case 5:
                if (!authorId.trim()) {
                    return 'メールアドレス（利用者 ID）を入力してください。'
                }
                if (!displayName.trim()) {
                    return '表示名を入力してください。'
                }
                return undefined
            default:
                return undefined
        }
    })()

    const goNext = () => {
        if (step === TOTAL_STEPS) {
            void finish()
            return
        }
        if (step === 2) {
            void loadModels()
        }
        setStep(step + 1)
    }

    return (
        <Wizard
            title={stepTitle(step, usesSignIn)}
            step={step}
            total={TOTAL_STEPS}
            onBack={step > 1 ? () => setStep(step - 1) : undefined}
            onNext={goNext}
            nextLabel={step === TOTAL_STEPS ? '設定を完了する' : '次へ'}
            nextDisabledReason={nextDisabledReason}
            persistenceNotice="キーは登録操作でのみ OS の安全な保管領域へ保存します。ほかの入力は「設定を完了する」で保存されます。"
        >
            {step === 1 ? (
                <>
                    <p className="rw-setup__lead">対話に使う AI プロバイダを選んでください。</p>
                    <p className="rw-setup__notice">{state.dataPolicyNotice}</p>
                    <fieldset className="rw-setup__choices">
                        {state.providers.map((p) => (
                            <label className="rw-setup__choice" key={p.id} data-selected={p.id === providerId}>
                                <input
                                    type="radio"
                                    name="provider"
                                    value={p.id}
                                    checked={p.id === providerId}
                                    onChange={() => {
                                        setProviderId(p.id)
                                        setVerified(false)
                                        setVerifyMessage('')
                                        setModels(null)
                                        setModelId('')
                                        // 認証方式はプロバイダごと。既定を持つ方式があればそれを選ぶ。
                                        const methods = p.authMethods ?? []
                                        const fallback = methods.find((m) => m.default) ?? methods[0]
                                        setAuthMethod(fallback ? fallback.id : AUTH_SECRET_KEY)
                                        setSignInState('')
                                    }}
                                />
                                <span className="rw-setup__choice-label">
                                    {p.displayName}
                                    <span className="rw-setup__choice-detail">
                                        データ利用ポリシー: {p.policyUrl}
                                    </span>
                                    {p.notice ? (
                                        <span className="rw-setup__choice-detail">{p.notice}</span>
                                    ) : null}
                                </span>
                            </label>
                        ))}
                    </fieldset>

                    {/*
                      * 認証方式の選択。
                      * **2 つ以上持つプロバイダでだけ出す**（他のプロバイダでは表示しない）。
                      * 方式ごとのポリシーと注意喚起はバックエンドの説明文をそのまま示す。
                      */}
                    {showAuthMethods ? (
                        <fieldset className="rw-setup__choices" aria-label="認証方式">
                            <legend className="rw-setup__legend">認証方式を選んでください。</legend>
                            {authMethods.map((m) => (
                                <label
                                    className="rw-setup__choice"
                                    key={m.id}
                                    data-selected={m.id === authMethod}
                                >
                                    <input
                                        type="radio"
                                        name="auth-method"
                                        value={m.id}
                                        checked={m.id === authMethod}
                                        onChange={() => {
                                            setAuthMethod(m.id)
                                            setVerified(false)
                                            setVerifyMessage('')
                                            setKeyInput('')
                                        }}
                                    />
                                    <span className="rw-setup__choice-label">
                                        {m.label}
                                        {m.default ? '（既定）' : ''}
                                        {m.description ? (
                                            <span className="rw-setup__choice-detail">{m.description}</span>
                                        ) : null}
                                    </span>
                                </label>
                            ))}
                        </fieldset>
                    ) : null}
                </>
            ) : null}

            {/*
              * サインイン方式ではキー入力欄を出さず、サインインの操作に置き換える。
              * 成功をもって疎通確認の成功とする。
              */}
            {step === 2 && usesSignIn ? (
                <>
                    <p className="rw-setup__lead">
                        {selectedProvider ? selectedProvider.displayName : ''} を ChatGPT のアカウントで使います。
                        シークレットキーは登録しません。
                    </p>
                    <CodexSignIn
                        label=""
                        methodDescription={selectedMethod?.description}
                        providerNotice={selectedProvider?.notice}
                        policyUrl={selectedProvider?.policyUrl}
                        onChange={setSignInState}
                    />
                </>
            ) : null}

            {step === 2 && !usesSignIn ? (
                <>
                    <p className="rw-setup__lead">
                        {selectedProvider ? selectedProvider.displayName : ''} のシークレットキーを登録します。
                        キーは OS の安全な保管領域にのみ保存し、画面へ再表示しません。
                    </p>
                    <div className="rw-setup__field">
                        <label className="rw-setup__label" htmlFor="setup-key">
                            シークレットキー
                        </label>
                        <input
                            id="setup-key"
                            className="rw-setup__input rw-setup__input--mono"
                            type="password"
                            autoComplete="off"
                            value={keyInput}
                            onChange={(e) => setKeyInput(e.target.value)}
                        />
                        <p className="rw-setup__hint">登録すると同時に疎通確認を行います。</p>
                    </div>
                    <div className="rw-setup__actions">
                        <Button
                            variant="secondary"
                            onClick={() => void verify()}
                            disabledReason={
                                verifying ? '疎通確認中です。' : keyInput.trim() ? undefined : 'キーを入力してください。'
                            }
                        >
                            登録して疎通確認
                        </Button>
                    </div>
                    {verifyMessage ? (
                        <p className="rw-setup__result" data-tone={verifyTone} role="status">
                            {verifyMessage}
                        </p>
                    ) : null}
                </>
            ) : null}

            {step === 3 ? (
                <>
                    <p className="rw-setup__lead">対話に使うモデルを選んでください。</p>
                    {models && models.fromKnownList ? (
                        <p className="rw-setup__result" data-tone="warn">
                            {models.notice}
                        </p>
                    ) : null}
                    {modelsError ? (
                        <p className="rw-setup__result" data-tone="danger">
                            モデル一覧を取得できませんでした。設定を確認して再実行してください。
                        </p>
                    ) : null}
                    <fieldset className="rw-setup__choices">
                        {(models ? models.models : []).map((m) => (
                            <label className="rw-setup__choice" key={m.id} data-selected={m.id === modelId}>
                                <input
                                    type="radio"
                                    name="model"
                                    value={m.id}
                                    checked={m.id === modelId}
                                    onChange={() => setModelId(m.id)}
                                />
                                <span className="rw-setup__choice-label">
                                    {m.displayName}
                                    {m.recommended ? '（推奨）' : ''}
                                    <span className="rw-setup__choice-detail rw-setup__meta">{m.id}</span>
                                </span>
                            </label>
                        ))}
                    </fieldset>
                </>
            ) : null}

            {step === 4 ? (
                <>
                    <p className="rw-setup__lead">
                        掘り下げの深さとトークン消費のバランスを選んでください。あとから設定画面で変更できます。
                    </p>
                    <fieldset className="rw-setup__choices">
                        {state.efforts.map((e) => (
                            <label className="rw-setup__choice" key={e.id} data-selected={e.id === effort}>
                                <input
                                    type="radio"
                                    name="effort"
                                    value={e.id}
                                    checked={e.id === effort}
                                    onChange={() => setEffort(e.id)}
                                />
                                <span className="rw-setup__choice-label">
                                    {e.label}
                                    {e.default ? '（既定）' : ''}
                                    <span className="rw-setup__choice-detail">{e.description}</span>
                                </span>
                            </label>
                        ))}
                    </fieldset>
                </>
            ) : null}

            {step === 5 ? (
                <>
                    <p className="rw-setup__lead">
                        共有プロジェクトで誰の作業かを記録するために登録します。メールアドレスは登録後に変更できません
                        （訂正はプロジェクトのオーナーが行います）。
                    </p>
                    <div className="rw-setup__field">
                        <label className="rw-setup__label" htmlFor="setup-author">
                            メールアドレス（利用者 ID）
                        </label>
                        <input
                            id="setup-author"
                            className="rw-setup__input rw-setup__input--mono"
                            type="text"
                            autoComplete="off"
                            value={authorId}
                            onChange={(e) => setAuthorId(e.target.value)}
                        />
                        <p className="rw-setup__hint">会社で使っているメールアドレスを入力してください。</p>
                    </div>
                    <div className="rw-setup__field">
                        <label className="rw-setup__label" htmlFor="setup-display-name">
                            表示名
                        </label>
                        <input
                            id="setup-display-name"
                            className="rw-setup__input"
                            type="text"
                            value={displayName}
                            onChange={(e) => setDisplayName(e.target.value)}
                        />
                        <p className="rw-setup__hint">表示名はあとから変更できます。</p>
                    </div>
                    {saveError ? (
                        <p className="rw-setup__result" data-tone="danger" role="status">
                            設定を保存できませんでした。入力内容を確認してください。
                        </p>
                    ) : null}
                </>
            ) : null}
        </Wizard>
    )
}
