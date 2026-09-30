import {useCallback, useEffect, useState} from 'react'
import {
    CheckSyncConnection,
    DeleteSyncCredential,
    RegisterSyncCredential,
    SyncCredentialView,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Banner, Button, Chip, errorText} from '../ui'
import './SyncCredentials.css'

/**
 * 同期先の認証情報。画面型は一覧・管理系。
 *
 * 認証方式（SSH 鍵 / アクセストークン）を選んで登録し、接続確認・削除を行う。
 * 登録はプロジェクトごと・端末ごと。
 *
 * - **平文の再表示機能を持たない**（登録済みかどうかとマスク表記だけを表示する。画面から秘密の値を読み取らせない）。
 *   入力欄はパスワード型で、登録後は入力値を保持しない。
 * - 共有フォルダの同期先では認証情報が要らない旨を表示し、入力を求めない。
 * - 接続確認は登録済みの認証情報で行い、認証エラー / 到達不能 / その他を区別して示す（利用者が次に何を直せばよいか分かるように）。
 */

export function SyncCredentials({
    projectId,
    projectName,
    /** requiresCredential は同期先が認証を要するか（共有フォルダなら false）。 */
    requiresCredential = true,
    /** canCheck は接続確認を行えるか（プロジェクトを開いているときだけ真）。 */
    canCheck = true,
    onBack,
    backLabel,
}: {
    projectId: string
    /** projectName は対象の対象システム名（どのプロジェクトの認証情報かを示す）。 */
    projectName?: string
    requiresCredential?: boolean
    canCheck?: boolean
    onBack: () => void
    /**
     * 戻る操作のラベル。**戻り先を知っているのは呼び出し元**であり、
     * この画面は設定・同期の 2 経路から開かれるため、語を親から受け取る。
     */
    backLabel: string
}) {
    const [view, setView] = useState<binding.SyncCredentialView | null>(null)
    const [kind, setKind] = useState('')
    const [username, setUsername] = useState('')
    const [secret, setSecret] = useState('')
    const [detail, setDetail] = useState('')
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')
    const [busy, setBusy] = useState(false)

    const fail = useCallback((err: unknown) => {
        setTone('danger')
        setMessage(errorText(err))
    }, [])

    const reload = useCallback(async () => {
        try {
            const loaded = await SyncCredentialView(projectId)
            setView(loaded)
            setKind((current) => current || loaded.kind || loaded.kinds[0]?.kind || '')
        } catch (err: unknown) {
            fail(err)
        }
    }, [projectId, fail])

    useEffect(() => {
        void reload()
    }, [reload])

    const register = async () => {
        setBusy(true)
        setMessage('')
        setDetail('')
        try {
            await RegisterSyncCredential({projectId, kind, username, secret} as binding.RegisterSyncCredentialRequest)
            setSecret('') // 入力値を画面に残さない（再表示しない）
            setUsername('')
            setTone('accent')
            setMessage('同期先の認証情報を登録しました。接続を確認してください。')
            await reload()
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const remove = async () => {
        setBusy(true)
        setMessage('')
        setDetail('')
        try {
            await DeleteSyncCredential(projectId)
            setTone('accent')
            setMessage('同期先の認証情報を削除しました。')
            await reload()
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const check = async () => {
        setBusy(true)
        setMessage('')
        setDetail('')
        try {
            const result = await CheckSyncConnection('', '')
            setTone(result.ok ? 'accent' : 'danger')
            setMessage(result.notice)
            setDetail(result.failure?.detail ?? '')
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const selected = view?.kinds.find((k) => k.kind === kind)
    const busyReason = busy ? '処理中です。' : undefined
    const registerReason = busyReason ?? (secret.trim() ? undefined : '認証情報を入力してください。')

    return (
        <section className="rw-cred" aria-label="同期先の認証情報">
            <header className="rw-cred__head">
                <h2 className="rw-cred__title">
                    同期先の認証情報{projectName ? `（${projectName}）` : ''}
                </h2>
                <Button onClick={onBack}>{backLabel}</Button>
            </header>

            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')}>
                    {detail ? <pre className="rw-cred__detail">{detail}</pre> : null}
                </Banner>
            ) : null}

            {!requiresCredential ? (
                <p className="rw-cred__hint" role="status">
                    この同期先（共有フォルダ上のリポジトリ）では認証情報の登録は要りません。フォルダへのアクセス権で扱われます。
                </p>
            ) : (
                <>
                    <p className="rw-cred__state">
                        状態:{' '}
                        <Chip tone={view?.registered ? 'accent' : 'warn'}>{view?.state ?? '未設定'}</Chip>
                        <span className="rw-cred__meta rw-mono">
                            {view?.registered ? `${view.kindLabel} ／ ${view.masked}` : ''}
                        </span>
                    </p>
                    <p className="rw-cred__hint">
                        登録した内容はこの端末の OS のセキュアストレージに保管され、再表示はできません。変更するときは登録し直してください。
                    </p>

                    <fieldset className="rw-cred__field">
                        <legend>認証方式</legend>
                        {(view?.kinds ?? []).map((option) => (
                            <label key={option.kind} className="rw-cred__choice">
                                <input
                                    type="radio"
                                    name="sync-credential-kind"
                                    checked={kind === option.kind}
                                    onChange={() => setKind(option.kind)}
                                />
                                <span>
                                    {option.label}
                                    <span className="rw-cred__meta">{option.hint}</span>
                                </span>
                            </label>
                        ))}
                    </fieldset>

                    {kind === 'token' ? (
                        <label className="rw-cred__input">
                            <span>利用者名（任意）</span>
                            <input
                                type="text"
                                value={username}
                                onChange={(e) => setUsername(e.target.value)}
                                aria-label="利用者名"
                            />
                        </label>
                    ) : null}

                    <label className="rw-cred__input">
                        <span>{selected?.label ?? '認証情報'}</span>
                        {kind === 'ssh_key' ? (
                            <textarea
                                value={secret}
                                rows={6}
                                onChange={(e) => setSecret(e.target.value)}
                                aria-label="SSH 秘密鍵"
                            />
                        ) : (
                            <input
                                type="password"
                                value={secret}
                                onChange={(e) => setSecret(e.target.value)}
                                aria-label="アクセストークン"
                            />
                        )}
                    </label>

                    <div className="rw-cred__actions">
                        <Button variant="primary" onClick={() => void register()} disabledReason={registerReason}>
                            登録する
                        </Button>
                        <Button
                            onClick={() => void check()}
                            disabledReason={
                                busyReason ??
                                (canCheck ? undefined : 'プロジェクトを開いてから接続を確認してください。')
                            }
                        >
                            接続を確認する
                        </Button>
                        <Button
                            variant="secondary"
                            onClick={() => void remove()}
                            disabledReason={busyReason ?? (view?.registered ? undefined : '登録されていません。')}
                        >
                            削除する
                        </Button>
                    </div>
                </>
            )}
        </section>
    )
}
