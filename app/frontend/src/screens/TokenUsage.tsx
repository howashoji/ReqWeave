import {useEffect, useState} from 'react'
import {TokenUsage as LoadTokenUsage} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {AppShell, Banner, Button, Chip, DataList, EmptyState, formatCount, formatLocal, formatRatio, type Row, errorText} from '../ui'
import './TokenUsage.css'

/**
 * トークン消費実績。画面型は一覧・管理系。
 *
 * 1 プロジェクトの**全期間累計**（プロバイダ別・対話セッション別）と、**直近 1 件**の消費値を示す。
 * 直近 1 件は API 応答が返した実績値であり、推定値で代用しない。実績を持たない送信は
 * 「実績なし」と表示して 0 と区別する。
 *
 * 期間別・作業者別の内訳とプロジェクト横断の一覧は AI 利用量ダッシュボードへ渡す。
 * AI を呼ばないため閲覧権限でも参照でき、オフラインでも成立する。
 */

/** 内訳の区分ラベル（空文字 = セッション文脈を持たない送信。ダッシュボードと同じ表記） */
function bucketLabel(key: string): string {
    return key.trim() === '' ? '対話セッション外（取り込み分析など）' : key
}

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

export function TokenUsage({
    projectPath = '',
    onBack,
    backLabel,
    onOpenDashboard,
    embedded = false,
}: {
    /** 対象プロジェクト。空なら開いているプロジェクト（対話画面の共通メニューから開いた場合） */
    projectPath?: string
    onBack: () => void
    /**
     * 埋め込み時の戻る操作のラベル。**戻り先を知っているのは呼び出し元**であり、
     * この画面は複数の経路から開かれるため、語を親から受け取る。
     * 埋め込みでないときは AppShell のナビが戻る操作を持つため、受け取らない（重複させない）。
     */
    /** AI 利用量ダッシュボードへの導線 */
    onOpenDashboard: () => void
} & ({embedded: true; backLabel: string} | {embedded?: false; backLabel?: never})) {
    const [view, setView] = useState<binding.TokenUsageView | null>(null)
    const [error, setError] = useState('')

    useEffect(() => {
        Promise.resolve()
            .then(() => LoadTokenUsage({path: projectPath} as binding.TokenUsageRequest))
            .then((next) => {
                setView(next)
                setError('')
            })
            .catch((err: unknown) => {
                setView(null)
                setError(errorText(err))
            })
    }, [projectPath])

    const latest = view?.latest ?? null

    const body = (
        <section className="rw-token" aria-label="トークン消費実績">
            <header className="rw-token__head">
                <h2 className="rw-token__title">トークン消費実績</h2>
                <div className="rw-token__actions">
                    <Button onClick={onOpenDashboard}>期間別・横断で見る（AI 利用量）</Button>
                    {/* 埋め込みでないときは AppShell のナビが戻る操作を持つ（重複させない）。 */}
                    {embedded ? <Button onClick={onBack}>{backLabel}</Button> : null}
                </div>
            </header>

            {error ? (
                <Banner tone="danger" title={error} onDismiss={() => setError('')} onDefer={() => setError('')} />
            ) : null}

            {view ? (
                <>
                    <dl className="rw-token__figures" aria-label="プロジェクトの累計">
                        <div>
                            <dt>対象システム</dt>
                            <dd>{view.targetSystemName}</dd>
                        </div>
                        <div>
                            <dt>トークン消費合計</dt>
                            <dd className="rw-mono">{formatCount(view.tokens)}</dd>
                        </div>
                        <div>
                            <dt>入力 / 出力 / 推論</dt>
                            <dd className="rw-mono">
                                {formatCount(view.tokensIn)} / {formatCount(view.tokensOut)} /{' '}
                                {formatCount(view.tokensReasoning)}
                            </dd>
                        </div>
                        <div>
                            <dt>送信件数</dt>
                            <dd className="rw-mono">{formatCount(view.sends)}</dd>
                        </div>
                        <div>
                            <dt>トークン実績なしの送信</dt>
                            <dd className="rw-mono">{formatCount(view.missingRecords)}</dd>
                        </div>
                        <div>
                            <dt>直近の利用</dt>
                            <dd className="rw-mono">{formatLocal(view.lastUsedAt ?? '')}</dd>
                        </div>
                        <div>
                            <dt>上限 / 消費率</dt>
                            <dd className="rw-mono">
                                {view.limitTokens === null || view.limitTokens === undefined
                                    ? '未設定'
                                    : `${formatCount(view.limitTokens)} / ${formatRatio(view.consumptionRatio)}`}
                            </dd>
                        </div>
                    </dl>

                    <section className="rw-token__latest" aria-label="直近の対話 1 件">
                        <h3 className="rw-token__subtitle">直近の対話 1 件</h3>
                        {latest ? (
                            <dl className="rw-token__figures">
                                <div>
                                    <dt>日時</dt>
                                    <dd className="rw-mono">{formatLocal(latest.at)}</dd>
                                </div>
                                <div>
                                    <dt>プロバイダ / モデル</dt>
                                    <dd className="rw-mono">
                                        {latest.provider} / {latest.model}
                                    </dd>
                                </div>
                                <div>
                                    <dt>対話セッション</dt>
                                    <dd className="rw-mono">{bucketLabel(latest.session ?? '')}</dd>
                                </div>
                                <div>
                                    <dt>消費（API 応答の実績値）</dt>
                                    <dd className="rw-mono">
                                        {latest.hasTokens ? (
                                            `${formatCount(latest.tokensTotal)}（入力 ${formatCount(latest.tokensIn)} / 出力 ${formatCount(latest.tokensOut)} / 推論 ${formatCount(latest.tokensReasoning)}）`
                                        ) : (
                                            <Chip tone="warn">実績なし</Chip>
                                        )}
                                    </dd>
                                </div>
                            </dl>
                        ) : (
                            <EmptyState
                                message="このプロジェクトではまだ AI を利用していません。"
                                automatic="対話・分析・成果物の生成を行うたびに、実績がここへ記録されます。"
                            />
                        )}
                    </section>

                    <DataList
                        caption="プロバイダ別の累計"
                        columns={BUCKET_COLUMNS}
                        rows={bucketRows(view.byProvider)}
                        empty={{
                            message: 'AI の送信はまだありません。',
                            automatic: '対話・分析・成果物の生成を行うたびに、実績がここへ記録されます。',
                        }}
                    />
                    <DataList
                        caption="対話セッション別の累計"
                        columns={BUCKET_COLUMNS}
                        rows={bucketRows(view.bySession)}
                        empty={{
                            message: 'AI の送信はまだありません。',
                            automatic: '対話・分析・成果物の生成を行うたびに、実績がここへ記録されます。',
                        }}
                    />

                    {/* 集計範囲の併記。共同プロジェクトでは
                        取り込んだ時点までの全メンバーぶんであることを必ず示す。 */}
                    {view.scopeNotice ? (
                        <p className="rw-token__hint" role="status">
                            {view.scopeNotice}
                        </p>
                    ) : null}

                    <p className="rw-token__hint">
                        記録済みの実績から集計します（AI へは送信しません）。期間別・作業者別の内訳と
                        プロジェクト横断の一覧は「AI 利用量」で確認できます。
                    </p>
                </>
            ) : null}
        </section>
    )

    if (embedded) {
        return body
    }
    return (
        <AppShell
            scope="global"
            breadcrumb={['reqweave', 'トークン消費実績']}
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
