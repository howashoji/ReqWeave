import {useCallback, useEffect, useRef, useState} from 'react'
import {
    AppInfo,
    DeleteKey,
    DiagnosticsPreview,
    ExportDiagnostics,
    RegisterKey,
    SaveProviderConfig,
    SetAITimeouts,
    SetDisplayName,
    SetTheme,
    SettingsView,
    SignOutCodex,
    Projects,
} from '../../wailsjs/go/binding/API'
import {applog, binding} from '../../wailsjs/go/models'
import {AppShell, Button, Chip, CodexSignIn, Overlay, PlanUsage, errorText} from '../ui'
import {Intro} from './Intro'
import {applyTheme, currentTheme, type Theme} from '../theme/theme'
import './Settings.css'

/**
 * 設定。
 *
 * プロバイダ・モデル・エフォート・キーの変更、AI 呼び出しの待ち時間、テーマ切替、作業者名の表示・変更、
 * アプリの版番号表示（更新確認が「現行版」として使う値）。
 * キーはマスク表示のみで平文の再表示機能を持たない（画面からキーが読み取られないように）。
 * プロジェクト横断の画面のため統制表示は出さない。
 *
 * **同期先の認証情報**はプロジェクトごと・端末ごとのため、
 * 同期先を設定した共同プロジェクトを選んで専用画面へ移る導線だけを置く。
 *
 * **紹介スライドの再表示**はこの画面から開く。
 * 再表示では初回の自動表示の記録（intro_shown）を変えない。
 */

type Tone = 'info' | 'accent' | 'danger' | 'warn'

/** シークレットキー方式（バックエンドの既定）。 */
const AUTH_SECRET_KEY = 'secret_key'
/** ChatGPT のアカウントでのサインイン。 */
const AUTH_CHATGPT_SIGNIN = 'chatgpt_signin'

export function Settings({
    onBack,
    onOpenSyncCredentials,
}: {
    onBack: () => void
    /** onOpenSyncCredentials は同期先の認証情報へ移る（対象のプロジェクトを渡す）。 */
    onOpenSyncCredentials?: (projectId: string, targetSystemName: string) => void
}) {
    const [view, setView] = useState<binding.SettingsView | null>(null)
    const [appInfo, setAppInfo] = useState<binding.AppInfo | null>(null)
    const [displayName, setDisplayName] = useState('')
    const [model, setModel] = useState('')
    const [effort, setEffort] = useState('')
    // 認証方式。切り替えは「設定を保存」で確定する。
    const [authMethod, setAuthMethod] = useState(AUTH_SECRET_KEY)
    // サインアウトの確認（ネイティブの confirm を使わず、アプリ内のダイアログで受ける）。
    const [confirmSignOut, setConfirmSignOut] = useState(false)
    /*
     * 認証方式を画面で選び直したか。**選び直した後の再読込で選択を巻き戻さない**ため持つ
     * （サインインの状態が変わるたびに設定を読み直すので、保存前の選択が消えると切り替えられない）。
     */
    const methodTouched = useRef(false)
    const [keyInput, setKeyInput] = useState('')
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<Tone>('info')
    const [theme, setTheme] = useState<Theme>(() => currentTheme())
    // 紹介スライドの再表示。記録（intro_shown）は変えない。
    const [showIntro, setShowIntro] = useState(false)
    const [connectSeconds, setConnectSeconds] = useState('')
    const [responseSeconds, setResponseSeconds] = useState('')
    // 同期先を設定した共同プロジェクト（同期先の認証情報への導線）。
    const [syncProjects, setSyncProjects] = useState<binding.ProjectSummary[]>([])
    // 診断情報の書き出し。確認するまで書き出せない。
    const [diagnostic, setDiagnostic] = useState<applog.Diagnostic | null>(null)

    const reload = useCallback(async () => {
        try {
            const next = await SettingsView()
            setView(next)
            setDisplayName(next.displayName)
            if (next.theme === 'dark' || next.theme === 'light') {
                setTheme(next.theme)
                applyTheme(next.theme)
            }
            setConnectSeconds(String(next.aiTimeouts.connectSeconds))
            setResponseSeconds(String(next.aiTimeouts.responseSeconds))
            const current = next.providers.find((p) => p.isDefault) ?? next.providers[0]
            if (current) {
                setModel(current.model)
                setEffort(current.effort)
                if (!methodTouched.current) {
                    setAuthMethod(current.authMethod ? current.authMethod : AUTH_SECRET_KEY)
                }
            }
        } catch (err: unknown) {
            setTone('danger')
            setMessage('設定を読み込めませんでした。アプリを再起動してください。')
        }
    }, [])

    useEffect(() => {
        void reload()
    }, [reload])

    // 同期先を設定した共同プロジェクト（同期先の認証情報への導線）。
    // 取得できない場合も設定画面の他の機能を止めない。
    useEffect(() => {
        Promise.resolve()
            .then(() => Projects())
            .then((list) => setSyncProjects(list.filter((p) => p.syncConfigured && p.projectId)))
            .catch(() => setSyncProjects([]))
    }, [])

    // アプリの版番号（正本 = internal/appversion/VERSION）。
    // 取得できない場合も設定画面の他の機能を止めない（版は「—」表示に倒す）。
    useEffect(() => {
        Promise.resolve()
            .then(() => AppInfo())
            .then(setAppInfo)
            .catch(() => setAppInfo(null))
    }, [])

    const current = view?.providers.find((p) => p.isDefault) ?? view?.providers[0]
    /*
     * 認証方式の選択肢はプロバイダ一覧定義（バックエンド）が正本。
     * **2 つ以上持つプロバイダでだけ切り替えを出す**（他のプロバイダでは表示しない）。
     */
    const providerOption = view?.options?.find((o) => o.id === current?.providerId)
    const authMethods = providerOption?.authMethods ?? []
    const showAuthMethods = authMethods.length > 1
    const selectedMethod = authMethods.find((m) => m.id === authMethod)
    /** 画面で選んでいる方式（保存前）。キー欄の出し分けはこちらに従う。 */
    const usesSignIn = authMethod === AUTH_CHATGPT_SIGNIN
    /** 保存済みの方式。サインアウトの導線は保存済みでサインイン方式のときにも出す。 */
    const storedUsesSignIn = current?.authMethod === AUTH_CHATGPT_SIGNIN

    const saveConfig = useCallback(async () => {
        if (!current) {
            return
        }
        try {
            await SaveProviderConfig({
                label: current.label,
                providerId: current.providerId,
                model,
                effort,
                authMethod,
                makeDefault: true,
            } as binding.UpdateProviderRequest)
            methodTouched.current = false // 保存後は保存値が正
            setTone('accent')
            setMessage('設定を保存しました。以後の AI 呼び出しから適用されます。')
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [current, model, effort, authMethod, reload])

    /*
     * サインアウト。確認操作を経てから行う。
     * サインアウトすると Codex App Server が保管している認証情報が OS のセキュアストレージから消え、
     * AI 呼び出しを伴う操作は「キー未登録」と同じ扱いでブロックされる（操作は隠さず、無効化して理由を示す）。
     */
    const signOut = useCallback(async () => {
        if (!current) {
            return
        }
        setConfirmSignOut(false)
        try {
            await SignOutCodex(current.label)
            methodTouched.current = false // 保存値（＝サインアウト後の状態）を読み直す
            setTone('warn')
            setMessage(
                'サインアウトしました。AI を使う操作は、もう一度サインインするまで実行できません。',
            )
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [current, reload])

    const replaceKey = useCallback(async () => {
        if (!current) {
            return
        }
        try {
            const result = await RegisterKey(current.providerId, current.label, keyInput)
            if (result.ok) {
                setKeyInput('')
                setTone('accent')
                setMessage('キーを更新しました。疎通確認に成功しています。')
            } else {
                setTone('danger')
                setMessage(`${result.reason}: ${result.detail}（変更前の設定はそのままです）`)
            }
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [current, keyInput, reload])

    const removeKey = useCallback(async () => {
        if (!current) {
            return
        }
        try {
            await DeleteKey(current.providerId, current.label)
            setTone('warn')
            setMessage('キーを削除しました。AI を使う操作は再登録するまで実行できません。')
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [current, reload])

    // 診断情報の書き出し。
    // 書き出す前に内容を提示し、確認操作を経てから保存する（外へ出す前に中身を見せる様式）。
    // 外部へは送らない（提供者のサーバへ送る仕組みは持たない）。
    const loadDiagnostics = useCallback(async () => {
        try {
            setDiagnostic(await DiagnosticsPreview())
            setTone('info')
            setMessage('書き出す内容を表示しました。内容を確認してから保存してください。')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const exportDiagnostics = useCallback(async () => {
        try {
            const saved = await ExportDiagnostics()
            if (!saved) {
                return
            }
            setTone('accent')
            setMessage(`診断情報を保存しました: ${saved}`)
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const saveName = useCallback(async () => {
        try {
            await SetDisplayName(displayName)
            setTone('accent')
            setMessage('表示名を変更しました。')
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [displayName, reload])

    // AI 呼び出しのタイムアウトは端末ごとのアプリ設定へ保存する（プロジェクトデータには入れない）。
    // 許容範囲の判定はバックエンド（範囲の正本）に任せ、画面は範囲を表示するだけにする。
    const saveTimeouts = useCallback(async () => {
        try {
            await SetAITimeouts(Number(connectSeconds), Number(responseSeconds))
            setTone('accent')
            setMessage('AI 呼び出しの待ち時間を変更しました。次の AI 呼び出しから適用されます。')
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [connectSeconds, responseSeconds, reload])

    // テーマは端末ごとのアプリ設定へ保存する（プロジェクトデータには入れない）。
    const toggleTheme = useCallback(async () => {
        const nextTheme: Theme = theme === 'dark' ? 'light' : 'dark'
        applyTheme(nextTheme)
        setTheme(nextTheme)
        try {
            await SetTheme(nextTheme)
        } catch (err: unknown) {
            // 保存できなければ表示を戻す（画面と保存内容を食い違わせない）
            applyTheme(theme)
            setTheme(theme)
            setTone('danger')
            setMessage('テーマを保存できませんでした。もう一度お試しください。')
        }
    }, [theme])

    // 紹介スライドの再表示。閉じると呼び出し元＝この画面へ戻り、記録は変えない。
    if (showIntro) {
        return <Intro reviewing onFinish={() => setShowIntro(false)} />
    }

    return (
        <AppShell
            scope="global"
            breadcrumb={['reqweave', '設定']}
            nav={
                <Button onClick={onBack} tooltip="設定を閉じて、プロジェクトの一覧へ戻ります。">
                    プロジェクト一覧へ戻る
                </Button>
            }
        >
            {view && !view.aiReady ? (
                <p className="rw-settings__message" data-tone="warn" role="status">
                    {view.aiBlockedReason}
                </p>
            ) : null}

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">AI プロバイダ</h2>
                {current ? (
                    <>
                        <div className="rw-settings__row">
                            <span className="rw-settings__label">プロバイダ</span>
                            <span className="rw-settings__value">{current.providerLabel}</span>
                        </div>
                        <div className="rw-settings__row">
                            <span className="rw-settings__label">データ利用ポリシー</span>
                            <span className="rw-settings__value rw-settings__value--mono">{current.policyUrl}</span>
                        </div>
                        {/*
                          * 認証方式。
                          * 2 つ以上持つプロバイダでだけ切り替えを出し、それ以外はラベルの表示もしない。
                          */}
                        {showAuthMethods ? (
                            <>
                                <div className="rw-settings__row">
                                    <label className="rw-settings__label" htmlFor="settings-auth-method">
                                        認証方式
                                    </label>
                                    <select
                                        id="settings-auth-method"
                                        className="rw-settings__select"
                                        value={authMethod}
                                        onChange={(e) => {
                                            methodTouched.current = true
                                            setAuthMethod(e.target.value)
                                        }}
                                    >
                                        {authMethods.map((m) => (
                                            <option key={m.id} value={m.id}>
                                                {m.label}
                                            </option>
                                        ))}
                                    </select>
                                </div>
                                {selectedMethod?.description ? (
                                    <p className="rw-settings__hint">{selectedMethod.description}</p>
                                ) : null}
                                <p className="rw-settings__hint">
                                    現在の設定: {current.authMethodLabel}。
                                    認証方式を切り替えても、サインインしたままにできます。
                                    Codex App Server が保管している認証情報を消すときは、別途サインアウトしてください。
                                </p>
                            </>
                        ) : null}
                        <div className="rw-settings__row">
                            <label className="rw-settings__label" htmlFor="settings-model">
                                モデル
                            </label>
                            <input
                                id="settings-model"
                                className="rw-settings__input"
                                type="text"
                                value={model}
                                onChange={(e) => setModel(e.target.value)}
                            />
                        </div>
                        <div className="rw-settings__row">
                            <label className="rw-settings__label" htmlFor="settings-effort">
                                エフォート
                            </label>
                            <select
                                id="settings-effort"
                                className="rw-settings__select"
                                value={effort}
                                onChange={(e) => setEffort(e.target.value)}
                            >
                                {(view ? view.efforts : []).map((e) => (
                                    <option key={e.id} value={e.id}>
                                        {e.label}: {e.description}
                                    </option>
                                ))}
                            </select>
                        </div>
                        <div className="rw-settings__actions">
                            <Button variant="primary" onClick={() => void saveConfig()}>
                                設定を保存
                            </Button>
                        </div>
                        <p className="rw-settings__hint">{view?.dataPolicyNotice}</p>
                        {current.notice ? <p className="rw-settings__hint">{current.notice}</p> : null}
                    </>
                ) : (
                    <p className="rw-settings__value">プロバイダが未設定です。</p>
                )}
            </section>

            {/*
              * ChatGPT のアカウント。
              * 画面で選んでいる方式か、保存済みの方式のどちらかがサインイン方式のときに出す
              * （切り替えの前にサインインできる／切り替えた後もサインアウトできる）。
              */}
            {usesSignIn || storedUsesSignIn ? (
                <section className="rw-settings__section">
                    <h2 className="rw-settings__title">ChatGPT のアカウント</h2>
                    <CodexSignIn
                        label={current ? current.label : ''}
                        methodDescription={selectedMethod?.description}
                        providerNotice={current?.notice}
                        policyUrl={current?.policyUrl}
                        onChange={() => void reload()}
                    />
                    <div className="rw-settings__actions">
                        <Button onClick={() => setConfirmSignOut(true)}>サインアウトする</Button>
                    </div>
                    <p className="rw-settings__hint">
                        サインアウトすると、Codex App Server が保管している認証情報が OS の安全な保管領域から消えます。
                        参照・エクスポートなど AI を呼ばない操作は、そのまま使えます。
                    </p>
                    <PlanUsage label={current ? current.label : ''} />
                </section>
            ) : null}

            {confirmSignOut ? (
                <Overlay label="サインアウトの確認" variant="modal" onClose={() => setConfirmSignOut(false)}>
                    <section className="rw-settings__confirm">
                        <h3 className="rw-settings__title">ChatGPT のアカウントからサインアウトします</h3>
                        <p className="rw-settings__value">
                            Codex App Server が保管している認証情報が OS の安全な保管領域から消えます。
                            AI を使う操作は、もう一度サインインするまで実行できません。
                        </p>
                        <div className="rw-settings__actions">
                            <Button variant="primary" onClick={() => void signOut()}>
                                サインアウトする
                            </Button>
                            <Button variant="quiet" onClick={() => setConfirmSignOut(false)}>
                                やめる
                            </Button>
                        </div>
                    </section>
                </Overlay>
            ) : null}

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">シークレットキー</h2>
                <div className="rw-settings__row">
                    <span className="rw-settings__label">状態</span>
                    <span className="rw-settings__value">
                        <Chip tone={current?.keyState === '登録済み' ? 'accent' : 'danger'}>
                            {current ? current.keyState : '未設定'}
                        </Chip>{' '}
                        <span className="rw-settings__value--mono">{current ? current.keyMasked : '—'}</span>
                    </span>
                </div>
                {/*
                  * サインイン方式ではキーを使わないため、入力欄と更新・削除の操作を出さない
                  * （状態欄はバックエンドが「—」を返す）。
                  */}
                {usesSignIn ? (
                    <p className="rw-settings__hint">
                        ChatGPT のアカウントでのサインインでは、シークレットキーを使いません。
                        キーを使うときは認証方式をシークレットキー方式へ切り替えてください。
                    </p>
                ) : (
                    <>
                        <div className="rw-settings__row">
                            <label className="rw-settings__label" htmlFor="settings-key">
                                キーを更新
                            </label>
                            <input
                                id="settings-key"
                                className="rw-settings__input rw-settings__value--mono"
                                type="password"
                                autoComplete="off"
                                value={keyInput}
                                onChange={(e) => setKeyInput(e.target.value)}
                            />
                        </div>
                        <div className="rw-settings__actions">
                            <Button
                                onClick={() => void replaceKey()}
                                disabledReason={keyInput.trim() ? undefined : '新しいキーを入力してください。'}
                            >
                                更新して疎通確認
                            </Button>
                            <Button
                                onClick={() => void removeKey()}
                                disabledReason={
                                    current?.keyState === '登録済み' ? undefined : 'キーは登録されていません。'
                                }
                            >
                                キーを削除
                            </Button>
                        </div>
                        <p className="rw-settings__hint">
                            キーは OS の安全な保管領域にのみ保存し、画面へ再表示しません。疎通確認に失敗した場合は
                            変更前の設定を保持します。
                        </p>
                    </>
                )}
            </section>

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">作業者名</h2>
                <div className="rw-settings__row">
                    <span className="rw-settings__label">メールアドレス（利用者 ID）</span>
                    <span className="rw-settings__value rw-settings__value--mono">{view ? view.authorId : ''}</span>
                </div>
                <div className="rw-settings__row">
                    <label className="rw-settings__label" htmlFor="settings-display-name">
                        表示名
                    </label>
                    <input
                        id="settings-display-name"
                        className="rw-settings__input"
                        type="text"
                        value={displayName}
                        onChange={(e) => setDisplayName(e.target.value)}
                    />
                </div>
                <div className="rw-settings__actions">
                    <Button
                        onClick={() => void saveName()}
                        disabledReason={displayName.trim() ? undefined : '表示名を入力してください。'}
                    >
                        表示名を保存
                    </Button>
                </div>
                <p className="rw-settings__hint">
                    メールアドレスは変更できません（訂正はプロジェクトのオーナーが行います）。
                </p>
            </section>

            {onOpenSyncCredentials ? (
                <section className="rw-settings__section">
                    <h2 className="rw-settings__title">同期先の認証情報</h2>
                    {syncProjects.length === 0 ? (
                        <p className="rw-settings__hint">
                            同期先を設定した共同プロジェクトがありません。認証情報はプロジェクトごと・この端末ごとに登録します。
                        </p>
                    ) : (
                        syncProjects.map((project) => (
                            <div className="rw-settings__row" key={project.path}>
                                <span className="rw-settings__label">{project.targetSystemName}</span>
                                <span className="rw-settings__value">
                                    {project.syncKindLabel}{' '}
                                    <Button
                                        onClick={() =>
                                            onOpenSyncCredentials(project.projectId, project.targetSystemName)
                                        }
                                    >
                                        認証情報を設定する
                                    </Button>
                                </span>
                            </div>
                        ))
                    )}
                    <p className="rw-settings__hint">
                        登録した認証情報は OS の安全な保管領域にのみ保存し、画面へ再表示しません。
                        共有フォルダの同期先では登録は要りません。
                    </p>
                </section>
            ) : null}

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">AI 呼び出し</h2>
                <div className="rw-settings__row">
                    <label className="rw-settings__label" htmlFor="rw-connect-timeout">
                        接続の待ち時間（秒）
                    </label>
                    <input
                        id="rw-connect-timeout"
                        className="rw-settings__input"
                        type="number"
                        inputMode="numeric"
                        min={view?.aiTimeouts.minConnectSeconds}
                        max={view?.aiTimeouts.maxConnectSeconds}
                        value={connectSeconds}
                        onChange={(e) => setConnectSeconds(e.target.value)}
                    />
                </div>
                <div className="rw-settings__row">
                    <label className="rw-settings__label" htmlFor="rw-response-timeout">
                        応答の待ち時間（秒）
                    </label>
                    <input
                        id="rw-response-timeout"
                        className="rw-settings__input"
                        type="number"
                        inputMode="numeric"
                        min={view?.aiTimeouts.minResponseSeconds}
                        max={view?.aiTimeouts.maxResponseSeconds}
                        value={responseSeconds}
                        onChange={(e) => setResponseSeconds(e.target.value)}
                    />
                </div>
                <div className="rw-settings__actions">
                    <Button onClick={() => void saveTimeouts()}>待ち時間を保存</Button>
                </div>
                <p className="rw-settings__hint">
                    {view
                        ? `接続は ${view.aiTimeouts.minConnectSeconds}〜${view.aiTimeouts.maxConnectSeconds} 秒、応答は ${view.aiTimeouts.minResponseSeconds}〜${view.aiTimeouts.maxResponseSeconds} 秒の範囲で指定できます。回線が遅い環境では応答の待ち時間を長くしてください。`
                        : ''}
                </p>
            </section>

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">診断情報</h2>
                <p className="rw-settings__hint">
                    不具合の調査に使う情報（動作の記録・アプリの版・OS の種別と版）を 1 つのファイルにまとめて保存します。
                    対話の内容・要件・決定事項や、登録したキー・認証情報は含みません。
                    保存したファイルは自動では送られません。渡す相手と手段はお使いの判断で選んでください。
                </p>
                <div className="rw-settings__actions">
                    <Button onClick={() => void loadDiagnostics()}>書き出す内容を確認</Button>
                    <Button
                        onClick={() => void exportDiagnostics()}
                        disabledReason={diagnostic ? undefined : '先に書き出す内容を確認してください。'}
                    >
                        ファイルに保存
                    </Button>
                </div>
                {diagnostic ? (
                    <>
                        <div className="rw-settings__row">
                            <span className="rw-settings__label">含まれるもの</span>
                            <span className="rw-settings__value">
                                {diagnostic.items.map((item) => `${item.name}（${item.bytes} バイト）`).join('、')}
                            </span>
                        </div>
                        <pre className="rw-settings__diagnostic" aria-label="書き出す内容">
                            {diagnostic.environment}
                            {diagnostic.logText}
                        </pre>
                        {diagnostic.truncated ? (
                            <p className="rw-settings__hint">
                                表示は新しい側だけを出しています。保存するファイルには全量（
                                {diagnostic.totalBytes} バイト）が入ります。
                            </p>
                        ) : null}
                    </>
                ) : null}
            </section>

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">このアプリについて</h2>
                <div className="rw-settings__row">
                    <span className="rw-settings__label">版</span>
                    <span className="rw-settings__value rw-settings__value--mono">
                        {appInfo ? appInfo.version : '—'}
                    </span>
                </div>
                <p className="rw-settings__hint">
                    新しい版が公開されているかは起動時に確認します。更新はお使いの操作で開始します。
                </p>
            </section>

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">はじめての方へ</h2>
                <div className="rw-settings__row">
                    <span className="rw-settings__label">紹介スライド</span>
                    <span className="rw-settings__value">
                        <Button onClick={() => setShowIntro(true)}>紹介スライドをもう一度見る</Button>
                    </span>
                </div>
                <p className="rw-settings__hint">
                    できること・はじめに決めること・チームでの進めかた・ファイルの扱いを説明します。
                    閉じるとこの画面へ戻ります。
                </p>
            </section>

            <section className="rw-settings__section">
                <h2 className="rw-settings__title">表示</h2>
                <div className="rw-settings__row">
                    <span className="rw-settings__label">テーマ</span>
                    <span className="rw-settings__value">
                        {theme === 'dark' ? 'ダーク' : 'ライト'}{' '}
                        <Button onClick={() => void toggleTheme()}>{theme === 'dark' ? 'ライトへ' : 'ダークへ'}</Button>
                    </span>
                </div>
                <p className="rw-settings__hint">テーマの選択はこの端末に保存され、次回起動時に復元されます。</p>
            </section>

            {message ? (
                <p className="rw-settings__message" data-tone={tone} role="status">
                    {message}
                </p>
            ) : null}
        </AppShell>
    )
}
