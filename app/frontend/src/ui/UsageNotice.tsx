import type {binding} from '../../wailsjs/go/models'
import {Banner} from './Banner'
import {Button} from './Button'
import './UsageNotice.css'

/**
 * トークン上限の警告・停止表示（パネル・バナー系）。
 *
 * AI 呼び出しを伴う画面へ置く。表示するかどうかと種別は、バインディングが返した
 * `UsageStatus.level` だけで決める（画面側で上限判定をやり直さない。判定はバインディングの前段の 1 か所に置く）。
 *
 *   none    … 上限未設定、または警告閾値未満 → 何も出さない
 *   warn    … 警告閾値以上・上限未満 → 橙。**AI 呼び出しの開始操作は妨げない**
 *   blocked … 上限到達 → 赤。開始できない理由・再開の方法・ダッシュボードへの導線
 *
 * 色以外の手掛かり（見出しの「警告」「停止」と本文）で 2 つの状態を区別する（色だけに頼らない）。
 * 共同プロジェクトでは**集計範囲の併記**を本文へ添える
 * （上限の判定も取り込んだ時点までの範囲で行うため、取り込みで初めて超過が分かりうる）。
 */

/** 3 桁区切り（表示整形は画面と同じ規則。ICU のロケール実装に依存させない） */
function count(value: number): string {
    return String(Math.trunc(value)).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

export function UsageNotice({
    status,
    onOpenDashboard,
    onDismiss,
}: {
    status: binding.UsageStatus | null
    /** AI 利用量ダッシュボードへ。上限到達時の確認先 */
    onOpenDashboard: () => void
    onDismiss: () => void
}) {
    if (!status || (status.level !== 'warn' && status.level !== 'blocked')) {
        return null
    }
    const remaining = status.remainingTokens ?? 0
    const limit = status.limitTokens ?? 0

    if (status.level === 'warn') {
        return (
            <Banner
                tone="warn"
                title={`警告: AI 利用量が上限に近づいています（上限まで残り ${count(remaining)} トークン）`}
                onDismiss={onDismiss}
                onDefer={onDismiss}
            >
                <p className="rw-usage-notice__text">
                    これまでの消費は {count(status.consumedTokens)} トークン（上限 {count(limit)}）です。
                    AI を使う操作はこのまま続けられます。
                </p>
                {status.scopeNotice ? <p className="rw-usage-notice__text">{status.scopeNotice}</p> : null}
                <div className="rw-usage-notice__actions">
                    <Button onClick={onOpenDashboard}>AI 利用量を確認する</Button>
                </div>
            </Banner>
        )
    }
    return (
        <Banner
            tone="danger"
            title="停止: AI の利用量がこのプロジェクトの上限に達したため、AI を使う操作を開始できません。"
            onDismiss={onDismiss}
            onDefer={onDismiss}
        >
            <p className="rw-usage-notice__text">
                これまでの消費は {count(status.consumedTokens)} トークン（上限 {count(limit)}）です。
                オーナーが上限を変更すると再開できます。参照・手動編集・進捗レポート・エクスポート・
                利用量の表示など、AI を使わない操作は続けられます。
            </p>
            {status.scopeNotice ? <p className="rw-usage-notice__text">{status.scopeNotice}</p> : null}
            <div className="rw-usage-notice__actions">
                <Button onClick={onOpenDashboard}>AI 利用量を確認する</Button>
            </div>
        </Banner>
    )
}
