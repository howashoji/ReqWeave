import {useEffect} from 'react'
import type {StateTone} from './state'
import {Button} from './Button'
import './Toast.css'

/**
 * 一過性の操作フィードバック（パネル・バナー系の一過性表示）。
 *
 * 保存・コピーのような**その場で終わる操作の結果**に使う。恒久的な状態の説明
 * （権限不足・接続不能・集計不能など、次の操作まで残る事実）はインラインバナー（Banner）に置く。
 * 画面下部へ重ねるのは、縦に長い一覧画面で上端のインライン表示が視界に入らないため。
 *
 * 表示形式はパネル・バナー系に揃える（左ボーダー色で種別・影を使わない・
 * 閉じる導線を必ず持つ）。自動で消えるが、消える前に閉じる操作もできる
 * （自動処理にも利用者が手を出せる手段を残す）。
 */
export function Toast({
    tone,
    message,
    onDismiss,
    durationMs = 6000,
}: {
    tone: StateTone
    message: string
    onDismiss: () => void
    /** 自動で閉じるまでの時間。0 以下なら自動で閉じない */
    durationMs?: number
}) {
    useEffect(() => {
        if (durationMs <= 0) {
            return
        }
        const timer = setTimeout(onDismiss, durationMs)
        return () => clearTimeout(timer)
    }, [durationMs, message, onDismiss])

    return (
        <div className={`rw-toast rw-toast--${tone}`} data-tone={tone} role="status" aria-live="polite">
            <p className="rw-toast__message">{message}</p>
            <Button variant="quiet" onClick={onDismiss} aria-label="通知を閉じる">
                閉じる
            </Button>
        </div>
    )
}
