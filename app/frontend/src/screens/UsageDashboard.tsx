import {useCallback, useEffect, useMemo, useState} from 'react'
import {
    ChooseUsageReportDestination,
    CopyUsageReport,
    CurrentPermission,
    SaveUsageReport,
    UsageDashboard as LoadUsageDashboard,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {AppShell, Banner, Button, Chip, DataList, defaultPeriod, EmptyState, formatCount, formatLocal, formatRatio, PlanUsage, Toast, type Row, errorText} from '../ui'
import './UsageDashboard.css'

/**
 * AI 利用量ダッシュボード。画面型は一覧・管理系。
 *
 * 期間を指定してプロジェクト横断の一覧を出し、行を選ぶとそのプロジェクトの
 * 内訳（プロバイダ別・対話セッション別・作業者別）とセッション統計を
 * 表示する。Markdown ファイル出力・クリップボードコピーは同じ本文（バインディングが組み立てる）。
 *
 * AI を呼ばないためオフライン・API 障害中でも成立し、閲覧権限でも参照できる。
 * 集計・権限判定はすべてバインディングが行い、画面は判定を持たない（判定を 1 か所に集めるため）。
 * 数値は必ず数字として読めるようにする（グラフのみの表現にしない）。
 */

/** 内訳の区分ラベル（空文字 = セッション文脈を持たない送信。Markdown 出力と同じ表記） */
function bucketLabel(key: string): string {
    return key.trim() === '' ? '対話セッション外（取り込み分析など）' : key
}

/** フェーズの表示ラベル（保存値は英語識別子）。網羅を型で強制する。 */
const PHASE_LABELS = {
    requirements: '要件定義',
    'basic-design': '基本設計',
} as const

type Phase = keyof typeof PHASE_LABELS

function phaseLabel(phase: string): string {
    return phase in PHASE_LABELS ? PHASE_LABELS[phase as Phase] : 'フェーズ不明'
}

/** 横断一覧の行の見出し（対象システム名が無ければフォルダのパス） */
function projectLabel(row: binding.ProjectUsageRow): string {
    return row.targetSystemName.trim() !== '' ? row.targetSystemName : row.path
}

const CROSS_COLUMNS = [
    {key: 'name', label: 'プロジェクト'},
    {key: 'tokens', label: 'トークン消費', mono: true},
    {key: 'lastUsed', label: '直近の利用', mono: true},
    {key: 'limit', label: '上限', mono: true},
    {key: 'ratio', label: '消費率', mono: true},
    {key: 'missing', label: '実績なしの送信', mono: true},
]

const BUCKET_COLUMNS = [
    {key: 'key', label: '区分'},
    {key: 'tokens', label: 'トークン消費', mono: true},
    {key: 'sends', label: '送信件数', mono: true},
    {key: 'missing', label: '実績なし', mono: true},
]

function bucketRows(buckets: binding.UsageBucketView[]): Row[] {
    return buckets.map((b, i) => ({
        id: `${i}-${b.key}`,
        cells: {
            key: bucketLabel(b.key),
            tokens: formatCount(b.tokens),
            sends: formatCount(b.sends),
            missing: formatCount(b.missing),
        },
    }))
}

export function UsageDashboard({
    onBack,
    backLabel,
    onOpenUsageLimit,
    embedded = false,
}: {
    onBack: () => void
    /**
     * 埋め込み時の戻る操作のラベル。**戻り先を知っているのは呼び出し元**であり、
     * この画面は複数の経路から開かれるため、語を親から受け取る。
     * 埋め込みでないときは AppShell のナビが戻る操作を持つため、受け取らない（重複させない）。
     */
    /**
     * トークン上限設定への導線。開いているプロジェクトが対象のため、
     * プロジェクトを開いていない経路（プロジェクト一覧から開いた場合）では渡さない。
     */
    onOpenUsageLimit?: () => void
} & ({embedded: true; backLabel: string} | {embedded?: false; backLabel?: never})) {
    const period = useMemo(() => defaultPeriod(new Date()), [])
    const [from, setFrom] = useState(period.from)
    const [to, setTo] = useState(period.to)
    const [selected, setSelected] = useState('')
    const [view, setView] = useState<binding.UsageDashboardView | null>(null)
    const [permission, setPermission] = useState<binding.PermissionView | null>(null)
    const [error, setError] = useState('')
    const [toast, setToast] = useState<{tone: 'accent' | 'danger'; message: string} | null>(null)
    const [busy, setBusy] = useState(false)

    // 上限設定の可否はバインディングの判定だけを使う（画面側で権限を判定しない）。
    // プロジェクトを開いていない経路では判定自体が取れない（開くと判定できる）。
    useEffect(() => {
        Promise.resolve()
            .then(() => CurrentPermission())
            .then(setPermission)
            .catch(() => setPermission(null))
    }, [])

    const load = useCallback(async (nextFrom: string, nextTo: string, path: string) => {
        setBusy(true)
        setError('')
        try {
            setView(await LoadUsageDashboard({from: nextFrom, to: nextTo, path} as binding.UsageReportRequest))
        } catch (err: unknown) {
            setView(null)
            setError(errorText(err))
        } finally {
            setBusy(false)
        }
    }, [])

    // 開いた時点の既定期間（当月）で集計しておく（読むだけの画面のため確認操作を挟まない）。
    useEffect(() => {
        void load(period.from, period.to, '')
    }, [load, period.from, period.to])

    const select = (path: string) => {
        const next = path === selected ? '' : path
        setSelected(next)
        void load(from, to, next)
    }

    const save = async () => {
        setBusy(true)
        try {
            const dst = await ChooseUsageReportDestination()
            if (!dst) {
                return
            }
            const saved = await SaveUsageReport({from, to, path: selected} as binding.UsageReportRequest, dst)
            setToast({tone: 'accent', message: `AI 利用量を保存しました（${saved}）。`})
        } catch (err: unknown) {
            setToast({tone: 'danger', message: errorText(err)})
        } finally {
            setBusy(false)
        }
    }

    const copy = async () => {
        setBusy(true)
        try {
            await CopyUsageReport({from, to, path: selected} as binding.UsageReportRequest)
            setToast({tone: 'accent', message: 'AI 利用量をクリップボードへコピーしました。'})
        } catch (err: unknown) {
            setToast({tone: 'danger', message: errorText(err)})
        } finally {
            setBusy(false)
        }
    }

    const busyReason = busy ? '処理中です。' : undefined
    const periodReason = from && to ? undefined : '対象期間の開始日と終了日を指定してください。'
    const outputReason = view ? busyReason ?? periodReason : '先に集計してください。'
    const limitReason = !onOpenUsageLimit
        ? 'プロジェクトを開いてから上限を設定してください。'
        : permission?.canManageUsageLimit
          ? undefined
          : (permission?.usageLimitReason ?? 'プロジェクトを開いてから上限を設定してください。')

    const rows: Row[] = (view?.projects ?? []).map((p) => ({
        id: p.path,
        cells: p.aggregated
            ? {
                  name: projectLabel(p),
                  tokens: formatCount(p.tokens),
                  lastUsed: formatLocal(p.lastUsedAt ?? ''),
                  limit: p.limitTokens === null || p.limitTokens === undefined ? '—' : formatCount(p.limitTokens),
                  ratio: formatRatio(p.consumptionRatio),
                  missing: formatCount(p.missingRecords),
              }
            : {
                  name: projectLabel(p),
                  tokens: <Chip tone="danger">集計不能</Chip>,
                  lastUsed: '—',
                  limit: '—',
                  ratio: '—',
                  missing: '—',
              },
    }))
    const unavailable = (view?.projects ?? []).filter((p) => !p.aggregated && p.notice)
    const detail = view?.detail ?? null

    const body = (
        <section className="rw-usage" aria-label="AI 利用量ダッシュボード">
            <header className="rw-usage__head">
                <h2 className="rw-usage__title">AI 利用量</h2>
                <div className="rw-usage__actions">
                    {/* 埋め込みでないときは AppShell のナビが戻る操作を持つ（重複させない）。 */}
                    {embedded ? <Button onClick={onBack}>{backLabel}</Button> : null}
                </div>
            </header>

            {error ? (
                <Banner tone="danger" title={error} onDismiss={() => setError('')} onDefer={() => setError('')} />
            ) : null}

            <div className="rw-usage__controls">
                <label className="rw-usage__field">
                    <span>開始日</span>
                    <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="開始日" />
                </label>
                <label className="rw-usage__field">
                    <span>終了日</span>
                    <input type="date" value={to} onChange={(e) => setTo(e.target.value)} aria-label="終了日" />
                </label>
                <Button
                    variant="primary"
                    onClick={() => void load(from, to, selected)}
                    disabledReason={busyReason ?? periodReason}
                >
                    集計する
                </Button>
                <Button onClick={() => void save()} disabledReason={outputReason}>
                    Markdown で保存する
                </Button>
                <Button onClick={() => void copy()} disabledReason={outputReason}>
                    クリップボードへコピーする
                </Button>
                <Button onClick={() => onOpenUsageLimit?.()} disabledReason={limitReason}>
                    上限を設定する
                </Button>
            </div>

            <p className="rw-usage__hint">
                記録済みの実績から集計します（AI へは送信しません）。プロジェクトデータは変化しません。
                {limitReason ? `　上限設定: ${limitReason}` : ''}
            </p>

            {/*
              * ChatGPT のプランの残量。
              * サインインしているときだけ出る（シークレットキー方式では値が届かないため出さない）。
              * **表示のための通信をしない**。トークン上限の判定にも使わない。
              */}
            <PlanUsage
                label=""
                note="この残量はプロジェクトのトークン上限の判定には使いません（上限の判定はトークン数で行います）。"
            />

            <DataList
                caption="プロジェクト横断の利用量"
                columns={CROSS_COLUMNS}
                rows={rows}
                selectedId={selected}
                onSelect={select}
                empty={{
                    message: 'この端末で開けるプロジェクトがありません。',
                    next: 'プロジェクト一覧で新規作成するか、既存のプロジェクトを開いてください。',
                }}
            />

            {unavailable.length > 0 ? (
                <ul className="rw-usage__notices" aria-label="集計できなかったプロジェクト">
                    {unavailable.map((p) => (
                        <li key={p.path}>
                            {projectLabel(p)}: {p.notice}
                        </li>
                    ))}
                </ul>
            ) : null}

            {detail ? (
                <section className="rw-usage__detail" aria-label="プロジェクトの内訳">
                    <h3 className="rw-usage__subtitle">{detail.targetSystemName} の内訳</h3>
                    {/* 集計範囲の併記。 */}
                    {detail.scopeNotice ? (
                        <p className="rw-usage__hint" role="status">
                            {detail.scopeNotice}
                        </p>
                    ) : null}
                    <dl className="rw-usage__figures">
                        <div>
                            <dt>トークン消費合計</dt>
                            <dd className="rw-mono">{formatCount(detail.tokens)}</dd>
                        </div>
                        <div>
                            <dt>入力 / 出力 / 推論</dt>
                            <dd className="rw-mono">
                                {formatCount(detail.tokensIn)} / {formatCount(detail.tokensOut)} /{' '}
                                {formatCount(detail.tokensReasoning)}
                            </dd>
                        </div>
                        <div>
                            <dt>送信件数</dt>
                            <dd className="rw-mono">{formatCount(detail.sends)}</dd>
                        </div>
                        <div>
                            <dt>トークン実績なしの送信</dt>
                            <dd className="rw-mono">{formatCount(detail.missingRecords)}</dd>
                        </div>
                        <div>
                            <dt>直近の利用</dt>
                            <dd className="rw-mono">{formatLocal(detail.lastUsedAt ?? '')}</dd>
                        </div>
                        <div>
                            <dt>上限 / 消費率</dt>
                            <dd className="rw-mono">
                                {detail.limitTokens === null || detail.limitTokens === undefined
                                    ? '未設定'
                                    : `${formatCount(detail.limitTokens)} / ${formatRatio(detail.consumptionRatio)}`}
                            </dd>
                        </div>
                    </dl>

                    <DataList
                        caption="プロバイダ別"
                        columns={BUCKET_COLUMNS}
                        rows={bucketRows(detail.byProvider)}
                        empty={{
                            message: 'この期間の送信はありません。',
                            next: '上の「開始日」「終了日」を広げると、ほかの期間の実績を確認できます。',
                        }}
                    />
                    <DataList
                        caption="対話セッション別"
                        columns={BUCKET_COLUMNS}
                        rows={bucketRows(detail.bySession)}
                        empty={{
                            message: 'この期間の送信はありません。',
                            next: '上の「開始日」「終了日」を広げると、ほかの期間の実績を確認できます。',
                        }}
                    />
                    <DataList
                        caption="作業者別"
                        columns={BUCKET_COLUMNS}
                        rows={bucketRows(detail.byAuthor)}
                        empty={{
                            message: 'この期間の送信はありません。',
                            next: '上の「開始日」「終了日」を広げると、ほかの期間の実績を確認できます。',
                        }}
                    />

                    <h3 className="rw-usage__subtitle">セッション統計</h3>
                    <dl className="rw-usage__figures">
                        <div>
                            <dt>対話セッション数</dt>
                            <dd className="rw-mono">
                                {formatCount(detail.sessions.sessionsTotal)}
                                {detail.sessions.sessionsByPhase.length > 0
                                    ? `（${detail.sessions.sessionsByPhase
                                          .map((p) => `${phaseLabel(p.phase)} ${formatCount(p.count)}`)
                                          .join(' / ')}）`
                                    : ''}
                            </dd>
                        </div>
                        <div>
                            <dt>質問票（発行 / 取込）</dt>
                            <dd className="rw-mono">
                                {formatCount(detail.sessions.questionnairesIssued)} /{' '}
                                {formatCount(detail.sessions.questionnairesImported)}
                            </dd>
                        </div>
                        <div>
                            <dt>発行から取込までの平均日数</dt>
                            <dd className="rw-mono">
                                {detail.sessions.questionnairesImported > 0
                                    ? detail.sessions.elapsedDaysAverage.toFixed(1)
                                    : '—'}
                            </dd>
                        </div>
                        <div>
                            <dt>承認された決定事項</dt>
                            <dd className="rw-mono">{formatCount(detail.sessions.decisionsApproved)}</dd>
                        </div>
                        <div>
                            <dt>決着した未決事項</dt>
                            <dd className="rw-mono">{formatCount(detail.sessions.openIssuesResolved)}</dd>
                        </div>
                        <div>
                            <dt>確定した要件項目</dt>
                            <dd className="rw-mono">{formatCount(detail.sessions.requirementsConfirmed)}</dd>
                        </div>
                    </dl>
                </section>
            ) : (
                <EmptyState
                    message="内訳を表示するプロジェクトを選んでいません。"
                    next="上の一覧の行を選ぶと、そのプロジェクトの内訳とセッション統計が表示されます。"
                />
            )}

            {toast ? (
                <Toast tone={toast.tone} message={toast.message} onDismiss={() => setToast(null)} />
            ) : null}
        </section>
    )

    if (embedded) {
        return body
    }
    return (
        <AppShell
            scope="global"
            breadcrumb={['reqweave', 'AI 利用量']}
            nav={
                <Button onClick={onBack} tooltip="この画面を閉じて、プロジェクトの一覧へ戻ります。">
                    プロジェクト一覧へ戻る
                </Button>
            }
        >
            {body}
        </AppShell>
    )
}
