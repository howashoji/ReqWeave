import {useCallback, useEffect, useState} from 'react'
import {
    AnalyzeImportedAnswers,
    ApproveImportDiff,
    ChooseReturnFile,
    ImportReturnFile,
    ValidateReturnFile,
} from '../../wailsjs/go/binding/API'
import {binding, dialogue, exchange} from '../../wailsjs/go/models'
import {
    Banner,
    Button,
    CandidateEvidence,
    Chip,
    DiffAddition,
    MergePanel,
    useGuideHint,
    errorText,
    lacksEvidence,
    noEvidenceReason,
} from '../ui'
import './ImportAnswers.css'

/**
 * 回答取込（差分承認）。
 *
 * 返送ファイルの読込 → 検証・突合表示 → 取込 → 反映差分の承認を
 * 1 画面で行う。承認操作で画面遷移を起こさない（対話画面の抽出候補パネルと同じ原則）。
 * 検証で止まった場合はプロジェクトデータを変更せず、原因と次の行動を 1 文で示す。
 */

/** 候補ごとの選択。既定は未選択で、承認したものだけが反映される（AI の案を人の承認なしに記録へ入れない）。 */
type Mark = 'pending' | 'approved' | 'discarded'

const ANSWER_KIND_LABELS = {
    answered: '回答あり',
    unknown: '不明',
} as const

function answerKindLabel(kind: string | undefined): string {
    if (!kind) {
        return '未回答'
    }
    return kind in ANSWER_KIND_LABELS ? ANSWER_KIND_LABELS[kind as keyof typeof ANSWER_KIND_LABELS] : '状態不明'
}

/** 確認を要する警告の説明。生の種別コードを画面に出さない。 */
const WARNING_TITLES = {
    reimport: '取込済みの質問票です',
    'content-modified': '質問部が発行時と違います',
    'addressee-mismatch': '宛先が発行時と一致しません',
} as const

function warningTitle(kind: string): string {
    return kind in WARNING_TITLES ? WARNING_TITLES[kind as keyof typeof WARNING_TITLES] : '確認が必要です'
}

export function ImportAnswers({onBack, initialFile}: {onBack: () => void; initialFile?: string}) {
    const [review, setReview] = useState<binding.ImportReviewView | null>(null)
    const [imported, setImported] = useState<binding.ImportResultView | null>(null)
    const [analysis, setAnalysis] = useState<dialogue.ImportAnalysis | null>(null)
    const [applied, setApplied] = useState<binding.ApprovalApplied | null>(null)
    const [conflicts, setConflicts] = useState<binding.ConflictView[]>([])
    const [conflictNotice, setConflictNotice] = useState('')
    const [confirmed, setConfirmed] = useState<Record<string, boolean>>({})
    const [marks, setMarks] = useState<Record<string, Mark>>({})
    const [edits, setEdits] = useState<Record<string, string>>({})
    const [ownerMarks, setOwnerMarks] = useState<Record<string, boolean>>({})
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')
    const [busy, setBusy] = useState(false)
    // 反映に失敗した理由。差分パネルの「承認した内容を反映する」のすぐ上に出す。
    const [approveError, setApproveError] = useState('')
    // 差分が入れ替わったら（別の返送ファイル・再分析）前の失敗理由は消す。
    useEffect(() => setApproveError(''), [analysis])

    const reset = useCallback(() => {
        setReview(null)
        setImported(null)
        setAnalysis(null)
        setApplied(null)
        setConfirmed({})
        setMarks({})
        setEdits({})
        setOwnerMarks({})
        setMessage('')
    }, [])

    /** 返送ファイルを検証する。検証で止まればプロジェクトデータは変わらない。 */
    const validate = useCallback(
        async (src: string) => {
            setBusy(true)
            reset()
            try {
                setReview(await ValidateReturnFile(src))
            } catch (err: unknown) {
                setTone('danger')
                setMessage(errorText(err))
            } finally {
                setBusy(false)
            }
        },
        [reset],
    )

    /** 返送ファイルを選んで検証する。 */
    const choose = async () => {
        let src = ''
        setBusy(true)
        try {
            src = await ChooseReturnFile()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
            return
        } finally {
            setBusy(false)
        }
        if (src) {
            await validate(src)
        }
    }

    // OS のファイル関連付け・ドラッグで渡された返送ファイル。
    // 選択ダイアログを経ずに同じ検証経路へ入る。
    useEffect(() => {
        if (initialFile) {
            void validate(initialFile)
        }
    }, [initialFile, validate])

    /** 確認を要する警告がすべて確認済みか。 */
    const allConfirmed = (review?.warnings ?? []).every((w) => confirmed[w.kind])

    const runImport = async () => {
        setBusy(true)
        setMessage('')
        try {
            const result = await ImportReturnFile({
                AllowReimport: Boolean(confirmed.reimport),
                AcceptModifiedContent: Boolean(confirmed['content-modified']),
                AcceptAddresseeMismatch: Boolean(confirmed['addressee-mismatch']),
            } as exchange.ImportConfirmation)
            setImported(result)
            // 取込後にそのまま反映差分を作る（AI 障害でも突き合わせ表示は残る）。
            setAnalysis(await AnalyzeImportedAnswers(result.questionnaireId))
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    const approve = async (merges: dialogue.MergeResolution[] = []) => {
        if (!imported || !analysis) {
            return
        }
        setBusy(true)
        setMessage('')
        setApproveError('')
        try {
            const decisions = (analysis.extraction?.decisions ?? [])
                .map((c, i) => ({c, key: `decision-${i}`}))
                .filter(({key}) => marks[key] === 'approved')
                .map(({c, key}) => ({
                    candidate: {...c, body: edits[key] ?? c.body},
                    // 決着させる未決事項は根拠の回答から辿る（QS-nnn#q-nn → 質問 → 発行元）。
                    resolvesIssueIds: resolvedIssuesFor(c.evidence_refs, review?.matches),
                }))
            const requirementUpdates = (analysis.extraction?.requirement_updates ?? [])
                .map((c, i) => ({c, key: `requirement-${i}`}))
                .filter(({key}) => marks[key] === 'approved')
                .map(({c, key}) => ({candidate: {...c, body_after: edits[key] ?? c.body_after}}))
            const unknownNotes = (analysis.unknownAnswers ?? []).map((u) => ({
                issueId: u.sourceIssueId,
                answerRef: u.answerRef,
                answeredAt: u.answeredAt,
                reason: u.reason,
            }))
            const ownerUpdates = (analysis.unknownAnswers ?? [])
                .filter((u) => ownerMarks[u.answerRef] && u.ownerCandidate)
                .map((u) => ({issueId: u.sourceIssueId, owner: u.ownerCandidate as string}))

            const outcome = await ApproveImportDiff(imported.questionnaireId, {
                decisions,
                requirementUpdates,
                unknownNotes,
                ownerUpdates,
                merges,
            } as unknown as dialogue.ImportApproval)
            if (outcome.conflicts?.length) {
                // 反映は行われていない。三面を出して本人がマージのしかたを選ぶ（三面マージ）。
                setConflicts(outcome.conflicts)
                setConflictNotice(outcome.notice ?? '')
                setTone('warn')
                setMessage(outcome.notice ?? '他のメンバーの変更と競合しています。')
                return
            }
            setConflicts([])
            setApplied(outcome.applied ?? null)
            setTone('accent')
            setMessage('承認した内容を反映しました。')
        } catch (err: unknown) {
            // 押したボタンのそばに出す（画面上端のバナーは差分の下で押した利用者から見えない）。
            setApproveError(errorText(err))
        } finally {
            setBusy(false)
        }
    }

    /*
     * いまこの画面で次に触る要素（上部の工程ガイドに出す）。
     */
    useGuideHint(
        review === null
            ? '「返送ファイルを選ぶ」を押して、関係者から届いた回答（.rwva）を読み込みます。'
            : '差分を確認し、反映するものを選んで「この回答を取り込む」を押します。',
    )

    return (
        <section className="rw-import" aria-label="回答取込">
            <header className="rw-import__head">
                <h2 className="rw-import__title">回答の取り込み</h2>
                <div className="rw-import__actions">
                    <Button onClick={() => void choose()} disabledReason={busy ? '処理中です。' : undefined}>
                        返送ファイルを選ぶ
                    </Button>
                    <Button onClick={onBack}>対話へ戻る</Button>
                </div>
            </header>

            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')} />
            ) : null}

            {review === null ? (
                <p className="rw-import__hint">
                    ステークホルダーから届いた返送ファイル（.rwva）を選ぶと、質問と回答の対応を確認できます。
                </p>
            ) : null}

            {review ? (
                <>
                    <dl className="rw-import__summary">
                        <dt>質問票</dt>
                        <dd className="rw-mono">{review.questionnaireId}</dd>
                        <dt>宛先</dt>
                        <dd>{review.addressee}</dd>
                        <dt>回答者</dt>
                        <dd>{review.respondent}</dd>
                    </dl>

                    {(review.warnings ?? []).map((w) => (
                        <section key={w.kind} className="rw-import__warning" aria-label={warningTitle(w.kind)}>
                            <h3 className="rw-import__section-title">{warningTitle(w.kind)}</h3>
                            <p className="rw-import__notice" role="alert">
                                {w.message}
                            </p>
                            {w.before || w.after ? (
                                <div className="rw-import__compare">
                                    <div>
                                        <p className="rw-import__label rw-mono">発行時</p>
                                        <pre className="rw-import__pre">{w.before}</pre>
                                    </div>
                                    <div>
                                        <p className="rw-import__label rw-mono">返送</p>
                                        <pre className="rw-import__pre">{w.after}</pre>
                                    </div>
                                </div>
                            ) : null}
                            <label className="rw-import__confirm">
                                <input
                                    type="checkbox"
                                    checked={Boolean(confirmed[w.kind])}
                                    onChange={(e) =>
                                        setConfirmed((prev) => ({...prev, [w.kind]: e.target.checked}))
                                    }
                                />
                                内容を確認しました
                            </label>
                        </section>
                    ))}

                    <section className="rw-import__section" aria-label="質問と回答の対応">
                        <h3 className="rw-import__section-title">質問と回答</h3>
                        {(review.matches ?? []).map((m) => (
                            <article key={m.questionId} className="rw-import__match">
                                <p className="rw-import__label rw-mono">
                                    {m.questionId} ／ 発行元 {m.sourceIssue}{' '}
                                    <Chip tone={m.kind === 'unknown' ? 'warn' : 'info'}>
                                        {answerKindLabel(m.answered ? m.kind : undefined)}
                                    </Chip>
                                </p>
                                <p className="rw-import__question">{m.questionText}</p>
                                <p className="rw-import__answer">
                                    {(m.selected ?? []).join(' / ')}
                                    {m.freeText ? ` ${m.freeText}` : ''}
                                    {m.body ? ` ${m.body}` : ''}
                                    {!m.answered ? '未回答' : ''}
                                </p>
                            </article>
                        ))}
                    </section>

                    {imported === null ? (
                        <div className="rw-import__actions">
                            <Button
                                variant="primary"
                                onClick={() => void runImport()}
                                disabledReason={
                                    !allConfirmed
                                        ? '確認が必要な内容があります。内容を確認してから取り込んでください。'
                                        : busy
                                          ? '処理中です。'
                                          : undefined
                                }
                            >
                                この回答を取り込む
                            </Button>
                        </div>
                    ) : null}
                </>
            ) : null}

            {conflicts.length > 0 ? (
                <MergePanel
                    conflicts={conflicts}
                    notice={conflictNotice}
                    proposals={importMergeProposals(analysis, edits)}
                    busy={busy}
                    onApprove={(merges) => void approve(merges)}
                    onDefer={() => {
                        setConflicts([])
                        setTone('info')
                        setMessage('反映は保留しました。差分は残っています。未決事項として起票することもできます。')
                    }}
                />
            ) : null}

            {analysis ? (
                <DiffPanel
                    analysis={analysis}
                    marks={marks}
                    edits={edits}
                    ownerMarks={ownerMarks}
                    applied={applied}
                    busy={busy}
                    onMark={(key, mark) => setMarks((prev) => ({...prev, [key]: mark}))}
                    onEdit={(key, value) => setEdits((prev) => ({...prev, [key]: value}))}
                    onOwnerMark={(ref, on) => setOwnerMarks((prev) => ({...prev, [ref]: on}))}
                    onApprove={() => void approve()}
                    error={approveError}
                />
            ) : null}
        </section>
    )
}

/** 競合パネルへ渡す「自分の反映案」（対象 ID → 反映しようとした本文）。 */
function importMergeProposals(
    analysis: dialogue.ImportAnalysis | null,
    edits: Record<string, string>,
): Record<string, string> {
    const out: Record<string, string> = {}
    ;(analysis?.extraction?.requirement_updates ?? []).forEach((c, i) => {
        if (c.operation === 'update' && c.target_id) {
            out[c.target_id] = edits[`requirement-${i}`] ?? c.body_after
        }
    })
    return out
}

/** 反映差分の承認。承認・破棄は画面遷移なしでこの場で完結する。 */
function DiffPanel({
    analysis,
    marks,
    edits,
    ownerMarks,
    applied,
    busy,
    onMark,
    onEdit,
    onOwnerMark,
    onApprove,
    error,
}: {
    analysis: dialogue.ImportAnalysis
    marks: Record<string, Mark>
    edits: Record<string, string>
    ownerMarks: Record<string, boolean>
    applied: binding.ApprovalApplied | null
    busy: boolean
    onMark: (key: string, mark: Mark) => void
    onEdit: (key: string, value: string) => void
    onOwnerMark: (ref: string, on: boolean) => void
    onApprove: () => void
    /** 反映に失敗した理由（空なら出さない） */
    error: string
}) {
    const decisions = analysis.extraction?.decisions ?? []
    const diffs = analysis.requirementDiffs ?? []
    const contradictions = analysis.contradictions ?? []
    const unknowns = analysis.unknownAnswers ?? []

    return (
        <section className="rw-import__diff" aria-label="反映差分">
            <h3 className="rw-import__section-title">反映する内容</h3>
            {analysis.notice ? (
                <p className="rw-import__notice" role="alert">
                    {analysis.notice}
                </p>
            ) : null}
            {analysis.fallback ? (
                <p className="rw-import__hint">
                    上の質問と回答の対応を見ながら、反映する決定内容を入力して承認してください。
                </p>
            ) : null}

            <h4 className="rw-import__group">未決事項の決着案</h4>
            {decisions.length === 0 ? <p className="rw-import__hint">なし</p> : null}
            {decisions.map((c, i) => {
                const key = `decision-${i}`
                return (
                    <article key={key} className="rw-import__candidate">
                        <p className="rw-import__label rw-mono">{c.topic_key}</p>
                        <textarea
                            className="rw-import__body"
                            aria-label={`決着案 ${i + 1}`}
                            value={edits[key] ?? c.body}
                            onChange={(e) => onEdit(key, e.target.value)}
                        />
                        {/* 根拠を特定できない決着案は文で示し、承認を選べなくする */}
                        <CandidateEvidence candidate={c} source="回答" recordable={false}>
                            <p className="rw-import__label rw-mono">根拠: {(c.evidence_refs ?? []).join(', ')}</p>
                        </CandidateEvidence>
                        <MarkRow
                            keyName={key}
                            mark={marks[key] ?? 'pending'}
                            onMark={onMark}
                            approveDisabledReason={lacksEvidence(c) ? noEvidenceReason('回答') : undefined}
                        />
                    </article>
                )
            })}

            <h4 className="rw-import__group">要件項目への反映（変更前後の対比）</h4>
            {diffs.length === 0 ? <p className="rw-import__hint">なし</p> : null}
            {diffs.map((d, i) => {
                const key = `requirement-${i}`
                return (
                    <article key={key} className="rw-import__candidate">
                        <p className="rw-import__label rw-mono">
                            {d.targetId ? `${d.targetId} を更新` : '新規に追加'} ／ {d.title}
                        </p>
                        <div className="rw-import__compare">
                            <div>
                                <p className="rw-import__label rw-mono">変更前</p>
                                <pre className="rw-import__pre">{d.bodyBefore || '（新規のため無し）'}</pre>
                            </div>
                            <div>
                                <p className="rw-import__label rw-mono">変更後</p>
                                <DiffAddition>
                                    <textarea
                                        className="rw-import__body"
                                        aria-label={`変更後の本文 ${i + 1}`}
                                        value={edits[key] ?? d.bodyAfter}
                                        onChange={(e) => onEdit(key, e.target.value)}
                                    />
                                </DiffAddition>
                            </div>
                        </div>
                        <MarkRow keyName={key} mark={marks[key] ?? 'pending'} onMark={onMark} />
                    </article>
                )
            })}

            <h4 className="rw-import__group">既存の決定との食い違い</h4>
            {contradictions.length === 0 ? <p className="rw-import__hint">なし</p> : null}
            {contradictions.map((c) => (
                <article key={c.decisionId} className="rw-import__contradiction">
                    <p className="rw-import__label rw-mono">
                        {c.decisionId} <Chip tone="warn">食い違い</Chip>
                    </p>
                    <p className="rw-import__question">{c.description}</p>
                    <pre className="rw-import__pre">{c.decisionBody}</pre>
                </article>
            ))}

            <h4 className="rw-import__group">「不明」の回答</h4>
            {unknowns.length === 0 ? <p className="rw-import__hint">なし</p> : null}
            {unknowns.map((u) => (
                <article key={u.answerRef} className="rw-import__candidate">
                    <p className="rw-import__label rw-mono">
                        {u.answerRef} ／ 発行元 {u.sourceIssueId}
                    </p>
                    <p className="rw-import__question">{u.reason}</p>
                    <p className="rw-import__hint">
                        経過（回答日時と理由）は未決事項へ追記されます。決着案は作りません。
                    </p>
                    {u.ownerCandidate ? (
                        <label className="rw-import__confirm">
                            <input
                                type="checkbox"
                                checked={Boolean(ownerMarks[u.answerRef])}
                                onChange={(e) => onOwnerMark(u.answerRef, e.target.checked)}
                            />
                            決める人を「{u.ownerCandidate}」に変える（現在: {u.currentOwner || '未設定'}）
                        </label>
                    ) : null}
                </article>
            ))}

            {applied ? (
                <section className="rw-import__section" aria-label="反映結果">
                    <h4 className="rw-import__group">反映しました</h4>
                    <ul className="rw-import__result">
                        <li>記録した決定事項: {(applied.decisionIds ?? []).join(' ') || 'なし'}</li>
                        <li>決着した未決事項: {(applied.resolvedIssueIds ?? []).join(' ') || 'なし'}</li>
                        <li>
                            ブロックが外れた要件項目: {(applied.unblockedRequirementIds ?? []).join(' ') || 'なし'}
                        </li>
                        <li>質問票の状態: 取込済み</li>
                    </ul>
                </section>
            ) : (
                <>
                {/* 反映に失敗した理由は押したボタンのすぐ上に出す */}
                {error ? (
                    <p className="rw-import__error" role="alert">
                        反映できませんでした。何も記録していません。
                        {'\n'}
                        {error}
                    </p>
                ) : null}
                <div className="rw-import__actions">
                    <Button
                        variant="primary"
                        onClick={onApprove}
                        disabledReason={busy ? '処理中です。' : undefined}
                    >
                        承認した内容を反映する
                    </Button>
                </div>
                </>
            )}
        </section>
    )
}

function MarkRow({
    keyName,
    mark,
    onMark,
    approveDisabledReason,
}: {
    keyName: string
    mark: Mark
    onMark: (key: string, mark: Mark) => void
    /** 承認を選べない理由（記録できない候補）。無ければ選べる */
    approveDisabledReason?: string
}) {
    return (
        <div className="rw-import__marks">
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
            <span className="rw-import__mark-state">{markLabel(mark)}</span>
        </div>
    )
}

/** 根拠の回答参照（QS-nnn#q-nn）から発行元未決事項を引く（根拠から発行元へ 1 操作で辿る）。 */
export function resolvedIssuesFor(
    evidenceRefs: string[] | undefined,
    matches: binding.AnswerMatchView[] | undefined,
): string[] {
    const byQuestion = new Map<string, string>()
    for (const m of matches ?? []) {
        if (m.sourceIssue) {
            byQuestion.set(m.questionId, m.sourceIssue)
        }
    }
    const out: string[] = []
    for (const ref of evidenceRefs ?? []) {
        const questionID = ref.split('#')[1] ?? ''
        const issue = byQuestion.get(questionID)
        if (issue && !out.includes(issue)) {
            out.push(issue)
        }
    }
    return out
}

function markLabel(mark: Mark): string {
    switch (mark) {
        case 'approved':
            return '承認する'
        case 'discarded':
            return '破棄する'
        default:
            return '未選択（反映されません）'
    }
}
