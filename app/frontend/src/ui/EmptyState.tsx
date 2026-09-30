import {Button} from './Button'
import './EmptyState.css'

/**
 * 空状態（どの画面の型でも共通）。
 *
 * 一覧・ペインが空になったとき、**いま何が無いか**だけを書くと画面が行き止まりになる。
 * 「押せる要素が無く、ここから何をどう進めたらいいのか分からない」という報告が
 * 対話画面・取り込み画面と続いたため、**次の一手を型で必須**にする。
 *
 * 2 通りのどちらかを必ず選ぶ:
 *
 * - `next`  … 利用者が取れる操作がある。**押すものの名前**をそのまま書く
 *             （「『生成する』で成果物を作成してください。」）。別の画面へ行くなら `action` も付ける。
 * - `automatic` … 利用者の操作では増えない（自動で増える／参照専用の集計）。
 *             **そうと分かる説明**を書く（「プロジェクトを閉じるたびに作成されます。」）。
 *
 * 区画の中の小さな内訳（「なし」等）は本コンポーネントの対象外。画面・ペインが空になる場面で使う。
 */
export type EmptyStateProps = {
    /** いま何が無いか。 */
    message: string
} & (
    | {
          /** 次に何をすると埋まるか。押すものの名前で書く。 */
          next: string
          /** 次の操作が別の画面にあるときの導線。 */
          action?: {label: string; onClick: () => void}
          automatic?: never
      }
    | {
          /** 利用者の操作では増えないもの。増える条件・参照専用である旨を書く。 */
          automatic: string
          next?: never
          action?: never
      }
)

export function EmptyState(props: EmptyStateProps) {
    const detail = props.next ?? props.automatic
    // 静的な説明であり、変化を読み上げる領域ではないため role="status" を付けない
    // （画面に元からある文。live region にすると操作結果の通知と混ざる）。
    return (
        <div className="rw-empty">
            <p className="rw-empty__message">{props.message}</p>
            <p className="rw-empty__detail">{detail}</p>
            {props.action ? (
                <Button variant="secondary" onClick={props.action.onClick}>
                    {props.action.label}
                </Button>
            ) : null}
        </div>
    )
}
