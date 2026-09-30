import {cloneElement, useId, useLayoutEffect, useRef, useState} from 'react'
import type {KeyboardEvent, ReactElement} from 'react'
import './Tooltip.css'

/**
 * ツールチップ。短いラベルだけでは役割が伝わらない操作へ、機能を示す 1 文を添える。
 *
 * - **アプリ内の要素で描く**。ブラウザ標準の `title` 属性に頼らない。
 *   表示までの遅延が大きく、テーマに追従せず、表示内容を自動テストで検証できないため。
 * - **マウス専用にしない**。ポインタを重ねたときに加えて、キーボードのフォーカスでも表示する。
 *   読み上げには `aria-describedby` で同じ文を渡す（視覚表示と読み上げ内容を一致させる）。
 * - **操作の代わりにしない**。ここにしか無い情報を置かない。読めなくても操作は完了できる。
 *
 * 対象要素は `aria-describedby` を受け取れる 1 要素であること（`Button` を想定）。
 * 無効化されたボタンはポインタ事象を発しないため、包む要素側で受け取る（`Tooltip.css` の `pointer-events`）。
 *
 * 既定は左揃えで開く。**上部ナビの右端のボタン**では画面外へはみ出すため、
 * 開いた直後に幅を測り、はみ出すときだけ右揃えへ倒す（読めない吹き出しを出さない）。
 */
type Props = {
    /** 機能を示す 1 文。内部用語・コード値を出さない */
    text: string
    children: ReactElement<{'aria-describedby'?: string}>
}

export function Tooltip({text, children}: Props) {
    const [open, setOpen] = useState(false)
    const [alignEnd, setAlignEnd] = useState(false)
    const bubble = useRef<HTMLSpanElement>(null)
    const id = useId()

    useLayoutEffect(() => {
        if (!open) {
            setAlignEnd(false)
            return
        }
        const el = bubble.current
        if (!el) {
            return
        }
        setAlignEnd(el.getBoundingClientRect().right > window.innerWidth)
    }, [open])

    return (
        <span
            className="rw-tooltip"
            onMouseEnter={() => setOpen(true)}
            onMouseLeave={() => setOpen(false)}
            onFocus={() => setOpen(true)}
            onBlur={() => setOpen(false)}
            onKeyDown={(e: KeyboardEvent) => {
                if (e.key === 'Escape') {
                    setOpen(false)
                }
            }}
        >
            {cloneElement(children, {'aria-describedby': open ? id : undefined})}
            {open ? (
                <span
                    id={id}
                    ref={bubble}
                    role="tooltip"
                    className={`rw-tooltip__bubble${alignEnd ? ' rw-tooltip__bubble--end' : ''}`}
                >
                    {text}
                </span>
            ) : null}
        </span>
    )
}
