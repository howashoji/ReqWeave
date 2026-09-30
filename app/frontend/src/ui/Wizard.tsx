import {createContext, useCallback, useContext, useEffect, useRef, useState} from 'react'
import type {PointerEvent as ReactPointerEvent, KeyboardEvent as ReactKeyboardEvent, ReactNode} from 'react'
import {Button} from './Button'
import './Wizard.css'

/**
 * ウィザード・回答系。
 *
 * 1 画面 1 目的の順次進行。**担当者向け統制表示（完成度・消費量・モデル等）を出さない**
 * （回答に集中させるため）。そのため本コンポーネントは AppShell の内側では使わず、単独で画面全体を占める。
 * 本文 15px 基準・進捗（n / 総数）表示・フッターに戻る / 進むを固定配置。
 * 自動保存など「失敗しても失われない」ことの明示を伴う。
 *
 * **下部ペイン（回答モードの AI 対話ペイン）**: `pane` を渡すと本文の下に
 * 上下分割のペインを出す。**本文を押し下げない**（本文とペインで高さを分け合い、フッターの
 * 主操作は常に画面内に残る）。分割の数値は下の定数に置く。
 */

/** 分割の数値。 */
const PANE_DEFAULT_RATIO = 0.4 // 既定はペインへ作業領域の 40%（許容 35〜50%）
const PANE_MIN_PX = 200 // ペインの最小高さ
const BODY_MIN_PX = 320 // 上側（質問・背景説明・回答欄）の最小高さ
const RESIZER_PX = 10 // 境界のつまみ
/** キーボードで動かす 1 回ぶん。 */
const PANE_STEP_PX = 24

/** ペインを開ける最小の作業領域の高さ。**比率より最小高さを優先する**。 */
export const PANE_MIN_WORK_PX = BODY_MIN_PX + PANE_MIN_PX

/**
 * 開けないときの理由と次の行動（無効化の理由と同じ方式）。
 *
 * **押し下げ・はみ出し・横並びへの組み替えで代替しない**（主操作を画面の外へ出さない）。
 */
export const PANE_UNAVAILABLE_REASON =
    'この画面の高さでは相談の欄を開けません。ウィンドウを高くするか、文字を小さくしてからお試しください。'

/** ペインを開けるかを本文側（子要素）へ伝える。 */
type WizardPaneState = {
    /** 作業領域の高さが足りているか。 */
    canOpenPane: boolean
    /** 開けない理由（開けるときは undefined）。 */
    paneUnavailableReason?: string
}

const WizardPaneContext = createContext<WizardPaneState>({canOpenPane: true})

/** 本文の中に置く「ペインを開く」操作から、開けるかどうかを読む。 */
export function useWizardPane(): WizardPaneState {
    return useContext(WizardPaneContext)
}

function clamp(value: number, min: number, max: number): number {
    return Math.min(Math.max(value, min), max)
}

export function Wizard({
    title,
    step,
    total,
    children,
    onBack,
    onNext,
    nextLabel = '次へ',
    backLabel = '戻る',
    nextDisabledReason,
    extraAction,
    pane,
    paneLabel = '下部のペイン',
    persistenceNotice = '回答は自動保存されます。中断しても同じところから再開できます。',
}: {
    title: string
    /** 1 始まり */
    step: number
    total: number
    children: ReactNode
    onBack?: () => void
    onNext: () => void
    nextLabel?: string
    backLabel?: string
    /** 進めない理由。指定すると「進む」を無効化し理由を示す */
    nextDisabledReason?: string
    /** 「進む」の手前に置く従属操作（例: 紹介スライドの「スキップ」）。省略時は何も出さない */
    extraAction?: ReactNode
    /** 本文の下に置く上下分割のペイン（回答モードの AI 対話ペイン）。省略時は分割しない */
    pane?: ReactNode
    /** ペインの読み上げ名（境界のつまみの説明に使う） */
    paneLabel?: string
    persistenceNotice?: string
}) {
    const workRef = useRef<HTMLDivElement | null>(null)
    const [workHeight, setWorkHeight] = useState(0)
    const [paneRatio, setPaneRatio] = useState(PANE_DEFAULT_RATIO)

    // 作業領域の高さを測る。文字の拡大・OS の表示倍率・ウィンドウ操作で変わるため監視する。
    useEffect(() => {
        const el = workRef.current
        if (!el || typeof ResizeObserver === 'undefined') {
            return
        }
        const observer = new ResizeObserver(() => setWorkHeight(el.clientHeight))
        observer.observe(el)
        setWorkHeight(el.clientHeight)
        return () => observer.disconnect()
    }, [])

    // 測れていない間（テストの仮想 DOM など）は開ける前提で扱う。開けない断定をしない。
    const canOpenPane = workHeight === 0 || workHeight >= PANE_MIN_WORK_PX
    const maxPanePx = Math.max(PANE_MIN_PX, workHeight - BODY_MIN_PX - RESIZER_PX)
    const panePx = clamp(Math.round(workHeight * paneRatio), PANE_MIN_PX, maxPanePx)

    const movePane = useCallback(
        (nextPx: number) => {
            if (workHeight <= 0) {
                return
            }
            setPaneRatio(clamp(nextPx, PANE_MIN_PX, maxPanePx) / workHeight)
        },
        [maxPanePx, workHeight],
    )

    const onResizerPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
        const startY = e.clientY
        const startPx = panePx
        e.currentTarget.setPointerCapture(e.pointerId)
        const onMove = (ev: PointerEvent) => movePane(startPx - (ev.clientY - startY))
        const onUp = () => {
            window.removeEventListener('pointermove', onMove)
            window.removeEventListener('pointerup', onUp)
        }
        window.addEventListener('pointermove', onMove)
        window.addEventListener('pointerup', onUp)
    }

    // キーボードでも変えられること。ポインタを使えない利用者を締め出さない。
    const onResizerKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
        const moves: Record<string, number> = {
            ArrowUp: panePx + PANE_STEP_PX,
            ArrowDown: panePx - PANE_STEP_PX,
            Home: PANE_MIN_PX,
            End: maxPanePx,
        }
        const next = moves[e.key]
        if (next === undefined) {
            return
        }
        e.preventDefault()
        movePane(next)
    }

    const rows = pane ? `minmax(0, 1fr) ${RESIZER_PX}px ${panePx}px` : undefined

    return (
        <div className="rw-wizard">
            <header className="rw-wizard__head">
                <h1 className="rw-wizard__title">{title}</h1>
                <p className="rw-wizard__progress rw-mono" aria-label="進捗">
                    {step} / {total}
                </p>
            </header>
            <div className="rw-wizard__work" ref={workRef} style={rows ? {gridTemplateRows: rows} : undefined}>
                <main className="rw-wizard__body">
                    <WizardPaneContext.Provider
                        value={{
                            canOpenPane,
                            paneUnavailableReason: canOpenPane ? undefined : PANE_UNAVAILABLE_REASON,
                        }}
                    >
                        {children}
                    </WizardPaneContext.Provider>
                </main>
                {pane ? (
                    <>
                        <div
                            className="rw-wizard__resizer"
                            role="separator"
                            aria-orientation="horizontal"
                            aria-label={`${paneLabel}の高さ`}
                            aria-valuenow={panePx}
                            aria-valuemin={PANE_MIN_PX}
                            aria-valuemax={maxPanePx}
                            tabIndex={0}
                            onPointerDown={onResizerPointerDown}
                            onKeyDown={onResizerKeyDown}
                        />
                        <section className="rw-wizard__pane" aria-label={paneLabel}>
                            {pane}
                        </section>
                    </>
                ) : null}
            </div>
            <p className="rw-wizard__notice">{persistenceNotice}</p>
            <footer className="rw-wizard__footer">
                <Button
                    variant="secondary"
                    onClick={onBack ?? (() => undefined)}
                    disabledReason={onBack ? undefined : '最初の項目のため戻れません。'}
                >
                    {backLabel}
                </Button>
                <div className="rw-wizard__footer-end">
                    {extraAction}
                    <Button variant="primary" onClick={onNext} disabledReason={nextDisabledReason}>
                        {nextLabel}
                    </Button>
                </div>
            </footer>
        </div>
    )
}
