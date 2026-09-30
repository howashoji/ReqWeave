import {useEffect, useState} from 'react'
import type {CSSProperties} from 'react'
import './FlyingCard.css'

/**
 * 抽出結果が中央から右ペインへ「移った」ことを、言葉を使わずに伝える演出。
 *
 * 送った回答が候補になって右へ並ぶ、という因果関係は、現状のUIでは送信してみるまで分からない。
 * そこで到着の瞬間に小さなカードを 1 つだけ飛ばす。
 *
 * - **飛ばすのは 1 つだけ**。候補が 3 件でも 3 つ飛ばさない（うるさくなる）
 * - 初回だけでなく**毎回**出す（初回限定だと読み飛ばしたときの救済が無い）
 * - `prefers-reduced-motion: reduce` では飛ばさない。**演出が無くても機能は損なわれない**
 *   （カードの到着そのものは右ペインの差し替えで分かる）
 *
 * 装飾でしかないため読み上げ・フォーカス順から外す。
 */
export function FlyingCard({from, to, onDone}: {from: DOMRect; to: DOMRect; onDone: () => void}) {
    const [arrived, setArrived] = useState(false)

    useEffect(() => {
        // 初回の描画位置を出発点として確定させてから、次のフレームで着地位置へ動かす。
        const raf = requestAnimationFrame(() => setArrived(true))
        const timer = setTimeout(onDone, 320)
        return () => {
            cancelAnimationFrame(raf)
            clearTimeout(timer)
        }
    }, [onDone])

    const rect = arrived ? to : from
    const width = Math.min(from.width, 160)
    return (
        <span
            className="rw-fly"
            aria-hidden="true"
            /* 位置と大きさの「値」だけを渡す。見た目の指定は CSS 側に置く（BD 実装規約）。 */
            style={{'--rw-fly-x': `${rect.left}px`, '--rw-fly-y': `${rect.top}px`, '--rw-fly-w': `${width}px`} as CSSProperties}
        />
    )
}

/** 動きを減らす設定かどうか（環境が答えられないときは「減らさない」に倒す）。 */
export function prefersReducedMotion(): boolean {
    return typeof window.matchMedia === 'function'
        ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
        : false
}
