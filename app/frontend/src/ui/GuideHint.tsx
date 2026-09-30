import {createContext, useContext, useEffect} from 'react'

/**
 * 画面内の「次に触る要素」の受け渡し。
 *
 * 工程ガイド（どの工程にいて次はどの工程か）はバックエンドが**永続した事実**から導く。
 * 一方で「いまこの画面のどの要素を操作するか」は、選択中の資料・同意チェックといった
 * **画面の一時状態**で決まる。これはバックエンドが持たないため、画面側から publish する。
 *
 * 表示場所は上部の常設ストリップに一本化しているため、値そのものは AppShell が持ち、
 * ここでは**設定する口だけ**を配る。画面を離れると自動で消える。
 */
const SetGuideHint = createContext<(hint: string) => void>(() => undefined)

export const GuideHintProvider = SetGuideHint.Provider

/**
 * いま開いている画面の「次に触る要素」を 1 つだけ示す。
 *
 * 文言には**画面上の表記をそのまま**含める（「『この内容で分析する』を押します。」）。
 * 内部用語・コード値を出さない。示すことが無いときは空文字を渡す。
 */
export function useGuideHint(hint: string): void {
    const set = useContext(SetGuideHint)
    useEffect(() => {
        set(hint)
        return () => set('')
    }, [hint, set])
}

/**
 * 画面が独立したコンポーネントになっていない場所（画面の中で分岐して描いている場合）から
 * publish するための薄い包み。描くものは持たない。
 */
export function ScreenHint({hint}: {hint: string}) {
    useGuideHint(hint)
    return null
}
