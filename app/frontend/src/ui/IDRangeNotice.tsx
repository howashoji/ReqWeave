import {useState} from 'react'
import type {binding} from '../../wailsjs/go/models'
import {Banner} from './Banner'
import {Button} from './Button'

/**
 * ID の番号帯の残量の警告（残りが少ない・使い切ったことを知らせる）。
 * パネル・バナー系。
 *
 * 共同プロジェクトでは新しい ID を**自分の番号帯**から採番する。番号帯は反映のときにまとめて
 * 確保されるため、残りが少なくなったら同期（取り込み・反映）へ誘導する。
 *
 * 判定（残りが少ない / 使い切った）と文言はバインディングが返した値をそのまま使う
 * （画面側で閾値を持たない）。番号帯を使わないプロジェクトでは空が返るため何も出さない。
 */
export function IDRangeNotice({warnings, onOpenSync}: {warnings: binding.IDRangeWarningView[] | null; onOpenSync?: () => void}) {
    // 閉じた通知は同じ種別について出し直さない（残量は操作のたびに再評価されるため）。
    const [dismissed, setDismissed] = useState<string[]>([])
    const shown = (warnings ?? []).filter((w) => !dismissed.includes(w.kindLabel))
    if (shown.length === 0) {
        return null
    }
    return (
        <>
            {shown.map((w) => (
                <Banner
                    key={w.kindLabel}
                    tone={w.exhausted ? 'danger' : 'warn'}
                    title={
                        w.exhausted
                            ? `${w.kindLabel}の番号を使い切りました`
                            : `${w.kindLabel}の番号の残りが少なくなっています（残り ${w.remaining} 件）`
                    }
                    onDismiss={() => setDismissed((prev) => [...prev, w.kindLabel])}
                    onDefer={() => setDismissed((prev) => [...prev, w.kindLabel])}
                >
                    <p>{w.message}</p>
                    {onOpenSync ? <Button onClick={onOpenSync}>同期を開く</Button> : null}
                </Banner>
            ))}
        </>
    )
}
