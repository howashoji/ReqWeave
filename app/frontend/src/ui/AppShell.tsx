import {useState} from 'react'
import type {CSSProperties, ReactNode} from 'react'
import {Chip} from './Chip'
import {formatCount} from './format'
import {GuideHintProvider} from './GuideHint'
import {GuideStrip} from './GuideStrip'
import type {GuideStripProps} from './GuideStrip'
import './AppShell.css'

/**
 * 担当者モードの 3 層構造。
 *
 *   1. グローバルヘッダー: パンくず「reqweave / プロジェクト / フェーズ」+ 画面ナビ
 *   2. コンテキストストリップ: 完成度セグメントバーと内訳 + 決定・未決・候補チップ
 *   3. 作業領域 + ステータスライン: エフォート・モデル・トークン消費
 *
 * 完成度・件数・トークン消費はプロジェクト文脈の画面で常時可視とする（担当者が進み具合と消費を見失わないように）。
 * そのため `scope="project"`（既定）ではこれらを省略できない。
 * プロジェクト横断・アプリ全体の画面（プロジェクト一覧・AI 利用量ダッシュボード）は `scope="global"` とし、
 * コンテキストストリップとステータスラインを出さず、指標は行内・表内へ分散表示する。
 * 回答モードでは本シェルを使わない（統制表示を一切出さない）。
 */

export type Completeness = {
    /** 章観点ごとの充足率（0〜100）。充足の 3 状態は色以外の手掛かりも併用する */
    sections: Array<{label: string; percent: number}>
}

export type RecordCounts = {
    decided: number
    open: number
    candidates: number
}

export type TokenUsage = {
    /** 期間内の消費トークン数 */
    consumed: number
    /** 上限設定時のみ。未設定なら null（残量を見えるようにするため） */
    limit: number | null
}

/** プロジェクト文脈の画面。統制表示（完成度・件数・エフォート・モデル・消費）を必ず持つ */
type ProjectScopeProps = {
    scope?: 'project'
    completeness: Completeness
    counts: RecordCounts
    effort: string
    model: string
    usage: TokenUsage
    /**
     * 工程ガイド。いまの工程と次にやることを常時出す。
     * 読み込み前は undefined（行ごと出さない。空の枠を先に見せない）。
     */
    guide?: Omit<GuideStripProps, 'hint'>
}

/** プロジェクト横断・アプリ全体の画面。統制表示を持たない */
type GlobalScopeProps = {
    scope: 'global'
    completeness?: never
    counts?: never
    effort?: never
    model?: never
    usage?: never
    guide?: never
}

type AppShellProps = {
    breadcrumb: string[]
    nav: ReactNode
    children: ReactNode
} & (ProjectScopeProps | GlobalScopeProps)

export function AppShell({breadcrumb, nav, children, ...rest}: AppShellProps) {
    // 画面が publish する「次に触る要素」。値はここで持ち、設定する口だけを配る（GuideHint）。
    const [hint, setHint] = useState('')
    const isGlobal = rest.scope === 'global'
    const completeness = rest.completeness ?? {sections: []}
    const counts = rest.counts ?? {decided: 0, open: 0, candidates: 0}
    const total = completeness.sections.length
    const overall =
        total === 0 ? 0 : Math.round(completeness.sections.reduce((a, s) => a + s.percent, 0) / total)

    return (
        <div className="rw-shell">
            <header className="rw-shell__header">
                <nav className="rw-breadcrumb rw-mono" aria-label="現在地">
                    {breadcrumb.map((part, i) => (
                        <span key={part} className="rw-breadcrumb__part">
                            {i > 0 ? <span className="rw-breadcrumb__sep"> / </span> : null}
                            {part}
                        </span>
                    ))}
                </nav>
                <div className="rw-shell__nav">{nav}</div>
            </header>

            {isGlobal ? null : (
                <div className="rw-strip" aria-label="プロジェクトの状態">
                    <div className="rw-strip__completeness">
                        <span className="rw-strip__label">完成度</span>
                        <span className="rw-mono rw-strip__overall">{overall}%</span>
                        <span className="rw-segments">
                            {completeness.sections.map((s) => (
                                <span
                                    key={s.label}
                                    className="rw-segment"
                                    title={`${s.label} ${s.percent}%`}
                                    aria-label={`${s.label} ${s.percent}%`}
                                >
                                    {/* 値のみをカスタムプロパティで渡す。見た目の指定は CSS 側に置く（BD 実装規約） */}
                                    <span
                                        className="rw-segment__fill"
                                        style={{'--rw-segment-percent': `${s.percent}%`} as CSSProperties}
                                    />
                                </span>
                            ))}
                        </span>
                    </div>
                    <div className="rw-strip__counts">
                        <Chip tone="accent">決定 {counts.decided}</Chip>
                        <Chip tone="warn">未決 {counts.open}</Chip>
                        <Chip tone="info">候補 {counts.candidates}</Chip>
                    </div>
                </div>
            )}

            {isGlobal || rest.guide === undefined ? null : <GuideStrip {...rest.guide} hint={hint} />}

            <main className="rw-shell__main">
                <GuideHintProvider value={setHint}>{children}</GuideHintProvider>
            </main>

            {isGlobal || rest.usage === undefined ? null : (
                <footer className="rw-statusline">
                    <span className="rw-statusline__item">
                        エフォート <span className="rw-mono">{rest.effort}</span>
                    </span>
                    <span className="rw-statusline__item">
                        モデル <span className="rw-mono">{rest.model}</span>
                    </span>
                    <span className="rw-statusline__item">
                        トークン{' '}
                        <span className="rw-mono">
                            {rest.usage.limit === null
                                ? formatCount(rest.usage.consumed)
                                : `${formatCount(rest.usage.consumed)} / ${formatCount(rest.usage.limit)}`}
                        </span>
                    </span>
                </footer>
            )}
        </div>
    )
}
