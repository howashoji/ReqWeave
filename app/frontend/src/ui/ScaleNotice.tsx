import {useState} from 'react'
import type {binding} from '../../wailsjs/go/models'
import {Banner} from './Banner'

/**
 * 規模上限の警告・予告表示（パネル・バナー系）。
 *
 * トークン上限（UsageNotice）と違い、**操作を止めない**。上限は性能保証の前提であり、
 * 到達しても保存・追加は続けられる。表示するかどうかと種別はバインディングが返した
 * `level` だけで決める（画面側で上限判定をやり直さない）。
 *
 *   warn     … 予告閾値（既定 80%）以上・上限未満 → 橙
 *   exceeded … 上限到達 → 赤。ただし「操作は続けられる」ことを本文で明示する
 *
 * 色以外の手掛かり（見出しの「予告」「上限到達」と本文）で 2 つの状態を区別する（色だけに頼らない）。
 */
export function ScaleNotice({warnings}: {warnings: binding.ScaleWarning[] | null}) {
    // 閉じた通知は同じ対象については出し直さない（保存のたびに再評価するため、
    // 閉じなければ操作のたびに再表示されてしまう）。
    const [dismissed, setDismissed] = useState<string[]>([])
    if (!warnings || warnings.length === 0) {
        return null
    }
    const shown = warnings.filter(
        (w) => (w.level === 'warn' || w.level === 'exceeded') && !dismissed.includes(w.kind),
    )
    if (shown.length === 0) {
        return null
    }
    return (
        <>
            {shown.map((w) => (
                <Banner
                    key={w.kind}
                    tone={w.level === 'exceeded' ? 'danger' : 'warn'}
                    title={
                        w.level === 'exceeded'
                            ? `上限到達: ${w.label}が想定の規模に達しました`
                            : `予告: ${w.label}が想定の規模に近づいています`
                    }
                    onDismiss={() => setDismissed((prev) => [...prev, w.kind])}
                    onDefer={() => setDismissed((prev) => [...prev, w.kind])}
                >
                    <p>{w.message}</p>
                </Banner>
            ))}
        </>
    )
}
