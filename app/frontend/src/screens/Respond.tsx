import {useCallback, useEffect, useRef, useState} from 'react'
import {
    ChooseReturnDestination,
    FinalizeAnswers,
    OpenQuestionnaireFile,
    RespondAIDialogue,
    SaveAnswer,
    SignOutRespondAI,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Button, Wizard, errorText, useWizardPane} from '../ui'
import {RespondAIPane} from './RespondAIPane'
import './Respond.css'

/**
 * 回答モード。
 *
 * 開始（パスコード入力）→ 質問回答（1 件ずつ）→ 回答一覧確認 → 返送ファイル出力。
 * AI を呼ばず、初期設定・シークレットキーなしで完結する（回答者は AI の設定をしていない前提）。
 * 担当者モードの画面へ遷移する導線を一切持たない（回答者に担当者の権限は無い）。
 * パスコードは入力欄の外へ出さず、保持もしない。
 *
 * **AI 対話ペイン（任意の機能）**: 質問回答の画面の下部に上下分割で置く。
 * 既定は閉で、回答欄の下の従属操作で開閉する。**画面も遷移も増やさない**。開閉状態は
 * アプリの実行中だけ保持し、**保存しない**（回答作業領域に平文の項目を増やさない）。
 * **ペインを一度も開かずに全質問へ回答して確定できる**（構造化回答 UI だけで完了できる導線を保つ）。
 */

/** 自由記述欄を持つ回答形式（AI の内容を移せる先）。 */
function acceptsFreeText(format: AnswerFormat): boolean {
    return format === 'free' || format === 'choice_with_free'
}

/**
 * 回答欄の下に置く「AI と相談する」の従属操作（AI 対話ペインの入口）。
 *
 * **Wizard の子として描く**（開けるかどうかは作業領域の高さで決まり、Wizard が測っている）。
 * 高さが足りないときは無効化し、理由と次の行動を示す（押し下げ・はみ出しで代替しない）。
 */
function AIPaneToggle({open, onToggle}: {open: boolean; onToggle: () => void}) {
    const {canOpenPane, paneUnavailableReason} = useWizardPane()
    return (
        <p className="rw-respond__ai-toggle">
            <Button
                onClick={onToggle}
                disabledReason={open || canOpenPane ? undefined : paneUnavailableReason}
            >
                {open ? '相談を閉じる' : 'AI と相談する'}
            </Button>
        </p>
    )
}

/** 回答形式のラベル。列挙の網羅を型で強制する（生のコード値を画面に出さないため）。 */
const ANSWER_FORMATS = ['choice', 'multi_choice', 'free', 'choice_with_free'] as const
type AnswerFormat = (typeof ANSWER_FORMATS)[number]

const ANSWER_FORMAT_HINTS = {
    choice: '当てはまるものを 1 つ選んでください。',
    multi_choice: '当てはまるものをすべて選んでください。',
    free: '自由にご記入ください。',
    choice_with_free: '当てはまるものを 1 つ選び、必要なら補足をご記入ください。',
} satisfies Record<AnswerFormat, string>

function isAnswerFormat(v: string): v is AnswerFormat {
    return (ANSWER_FORMATS as readonly string[]).includes(v)
}

/** 回答の種別（保存形式の kind）。 */
const KIND_ANSWERED = 'answered'
const KIND_UNKNOWN = 'unknown'

/** 入力中の 1 問ぶんの回答。 */
type Draft = {
    kind: string
    selected: string[]
    freeText: string
    body: string
}

const EMPTY_DRAFT: Draft = {kind: KIND_ANSWERED, selected: [], freeText: '', body: ''}

function toDraft(a: binding.RespondAnswerView | undefined): Draft {
    if (!a) {
        return EMPTY_DRAFT
    }
    return {
        kind: a.kind,
        selected: a.selected ?? [],
        freeText: a.freeText ?? '',
        body: a.body ?? '',
    }
}

/** 回答済みとみなせるか（「不明」は理由が未記入でも回答済み）。 */
function isAnswered(format: AnswerFormat, draft: Draft): boolean {
    if (draft.kind === KIND_UNKNOWN) {
        return true
    }
    switch (format) {
        case 'choice':
        case 'multi_choice':
            return draft.selected.length > 0
        case 'choice_with_free':
            return draft.selected.length > 0 || draft.freeText.trim() !== ''
        case 'free':
            return draft.freeText.trim() !== ''
    }
}

export function Respond({filePath, fileName}: {filePath: string; fileName: string}) {
    const [opened, setOpened] = useState<binding.RespondOpenResult | null>(null)
    const [passcode, setPasscode] = useState('')
    const [error, setError] = useState('')
    const [busy, setBusy] = useState(false)
    const [step, setStep] = useState<'start' | 'answer' | 'review' | 'export'>('start')
    const [index, setIndex] = useState(0)
    const [drafts, setDrafts] = useState<Record<string, Draft>>({})
    const [exported, setExported] = useState<binding.RespondExportView | null>(null)
    // AI 対話ペインの開閉（既定は閉・質問を進めても開いたまま・保存しない）。
    const [aiOpen, setAIOpen] = useState(false)
    const [aiView, setAIView] = useState<binding.RespondAIDialogueView | null>(null)
    const [signedOut, setSignedOut] = useState('')
    // ペインを閉じたときにフォーカスを戻す先。
    const answerRef = useRef<HTMLFieldSetElement | null>(null)

    /** パスコードを検証して質問票を開く。一致するまで内容は届かない。 */
    const open = async () => {
        setBusy(true)
        setError('')
        try {
            const result = await OpenQuestionnaireFile(filePath, passcode)
            const initial: Record<string, Draft> = {}
            for (const a of result.answers ?? []) {
                initial[a.questionId] = toDraft(a)
            }
            setDrafts(initial)
            setOpened(result)
            // 入力欄からパスコードを消す（画面に残さない）。
            setPasscode('')
            setStep(result.status === 'answered' ? 'export' : 'answer')
        } catch (err: unknown) {
            setError(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    const questions = opened?.questions ?? []
    const current = questions[index]
    const draft = current ? (drafts[current.id] ?? EMPTY_DRAFT) : EMPTY_DRAFT
    const format: AnswerFormat = current && isAnswerFormat(current.answerFormat) ? current.answerFormat : 'free'

    /** 入力のたびに保存する（中断しても失われない）。 */
    const save = useCallback(
        async (questionID: string, next: Draft) => {
            setDrafts((prev) => ({...prev, [questionID]: next}))
            try {
                await SaveAnswer({
                    questionId: questionID,
                    kind: next.kind,
                    selected: next.selected,
                    freeText: next.freeText,
                    body: next.body,
                } as binding.AnswerInput)
                setError('')
            } catch (err: unknown) {
                setError(`${errorText(err)} 入力内容が保存できていません。システム担当者へ連絡してください。`)
            }
        },
        [],
    )

    const update = (patch: Partial<Draft>) => {
        if (!current) {
            return
        }
        void save(current.id, {...draft, ...patch})
    }

    const unanswered = questions.filter((q) => {
        const f = isAnswerFormat(q.answerFormat) ? q.answerFormat : 'free'
        return !isAnswered(f, drafts[q.id] ?? EMPTY_DRAFT)
    })

    /*
     * 返送の画面でだけ AI の状態を読む（サインアウトの導線を出すかの判定）。
     * **通信は起こさない**（保持している状態を返すだけのバインディング）。
     */
    useEffect(() => {
        if (step !== 'export') {
            return
        }
        void Promise.resolve()
            .then(() => RespondAIDialogue())
            .then((next) => setAIView(next))
            .catch(() => undefined) // 返送の作業を妨げない（導線が出ないだけ）
    }, [step])

    /** サインアウト（確認操作を経て実行する）。 */
    const signOut = async () => {
        setBusy(true)
        setError('')
        try {
            setAIView(await SignOutRespondAI())
            setSignedOut('サインアウトしました。')
        } catch (err: unknown) {
            setError(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    const exportReturn = async () => {
        setBusy(true)
        setError('')
        try {
            const dst = await ChooseReturnDestination()
            if (!dst) {
                return
            }
            setExported(await FinalizeAnswers(dst))
            setStep('export')
        } catch (err: unknown) {
            setError(`${errorText(err)} 解決しないときはシステム担当者へ連絡してください。`)
        } finally {
            setBusy(false)
        }
    }

    // ---- 開始（パスコード入力）--------------------------------
    if (!opened) {
        return (
            <div className="rw-respond">
                <h1 className="rw-respond__title">質問票をひらく</h1>
                <p className="rw-respond__file rw-mono">{fileName}</p>
                <p className="rw-respond__lead">
                    担当者から別の方法（電話・チャットなど）で届いたパスコードを入力してください。
                </p>
                <label className="rw-respond__field">
                    パスコード
                    <input
                        type="password"
                        value={passcode}
                        onChange={(e) => setPasscode(e.target.value)}
                        autoComplete="off"
                    />
                </label>
                {error ? (
                    <p className="rw-respond__error" role="alert">
                        {error}
                    </p>
                ) : null}
                <Button
                    variant="primary"
                    onClick={() => void open()}
                    disabledReason={
                        passcode.trim() === '' ? 'パスコードを入力してください。' : busy ? '確認しています。' : undefined
                    }
                >
                    質問をひらく
                </Button>
            </div>
        )
    }

    // ---- 返送ファイル出力 ------------------------------------
    if (step === 'export') {
        return (
            <div className="rw-respond">
                <h1 className="rw-respond__title">回答を返送する</h1>
                {exported ? (
                    <>
                        <p className="rw-respond__lead">{exported.notice}</p>
                        <p className="rw-respond__file rw-mono">{exported.path}</p>
                        <Button onClick={() => void exportReturn()} disabledReason={busy ? '処理中です。' : undefined}>
                            もう一度保存する
                        </Button>
                    </>
                ) : (
                    <>
                        <p className="rw-respond__lead">
                            回答は確定済みです。返送ファイルを保存して担当者へお送りください。
                        </p>
                        <Button
                            variant="primary"
                            onClick={() => void exportReturn()}
                            disabledReason={busy ? '処理中です。' : undefined}
                        >
                            返送ファイルを保存する
                        </Button>
                    </>
                )}
                {aiView?.canSignOut || signedOut ? (
                    <section className="rw-respond__signout" aria-label="ChatGPT のサインアウト">
                        <h2 className="rw-respond__subtitle">ChatGPT のサインアウト</h2>
                        <p className="rw-respond__lead">
                            AI との相談に使った ChatGPT のアカウントからサインアウトできます。
                            保存した返送ファイルは変わりません。サインアウトしないまま終えてもかまいません。
                        </p>
                        {signedOut ? (
                            <p className="rw-respond__notice" role="status">
                                {signedOut}
                            </p>
                        ) : (
                            <Button onClick={() => void signOut()} disabledReason={busy ? '処理中です。' : undefined}>
                                サインアウトする
                            </Button>
                        )}
                    </section>
                ) : null}
                {error ? (
                    <p className="rw-respond__error" role="alert">
                        {error}
                    </p>
                ) : null}
            </div>
        )
    }

    // ---- 回答一覧確認 ----------------------------------------
    if (step === 'review') {
        return (
            <Wizard
                title="回答の確認"
                step={questions.length}
                total={questions.length}
                onBack={() => setStep('answer')}
                onNext={() => void exportReturn()}
                nextLabel="この内容で確定して返送ファイルを作る"
                backLabel="質問に戻る"
                nextDisabledReason={
                    unanswered.length > 0
                        ? `未回答の質問が ${unanswered.length} 件あります。すべての質問にお答えください（わからない場合は「わからない」を選べます）。`
                        : busy
                          ? '処理中です。'
                          : undefined
                }
            >
                <ul className="rw-respond__review" aria-label="回答一覧">
                    {questions.map((q, i) => {
                        const d = drafts[q.id] ?? EMPTY_DRAFT
                        return (
                            <li key={q.id} className="rw-respond__review-item">
                                <p className="rw-respond__question">{q.text}</p>
                                <p className="rw-respond__answer">
                                    {d.kind === KIND_UNKNOWN
                                        ? `わからない${d.body ? `（${d.body}）` : ''}`
                                        : [d.selected.join(' / '), d.freeText].filter(Boolean).join(' ') || '未回答'}
                                </p>
                                <Button
                                    onClick={() => {
                                        setIndex(i)
                                        setStep('answer')
                                    }}
                                >
                                    この質問に戻って直す
                                </Button>
                            </li>
                        )
                    })}
                </ul>
                {error ? (
                    <p className="rw-respond__error" role="alert">
                        {error}
                    </p>
                ) : null}
            </Wizard>
        )
    }

    // ---- 質問回答 --------------------------------------------
    if (!current) {
        return (
            <div className="rw-respond">
                <p className="rw-respond__error" role="alert">
                    質問を読み取れませんでした。システム担当者へ連絡してください。
                </p>
            </div>
        )
    }

    return (
        <Wizard
            title={`質問 ${index + 1}`}
            step={index + 1}
            total={questions.length}
            onBack={index > 0 ? () => setIndex(index - 1) : undefined}
            onNext={() => (index + 1 < questions.length ? setIndex(index + 1) : setStep('review'))}
            nextLabel={index + 1 < questions.length ? '次の質問へ' : '回答を確認する'}
            paneLabel="AI との相談"
            pane={
                aiOpen ? (
                    <RespondAIPane
                        questionID={current.id}
                        canApplyToAnswer={acceptsFreeText(format)}
                        onApplyDraft={(d) => update({freeText: d.freeText, kind: KIND_ANSWERED})}
                    />
                ) : undefined
            }
        >
            {opened.notice ? (
                <p className="rw-respond__notice" role="status">
                    {opened.notice}
                </p>
            ) : null}
            {opened.restored && index === 0 ? (
                <p className="rw-respond__notice" role="status">
                    前回の続きから再開しました。入力済みの回答はそのまま残っています。
                </p>
            ) : null}

            <p className="rw-respond__question">{current.text}</p>
            <p className="rw-respond__background">{current.background}</p>
            <p className="rw-respond__hint">{ANSWER_FORMAT_HINTS[format]}</p>

            <fieldset
                className="rw-respond__fieldset"
                disabled={draft.kind === KIND_UNKNOWN}
                ref={answerRef}
                tabIndex={-1}
            >
                <legend>回答</legend>
                {format === 'choice' || format === 'choice_with_free' ? (
                    <>
                        {current.choices?.map((c) => (
                            <label key={c} className="rw-respond__choice">
                                <input
                                    type="radio"
                                    name={`q-${current.id}`}
                                    checked={draft.selected.includes(c)}
                                    onChange={() => update({selected: [c], kind: KIND_ANSWERED})}
                                />
                                <span>{c}</span>
                            </label>
                        ))}
                    </>
                ) : null}
                {format === 'multi_choice' ? (
                    <>
                        {current.choices?.map((c) => (
                            <label key={c} className="rw-respond__choice">
                                <input
                                    type="checkbox"
                                    checked={draft.selected.includes(c)}
                                    onChange={(e) =>
                                        update({
                                            kind: KIND_ANSWERED,
                                            selected: e.target.checked
                                                ? [...draft.selected, c]
                                                : draft.selected.filter((x) => x !== c),
                                        })
                                    }
                                />
                                <span>{c}</span>
                            </label>
                        ))}
                    </>
                ) : null}
                {format === 'free' || format === 'choice_with_free' ? (
                    <label className="rw-respond__field">
                        {format === 'free' ? '回答' : '補足（任意）'}
                        <textarea
                            value={draft.freeText}
                            onChange={(e) => update({freeText: e.target.value, kind: KIND_ANSWERED})}
                        />
                    </label>
                ) : null}
            </fieldset>

            <label className="rw-respond__unknown">
                <input
                    type="checkbox"
                    checked={draft.kind === KIND_UNKNOWN}
                    onChange={(e) =>
                        update({kind: e.target.checked ? KIND_UNKNOWN : KIND_ANSWERED})
                    }
                />
                わからない
            </label>
            {draft.kind === KIND_UNKNOWN ? (
                <label className="rw-respond__field">
                    わからない理由・確認先（未記入でもかまいません）
                    <textarea value={draft.body} onChange={(e) => update({body: e.target.value})} />
                </label>
            ) : null}

            <AIPaneToggle
                open={aiOpen}
                onToggle={() => {
                    // 閉じるときはフォーカスを回答欄へ戻す。
                    setAIOpen(!aiOpen)
                    if (aiOpen) {
                        answerRef.current?.focus()
                    }
                }}
            />

            {error ? (
                <p className="rw-respond__error" role="alert">
                    {error}
                </p>
            ) : null}
        </Wizard>
    )
}
