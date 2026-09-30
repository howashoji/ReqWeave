import type {ButtonHTMLAttributes} from 'react'
import {Tooltip} from './Tooltip'
import './Button.css'

/**
 * 操作ボタン。
 *
 * - `primary`: 緑の塗り。**1 画面に 1 つだけ**（確定・承認などの主操作）。
 * - `secondary`: 枠線。破壊的でない従属操作。
 * - `quiet`: テキストのみ。取消系。
 *
 * 状態色は形で役割を区別する（緑の塗り = 操作 / チップ = 状態表示）。
 */
export type ButtonVariant = 'primary' | 'secondary' | 'quiet'

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
    variant?: ButtonVariant
    /** 無効化の理由。無効時は必ず示す（押せない理由が分からないまま止めない） */
    disabledReason?: string
    /**
     * 機能を示す 1 文。渡すとアプリ内の要素で描くツールチップになる。
     * 適用範囲はまず上部ナビ。
     */
    tooltip?: string
}

export function Button({
    variant = 'secondary',
    disabledReason,
    disabled,
    tooltip,
    children,
    ...rest
}: Props) {
    const isDisabled = disabled ?? Boolean(disabledReason)
    // 無効化されているときは理由を優先し、機能説明と二重に出さない。
    const hint = isDisabled ? (disabledReason ?? tooltip) : tooltip
    const button = (
        <button
            type="button"
            className={`rw-button rw-button--${variant}`}
            data-variant={variant}
            disabled={isDisabled}
            {...rest}
        >
            {children}
        </button>
    )
    // 理由も機能説明もアプリ内の要素で描き、`title` 属性は使わない。
    // **無効化の理由は段階適用の対象外**: 無効化された要素は
    // ポインタ事象を発しないため、`title` に載せた理由は画面のどこにも出ない（実際にそれで見逃した）。
    if (hint === undefined) {
        return button
    }
    return <Tooltip text={hint}>{button}</Tooltip>
}
