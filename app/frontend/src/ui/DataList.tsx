import type {ReactNode} from 'react'
import {EmptyState} from './EmptyState'
import type {EmptyStateProps} from './EmptyState'
import './DataList.css'

/**
 * 一覧・管理系。
 *
 * 見出し行（`muted` のモノスペース小ラベル）+ 1px 区切りの行リスト。
 * 行内のメタ情報（ID・日時・件数・消費量）はモノスペース、状態はチップ 3 点セット。
 * 選択行は `surface` 地 + `accent` 左ボーダー。
 */

export type Column = {
    key: string
    label: string
    /** ID・日時・件数・消費量などはモノスペースで表示する */
    mono?: boolean
}

export type Row = {
    id: string
    cells: Record<string, ReactNode>
}

export function DataList({
    caption,
    columns,
    rows,
    selectedId,
    onSelect,
    empty,
}: {
    caption: string
    columns: Column[]
    rows: Row[]
    selectedId?: string
    onSelect?: (id: string) => void
    /**
     * 行が無いときの表示。**既定値を持たせない**。
     * 「該当する項目はありません。」だけの行き止まりを作らないため、
     * 呼び出し側に「次の一手」か「利用者の操作では増えない理由」の明示を型で求める。
     */
    empty: EmptyStateProps
}) {
    return (
        <div className="rw-list" role="table" aria-label={caption}>
            <div className="rw-list__head rw-mono" role="row">
                {columns.map((c) => (
                    <span key={c.key} className="rw-list__label" role="columnheader">
                        {c.label}
                    </span>
                ))}
            </div>
            {rows.length === 0 ? (
                <div className="rw-list__empty">
                    <EmptyState {...empty} />
                </div>
            ) : (
                rows.map((row) => (
                    <div
                        key={row.id}
                        role="row"
                        aria-selected={row.id === selectedId}
                        className={`rw-list__row${row.id === selectedId ? ' rw-list__row--selected' : ''}`}
                        onClick={onSelect ? () => onSelect(row.id) : undefined}
                    >
                        {columns.map((c) => (
                            <span
                                key={c.key}
                                role="cell"
                                className={`rw-list__cell${c.mono ? ' rw-mono' : ''}`}
                            >
                                {row.cells[c.key]}
                            </span>
                        ))}
                    </div>
                ))
            )}
        </div>
    )
}
