import {useCallback, useEffect, useState} from 'react'
import {
    ChooseFolder,
    DocumentVersions,
    ExportDocuments,
} from '../../wailsjs/go/binding/API'
import {binding, docgen} from '../../wailsjs/go/models'
import {Banner, Button, Chip, errorText} from '../ui'
import './Export.css'

/**
 * エクスポート。
 *
 * 対象（フェーズ・版）と出力先の指定、整合性検証結果の提示と「修正に戻る / 警告付きでエクスポート」の選択、
 * 出力ファイル一覧の表示。
 */

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

function versionLabel(version: number): string {
    return version === 0 ? 'ドラフト' : `確定版 v${version}`
}

export function Export({onBack}: {onBack: () => void}) {
    const [requirementVersions, setRequirementVersions] = useState<binding.DocumentVersionView[]>([])
    const [designVersions, setDesignVersions] = useState<binding.DocumentVersionView[]>([])
    const [requirements, setRequirements] = useState(0)
    const [basicDesign, setBasicDesign] = useState(0)
    const [includeDesign, setIncludeDesign] = useState(false)
    const [destination, setDestination] = useState('')
    const [result, setResult] = useState<docgen.ExportResult | null>(null)
    const [busy, setBusy] = useState(false)
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<'info' | 'accent' | 'warn' | 'danger'>('info')

    useEffect(() => {
        Promise.all([DocumentVersions('requirements'), DocumentVersions('basic-design')])
            .then(([req, design]) => {
                setRequirementVersions(req.filter((v) => v.hasContent))
                setDesignVersions(design.filter((v) => v.hasContent))
                const latestReq = req.find((v) => v.hasContent && v.version > 0) ?? req.find((v) => v.hasContent)
                if (latestReq) {
                    setRequirements(latestReq.version)
                }
            })
            .catch((err: unknown) => {
                setTone('danger')
                setMessage(errorText(err))
            })
    }, [])

    const chooseFolder = useCallback(async () => {
        try {
            const path = await ChooseFolder('エクスポート先を選ぶ')
            if (path) {
                setDestination(path)
            }
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const run = useCallback(
        async (acceptWarnings: boolean) => {
            setBusy(true)
            setMessage('')
            try {
                const got = await ExportDocuments(
                    binding.ExportRequestView.createFrom({
                        destination,
                        requirements,
                        basicDesign,
                        includeBasicDesign: includeDesign,
                        acceptWarnings,
                    }),
                )
                setResult(got)
                if (got.exported) {
                    setTone('accent')
                    setMessage(
                        got.verification.violations?.length
                            ? `警告付きで出力しました（${got.files.length} ファイル）。未解決事項は CLAUDE.md と export-report.md に記載しています。`
                            : `検証合格。${got.files.length} ファイルを出力しました。`,
                    )
                } else {
                    setTone('warn')
                    setMessage('整合性の違反があります。修正に戻るか、警告付きで出力するかを選んでください。')
                }
            } catch (err: unknown) {
                setTone('danger')
                setMessage(errorText(err))
            } finally {
                setBusy(false)
            }
        },
        [destination, requirements, basicDesign, includeDesign],
    )

    const violations = result?.verification.violations ?? []
    const canRun = destination.trim().length > 0 && !busy

    return (
        <section className="rw-export" aria-label="エクスポート">
            <header className="rw-export__head">
                <h2 className="rw-export__title">開発AIへのエクスポート</h2>
                <Button onClick={onBack}>成果物へ戻る</Button>
            </header>
            {message ? (
                <Banner tone={tone} title={message} onDismiss={() => setMessage('')} onDefer={() => setMessage('')} />
            ) : null}

            <div className="rw-export__row">
                <label className="rw-export__label" htmlFor="rw-export-req">
                    要件定義書
                </label>
                <select
                    id="rw-export-req"
                    value={requirements}
                    onChange={(e) => setRequirements(Number(e.target.value))}
                >
                    {requirementVersions.map((v) => (
                        <option key={v.version} value={v.version}>
                            {versionLabel(v.version)}
                        </option>
                    ))}
                </select>
            </div>

            <div className="rw-export__row">
                <label className="rw-export__label" htmlFor="rw-export-design">
                    基本設計書
                </label>
                <span className="rw-export__value">
                    <label className="rw-export__check">
                        <input
                            type="checkbox"
                            checked={includeDesign}
                            onChange={(e) => setIncludeDesign(e.target.checked)}
                            disabled={designVersions.length === 0}
                            aria-label="基本設計書を同梱する"
                        />
                        同梱する
                    </label>
                    <select
                        id="rw-export-design"
                        value={basicDesign}
                        onChange={(e) => setBasicDesign(Number(e.target.value))}
                        disabled={!includeDesign || designVersions.length === 0}
                    >
                        {designVersions.map((v) => (
                            <option key={v.version} value={v.version}>
                                {versionLabel(v.version)}
                            </option>
                        ))}
                    </select>
                    {designVersions.length === 0 ? (
                        <span className="rw-export__hint">
                            まだ生成されていません。成果物の画面で基本設計を生成すると選べます。
                        </span>
                    ) : null}
                </span>
            </div>

            <div className="rw-export__row">
                <label className="rw-export__label" htmlFor="rw-export-dest">
                    出力先
                </label>
                <span className="rw-export__value">
                    <input
                        id="rw-export-dest"
                        className="rw-export__input"
                        value={destination}
                        onChange={(e) => setDestination(e.target.value)}
                        placeholder="出力先フォルダのパス"
                    />
                    <Button onClick={() => void chooseFolder()}>フォルダを選ぶ</Button>
                </span>
            </div>

            <div className="rw-export__actions">
                <Button
                    variant="primary"
                    onClick={() => void run(false)}
                    disabledReason={canRun ? undefined : '出力先を指定してください。'}
                >
                    エクスポート
                </Button>
            </div>

            {result && !result.exported ? (
                <section className="rw-export__violations" aria-label="整合性検証の結果">
                    <h3 className="rw-export__subtitle">
                        整合性の違反（エラー {countBy(violations, 'error')} 件 / 警告 {countBy(violations, 'warning')} 件）
                    </h3>
                    <ul className="rw-export__list">
                        {violations.map((v, i) => (
                            <li key={`${v.check}-${i}`}>
                                <Chip tone={v.severity === 'error' ? 'danger' : 'warn'}>
                                    {v.severity === 'error' ? 'エラー' : '警告'}
                                </Chip>{' '}
                                {checkLabel(v.check)}: {v.message}
                                {v.file ? (
                                    <span className="rw-mono">
                                        {' '}
                                        （{v.file}
                                        {v.line ? `:${v.line}` : ''}）
                                    </span>
                                ) : null}
                            </li>
                        ))}
                    </ul>
                    <div className="rw-export__actions">
                        <Button onClick={onBack}>修正に戻る</Button>
                        <Button variant="primary" onClick={() => void run(true)}>
                            警告付きでエクスポート
                        </Button>
                    </div>
                </section>
            ) : null}

            {result?.exported ? (
                <section className="rw-export__files" aria-label="出力ファイル">
                    <h3 className="rw-export__subtitle">
                        出力ファイル（{result.files.length} 件）
                        {violations.length === 0 ? <Chip tone="accent">検証合格</Chip> : null}
                    </h3>
                    <ul className="rw-export__list rw-mono">
                        {result.files.map((f) => (
                            <li key={f}>{f}</li>
                        ))}
                    </ul>
                </section>
            ) : null}
        </section>
    )
}

function countBy(violations: docgen.Violation[], severity: string): number {
    return violations.filter((v) => v.severity === severity).length
}
