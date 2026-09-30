import type {ReactNode} from 'react'
import type {StateTone} from './state'
import {Button} from './Button'
import './Banner.css'

/**
 * パネル・バナー系。
 *
 * 呼び出し元画面の文脈を保ったまま重ねる（全画面遷移にしない）。
 * 種別は左ボーダー色で示す: 情報・候補 = info / 警告・上限接近 = warn /
 * エラー・上限到達 = danger / 承認待ち候補 = accent。
 * **閉じる・あとで見る導線を必ず持つ**（出しっぱなしで作業を塞がない。省略可能な prop にしない）。
 */
export function Banner({
    tone,
    title,
    children,
    onDismiss,
    onDefer,
    deferLabel = 'あとで見る',
}: {
    tone: StateTone
    title: string
    children?: ReactNode
    onDismiss: () => void
    onDefer: () => void
    deferLabel?: string
}) {
    return (
        <section className={`rw-banner rw-banner--${tone}`} data-tone={tone} role="status">
            <div className="rw-banner__body">
                <p className="rw-banner__title">{title}</p>
                {children ? <div className="rw-banner__detail">{children}</div> : null}
            </div>
            <div className="rw-banner__actions">
                <Button variant="quiet" onClick={onDefer}>
                    {deferLabel}
                </Button>
                <Button variant="quiet" onClick={onDismiss} aria-label="閉じる">
                    閉じる
                </Button>
            </div>
        </section>
    )
}
