import {useCallback, useEffect, useMemo, useState} from 'react'
import {
    AddStakeholder,
    ChooseIssueDestination,
    GenerateQuestionDrafts,
    IssueQuestionnaire,
    OpenIssues,
    PreviewQuestionnaireIssue,
    Questionnaires as ListQuestionnaires,
    ReissueQuestionnaire,
    Roster,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Banner, Button, Chip, DataList, formatLocal, useGuideHint, errorText} from '../ui'
import './Questionnaires.css'

/**
 * 質問票管理。
 *
 * 未決事項の選択 → 宛先指定（名簿。未登録はここで登録）→ 質問文の生成・編集 →
 * 発行前プレビュー → 確認操作 → ファイル出力 → パスコードの 1 回提示。
 *
 * パスコードは出力直後に 1 回だけ表示し、保持しない（アプリの中に残さず、漏れる経路を作らない）。
 * 発行フローを閉じると画面の状態ごと破棄するため、戻っても再表示されない。
 * 確認はアプリ内の表示で行い、ネイティブ confirm/alert/prompt を使わない（テーマに追従しないため）。
 */

/** 発行フローの段階。プレビューを経ないと出力へ進めない（何を送るかを確かめてから出すため）。 */
type Step = 'issues' | 'addressee' | 'questions' | 'preview' | 'issued'

const STEP_LABELS: Record<Step, string> = {
    issues: '1. 聞く論点を選ぶ',
    addressee: '2. 宛先を決める',
    questions: '3. 質問文を整える',
    preview: '4. 内容を確認する',
    issued: '5. 発行完了',
}

const ANSWER_FORMAT_LABELS = {
    choice: '選択（1 つ）',
    multi_choice: '選択（複数）',
    free: '自由記述',
    choice_with_free: '選択＋自由記述',
} as const

type AnswerFormat = keyof typeof ANSWER_FORMAT_LABELS

function isAnswerFormat(v: string): v is AnswerFormat {
    return v in ANSWER_FORMAT_LABELS
}

/** 編集中の質問 1 件。 */
type QuestionEdit = {
    sourceIssue: string
    text: string
    background: string
    answerFormat: AnswerFormat
    choices: string
}

function toDraftRequest(addresseeRef: string, questions: QuestionEdit[]): binding.IssueDraftRequest {
    return {
        addresseeRef,
        questions: questions.map((q) => ({
            sourceIssue: q.sourceIssue,
            text: q.text.trim(),
            background: q.background.trim(),
            answerFormat: q.answerFormat,
            choices: q.answerFormat === 'free' ? [] : splitChoices(q.choices),
        })),
    } as binding.IssueDraftRequest
}

/** 選択肢は 1 行 1 件で入力する。 */
function splitChoices(value: string): string[] {
    return value
        .split('\n')
        .map((line) => line.trim())
        .filter((line) => line !== '')
}

/** 未入力の質問があるかを返す（発行前の入力チェック）。 */
function incompleteReason(questions: QuestionEdit[]): string | undefined {
    if (questions.length === 0) {
        return '質問がありません。論点を選び直してください。'
    }
    for (const q of questions) {
        if (q.text.trim() === '') {
            return '質問文が空の質問があります。すべての質問文を入力してください。'
        }
        if (q.background.trim() === '') {
            return '背景説明が空の質問があります。なぜ聞くかを入力してください。'
        }
        if (q.answerFormat !== 'free' && splitChoices(q.choices).length === 0) {
            return '選択肢が空の質問があります。選択肢を入力するか、自由記述に変えてください。'
        }
    }
    return undefined
}

export function QuestionnaireManager({onBack}: {onBack: () => void}) {
    const [list, setList] = useState<binding.QuestionnaireView[]>([])
    const [issues, setIssues] = useState<binding.OpenIssueView[]>([])
    const [roster, setRoster] = useState<binding.StakeholderView[]>([])
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')
    const [busy, setBusy] = useState(false)

    // 発行フローの状態。フローを閉じるときにまとめて捨てる（パスコードを残さない）。
    const [step, setStep] = useState<Step | null>(null)
    const [selectedIssues, setSelectedIssues] = useState<string[]>([])
    const [addresseeRef, setAddresseeRef] = useState('')
    const [newName, setNewName] = useState('')
    const [newOrg, setNewOrg] = useState('')
    const [questions, setQuestions] = useState<QuestionEdit[]>([])
    const [generationNotice, setGenerationNotice] = useState('')
    const [preview, setPreview] = useState<binding.IssuePreviewView | null>(null)
    const [issued, setIssued] = useState<binding.IssueResultView | null>(null)

    const reload = useCallback(() => {
        Promise.all([ListQuestionnaires(), OpenIssues(), Roster()])
            .then(([q, i, r]) => {
                setList(q ?? [])
                setIssues(i ?? [])
                setRoster(r ?? [])
            })
            .catch((err: unknown) => {
                setTone('danger')
                setMessage(errorText(err))
            })
    }, [])

    useEffect(reload, [reload])

    // 質問票にできるのは未決のままの論点だけ（決着済みは選べない）。
    const selectableIssues = useMemo(() => issues.filter((i) => i.status === 'open'), [issues])

    /** 発行フローを閉じる。パスコードを含む状態をすべて破棄する。 */
    const closeFlow = useCallback(() => {
        setStep(null)
        setSelectedIssues([])
        setAddresseeRef('')
        setQuestions([])
        setPreview(null)
        setIssued(null)
        setGenerationNotice('')
        setNewName('')
        setNewOrg('')
        reload()
    }, [reload])

    const toggleIssue = (id: string) => {
        setSelectedIssues((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
    }

    /** 質問文を AI で生成する。障害時は論点を転記したテンプレートで手入力へ進む（AI が使えなくても作業を止めない）。 */
    const generate = async () => {
        setBusy(true)
        setMessage('')
        try {
            const result = await GenerateQuestionDrafts(selectedIssues)
            setQuestions(
                (result.drafts ?? []).map((d) => ({
                    sourceIssue: d.sourceIssue,
                    text: d.text,
                    background: d.background,
                    answerFormat: isAnswerFormat(d.answerFormat) ? d.answerFormat : 'free',
                    choices: (d.choices ?? []).join('\n'),
                })),
            )
            setGenerationNotice(result.fallback ? (result.notice ?? '') : '')
            setStep('questions')
        } catch (err: unknown) {
            // AI を使わずに手入力で進める（発行までの経路を断たない）。
            setQuestions(
                selectedIssues.map((id) => ({
                    sourceIssue: id,
                    text: issues.find((i) => i.id === id)?.topic ?? '',
                    background: '',
                    answerFormat: 'free' as AnswerFormat,
                    choices: '',
                })),
            )
            setGenerationNotice(`${errorText(err)} 論点を転記しましたので、質問文と背景説明を入力してください。`)
            setStep('questions')
        } finally {
            setBusy(false)
        }
    }

    const showPreview = async () => {
        setBusy(true)
        setMessage('')
        try {
            const got = await PreviewQuestionnaireIssue(toDraftRequest(addresseeRef, questions))
            setPreview(got)
            setStep('preview')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    const issue = async () => {
        setBusy(true)
        setMessage('')
        try {
            const dst = await ChooseIssueDestination(preview?.questionnaireId ?? '')
            if (!dst) {
                return
            }
            const result = await IssueQuestionnaire(toDraftRequest(addresseeRef, questions), dst)
            setIssued(result)
            setStep('issued')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    const reissue = async (id: string) => {
        setBusy(true)
        setMessage('')
        try {
            const dst = await ChooseIssueDestination(id)
            if (!dst) {
                return
            }
            const result = await ReissueQuestionnaire(id, dst)
            setIssued(result)
            setStep('issued')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    const addStakeholder = async () => {
        setBusy(true)
        try {
            const added = await AddStakeholder({name: newName, org: newOrg} as binding.StakeholderRequest)
            setRoster((prev) => [...prev, added])
            setAddresseeRef(added.id)
            setNewName('')
            setNewOrg('')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    /*
     * いまこの画面で次に触る要素（上部の工程ガイドに出す）。
     * 発行の各段で、押す操作の名前をそのまま示す。
     */
    useGuideHint(
        step === null
            ? '「新しい質問票を作る」を押して、関係者に聞く論点を選びます。'
            : step === 'issues'
              ? '聞きたい未決事項にチェックを入れ、「次へ（宛先を決める）」を押します。'
              : step === 'addressee'
                ? '名簿から宛先を選び、「次へ（質問文を作る）」を押します。'
                : step === 'questions'
                  ? '質問文と背景説明を整えて、「内容を確認する」を押します。'
                  : step === 'preview'
                    ? '内容を確かめて「この内容で発行する」を押します。'
                    : 'パスコードを控え、質問票ファイルを宛先へ送ります。',
    )

    return (
        <section className="rw-qs" aria-label="質問票管理">
            <header className="rw-qs__head">
                <h2 className="rw-qs__title">質問票</h2>
                <div className="rw-qs__actions">
                    {step === null ? (
                        <Button
                            variant="primary"
                            onClick={() => {
                                setStep('issues')
                                setMessage('')
                            }}
                        >
                            新しい質問票を作る
                        </Button>
                    ) : (
                        <Button variant="quiet" onClick={closeFlow}>
                            発行をやめる
                        </Button>
                    )}
                    <Button onClick={onBack}>対話へ戻る</Button>
                </div>
            </header>

            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')} />
            ) : null}

            {step === null ? (
                <QuestionnaireList list={list} busy={busy} onReissue={(id) => void reissue(id)} />
            ) : (
                <section className="rw-qs__flow" aria-label={STEP_LABELS[step]}>
                    <p className="rw-qs__step rw-mono">{STEP_LABELS[step]}</p>

                    {step === 'issues' ? (
                        <>
                            <DataList
                                caption="聞く論点"
                                columns={[
                                    {key: 'select', label: '選択'},
                                    {key: 'id', label: 'ID', mono: true},
                                    {key: 'topic', label: '論点'},
                                    {key: 'owner', label: '決める人'},
                                    {key: 'questionnaire', label: '質問票'},
                                ]}
                                rows={selectableIssues.map((i) => ({
                                    id: i.id,
                                    cells: {
                                        select: (
                                            <input
                                                type="checkbox"
                                                aria-label={`${i.id} を選ぶ`}
                                                checked={selectedIssues.includes(i.id)}
                                                onChange={() => toggleIssue(i.id)}
                                            />
                                        ),
                                        id: i.id,
                                        topic: i.topic,
                                        owner: i.owner,
                                        questionnaire: <Chip tone="info">{i.questionnaireStatus}</Chip>,
                                    },
                                }))}
                                empty={{
                                    message: 'ステークホルダーに聞ける未決事項がありません。',
                                    next: '対話画面で「次の質問」に答えると、未決事項が起票されます。',
                                    action: {label: '対話へ戻る', onClick: onBack},
                                }}
                            />
                            <div className="rw-qs__actions">
                                <Button
                                    variant="primary"
                                    onClick={() => setStep('addressee')}
                                    disabledReason={
                                        selectedIssues.length === 0 ? '聞く論点を 1 件以上選んでください。' : undefined
                                    }
                                >
                                    次へ（宛先を決める）
                                </Button>
                            </div>
                        </>
                    ) : null}

                    {step === 'addressee' ? (
                        <>
                            <fieldset className="rw-qs__fieldset">
                                <legend>名簿から宛先を選ぶ</legend>
                                {roster.length === 0 ? (
                                    <p className="rw-qs__hint">
                                        名簿に宛先がありません。氏名と所属を登録してから発行に進んでください。
                                    </p>
                                ) : (
                                    roster.map((s) => (
                                        <label key={s.id} className="rw-qs__choice">
                                            <input
                                                type="radio"
                                                name="addressee"
                                                value={s.id}
                                                checked={addresseeRef === s.id}
                                                onChange={() => setAddresseeRef(s.id)}
                                            />
                                            <span>{s.label}</span>
                                        </label>
                                    ))
                                )}
                            </fieldset>

                            <fieldset className="rw-qs__fieldset">
                                <legend>名簿に登録する</legend>
                                <label className="rw-qs__field">
                                    氏名
                                    <input value={newName} onChange={(e) => setNewName(e.target.value)} />
                                </label>
                                <label className="rw-qs__field">
                                    所属
                                    <input value={newOrg} onChange={(e) => setNewOrg(e.target.value)} />
                                </label>
                                <Button
                                    onClick={() => void addStakeholder()}
                                    disabledReason={
                                        newName.trim() === '' || newOrg.trim() === ''
                                            ? '氏名と所属を入力してください。'
                                            : undefined
                                    }
                                >
                                    名簿へ登録して宛先にする
                                </Button>
                            </fieldset>

                            <div className="rw-qs__actions">
                                <Button variant="quiet" onClick={() => setStep('issues')}>
                                    戻る
                                </Button>
                                <Button
                                    variant="primary"
                                    onClick={() => void generate()}
                                    disabledReason={
                                        addresseeRef === ''
                                            ? '名簿から宛先を選ぶか、名簿へ登録してください。'
                                            : busy
                                              ? '処理中です。'
                                              : undefined
                                    }
                                >
                                    次へ（質問文を作る）
                                </Button>
                            </div>
                        </>
                    ) : null}

                    {step === 'questions' ? (
                        <>
                            {generationNotice ? (
                                <Banner
                                    tone="warn"
                                    title={generationNotice}
                                    onDismiss={() => setGenerationNotice('')}
                                    onDefer={() => setGenerationNotice('')}
                                />
                            ) : null}
                            {questions.map((q, index) => (
                                <fieldset key={`${q.sourceIssue}-${index}`} className="rw-qs__fieldset">
                                    <legend>
                                        質問 {index + 1}（<span className="rw-mono">{q.sourceIssue}</span>）
                                    </legend>
                                    <label className="rw-qs__field">
                                        質問文
                                        <textarea
                                            value={q.text}
                                            onChange={(e) =>
                                                setQuestions((prev) =>
                                                    prev.map((x, i) =>
                                                        i === index ? {...x, text: e.target.value} : x,
                                                    ),
                                                )
                                            }
                                        />
                                    </label>
                                    <label className="rw-qs__field">
                                        背景説明
                                        <textarea
                                            value={q.background}
                                            onChange={(e) =>
                                                setQuestions((prev) =>
                                                    prev.map((x, i) =>
                                                        i === index ? {...x, background: e.target.value} : x,
                                                    ),
                                                )
                                            }
                                        />
                                    </label>
                                    <label className="rw-qs__field">
                                        回答のしかた
                                        <select
                                            value={q.answerFormat}
                                            onChange={(e) =>
                                                setQuestions((prev) =>
                                                    prev.map((x, i) =>
                                                        i === index && isAnswerFormat(e.target.value)
                                                            ? {...x, answerFormat: e.target.value}
                                                            : x,
                                                    ),
                                                )
                                            }
                                        >
                                            {Object.entries(ANSWER_FORMAT_LABELS).map(([value, label]) => (
                                                <option key={value} value={value}>
                                                    {label}
                                                </option>
                                            ))}
                                        </select>
                                    </label>
                                    {q.answerFormat === 'free' ? null : (
                                        <label className="rw-qs__field">
                                            選択肢（1 行に 1 つ）
                                            <textarea
                                                value={q.choices}
                                                onChange={(e) =>
                                                    setQuestions((prev) =>
                                                        prev.map((x, i) =>
                                                            i === index ? {...x, choices: e.target.value} : x,
                                                        ),
                                                    )
                                                }
                                            />
                                        </label>
                                    )}
                                </fieldset>
                            ))}
                            <div className="rw-qs__actions">
                                <Button variant="quiet" onClick={() => setStep('addressee')}>
                                    戻る
                                </Button>
                                <Button
                                    variant="primary"
                                    onClick={() => void showPreview()}
                                    disabledReason={incompleteReason(questions)}
                                >
                                    内容を確認する
                                </Button>
                            </div>
                        </>
                    ) : null}

                    {step === 'preview' && preview ? (
                        <>
                            <dl className="rw-qs__summary">
                                <dt>宛先</dt>
                                <dd>{preview.addressee}</dd>
                                <dt>質問数</dt>
                                <dd className="rw-mono">{preview.questionCount}</dd>
                                <dt>発行元の論点</dt>
                                <dd className="rw-mono">{(preview.sourceIssues ?? []).join(' ')}</dd>
                            </dl>

                            <section className="rw-qs__section" aria-label="質問票の全文">
                                <h3 className="rw-qs__section-title">この内容が相手に届きます</h3>
                                <pre className="rw-qs__markdown">{preview.questionnaireMarkdown}</pre>
                            </section>

                            <section className="rw-qs__section" aria-label="同梱する用語">
                                <h3 className="rw-qs__section-title">いっしょに送る用語</h3>
                                {(preview.terms ?? []).length === 0 ? (
                                    <p className="rw-qs__hint">同梱する用語はありません。</p>
                                ) : (
                                    <ul className="rw-qs__terms">
                                        {(preview.terms ?? []).map((t) => (
                                            <li key={t.name}>
                                                <b>{t.name}</b>: {t.definition}
                                            </li>
                                        ))}
                                    </ul>
                                )}
                            </section>

                            <section className="rw-qs__section" aria-label="含まれないもの">
                                <h3 className="rw-qs__section-title">含まれないもの</h3>
                                <ul className="rw-qs__excluded">
                                    {(preview.excluded ?? []).map((x) => (
                                        <li key={x}>{x}</li>
                                    ))}
                                </ul>
                            </section>

                            <div className="rw-qs__actions">
                                <Button variant="secondary" onClick={() => setStep('questions')}>
                                    質問文を直す
                                </Button>
                                <Button
                                    variant="primary"
                                    onClick={() => void issue()}
                                    disabledReason={busy ? '処理中です。' : undefined}
                                >
                                    この内容で発行する
                                </Button>
                            </div>
                        </>
                    ) : null}

                    {step === 'issued' && issued ? (
                        <section className="rw-qs__section" aria-label="発行完了">
                            <h3 className="rw-qs__section-title">
                                {issued.reissued ? '再発行しました' : '発行しました'}
                            </h3>
                            <dl className="rw-qs__summary">
                                <dt>質問票</dt>
                                <dd className="rw-mono">{issued.questionnaireId}</dd>
                                <dt>保存先</dt>
                                <dd className="rw-mono">{issued.path}</dd>
                                <dt>パスコード</dt>
                                <dd className="rw-mono rw-qs__passcode">{issued.passcode}</dd>
                            </dl>
                            <p className="rw-qs__notice" data-tone="warn" role="alert">
                                {issued.passcodeNotice}
                            </p>
                            <div className="rw-qs__actions">
                                <Button variant="primary" onClick={closeFlow}>
                                    パスコードを控えたので閉じる
                                </Button>
                            </div>
                        </section>
                    ) : null}
                </section>
            )}
        </section>
    )
}

/** 質問票一覧。 */
function QuestionnaireList({
    list,
    busy,
    onReissue,
}: {
    list: binding.QuestionnaireView[]
    busy: boolean
    onReissue: (id: string) => void
}) {
    return (
        <DataList
            caption="質問票一覧"
            columns={[
                {key: 'id', label: 'ID', mono: true},
                {key: 'addressee', label: '宛先'},
                {key: 'issuedAt', label: '発行日時', mono: true},
                {key: 'status', label: '状態'},
                {key: 'elapsed', label: '経過日数', mono: true},
                {key: 'action', label: '操作'},
            ]}
            rows={list.map((q) => ({
                id: q.id,
                cells: {
                    id: q.id,
                    addressee: q.addressee,
                    issuedAt: formatLocal(q.issuedAt),
                    status: <Chip tone="info">{q.statusLabel}</Chip>,
                    elapsed: `${q.elapsedDays} 日`,
                    action:
                        q.status === 'issued' ? (
                            <Button
                                onClick={() => onReissue(q.id)}
                                disabledReason={busy ? '処理中です。' : undefined}
                            >
                                再発行
                            </Button>
                        ) : null,
                },
            }))}
            empty={{
                message: 'まだ質問票がありません。',
                next: '「新しい質問票を作る」から発行してください。',
            }}
        />
    )
}

