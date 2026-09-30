import {useCallback, useEffect, useRef, useState} from 'react'
import type {CSSProperties, ReactNode} from 'react'
import {PaneWidths, SetPaneWidths} from '../../wailsjs/go/binding/API'
import {Button} from './Button'
import './DocumentWorkspace.css'

/**
 * 文書・承認系。
 *
 * 3 ペイン構成: 左 = 対象・版の一覧 / 中 = 本文・差分 / 右 = チェック結果・操作。
 * **プライマリ操作は 1 画面 1 つの緑塗りボタンに限定する**ため、primaryAction は単数の prop とし、
 * 配列や ReactNode を受け取らない（画面ごとに複数置けない形で型に落とす）。
 * 破壊的でない従属操作は枠線ボタン、取消系はテキストボタン。
 *
 * **主操作が無効なときは理由をボタンの直下に常時表示する**。
 * ツールチップだけに置くと、ポインタを重ねない限り「なぜ押せないか」が画面に無い状態になる。
 *
 * **左右のペインの幅は境界のつまみで変えられる**。
 * 変えた幅は端末ごとのアプリ設定へ保存し、次に開いたときも同じ幅で表示する
 * （保存先・許容範囲の正本はバインディング）。
 */

export type Action = {
    label: string
    onClick: () => void
    /** 無効化する場合は理由を必ず添える */
    disabledReason?: string
}

/** 幅を変える対象のペイン。 */
type Edge = 'versions' | 'checks'

export function DocumentWorkspace({
    versionsPane,
    documentPane,
    checksPane,
    primaryAction,
    secondaryActions = [],
    cancelAction,
    actionError,
}: {
    versionsPane: ReactNode
    documentPane: ReactNode
    checksPane: ReactNode
    primaryAction: Action
    secondaryActions?: Action[]
    cancelAction?: Action
    /**
     * 主操作が失敗した理由。**押したボタンのすぐ上**に出す。
     * 画面上端のバナーは、右ペインを下へスクロールして押した利用者から見えない。
     */
    actionError?: string
}) {
    // 既定値・許容範囲はバインディングが持つ（画面に二重に持たせない）。
    const [widths, setWidths] = useState({versions: 220, checks: 360, min: 160, max: 640})
    const drag = useRef<{edge: Edge; startX: number; startWidth: number} | null>(null)

    useEffect(() => {
        let alive = true
        PaneWidths()
            .then((w) => {
                if (alive) {
                    setWidths(w)
                }
            })
            // 読めなくても既定幅で表示できる（表示設定のため機能は止めない）。
            .catch(() => undefined)
        return () => {
            alive = false
        }
    }, [])

    const clamp = useCallback((v: number) => Math.min(widths.max, Math.max(widths.min, Math.round(v))), [widths])

    /** 幅を変えて端末ごとの設定へ保存する。保存に失敗しても画面上の幅は保つ。 */
    const apply = useCallback(
        (next: {versions: number; checks: number}, save: boolean) => {
            setWidths((w) => ({...w, ...next}))
            if (save) {
                void SetPaneWidths(next.versions, next.checks).catch(() => undefined)
            }
        },
        [],
    )

    useEffect(() => {
        const onMove = (e: MouseEvent) => {
            const d = drag.current
            if (!d) {
                return
            }
            // 左は右へ動かすほど広く、右は左へ動かすほど広い。
            const delta = d.edge === 'versions' ? e.clientX - d.startX : d.startX - e.clientX
            const value = clamp(d.startWidth + delta)
            apply({versions: widths.versions, checks: widths.checks, [d.edge]: value}, false)
        }
        const onUp = () => {
            if (!drag.current) {
                return
            }
            drag.current = null
            document.body.classList.remove('rw-doc--resizing')
            void SetPaneWidths(widths.versions, widths.checks).catch(() => undefined)
        }
        window.addEventListener('mousemove', onMove)
        window.addEventListener('mouseup', onUp)
        return () => {
            window.removeEventListener('mousemove', onMove)
            window.removeEventListener('mouseup', onUp)
        }
    }, [apply, clamp, widths])

    const startDrag = (edge: Edge) => (e: React.MouseEvent) => {
        drag.current = {edge, startX: e.clientX, startWidth: widths[edge]}
        document.body.classList.add('rw-doc--resizing')
    }

    /** キーボードでも幅を変えられる（マウス専用にしない）。左右キーで 16px ずつ。 */
    const onKey = (edge: Edge) => (e: React.KeyboardEvent) => {
        const step = e.key === 'ArrowLeft' ? -16 : e.key === 'ArrowRight' ? 16 : 0
        if (step === 0) {
            return
        }
        e.preventDefault()
        const delta = edge === 'versions' ? step : -step
        apply({versions: widths.versions, checks: widths.checks, [edge]: clamp(widths[edge] + delta)}, true)
    }

    const handle = (edge: Edge, label: string) => (
        <div
            className="rw-doc__resizer"
            role="separator"
            aria-orientation="vertical"
            aria-label={label}
            aria-valuenow={widths[edge]}
            aria-valuemin={widths.min}
            aria-valuemax={widths.max}
            tabIndex={0}
            onMouseDown={startDrag(edge)}
            onKeyDown={onKey(edge)}
        />
    )

    return (
        <div
            className="rw-doc"
            // 値のみをカスタムプロパティで渡す（見た目の指定は CSS 側 = BD 実装規約）
            style={
                {
                    '--rw-doc-versions': `${widths.versions}px`,
                    '--rw-doc-checks': `${widths.checks}px`,
                } as CSSProperties
            }
        >
            <aside className="rw-doc__versions" aria-label="対象・版の一覧">
                {versionsPane}
            </aside>
            {handle('versions', '左のペインの幅')}
            <section className="rw-doc__body" aria-label="本文・差分">
                {documentPane}
            </section>
            {handle('checks', '右のペインの幅')}
            <aside className="rw-doc__checks" aria-label="チェック結果・操作">
                {checksPane}
                <div className="rw-doc__footer">
                    {actionError ? (
                        <p className="rw-doc__error" role="alert">
                            {actionError}
                        </p>
                    ) : null}
                    <div className="rw-doc__actions">
                        <Button
                            variant="primary"
                            onClick={primaryAction.onClick}
                            disabledReason={primaryAction.disabledReason}
                        >
                            {primaryAction.label}
                        </Button>
                        {secondaryActions.map((a) => (
                            <Button
                                key={a.label}
                                variant="secondary"
                                onClick={a.onClick}
                                disabledReason={a.disabledReason}
                            >
                                {a.label}
                            </Button>
                        ))}
                        {cancelAction ? (
                            <Button variant="quiet" onClick={cancelAction.onClick}>
                                {cancelAction.label}
                            </Button>
                        ) : null}
                    </div>
                    {/* 主操作が押せない理由は、ホバーを待たずボタンの直下に出す
                        （ポインタを使わない利用者にも届かせる） */}
                    {primaryAction.disabledReason ? (
                        <p className="rw-doc__reason" role="status">
                            {primaryAction.disabledReason}
                        </p>
                    ) : null}
                </div>
            </aside>
        </div>
    )
}

/** 差分の追加行（accent 系の淡背景 + 左ボーダー）。 */
export function DiffAddition({children}: {children: ReactNode}) {
    return <div className="rw-diff-add">{children}</div>
}
