import {describe, expect, it} from 'vitest'

/**
 * UI 実装規約の機械検証（生のコード値・影・余白・重ねる面・空状態などの書き方の規則）。
 *
 * 対象は出荷されるソースのみ。テストファイル（*.test.ts / *.test.tsx）は配布物に含まれないため除外する
 * （本ファイル自身が検査パターンの文字列を持つため、含めると自己検出になる）。
 */

const sources = import.meta.glob('../**/*.{ts,tsx}', {eager: true, query: '?raw', import: 'default'}) as Record<
    string,
    string
>
const styles = import.meta.glob('../**/*.css', {eager: true, query: '?raw', import: 'default'}) as Record<
    string,
    string
>

const shipped = Object.entries(sources).filter(([path]) => !/\.test\.tsx?$/.test(path))

function findLines(text: string, pattern: RegExp): string[] {
    return text
        .split('\n')
        .filter((line) => pattern.test(line))
        .map((line) => line.trim())
}

describe('UI 実装規約', () => {
    it('検査対象のソースを実際に読み込めている（空振りで緑にならないこと）', () => {
        expect(shipped.length).toBeGreaterThanOrEqual(10)
        expect(Object.keys(styles).length).toBeGreaterThanOrEqual(8)
    })

    it('HTML の挿入は Markdown.tsx に閉じ、必ずサニタイズを通す', () => {
        // AI の出力と取り込んだ資料は本システムが内容を保証できない。素の HTML を挿し込む経路を
        // 画面ごとに作ると、資料に埋め込まれた記述で画面の振る舞いを変えられる。
        // 挿入は 1 か所（ui/Markdown.tsx）に集約し、そこでだけサニタイズを通す。
        const insertion = /dangerouslySetInnerHTML|\.innerHTML\s*=/
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            const lines = findLines(text, insertion)
            if (lines.length === 0) continue
            if (!path.endsWith('/Markdown.tsx')) {
                offenders.push(`${path}: ${lines.join(' / ')}`)
                continue
            }
            // Markdown.tsx の中でも、挿入する値はサニタイズの戻り値でなければならない。
            for (const line of lines) {
                const sanitizedHere = /DOMPurify\.sanitize/.test(line)
                const sanitizedVar = /__html:\s*\{?\s*html\b|__html:\s*html\b/.test(line)
                if (!sanitizedHere && !sanitizedVar) {
                    offenders.push(`${path}: ${line}`)
                }
            }
        }
        expect(offenders, 'サニタイズを通さない HTML 挿入がある').toEqual([])
        // 空振りの検出: 挿入そのものが 1 件も無ければ、検査が意味を持っていない。
        const total = shipped.filter(([, text]) => insertion.test(text)).length
        expect(total, 'HTML を挿入している箇所が 1 件も見つからない（検査が空振り）').toBeGreaterThanOrEqual(1)
    })

    it('inline style を書かない（値のみを渡すカスタムプロパティは可）', () => {
        // 見た目の指定は CSS のクラス・トークンに寄せる。JSX から渡してよいのは
        // --rw-* の「値」だけ（例: 充足率の幅）。
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            for (const line of findLines(text, /style=\{\{/)) {
                if (!/style=\{\{\s*'--rw-/.test(line)) {
                    offenders.push(`${path}: ${line}`)
                }
            }
        }
        expect(offenders).toEqual([])
    })

    it('ネイティブの confirm / alert / prompt を使わない（テーマ非追従・自動テスト不能のため）', () => {
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            for (const line of findLines(text, /(?:^|[^.\w])(?:window\.)?(?:confirm|alert|prompt)\s*\(/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders).toEqual([])
    })

    // 日時・件数の表記を画面ごとに作らない（表記ゆれの再発防止）。
    // 実装は ui/format.ts の 1 か所に限る。ICU（toLocale*）は環境差で表示が変わるため使わない。
    it('日時・件数の整形を ui/format.ts の外に書かない', () => {
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            if (path.endsWith('/format.ts')) {
                continue
            }
            // ICU 依存の整形（ロケール実装で桁区切り・区切り文字が変わる）
            for (const line of findLines(text, /\.toLocale(?:String|DateString|TimeString)\s*\(/)) {
                offenders.push(`${path}: ${line}`)
            }
            // Date から表示文字列を自前で組み立てている（formatLocal / defaultPeriod の再実装）
            for (const line of findLines(text, /\.getFullYear\s*\(\)/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders).toEqual([])
    })

    // 画面に内部用語を出さない（エラーも案内も利用者の語彙で書く）。
    // 利用者 ID の入力欄は「メールアドレス（利用者 ID）」と表記し、UPN という語を画面へ出さない
    // （UPN は組織 ID 基盤の用語であり、利用者が何を入力すべきか分からなくなる）。
    it('内部用語「UPN」を画面のソースに書かない', () => {
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            for (const line of findLines(text, /UPN/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders).toEqual([])
    })

    // Wails はエラーを new Error(文言) で包んで返すため、String(err) で出すと
    // 画面に「Error: 」が付く。エラーの表示は ui/errorText.ts を必ず通す。
    it('エラーを String(err) で画面へ出さない（errorText を通す）', () => {
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            if (path.endsWith('/errorText.ts')) {
                continue
            }
            for (const line of findLines(text, /String\(\s*(?:err|error|e)\s*\)/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders).toEqual([])
        // 空振りの検出: エラー表示の口が 1 件も見つからなければ、置き換えが検査外へ逃げている。
        const using = shipped.filter(([, text]) => /errorText\(/.test(text)).length
        expect(using, 'errorText を使っている画面が見つからない（検査が空振り）').toBeGreaterThanOrEqual(10)
    })

    it('コンポーネント CSS は色を必ずトークン参照で書く（生の色値を書かない）', () => {
        // 生の色値を持ってよいのは tokens.css（トークン定義そのもの）だけ。
        const rawColor = /(#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\()/
        const offenders: string[] = []
        for (const [path, text] of Object.entries(styles)) {
            if (path.endsWith('/tokens.css')) {
                continue
            }
            for (const line of findLines(text, rawColor)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders).toEqual([])
    })

    it('コンポーネント CSS は影を使わない', () => {
        const offenders: string[] = []
        for (const [path, text] of Object.entries(styles)) {
            for (const line of findLines(text, /box-shadow\s*:|text-shadow\s*:|filter\s*:\s*drop-shadow/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders).toEqual([])
    })

    // 余白は tokens.css の余白スケール（--rw-space-1〜5）だけを使う。
    //
    // 画面ごとに 1〜20px の値が散らばり、詰まり方がばらついていた（2026-09-07 に
    // 「狭苦しい」と実機で指摘）。padding / margin / gap に px を直接書かせない。
    // 0 と auto、文字量に追従する em / % は対象外（余白スケールで表せないため）。
    it('余白は px を直書きせず余白スケールのトークンで書く', () => {
        const spacing = /^\s*(padding|margin|gap|row-gap|column-gap)(-(top|right|bottom|left|inline|block))?\s*:/
        const offenders: string[] = []
        for (const [path, text] of Object.entries(styles)) {
            if (path.endsWith('/tokens.css')) {
                continue
            }
            for (const line of findLines(text, spacing)) {
                if (/\d+px/.test(line)) {
                    offenders.push(`${path}: ${line}`)
                }
            }
        }
        expect(offenders).toEqual([])
    })

    it('余白スケールは 5 段そろっている（値の追加はデザイン規約の変更を伴う）', () => {
        const tokens = Object.entries(styles).find(([path]) => path.endsWith('/tokens.css'))
        expect(tokens, 'tokens.css が見つからない').toBeDefined()
        for (const step of [1, 2, 3, 4, 5]) {
            expect(tokens![1], `--rw-space-${step} が無い`).toMatch(
                new RegExp(`--rw-space-${step}\\s*:\\s*\\d+px`),
            )
        }
    })

    // 承認可否を判断する候補の本文が読める大きさであること。
    //
    // 右ペイン 280px・入力欄 3 行では本文が見切れ、「狭くてとても読みづらい」と実機で指摘された。
    // jsdom はレイアウトを計算しないため、規則そのものを検査する。
    it('候補を読む場所（右ペインと入力欄）が十分な大きさを持つ', () => {
        const css = (suffix: string) => {
            const found = Object.entries(styles).find(([path]) => path.endsWith(suffix))
            expect(found, `${suffix} が見つからない`).toBeDefined()
            return found![1]
        }
        // 3 ペインの右列の既定は 360px 以上。中央は minmax(0, …) で潰れない。
        // 幅は利用者が変えられるため、CSS には var() の既定値として現れる。
        const columns = css('/DocumentWorkspace.css').match(/grid-template-columns\s*:\s*([^;]+);/)
        expect(columns, 'grid-template-columns が見つからない').not.toBeNull()
        const fallback = columns![1].match(/var\(--rw-doc-checks,\s*(\d+)px\)/)
        expect(fallback, '右列の既定値（var の第 2 引数）が読めない').not.toBeNull()
        expect(Number.parseInt(fallback![1], 10)).toBeGreaterThanOrEqual(360)
        expect(columns![1]).toMatch(/minmax\(0,/)

        // 候補の入力欄は最低高さと縦方向のリサイズを持つ（取り込み画面・対話画面の双方）。
        for (const [file, selector] of [
            ['/Imports.css', '.rw-imports__candidate textarea'],
            ['/Dialogue.css', '.rw-candidates__body'],
        ] as const) {
            // 同じ要素に複数の規則が当たるため、**どれかが**高さとリサイズを与えていればよい。
            const blocks = css(file).match(new RegExp(`\\${selector}[^{}]*\\{[^}]*\\}`, 'g')) ?? []
            expect(blocks.length, `${selector} の定義が見つからない`).toBeGreaterThan(0)
            expect(
                blocks.some((b) => /min-height\s*:\s*\d+em/.test(b)),
                `${selector} に min-height（em）が無い`,
            ).toBe(true)
            expect(
                blocks.some((b) => /resize\s*:\s*vertical/.test(b)),
                `${selector} が縦に広げられない`,
            ).toBe(true)
        }
    })

    // hidden で隠した要素が CSS の display で復活しないこと。
    //
    // `[hidden] { display: none }` はブラウザ標準スタイルであり、作成者スタイルの display が
    // 常に勝つ。実際に `.rw-dialogue { display: grid }` が hidden を打ち消し、対話画面が
    // 別の画面と二重に表示されていた（2026-09-07 に実機で判明）。
    // vitest（jsdom）は CSS を適用しないため、基底スタイルに規則があることを検査する。
    it('hidden で隠した要素は基底スタイルで必ず消す', () => {
        const base = Object.entries(styles).find(([path]) => path.endsWith('/style.css'))
        expect(base, 'style.css が見つからない').toBeDefined()
        const block = base![1].match(/\[hidden\]\s*\{[^}]*\}/)
        expect(block, '[hidden] の規則が無い').not.toBeNull()
        expect(block![0]).toMatch(/display\s*:\s*none\s*!important/)
    })

    /*
     * 重ねて出す面は共通の Overlay に寄せ、画面ごとに作り込まない。
     *
     * 「一時的に見るもの・答えるもの」を画面ごとの CSS で浮かせると、置き方・閉じ方・
     * 高さの頭打ちが画面ごとにばらつき、作業領域を押し下げるような取りこぼしが再発する。
     * 画面に固定して浮かせてよいのは、共通部品として作った 4 つだけ。
     */
    it('画面へ固定して浮かせるのは共通部品だけ（重ねる面は Overlay に寄せる）', () => {
        // Overlay / ツールチップ / トースト・飛ぶカード。
        const allowed = ['/Overlay.css', '/Tooltip.css', '/Toast.css', '/FlyingCard.css']
        const offenders: string[] = []
        for (const [path, text] of Object.entries(styles)) {
            if (allowed.some((a) => path.endsWith(a))) {
                continue
            }
            for (const line of findLines(text, /position\s*:\s*fixed/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders, '重ねる面は ui/Overlay を使うこと').toEqual([])
    })

    /*
     * 作業領域へ重ねて出すものは、通常の流れで高さを占めない。
     *
     * 工程の全体像を流れの中へ挿し込んでいたため、開いた瞬間に
     * 対話ペインがそのぶん下へ押し出され、**ウィンドウを広げるかスクロールしないと見えなかった**
     * （2026-09-09 に実機で判明）。置き方は共通の Overlay が持つ。
     * jsdom はレイアウトを計算しないため、規則そのものを検査する。
     */
    it('重ねて出すものは通常の流れで高さを占めず、画面に収まる高さで頭打ちにする', () => {
        const found = Object.entries(styles).find(([path]) => path.endsWith('/Overlay.css'))
        expect(found, 'Overlay.css が見つからない').toBeDefined()
        const text = found![1]
        const block = (selector: string) => {
            const m = text.match(new RegExp(`\\${selector}\\s*\\{[^}]*\\}`))
            expect(m, `${selector} の定義が見つからない`).not.toBeNull()
            return m![0]
        }
        // 土台は流れの中で場所を取らない（下の作業領域を動かさない）。
        const anchor = block('.rw-overlay-anchor')
        expect(anchor).toMatch(/position\s*:\s*relative/)
        expect(anchor).toMatch(/height\s*:\s*0/)
        // 本体は重ねる。高さは画面に収まる上限で頭打ちにし、あふれ分は中で送る。
        const panel = block('.rw-overlay')
        expect(panel).toMatch(/position\s*:\s*fixed/)
        expect(panel).toMatch(/max-height\s*:\s*min\(\s*\d+vh/)
        expect(panel).toMatch(/overflow-y\s*:\s*auto/)
        // 開く操作の真下に出す置き方も、流れの中では高さを持たない土台の上に重ねる。
        expect(text).toMatch(/\[data-anchored='true'\]\s*\.rw-overlay\s*\{[^}]*position\s*:\s*absolute/)
    })

    // 3 ペイン（文書・承認系）が内容の高さで伸びないこと。
    //
    // 資料や版が増えると、外枠に高さが無いために `.rw-doc { height: 100% }` が解決されず、
    // 各ペインの overflow が効かないまま画面全体が縦に伸びた（2026-09-07 に実機で判明）。
    // jsdom はレイアウトを計算しないため、**規則そのもの**を検査する。
    it('3 ペインは画面本体の高さに収まり、各ペインの中でスクロールする', () => {
        const css = (suffix: string) => {
            const found = Object.entries(styles).find(([path]) => path.endsWith(suffix))
            expect(found, `${suffix} が見つからない`).toBeDefined()
            return found![1]
        }
        const block = (text: string, selector: string) => {
            const m = text.match(new RegExp(`\\${selector}\\s*\\{[^}]*\\}`))
            expect(m, `${selector} の定義が見つからない`).not.toBeNull()
            return m![0]
        }

        // 3 ペインの外枠は残り高さに収まり、内容で伸びない。
        const doc = block(css('/DocumentWorkspace.css'), '.rw-doc')
        expect(doc).toMatch(/height\s*:\s*100%/)
        expect(doc).toMatch(/min-height\s*:\s*0/)
        expect(doc).toMatch(/flex\s*:/)

        // 各ペインはその中でスクロールする。
        const panes = css('/DocumentWorkspace.css')
        expect(block(panes, '.rw-doc__body')).toMatch(/overflow\s*:\s*auto/)
        expect(panes).toMatch(/\.rw-doc__versions,\s*\n\.rw-doc__checks\s*\{[^}]*overflow\s*:\s*auto/)

        // DocumentWorkspace を使う画面の外枠は、画面本体の高さを持つ
        // （持たないと上の height: 100% が解決されない）。
        const users = shipped.filter(([, text]) => /\bDocumentWorkspace\b/.test(text) && /screens\//.test('x'))
        void users
        const hosts: Array<[string, string]> = [
            ['/Imports.css', '.rw-imports'],
            ['/Documents.css', '.rw-documents'],
        ]
        for (const [file, selector] of hosts) {
            const wrapper = block(css(file), selector)
            expect(wrapper, `${selector} に高さが無い`).toMatch(/height\s*:\s*100%/)
            expect(wrapper, `${selector} に min-height: 0 が無い`).toMatch(/min-height\s*:\s*0/)
        }

        // 画面が増えたら上の一覧も増やす（空振りで緑にならないための番人）。
        const screensUsingWorkspace = shipped.filter(
            ([path, text]) => /\/screens\//.test(path) && /<DocumentWorkspace/.test(text),
        )
        expect(
            screensUsingWorkspace.map(([path]) => path.split('/').pop()).sort(),
            '3 ペインを使う画面が増減している。hosts の一覧を更新すること',
        ).toEqual(['Documents.tsx', 'Imports.tsx'])
    })

    /*
     * 押せるものは枠を持つ。
     *
     * 枠を持たない文字だけのボタン（variant="quiet"）は**取消系に限る**。
     * 行の操作を文字だけで描くと、押せるものが本文と区別できない
     * （利用者報告「クリッカブルな要素がない」の直接原因のひとつ）。
     */
    it('枠なしの文字ボタン（quiet）は取消系のラベルだけに使う', () => {
        // 取消系の語彙。増やすときは「取消・戻る・閉じるの操作だけを枠なしにする」規則に照らして判断する。
        const cancels = [
            '閉じる',
            'やめる',
            '取りやめる',
            '取消',
            'スキップ',
            '戻る',
            '発行をやめる',
            '反映せず保留する',
            '取り込みをやめる',
            '取り込みをやめる（作業コピーは変わりません）',
            // 回答モードの AI 対話ペインを閉じる（ペインを畳むだけの取消系の操作）。
            '相談を閉じる',
        ]
        // ラベルが変数のもの。取消の位置に固定されている呼び出しだけを、理由つきで許す。
        const dynamic: Record<string, string> = {
            '{deferLabel}': 'Banner の「あとで見る」（既定値も取消系）',
            '{cancelAction.label}': 'DocumentWorkspace の取消操作（型の上で取消の位置に固定）',
        }

        const offenders: string[] = []
        let checked = 0
        for (const [path, text] of shipped) {
            // 式で quiet を選ぶ書き方（variant={... 'quiet' ...}）は、ラベルを機械で追えない。
            // 実際にこの穴を通って「破棄」ボタンが枠を失っていた（是正済み）。
            for (const line of findLines(text, /variant=\{[^}]*'quiet'/)) {
                offenders.push(`${path}: ${line}`)
            }
            let from = 0
            for (;;) {
                const at = text.indexOf('variant="quiet"', from)
                if (at < 0) {
                    break
                }
                from = at + 1
                // 開始タグの終わり（>）から </Button> までが表示ラベル。
                // 属性値の中の `=>`（アロー関数）や `{...}` の中の `>` を終わりと見なさない。
                let depth = 0
                let open = -1
                for (let k = at; k < text.length; k += 1) {
                    const ch = text[k]
                    if (ch === '{') {
                        depth += 1
                    } else if (ch === '}') {
                        depth -= 1
                    } else if (ch === '>' && depth === 0 && text[k - 1] !== '=') {
                        open = k
                        break
                    }
                }
                const close = text.indexOf('</Button>', open)
                expect(open, `${path}: quiet ボタンの開始タグを読めない`).toBeGreaterThan(0)
                expect(close, `${path}: quiet ボタンの終わりを読めない`).toBeGreaterThan(open)
                const label = text.slice(open + 1, close).trim()
                checked += 1
                if (!cancels.includes(label) && !(label in dynamic)) {
                    offenders.push(`${path}: ${label}`)
                }
            }
        }
        // 空振りで緑にならないための番人（quiet の呼び出しが消えたら気づく）。
        expect(checked).toBeGreaterThanOrEqual(20)
        expect(offenders, '取消系でない操作が枠なしの文字ボタンになっている').toEqual(
            [],
        )
    })

    /*
     * 無効化の理由を `title` 属性へ載せない。
     *
     * 無効化された要素はポインタ事象を発しないため、`title` に載せた理由は画面に出ない。
     * それでも「理由を付けた」ように見えてしまうため、ソースの側で塞ぐ。
     */
    it('無効化の理由を title 属性へ渡さない（アプリ内の要素で描く）', () => {
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            for (const line of findLines(text, /title=/)) {
                if (/disabledReason|reason/i.test(line)) {
                    offenders.push(`${path}: ${line}`)
                }
            }
        }
        expect(offenders).toEqual([])
    })

    /*
     * 空状態を画面へ直書きしない。
     *
     * 「まだありません。」だけを書くと画面が行き止まりになる（対話画面・取り込み画面で実際に起きた）。
     * 共通の EmptyState に寄せることで、次の一手か「利用者の操作では増えない理由」の
     * どちらかを型で必ず書かせる。区画内の小さな内訳（「なし」等）は `__none` を使い分ける。
     */
    it('空状態を画面へ直書きしない（EmptyState / DataList に寄せる）', () => {
        const offenders: string[] = []
        for (const [path, text] of shipped) {
            // DataList は EmptyState を内側に置くための枠だけを持つ（rw-list__empty）。
            if (path.endsWith('/DataList.tsx')) {
                continue
            }
            for (const line of findLines(text, /__empty/)) {
                offenders.push(`${path}: ${line}`)
            }
        }
        expect(offenders, '空状態は EmptyState を使うこと').toEqual([])
    })

    /*
     * 画面を離れる操作は、戻り先が分かる語にする。
     *
     * 「閉じる」ではどこへ戻るか分からず、押してよいかの判断ができない。
     * 戻り先を知っているのは呼び出し元なので、複数の経路から開かれる画面は親からラベルを受け取る。
     */
    it('戻る操作のラベルを「閉じる」にしない（戻り先が分かる語にする）', () => {
        const offenders: string[] = []
        let checked = 0
        for (const [path, text] of shipped) {
            let from = 0
            for (;;) {
                const at = text.indexOf('onClick={onBack}', from)
                if (at < 0) {
                    break
                }
                from = at + 1
                let depth = 0
                let open = -1
                for (let k = at; k < text.length; k += 1) {
                    const ch = text[k]
                    if (ch === '{') {
                        depth += 1
                    } else if (ch === '}') {
                        depth -= 1
                    } else if (ch === '>' && depth === 0 && text[k - 1] !== '=') {
                        open = k
                        break
                    }
                }
                const close = text.indexOf('</Button>', open)
                expect(open, `${path}: 戻るボタンの開始タグを読めない`).toBeGreaterThan(0)
                expect(close, `${path}: 戻るボタンの終わりを読めない`).toBeGreaterThan(open)
                const label = text.slice(open + 1, close).trim()
                checked += 1
                // 変数のラベルは呼び出し元が渡す（親が戻り先を知っている）。
                if (label === '閉じる' || (!label.includes('{') && !/戻る|一覧へ/.test(label))) {
                    offenders.push(`${path}: ${label}`)
                }
            }
        }
        // 空振りで緑にならないための番人。
        expect(checked).toBeGreaterThanOrEqual(15)
        expect(offenders, '戻り先が分からないラベルになっている').toEqual([])
    })

    /*
     * 「次に触る要素」を出す画面と出さない画面を、はっきりさせる。
     *
     * 工程ガイドは画面をまたぐ順序を示すが、いま開いている画面の中で
     * 次に触る要素は画面側が publish する。**どの画面が出すかを一覧で持ち**、
     * 画面が増えたときに「入れ忘れ」に気づけるようにする。
     */
    it('画面内の次操作を示す画面が、意図した一覧と一致している', () => {
        // 業務の主線（資料の取り込み → 対話 → 質問票 → 回答の取込 → 文書）にある画面。ここでの手戻りが最も高くつく。
        const publishes = [
            'Dialogue.tsx', // 対話（画面の中で分岐するため ScreenHint で publish する）
            'Documents.tsx',
            'ImportAnswers.tsx',
            'Imports.tsx',
            'Questionnaires.tsx',
        ]
        const found = shipped
            .filter(([path]) => /\/screens\//.test(path))
            .filter(([, text]) => /useGuideHint|<ScreenHint/.test(text))
            .map(([path]) => path.split('/').pop() as string)
            .sort()
        expect(
            found,
            '次操作を示す画面が増減している。増やしたら一覧へ足し、減らしたなら理由を残すこと',
        ).toEqual([...publishes].sort())
    })

    /*
     * 画面の外枠は「位置で行の役割を決める」書き方をしない。
     *
     * `grid-template-rows: auto auto 1fr auto` のように**位置で**役割を決めていると、
     * 行を 1 つ挿し込んだ時点で割り当てがずれる。実際に工程ガイドを足したとき、
     * ガイドの行が縦の余りを全部取り、本体が内側でスクロールしなくなって
     * **上部ナビが画面外へ出た**（jsdom はレイアウトを持たないため、描画のテストでは捕まらない）。
     */
    it('画面の外枠は本体だけが伸びる書き方になっている（行を足しても壊れない）', () => {
        const shell = Object.entries(styles).find(([path]) => path.endsWith('/AppShell.css'))
        expect(shell, 'AppShell.css が見つからない').toBeDefined()
        const css = shell![1]

        const outer = css.match(/\.rw-shell\s*\{[^}]*\}/)
        expect(outer, '.rw-shell の定義が見つからない').not.toBeNull()
        // 位置で役割を決めない（行を足した瞬間にずれる書き方を禁じる）。
        expect(outer![0], '外枠が行の位置で役割を決めている').not.toMatch(/grid-template-rows/)

        const main = css.match(/\.rw-shell__main\s*\{[^}]*\}/)
        expect(main, '.rw-shell__main の定義が見つからない').not.toBeNull()
        // 伸びるのは本体だけ。内側でスクロールする。
        expect(main![0], '本体が縦の余りを取っていない').toMatch(/flex\s*:\s*1\s+1\s+0/)
        expect(main![0], '本体の中でスクロールしない').toMatch(/overflow\s*:\s*auto/)
        expect(main![0], '本体に min-height: 0 が無い').toMatch(/min-height\s*:\s*0/)
    })

    // 画面の中身がウィンドウの端に接しないこと。
    //
    // ヘッダーとコンテキストストリップは左右 16px を持つのに本体（rw-shell__main）が
    // 持っておらず、全画面で中身が端に貼り付いていた（2026-09-04 に実機で判明）。
    it('画面本体は左右に余白を持つ（ウィンドウ端に接しない）', () => {
        const shell = Object.entries(styles).find(([path]) => path.endsWith('/AppShell.css'))
        expect(shell, 'AppShell.css が見つからない').toBeDefined()
        const block = shell![1].match(/\.rw-shell__main\s*\{[^}]*\}/)
        expect(block, '.rw-shell__main の定義が見つからない').not.toBeNull()
        expect(block![0]).toMatch(/padding\s*:/)
    })
})
