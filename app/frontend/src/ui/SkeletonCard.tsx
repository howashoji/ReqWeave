import './SkeletonCard.css'

/**
 * 抽出中に「これから何が並ぶか」を形で示すカード。
 *
 * 待ち時間を**説明文を読ませる時間ではなく、結果の型を先に見せる時間**として使う。
 * そのため実際の候補カードと**外形（左ボーダー・地色・余白）を共有**し、
 * **承認 / 破棄の輪郭まで描く**。ここが「何をする場所か」を伝える最重要の部分であり、
 * 中身のプレースホルダだけでは「読む場所」に見えてしまう。
 *
 * 輪郭は操作ではないため `aria-hidden` とし、読み上げ・フォーカス順から外す
 * （押せないものをフォーカスさせない）。
 * 状況は包む側が 1 か所で伝える（カードごとに読み上げない）。
 */
export function SkeletonCard() {
    return (
        <div className="rw-skeleton" aria-hidden="true">
            <span className="rw-skeleton__bar rw-skeleton__bar--title" />
            <span className="rw-skeleton__bar" />
            <span className="rw-skeleton__bar rw-skeleton__bar--short" />
            <span className="rw-skeleton__marks">
                <span className="rw-skeleton__ghost">承認</span>
                <span className="rw-skeleton__ghost">破棄</span>
            </span>
        </div>
    )
}
