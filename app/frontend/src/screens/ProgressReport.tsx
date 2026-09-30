import {useCallback, useEffect, useMemo, useState} from 'react'
import {
    ChooseProgressReportDestination,
    CopyProgressReport,
    CurrentPermission,
    ProgressReport as BuildProgressReport,
    SaveProgressReport,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Banner, Button, defaultPeriod, EmptyState, Markdown, errorText} from '../ui'
import './ProgressReport.css'

/**
 * 進捗レポート。画面型は一覧・管理系。
 *
 * 期間を指定してレコードから機械組立てし（AI を呼ばない）、
 * 単一様式（要約先頭＋詳細）のプレビューを表示して、Markdown ファイル出力と
 * クリップボードコピーを行う。レポートはプロジェクトデータへ保存しない。
 *
 * 生成・出力は**閲覧権限でも実行できる**（取り込み画面と権限が異なる）。
 * 権限判定はバインディングが行い、画面は判定を持たない（判定を 1 か所に集めるため）。
 */

export function ProgressReport({onBack}: {onBack: () => void}) {
    const period = useMemo(() => defaultPeriod(new Date()), [])
    const [from, setFrom] = useState(period.from)
    const [to, setTo] = useState(period.to)
    const [report, setReport] = useState<binding.ProgressReportView | null>(null)
    const [permission, setPermission] = useState<binding.PermissionView | null>(null)
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')
    const [busy, setBusy] = useState(false)

    useEffect(() => {
        Promise.resolve()
            .then(() => CurrentPermission())
            .then(setPermission)
            .catch(() => undefined)
    }, [])

    const fail = useCallback((err: unknown) => {
        setTone('danger')
        setMessage(errorText(err))
    }, [])

    const request = useMemo(() => ({from, to}) as binding.ProgressReportRequest, [from, to])

    const build = async () => {
        setBusy(true)
        setMessage('')
        try {
            setReport(await BuildProgressReport(request))
        } catch (err: unknown) {
            setReport(null)
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const save = async () => {
        setBusy(true)
        setMessage('')
        try {
            const dst = await ChooseProgressReportDestination()
            if (!dst) {
                return
            }
            const saved = await SaveProgressReport(request, dst)
            setTone('accent')
            setMessage(`進捗レポートを保存しました（${saved}）。`)
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const copy = async () => {
        setBusy(true)
        setMessage('')
        try {
            await CopyProgressReport(request)
            setTone('accent')
            setMessage('進捗レポートをクリップボードへコピーしました。')
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const periodReason = from && to ? undefined : '対象期間の開始日と終了日を指定してください。'
    const busyReason = busy ? '処理中です。' : undefined
    const outputReason = report ? busyReason ?? periodReason : '先にレポートを作成してください。'

    return (
        <section className="rw-report" aria-label="進捗レポート">
            <header className="rw-report__head">
                <h2 className="rw-report__title">進捗レポート</h2>
                <div className="rw-report__actions">
                    <Button onClick={onBack}>対話へ戻る</Button>
                </div>
            </header>

            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')} />
            ) : null}

            <div className="rw-report__controls">
                <label className="rw-report__field">
                    <span>開始日</span>
                    <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="開始日" />
                </label>
                <label className="rw-report__field">
                    <span>終了日</span>
                    <input type="date" value={to} onChange={(e) => setTo(e.target.value)} aria-label="終了日" />
                </label>
                <Button
                    variant="primary"
                    onClick={() => void build()}
                    disabledReason={busyReason ?? periodReason}
                >
                    レポートを作成する
                </Button>
                <Button onClick={() => void save()} disabledReason={outputReason}>
                    Markdown で保存する
                </Button>
                <Button onClick={() => void copy()} disabledReason={outputReason}>
                    クリップボードへコピーする
                </Button>
            </div>

            <p className="rw-report__hint">
                レコードから組み立てます（AI へは送信しません）。プロジェクトデータは変化しません。
            </p>

            {permission ? (
                <p className="rw-report__meta">
                    権限: {permission.roleLabel}
                    {permission.canEdit ? '' : '（閲覧権限でも進捗レポートは作成・出力できます）'}
                </p>
            ) : null}

            {report ? (
                <article className="rw-report__preview" aria-label="レポートのプレビュー">
                    <Markdown source={report.markdown} />
                </article>
            ) : (
                <EmptyState
                    message="レポートはまだ作成していません。"
                    next="期間を指定して「レポートを作成する」を押すと、ここにプレビューが出ます。"
                />
            )}
        </section>
    )
}
