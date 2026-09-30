import {useCallback, useEffect, useLayoutEffect, useRef} from 'react'
import type {DependencyList, RefObject} from 'react'

/**
 * スクロールする履歴を「最新の発話が見える位置」に保つ。
 *
 * 新しい質問が履歴の末尾に追加されても履歴が古い位置のままだと、利用者は
 * 「質問が表示されない」と受け取る（0.1.4 の実機で判明）。
 *
 * - 内容が増えたとき（deps が変わったとき）、**利用者が末尾付近を見ていれば**末尾へ送る。
 * - 利用者が上へ戻って過去の発話を読んでいる間は送らない（応答待ちの間も履歴の閲覧を妨げない）。
 * - 返す関数 `follow()` を呼ぶと、その場で末尾へ送り、以後も末尾に付いていく。
 *   利用者自身の操作（「次の質問」「送信」）の直後に呼ぶ。操作した本人は結果を見に行くため。
 * - 開いた直後は末尾に付いた状態から始める（最初に見えるのが最新の発話になる）。
 */
export function useFollowLatest(ref: RefObject<HTMLElement | null>, deps: DependencyList): () => void {
    // 末尾に付いていくか。利用者のスクロールで切り替わる。
    const following = useRef(true)

    useEffect(() => {
        const el = ref.current
        if (!el) {
            return
        }
        const onScroll = () => {
            following.current = distanceFromBottom(el) <= NEAR_BOTTOM_PX
        }
        el.addEventListener('scroll', onScroll)
        return () => el.removeEventListener('scroll', onScroll)
    }, [ref])

    // 描画のあと・画面に出る前に送る（古い位置が一瞬見えるのを避ける）。
    useLayoutEffect(() => {
        const el = ref.current
        if (el && following.current) {
            el.scrollTop = el.scrollHeight
        }
        // deps は呼び出し側が「内容が増えた」ことを表す値を渡す。
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, deps)

    return useCallback(() => {
        following.current = true
        const el = ref.current
        if (el) {
            el.scrollTop = el.scrollHeight
        }
    }, [ref])
}

/** 末尾に付いているとみなす距離。行の途中で止まった程度のずれは末尾扱いにする。 */
const NEAR_BOTTOM_PX = 48

function distanceFromBottom(el: HTMLElement): number {
    return el.scrollHeight - el.scrollTop - el.clientHeight
}
