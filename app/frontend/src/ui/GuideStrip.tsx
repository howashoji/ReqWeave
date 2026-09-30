import {useState} from 'react'
import {Button} from './Button'
import {WorkflowOverview} from './WorkflowOverview'
import type {WorkflowStage} from './WorkflowOverview'
import './GuideStrip.css'

/**
 * 工程ガイド。
 *
 * 3 層構造のコンテキストストリップに、
 * **いまどの工程にいるか**と**次にやること**を常時出す。
 *
 * 「次にやること」は 2 系統ある:
 *
 * - `hint` … いま開いている画面で次に触る要素（画面の一時状態から。画面側が publish する）
 * - `next` … 画面をまたぐ次の工程（プロジェクトの永続状態から。バックエンドが導く）
 *
 * **手を動かす先が最初に目に入る**よう、`hint` があるときはそちらを見せ、
 * 別画面へ移る操作は引っ込める（いまの手を止めさせない）。
 * ただし `note`（ほかにも取れる道）は**常に出す**。そこにしか無い選択肢であり、
 * 引っ込めると「関係者に聞く」という道が画面のどこからも見えなくなる。
 * 順序は強制しない（工程は行き来してよく、他の操作を妨げない）。
 */

export type GuideAction = {label: string; onClick: () => void}

/**
 * 1 サイクルの流れ。
 *
 * 工程ガイド（どの工程にいるか）よりも内側の、**対話 1 往復の流れ**を示す。
 * 利用者の語彙で書き、内部の処理名を出さない。
 */
export const CYCLE_STEPS = ['質問', '回答', '抽出', '承認', '確定'] as const

export type CycleStep = (typeof CYCLE_STEPS)[number]

export type GuideStripProps = {
    stageLabel: string
    /** 工程の位置（1 始まり）と総数。「いま何合目か」を数で示す。 */
    stageIndex: number
    stageTotal: number
    /** 画面をまたぐ次の一手（1 文）。 */
    next: string
    /** その工程の画面へ移る操作。 */
    action?: GuideAction
    /** ほかにも取れる道（任意）。 */
    note?: string
    noteAction?: GuideAction
    /** いま開いている画面で次に触る要素（画面側が publish したもの）。 */
    hint?: string
    /**
     * 1 サイクルの現在地。対話の画面でだけ渡す。
     * 渡さないときはステッパーを出さない（関係の無い画面で縦を使わない）。
     */
    cycleStep?: CycleStep
    /**
     * 工程の並び。全体像を開く操作を出すために要る。
     * 取得できていないときは渡さない（開いても空の一覧しか出ないため操作を出さない）。
     */
    stages?: WorkflowStage[]
    /** いまの工程の ID（全体像で現在地に印を付ける）。 */
    stageId?: string
    /** 工程の画面へ移る手を作る。移れない行き先には undefined を返す。 */
    onMove?: (target: string) => (() => void) | undefined
}

export function GuideStrip({
    stageLabel,
    stageIndex,
    stageTotal,
    next,
    action,
    note,
    noteAction,
    hint,
    cycleStep,
    stages,
    stageId,
    onMove,
}: GuideStripProps) {
    const onThisScreen = Boolean(hint)
    // 全体像は**開いている間だけ**出す（常設すると縦を占め、いまの一手が押し出される）。
    const [overview, setOverview] = useState(false)
    // 並びと移動手段の両方が揃って初めて開ける（開いても何もできない状態を作らない）。
    const canOpenOverview = Boolean(stages && stages.length > 0 && onMove)
    return (
        <>
        <div className="rw-guide" aria-label="いまの工程と次にやること" data-cycle={cycleStep}>
            <span className="rw-guide__stage">
                <span className="rw-guide__step rw-mono">
                    {stageIndex}/{stageTotal}
                </span>
                {stageLabel}
            </span>
            {/* どの画面からも 1 操作で全体像へ */}
            {canOpenOverview ? (
                <Button
                    variant="secondary"
                    aria-expanded={overview}
                    onClick={() => setOverview((open) => !open)}
                    tooltip="要件定義の 6 工程と、いまどこにいるかを一覧で見ます。"
                >
                    工程の全体像
                </Button>
            ) : null}
            <span className="rw-guide__next">
                <span className="rw-guide__label">次にやること</span>
                {onThisScreen ? hint : next}
            </span>
            {/* 画面内でやることがあるうちは、別の画面へ誘わない（手が止まる） */}
            {onThisScreen || !action ? null : (
                <Button variant="secondary" onClick={action.onClick}>
                    {action.label}
                </Button>
            )}
            {/* ほかにも取れる道は常に出す（引っ込めると、その道が画面から消える） */}
            {note ? <span className="rw-guide__note">{note}</span> : null}
            {note && noteAction ? (
                <Button variant="secondary" onClick={noteAction.onClick}>
                    {noteAction.label}
                </Button>
            ) : null}
            {cycleStep ? <CycleStepper current={cycleStep} /> : null}
        </div>
        {overview && stages && onMove ? (
            <WorkflowOverview
                stages={stages}
                currentStageID={stageId ?? ''}
                onMove={onMove}
                onClose={() => setOverview(false)}
            />
        ) : null}
        </>
    )
}

/**
 * 1 サイクルの流れと現在地。表示だけで、押しての移動は持たない。
 *
 * 現在地は**色だけで示さない**（記号「▶」と読み上げ用の語を併せる）。
 */
function CycleStepper({current}: {current: CycleStep}) {
    const at = CYCLE_STEPS.indexOf(current)
    return (
        <span className="rw-cycle" aria-label={`いまのやり取りの段階: ${current}`}>
            {CYCLE_STEPS.map((step, i) => (
                <span
                    key={step}
                    className="rw-cycle__step"
                    data-state={i === at ? 'current' : i < at ? 'done' : 'ahead'}
                >
                    {i > 0 ? <span className="rw-cycle__sep" aria-hidden="true">▶</span> : null}
                    {step}
                    {i === at ? <span className="rw-cycle__here">（いまここ）</span> : null}
                </span>
            ))}
        </span>
    )
}
