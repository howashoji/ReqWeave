import {render, screen} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {CodexSignIn} from './CodexSignIn'
import {PlanUsage} from './PlanUsage'

/*
 * サインインと残量の**実描画**での崩れ。
 *
 * どちらも折り返せない長い文字列（ポリシーの参照先の URL・メールアドレス）と、
 * 4 列の表を持ち込む。jsdom は幅を持たないため、横のはみ出しは単体テストでは捕まらない。
 * 設定画面は狭い幅でも使えなければならないので、**いちばん狭い側**で見る。
 */

const codexSignInState = vi.hoisted(() => vi.fn())
const codexPlanUsage = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    CodexSignInState: codexSignInState,
    StartCodexSignIn: vi.fn(() => Promise.resolve({})),
    ReopenCodexSignInPage: vi.fn(() => Promise.resolve()),
    CancelCodexSignIn: vi.fn(() => Promise.resolve()),
    CodexPlanUsage: codexPlanUsage,
}))

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, _cb: (ev: unknown) => void) => () => undefined,
}))

/** 折り返せない長い文字列（実際にはみ出しの原因になる形）。 */
const LONG_POLICY_URL = 'https://openai.com/policies/api-data-usage-policies-for-chatgpt-sign-in-accounts/'
const LONG_EMAIL = 'taro.yamamoto.representative.office@example-corporation-group.co.jp'

/** 設定画面の区画と同じくらい狭い幅（最小ウィンドウで想定される内側の幅）。 */
const NARROW = '320px'

/**
 * 枠とその**中のすべての要素**の横のはみ出しを測り、いちばん大きいものを返す。
 *
 * 枠だけを見ると取りこぼす: 段落の中で文字が枠から出ている場合、親の `scrollWidth` は
 * 増えないことがある（実測。`white-space: nowrap` を入れても枠は 318/318 のままだった）。
 * はみ出した要素の名前も返し、どこが原因かを失敗の文言で分かるようにする。
 */
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

describe('サインインと残量のレイアウト（実描画）', () => {
    beforeEach(() => {
        codexSignInState.mockReset()
        codexPlanUsage.mockReset().mockResolvedValue({available: false, fetched: false})
    })

    it('説明（長いポリシーの参照先）が狭い幅で横にはみ出さない', async () => {
        codexSignInState.mockResolvedValue({available: true, state: 'signed_out', waitMinutes: 15})
        render(
            <div style={{width: NARROW}}>
                <CodexSignIn
                    label="既定"
                    methodDescription="ブラウザで OpenAI のサインインのページを開いて許可します。"
                    providerNotice="Codex App Server では、本システムが送る内容に加えて、Codex 自身が道具の定義文を送ります。"
                    policyUrl={LONG_POLICY_URL}
                />
            </div>,
        )

        // 読み込み中の面にも同じ名前が付く。**説明が出てから**測る（読み込み中の短い文で緑にしない）。
        await screen.findByRole('button', {name: 'サインイン'})
        const panel = screen.getByLabelText('ChatGPT のアカウントでのサインイン')
        const worst = worstOverflowX(panel)
        expect(worst.amount, `サインインの説明が横にはみ出している（${worst.where}）`).toBeLessThanOrEqual(0)
    })

    it('成功（長いメールアドレス）が狭い幅で横にはみ出さない', async () => {
        codexSignInState.mockResolvedValue({
            available: true,
            state: 'signed_in',
            account: {email: LONG_EMAIL, planLabel: 'Plus', accountLabel: 'ChatGPT のアカウント'},
        })
        render(
            <div style={{width: NARROW}}>
                <CodexSignIn label="既定" />
            </div>,
        )

        await screen.findByText('サインインしました。')
        const panel = screen.getByLabelText('ChatGPT のアカウントでのサインイン')
        const worst = worstOverflowX(panel)
        expect(worst.amount, `アカウントの情報が横にはみ出している（${worst.where}）`).toBeLessThanOrEqual(0)
    })

    it('待機の操作（2 つのボタン）が狭い幅で横にはみ出さない', async () => {
        codexSignInState.mockResolvedValue({available: true, state: 'waiting', waitMinutes: 15})
        render(
            <div style={{width: NARROW}}>
                <CodexSignIn label="既定" />
            </div>,
        )

        await screen.findByText('ブラウザでサインインを完了してください。')
        const panel = screen.getByLabelText('ChatGPT のアカウントでのサインイン')
        const worst = worstOverflowX(panel)
        expect(worst.amount, `待機の操作が横にはみ出している（${worst.where}）`).toBeLessThanOrEqual(0)
    })

    it('残量の 4 列の表が狭い幅で横にはみ出さない', async () => {
        codexPlanUsage.mockResolvedValue({
            available: true,
            fetched: true,
            planLabel: 'Plus',
            receivedAt: '2026-09-15T09:30:00Z',
            windows: [
                {
                    label: '5 時間の利用枠',
                    usedPercent: 42,
                    windowDurationMins: 300,
                    windowLabel: '5 時間',
                    resetsAt: '2026-09-15T23:00:00Z',
                },
            ],
        })
        render(
            <div style={{width: NARROW}}>
                <PlanUsage label="既定" note="この残量はプロジェクトのトークン上限の判定には使いません。" />
            </div>,
        )

        const panel = await screen.findByLabelText('ChatGPT のプランの残量')
        const worst = worstOverflowX(panel)
        expect(worst.amount, `残量の表が横にはみ出している（${worst.where}）`).toBeLessThanOrEqual(0)
    })
})
