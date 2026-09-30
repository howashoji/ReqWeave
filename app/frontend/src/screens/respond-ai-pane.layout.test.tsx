import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Respond} from './Respond'

/*
 * 回答モードの AI 対話ペイン（回答画面の下部）の**実描画**での崩れ。
 *
 * 設計が求めるのは「最小ウィンドウ（1024×720）でペインを開いたまま、質問本文・背景説明・回答欄・
 * フッターの『次の質問へ』が同時に見えていること」。jsdom は高さを計算しないため、押し下げ・
 * はみ出しは単体テストでは捕まらない（実ブラウザのテストを置く理由そのもの）。ここでは実ブラウザで寸法を見る。
 */

const openQuestionnaireFile = vi.hoisted(() => vi.fn())
const respondAIDialogue = vi.hoisted(() => vi.fn())
/*
 * 実ブラウザでは**モジュール全体が本物と同じ形**でないと読み込めない（足りない export で失敗する）。
 * そこで本物を読み込み、この画面が呼ぶものだけを差し替える（呼ばないものは本物のまま残す）。
 */
vi.mock('../../wailsjs/go/binding/API', async (importOriginal) => ({
    ...(await importOriginal<Record<string, unknown>>()),
    OpenQuestionnaireFile: openQuestionnaireFile,
    SaveAnswer: vi.fn(() => Promise.resolve({answered: 0, total: 1})),
    RespondAIDialogue: respondAIDialogue,
    RespondAIProviders: vi.fn(() => Promise.resolve([])),
    RespondAIScope: vi.fn(() => Promise.resolve({sent: [], notSent: [], noRecordNotice: ''})),
    CodexSignInState: vi.fn(() => Promise.resolve({available: false, state: 'signed_out'})),
}))

vi.mock('../../wailsjs/runtime/runtime', () => ({EventsOn: () => () => undefined}))

const QUESTION = {
    id: 'q-01',
    text: '月末の締め作業で手作業になっている工程を教えてください。',
    background: '対象範囲の判断に使います。作業の名前と担当の部署が分かると、質問を絞り込めます。',
    answerFormat: 'free',
}

const OPENED = {
    questionnaireId: 'QS-001',
    addressee: '佐藤（営業部）',
    questions: [QUESTION, {...QUESTION, id: 'q-02', text: '連絡経路を教えてください。'}],
    status: 'answering',
    restored: false,
}

/** 履歴がペインの高さを超える量の発話（ペインの中だけで送れることを見る）。 */
const LONG_HISTORY = {
    enabled: true,
    providerId: 'anthropic',
    providerLabel: 'Anthropic（Claude）',
    authMethod: 'secret_key',
    authMethodLabel: 'シークレットキー方式',
    canSignOut: false,
    running: false,
    utterances: Array.from({length: 24}, (_, i) => ({
        id: `utt-${i}`,
        speakerLabel: i % 2 === 0 ? 'あなた' : 'AI',
        isAgent: i % 2 === 1,
        at: '2026-09-16 12:00',
        body: `やり取り ${i + 1}: 締め作業のうち、どの工程を手作業と呼ぶかを決めるところから始めます。`,
        canApply: i % 2 === 1,
    })),
}

/** 枠と中のすべての要素の横のはみ出しのうち、いちばん大きいもの。 */
function worstOverflowX(root: HTMLElement): {amount: number; where: string} {
    let worst = {amount: root.scrollWidth - root.clientWidth, where: root.className}
    for (const el of Array.from(root.querySelectorAll('*')) as HTMLElement[]) {
        const amount = el.scrollWidth - el.clientWidth
        if (amount > worst.amount) {
            worst = {amount, where: `${el.tagName.toLowerCase()}.${el.className}`}
        }
    }
    return worst
}

/** 最小ウィンドウ（1024×720）の作業領域に回答モードを描いて開く。 */
async function openAt(height: number) {
    const {container} = render(
        <div style={{width: '1024px', height: `${height}px`}}>
            <Respond filePath="/tmp/QS-001.rwvq" fileName="QS-001.rwvq" />
        </div>,
    )
    fireEvent.change(await screen.findByLabelText('パスコード'), {target: {value: 'AbCdEfGh2345'}})
    fireEvent.click(screen.getByRole('button', {name: '質問をひらく'}))
    await screen.findByText(QUESTION.text)
    return container.firstElementChild as HTMLElement
}

function q(selector: string): HTMLElement {
    const el = document.querySelector(selector) as HTMLElement | null
    expect(el, `${selector} が無い`).not.toBeNull()
    return el as HTMLElement
}

describe('回答モードの AI 対話ペインのレイアウト（実描画）', () => {
    beforeEach(() => {
        vi.clearAllMocks()
        openQuestionnaireFile.mockResolvedValue(OPENED)
        respondAIDialogue.mockResolvedValue(LONG_HISTORY)
    })

    it('最小ウィンドウでペインを開いても、質問・背景説明・回答欄・「次の質問へ」が同時に見える', async () => {
        const frame = await openAt(720)
        fireEvent.click(screen.getByRole('button', {name: 'AI と相談する'}))
        await screen.findByLabelText('相談したいこと')

        const box = frame.getBoundingClientRect()
        const mustBeVisible: Array<[string, HTMLElement]> = [
            ['質問本文', screen.getByText(QUESTION.text)],
            ['背景説明', screen.getByText(QUESTION.background)],
            ['回答欄', q('.rw-respond__fieldset')],
            ['次の質問へ', screen.getByRole('button', {name: '次の質問へ'})],
            // ペイン自身も見えていること。本文の中に積むと、開いた瞬間にここが折り返しの下へ出る。
            ['相談の入力欄', screen.getByLabelText('相談したいこと')],
        ]
        for (const [name, el] of mustBeVisible) {
            const r = el.getBoundingClientRect()
            expect(r.height, `${name} の高さが 0（描かれていない）`).toBeGreaterThan(0)
            expect(r.top, `${name} が作業領域の上へ出ている`).toBeGreaterThanOrEqual(box.top - 1)
            expect(r.bottom, `${name} が作業領域の下へ押し出されている`).toBeLessThanOrEqual(box.bottom + 1)
        }

        // 横のはみ出しを作らない（入力欄の固有幅で右へ出るのが典型。以前に実測した崩れ方）。
        const worst = worstOverflowX(frame)
        expect(worst.amount, `横にはみ出している: ${worst.where}`).toBeLessThanOrEqual(1)
    })

    it('対話履歴はペインの中だけでスクロールする（ページ全体を伸ばさない）', async () => {
        const frame = await openAt(720)
        const beforeBottom = q('.rw-wizard__footer').getBoundingClientRect().bottom
        fireEvent.click(screen.getByRole('button', {name: 'AI と相談する'}))
        await screen.findByLabelText('相談したいこと')

        const history = q('.rw-respond-ai__history')
        expect(history.scrollHeight, '履歴がペインの高さを超えていない（検査が成立していない）').toBeGreaterThan(
            history.clientHeight,
        )
        // 送れるのは履歴だけ。フッターの位置は開く前と変わらない。
        expect(q('.rw-wizard__footer').getBoundingClientRect().bottom).toBeCloseTo(beforeBottom, 0)
        expect(frame.scrollHeight - frame.clientHeight, 'ページ全体が縦に伸びている').toBeLessThanOrEqual(1)
    })

    it('境界のつまみでペインの高さを変えられ、上側の最小高さを割らない', async () => {
        await openAt(720)
        fireEvent.click(screen.getByRole('button', {name: 'AI と相談する'}))
        await screen.findByLabelText('相談したいこと')

        const resizer = screen.getByRole('separator', {name: 'AI との相談の高さ'})
        const before = q('.rw-wizard__pane').getBoundingClientRect().height
        // キーボードでも変えられること（ポインタを使えない利用者を締め出さない）。
        fireEvent.keyDown(resizer, {key: 'ArrowUp'})
        expect(q('.rw-wizard__pane').getBoundingClientRect().height).toBeGreaterThan(before)

        // 目いっぱい広げても、上側（質問〜回答欄）は 320px を割らない。
        fireEvent.keyDown(resizer, {key: 'End'})
        // 実寸は端数を持つ（行の高さの丸め）。1px の端数まで踏み込んで赤にしない。
        expect(q('.rw-wizard__body').getBoundingClientRect().height).toBeGreaterThan(319)
        expect(q('.rw-wizard__pane').getBoundingClientRect().height).toBeGreaterThanOrEqual(200)
        expect(screen.getByRole('button', {name: '次の質問へ'})).toBeTruthy()
    })

    it('高さが足りないときは開く操作を無効化し、理由と次の行動を示す', async () => {
        // 上側 320px + ペイン 200px を満たせない高さ（文字の拡大・表示倍率で起こりうる）。
        await openAt(420)
        // 高さは描画後に測って初めて分かる（測る前に「開けない」と断定しない）。
        const toggle = await waitFor(() => {
            const found = screen.getByRole('button', {name: 'AI と相談する'})
            expect(found).toBeDisabled()
            return found
        })

        fireEvent.mouseOver(toggle)
        expect(document.body.textContent).toContain('ウィンドウを高くする')
        // 押し下げ・はみ出しで代替していない（ペインは開いていない）。
        expect(document.querySelector('.rw-wizard__pane')).toBeNull()
    })
})
