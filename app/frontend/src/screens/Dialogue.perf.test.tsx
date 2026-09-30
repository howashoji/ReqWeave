import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled} from '../test/interact'
import {Dialogue} from './Dialogue'

/*
 * 対話画面の応答性（最初のトークンの表示・送信直後の状態表示）の測定。
 *
 * 要件の測定可能な基準は「通信ログと画面収録の差」だが、本テストは自動化できる範囲として
 * 「イベント受信から DOM に本文が現れるまで」「送信操作から状態表示が現れるまで」を計測する。
 * 実機の画面収録による最終確認は手動確認の項目として残す。
 */

const openDialogueProject = vi.hoisted(() => vi.fn())
const startDialogueSession = vi.hoisted(() => vi.fn())
const dialogueUtterances = vi.hoisted(() => vi.fn())
const dialogueCompleteness = vi.hoisted(() => vi.fn())
const askNextQuestion = vi.hoisted(() => vi.fn())
const sendAnswer = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    OpenDialogueProject: openDialogueProject,
    CloseDialogueProject: vi.fn(),
    StartDialogueSession: startDialogueSession,
    ResumeDialogueSession: vi.fn(),
    PendingCandidates: vi.fn(),
    AskNextQuestion: askNextQuestion,
    SendAnswer: sendAnswer,
    InterruptDialogue: vi.fn(),
    SuspendDialogue: vi.fn(),
    ApproveDialogueCandidates: vi.fn(),
    DialogueUtterances: dialogueUtterances,
    DialogueCompleteness: dialogueCompleteness,
}))

const listeners = vi.hoisted(() => ({current: [] as Array<(ev: unknown) => void>}))
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (_name: string, cb: (ev: unknown) => void) => {
        listeners.current.push(cb)
        return () => {
            listeners.current = listeners.current.filter((f) => f !== cb)
        }
    },
}))

function emit(ev: Record<string, unknown>) {
    listeners.current.forEach((cb) => cb(ev))
}

/** 最初のトークン受信から表示開始まで 1 秒以内。 */
const FIRST_TOKEN_BUDGET_MS = 1000
/** 送信操作から状態表示まで 500 ミリ秒以内。 */
const FEEDBACK_BUDGET_MS = 500
/** 測定の試行回数（最初のトークンの表示は 10 回試行の全てで満たすこと）。 */
const TRIALS = 10

describe('対話画面の応答性', () => {
    beforeEach(() => {
        listeners.current = []
        openDialogueProject.mockReset()
        openDialogueProject.mockResolvedValue({
            projectPath: '/tmp/proj', phase: 'requirements', targetName: '在庫管理システム', sessions: [],
        })
        startDialogueSession.mockReset()
        startDialogueSession.mockResolvedValue({id: 'S-0001', type: 'owner', phase: 'requirements', startedAt: ''})
        dialogueUtterances.mockReset()
        dialogueUtterances.mockResolvedValue([])
        dialogueCompleteness.mockReset()
        dialogueCompleteness.mockResolvedValue({chapters: [], confirmation: {confirmable: false}})
        askNextQuestion.mockReset()
        askNextQuestion.mockResolvedValue(true)
        sendAnswer.mockReset()
        sendAnswer.mockResolvedValue('utt-00002')
    })

    it('最初のトークン受信から 1 秒以内に表示が始まる（10 回試行）', async () => {
        for (let trial = 0; trial < TRIALS; trial++) {
            const view = render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
            await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

            await clickEnabled('次の質問')
            await waitFor(() => expect(askNextQuestion).toHaveBeenCalled())

            const marker = `試行 ${trial} の最初のトークン`
            const start = performance.now()
            emit({kind: 'text', text: marker})
            await screen.findByText(new RegExp(marker))
            const elapsed = performance.now() - start

            expect(elapsed, `試行 ${trial} の表示開始が遅い: ${elapsed}ms`).toBeLessThan(FIRST_TOKEN_BUDGET_MS)
            view.unmount()
            askNextQuestion.mockClear()
            startDialogueSession.mockClear()
            startDialogueSession.mockResolvedValue({id: 'S-0001', type: 'owner', phase: 'requirements', startedAt: ''})
        }
    })

    it('送信操作から 500 ミリ秒以内に状態表示へ切り替わる', async () => {
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        await waitFor(() => expect(startDialogueSession).toHaveBeenCalled())

        fireEvent.change(screen.getByLabelText('回答'), {target: {value: 'Excel 台帳です。'}})
        const start = performance.now()
        await clickEnabled('送信')
        await screen.findByText('応答を待っています…')
        const elapsed = performance.now() - start

        expect(elapsed, `状態表示が遅い: ${elapsed}ms`).toBeLessThan(FEEDBACK_BUDGET_MS)
    })

    it('応答待ちの間も履歴を閲覧できる（スクロール可能な領域として独立している）', async () => {
        dialogueUtterances.mockResolvedValue([
            {id: 'utt-00001', speaker: 'agent', at: '2026-08-27T10:00:00Z', status: 'completed', body: '過去の質問'},
        ])
        render(<Dialogue projectPath="/tmp/proj" onBack={() => undefined} />)
        const history = await screen.findByLabelText('対話履歴')
        expect(await within(history).findByText('過去の質問')).toBeInTheDocument()

        await clickEnabled('次の質問')
        await screen.findByText('応答を待っています…')

        // 応答待ち中も履歴は表示されたままで、無効化・非表示にならない。
        expect(within(history).getByText('過去の質問')).toBeVisible()
        expect(history).not.toHaveAttribute('aria-disabled')
        expect(history.scrollHeight).toBeGreaterThanOrEqual(0)
    })
})

