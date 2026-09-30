import {useEffect, useState} from 'react'
import {CodexPlanUsage} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {DataList} from './DataList'
import {formatLocal, formatRatio} from './format'
import './PlanUsage.css'

/**
 * ChatGPT のプランの残量（参考）。
 *
 * 設定と AI 利用量ダッシュボードの**両方で同じ値・同じ様式**を出す。
 *
 * - **表示のための通信をしない**。バックエンドは直近の AI 呼び出しで受け取った値を返すだけで、
 *   この画面を開いても取りに行かない。
 * - シークレットキー方式では値が届かないため**欄ごと出さない**（`available` が false）。
 * - まだ受け取っていないときは黙って空欄にせず、その旨と次の行動を出す（`notice`）。
 * - 時刻は UTC の ISO 8601 で届く。表示はローカルへ変換する。
 */

const WINDOW_COLUMNS = [
    {key: 'label', label: '枠'},
    {key: 'used', label: '使用率', mono: true},
    {key: 'duration', label: '枠の長さ', mono: true},
    {key: 'resets', label: '次の回復', mono: true},
]

export function PlanUsage({
    label,
    note,
}: {
    /** 対象のプロバイダ設定の表示名（空文字＝この端末の Codex App Server の設定）。 */
    label: string
    /** 画面ごとに添える補足（例: トークン上限の判定には使わない旨）。 */
    note?: string
}) {
    const [view, setView] = useState<binding.PlanUsageView | null>(null)

    /*
     * 取得は通信を起こさない（保持している値を返すだけ）。取得できない場合も
     * 画面の他の機能を止めない（残量は参考情報）。
     */
    useEffect(() => {
        Promise.resolve()
            .then(() => CodexPlanUsage(label))
            .then((next) => setView(next))
            .catch(() => setView(null))
    }, [label])

    if (!view || !view.available) {
        return null
    }

    const rows = (view.windows ?? []).map((w, i) => ({
        id: `${i}-${w.label}`,
        cells: {
            label: w.label,
            used: formatRatio(w.usedPercent / 100),
            duration: w.windowLabel ? w.windowLabel : '—',
            resets: formatLocal(w.resetsAt ?? ''),
        },
    }))

    return (
        <section className="rw-planusage" aria-label="ChatGPT のプランの残量">
            <h3 className="rw-planusage__title">ChatGPT のプランの残量（参考）</h3>
            {view.fetched ? (
                <>
                    <dl className="rw-planusage__figures">
                        <div>
                            <dt>プラン</dt>
                            <dd>{view.planLabel ? view.planLabel : '不明'}</dd>
                        </div>
                        <div>
                            <dt>受け取った時刻</dt>
                            <dd className="rw-mono">{formatLocal(view.receivedAt ?? '')}</dd>
                        </div>
                    </dl>
                    <DataList
                        caption="プランの利用枠"
                        columns={WINDOW_COLUMNS}
                        rows={rows}
                        empty={{
                            message: '利用枠の内訳は届いていません。',
                            next: '次に AI を使う操作を行うと、枠ごとの使用率が表示されます。',
                        }}
                    />
                </>
            ) : (
                <p className="rw-planusage__hint" role="status">
                    {view.notice
                        ? view.notice
                        : 'まだ取得していません（AI を使うと表示されます）'}
                </p>
            )}
            <p className="rw-planusage__hint">
                値は直近の AI 呼び出しで受け取ったものです（この画面を開いても取りに行きません）。
                {note ? `　${note}` : ''}
            </p>
        </section>
    )
}
