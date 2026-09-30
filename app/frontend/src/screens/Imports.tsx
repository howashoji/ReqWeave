import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react'
import {
    AddPerspective,
    AnalyzeImport,
    ApproveImportCandidates,
    ChooseImportDirectory,
    ChooseImportFile,
    CurrentPermission,
    Evidence,
    FeedbackClassificationOptions,
    ImportClipboardText,
    ImportContent,
    ImportDirectory,
    ImportFile,
    ImportKinds,
    Imports as ListImports,
    PendingImportCandidates,
    Perspectives as ListPerspectives,
    PreviewImportAnalysis,
    RemovePerspective,
    ScanImportDirectory,
    SetFeedbackClassification,
    UpdatePerspective,
} from '../../wailsjs/go/binding/API'
import {binding, dialogue} from '../../wailsjs/go/models'
import {
    Banner,
    Button,
    CandidateEvidence,
    Chip,
    DocumentWorkspace,
    EmptyState,
    formatCount,
    MergePanel,
    useGuideHint,
    errorText,
    lacksEvidence,
    noEvidenceReason,
} from '../ui'
import './Imports.css'

/**
 * 取り込み。画面型は文書・承認系の 3 ペイン。
 *
 * 左 = 取り込み操作と取り込み済み一覧 / 中 = 抽出テキスト（根拠箇所の強調）と送信前プレビュー /
 * 右 = 候補の承認とプロジェクト観点の管理。
 *
 * 承認は画面遷移なしでこの場で完結する（対話画面の抽出候補パネルと同じ原則）。
 * 権限の判定はバインディングが行い、画面はその結果で無効化と理由表示だけを行う
 * （操作を隠さない。何ができないかと理由を利用者が知れるように）。
 */

/** 候補ごとの選択。既定は未選択で、承認したものだけが反映される（AI の案を人の承認なしに記録へ入れない）。 */
type Mark = 'pending' | 'approved' | 'discarded'

/** 根拠参照（IMP-nnn#Lm-Ln）から行範囲を取り出す。形式外は null。 */
function refLines(ref: string): {from: number; to: number} | null {
    const m = /^IMP-\d{3}#L(\d+)-L(\d+)$/.exec(ref)
    if (!m) {
        return null
    }
    return {from: Number(m[1]), to: Number(m[2])}
}

export function Imports({onBack}: {onBack: () => void}) {
    const [kinds, setKinds] = useState<binding.ImportKindOption[]>([])
    const [kind, setKind] = useState<string>('material')
    const [list, setList] = useState<binding.ImportView[]>([])
    const [selected, setSelected] = useState<string>('')
    const [content, setContent] = useState<binding.ImportContentView | null>(null)
    const [paste, setPaste] = useState('')
    // ディレクトリ指定の一括取り込み。
    // scan = 確認待ちの内容（実行前）、batch = 実行結果。どちらも取りやめ・閉じるで消える。
    const [scan, setScan] = useState<binding.ImportScanView | null>(null)
    const [batch, setBatch] = useState<binding.ImportBatchView | null>(null)
    const [permission, setPermission] = useState<binding.PermissionView | null>(null)
    const [preview, setPreview] = useState<dialogue.ImportSendPreview | null>(null)
    // 送信前プレビューの位置（押した結果まで自動で送る）。
    const previewRef = useRef<HTMLHeadingElement>(null)
    const [consent, setConsent] = useState(false)
    const [analysis, setAnalysis] = useState<dialogue.FeedbackAnalysis | null>(null)
    const [pending, setPending] = useState<dialogue.Extraction | null>(null)
    const [marks, setMarks] = useState<Record<string, Mark>>({})
    const [edits, setEdits] = useState<Record<string, string>>({})
    // 未決事項の候補の「決める人・期限」（決める人が空だと記録できない）。
    const [owners, setOwners] = useState<Record<string, {owner: string; due: string}>>({})
    // 反映に失敗した理由。右下の反映ボタンのすぐ上に出す。
    const [approveError, setApproveError] = useState('')
    const [revertConfirmed, setRevertConfirmed] = useState<Record<string, boolean>>({})
    // 競合。反映は行われていない状態で、本人がマージのしかたを選ぶ。
    const [conflicts, setConflicts] = useState<binding.ConflictView[]>([])
    const [conflictNotice, setConflictNotice] = useState('')
    const [highlight, setHighlight] = useState<{from: number; to: number} | null>(null)
    const [evidence, setEvidence] = useState<binding.EvidenceView | null>(null)
    const [classifications, setClassifications] = useState<binding.FeedbackClassificationOption[]>([])
    const [perspectives, setPerspectives] = useState<binding.PerspectiveView[]>([])
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')
    const [busy, setBusy] = useState(false)

    const fail = useCallback((err: unknown) => {
        setTone('danger')
        setMessage(errorText(err))
    }, [])

    const reloadList = useCallback(async () => {
        try {
            setList(await ListImports())
        } catch (err: unknown) {
            fail(err)
        }
    }, [fail])

    const reloadPerspectives = useCallback(async () => {
        try {
            setPerspectives(await ListPerspectives())
        } catch (err: unknown) {
            fail(err)
        }
    }, [fail])

    useEffect(() => {
        Promise.resolve()
            .then(() => ImportKinds())
            .then(setKinds)
            .catch(() => undefined)
        Promise.resolve()
            .then(() => FeedbackClassificationOptions())
            .then(setClassifications)
            .catch(() => undefined)
        Promise.resolve()
            .then(() => CurrentPermission())
            .then(setPermission)
            .catch(() => undefined)
        void reloadList()
        void reloadPerspectives()
    }, [reloadList, reloadPerspectives])

    /** 資料を選び直したら、内容と分析の状態を読み直す（未承認候補は保全から復元する）。 */
    const select = useCallback(
        async (id: string) => {
            setSelected(id)
            setPreview(null)
            setConsent(false)
            setAnalysis(null)
            setMarks({})
            setEdits({})
            setOwners({})
            setHighlight(null)
            setEvidence(null)
            try {
                setContent(await ImportContent(id))
                setPending(await PendingImportCandidates(id))
            } catch (err: unknown) {
                fail(err)
            }
        },
        [fail],
    )

    const canEdit = permission?.canEdit ?? false
    const denyReason = permission?.reason ?? '編集権限が必要です。オーナーに権限の変更を依頼してください。'
    const editReason = (reason?: string) => (canEdit ? reason : denyReason)

    const importFile = async () => {
        setBusy(true)
        setMessage('')
        try {
            const path = await ChooseImportFile()
            if (!path) {
                return
            }
            const added = await ImportFile(path, kind)
            await reloadList()
            await select(added.id)
            setTone('accent')
            setMessage(`${added.sourceName} を取り込みました。`)
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    /** フォルダを選んで中身を調べる。**ここでは取り込まない**（確認操作の材料を出すだけ）。 */
    const chooseDirectory = async () => {
        setBusy(true)
        setMessage('')
        setBatch(null)
        try {
            const dir = await ChooseImportDirectory()
            if (!dir) {
                return
            }
            setScan(await ScanImportDirectory(dir))
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    /** 確認したうえで一括取り込みを実行する（取り込むだけで、分析は行わない）。 */
    const runDirectoryImport = async () => {
        if (!scan) {
            return
        }
        setBusy(true)
        setMessage('')
        try {
            const result = await ImportDirectory(scan.dir, kind)
            setScan(null)
            setBatch(result)
            await reloadList()
            setTone(result.failedCount > 0 ? 'warn' : 'accent')
            setMessage(result.notice)
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const importPaste = async () => {
        setBusy(true)
        setMessage('')
        try {
            const added = await ImportClipboardText(paste, kind)
            setPaste('')
            await reloadList()
            await select(added.id)
            setTone('accent')
            setMessage('貼り付けた内容を取り込みました。')
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const buildPreview = async () => {
        setBusy(true)
        setMessage('')
        try {
            const built = await PreviewImportAnalysis(selected)
            setPreview(built)
            setConsent(false)
            // **押した結果を画面で伝える**（実機で「処理が進まない」と受け取られた）。
            // 結果は中央ペインの抽出テキストより下に出るため、案内文とスクロールの両方で示す。
            setTone(built.tooLarge ? 'warn' : 'info')
            setMessage(
                built.tooLarge
                    ? (built.notice ?? '送信内容が大きすぎるため分析を開始できません。資料を分けて取り込み直してください。')
                    : `送信内容を作成しました（分割 ${built.chunkCount} 件・概算 ${built.estimatedTokens} トークン）。` +
                      '内容を確認し、「送信することに同意します」にチェックしてから分析してください。',
            )
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    // プレビューが出たら、その位置まで送る（利用者が探さなくてよいようにする）。
    // 描画の確定と同じ段で動かす（描画後にずらすと一瞬だけ元の位置が見えてちらつく）。
    useLayoutEffect(() => {
        if (preview && previewRef.current) {
            previewRef.current.scrollIntoView({block: 'start'})
        }
    }, [preview])

    const analyze = async () => {
        setBusy(true)
        setMessage('')
        try {
            const result = await AnalyzeImport(selected, true)
            setAnalysis(result)
            setPending(result.extraction ?? null)
            await reloadList()
            if (result.notice) {
                setTone('warn')
                setMessage(result.notice)
            }
        } catch (err: unknown) {
            fail(err)
        } finally {
            setBusy(false)
        }
    }

    const candidates = analysis?.extraction ?? pending
    // 候補が入れ替わったら（別の資料・新しい分析・反映の完了）前の失敗理由は消す。
    useEffect(() => setApproveError(''), [candidates])

    /*
     * いまこの画面で次に触る要素（上部の工程ガイドに出す）。
     *
     * 画面の一時状態（選択中の資料・プレビュー・同意・候補）で決まるため、
     * バックエンドの工程ガイドではなく画面側が出す。**押す要素の名前をそのまま**書く。
     */
    const hint = (() => {
        if (!canEdit) {
            return ''
        }
        if (candidates) {
            return '候補ごとに「承認」か「破棄」を選び、右下の「承認した候補を反映する」を押します。'
        }
        if (!selected) {
            return list.length === 0
                ? '左の「ファイルを選ぶ」か「フォルダを選ぶ（一括）」で、資料を取り込みます。'
                : '左の「取り込み済み」から、分析したい資料を選びます。'
        }
        if (content?.extractionFailed) {
            return 'この資料はテキストを取り出せていません。左の一覧から別の資料を選びます。'
        }
        if (!preview) {
            return '右下の「送信内容を確認する」を押して、AI へ送る内容を確かめます。'
        }
        if (preview.tooLarge) {
            return 'この資料は送信内容が大きすぎます。資料を分けて取り込み直します。'
        }
        if (!consent) {
            return '中央の「上の内容を AI プロバイダへ送信することに同意します」にチェックを入れます。'
        }
        return '右下の「この内容で分析する」を押します。'
    })()
    useGuideHint(hint)

    /** 承認した候補だけを渡す（破棄した候補はいかなる記録にもならない）。 */
    const approve = async (merges: dialogue.MergeResolution[] = []) => {
        if (!candidates) {
            return
        }
        setBusy(true)
        setMessage('')
        setApproveError('')
        try {
            const decisions = (candidates.decisions ?? [])
                .map((c, i) => ({c, key: `decision-${i}`}))
                .filter(({key}) => marks[key] === 'approved')
                .map(({c, key}) => ({candidate: {...c, body: edits[key] ?? c.body}}))
            const openIssues = (candidates.open_issues ?? [])
                .map((c, i) => ({c, key: `issue-${i}`}))
                .filter(({key}) => marks[key] === 'approved')
                .map(({c, key}) => {
                    const value = owners[key] ?? {owner: c.owner ?? '', due: c.due ?? ''}
                    return {...c, topic: edits[key] ?? c.topic, owner: value.owner, due: value.due}
                })
            const requirementUpdates = (candidates.requirement_updates ?? [])
                .map((c, i) => ({c, key: `requirement-${i}`}))
                .filter(({key}) => marks[key] === 'approved')
                .map(({c, key}) => ({
                    candidate: {...c, body_after: edits[key] ?? c.body_after},
                }))
            const termCandidates = (candidates.term_candidates ?? []).filter(
                (_, i) => marks[`term-${i}`] === 'approved',
            )
            const approvedPerspectives = (candidates.perspective_candidates ?? [])
                .map((c, i) => ({c, key: `perspective-${i}`}))
                .filter(({key}) => marks[key] === 'approved')
                .map(({c, key}) => ({...c, name: edits[key] ?? c.name}))

            const outcome = await ApproveImportCandidates(selected, {
                decisions,
                openIssues,
                requirementUpdates,
                termCandidates,
                perspectives: approvedPerspectives,
                revertConfirmed: Object.keys(revertConfirmed).filter((id) => revertConfirmed[id]),
                merges,
            } as unknown as dialogue.FeedbackApproval)
            if (outcome.conflicts?.length) {
                // 反映は行われていない。三面を出して本人がマージのしかたを選ぶ（三面マージ）。
                setConflicts(outcome.conflicts)
                setConflictNotice(outcome.notice ?? '')
                setTone('warn')
                setMessage(outcome.notice ?? '他のメンバーの変更と競合しています。')
                return
            }
            setConflicts([])
            setAnalysis(null)
            setPending(null)
            setMarks({})
            setEdits({})
            setOwners({})
            await reloadList()
            await reloadPerspectives()
            setTone('accent')
            setMessage(approvedMessage(outcome.applied))
        } catch (err: unknown) {
            // 押したボタンのそばに出す（画面上端のバナーは右ペインから見えない）。
            setApproveError(`反映できませんでした。何も記録していません。\n${errorText(err)}`)
        } finally {
            setBusy(false)
        }
    }

    /** 根拠箇所を抽出テキストの該当行として示す（候補と同時に視認できる）。 */
    const showEvidence = async (ref: string) => {
        setHighlight(refLines(ref))
        try {
            setEvidence(await Evidence(ref))
        } catch {
            setEvidence(null)
        }
    }

    const selectedView = useMemo(() => list.find((v) => v.id === selected) ?? null, [list, selected])
    const isFeedback = selectedView?.kind === 'dev-ai-feedback'

    const extractedLines = useMemo(() => (content?.extracted ?? '').split('\n'), [content])

    // 一括取り込みの確認文に出す種別名（生の値は出さない）。
    const kindLabel = kinds.find((k) => k.id === kind)?.label ?? ''

    const versionsPane = (
        <>
            <h3 className="rw-imports__pane-title">取り込む</h3>
            <label className="rw-imports__field">
                <span>種別</span>
                <select value={kind} onChange={(e) => setKind(e.target.value)} aria-label="種別">
                    {kinds.map((k) => (
                        <option key={k.id} value={k.id}>
                            {k.label}
                        </option>
                    ))}
                </select>
            </label>
            <p className="rw-imports__hint">{kinds.find((k) => k.id === kind)?.summary ?? ''}</p>
            <Button
                onClick={() => void importFile()}
                disabledReason={editReason(busy ? '処理中です。' : undefined)}
            >
                ファイルを選ぶ
            </Button>
            <Button
                onClick={() => void chooseDirectory()}
                disabledReason={editReason(busy ? '処理中です。' : undefined)}
            >
                フォルダを選ぶ（一括）
            </Button>
            {scan ? (
                <section className="rw-imports__batch" aria-label="一括取り込みの確認">
                    <p className="rw-imports__batch-lead">
                        {`${formatCount(scan.count)} 件（${scan.totalSizeLabel}）を「${kindLabel}」として取り込みます。`}
                    </p>
                    <p className="rw-imports__hint">
                        サブフォルダも対象です。取り込むのは原本の登録までで、AI での分析は行いません。
                    </p>
                    {scan.notice ? <p className="rw-imports__hint">{scan.notice}</p> : null}
                    <ul className="rw-imports__batch-list">
                        {scan.files.map((f) => (
                            <li key={f.name}>{f.name}</li>
                        ))}
                    </ul>
                    {scan.count > scan.files.length ? (
                        <p className="rw-imports__hint">
                            {`ほか ${formatCount(scan.count - scan.files.length)} 件（一覧は先頭のみ表示しています）`}
                        </p>
                    ) : null}
                    {scan.skippedCount > 0 ? (
                        <>
                            <p className="rw-imports__batch-lead">
                                {`取り込まないもの ${formatCount(scan.skippedCount)} 件`}
                            </p>
                            <ul className="rw-imports__batch-list">
                                {scan.skipped.map((s) => (
                                    <li key={s.name}>{`${s.name} — ${s.reason}`}</li>
                                ))}
                            </ul>
                        </>
                    ) : null}
                    {scan.blocked ? <p className="rw-imports__batch-blocked">{scan.blocked}</p> : null}
                    <div className="rw-imports__batch-actions">
                        <Button
                            variant="primary"
                            onClick={() => void runDirectoryImport()}
                            disabledReason={editReason(
                                busy ? '処理中です。' : scan.blocked ? scan.blocked : undefined,
                            )}
                        >
                            この内容で取り込む
                        </Button>
                        <Button variant="quiet" onClick={() => setScan(null)}>
                            取りやめる
                        </Button>
                    </div>
                </section>
            ) : null}
            {batch && batch.failedCount > 0 ? (
                <section className="rw-imports__batch" aria-label="取り込めなかったファイル">
                    <p className="rw-imports__batch-lead">
                        {`取り込めなかったもの ${formatCount(batch.failedCount)} 件`}
                    </p>
                    <ul className="rw-imports__batch-list">
                        {batch.failed.map((f) => (
                            <li key={f.name}>{`${f.name} — ${f.reason}`}</li>
                        ))}
                    </ul>
                    <div className="rw-imports__batch-actions">
                        <Button variant="quiet" onClick={() => setBatch(null)}>
                            閉じる
                        </Button>
                    </div>
                </section>
            ) : null}
            <label className="rw-imports__field">
                <span>貼り付け</span>
                <textarea
                    value={paste}
                    onChange={(e) => setPaste(e.target.value)}
                    aria-label="貼り付ける本文"
                    rows={4}
                />
            </label>
            <Button
                onClick={() => void importPaste()}
                disabledReason={editReason(
                    busy ? '処理中です。' : paste.trim() ? undefined : '取り込む本文を貼り付けてください。',
                )}
            >
                貼り付けを取り込む
            </Button>

            <h3 className="rw-imports__pane-title">取り込み済み</h3>
            <ul className="rw-imports__list">
                {list.map((v) => (
                    <li key={v.id}>
                        {/*
                          カード全体を 1 つのボタンにする。資料名だけが押せると
                          どこを押せばよいか分からない。選択中の見た目は
                          一覧・管理系の画面の規定（surface 地 + accent の左ボーダー）に合わせ、
                          緑塗り（primary = 1 画面 1 つ）は使わない。
                        */}
                        <button
                            type="button"
                            className={`rw-imports__card${
                                v.id === selected ? ' rw-imports__card--selected' : ''
                            }`}
                            aria-pressed={v.id === selected}
                            onClick={() => void select(v.id)}
                        >
                            <span className="rw-imports__card-name">{v.sourceName}</span>
                            <span className="rw-imports__meta">
                                <span className="rw-mono">{v.id}</span> {v.kindLabel} ／ {v.importedAt}
                            </span>
                            <span className="rw-imports__meta">
                                <Chip tone={v.extracted ? 'info' : 'warn'}>{v.statusLabel}</Chip>
                                {v.classificationLabel ? <Chip tone="info">{v.classificationLabel}</Chip> : null}
                                {v.analysisLabel ? <Chip tone="accent">{v.analysisLabel}</Chip> : null}
                            </span>
                        </button>
                    </li>
                ))}
                {list.length === 0 ? (
                    <li>
                        <EmptyState
                            message="まだ取り込んでいません。"
                            next="上の「ファイルを選ぶ」「フォルダを選ぶ（一括）」か、貼り付けで資料を登録します。"
                        />
                    </li>
                ) : null}
            </ul>
        </>
    )

    const documentPane = (
        <>
            {content === null ? (
                <EmptyState
                    message="表示する資料を選んでいません。"
                    next="左の一覧から資料を選ぶと、抽出テキストと原本を確認できます。"
                />
            ) : (
                <>
                    <h3 className="rw-imports__pane-title">
                        抽出テキスト <span className="rw-mono">{content.id}</span>
                    </h3>
                    {content.notice ? (
                        <p className="rw-imports__notice" role="alert">
                            {content.notice}
                        </p>
                    ) : null}
                    <p className="rw-imports__meta">
                        原本: <span className="rw-mono">{content.sourcePath}</span>（{content.sourceName}）
                    </p>
                    {content.extractionFailed ? (
                        content.sourceText ? (
                            <pre className="rw-imports__source">{content.sourceText}</pre>
                        ) : (
                            <p className="rw-imports__meta">
                                この形式の原本は画面に表示できません。上の場所に保持しています。
                            </p>
                        )
                    ) : (
                        <ol className="rw-imports__text" aria-label="抽出テキスト">
                            {extractedLines.map((line, i) => {
                                const n = i + 1
                                const on = highlight && n >= highlight.from && n <= highlight.to
                                return (
                                    <li
                                        key={n}
                                        className={on ? 'rw-imports__line rw-imports__line--on' : 'rw-imports__line'}
                                        data-line={n}
                                    >
                                        {line}
                                    </li>
                                )
                            })}
                        </ol>
                    )}

                    <h3 className="rw-imports__pane-title" ref={previewRef} tabIndex={-1}>
                        送信前プレビュー
                    </h3>
                    {preview === null ? (
                        <p className="rw-imports__meta">
                            分析する前に、送信する内容そのものを確認できます。
                        </p>
                    ) : preview.tooLarge ? (
                        <p className="rw-imports__notice" role="alert">
                            {preview.notice}
                        </p>
                    ) : (
                        <>
                            <p className="rw-imports__meta">
                                分割 {preview.chunkCount} 件 ／ 概算 {preview.estimatedTokens} トークン
                            </p>
                            <details className="rw-imports__preview">
                                <summary>送信するシステムプロンプト</summary>
                                <pre className="rw-imports__pre">{preview.system}</pre>
                            </details>
                            <details className="rw-imports__preview">
                                <summary>送信する注入文脈</summary>
                                <pre className="rw-imports__pre">{preview.context}</pre>
                            </details>
                            {preview.chunks.map((c) => (
                                <details key={c.index} className="rw-imports__preview">
                                    <summary>
                                        送信する本文 {c.index}/{preview.chunkCount}（L{c.startLine}-L{c.endLine}）
                                    </summary>
                                    <pre className="rw-imports__pre">{c.text}</pre>
                                </details>
                            ))}
                            <label className="rw-imports__consent">
                                <input
                                    type="checkbox"
                                    checked={consent}
                                    onChange={(e) => setConsent(e.target.checked)}
                                    disabled={!canEdit}
                                />
                                上の内容を AI プロバイダへ送信することに同意します
                            </label>
                        </>
                    )}
                </>
            )}
        </>
    )

    const checksPane = (
        <>
            {conflicts.length > 0 ? (
                <MergePanel
                    conflicts={conflicts}
                    notice={conflictNotice}
                    proposals={candidateProposals(candidates, edits)}
                    busy={busy}
                    onApprove={(merges) => void approve(merges)}
                    onDefer={() => {
                        setConflicts([])
                        setTone('info')
                        setMessage('反映は保留しました。候補は残っています。未決事項として起票することもできます。')
                    }}
                />
            ) : null}

            <h3 className="rw-imports__pane-title">候補</h3>
            {candidates === null ? (
                <NextSteps analyzable={Boolean(selected) && !content?.extractionFailed} />
            ) : (
                <CandidatePanel
                    candidates={candidates}
                    marks={marks}
                    edits={edits}
                    revertConfirmed={revertConfirmed}
                    revertTargets={analysis?.revertTargets ?? []}
                    canEdit={canEdit}
                    denyReason={denyReason}
                    onMark={(key, mark) => setMarks((prev) => ({...prev, [key]: mark}))}
                    onEdit={(key, value) => setEdits((prev) => ({...prev, [key]: value}))}
                    onRevertConfirm={(id, on) => setRevertConfirmed((prev) => ({...prev, [id]: on}))}
                    onEvidence={(ref) => void showEvidence(ref)}
                    owners={owners}
                    onOwner={(key, value) => setOwners((prev) => ({...prev, [key]: value}))}
                />
            )}
            {evidence && !evidence.found ? (
                <p className="rw-imports__notice" role="alert">
                    根拠の該当箇所が見つかりません（参照欠落）。
                </p>
            ) : null}

            {isFeedback ? (
                <ClassificationPanel
                    importID={selected}
                    current={selectedView?.classification ?? ''}
                    options={classifications}
                    canEdit={canEdit}
                    denyReason={denyReason}
                    onChanged={() => void reloadList()}
                    onError={fail}
                />
            ) : null}

            <PerspectivePanel
                perspectives={perspectives}
                canEdit={canEdit}
                denyReason={denyReason}
                onChanged={() => void reloadPerspectives()}
                onError={fail}
            />
        </>
    )

    const primaryAction = candidates
        ? {
              label: '承認した候補を反映する',
              onClick: () => void approve(),
              disabledReason: editReason(busy ? '処理中です。' : undefined),
          }
        : preview && !preview.tooLarge
          ? {
                label: 'この内容で分析する',
                onClick: () => void analyze(),
                disabledReason: editReason(
                    busy ? '処理中です。' : consent ? undefined : '送信内容を確認して同意してください。',
                ),
            }
          : {
                label: '送信内容を確認する',
                onClick: () => void buildPreview(),
                disabledReason: editReason(
                    busy
                        ? '処理中です。'
                        : selected
                          ? content?.extractionFailed
                              ? 'この資料はテキストを取り出せていないため分析できません。'
                              : undefined
                          : '資料を選んでください。',
                ),
            }

    return (
        <section className="rw-imports" aria-label="取り込み">
            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')} />
            ) : null}
            {permission && !canEdit ? (
                <p className="rw-imports__notice" role="status">
                    {denyReason}
                </p>
            ) : null}
            <DocumentWorkspace
                versionsPane={versionsPane}
                documentPane={documentPane}
                checksPane={checksPane}
                primaryAction={primaryAction}
                actionError={approveError}
                cancelAction={{label: '対話へ戻る', onClick: onBack}}
            />
        </section>
    )
}

/** 競合パネルへ渡す「自分の反映案」（対象 ID → 反映しようとした本文）。 */
function candidateProposals(
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

/**
 * 承認待ちの候補が無いときに、右ペインへ**次の一手**を出す（押せる要素が無い画面を作らない）。
 *
 * 反映を終えた直後はこの画面ですることが無くなり、押せる要素も見当たらないため
 * 「ここから何をどう進めたらいいのか分からない」状態になっていた（利用者からの報告）。
 */
function NextSteps({analyzable}: {analyzable: boolean}) {
    return (
        <div className="rw-imports__next">
            <p className="rw-imports__meta">いま承認待ちの候補はありません。次のどちらかに進めます。</p>
            <ul className="rw-imports__next-list">
                <li>
                    <b>この資料を分析する</b>:{' '}
                    {analyzable
                        ? '「送信内容を確認する」→ 内容に同意 →「この内容で分析する」の順に押します。'
                        : '左の一覧で資料を選ぶと「送信内容を確認する」が押せるようになります。'}
                </li>
                <li>
                    <b>質問を進める</b>: 「対話へ戻る」を押し、対話画面で「次の質問」を押します。
                    ここで登録したプロジェクト観点が、その質問に加わります。
                </li>
            </ul>
        </div>
    )
}

/** 反映結果の 1 文（何がいくつ記録されたか）。 */
function approvedMessage(result: binding.ApprovalApplied | undefined): string {
    if (!result) {
        return '承認した候補はありませんでした。'
    }
    const parts: string[] = []
    const counts: Array<[string, number]> = [
        ['決定事項', result.decisionIds?.length ?? 0],
        ['未決事項', result.openIssueIds?.length ?? 0],
        ['要件項目', result.requirementIds?.length ?? 0],
        ['用語', result.termNames?.length ?? 0],
        ['プロジェクト観点', result.perspectiveIds?.length ?? 0],
    ]
    counts.forEach(([label, n]) => {
        if (n > 0) {
            parts.push(`${label} ${n} 件`)
        }
    })
    if (result.revertedRequirementIds?.length) {
        parts.push(`差し戻し ${result.revertedRequirementIds.length} 件`)
    }
    if (parts.length === 0) {
        return '承認した候補はありませんでした。'
    }
    const applied = `${parts.join(' / ')}を反映しました。`
    // 反映で終わりに見えないよう、次に押す操作をそのまま添える。
    return (result.perspectiveIds?.length ?? 0) > 0
        ? `${applied}「対話へ戻る」を押すと、登録した観点を含む質問を作れます。`
        : applied
}

/** 候補の承認・編集・破棄。画面遷移を起こさない。 */
function CandidatePanel({
    candidates,
    marks,
    edits,
    revertConfirmed,
    revertTargets,
    canEdit,
    denyReason,
    onMark,
    onEdit,
    onRevertConfirm,
    onEvidence,
    owners,
    onOwner,
}: {
    candidates: dialogue.Extraction
    marks: Record<string, Mark>
    edits: Record<string, string>
    revertConfirmed: Record<string, boolean>
    revertTargets: string[]
    canEdit: boolean
    denyReason: string
    onMark: (key: string, mark: Mark) => void
    onEdit: (key: string, value: string) => void
    onRevertConfirm: (id: string, on: boolean) => void
    onEvidence: (ref: string) => void
    owners: Record<string, {owner: string; due: string}>
    onOwner: (key: string, value: {owner: string; due: string}) => void
}) {
    // 候補の本文は**既定で読むだけ**にする（入力欄のままだと枠とスクロールで読みにくい）。
    // 入力欄のまま並べると、承認可否を判断するときに読みづらい（枠・スクロール・折り返しの制限）。
    // 直したいときだけ「編集」で入力欄へ切り替える。編集した内容は承認時にそのまま使う。
    const [editing, setEditing] = useState<Record<string, boolean>>({})

    /** 候補の本文。読むだけの表示と入力欄を切り替える（`line` は 1 行ものの候補）。 */
    const body = (key: string, value: string, label: string, kind: 'text' | 'line' = 'text') => {
        if (!editing[key]) {
            return (
                <p className="rw-imports__candidate-body" aria-label={label}>
                    {value}
                </p>
            )
        }
        return kind === 'line' ? (
            <input
                value={value}
                onChange={(e) => onEdit(key, e.target.value)}
                aria-label={label}
                disabled={!canEdit}
            />
        ) : (
            <textarea
                value={value}
                onChange={(e) => onEdit(key, e.target.value)}
                aria-label={label}
                disabled={!canEdit}
            />
        )
    }

    // 押せるものは枠を持たせる。承認だけが選択時に緑の塗りになり、
    // 未選択の操作も枠線で「押せる」と分かる（枠なしの文字ボタンは取消系だけに使う）。
    const markButtons = (key: string, editable = false, approveDisabledReason?: string) => (
        <span className="rw-imports__marks">
            <Button
                variant={marks[key] === 'approved' ? 'primary' : 'secondary'}
                onClick={() => onMark(key, 'approved')}
                disabledReason={canEdit ? approveDisabledReason : denyReason}
            >
                承認
            </Button>
            <Button
                variant="secondary"
                onClick={() => onMark(key, 'discarded')}
                disabledReason={canEdit ? undefined : denyReason}
            >
                破棄
            </Button>
            {editable ? (
                <Button
                    variant="secondary"
                    onClick={() => setEditing((e) => ({...e, [key]: !e[key]}))}
                    disabledReason={canEdit ? undefined : denyReason}
                >
                    {editing[key] ? '編集をやめる' : '編集'}
                </Button>
            ) : null}
        </span>
    )

    const evidenceLinks = (refs: string[] | undefined) => (
        <span className="rw-imports__evidence">
            {(refs ?? []).map((ref) => (
                <Button key={ref} variant="secondary" onClick={() => onEvidence(ref)}>
                    {ref}
                </Button>
            ))}
        </span>
    )

    /*
     * 根拠を特定できない候補は、何が問題で次に何をするかを文で示す（対話画面の抽出候補パネルと同じ規則）。
     * 決定事項・未決事項・質問観点は根拠が無いと記録できないため承認を選べなくする。
     */
    const evidence = (c: {evidence_refs?: string[]; missing_evidence?: boolean}, recordable: boolean) => (
        <CandidateEvidence candidate={c} source="資料の該当箇所" recordable={recordable}>
            {evidenceLinks(c.evidence_refs)}
        </CandidateEvidence>
    )
    const blocked = (c: {evidence_refs?: string[]; missing_evidence?: boolean}) =>
        lacksEvidence(c) ? noEvidenceReason('資料の該当箇所') : undefined

    return (
        <>
            {(candidates.decisions ?? []).map((c, i) => {
                const key = `decision-${i}`
                return (
                    <article key={key} className="rw-imports__candidate">
                        <p className="rw-imports__label">決定事項の候補 ／ 論点 {c.topic_key}</p>
                        {body(key, edits[key] ?? c.body, `決定事項の候補 ${i + 1}`)}
                        {c.duplicate_of ? <Chip tone="warn">{c.duplicate_of} と重複の可能性</Chip> : null}
                        {evidence(c, false)}
                        {markButtons(key, true, blocked(c))}
                    </article>
                )
            })}

            {(candidates.open_issues ?? []).map((c, i) => {
                const key = `issue-${i}`
                const value = owners[key] ?? {owner: c.owner ?? '', due: c.due ?? ''}
                return (
                    <article key={key} className="rw-imports__candidate">
                        <p className="rw-imports__label">未決事項の候補</p>
                        {body(key, edits[key] ?? c.topic, `未決事項の候補 ${i + 1}`)}
                        {/* 未決事項の承認には「誰が・いつまでに」が必須。空のままでは記録できない */}
                        <label className="rw-imports__field">
                            <span>決める人</span>
                            <input
                                value={value.owner}
                                onChange={(e) => onOwner(key, {...value, owner: e.target.value})}
                                aria-label={`未決事項の候補 ${i + 1} の決める人`}
                                disabled={!canEdit}
                            />
                        </label>
                        <label className="rw-imports__field">
                            <span>期限</span>
                            <input
                                value={value.due}
                                placeholder="YYYY-MM-DD"
                                onChange={(e) => onOwner(key, {...value, due: e.target.value})}
                                aria-label={`未決事項の候補 ${i + 1} の期限`}
                                disabled={!canEdit}
                            />
                        </label>
                        {c.duplicate_of ? <Chip tone="warn">{c.duplicate_of} と重複の可能性</Chip> : null}
                        {evidence(c, false)}
                        {markButtons(key, true, blocked(c))}
                    </article>
                )
            })}

            {(candidates.requirement_updates ?? []).map((c, i) => {
                const key = `requirement-${i}`
                const targetID = c.target_id ?? ''
                const update = c.operation === 'update' && targetID !== ''
                return (
                    <article key={key} className="rw-imports__candidate">
                        <p className="rw-imports__label">
                            要件項目の候補 ／{' '}
                            {update ? `${targetID} の変更` : `新規（${c.kind === 'non-functional' ? 'NFR' : 'FR'}-${c.id_group || '（自動）'}-nnn）`}{' '}
                            ／ {c.title}
                        </p>
                        {body(key, edits[key] ?? c.body_after, `要件項目の候補 ${i + 1}`)}
                        {/* ID は記録するときに ReqWeave が付ける。利用者には入力させない */}
                        {c.duplicate_of ? <Chip tone="warn">{c.duplicate_of} と重複の可能性</Chip> : null}
                        {(c.related_ids ?? []).length > 0 ? (
                            <p className="rw-imports__meta">
                                関連 ID: <span className="rw-mono">{(c.related_ids ?? []).join(' ')}</span>
                            </p>
                        ) : null}
                        {update && revertTargets.includes(targetID) ? (
                            <label className="rw-imports__consent">
                                <input
                                    type="checkbox"
                                    checked={Boolean(revertConfirmed[targetID])}
                                    onChange={(e) => onRevertConfirm(targetID, e.target.checked)}
                                    disabled={!canEdit}
                                />
                                {targetID} の変更影響を確認しました（合意済み要件を差し戻します）
                            </label>
                        ) : null}
                        {evidence(c, true)}
                        {markButtons(key, true)}
                    </article>
                )
            })}

            {(candidates.term_candidates ?? []).map((c, i) => {
                const key = `term-${i}`
                return (
                    <article key={key} className="rw-imports__candidate">
                        <p className="rw-imports__label">用語の候補 ／ {c.term}（{c.english}）</p>
                        <p className="rw-imports__meta">{c.definition}</p>
                        {evidenceLinks(c.evidence_refs)}
                        {markButtons(key)}
                    </article>
                )
            })}

            {(candidates.perspective_candidates ?? []).map((c, i) => {
                const key = `perspective-${i}`
                return (
                    <article key={key} className="rw-imports__candidate">
                        <p className="rw-imports__label">質問観点の候補</p>
                        {body(key, edits[key] ?? c.name, `質問観点の候補 ${i + 1}`, 'line')}
                        <p className="rw-imports__meta">{c.summary}</p>
                        {c.duplicate_of ? <Chip tone="warn">{c.duplicate_of} と重複の可能性</Chip> : null}
                        {evidence(c, false)}
                        {markButtons(key, true, blocked(c))}
                    </article>
                )
            })}

            {(candidates.contradictions ?? []).map((c, i) => (
                <article key={`contradiction-${i}`} className="rw-imports__candidate">
                    <p className="rw-imports__label">
                        <Chip tone="danger">矛盾の指摘</Chip> {c.with_decision_id}
                    </p>
                    <p className="rw-imports__meta">{c.description}</p>
                    {evidenceLinks(c.evidence_refs)}
                </article>
            ))}
        </>
    )
}

/** 開発AIフィードバックの分類付与。AI は分類しない（担当者が付ける）。 */
function ClassificationPanel({
    importID,
    current,
    options,
    canEdit,
    denyReason,
    onChanged,
    onError,
}: {
    importID: string
    current: string
    options: binding.FeedbackClassificationOption[]
    canEdit: boolean
    denyReason: string
    onChanged: () => void
    onError: (err: unknown) => void
}) {
    const [value, setValue] = useState(current)
    useEffect(() => setValue(current), [current, importID])

    const apply = async (next: string) => {
        setValue(next)
        try {
            await SetFeedbackClassification(importID, next)
            onChanged()
        } catch (err: unknown) {
            onError(err)
        }
    }

    return (
        <>
            <h3 className="rw-imports__pane-title">フィードバックの分類</h3>
            <label className="rw-imports__field">
                <span>分類</span>
                <select
                    value={value}
                    onChange={(e) => void apply(e.target.value)}
                    aria-label="フィードバックの分類"
                    disabled={!canEdit}
                >
                    <option value="">未分類</option>
                    {options.map((o) => (
                        <option key={o.value} value={o.value}>
                            {o.label}
                        </option>
                    ))}
                </select>
            </label>
        </>
    )
}

/** プロジェクト観点の一覧・手動追加・編集・削除（AI 呼び出しを伴わない）。 */
function PerspectivePanel({
    perspectives,
    canEdit,
    denyReason,
    onChanged,
    onError,
}: {
    perspectives: binding.PerspectiveView[]
    canEdit: boolean
    denyReason: string
    onChanged: () => void
    onError: (err: unknown) => void
}) {
    const [name, setName] = useState('')
    const [summary, setSummary] = useState('')
    const [editing, setEditing] = useState<string>('')

    const add = async () => {
        try {
            await AddPerspective({id: '', name, summary} as binding.PerspectiveRequest)
            setName('')
            setSummary('')
            onChanged()
        } catch (err: unknown) {
            onError(err)
        }
    }

    const update = async (id: string, nextName: string, nextSummary: string) => {
        try {
            await UpdatePerspective({id, name: nextName, summary: nextSummary} as binding.PerspectiveRequest)
            setEditing('')
            onChanged()
        } catch (err: unknown) {
            onError(err)
        }
    }

    const remove = async (id: string) => {
        try {
            await RemovePerspective(id)
            onChanged()
        } catch (err: unknown) {
            onError(err)
        }
    }

    return (
        <>
            <h3 className="rw-imports__pane-title">プロジェクト観点</h3>
            {/* 進んだのに完成度が 0% のままで手掛かりを失う状態を作らない（観点は完成度の分母に数えない） */}
            <p className="rw-imports__meta">
                ここに登録した観点は、対話画面で作る質問に加わります。完成度の割合には数えません。
            </p>
            <ul className="rw-imports__perspectives">
                {perspectives.map((p) => (
                    <li key={p.id}>
                        {editing === p.id ? (
                            <PerspectiveEditor
                                view={p}
                                onCancel={() => setEditing('')}
                                onSave={(n, s) => void update(p.id, n, s)}
                            />
                        ) : (
                            <>
                                <p className="rw-imports__label">
                                    <span className="rw-mono">{p.id}</span> {p.name}
                                </p>
                                <p className="rw-imports__meta">{p.summary}</p>
                                {/* 由来は「状態」ではないためチップで描かない。
                                    塗りつぶしのチップは押せる操作と誤認される。 */}
                                <p className="rw-imports__meta">
                                    {p.originLabel}
                                    {p.evidence ? <span className="rw-mono"> {p.evidence}</span> : null}
                                </p>
                                <span className="rw-imports__marks">
                                    <Button
                                        variant="secondary"
                                        onClick={() => setEditing(p.id)}
                                        disabledReason={canEdit ? undefined : denyReason}
                                    >
                                        編集
                                    </Button>
                                    <Button
                                        variant="secondary"
                                        onClick={() => void remove(p.id)}
                                        disabledReason={canEdit ? undefined : denyReason}
                                    >
                                        削除
                                    </Button>
                                </span>
                            </>
                        )}
                    </li>
                ))}
                {perspectives.length === 0 ? (
                    <li>
                        <EmptyState
                            message="プロジェクト観点はまだありません。"
                            next="資料を分析して質問観点候補を承認するか、下の「観点を追加する」で登録します。"
                        />
                    </li>
                ) : null}
            </ul>
            <label className="rw-imports__field">
                <span>観点名</span>
                <input value={name} onChange={(e) => setName(e.target.value)} aria-label="観点名" disabled={!canEdit} />
            </label>
            <label className="rw-imports__field">
                <span>要旨</span>
                <input
                    value={summary}
                    onChange={(e) => setSummary(e.target.value)}
                    aria-label="観点の要旨"
                    disabled={!canEdit}
                />
            </label>
            <Button
                onClick={() => void add()}
                disabledReason={
                    canEdit
                        ? name.trim() && summary.trim()
                            ? undefined
                            : '観点名と要旨を入力してください。'
                        : denyReason
                }
            >
                観点を追加する
            </Button>
        </>
    )
}

/** 観点 1 件の編集フォーム。 */
function PerspectiveEditor({
    view,
    onCancel,
    onSave,
}: {
    view: binding.PerspectiveView
    onCancel: () => void
    onSave: (name: string, summary: string) => void
}) {
    const [name, setName] = useState(view.name)
    const [summary, setSummary] = useState(view.summary)
    return (
        <>
            <label className="rw-imports__field">
                <span>観点名</span>
                <input
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    aria-label={`${view.id} の観点名`}
                />
            </label>
            <label className="rw-imports__field">
                <span>要旨</span>
                <input
                    value={summary}
                    onChange={(e) => setSummary(e.target.value)}
                    aria-label={`${view.id} の要旨`}
                />
            </label>
            <span className="rw-imports__marks">
                <Button variant="secondary" onClick={() => onSave(name, summary)}>
                    保存
                </Button>
                <Button variant="quiet" onClick={onCancel}>
                    取消
                </Button>
            </span>
        </>
    )
}
