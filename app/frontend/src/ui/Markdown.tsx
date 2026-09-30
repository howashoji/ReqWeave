import {useEffect, useMemo, useRef, useState} from 'react'
import DOMPurify from 'dompurify'
import {marked} from 'marked'
import {currentTheme} from '../theme/theme'
import './Markdown.css'

/**
 * 成果物ドキュメントの表示。
 *
 * Markdown を描画し、mermaid のフェンスブロックは図として描画する（画像ファイルは使わない）。
 * 生成物は AI の出力を含むため、描画前に必ずサニタイズする（スクリプト・イベント属性を落とす）。
 * mermaid はバンドル済みで、描画時に外部へ通信しない。
 * 図の配色は画面テーマに追従させる（テーマごとの色トークン対の差し替えに合わせる）。
 */

type MermaidBlock = {id: string; code: string}

/** mermaid フェンスを取り出し、本文からはプレースホルダへ置き換える。 */
function extractMermaid(markdown: string): {text: string; blocks: MermaidBlock[]} {
    const blocks: MermaidBlock[] = []
    const text = markdown.replace(/```mermaid\n([\s\S]*?)```/g, (_match, code: string) => {
        const id = `rw-mermaid-${blocks.length}`
        blocks.push({id, code: code.trim()})
        return `<div class="rw-markdown__mermaid" data-mermaid-id="${id}"></div>`
    })
    return {text, blocks}
}

export function Markdown({source}: {source: string}) {
    const container = useRef<HTMLDivElement>(null)
    const [failed, setFailed] = useState(false)

    const {html, blocks} = useMemo(() => {
        const {text, blocks} = extractMermaid(source)
        const parsed = marked.parse(text, {async: false, gfm: true, breaks: false}) as string
        return {html: DOMPurify.sanitize(parsed, {ADD_ATTR: ['data-mermaid-id']}), blocks}
    }, [source])

    useEffect(() => {
        if (blocks.length === 0 || !container.current) {
            return
        }
        let cancelled = false
        // mermaid は初期化・描画が重いため、図があるときだけ読み込む。
        void import('mermaid')
            .then(async ({default: mermaid}) => {
                if (cancelled || !container.current) {
                    return
                }
                mermaid.initialize({
                    startOnLoad: false,
                    securityLevel: 'strict',
                    // ライトテーマでは図もライト側で描く。
                    theme: currentTheme() === 'light' ? 'default' : 'dark',
                    // **ラベルを HTML ではなく SVG の <text> で描く**。
                    // 既定（htmlLabels: true）では mermaid がラベルを <foreignObject> の中の
                    // HTML として出すが、DOMPurify は foreignObject を**中身ごと**落とすため
                    // （svgDisallowed と DEFAULT_FORBID_CONTENTS の双方に入っている）、
                    // 四角と線だけが残って文字が全部消える。
                    // **サニタイズを緩めて解決しない**（成果物プレビューは AI の生成物を描くため
                    // スクリプト経路を開けない）。
                    //
                    // **効くのは最上位の htmlLabels**。mermaid 11.17 では
                    // `flowchart.htmlLabels` だけを false にしても foreignObject のまま出る
                    // （実測: flowchart のみ = foreignObject 4 個 / 最上位 = 0 個）。
                    // 図の種類ごとの指定は最上位に合わせて残す。
                    htmlLabels: false,
                    flowchart: {htmlLabels: false},
                    class: {htmlLabels: false},
                })
                for (const block of blocks) {
                    const target = container.current.querySelector(`[data-mermaid-id="${block.id}"]`)
                    if (!target) {
                        continue
                    }
                    try {
                        const {svg} = await mermaid.render(`${block.id}-svg`, block.code)
                        target.innerHTML = DOMPurify.sanitize(svg)
                    } catch {
                        // 図の記述が壊れていても本文の閲覧は続けられるようにする。
                        target.textContent = block.code
                        target.classList.add('rw-markdown__mermaid--raw')
                        setFailed(true)
                    }
                }
            })
            .catch(() => setFailed(true))
        return () => {
            cancelled = true
        }
    }, [blocks, html])

    return (
        <div className="rw-markdown">
            {failed ? (
                <p className="rw-markdown__notice" role="status">
                    一部の図を描画できませんでした。記述をそのまま表示しています。
                </p>
            ) : null}
            <div ref={container} dangerouslySetInnerHTML={{__html: html}} />
        </div>
    )
}
