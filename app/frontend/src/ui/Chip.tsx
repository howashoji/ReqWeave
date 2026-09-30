import type {ReactNode} from 'react'
import type {StateTone} from './state'
import './Chip.css'

/**
 * 状態チップ。
 * 「状態色の文字 + 淡い同系背景 + 同系枠線」の 3 点セットで統一する。
 * 状態ラベルはモノスペース。
 */
export function Chip({tone, children}: {tone: StateTone; children: ReactNode}) {
    return (
        <span className={`rw-chip rw-chip--${tone} rw-mono`} data-tone={tone}>
            {children}
        </span>
    )
}
