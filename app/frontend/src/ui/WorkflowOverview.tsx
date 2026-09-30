import {Button} from './Button'
import {Overlay} from './Overlay'
import './WorkflowOverview.css'

/**
 * 工程の全体像。
 *
 * 工程ガイドは**いまの 1 段**しか示さないため、
 * 「この先に何があるのか」「いま見ている画面は全体のどこなのか」が分からなかった。
 * ここでは 6 工程を並べ、**現在地・その工程の目的・使う画面**を 1 か所で見せる。
 *
 * - **工程の説明の正本はバックエンド**（`internal/guide` の `Stages`）。画面側で言い回しを作らない。
 *   紹介スライドはアプリ全体の考え方の案内であり、工程の一覧を持たない（ここと住み分ける）。
 * - **作業領域に重ねて出す**（流れへ挿し込むと作業領域が押し下げられる）。置き方・閉じ方・高さの頭打ちは
 *   共通の `Overlay`（`anchored` = 開く操作の真下）に持たせ、ここでは中身だけを書く。
 * - **現在地は色だけで示さない**（記号と語を併せる）。
 * - **順序を強制しない**（工程は行き来してよい）。上から順に進める決まりが無いことを明記する。
 * - 行き先を知らない工程は移動操作を出さない（押しても何も起きないボタンを作らない）。
 */

export type WorkflowStage = {
    id: string
    label: string
    /** その工程で何をするかの 1 文（バックエンドが持つ）。 */
    purpose: string
    /** その工程で使う画面の名前（画面上の表記のまま）。 */
    screen: string
    /** そこへ移る行き先（画面側の view 名）。 */
    target: string
}

export function WorkflowOverview({
    stages,
    currentStageID,
    onMove,
    onClose,
}: {
    stages: WorkflowStage[]
    /** いまの工程。並びに無い ID のときは、どの行にも印を付けない。 */
    currentStageID: string
    /** 行き先へ移る。移れない行き先のときは undefined を返す（操作を出さない）。 */
    onMove: (target: string) => (() => void) | undefined
    onClose: () => void
}) {
    return (
        <Overlay label="工程の全体像" anchored onClose={onClose}>
                <div className="rw-overview__head">
                    <h2 className="rw-overview__title">工程の全体像</h2>
                    <p className="rw-overview__lead">
                        上から順に進める決まりはありません。必要な工程から始められます。
                    </p>
                    <Button variant="quiet" onClick={onClose}>
                        閉じる
                    </Button>
                </div>
                <ol className="rw-overview__list">
                    {stages.map((stage, i) => {
                        const here = stage.id === currentStageID
                        const move = onMove(stage.target)
                        return (
                            <li
                                key={stage.id}
                                className="rw-overview__stage"
                                data-state={here ? 'current' : 'other'}
                            >
                                <span className="rw-overview__no rw-mono">
                                    {i + 1}/{stages.length}
                                </span>
                                <span className="rw-overview__body">
                                    <span className="rw-overview__name">
                                        {stage.label}
                                        {here ? (
                                            <span className="rw-overview__here">（いまここ）</span>
                                        ) : null}
                                    </span>
                                    <span className="rw-overview__purpose">{stage.purpose}</span>
                                    <span className="rw-overview__screen">
                                        使う画面: {stage.screen}
                                    </span>
                                </span>
                                {move ? (
                                    <Button
                                        variant="secondary"
                                        onClick={() => {
                                            move()
                                            onClose()
                                        }}
                                    >
                                        {stage.screen}を開く
                                    </Button>
                                ) : null}
                            </li>
                        )
                    })}
                </ol>
        </Overlay>
    )
}
