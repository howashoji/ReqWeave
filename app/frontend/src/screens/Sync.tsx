import {useCallback, useEffect, useState} from 'react'
import {
    CheckSyncConnection,
    CreateSyncMergeOpenIssue,
    IncorporateSync,
    PreviewSyncPublish,
    PublishSync,
    ReattachSync,
    SyncLog,
    SyncMergeOptions,
    SyncStatus,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Banner, Button, Chip, DataList, EmptyState, SyncMergePanel, errorText} from '../ui'
import './Sync.css'

/**
 * 同期。画面型はパネル・バナー系。
 *
 * 取り込み・反映の実行と結果の提示、最後に同期した日時と未反映の変更の有無、同期の記録の参照。
 *
 * - **反映は事前提示と確認操作を経てから実行する**（何が同期先へ載るかを見せる）。
 * - 取り込みで競合したときは三面マージを開く。承認するまで作業コピーは変わらない。
 * - **閲覧権限では反映を無効化し理由を表示する**（取り込みは可）。判定はバックエンド。
 * - 到達できないときは原因と次の行動を 1 文で示し、作業を続けられる旨を併記する。
 *   同期先へ到達できないことを常時の警告表示にしない（同期はしなくても単独で作業できるため）。
 * - git の語・生のエラー文言は出さない（文言はすべてバックエンドが組み立てる）。
 */

/** 進行状況イベント（バックエンドの binding.SyncProgressEvent）。 */
type SyncProgressEvent = {stage: string; label: string}

export function Sync({
    onBack,
    onIncorporated,
    onOpenCredentials,
}: {
    onBack: () => void
    /** onIncorporated は取り込み完了の通知（親が変更要約を出す）。 */
    onIncorporated: () => void
    /** onOpenCredentials は同期先の認証情報へ移る。 */
    onOpenCredentials: () => void
}) {
    const [status, setStatus] = useState<binding.SyncStatusView | null>(null)
    const [log, setLog] = useState<binding.SyncLogView | null>(null)
    const [choices, setChoices] = useState<binding.SyncChoiceOption[]>([])
    const [preview, setPreview] = useState<binding.SyncPublishPreviewView | null>(null)
    const [conflicts, setConflicts] = useState<binding.SyncConflictView[]>([])
    const [resolutions, setResolutions] = useState<Record<string, binding.SyncResolutionInput>>({})
    const [conflictNotice, setConflictNotice] = useState('')
    const [failure, setFailure] = useState<binding.SyncFailureView | null>(null)
    const [showDetail, setShowDetail] = useState(false)
    const [progress, setProgress] = useState('')
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')
    const [busy, setBusy] = useState(false)

    const fail = useCallback((err: unknown) => {
        setTone('danger')
        setMessage(errorText(err))
    }, [])

    const reload = useCallback(async () => {
        try {
            setStatus(await SyncStatus())
            setLog(await SyncLog())
        } catch (err: unknown) {
            fail(err)
        }
    }, [fail])

    useEffect(() => {
        void reload()
        Promise.resolve()
            .then(() => SyncMergeOptions())
            .then(setChoices)
            .catch(() => undefined)
    }, [reload])

    // 進行状況。段階名はバックエンドが日本語で渡す。
    useEffect(() => {
        const off = EventsOn('sync:progress', (ev: SyncProgressEvent) => setProgress(ev.label))
        return () => off()
    }, [])

    const begin = () => {
        setBusy(true)
        setMessage('')
        setFailure(null)
        setShowDetail(false)
    }

    const end = () => {
        setBusy(false)
        setProgress('')
    }

    /**
     * 取り込み（承認つきで再実行できる）。
     *
     * 承認は対象 ID をキーに画面が保持し、実行時に配列へ直して渡す（**空で呼ぶのが初回**）。
     */
    const incorporate = async (approved: Record<string, binding.SyncResolutionInput>) => {
        begin()
        try {
            const result = await IncorporateSync(
                Object.entries(approved).map(([id, r]) => ({...r, conflictId: id}) as binding.SyncResolutionInput),
            )
            if (result.failure) {
                setFailure(result.failure)
                setTone('danger')
                setMessage(result.notice)
                return
            }
            if (result.conflicts && result.conflicts.length > 0) {
                setConflicts(result.conflicts)
                setConflictNotice(result.notice)
                setTone('warn')
                setMessage('')
                return
            }
            setConflicts([])
            setResolutions({})
            setTone('accent')
            setMessage(result.notice)
            await reload()
            onIncorporated()
        } catch (err: unknown) {
            fail(err)
        } finally {
            end()
        }
    }

    const cancelMerge = () => {
        setConflicts([])
        setResolutions({})
        setConflictNotice('')
        setTone('info')
        setMessage('取り込みを中止しました。作業コピーの内容は変わっていません。')
    }

    const createOpenIssue = async (conflict: binding.SyncConflictView) => {
        begin()
        try {
            const created = await CreateSyncMergeOpenIssue({
                conflictId: conflict.id,
                label: conflict.label,
                theirsAuthor: conflict.theirsAuthor,
                theirs: conflict.theirs,
                ours: conflict.ours,
            } as binding.CreateSyncMergeOpenIssueRequest)
            setResolutions((prev) => ({
                ...prev,
                [conflict.id]: {...prev[conflict.id], choice: 'open-issue', openIssueId: created.openIssueId},
            }))
            setTone('accent')
            setMessage(created.notice)
        } catch (err: unknown) {
            fail(err)
        } finally {
            end()
        }
    }

    /** 反映の事前提示。 */
    const previewPublish = async () => {
        begin()
        try {
            const result = await PreviewSyncPublish()
            setPreview(result)
            if (result.failure) {
                setFailure(result.failure)
                setTone('danger')
                setMessage(result.notice)
            }
        } catch (err: unknown) {
            fail(err)
        } finally {
            end()
        }
    }

    /**
     * 取り直し。
     *
     * バックアップから復元した作業コピーには同期の管理情報が無い（自動退避に含めないため）。
     * 同期先から受け取り直すだけで**同期先へは書かない**。復元した内容は未反映の変更として残る。
     */
    const reattach = async () => {
        begin()
        try {
            const result = await ReattachSync()
            setPreview(null)
            if (result.failure) {
                setFailure(result.failure)
                setTone('danger')
                setMessage(result.notice)
                return
            }
            setTone(result.restorePending ? 'warn' : 'accent')
            setMessage(result.notice)
            await reload()
        } catch (err: unknown) {
            fail(err)
        } finally {
            end()
        }
    }

    /**
     * 反映。
     *
     * 復元した内容を載せる場合（preview.restorePending）は、事前提示を見たうえでのこの操作が
     * 「反映してよい」という確認になる（無確認で同期先へ書かない）。
     */
    const publish = async () => {
        begin()
        try {
            const result = await PublishSync(preview?.restorePending === true)
            setPreview(null)
            if (result.failure) {
                setFailure(result.failure)
                setTone('danger')
                setMessage(result.notice)
                return
            }
            setTone('accent')
            setMessage(result.notice)
            await reload()
        } catch (err: unknown) {
            fail(err)
        } finally {
            end()
        }
    }

    const check = async () => {
        begin()
        try {
            const result = await CheckSyncConnection('', '')
            if (result.failure) {
                setFailure(result.failure)
                setTone('danger')
            } else {
                setTone('accent')
            }
            setMessage(result.notice)
        } catch (err: unknown) {
            fail(err)
        } finally {
            end()
        }
    }

    const busyReason = busy ? '処理中です。' : undefined
    const unavailable = status && !status.available ? status.unavailableReason : undefined
    const incorporateReason = busyReason ?? unavailable
    const publishReason = busyReason ?? unavailable ?? (status?.canPublish ? undefined : status?.publishReason)

    const logRows = (log?.entries ?? []).map((entry, i) => ({
        id: `${entry.at}-${i}`,
        cells: {
            at: entry.at,
            operation: entry.operation,
            remote: entry.remote,
            summary: entry.summary,
            author: entry.author,
            result: (
                <Chip tone={entry.result === '成功' ? 'accent' : entry.result === '失敗' ? 'danger' : 'warn'}>
                    {entry.result}
                </Chip>
            ),
            failure: entry.failure ?? '',
        },
    }))

    return (
        <section className="rw-sync" aria-label="同期">
            <header className="rw-sync__head">
                <h2 className="rw-sync__title">同期</h2>
                <Button onClick={onBack}>対話へ戻る</Button>
            </header>

            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')}>
                    {failure?.detail ? (
                        <>
                            <Button variant="secondary" onClick={() => setShowDetail((v) => !v)}>
                                {showDetail ? '詳細を隠す' : '詳細を見る'}
                            </Button>
                            {showDetail ? <pre className="rw-sync__detail">{failure.detail}</pre> : null}
                        </>
                    ) : null}
                </Banner>
            ) : null}

            {status && !status.configured ? (
                <EmptyState
                    message={status.notice ?? '同期先が設定されていません。'}
                    next="オーナーが「メンバー」画面から同期先を設定すると、取り込み・反映ができるようになります。"
                />
            ) : null}

            {status?.configured ? (
                <>
                    <dl className="rw-sync__facts">
                        <div>
                            <dt>同期先</dt>
                            <dd>
                                {status.kindLabel}
                                <span className="rw-sync__meta rw-mono">{status.location}</span>
                            </dd>
                        </div>
                        <div>
                            <dt>最後に同期した日時</dt>
                            <dd className="rw-mono">{status.lastSyncedAt || 'まだ同期していません'}</dd>
                        </div>
                        <div>
                            <dt>最後に取り込んだ日時</dt>
                            <dd className="rw-mono">{status.lastIncorporationAt || 'まだ取り込んでいません'}</dd>
                        </div>
                        <div>
                            <dt>未反映の変更</dt>
                            <dd>
                                {status.hasUnpublished ? (
                                    <>
                                        <Chip tone="warn">あり</Chip>
                                        <span className="rw-sync__meta rw-mono">{status.unpublishedSummary}</span>
                                    </>
                                ) : (
                                    <Chip tone="accent">なし</Chip>
                                )}
                            </dd>
                        </div>
                    </dl>

                    {status.notice ? (
                        <p className="rw-sync__notice" role="status">
                            {status.notice}
                        </p>
                    ) : null}

                    {progress ? (
                        <p className="rw-sync__progress" role="status">
                            {progress}
                        </p>
                    ) : null}

                    <div className="rw-sync__actions">
                        <Button
                            variant="primary"
                            onClick={() => void incorporate(resolutions)}
                            disabledReason={incorporateReason}
                        >
                            取り込む
                        </Button>
                        <Button onClick={() => void previewPublish()} disabledReason={publishReason}>
                            反映する内容を確認する
                        </Button>
                        {status.needsReattach ? (
                            <Button
                                variant="primary"
                                onClick={() => void reattach()}
                                disabledReason={busyReason ?? unavailable}
                            >
                                同期先から取り直す
                            </Button>
                        ) : null}
                        <Button onClick={() => void check()} disabledReason={busyReason ?? unavailable}>
                            接続を確認する
                        </Button>
                        {status.credentialRequired ? (
                            <Button onClick={onOpenCredentials}>
                                同期先の認証情報{status.credentialRegistered ? '' : '（未登録）'}
                            </Button>
                        ) : null}
                    </div>

                    {preview && !preview.failure ? (
                        <section className="rw-sync__preview" aria-label="反映する内容">
                            <p className="rw-sync__notice">{preview.notice}</p>
                            {preview.restorePending ? (
                                <p className="rw-sync__notice" role="status">
                                    復元した内容をそのまま載せると、他のメンバーには成果が古い内容へ戻ったように
                                    見えることがあります。上の件数を確かめてから反映してください。
                                </p>
                            ) : null}
                            <ul className="rw-sync__items">
                                {(preview.items ?? []).map((item) => (
                                    <li key={item.categoryLabel} className="rw-mono">
                                        {item.categoryLabel} {item.total} 件（追加 {item.added} / 変更{' '}
                                        {item.modified} / 削除 {item.removed}）
                                    </li>
                                ))}
                            </ul>
                            <div className="rw-sync__actions">
                                <Button
                                    variant="primary"
                                    onClick={() => void publish()}
                                    disabledReason={
                                        publishReason ??
                                        (preview.needsReattach
                                            ? '先に同期先から取り直してください。'
                                            : preview.total > 0
                                              ? undefined
                                              : '反映する変更がありません。')
                                    }
                                >
                                    この内容で反映する
                                </Button>
                                <Button variant="quiet" onClick={() => setPreview(null)}>
                                    やめる
                                </Button>
                            </div>
                        </section>
                    ) : null}

                    {conflicts.length > 0 ? (
                        <SyncMergePanel
                            conflicts={conflicts}
                            choices={choices}
                            notice={conflictNotice}
                            busy={busy}
                            resolutions={resolutions}
                            onChange={(id, resolution) =>
                                setResolutions((prev) => ({...prev, [id]: resolution}))
                            }
                            onCreateOpenIssue={(conflict) => void createOpenIssue(conflict)}
                            onApply={() => void incorporate(resolutions)}
                            onCancel={cancelMerge}
                        />
                    ) : null}

                    <section className="rw-sync__log" aria-label="同期の記録">
                        <h3 className="rw-sync__subtitle">同期の記録</h3>
                        <DataList
                            caption="同期の記録"
                            columns={[
                                {key: 'at', label: '日時', mono: true},
                                {key: 'operation', label: '種別'},
                                {key: 'remote', label: '同期先'},
                                {key: 'summary', label: '内容'},
                                {key: 'author', label: '作業者'},
                                {key: 'result', label: '結果'},
                                {key: 'failure', label: '理由'},
                            ]}
                            rows={logRows}
                            empty={{
                                message: log?.notice ?? '同期の記録はまだありません。',
                                automatic: '「取り込む」「反映する」を行うたびに、結果がここへ残ります。',
                            }}
                        />
                    </section>
                </>
            ) : null}
        </section>
    )
}
