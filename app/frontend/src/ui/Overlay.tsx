import {useEffect, useRef} from 'react'
import type {KeyboardEvent as ReactKeyboardEvent, ReactNode} from 'react'
import './Overlay.css'

/**
 * 重ねて出す面（どの画面の型でも共通）。
 *
 * **一時的に見るもの・答えるもの**は作業領域へ**重ねる**。通常の流れへ挿し込むと、
 * 開いた瞬間に作業がそのぶん下へ押し出され、**ウィンドウを広げるかスクロールしないと見えない**
 * （実機で発生。同型が画面ごとに散在していた）。
 *
 * 2 種類だけを持ち、画面ごとに作り込まない:
 *
 * - `popover`（既定）… **背後の操作を妨げない**参照。外側・Esc・「閉じる」で閉じる。
 * - `modal` … **不可逆な操作の着手確認**。背後を暗くし、操作させない。Esc・取消で閉じる。
 *   フォーカスは面の中を回る（背後の要素へ Tab で抜けない）。
 *
 * 置き方:
 *
 * - 既定は**画面に固定して浮かせる**（ペインの中で開いても、ペインの枠に切られない）。
 * - `anchored` は**直前の行の直下**へ重ねる（工程ガイドの全体像。開く操作の真下に出す）。
 *
 * どちらも**流れの中では高さを持たない**（下の作業領域を動かさない）。
 * 高さは画面に収まる上限で頭打ちにし、あふれる分は面の中で送る。
 */

export function Overlay({
    label,
    variant = 'popover',
    anchored = false,
    onClose,
    children,
}: {
    /** 面の名前（読み上げに使う。画面上の見出しと合わせる）。 */
    label: string
    variant?: 'popover' | 'modal'
    /** true のとき、直前の行の直下へ重ねる（既定は画面に固定して浮かせる）。 */
    anchored?: boolean
    /** 閉じる（modal では取消と同じ意味）。 */
    onClose: () => void
    children: ReactNode
}) {
    const panel = useRef<HTMLDivElement | null>(null)
    const modal = variant === 'modal'

    // Esc で閉じる（ネイティブのダイアログは使わない。テーマに追従せず、自動テストもできないため）。
    useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                onClose()
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [onClose])

    // 開いた直後の読み上げ位置を面へ移す（開いたのに読み上げが元の位置に残らないように）。
    useEffect(() => {
        panel.current?.focus()
    }, [])

    /*
     * modal はフォーカスを面の中で回す。背後を操作させない面から Tab で抜けられると、
     * 見えているものと操作しているものが食い違う（読み上げ利用者には気づけない）。
     */
    const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
        if (!modal || event.key !== 'Tab') {
            return
        }
        const focusable = panel.current?.querySelectorAll<HTMLElement>(
            'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href]',
        )
        if (!focusable || focusable.length === 0) {
            return
        }
        const first = focusable[0]
        const last = focusable[focusable.length - 1]
        if (event.shiftKey && document.activeElement === first) {
            event.preventDefault()
            last.focus()
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault()
            first.focus()
        }
    }

    return (
        <div className="rw-overlay-anchor" data-anchored={anchored ? 'true' : undefined}>
            {/*
              * 外側の面。popover は見た目を持たず、押すと閉じる。
              * modal は背後を暗くし、押すと取消になる（背後の操作は通さない）。
              */}
            <div
                className={`rw-overlay__outside rw-overlay__outside--${variant}`}
                aria-hidden="true"
                onClick={onClose}
            />
            <div
                className={`rw-overlay rw-overlay--${variant}`}
                ref={panel}
                tabIndex={-1}
                role={modal ? 'dialog' : 'group'}
                aria-modal={modal ? true : undefined}
                aria-label={label}
                onKeyDown={onKeyDown}
            >
                {children}
            </div>
        </div>
    )
}
