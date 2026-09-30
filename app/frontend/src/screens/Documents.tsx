import {useCallback, useEffect, useMemo, useState} from 'react'
import {
    CheckDocumentReservation,
    ConfirmCheck,
    ConfirmDocument,
    DocumentChapters,
    DocumentDiff,
    DocumentVersions,
    GenerateDocument,
    MoveToBasicDesign,
    VerifyDocument,
} from '../../wailsjs/go/binding/API'
import {binding, docgen} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Banner, Button, Chip, DocumentWorkspace, EmptyState, Markdown, useGuideHint, WorkStartDialog, errorText} from '../ui'
import './Documents.css'

/**
 * 成果物プレビュー・版差分。
 *
 * Markdown+Mermaid のレンダリング、版一覧（確定版とドラフト）、任意 2 版間の差分、
 * 確定前チェックと確定操作、基本設計フェーズへの移行。
 *
 * 生成・差分再生成・確定は**着手時に予約の状態を確認し、進め方（排他 / 並行）を選んでから始める**
 * （共同作業で同じ文書を同時に作り替えないように）。予約の記録と完了時の解除はバインディングが行う。
 */

type DocumentEvent = {
    kind: 'chapter-start' | 'chapter-done' | 'done' | 'error'
    chapter?: string
    title?: string
    index?: number
    total?: number
    reused?: boolean
    errors?: number
    warnings?: number
    errorClass?: string
    /** 利用者向けの 1 文（原因＋次の行動）。文言の正本はバックエンドのエラーカタログ。 */
    userMessage?: string
    message?: string
}

const KIND_LABELS = {
    requirements: '要件定義書',
    'basic-design': '基本設計書',
} as const

type DocKind = keyof typeof KIND_LABELS

/** 検証項目の表示名。生のコード値だけを出さない。 */
const CHECK_LABELS = {
    V1: 'ID 参照切れ',
    V2: '参照欠落',
    V3: 'ブロックする未決事項',
    V4: '用語不一致',
    V5: '曖昧語',
    V6: '受け入れ条件の欠落',
} as const

function checkLabel(check: string): string {
    return check in CHECK_LABELS ? `${check} ${CHECK_LABELS[check as keyof typeof CHECK_LABELS]}` : check
}

/** 予約の対象となる操作。表示名は 1 か所で持つ。 */
const WORK_LABELS = {
    generate: '成果物の生成',
    regenerate: '成果物の差分再生成',
    confirm: '成果物の確定',
} as const

type PendingWork = {
    operation: keyof typeof WORK_LABELS
    check: binding.ReservationCheckView | null
}

export function Documents({
    kind,
    onBack,
    onExport,
}: {
    kind: DocKind
    onBack: () => void
    onExport?: () => void
}) {
    const [versions, setVersions] = useState<binding.DocumentVersionView[]>([])
    const [selectedVersion, setSelectedVersion] = useState(0)
    const [chapters, setChapters] = useState<binding.DocumentChapterView[]>([])
    const [selectedFile, setSelectedFile] = useState('')
    const [check, setCheck] = useState<binding.ConfirmCheckView | null>(null)
    const [verification, setVerification] = useState<docgen.VerifyResult | null>(null)
    const [diffTarget, setDiffTarget] = useState<number | null>(null)
    const [diffs, setDiffs] = useState<docgen.ChapterDiff[]>([])
    const [progress, setProgress] = useState('')
    // pending は着手前の進め方の選択待ち（予約の選択ダイアログ）。
    const [pending, setPending] = useState<PendingWork | null>(null)
    const [busy, setBusy] = useState(false)
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')

    const reload = useCallback(async () => {
        try {
            const list = await DocumentVersions(kind)
            setVersions(list)
            const body = await DocumentChapters(kind, selectedVersion)
            setChapters(body)
            setSelectedFile((prev) => (body.some((c) => c.fileName === prev) ? prev : body[0]?.fileName ?? ''))
            setCheck(await ConfirmCheck(kind))
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [kind, selectedVersion])

    useEffect(() => {
        void reload()
    }, [reload])

    // 生成の進行（章ごと）を受け取る。
    useEffect(() => {
        const off = EventsOn('document:event', (ev: DocumentEvent) => {
            switch (ev.kind) {
                case 'chapter-start':
                    setProgress(`${ev.index}/${ev.total} ${ev.title ?? ''} を生成中…`)
                    break
                case 'chapter-done':
                    setProgress(
                        ev.reused
                            ? `${ev.index}/${ev.total} ${ev.title ?? ''} は変更がないため再利用しました`
                            : `${ev.index}/${ev.total} ${ev.title ?? ''} を生成しました`,
                    )
                    break
                case 'done':
                    setProgress('')
                    setBusy(false)
                    setTone('accent')
                    setMessage(
                        ev.errors || ev.warnings
                            ? `生成しました（検証: エラー ${ev.errors ?? 0} 件 / 警告 ${ev.warnings ?? 0} 件）。`
                            : '生成しました（検証合格）。',
                    )
                    void reload()
                    break
                case 'error':
                    setProgress('')
                    setBusy(false)
                    setTone('danger')
                    setMessage(generationError(ev))
                    break
                default:
                    break
            }
        })
        return () => off()
    }, [reload])

    const generate = useCallback(
        async (differential: boolean, work: binding.WorkStart) => {
            setBusy(true)
            setMessage('')
            setProgress('生成を開始しています…')
            try {
                await GenerateDocument(kind, differential, work)
            } catch (err: unknown) {
                setBusy(false)
                setProgress('')
                setTone('danger')
                setMessage(errorText(err))
            }
        },
        [kind],
    )

    /**
     * 着手前の予約の確認。他のメンバーの排他予約があれば警告文を受け取り、
     * 進め方の選択ダイアログへ渡す。確認に失敗しても着手経路は塞がない（判定はバインディングが行う）。
     */
    const askWorkStart = useCallback(async (op: PendingWork['operation']) => {
        let check: binding.ReservationCheckView | null = null
        try {
            check = await CheckDocumentReservation(kind)
        } catch {
            check = null
        }
        setPending({operation: op, check})
    }, [kind])

    const verify = useCallback(async () => {
        try {
            setVerification(await VerifyDocument(kind, selectedVersion))
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [kind, selectedVersion])

    const runConfirm = useCallback(async (work: binding.WorkStart) => {
        try {
            const result = await ConfirmDocument(kind, work)
            setTone('accent')
            setMessage(
                `確定版 v${result.version} を保存しました。` +
                    (result.canMoveToBasicDesign ? '基本設計フェーズへ進めます。' : ''),
            )
            await reload()
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [kind, reload])

    const moveToDesign = useCallback(async () => {
        try {
            await MoveToBasicDesign()
            setTone('accent')
            setMessage('基本設計フェーズへ移行しました。対話画面から基本設計を進められます。')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const showDiff = useCallback(
        async (from: number) => {
            try {
                setDiffTarget(from)
                setDiffs(await DocumentDiff(kind, from, selectedVersion))
            } catch (err: unknown) {
                setTone('danger')
                setMessage(errorText(err))
            }
        },
        [kind, selectedVersion],
    )

    const current = useMemo(
        () => chapters.find((c) => c.fileName === selectedFile) ?? null,
        [chapters, selectedFile],
    )

    const versionsPane = (
        <>
            <h3 className="rw-documents__pane-title">版</h3>
            <ul className="rw-documents__versions">
                {versions.map((v) => (
                    <li key={v.version}>
                        <Button
                            variant={v.version === selectedVersion ? 'primary' : 'secondary'}
                            onClick={() => setSelectedVersion(v.version)}
                            disabledReason={v.hasContent ? undefined : 'まだ生成されていません。'}
                        >
                            {v.label}
                        </Button>
                        {v.version !== selectedVersion && v.hasContent ? (
                            <Button variant="secondary" onClick={() => void showDiff(v.version)}>
                                差分
                            </Button>
                        ) : null}
                    </li>
                ))}
            </ul>
            <h3 className="rw-documents__pane-title">文書</h3>
            <ul className="rw-documents__chapters">
                {chapters.map((c) => (
                    <li key={c.fileName}>
                        <Button
                            variant={c.fileName === selectedFile ? 'primary' : 'secondary'}
                            onClick={() => setSelectedFile(c.fileName)}
                        >
                            {c.title || c.fileName}
                        </Button>
                    </li>
                ))}
            </ul>
        </>
    )

    const documentPane = (
        <>
            {progress ? (
                <p className="rw-documents__progress" role="status">
                    {progress}
                </p>
            ) : null}
            {diffTarget !== null ? (
                <DiffView
                    diffs={diffs}
                    from={diffTarget}
                    to={selectedVersion}
                    onClose={() => setDiffTarget(null)}
                />
            ) : current ? (
                <Markdown source={current.body} />
            ) : (
                <EmptyState
                    message="成果物はまだ生成されていません。"
                    next="右の「生成する」で作成すると、ここに本文が表示されます。"
                />
            )}
        </>
    )

    const checksPane = (
        <>
            <h3 className="rw-documents__pane-title">確定前チェック</h3>
            {check ? (
                <ul className="rw-documents__checks">
                    <li>
                        未合意の要件項目: {check.draftRequirements?.length ?? 0} 件
                        {check.draftRequirements?.length ? (
                            <span className="rw-documents__ids rw-mono">{check.draftRequirements.join(', ')}</span>
                        ) : null}
                    </li>
                    <li>
                        ブロックする未決事項: {check.blockingIssues?.length ?? 0} 件
                        {check.blockingIssues?.length ? (
                            <span className="rw-documents__ids rw-mono">{check.blockingIssues.join(', ')}</span>
                        ) : null}
                    </li>
                    <li>成果物: {check.hasDraft ? 'ドラフトあり' : '未生成'}</li>
                    {/* 確定できない理由は「確定する」の直下に 1 か所だけ出す
                        （DocumentWorkspace が出す。同じ文をここへ二重に出さない） */}
                </ul>
            ) : null}

            <h3 className="rw-documents__pane-title">整合性検証</h3>
            <Button onClick={() => void verify()}>検証する</Button>
            {verification ? (
                verification.violations?.length ? (
                    <ul className="rw-documents__violations">
                        {verification.violations.map((v, i) => (
                            <li key={`${v.check}-${i}`}>
                                <Chip tone={v.severity === 'error' ? 'danger' : 'warn'}>
                                    {v.severity === 'error' ? 'エラー' : '警告'}
                                </Chip>{' '}
                                {checkLabel(v.check)}: {v.message}
                                {v.file ? <span className="rw-mono"> （{v.file}{v.line ? `:${v.line}` : ''}）</span> : null}
                            </li>
                        ))}
                    </ul>
                ) : (
                    <p className="rw-documents__ok">検証合格（違反はありません）。</p>
                )
            ) : null}
        </>
    )

    /*
     * いまこの画面で次に触る要素（上部の工程ガイドに出す）。
     * 生成前 → 生成、生成後 → 確定、確定できないときは理由の在り処を示す。
     */
    useGuideHint(
        current === null || !current.body
            ? '右の「生成する」を押して、要件定義書を作ります。'
            : check?.confirmable
              ? '本文を確認し、右下の「確定する」を押します。'
              : '右の「確定前チェック」に、確定できない理由が出ています。解消してから確定します。',
    )

    return (
        <section className="rw-documents" aria-label={KIND_LABELS[kind]}>
            <header className="rw-documents__head">
                <h2 className="rw-documents__title">{KIND_LABELS[kind]}</h2>
                <div className="rw-documents__actions">
                    <Button
                        onClick={() => void askWorkStart('generate')}
                        disabledReason={busy ? '生成中です。' : undefined}
                    >
                        生成する
                    </Button>
                    <Button
                        onClick={() => void askWorkStart('regenerate')}
                        disabledReason={busy ? '生成中です。' : undefined}
                    >
                        変更分だけ再生成
                    </Button>
                    {onExport ? <Button onClick={onExport}>エクスポート</Button> : null}
                    <Button onClick={onBack}>対話へ戻る</Button>
                </div>
            </header>
            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')} />
            ) : null}

            {pending ? (
                <WorkStartDialog
                    operation={WORK_LABELS[pending.operation]}
                    check={pending.check}
                    busy={busy}
                    onStart={(work) => {
                        const operation = pending.operation
                        setPending(null)
                        if (operation === 'confirm') {
                            void runConfirm(work)
                        } else {
                            void generate(operation === 'regenerate', work)
                        }
                    }}
                    onCancel={() => setPending(null)}
                />
            ) : null}

            <DocumentWorkspace
                versionsPane={versionsPane}
                documentPane={documentPane}
                checksPane={checksPane}
                primaryAction={{
                    label: '確定する',
                    onClick: () => void askWorkStart('confirm'),
                    disabledReason: check?.confirmable ? undefined : check?.reason || '確定できません。',
                }}
                secondaryActions={
                    kind === 'requirements'
                        ? [{label: '基本設計フェーズへ', onClick: () => void moveToDesign()}]
                        : []
                }
                cancelAction={{label: '対話へ戻る', onClick: onBack}}
            />
        </section>
    )
}

/** 版間の差分表示（変更前後を並べる）。 */
function DiffView({
    diffs,
    from,
    to,
    onClose,
}: {
    diffs: docgen.ChapterDiff[]
    from: number
    to: number
    onClose: () => void
}) {
    const label = (v: number) => (v === 0 ? 'ドラフト' : `確定版 v${v}`)
    const changed = diffs.filter((d) => d.status !== 'unchanged')
    return (
        <section className="rw-diff" aria-label="版差分">
            <header className="rw-diff__head">
                <h3 className="rw-documents__pane-title">
                    {label(from)} → {label(to)}（変更 {changed.length} 文書 / 全 {diffs.length} 文書）
                </h3>
                <Button onClick={onClose}>閉じる</Button>
            </header>
            {changed.length === 0 ? <p className="rw-documents__none">差分はありません。</p> : null}
            {changed.map((d) => (
                <article key={d.fileName} className="rw-diff__file">
                    <h4 className="rw-diff__name rw-mono">
                        {d.fileName}（+{d.added} / -{d.removed}）
                    </h4>
                    <pre className="rw-diff__lines">
                        {d.lines?.map((line, i) => (
                            <span key={i} className={`rw-diff__line rw-diff__line--${line.kind}`}>
                                {line.kind === 'add' ? '+ ' : line.kind === 'remove' ? '- ' : '  '}
                                {line.text}
                                {'\n'}
                            </span>
                        ))}
                    </pre>
                </article>
            ))}
        </section>
    )
}

/**
 * 生成エラーを「原因＋次の行動」の 1 文にする。
 *
 * **文言の正本はバックエンドのエラーカタログ**であり、届いた `userMessage` を
 * そのまま表示する。画面側にコード別の表を持たない（二重管理の禁止）。
 */
function generationError(ev: DocumentEvent): string {
    if (ev.userMessage) {
        return ev.userMessage
    }
    switch (ev.errorClass) {
        case 'transient':
            return '接続が不安定なため生成を完了できませんでした。通信を確認して、もう一度お試しください。既存の成果物は変更されていません。'
        case 'config':
            return 'AI プロバイダの設定に問題があります。設定画面でキーとモデルを確認してください。既存の成果物は変更されていません。'
        case 'permanent':
            return '生成した内容を保存できませんでした。保存先の空き容量と権限を確認してください。'
        default:
            return ev.message || '生成を完了できませんでした。もう一度お試しください。'
    }
}
