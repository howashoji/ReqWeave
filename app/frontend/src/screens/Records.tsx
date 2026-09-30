import {useCallback, useEffect, useState} from 'react'
import {
    AgreeRequirement,
    ChangeHistory,
    CheckPhaseDocumentReservation,
    Decisions,
    DialogueSessions,
    DialogueUtterances,
    EditRequirement,
    Evidence,
    Impact,
    OpenIssues,
    Requirements,
    RevertRequirement,
    SearchDialogueUtterances,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Button, Chip, DataList, EmptyState, formatLocal, Overlay, WorkStartDialog, errorText} from '../ui'
import './Records.css'

/**
 * 決定事項・未決事項一覧／要件項目一覧・詳細／対話履歴一覧。
 *
 * いずれも対話画面の文脈を保ったまま開くパネル群として実装する（作業の流れを画面遷移で切らない）。
 * 決定事項の本文を書き換える操作は設けない（決定の記録は追記のみ）。対話履歴は参照専用（監査の記録として改変させない）。
 */

const REQUIREMENT_STATUS_LABELS = {
    draft: 'ドラフト',
    agreed: '合意済み',
} as const

const ISSUE_STATUS_LABELS = {
    open: '未決',
    resolved: '決着',
} as const

const PRIORITY_LABELS = {
    must: '必須',
    should: '推奨',
    may: '任意',
} as const

const PHASE_LABELS = {
    requirements: '要件定義',
    'basic-design': '基本設計',
} as const

const SESSION_TYPE_LABELS = {
    owner: '担当者',
    stakeholder: 'ステークホルダー',
} as const

const SPEAKER_LABELS = {
    agent: 'エージェント',
    user: '担当者',
} as const

function labelOf<T extends Record<string, string>>(map: T, key: string): string {
    return key in map ? map[key as keyof T] : '状態不明'
}

/**
 * 変更影響表示。
 *
 * 要件項目・決定事項を起点に、収載する成果物章・参照する設計要素・
 * ブロックする/していた未決事項を逆引きして示す。呼び出し元の文脈を保つパネルとして開く。
 */
export function ImpactPanel({target, onClose}: {target: string; onClose: () => void}) {
    const [impact, setImpact] = useState<binding.ImpactView | null>(null)
    const [error, setError] = useState('')

    useEffect(() => {
        Impact(target)
            .then(setImpact)
            .catch((err: unknown) => setError(errorText(err)))
    }, [target])

    return (
        // 差し戻しの前に**一時的に読む**ものなので重ねる。一覧・詳細を押し下げない。
        <Overlay label="変更影響" onClose={onClose}>
        <section className="rw-records__impact">
            <header className="rw-records__head">
                <h3 className="rw-records__section">{target} の影響範囲</h3>
                <Button onClick={onClose}>閉じる</Button>
            </header>
            {error ? (
                <p className="rw-records__error" role="alert">
                    {error}
                </p>
            ) : null}
            {impact ? (
                <ul className="rw-records__list rw-records__list--compact-block">
                    <li>
                        収載する成果物の章:{' '}
                        {impact.chapters?.length
                            ? impact.chapters.map((c) => c.title || c.fileName).join(' / ')
                            : 'なし'}
                    </li>
                    <li>
                        参照する設計要素:{' '}
                        {impact.designChapters?.length
                            ? impact.designChapters.map((c) => c.title || c.fileName).join(' / ')
                            : 'なし'}
                    </li>
                    <li>
                        根拠とする要件項目:{' '}
                        <span className="rw-mono">{impact.requirements?.join(', ') || 'なし'}</span>
                    </li>
                    <li>
                        ブロックする / していた未決事項:{' '}
                        <span className="rw-mono">{impact.openIssues?.join(', ') || 'なし'}</span>
                    </li>
                </ul>
            ) : null}
        </section>
        </Overlay>
    )
}

/**
 * 根拠の中身。
 *
 * 根拠は種別で内容が違う（発話 = 前後文脈 / 取り込み資料 = 原本の所在と該当箇所 /
 * 回答 = 質問と答えの対）。**バインディングが返した種別を取りこぼさず描画する**
 * ため、種別ごとの分岐を 1 か所に集める（以前に種別の描画漏れで中身が空になった）。
 *
 * どの種別も描画できないときは「見つかった」とだけ言って空にせず、想定外として明示する
 * （見出しだけ出て中身が無い状態は、参照欠落と見分けがつかない）。
 */
function EvidenceBody({evidence}: {evidence: binding.EvidenceView}) {
    if (!evidence.found) {
        return <p className="rw-records__notice">参照先が見つかりません（参照欠落）。</p>
    }
    if (evidence.context && evidence.context.length > 0) {
        return (
            <>
                {evidence.context.map((u) => (
                    <p key={u.id} className="rw-records__context">
                        <span className="rw-mono">{u.speaker === 'agent' ? 'エージェント' : '担当者'}</span>{' '}
                        {u.body}
                    </p>
                ))}
            </>
        )
    }
    if (evidence.import) {
        const loc = evidence.import
        return (
            <div className="rw-records__evidence-import">
                <p className="rw-records__context">
                    取り込み資料 <span className="rw-mono">{loc.importId}</span>「{loc.sourceName}」の
                    {loc.startLine}〜{loc.endLine} 行目
                </p>
                <p className="rw-records__evidence-meta">原本の場所: {loc.sourcePath}</p>
                {loc.excerpt?.map((line, i) => (
                    <p key={`${loc.startLine + i}`} className="rw-records__context">
                        <span className="rw-mono">{loc.startLine + i}</span> {line}
                    </p>
                ))}
            </div>
        )
    }
    if (evidence.answer) {
        const a = evidence.answer
        return (
            <div className="rw-records__evidence-answer">
                <p className="rw-records__context">
                    質問 <span className="rw-mono">{a.questionnaireId}#{a.questionId}</span>: {a.questionText}
                </p>
                {a.background ? <p className="rw-records__evidence-meta">背景説明: {a.background}</p> : null}
                {a.kind === 'unknown' ? (
                    <p className="rw-records__context">回答: 不明（{a.body || '理由の記載なし'}）</p>
                ) : (
                    <>
                        {a.selected && a.selected.length > 0 ? (
                            <p className="rw-records__context">回答: {a.selected.join('、')}</p>
                        ) : null}
                        {a.freeText ? <p className="rw-records__context">回答: {a.freeText}</p> : null}
                        {a.body ? <p className="rw-records__context">補足: {a.body}</p> : null}
                    </>
                )}
                <p className="rw-records__evidence-meta">
                    回答者: {a.respondent}／回答日時: {formatLocal(a.answeredAt)}
                    {a.sourceIssue ? `／発行元: ${a.sourceIssue}` : ''}
                </p>
            </div>
        )
    }
    return (
        <p className="rw-records__notice">
            この根拠の種別に対応する表示がありません。開発元へ連絡してください（参照: {evidence.ref}）。
        </p>
    )
}

/** 決定事項・未決事項一覧。 */
export function DecisionsAndIssues({onBack}: {onBack: () => void}) {
    const [decisions, setDecisions] = useState<binding.DecisionView[]>([])
    const [issues, setIssues] = useState<binding.OpenIssueView[]>([])
    const [history, setHistory] = useState<binding.ChangeHistoryView[]>([])
    const [evidence, setEvidence] = useState<binding.EvidenceView | null>(null)
    const [impactTarget, setImpactTarget] = useState('')
    const [error, setError] = useState('')

    useEffect(() => {
        Promise.all([Decisions(), OpenIssues(), ChangeHistory()])
            .then(([d, i, h]) => {
                setDecisions(d)
                setIssues(i)
                setHistory(h)
            })
            .catch((err: unknown) => setError(errorText(err)))
    }, [])

    const showEvidence = useCallback(async (ref: string) => {
        try {
            setEvidence(await Evidence(ref))
        } catch (err: unknown) {
            setError(errorText(err))
        }
    }, [])

    return (
        <section className="rw-records" aria-label="決定事項・未決事項">
            <header className="rw-records__head">
                <h2 className="rw-records__title">決定事項・未決事項</h2>
                <Button onClick={onBack}>対話へ戻る</Button>
            </header>
            {error ? (
                <p className="rw-records__error" role="alert">
                    {error}
                </p>
            ) : null}

            <h3 className="rw-records__section">決定事項</h3>
            {decisions.length === 0 ? (
                <EmptyState
                    message="決定事項はまだありません。"
                    next="対話画面で「次の質問」に答え、抽出された候補を承認すると記録されます。"
                    action={{label: '対話へ戻る', onClick: onBack}}
                />
            ) : null}
            <ul className="rw-records__list">
                {decisions.map((d) => (
                    <li key={d.id} className="rw-records__item">
                        <p className="rw-records__meta rw-mono">
                            {d.id} / {d.topicKey} / {d.decidedAt}
                            {d.supersededBy ? <Chip tone="warn">{d.supersededBy} で置き換え</Chip> : null}
                        </p>
                        <p className="rw-records__body">{d.body}</p>
                        <p className="rw-records__evidence">
                            根拠:{' '}
                            {d.evidence.map((ref) => (
                                <Button key={ref} variant="secondary" onClick={() => void showEvidence(ref)}>
                                    {ref}
                                </Button>
                            ))}
                            <Button variant="secondary" onClick={() => setImpactTarget(d.id)}>
                                影響範囲
                            </Button>
                        </p>
                    </li>
                ))}
            </ul>

            <h3 className="rw-records__section">未決事項</h3>
            {issues.length === 0 ? (
                <EmptyState
                    message="未決事項はまだありません。"
                    next="対話で決めきれなかったことは、候補の承認で未決事項として記録されます。"
                    action={{label: '対話へ戻る', onClick: onBack}}
                />
            ) : null}
            <ul className="rw-records__list">
                {issues.map((i) => (
                    <li key={i.id} className="rw-records__item" data-overdue={i.overdue}>
                        <p className="rw-records__meta rw-mono">
                            {i.id} / 決める人: {i.owner} / 期限: {i.due || '未定'} /{' '}
                            {labelOf(ISSUE_STATUS_LABELS, i.status)} / 質問票: {i.questionnaireStatus}
                            {i.overdue ? <Chip tone="danger">期限超過</Chip> : null}
                        </p>
                        <p className="rw-records__body">{i.topic}</p>
                        {i.blocking?.length ? (
                            <p className="rw-records__blocking rw-mono">
                                ブロック対象: {i.blocking.join(', ')}
                            </p>
                        ) : null}
                    </li>
                ))}
            </ul>

            {impactTarget ? <ImpactPanel target={impactTarget} onClose={() => setImpactTarget('')} /> : null}

            {evidence ? (
                // 根拠は**読むための一時の面**。一覧を押し下げない。
                <Overlay label="根拠" onClose={() => setEvidence(null)}>
                    <section className="rw-records__evidence-panel">
                        <h3 className="rw-records__section">根拠 {evidence.ref}</h3>
                        <EvidenceBody evidence={evidence} />
                        <Button onClick={() => setEvidence(null)}>閉じる</Button>
                    </section>
                </Overlay>
            ) : null}

            <h3 className="rw-records__section">変更履歴</h3>
            {history.length === 0 ? (
                <EmptyState
                    message="変更履歴はまだありません。"
                    automatic="決定事項・未決事項・要件項目を記録するたびに、ここへ残ります。"
                />
            ) : null}
            <ul className="rw-records__list rw-records__list--compact">
                {history.slice(0, 20).map((h, i) => (
                    <li key={`${h.at}-${i}`} className="rw-records__history rw-mono">
                        {h.at} {h.author} {h.target} {h.change}
                    </li>
                ))}
            </ul>
        </section>
    )
}

/** 要件項目一覧・詳細。手動編集・差し戻し（理由必須）を持つ。 */
export function RequirementList({onBack}: {onBack: () => void}) {
    const [items, setItems] = useState<binding.RequirementView[]>([])
    const [selected, setSelected] = useState<binding.RequirementView | null>(null)
    const [title, setTitle] = useState('')
    const [body, setBody] = useState('')
    const [reason, setReason] = useState('')
    const [message, setMessage] = useState('')
    const [error, setError] = useState('')
    const [showImpact, setShowImpact] = useState(false)
    // pendingRevert は差し戻しの着手前の進め方の選択待ち（予約の選択ダイアログ）。
    const [pendingRevert, setPendingRevert] = useState<binding.ReservationCheckView | null>(null)

    const reload = useCallback(async () => {
        try {
            const list = await Requirements()
            setItems(list)
            setSelected((prev) => (prev ? list.find((r) => r.id === prev.id) ?? null : null))
        } catch (err: unknown) {
            setError(errorText(err))
        }
    }, [])

    useEffect(() => {
        void reload()
    }, [reload])

    function select(item: binding.RequirementView) {
        setSelected(item)
        setTitle(item.title)
        setBody(item.body)
        setReason('')
        setMessage('')
        setShowImpact(false)
    }

    const save = useCallback(async () => {
        if (!selected) {
            return
        }
        try {
            await EditRequirement({
                id: selected.id,
                title,
                body,
                acceptanceCriteria: selected.acceptanceCriteria ?? [],
            })
            setMessage('要件項目を更新しました。')
            await reload()
        } catch (err: unknown) {
            setError(errorText(err))
        }
    }, [selected, title, body, reload])

    const agree = useCallback(async () => {
        if (!selected) {
            return
        }
        try {
            await AgreeRequirement(selected.id)
            setMessage('合意済みにしました。')
            await reload()
        } catch (err: unknown) {
            setError(errorText(err))
        }
    }, [selected, reload])

    /**
     * 差し戻しの着手。
     * 成果物の内容を変える操作のため、進め方（排他 / 並行）を選んでから実行する。
     */
    const askRevert = useCallback(async () => {
        let check: binding.ReservationCheckView | null = null
        try {
            check = await CheckPhaseDocumentReservation()
        } catch {
            check = null
        }
        setPendingRevert(check ?? ({} as binding.ReservationCheckView))
    }, [])

    const revert = useCallback(
        async (work: binding.WorkStart) => {
            if (!selected) {
                return
            }
            try {
                await RevertRequirement(selected.id, reason, work)
                setMessage('差し戻しました。理由を記録しています。')
                setReason('')
                await reload()
            } catch (err: unknown) {
                setError(errorText(err))
            }
        },
        [selected, reason, reload],
    )

    const columns = [
        {key: 'id', label: 'ID', mono: true},
        {key: 'title', label: '要件項目'},
        {key: 'priority', label: '優先度'},
        {key: 'state', label: '状態'},
        {key: 'blocked', label: 'ブロック'},
    ]
    const rows = items.map((r) => ({
        id: r.id,
        cells: {
            id: r.id,
            title: r.title,
            priority: labelOf(PRIORITY_LABELS, r.priority),
            state: (
                <Chip tone={r.status === 'agreed' ? 'accent' : 'info'}>
                    {labelOf(REQUIREMENT_STATUS_LABELS, r.status)}
                </Chip>
            ),
            blocked: r.blockedBy?.length ? <Chip tone="warn">未決 {r.blockedBy.length}</Chip> : '—',
        },
    }))

    return (
        <section className="rw-records" aria-label="要件項目">
            <header className="rw-records__head">
                <h2 className="rw-records__title">要件項目</h2>
                <Button onClick={onBack}>対話へ戻る</Button>
            </header>
            {error ? (
                <p className="rw-records__error" role="alert">
                    {error}
                </p>
            ) : null}
            {message ? <p className="rw-records__message">{message}</p> : null}

            <DataList
                caption="要件項目"
                columns={columns}
                rows={rows}
                empty={{
                    message: '要件項目はまだありません。',
                    next: '対話画面で「次の質問」に答え、抽出された候補を承認すると増えます。',
                    action: {label: '対話へ戻る', onClick: onBack},
                }}
                selectedId={selected?.id}
                onSelect={(id) => {
                    const item = items.find((r) => r.id === id)
                    if (item) {
                        select(item)
                    }
                }}
            />

            {selected ? (
                <section className="rw-records__detail" aria-label="要件項目の詳細">
                    <h3 className="rw-records__section">
                        {selected.id} {selected.missingEvidence ? <Chip tone="warn">参照欠落</Chip> : null}
                    </h3>
                    <label className="rw-records__field">
                        要件名
                        <input value={title} onChange={(e) => setTitle(e.target.value)} aria-label="要件名" />
                    </label>
                    <label className="rw-records__field">
                        要件文
                        <textarea value={body} onChange={(e) => setBody(e.target.value)} aria-label="要件文" />
                    </label>
                    {selected.acceptanceCriteria?.length ? (
                        <ul className="rw-records__criteria">
                            {selected.acceptanceCriteria.map((c) => (
                                <li key={c}>{c}</li>
                            ))}
                        </ul>
                    ) : (
                        <EmptyState
                            message="受け入れ条件がありません。"
                            next="対話画面で「次の質問」に答えると抽出されます。要件文の編集でも足せます。"
                        />
                    )}
                    {selected.decisions?.length ? (
                        <p className="rw-records__meta rw-mono">根拠の決定事項: {selected.decisions.join(', ')}</p>
                    ) : null}
                    {selected.revertedReason ? (
                        <p className="rw-records__meta">差し戻しの理由: {selected.revertedReason}</p>
                    ) : null}

                    {/* 差し戻し操作の前に影響一覧を確認表示する。 */}
                    {showImpact ? (
                        <ImpactPanel target={selected.id} onClose={() => setShowImpact(false)} />
                    ) : null}

                    {pendingRevert ? (
                        <WorkStartDialog
                            operation="要件項目の差し戻し"
                            check={pendingRevert}
                            busy={false}
                            onStart={(work) => {
                                setPendingRevert(null)
                                void revert(work)
                            }}
                            onCancel={() => setPendingRevert(null)}
                        />
                    ) : null}

                    <div className="rw-records__actions">
                        <Button variant="primary" onClick={() => void save()}>
                            編集を保存
                        </Button>
                        <Button onClick={() => setShowImpact(true)}>影響範囲を見る</Button>
                        {selected.status === 'draft' ? (
                            <Button onClick={() => void agree()}>合意済みにする</Button>
                        ) : (
                            <>
                                <label className="rw-records__field">
                                    差し戻しの理由
                                    <input
                                        value={reason}
                                        onChange={(e) => setReason(e.target.value)}
                                        aria-label="差し戻しの理由"
                                    />
                                </label>
                                <Button
                                    onClick={() => {
                                        setShowImpact(true)
                                        void askRevert()
                                    }}
                                    disabledReason={reason.trim() ? undefined : '差し戻しの理由を入力してください。'}
                                >
                                    差し戻す
                                </Button>
                            </>
                        )}
                    </div>
                </section>
            ) : null}
        </section>
    )
}

/** 対話履歴一覧。参照専用で、書き換え操作を持たない。 */
/**
 * 対話履歴一覧。参照専用。
 *
 * フェーズ・種別の絞り込みと、発話本文の全文検索。
 * 検索は絞り込みを通過したセッションだけを対象にし（併用）、
 * 結果の項目を選ぶと当該セッションを開いて該当発話へ移動する。
 * 検索の経路から発話を書き換える手段は設けない。
 */
export function DialogueHistory({onBack}: {onBack: () => void}) {
    const [sessions, setSessions] = useState<binding.DialogueSessionView[]>([])
    const [selected, setSelected] = useState('')
    const [utterances, setUtterances] = useState<binding.UtteranceView[]>([])
    const [phase, setPhase] = useState('all')
    const [kind, setKind] = useState('all')
    const [error, setError] = useState('')
    const [query, setQuery] = useState('')
    const [result, setResult] = useState<binding.UtteranceSearchView | null>(null)
    // 検索を実行したときの条件（0 件のときに「何をどう探したか」を示すため実行時点の値を保つ）。
    const [searched, setSearched] = useState<{text: string; phase: string; kind: string} | null>(null)
    const [focused, setFocused] = useState('')

    useEffect(() => {
        DialogueSessions()
            .then((list) => {
                setSessions(list)
                if (list[0]) {
                    setSelected(list[0].id)
                }
            })
            .catch((err: unknown) => setError(errorText(err)))
    }, [])

    useEffect(() => {
        if (!selected) {
            return
        }
        DialogueUtterances(selected)
            .then(setUtterances)
            .catch((err: unknown) => setError(errorText(err)))
    }, [selected])

    // 該当発話へ移動する（発話の読み込みが終わってから位置を合わせる）。
    useEffect(() => {
        if (!focused) {
            return
        }
        const el = document.getElementById(utteranceDomID(focused))
        if (el && typeof el.scrollIntoView === 'function') {
            el.scrollIntoView({block: 'center'})
        }
    }, [focused, utterances])

    const visible = sessions.filter(
        (s) => (phase === 'all' || s.phase === phase) && (kind === 'all' || s.type === kind),
    )

    function runSearch() {
        const text = query.trim()
        if (!text) {
            setError('探したい語句がありません。検索したい語句を入力してください。')
            return
        }
        SearchDialogueUtterances(text, phase, kind)
            .then((found) => {
                setError('')
                setResult(found)
                setSearched({text, phase, kind})
                setFocused('')
            })
            .catch((err: unknown) => setError(errorText(err)))
    }

    function clearSearch() {
        setQuery('')
        setResult(null)
        setSearched(null)
        setFocused('')
    }

    function openHit(hit: binding.UtteranceHitView) {
        setSelected(hit.sessionId)
        setFocused(hit.id)
    }

    return (
        <section className="rw-records" aria-label="対話履歴">
            <header className="rw-records__head">
                <h2 className="rw-records__title">対話履歴</h2>
                <Button onClick={onBack}>対話へ戻る</Button>
            </header>
            {error ? (
                <p className="rw-records__error" role="alert">
                    {error}
                </p>
            ) : null}

            <div className="rw-records__filters">
                <label className="rw-records__field">
                    フェーズ
                    <select value={phase} onChange={(e) => setPhase(e.target.value)} aria-label="フェーズで絞り込む">
                        <option value="all">すべて</option>
                        <option value="requirements">要件定義</option>
                        <option value="basic-design">基本設計</option>
                    </select>
                </label>
                <label className="rw-records__field">
                    種別
                    <select value={kind} onChange={(e) => setKind(e.target.value)} aria-label="種別で絞り込む">
                        <option value="all">すべて</option>
                        <option value="owner">担当者</option>
                        <option value="stakeholder">ステークホルダー</option>
                    </select>
                </label>
                <label className="rw-records__field rw-records__field--search">
                    発話の検索
                    <input
                        type="search"
                        value={query}
                        aria-label="発話本文を検索する"
                        placeholder="語句を入力"
                        onChange={(e) => setQuery(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter') {
                                e.preventDefault()
                                runSearch()
                            }
                        }}
                    />
                </label>
                <Button onClick={runSearch}>検索</Button>
                {result ? <Button onClick={clearSearch}>検索を解除</Button> : null}
            </div>

            {result && searched ? (
                <div className="rw-records__search" aria-label="検索結果">
                    <p className="rw-records__search-summary" role="status">
                        「{searched.text}」の検索結果 {result.total} 件（絞り込み: フェーズ=
                        {searched.phase === 'all' ? 'すべて' : labelOf(PHASE_LABELS, searched.phase)} / 種別=
                        {searched.kind === 'all' ? 'すべて' : labelOf(SESSION_TYPE_LABELS, searched.kind)}）
                        {result.truncated ? `。先頭 ${result.limit} 件を表示しています` : ''}
                    </p>
                    {result.total === 0 ? (
                        <EmptyState
                            message="該当する発話はありません。"
                            next="語句を短くするか、フェーズ・種別の絞り込みを「すべて」に戻して検索してください。"
                        />
                    ) : (
                        <ul className="rw-records__list rw-records__list--compact-block" aria-label="検索結果一覧">
                            {result.hits.map((hit) => (
                                <li key={`${hit.sessionId}-${hit.id}`}>
                                    <Button variant="secondary" onClick={() => openHit(hit)}>
                                        {hit.sessionId} / {labelOf(PHASE_LABELS, hit.phase)} /{' '}
                                        {labelOf(SESSION_TYPE_LABELS, hit.type)} / {hit.id} /{' '}
                                        {labelOf(SPEAKER_LABELS, hit.speaker)} / {formatLocal(hit.at)}
                                    </Button>
                                    {hit.status === 'interrupted' ? <Chip tone="warn">中断</Chip> : null}
                                    <span className="rw-records__excerpt">{hit.excerpt}</span>
                                </li>
                            ))}
                        </ul>
                    )}
                </div>
            ) : null}

            <ul className="rw-records__list rw-records__list--compact" aria-label="セッション一覧">
                {visible.map((s) => (
                    <li key={s.id}>
                        <Button variant={s.id === selected ? 'primary' : 'secondary'} onClick={() => setSelected(s.id)}>
                            {s.id} / {labelOf(PHASE_LABELS, s.phase)} / {labelOf(SESSION_TYPE_LABELS, s.type)}
                        </Button>
                    </li>
                ))}
            </ul>

            <div className="rw-records__utterances" aria-label="発話履歴">
                {utterances.map((u) => (
                    <p
                        key={u.id}
                        id={utteranceDomID(u.id)}
                        className="rw-records__utterance"
                        data-focused={u.id === focused ? 'true' : undefined}
                    >
                        <span className="rw-mono">
                            {u.id} {labelOf(SPEAKER_LABELS, u.speaker)} {u.at}
                        </span>
                        {u.status === 'interrupted' ? <Chip tone="warn">中断</Chip> : null}
                        <span className="rw-records__utterance-body">{u.body}</span>
                    </p>
                ))}
            </div>
        </section>
    )
}

/** 発話 1 件の DOM id（検索結果からの移動先）。 */
function utteranceDomID(utteranceID: string): string {
    return `rw-utterance-${utteranceID}`
}
