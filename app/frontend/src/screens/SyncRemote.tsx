import {useCallback, useEffect, useState} from 'react'
import {CheckSyncConnection, ChooseFolder, SetSyncRemote, SyncRemote as LoadSyncRemote} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Banner, Button, Chip, errorText} from '../ui'
import './SyncRemote.css'

/**
 * 同期先の設定。画面型は一覧・管理系。
 *
 * 種別（共有フォルダ上のリポジトリ〔既定〕/ 社内 Git サーバ / 外部 Git サーバ）と所在を設定する。
 *
 * - **オーナーのみ**変更できる。編集・閲覧権限では無効化して理由を表示する（ほかの管理画面と同じ方式）。
 * - **外部 Git サーバを選んだときは明示表示と同意操作を経る**（同意なしに保存しない。社外へデータを出すことを利用者が知ったうえで選ぶように）。
 * - 所在に資格情報を含む URL は受け付けない（判定はバックエンドが行う）。
 * - 認証情報は別画面。ここでは登録状態と導線だけを示す。
 */

export function SyncRemote({
    onBack,
    onOpenCredentials,
}: {
    onBack: () => void
    onOpenCredentials: () => void
}) {
    const [view, setView] = useState<binding.SyncRemoteView | null>(null)
    const [kind, setKind] = useState('')
    const [location, setLocation] = useState('')
    const [consent, setConsent] = useState(false)
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
            const loaded = await LoadSyncRemote()
            setView(loaded)
            setKind((current) => current || loaded.kind || loaded.kinds[0]?.kind || '')
            setLocation((current) => current || loaded.location || '')
        } catch (err: unknown) {
            fail(err)
        }
    }, [fail])

    useEffect(() => {
        void reload()
    }, [reload])

    const selected = view?.kinds.find((k) => k.kind === kind)

    const save = async () => {
        setBusy(true)
        setMessage('')
        setDetail('')
        try {
            const saved = await SetSyncRemote({kind, location, externalConsent: consent} as binding.SetSyncRemoteRequest)
            setView(saved)
            setTone('accent')
            setMessage('同期先を設定しました。この設定は反映して初めて他のメンバーへ及びます。')
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
            const result = await CheckSyncConnection(kind, location)
            setTone(result.ok ? 'accent' : 'danger')
            setMessage(result.notice)
            setDetail(result.failure?.detail ?? '')
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const chooseFolder = async () => {
        try {
            const picked = await ChooseFolder('同期先のフォルダを選ぶ')
            if (picked) {
                setLocation(picked)
            }
        } catch (err: unknown) {
            fail(err)
        }
    }

    const busyReason = busy ? '処理中です。' : undefined
    const manageReason = view?.canManage ? undefined : view?.manageReason
    const consentReason =
        selected?.requiresConsent && !consent ? '外部 Git サーバへ保管することへの同意が必要です。' : undefined
    const locationReason = location.trim() ? undefined : '同期先の所在を入力してください。'
    const saveReason = busyReason ?? manageReason ?? locationReason ?? consentReason

    return (
        <section className="rw-remote" aria-label="同期先の設定">
            <header className="rw-remote__head">
                <h2 className="rw-remote__title">同期先の設定</h2>
                <Button onClick={onBack}>メンバーへ戻る</Button>
            </header>

            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')}>
                    {detail ? <pre className="rw-remote__detail">{detail}</pre> : null}
                </Banner>
            ) : null}

            {view?.notice ? (
                <p className="rw-remote__hint" role="status">
                    {view.notice}
                </p>
            ) : null}
            {manageReason ? (
                <p className="rw-remote__hint" role="status">
                    {manageReason}
                </p>
            ) : null}

            <fieldset className="rw-remote__field">
                <legend>同期先の種別</legend>
                {(view?.kinds ?? []).map((option) => (
                    <label key={option.kind} className="rw-remote__choice">
                        <input
                            type="radio"
                            name="sync-remote-kind"
                            checked={kind === option.kind}
                            disabled={!view?.canManage}
                            onChange={() => {
                                setKind(option.kind)
                                setConsent(false)
                            }}
                        />
                        <span>
                            {option.label}
                            <span className="rw-remote__meta">{option.hint}</span>
                        </span>
                    </label>
                ))}
            </fieldset>

            <label className="rw-remote__input">
                <span>所在</span>
                <input
                    type="text"
                    value={location}
                    disabled={!view?.canManage}
                    onChange={(e) => setLocation(e.target.value)}
                    aria-label="同期先の所在"
                />
            </label>
            {kind === 'folder' ? (
                <div className="rw-remote__actions">
                    <Button onClick={() => void chooseFolder()} disabledReason={manageReason}>
                        フォルダを選ぶ
                    </Button>
                </div>
            ) : null}

            {selected?.requiresConsent ? (
                <div className="rw-remote__consent">
                    <p className="rw-remote__consent-text" role="alert">
                        {selected.consentText}
                    </p>
                    <label className="rw-remote__choice">
                        <input
                            type="checkbox"
                            checked={consent}
                            disabled={!view?.canManage}
                            onChange={(e) => setConsent(e.target.checked)}
                        />
                        <span>上記の内容に同意します</span>
                    </label>
                </div>
            ) : null}

            <div className="rw-remote__actions">
                <Button variant="primary" onClick={() => void save()} disabledReason={saveReason}>
                    この同期先を保存する
                </Button>
                <Button onClick={() => void check()} disabledReason={busyReason ?? locationReason}>
                    接続を確認する
                </Button>
            </div>

            {selected?.requiresCredential ? (
                <p className="rw-remote__hint">
                    認証情報:{' '}
                    <Chip tone={view?.credential.registered ? 'accent' : 'warn'}>
                        {view?.credential.state ?? '未設定'}
                    </Chip>{' '}
                    <Button variant="secondary" onClick={onOpenCredentials}>
                        同期先の認証情報を設定する
                    </Button>
                </p>
            ) : null}
        </section>
    )
}
