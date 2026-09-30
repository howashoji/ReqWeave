import {useCallback, useEffect, useMemo, useRef, useState} from 'react'
import {
    AcknowledgeChangeSummary,
    ApproveDialogueCandidates,
    AskNextQuestion,
    ChangeSummary,
    IDRangeWarnings,
    CloseDialogueProject,
    DialogueCompleteness,
    WorkflowGuide,
    DialogueUtterances,
    InterruptDialogue,
    OpenDialogueProject,
    PendingCandidates,
    ResumeDialogueSession,
    SendAnswer,
    StartDialogueSession,
    SuspendDialogue,
    UsageStatusNow,
} from '../../wailsjs/go/binding/API'
import {binding, dialogue} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {AppShell, Banner, Button, CandidateEvidence, ChangeSummaryPanel, Chip, EmptyState, FlyingCard, IDRangeNotice, lacksEvidence, MergePanel, noEvidenceReason, prefersReducedMotion, ScreenHint, SkeletonCard, Toast, UsageNotice, Utterance, errorText, useFollowLatest} from '../ui'
import type {CycleStep} from '../ui'
import {Documents} from './Documents'
import {Export} from './Export'
import {ImportAnswers} from './ImportAnswers'
import {Imports} from './Imports'
import {Members} from './Members'
import {Sync} from './Sync'
import {SyncCredentials} from './SyncCredentials'
import {SyncRemote} from './SyncRemote'
import {ProgressReport} from './ProgressReport'
import {QuestionnaireManager} from './Questionnaires'
import {DecisionsAndIssues, DialogueHistory, RequirementList} from './Records'
import {TokenUsage} from './TokenUsage'
import {UsageDashboard} from './UsageDashboard'
import {UsageLimit} from './UsageLimit'
import './Dialogue.css'

/**
 * 対話画面。
 *
 * ストリーミング表示・入力欄と送信フィードバック・
 * 抽出候補パネル・完成度表示・中断ボタン。
 *
 * 完成度と確定可否はバックエンドの算出値をそのまま表示する（UI 側で再計算しない。算出を 1 か所に集める）。
 * ネイティブ confirm/alert/prompt は使わない（アプリ内の表示で完結させる）。
 */

/** 対話状態の日本語ラベル（保存値は英語識別子）。型で網羅を強制する。 */
/**
 * 回答送信〜抽出結果の到着までの 4 状態。
 *
 * - `idle`       … 通常。入力できる
 * - `sending`    … 送信中。入力欄を止める
 * - `extracting` … 抽出中。中央は沈み込み、右はスケルトンを出す
 * - `arrived`    … 到着直後の短い遷移（カードが飛ぶ）。すぐ `idle` へ戻る
 */
export type ExtractionPhase = 'idle' | 'sending' | 'extracting' | 'arrived'

/** `arrived` を保つ時間（ms）。飛ぶトランジション（280ms）を見せ切ってから通常へ戻す。 */
const ARRIVED_MS = 300

const STATE_LABELS = {
    'questioning': '質問を作成中',
    'awaiting-answer': '回答をお待ちしています',
    'extracting': '回答を分析中',
    'awaiting-approval': '候補の承認をお待ちしています',
    'applying': '記録へ反映中',
    'suspended': '中断中',
    'failed': '接続に失敗しました',
} as const

type DialogueState = keyof typeof STATE_LABELS

function stateLabel(state: string): string {
    return state in STATE_LABELS ? STATE_LABELS[state as DialogueState] : '状態不明'
}

/** 章観点の 3 状態。色だけで区別しないため文字ラベルを併記する。 */
const CHAPTER_STATE_LABELS = {
    untouched: '未着手',
    'with-open-issues': '記載あり・未決あり',
    settled: '記載あり・未決なし',
} as const

type ChapterState = keyof typeof CHAPTER_STATE_LABELS

function chapterStateLabel(state: string): string {
    return state in CHAPTER_STATE_LABELS ? CHAPTER_STATE_LABELS[state as ChapterState] : '状態不明'
}

type Tone = 'info' | 'accent' | 'warn' | 'danger'

/**
 * 対話イベント（バックエンドの dialogue.Event）。
 *
 * Wails のバインディング生成はメソッドの引数・戻り値の型だけを出力するため、
 * イベントで届く型はここで定義する（フィールド名はバックエンドの JSON タグと一致させる）。
 */
type DialogueEvent = {
    kind: 'text' | 'replaced' | 'candidates' | 'fallback' | 'done' | 'error'
    text?: string
    utteranceId?: string
    state?: string
    interrupted?: boolean
    topicKey?: string
    reconfirm?: boolean
    errorClass?: string
    /** 利用者向けの 1 文（原因＋次の行動）。文言の正本はバックエンドのエラーカタログ。 */
    userMessage?: string
    message?: string
    extraction?: dialogue.Extraction
}

/**
 * AI 呼び出しを伴うビュー（トークン上限の警告・ブロックは、AI 呼び出しを伴う各画面に表示する）。
 * バインディング側で上限判定の前段（beginAICall）を通る操作を持つ画面と一致させる:
 * 対話（次の質問・回答からの抽出）/ 質問票（質問文の生成）/ 回答取込（回答の分析）/
 * 資料取込（資料の分析）/ 成果物（成果物の生成）。
 */
const AI_VIEWS = ['dialogue', 'questionnaires', 'import-answers', 'imports', 'documents', 'design-documents'] as const

/**
 * 対話の文脈を保ったまま切り替える画面。
 *
 * 工程ガイドが返す行き先もこの集合の名前で来る。**値と型を 1 か所から作る**ため
 * 配列で持ち、型はそこから導く（型だけ直して実体を直し忘れる事故を防ぐ）。
 */
const VIEWS = [
    'dialogue',
    'records',
    'requirements',
    'history',
    'questionnaires',
    'import-answers',
    'imports',
    'progress-report',
    'members',
    'usage',
    'usage-limit',
    'token-usage',
    'documents',
    'design-documents',
    'export',
    'sync',
    'sync-remote',
    'sync-credentials',
] as const

type View = (typeof VIEWS)[number]

/** 候補の承認状態（承認 / 編集して承認 / 破棄）。 */
type CandidateDecision = 'pending' | 'approved' | 'discarded'

export function Dialogue({
    projectPath,
    onBack,
    importFile,
    onImportFileHandled,
    initialView,
}: {
    projectPath: string
    onBack: () => void
    /** initialView は開いた直後に表示するパネル（プロジェクト一覧から同期を開く経路）。 */
    initialView?: 'sync'
    // importFile は OS のファイル関連付け・ドラッグで渡された返送ファイル。
    // 受け取ったら回答取込を開いてそのファイルを検証する。
    importFile?: string
    onImportFileHandled?: () => void
}) {
    const [opened, setOpened] = useState<binding.DialogueOpenResult | null>(null)
    const [sessionID, setSessionID] = useState('')
    const [utterances, setUtterances] = useState<binding.UtteranceView[]>([])
    const [streaming, setStreaming] = useState('')
    /*
     * 回答送信〜抽出結果の到着までの画面ステート。
     *
     * 対話ペインと抽出候補ペインが**同じ 1 つの値**を見る（別々に持つと表示がずれる）。
     * `busy` / `waiting` は質問の生成待ちでも立つため、抽出中だけを表すこの値と役割を分ける。
     */
    const [phase, setPhase] = useState<ExtractionPhase>('idle')
    const [busy, setBusy] = useState(false)
    const [waiting, setWaiting] = useState(false)
    const [state, setState] = useState<string>('suspended')
    const [answer, setAnswer] = useState('')
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<Tone>('info')
    const [completeness, setCompleteness] = useState<binding.DialogueCompletenessView | null>(null)
    // 工程ガイド。導出はバックエンドが行い、画面は表示と移動だけを担う。
    const [guide, setGuide] = useState<binding.GuideView | null>(null)
    const [candidates, setCandidates] = useState<dialogue.Extraction | null>(null)
    const [fallbackText, setFallbackText] = useState('')
    const [decisionMarks, setDecisionMarks] = useState<Record<string, CandidateDecision>>({})
    const [edits, setEdits] = useState<Record<string, string>>({})
    const [owners, setOwners] = useState<Record<string, {owner: string; due: string}>>({})
    // 反映に失敗した理由。候補パネルの「選んだ内容で反映」のそばに出す。
    const [approveError, setApproveError] = useState('')
    // 候補が入れ替わったら（新しい抽出・反映の完了）前の失敗理由は消す。
    useEffect(() => setApproveError(''), [candidates])
    // 競合。反映は行われていない状態で、本人がマージのしかたを選ぶ。
    // 変更要約。開いたときに自動表示し、メニューから再表示できる。
    const [changeSummary, setChangeSummary] = useState<binding.ChangeSummaryView | null>(null)
    // ID の番号帯の残量。残りが少ない種別だけが返る。
    const [idRanges, setIdRanges] = useState<binding.IDRangeWarningView[] | null>(null)

    /**
     * 変更要約を閉じる。**確認済みの取り込みの位置**を記録し、次回は以降の分だけを提示する
     * （位置は端末ごとのアプリ設定に持つ）。記録に失敗しても閉じる操作は止めない。
     */
    /** 番号帯の残量を取り直す（開いたとき・同期のあと。取得できない場合は通知を出さない）。 */
    const reloadIdRanges = useCallback(() => {
        Promise.resolve()
            .then(() => IDRangeWarnings())
            .then(setIdRanges)
            .catch(() => setIdRanges(null))
    }, [])

    const closeChangeSummary = useCallback(() => {
        setChangeSummary(null)
        void AcknowledgeChangeSummary().catch(() => undefined)
    }, [])
    const [conflicts, setConflicts] = useState<binding.ConflictView[]>([])
    const [conflictNotice, setConflictNotice] = useState('')
    // 利用量と上限。判定済みの値をそのまま表示する（画面で判定しない）。
    const [usage, setUsage] = useState<binding.UsageStatus | null>(null)
    const [usageDismissed, setUsageDismissed] = useState(false)
    /*
     * 一過性の操作結果（トーストで出す）。
     *
     * 反映の結果を中央ペイン上端のバナーへ出していたが、押したボタンも候補も**右ペイン**にあり、
     * 視線の外だった。「押したのに何も起きない」に見えていた原因のひとつ。
     * 次の操作まで残る事実（競合・エラー）は従来どおりバナーへ置く。
     */
    const [toast, setToast] = useState<{tone: 'accent' | 'warn'; message: string} | null>(null)
    const streamRef = useRef('')
    // 飛ぶ演出の出発点（対話ログの右端）と着地点（抽出候補パネル）。位置は描画後に測る。
    const streamElRef = useRef<HTMLDivElement | null>(null)
    const sideElRef = useRef<HTMLElement | null>(null)
    const [flight, setFlight] = useState<{from: DOMRect; to: DOMRect} | null>(null)
    /*
     * 対話履歴は最新の発話が見える位置に保つ。
     * 新しい質問が末尾に出ても履歴が古い位置のままで、利用者が気付けなかった（0.1.4）。
     * 利用者が上へ戻って読んでいる間は送らない。「次の質問」「送信」の直後は必ず末尾へ。
     */
    const followLatest = useFollowLatest(streamElRef, [utterances, streaming, waiting])
    // 一覧系（決定・未決／要件項目／対話履歴）は対話の文脈を保ったまま切り替える。
    const [view, setView] = useState<View>(initialView ?? 'dialogue')

    const reloadUtterances = useCallback(async (id: string) => {
        const list = await DialogueUtterances(id)
        setUtterances(list)
    }, [])

    // 利用量は AI 呼び出しのたびに変わる。表示値は取得口の結果をそのまま使う（上限の判定はバインディングの前段に集めてある）。
    const reloadUsage = useCallback(async () => {
        try {
            const next = await UsageStatusNow()
            setUsage(next)
            // 状態が上がった（none → warn → blocked）ときは、閉じた警告をもう一度出す。
            setUsageDismissed((dismissed) => (next.level === 'none' ? false : dismissed))
        } catch {
            /* 利用量が取れなくても対話は続けられる（上限判定はバインディング前段が行う） */
        }
    }, [])

    const reloadCompleteness = useCallback(async () => {
        try {
            setCompleteness(await DialogueCompleteness())
        } catch {
            /* 完成度が取れなくても対話は続けられる */
        }
        try {
            setGuide(await WorkflowGuide())
        } catch {
            /* 工程ガイドが取れなくても作業は続けられる（手掛かりであり関門ではない） */
        }
    }, [])

    // 画面を切り替えるたびに工程ガイドを引き直す（別の画面での操作で状態が変わっている）。
    useEffect(() => {
        void reloadCompleteness()
    }, [view, reloadCompleteness])

    // OS から返送ファイルを受け取ったら回答取込を開く。
    useEffect(() => {
        if (importFile) {
            setView('import-answers')
        }
    }, [importFile])

    // 対話イベント。チャンクは受信のたびに追記する（履歴全体を再描画しない）。
    useEffect(() => {
        const off = EventsOn('dialogue:event', (ev: DialogueEvent) => {
            switch (ev.kind) {
                case 'text':
                    setWaiting(false)
                    streamRef.current += ev.text ?? ''
                    setStreaming(streamRef.current)
                    break
                case 'replaced':
                    // 既決の論点だったため作り直す（決まった論点を聞き直さない）。表示中の本文を捨てる。
                    streamRef.current = ''
                    setStreaming('')
                    setTone('info')
                    setMessage('決定済みの論点だったため、質問を作り直しています。')
                    break
                case 'candidates':
                    setCandidates(ev.extraction ?? null)
                    setDecisionMarks({})
                    setState(ev.state ?? '')
                    setBusy(false)
                    setWaiting(false)
                    setPhase('arrived')
                    break
                case 'fallback':
                    setCandidates(null)
                    setFallbackText(ev.text ?? '')
                    setState(ev.state ?? '')
                    setBusy(false)
                    setWaiting(false)
                    // 読み取れなかったときも待機表示は畳む（スケルトンを残して待たせない）。
                    setPhase('idle')
                    setTone('warn')
                    setMessage('分析結果を読み取れませんでした。応答内容を確認して手動で起票してください。')
                    break
                case 'done':
                    streamRef.current = ''
                    setStreaming('')
                    setBusy(false)
                    setWaiting(false)
                    setState(ev.state ?? '')
                    // 中断・質問生成の完了はいずれも待機表示の終わり。
                    setPhase('idle')
                    if (ev.interrupted) {
                        setTone('warn')
                        setMessage('中断しました。ここまでの内容は履歴に残っています。')
                    } else if (ev.reconfirm) {
                        setTone('info')
                        setMessage('決定済みの内容を変更するための再確認です。')
                    }
                    break
                case 'error':
                    streamRef.current = ''
                    setStreaming('')
                    setBusy(false)
                    setWaiting(false)
                    setState(ev.state ?? '')
                    // 失敗したことと次の行動はバナーが示す。待機表示は畳む。
                    setPhase('idle')
                    setTone('danger')
                    setMessage(errorMessage(ev))
                    break
                default:
                    break
            }
        })
        return () => off()
    }, [])

    // イベントで発話が確定したら履歴を読み直す。
    useEffect(() => {
        if (!sessionID || busy) {
            return
        }
        void reloadUtterances(sessionID)
        void reloadCompleteness()
        void reloadUsage()
    }, [sessionID, busy, reloadUtterances, reloadCompleteness, reloadUsage])

    // 質問の生成を要求する。session を引数に取るのは、**セッションを作った直後**にも
    // 呼ぶため（state 反映を待たずに新しい id で呼ぶ）。
    const askQuestion = useCallback(async (session: string) => {
        if (!session) {
            return
        }
        setBusy(true)
        setWaiting(true)
        setMessage('')
        streamRef.current = ''
        setStreaming('')
        // 新しい質問はここから末尾に出る。押した本人が見に行けるよう末尾へ送る。
        followLatest()
        try {
            const asked = await AskNextQuestion(session)
            if (!asked) {
                setBusy(false)
                setWaiting(false)
                setTone('accent')
                setMessage('すべての章観点が充足しました。成果物の生成へ進めます。')
            }
        } catch (err: unknown) {
            setBusy(false)
            setWaiting(false)
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [followLatest])

    const askNext = useCallback(() => askQuestion(sessionID), [askQuestion, sessionID])

    // 画面に入ったらプロジェクトを開き、既存セッションがあれば再開する。
    useEffect(() => {
        let cancelled = false
        Promise.resolve()
            .then(() => OpenDialogueProject(projectPath))
            .then(async (result) => {
                if (cancelled) {
                    return
                }
                setOpened(result)
                // 前回以降の他の作業者による変更を提示する（変更要約）。
                try {
                    const summary = await ChangeSummary()
                    if (!cancelled && summary.items?.length) {
                        setChangeSummary(summary)
                    }
                } catch {
                    /* 変更要約が取れなくても対話は続けられる */
                }
                // ID の番号帯の残量。同期先が未設定なら空が返る。
                reloadIdRanges()
                const existing = result.sessions?.[0]
                if (existing) {
                    setSessionID(existing.id)
                    const resumed = await ResumeDialogueSession(existing.id)
                    setState(resumed.state)
                    const pending = await PendingCandidates(existing.id)
                    setCandidates(pending)
                    if (resumed.presentedQuestion && !resumed.presentedQuestion.answered) {
                        setTone('info')
                        setMessage('中断した対話を再開しました。前回の質問にお答えください。')
                    }
                    return
                }
                const created = await StartDialogueSession()
                setSessionID(created.id)
                setState('questioning')
            })
            .catch((err: unknown) => {
                setTone('danger')
                setMessage(errorText(err))
            })
        return () => {
            cancelled = true
            void CloseDialogueProject()
        }
    }, [projectPath, reloadIdRanges])


    const send = useCallback(async () => {
        if (!sessionID || !answer.trim()) {
            return
        }
        // 送信操作から 500ms 以内の状態表示はフロント側で即時に行う（バックエンドの応答を待たない）。
        setBusy(true)
        setWaiting(true)
        setPhase('sending')
        setMessage('')
        // 送った回答は末尾に出る。押した本人が見に行けるよう末尾へ送る。
        followLatest()
        const text = answer
        try {
            await SendAnswer(sessionID, text)
            setAnswer('')
            // 送信が通った時点から抽出中。右ペインに結果の型（スケルトン）を先に出す。
            setPhase('extracting')
            await reloadUtterances(sessionID)
        } catch (err: unknown) {
            // 送信失敗時は入力テキストを保持して再送できるようにする（書いた回答を失わせない）。
            setBusy(false)
            setWaiting(false)
            setPhase('idle')
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [sessionID, answer, reloadUtterances, followLatest])

    // 到着直後の演出は短く終わらせ、通常の操作へ戻す。
    useEffect(() => {
        if (phase !== 'arrived') {
            return
        }
        const timer = setTimeout(() => setPhase('idle'), ARRIVED_MS)
        return () => clearTimeout(timer)
    }, [phase])

    /*
     * 到着の瞬間、中央から右ペインへカードを 1 つだけ飛ばす（候補がどこへ出たかを目で追えるように）。
     * 位置は実際の要素から測る（固定値を書くとレイアウトを変えたときに合わなくなる）。
     * 動きを減らす設定のときは飛ばさない。演出が無くても、候補は右ペインに現れる。
     */
    useEffect(() => {
        if (phase !== 'arrived' || prefersReducedMotion()) {
            return
        }
        const from = streamElRef.current?.getBoundingClientRect()
        const to = sideElRef.current?.getBoundingClientRect()
        if (!from || !to) {
            return
        }
        setFlight({from, to})
    }, [phase])

    const interrupt = useCallback(async () => {
        try {
            await InterruptDialogue()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const approve = useCallback(async (merges: dialogue.MergeResolution[] = []) => {
        if (!sessionID || !candidates) {
            return
        }
        const request = buildApproval(candidates, decisionMarks, edits, owners)
        request.merges = merges
        setBusy(true)
        setApproveError('')
        try {
            const outcome = await ApproveDialogueCandidates(sessionID, request)
            if (outcome.conflicts?.length) {
                // 反映は行われていない。三面を出して本人がマージのしかたを選ぶ（三面マージ）。
                setConflicts(outcome.conflicts)
                setConflictNotice(outcome.notice ?? '')
                setTone('warn')
                setMessage(outcome.notice ?? '他のメンバーの変更と競合しています。')
                return
            }
            setConflicts([])
            setConflictNotice('')
            setCandidates(null)
            setDecisionMarks({})
            setEdits({})
            setState(outcome.applied?.state ?? '')
            setToast({tone: 'accent', message: approvalMessage(outcome.applied)})
            await reloadCompleteness()
            await reloadUtterances(sessionID)
        } catch (err: unknown) {
            // 押したボタンのそばに出す。中央ペイン上端のバナーは、右ペインを下へスクロールして
            // 押した利用者から見えない（成功側は同じ理由でトーストへ移した）。
            setApproveError(errorText(err))
        } finally {
            setBusy(false)
        }
    }, [sessionID, candidates, decisionMarks, edits, owners, reloadCompleteness, reloadUtterances])

    const sections = useMemo(
        () => ({
            sections: (completeness?.chapters ?? []).map((c) => ({label: c.name, percent: c.percent})),
        }),
        [completeness],
    )

    /*
     * 1 サイクルの現在地。
     * 対話の画面でだけ出す（他の画面では 1 往復の流れの中に居ないため）。
     */
    const cycleStep = useMemo<CycleStep | undefined>(() => {
        if (view !== 'dialogue') {
            return undefined
        }
        if (phase === 'sending') {
            return '回答'
        }
        if (phase === 'extracting') {
            return '抽出'
        }
        if (candidates) {
            return '承認'
        }
        return '質問'
    }, [view, phase, candidates])

    /**
     * 工程ガイドの表示内容。
     *
     * 行き先はバックエンドが工程 ID で返す。**この画面の view 名と同じ集合**に閉じており、
     * 対応表を画面側で作らない（増えたときに片方だけ直す事故を避ける）。
     * 知らない行き先が来たら移動操作を出さない（押しても何も起きないボタンを作らない）。
     */
    const guideStrip = useMemo(() => {
        if (!guide) {
            return undefined
        }
        const move = (target: string) =>
            (VIEWS as readonly string[]).includes(target)
                ? () => setView(target as typeof view)
                : undefined
        const go = move(guide.target)
        const goNote = guide.noteTarget ? move(guide.noteTarget) : undefined
        return {
            stageLabel: guide.stageLabel,
            stageIndex: guide.stageIndex,
            stageTotal: guide.stageTotal,
            next: guide.next,
            action: go ? {label: guide.button, onClick: go} : undefined,
            note: guide.note,
            noteAction: goNote && guide.noteButton ? {label: guide.noteButton, onClick: goNote} : undefined,
            cycleStep,
            // 工程の全体像。並びも説明もバックエンドが持つ。
            stages: guide.stages,
            stageId: guide.stageId,
            onMove: move,
        }
    }, [guide, cycleStep])



    const counts = useMemo(() => {
        const ex = candidates
        const pending = ex
            ? ex.decisions.length + ex.open_issues.length + ex.requirement_updates.length
            : 0
        const open = completeness?.chapters.reduce((sum, c) => sum + c.openIssues, 0) ?? 0
        const decided = completeness?.chapters.reduce((sum, c) => sum + c.satisfied, 0) ?? 0
        return {decided, open, candidates: pending}
    }, [candidates, completeness])

    const canSend = Boolean(sessionID) && answer.trim().length > 0 && !busy
    const showInterrupt = busy
    // 「質問を作成中」なのに何も動いていない状態（生成が失敗した後など）を見分ける。
    // 進行中だと偽らない。**次に押すものは工程ガイドが示す**ため、ここは状態だけを言う
    // （同じ文を 2 か所に出さない）。
    const idleAwaitingQuestion = state === 'questioning' && !busy && !waiting && !streaming
    // 送信〜抽出の間だけ入力を止める（質問の生成待ちでは止めない）。「中断」は有効のまま。
    const sendingOrExtracting = phase === 'sending' || phase === 'extracting'

    return (
        <AppShell
            breadcrumb={['reqweave', opened?.targetName ?? '', phaseLabel(opened?.phase)]}
            nav={
                <>
                    {/* 上部ナビの各ボタンには機能を示す 1 文を添える（まず上部ナビから） */}
                    <Button
                        onClick={() => setView('records')}
                        tooltip="対話から抽出した決定事項と未決事項を一覧で確認します。"
                    >
                        決定・未決
                    </Button>
                    <Button
                        onClick={() => setView('requirements')}
                        tooltip="抽出済みの要件項目を一覧で確認し、内容を直します。"
                    >
                        要件項目
                    </Button>
                    <Button
                        onClick={() => setView('history')}
                        tooltip="これまでのやり取りをさかのぼって読みます。"
                    >
                        対話履歴
                    </Button>
                    <Button
                        onClick={() => setView('questionnaires')}
                        tooltip="関係者に確認したいことを質問票にまとめて書き出します。"
                    >
                        質問票
                    </Button>
                    <Button
                        onClick={() => setView('import-answers')}
                        tooltip="関係者から返ってきた回答ファイルを取り込みます。"
                    >
                        回答取込
                    </Button>
                    <Button
                        onClick={() => setView('imports')}
                        tooltip="既存の資料を読み込ませて、要件の抽出に使います。"
                    >
                        資料取込
                    </Button>
                    <Button
                        onClick={() => setView('progress-report')}
                        tooltip="要件定義の進み具合を報告用にまとめます。"
                    >
                        進捗レポート
                    </Button>
                    <Button
                        onClick={() => setView('members')}
                        tooltip="このプロジェクトに参加している人と権限を管理します。"
                    >
                        メンバー
                    </Button>
                    <Button
                        onClick={() => setView('sync')}
                        tooltip="ほかのメンバーの変更を取り込み、自分の変更を反映します。"
                    >
                        同期
                    </Button>
                    <Button
                        onClick={() => setView('usage')}
                        tooltip="AI の利用量と上限の設定を確認します。"
                    >
                        AI 利用量
                    </Button>
                    <Button
                        onClick={() => setView('token-usage')}
                        tooltip="トークン消費の実績を期間ごとに確認します。"
                    >
                        消費実績
                    </Button>
                    <Button
                        onClick={() => {
                            void ChangeSummary()
                                .then(setChangeSummary)
                                .catch(() => undefined)
                        }}
                        tooltip="前回の取り込みで何が変わったかを確認します。"
                    >
                        取り込んだ変更
                    </Button>
                    <Button
                        onClick={() => setView('documents')}
                        tooltip="要件定義書を生成し、内容を確認して版を確定します。"
                    >
                        要件定義書
                    </Button>
                    {opened?.phase === 'basic-design' ? (
                        <Button
                            onClick={() => setView('design-documents')}
                            tooltip="基本設計書を生成し、内容を確認して版を確定します。"
                        >
                            基本設計書
                        </Button>
                    ) : null}
                    <Button
                        onClick={() => {
                            if (sessionID) {
                                void SuspendDialogue(sessionID)
                            }
                            onBack()
                        }}
                        tooltip="対話を中断して、プロジェクトの一覧へ戻ります。"
                    >
                        プロジェクト一覧へ戻る
                    </Button>
                </>
            }
            completeness={sections}
            counts={counts}
            guide={guideStrip}
            effort="標準"
            model=""
            usage={{consumed: usage?.consumedTokens ?? 0, limit: usage?.limitTokens ?? null}}
        >
            <IDRangeNotice warnings={idRanges} onOpenSync={() => setView('sync')} />
            {usageDismissed || !(AI_VIEWS as readonly string[]).includes(view) ? null : (
                <UsageNotice
                    status={usage}
                    onOpenDashboard={() => setView('usage')}
                    onDismiss={() => setUsageDismissed(true)}
                />
            )}
            {view === 'records' ? <DecisionsAndIssues onBack={() => setView('dialogue')} /> : null}
            {view === 'requirements' ? <RequirementList onBack={() => setView('dialogue')} /> : null}
            {view === 'history' ? <DialogueHistory onBack={() => setView('dialogue')} /> : null}
            {view === 'questionnaires' ? <QuestionnaireManager onBack={() => setView('dialogue')} /> : null}
            {view === 'import-answers' ? (
                <ImportAnswers
                    initialFile={importFile}
                    onBack={() => {
                        setView('dialogue')
                        onImportFileHandled?.()
                    }}
                />
            ) : null}
            {view === 'imports' ? <Imports onBack={() => setView('dialogue')} /> : null}
            {view === 'progress-report' ? <ProgressReport onBack={() => setView('dialogue')} /> : null}
            {view === 'members' ? (
                <Members onBack={() => setView('dialogue')} onOpenSyncRemote={() => setView('sync-remote')} />
            ) : null}
            {view === 'sync' ? (
                <Sync
                    onBack={() => setView('dialogue')}
                    onIncorporated={() => {
                        // 取り込みで入った変更は変更要約で提示する。
                        void ChangeSummary()
                            .then(setChangeSummary)
                            .catch(() => undefined)
                        // 番号帯の残量は同期のあとに更新する（番号帯の確保は反映の直前に行う）。
                        reloadIdRanges()
                    }}
                    onOpenCredentials={() => setView('sync-credentials')}
                />
            ) : null}
            {view === 'sync-remote' ? (
                <SyncRemote
                    onBack={() => setView('members')}
                    onOpenCredentials={() => setView('sync-credentials')}
                />
            ) : null}
            {view === 'sync-credentials' ? (
                <SyncCredentials
                    projectId={opened?.projectId ?? ''}
                    projectName={opened?.targetName ?? ''}
                    onBack={() => setView('sync')}
                    backLabel="同期へ戻る"
                />
            ) : null}
            {view === 'usage' ? (
                <UsageDashboard
                    embedded
                    onBack={() => setView('dialogue')}
                    backLabel="対話へ戻る"
                    onOpenUsageLimit={() => setView('usage-limit')}
                />
            ) : null}
            {view === 'token-usage' ? (
                <TokenUsage
                    embedded
                    onBack={() => setView('dialogue')}
                    backLabel="対話へ戻る"
                    onOpenDashboard={() => setView('usage')}
                />
            ) : null}
            {view === 'usage-limit' ? (
                <UsageLimit onBack={() => setView('usage')} onChanged={() => void reloadUsage()} />
            ) : null}
            {view === 'documents' ? (
                <Documents
                    kind="requirements"
                    onBack={() => setView('dialogue')}
                    onExport={() => setView('export')}
                />
            ) : null}
            {view === 'design-documents' ? (
                <Documents
                    kind="basic-design"
                    onBack={() => setView('dialogue')}
                    onExport={() => setView('export')}
                />
            ) : null}
            {view === 'export' ? <Export onBack={() => setView('documents')} /> : null}
            {changeSummary ? (
                <ChangeSummaryPanel
                    summary={changeSummary}
                    onOpenRecord={(target) => {
                        // 該当レコードへ遷移する（変更影響表示と同じ入口 = 決定・未決／要件項目の一覧）。
                        closeChangeSummary()
                        setView(target.startsWith('FR-') || target.startsWith('NFR-') ? 'requirements' : 'records')
                    }}
                    onClose={closeChangeSummary}
                />
            ) : null}
            {/* 対話の画面は独立したコンポーネントになっていないため、包みから publish する
                （上部の工程ガイドに出す）。 */}
            {view === 'dialogue' ? (
                <ScreenHint
                    hint={
                        // セッションは画面を開いた時点で自動的に始まる。まだ無い間は
                        // 押すものが画面に無いため、この画面での案内は出さない（工程ガイドが出る）。
                        !sessionID
                            ? ''
                            : busy || waiting || streaming
                              ? '応答を待っています。止めたいときは「中断」を押します。'
                              : candidates
                                ? '右の候補ごとに「承認」か「破棄」を選び、「選んだ内容で反映」を押します。'
                                : idleAwaitingQuestion
                                  ? '「次の質問」を押すと、次に確認する論点の質問を作ります。'
                                  : '「回答」に答えを書いて「送信」を押します。'
                    }
                />
            ) : null}
            <div className="rw-dialogue" hidden={view !== 'dialogue'}>
                {/* 抽出中は沈み込ませる（**マスクしない**。回答は読めるまま） */}
                <section className="rw-dialogue__main" aria-label="対話" data-phase={phase}>
                    <p className="rw-dialogue__state" role="status">
                        {idleAwaitingQuestion ? '待機中' : stateLabel(state)}
                    </p>
                    {message ? (
                        <Banner
                            tone={tone}
                            title={message}
                            onDismiss={() => setMessage('')}
                            onDefer={() => setMessage('')}
                        />
                    ) : null}

                    <div className="rw-dialogue__stream" aria-label="対話履歴" ref={streamElRef}>
                        {utterances.map((u) => (
                            <Utterance
                                key={u.id}
                                speaker={u.speaker === 'agent' ? 'ai' : 'human'}
                                name={u.speaker === 'agent' ? 'エージェント' : '担当者'}
                                timestamp={u.at}
                                body={u.body}
                                interrupted={u.status === 'interrupted'}
                            />
                        ))}
                        {streaming ? (
                            <Utterance speaker="ai" name="エージェント" timestamp="" body={streaming} />
                        ) : null}
                        {waiting && !streaming ? (
                            <p className="rw-dialogue__waiting" role="status">
                                応答を待っています…
                            </p>
                        ) : null}
                    </div>

                    {/* 進行中であることの細い手掛かり。長さは示さない（所要が読めないため） */}
                    {sendingOrExtracting ? <div className="rw-dialogue__progress" aria-hidden="true" /> : null}

                    <div className="rw-dialogue__composer">
                        <label className="rw-dialogue__label" htmlFor="rw-answer">
                            回答
                        </label>
                        <textarea
                            id="rw-answer"
                            className="rw-dialogue__input"
                            rows={4}
                            value={answer}
                            onChange={(e) => setAnswer(e.target.value)}
                            disabled={sendingOrExtracting}
                            placeholder={sendingOrExtracting ? '抽出が終わるまでお待ちください' : undefined}
                        />
                        <div className="rw-dialogue__actions">
                            {showInterrupt ? (
                                <Button variant="secondary" onClick={() => void interrupt()}>
                                    中断
                                </Button>
                            ) : (
                                <Button
                                    variant="primary"
                                    onClick={() => void send()}
                                    disabledReason={canSend ? undefined : '回答を入力してください。'}
                                >
                                    送信
                                </Button>
                            )}
                            <Button
                                onClick={() => void askNext()}
                                disabledReason={
                                    sendingOrExtracting
                                        ? '抽出が終わるまでお待ちください。'
                                        : busy
                                          ? '応答を待っています。'
                                          : undefined
                                }
                            >
                                次の質問
                            </Button>
                        </div>
                    </div>
                </section>

                <aside className="rw-dialogue__side" aria-label="候補と完成度" ref={sideElRef}>
                    {conflicts.length > 0 ? (
                        <MergePanel
                            conflicts={conflicts}
                            notice={conflictNotice}
                            proposals={mergeProposals(candidates, edits)}
                            busy={busy}
                            onApprove={(merges) => void approve(merges)}
                            onDefer={() => {
                                setConflicts([])
                                setTone('info')
                                setMessage(
                                    '反映は保留しました。候補は残っています。未決事項として起票することもできます。',
                                )
                            }}
                        />
                    ) : null}
                    <CandidatePanel
                        phase={phase}
                        candidates={candidates}
                        fallbackText={fallbackText}
                        marks={decisionMarks}
                        edits={edits}
                        owners={owners}
                        onMark={(key, mark) => setDecisionMarks((prev) => ({...prev, [key]: mark}))}
                        onEdit={(key, value) => setEdits((prev) => ({...prev, [key]: value}))}
                        onOwner={(key, value) => setOwners((prev) => ({...prev, [key]: value}))}
                        onApprove={() => void approve()}
                        busy={busy}
                        error={approveError}
                    />
                    <CompletenessPanel view={completeness} />
                </aside>
            </div>
            {flight ? (
                <FlyingCard from={flight.from} to={flight.to} onDone={() => setFlight(null)} />
            ) : null}
            {toast ? (
                <Toast tone={toast.tone} message={toast.message} onDismiss={() => setToast(null)} />
            ) : null}
        </AppShell>
    )
}

/**
 * 抽出候補パネル。3 区分で表示し、候補ごとに承認 / 編集して承認 / 破棄を選ぶ。
 *
 * 抽出中は**結果の型を先に見せる**（何が出てくるかを待つ間に分かるように）。
 */
function CandidatePanel({
    phase,
    candidates,
    fallbackText,
    marks,
    edits,
    owners,
    onMark,
    onEdit,
    onOwner,
    onApprove,
    busy,
    error,
}: {
    candidates: dialogue.Extraction | null
    fallbackText: string
    marks: Record<string, CandidateDecision>
    edits: Record<string, string>
    owners: Record<string, {owner: string; due: string}>
    onMark: (key: string, mark: CandidateDecision) => void
    onEdit: (key: string, value: string) => void
    onOwner: (key: string, value: {owner: string; due: string}) => void
    onApprove: () => void
    busy: boolean
    phase: ExtractionPhase
    /** 反映に失敗した理由（空なら出さない） */
    error: string
}) {
    /*
     * 抽出中は空のカードを 2 枚出す。件数は事前に分からないため**枚数を固定**する
     * （多すぎると到着時の差し替えで見た目が崩れる）。
     * 見出しに「抽出中」を添え、状況は読み上げにも 1 か所で伝える。
     */
    if (phase === 'extracting') {
        return (
            <section className="rw-candidates" aria-label="抽出候補">
                <h2 className="rw-candidates__title">抽出候補（抽出中）</h2>
                <p className="rw-candidates__extracting" role="status">
                    送った回答から候補を作っています。ここに並んだら、1 件ずつ承認または破棄します。
                </p>
                <SkeletonCard />
                <SkeletonCard />
            </section>
        )
    }
    if (fallbackText) {
        return (
            <section className="rw-candidates" aria-label="抽出候補">
                <h2 className="rw-candidates__title">分析結果</h2>
                <p className="rw-candidates__notice">
                    形式どおりの分析結果が得られませんでした。下の応答内容を読み、必要な内容は
                    「決定・未決」から手で起票してください。もう一度試すこともできます。
                </p>
                <pre className="rw-candidates__raw">{fallbackText}</pre>
            </section>
        )
    }
    if (!candidates) {
        return (
            <section className="rw-candidates" aria-label="抽出候補">
                <h2 className="rw-candidates__title">抽出候補</h2>
                {/* 操作名ではなく「実際に起きること」を主語にする。 */}
                <EmptyState
                    message="まだ候補はありません"
                    next="回答を送信すると、ここに要件候補が並びます。1 件ずつ承認または破棄すると、左の完成度に反映されます。"
                />
            </section>
        )
    }

    /*
     * 抽出は動いたが、今回の回答からは 1 件も出なかった場合。
     *
     * 3 区分すべてに「なし」を並べると、動かなかったのか出なかったのかが分からない。
     * **出なかったことを 1 文で言い切り**、次に取る手を添える。
     */
    if (
        candidates.decisions.length === 0 &&
        candidates.open_issues.length === 0 &&
        candidates.requirement_updates.length === 0
    ) {
        return (
            <section className="rw-candidates" aria-label="抽出候補">
                <h2 className="rw-candidates__title">抽出候補</h2>
                <EmptyState
                    message="今回の回答からは候補が出ませんでした"
                    next="「次の質問」で先へ進むか、いまの回答に具体的な条件・数値・例を足して送り直します。"
                />
            </section>
        )
    }

    /*
     * 選んだ件数。1 件も選ばずに押すと、記録は変わらず候補も残るため
     * 「押したのに何も起きない」ように見える。押す前に選ばせ、何が反映されるかも言う。
     */
    const marked = Object.values(marks).filter((m) => m === 'approved' || m === 'discarded')
    const chosen = marked.length
    const approved = marked.filter((m) => m === 'approved').length

    /*
     * 根拠を特定できなかった候補。示し方は資料取込・回答取込と共通の部品に置く
     * （ui/CandidateEvidence）。決定事項・未決事項は根拠が無いと記録できないため
     * 承認を選べなくし、要件項目は記録できる旨を添える。
     */
    const evidenceNote = (c: {evidence_refs?: string[]; missing_evidence?: boolean}, recordable: boolean) => (
        <CandidateEvidence candidate={c} source="発言" recordable={recordable}>
            <p className="rw-candidates__evidence rw-mono">根拠: {c.evidence_refs?.join(', ')}</p>
        </CandidateEvidence>
    )
    const blocked = (c: {evidence_refs?: string[]; missing_evidence?: boolean}) =>
        lacksEvidence(c) ? noEvidenceReason('発言') : undefined
    // 押す前に分かる入力不足。押してから失敗させない。
    const inputReason = inputProblem(candidates, marks, owners)

    return (
        <section className="rw-candidates" aria-label="抽出候補">
            <h2 className="rw-candidates__title">抽出候補</h2>

            <h3 className="rw-candidates__group">決定事項候補</h3>
            {candidates.decisions.length === 0 ? <p className="rw-candidates__none">なし</p> : null}
            {candidates.decisions.map((c, i) => {
                const key = `decision-${i}`
                return (
                    <article key={key} className="rw-candidates__item">
                        <p className="rw-candidates__meta rw-mono">{c.topic_key}</p>
                        <CandidateBody
                            label={`決定事項候補 ${i + 1}`}
                            value={edits[key] ?? c.body}
                            onChange={(v) => onEdit(key, v)}
                        />
                        {evidenceNote(c, false)}
                        <MarkButtons
                            keyName={key}
                            mark={marks[key] ?? 'pending'}
                            onMark={onMark}
                            approveDisabledReason={blocked(c)}
                        />
                    </article>
                )
            })}

            <h3 className="rw-candidates__group">未決事項候補</h3>
            {candidates.open_issues.length === 0 ? <p className="rw-candidates__none">なし</p> : null}
            {candidates.open_issues.map((c, i) => {
                const key = `issue-${i}`
                const value = owners[key] ?? {owner: c.owner ?? '', due: c.due ?? ''}
                return (
                    <article key={key} className="rw-candidates__item">
                        <p className="rw-candidates__body-text">{c.topic}</p>
                        {/* 未決事項の承認には「誰が・いつまでに」が必須 */}
                        <label className="rw-candidates__field">
                            決める人
                            <input
                                aria-label={`未決事項候補 ${i + 1} の決める人`}
                                value={value.owner}
                                onChange={(e) => onOwner(key, {...value, owner: e.target.value})}
                            />
                        </label>
                        <label className="rw-candidates__field">
                            期限
                            <input
                                aria-label={`未決事項候補 ${i + 1} の期限`}
                                placeholder="YYYY-MM-DD"
                                value={value.due}
                                onChange={(e) => onOwner(key, {...value, due: e.target.value})}
                            />
                        </label>
                        {evidenceNote(c, false)}
                        <MarkButtons
                            keyName={key}
                            mark={marks[key] ?? 'pending'}
                            onMark={onMark}
                            approveDisabledReason={blocked(c)}
                        />
                    </article>
                )
            })}

            <h3 className="rw-candidates__group">要件項目への反映案</h3>
            {candidates.requirement_updates.length === 0 ? (
                <p className="rw-candidates__none">なし</p>
            ) : null}
            {candidates.requirement_updates.map((c, i) => {
                const key = `requirement-${i}`
                return (
                    <article key={key} className="rw-candidates__item">
                        <p className="rw-candidates__meta rw-mono">
                            {c.operation === 'update' ? c.target_id : `新規 ${plannedRequirementID(c)}`} / {c.chapter}
                        </p>
                        <p className="rw-candidates__body-text">{c.title}</p>
                        <CandidateBody
                            label={`要件項目の反映案 ${i + 1}`}
                            value={edits[key] ?? c.body_after}
                            onChange={(v) => onEdit(key, v)}
                        />
                        {/* ID は ReqWeave が付ける。利用者には入力させない */}
                        {c.operation === 'create' ? (
                            <p className="rw-candidates__hint">ID は記録するときに ReqWeave が付けます（nnn は連番）。</p>
                        ) : null}
                        {evidenceNote(c, true)}
                        <MarkButtons keyName={key} mark={marks[key] ?? 'pending'} onMark={onMark} />
                    </article>
                )
            })}

            {/* 反映に失敗した理由は押したボタンのすぐ上に出す */}
            {error ? (
                <p className="rw-candidates__error" role="alert">
                    反映できませんでした。何も記録していません。
                    {'\n'}
                    {error}
                </p>
            ) : null}
            {/* 押せない理由はホバーを待たずボタンのそばに出す（ツールチップだけに隠さない） */}
            {!busy && chosen > 0 && inputReason ? (
                <p className="rw-candidates__reason" role="status">
                    {inputReason}
                </p>
            ) : null}
            <div className="rw-candidates__actions">
                <Button
                    variant="primary"
                    onClick={onApprove}
                    disabledReason={
                        busy
                            ? '処理中です。'
                            : chosen === 0
                              ? '候補ごとに「承認」か「破棄」を選んでください。'
                              : inputReason
                    }
                >
                    選んだ内容で反映
                </Button>
                {/* 何が反映されるかを押す前に言う（押しても変化が無い、を作らない） */}
                {chosen > 0 ? (
                    <span className="rw-candidates__chosen">
                        承認 {approved} 件 ／ 破棄 {chosen - approved} 件
                    </span>
                ) : null}
            </div>
        </section>
    )
}

/**
 * 候補の本文（対話画面の候補パネル）。**既定は読むだけ**。
 *
 * 入力欄のまま並べると、承認可否を判断するときに読みづらい（枠・スクロール・折り返しの制限）。
 * 直したいときだけ「編集」で入力欄へ切り替える。編集した内容は承認時にそのまま使う。
 */
function CandidateBody({
    label,
    value,
    onChange,
}: {
    label: string
    value: string
    onChange: (value: string) => void
}) {
    const [editing, setEditing] = useState(false)
    return (
        <>
            {editing ? (
                <textarea
                    className="rw-candidates__body"
                    aria-label={label}
                    value={value}
                    onChange={(e) => onChange(e.target.value)}
                />
            ) : (
                <p className="rw-candidates__body-read" aria-label={label}>
                    {value}
                </p>
            )}
            <Button variant="secondary" onClick={() => setEditing((on) => !on)}>
                {editing ? '編集をやめる' : '編集'}
            </Button>
        </>
    )
}

function MarkButtons({
    keyName,
    mark,
    onMark,
    approveDisabledReason,
}: {
    keyName: string
    mark: CandidateDecision
    onMark: (key: string, mark: CandidateDecision) => void
    /** 承認を選べない理由（記録できない候補）。無ければ選べる */
    approveDisabledReason?: string
}) {
    return (
        <div className="rw-candidates__marks">
            <Button
                variant={mark === 'approved' ? 'primary' : 'secondary'}
                onClick={() => onMark(keyName, 'approved')}
                disabledReason={approveDisabledReason}
            >
                承認
            </Button>
            <Button variant="secondary" onClick={() => onMark(keyName, 'discarded')}>
                破棄
            </Button>
            <span className="rw-candidates__mark-state">{markLabel(mark)}</span>
        </div>
    )
}

function markLabel(mark: CandidateDecision): string {
    switch (mark) {
        case 'approved':
            return '承認する'
        case 'discarded':
            return '破棄する'
        default:
            return '未選択（反映されません）'
    }
}

/** 完成度表示。3 状態は色分けと文字ラベルを併用する。 */
function CompletenessPanel({view}: {view: binding.DialogueCompletenessView | null}) {
    if (!view) {
        return null
    }
    return (
        <section className="rw-completeness" aria-label="完成度">
            <h2 className="rw-completeness__title">完成度</h2>
            <ul className="rw-completeness__list">
                {view.chapters.map((c) => (
                    <li key={c.chapterId} className="rw-completeness__row" data-state={c.state}>
                        <span className="rw-completeness__name">{c.name}</span>
                        <span className="rw-completeness__percent rw-mono">{c.percent}%</span>
                        <span className="rw-completeness__state">{chapterStateLabel(c.state)}</span>
                        {c.openIssues > 0 ? <Chip tone="warn">未決 {c.openIssues}</Chip> : null}
                    </li>
                ))}
            </ul>
            <p className="rw-completeness__confirm">
                {view.confirmation.confirmable ? '確定できます。' : reasonOf(view.confirmation)}
            </p>
        </section>
    )
}

function reasonOf(confirmation: dialogue.Confirmation): string {
    const parts: string[] = []
    if (confirmation.draftRequirements?.length) {
        parts.push(`未合意の要件項目が ${confirmation.draftRequirements.length} 件あります`)
    }
    if (confirmation.blockingIssues?.length) {
        parts.push(`要件項目をブロックする未決事項が ${confirmation.blockingIssues.length} 件あります`)
    }
    if (parts.length === 0) {
        return '確定前チェックの対象がまだありません。'
    }
    return `${parts.join('。')}。解消してから確定してください。`
}

/**
 * エラーイベントを「原因＋次の行動」の 1 文にする。
 *
 * **文言の正本はバックエンドのエラーカタログ**（分類 × プロバイダ固有コード）であり、
 * 届いた `userMessage` をそのまま表示する。画面側にコード別の表を持たない（二重管理の禁止）。
 * 古い版のイベント（`userMessage` を持たない）に備えて分類ごとの既定文だけを残す。
 */
function errorMessage(ev: DialogueEvent): string {
    if (ev.userMessage) {
        return ev.userMessage
    }
    switch (ev.errorClass) {
        case 'transient':
            return '接続が不安定なため応答を受け取れませんでした。通信を確認して、もう一度お試しください。'
        case 'config':
            return 'AI プロバイダの設定に問題があります。設定画面でキーとモデルを確認してください。'
        case 'permanent':
            return '送信内容を処理できませんでした。入力を短くするか、対象を分けてお試しください。'
        default:
            return ev.message || '応答を受け取れませんでした。もう一度お試しください。'
    }
}

/** 競合パネルへ渡す「自分の反映案」（対象 ID → 反映しようとした本文）。 */
export function mergeProposals(
    candidates: dialogue.Extraction | null,
    edits: Record<string, string>,
): Record<string, string> {
    const out: Record<string, string> = {}
    ;(candidates?.requirement_updates ?? []).forEach((c, i) => {
        if (c.operation === 'update' && c.target_id) {
            out[c.target_id] = edits[`requirement-${i}`] ?? c.body_after
        }
    })
    ;(candidates?.term_candidates ?? []).forEach((c) => {
        out[`term:${c.term}`] = c.definition
    })
    return out
}

function approvalMessage(result: binding.ApprovalApplied | undefined): string {
    const parts: string[] = []
    if (!result) {
        return '候補はすべて破棄しました。記録は変わっていません。'
    }
    if (result.decisionIds?.length) {
        parts.push(`決定事項 ${result.decisionIds.length} 件`)
    }
    if (result.openIssueIds?.length) {
        parts.push(`未決事項 ${result.openIssueIds.length} 件`)
    }
    if (result.requirementIds?.length) {
        parts.push(`要件項目 ${result.requirementIds.length} 件`)
    }
    if (parts.length === 0) {
        return '候補はすべて破棄しました。記録は変わっていません。'
    }
    let text = `${parts.join(' / ')}を記録しました。`
    if (result.unblockedRequirementIds?.length) {
        text += `ブロックが外れた要件項目: ${result.unblockedRequirementIds.join(', ')}`
    }
    return text
}

function phaseLabel(phase: string | undefined): string {
    return phase === 'basic-design' ? '基本設計' : '要件定義'
}

/**
 * 押す前に分かる入力不足。承認した候補だけを見る。無ければ undefined。
 *
 * 条件はバックエンドの書き込み前検証（dialogue.ValidateApproval）と同じ。
 * 最後の番人はバックエンドであり、ここは「押してから失敗させない」ための先回り。
 */
export function inputProblem(
    candidates: dialogue.Extraction,
    marks: Record<string, CandidateDecision>,
    owners: Record<string, {owner: string; due: string}>,
): string | undefined {
    for (const [i, c] of candidates.open_issues.entries()) {
        const key = `issue-${i}`
        if (marks[key] !== 'approved') {
            continue
        }
        const value = owners[key] ?? {owner: c.owner ?? '', due: c.due ?? ''}
        if (!value.owner.trim()) {
            return `未決事項候補「${shortLabel(c.topic)}」の「決める人」を入れてください。`
        }
        if (value.due && !/^\d{4}-\d{2}-\d{2}$/.test(value.due)) {
            return `未決事項候補「${shortLabel(c.topic)}」の期限は 2026-09-30 のように年-月-日で入れてください。`
        }
    }
    // 要件項目の ID グループ・種別は利用者の入力ではない（ReqWeave が決める）。
    return undefined
}

/**
 * 新規の要件項目に付く ID の形（例: FR-INV-nnn）。連番は記録するときに採るため nnn で示す。
 *
 * グループ・種別はバックエンドが抽出時に確定して候補へ入れてある（ResolveRequirementIDs）。
 * 画面は表示するだけで、決め直さない（二重に持つと食い違う）。
 */
function plannedRequirementID(c: {kind?: string; id_group?: string}): string {
    const prefix = c.kind === 'non-functional' ? 'NFR' : 'FR'
    return `${prefix}-${c.id_group || '（自動）'}-nnn`
}

/** 候補を文中で指す短い表記（1 行目・24 文字まで。バックエンドの candidateLabel と同じ）。 */
function shortLabel(text: string): string {
    const line = [...((text ?? '').split('\n').find((l) => l.trim()) ?? '').trim()]
    return line.length > 24 ? `${line.slice(0, 24).join('')}…` : line.join('')
}

/** 承認された候補だけを反映要求へ組み立てる（未選択・破棄は渡さない）。 */
function buildApproval(
    candidates: dialogue.Extraction,
    marks: Record<string, CandidateDecision>,
    edits: Record<string, string>,
    owners: Record<string, {owner: string; due: string}>,
): dialogue.ApprovalRequest {
    const decisions: dialogue.DecisionApproval[] = []
    const openIssues: dialogue.OpenIssueCandidate[] = []
    const requirementUpdates: dialogue.RequirementApproval[] = []
    candidates.decisions.forEach((c, i) => {
        const key = `decision-${i}`
        if (marks[key] !== 'approved') {
            return
        }
        decisions.push(
            dialogue.DecisionApproval.createFrom({
                candidate: {...c, body: edits[key] ?? c.body},
                resolvesIssueIds: [],
            }),
        )
    })
    candidates.open_issues.forEach((c, i) => {
        const key = `issue-${i}`
        if (marks[key] !== 'approved') {
            return
        }
        const value = owners[key] ?? {owner: c.owner ?? '', due: c.due ?? ''}
        openIssues.push(
            dialogue.OpenIssueCandidate.createFrom({...c, owner: value.owner, due: value.due}),
        )
    })
    candidates.requirement_updates.forEach((c, i) => {
        const key = `requirement-${i}`
        if (marks[key] !== 'approved') {
            return
        }
        requirementUpdates.push(
            dialogue.RequirementApproval.createFrom({
                // ID グループ・種別は候補に確定済み（渡さなければバックエンドが決める）。
                candidate: {...c, body_after: edits[key] ?? c.body_after},
            }),
        )
    })
    return dialogue.ApprovalRequest.createFrom({
        decisions,
        openIssues,
        requirementUpdates,
        termCandidates: [],
    })
}
