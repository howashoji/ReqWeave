import {useCallback, useEffect, useState} from 'react'
import {ClearUsageLimit, CurrentPermission, SetUsageLimit, UsageStatusNow} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Banner, Button, Chip, formatCount, formatRatio, Toast, errorText} from '../ui'
import './UsageLimit.css'

/**
 * トークン上限設定。画面型は一覧・管理系。
 *
 * 上限値（プロジェクト累計のトークン数）と警告閾値（既定 80%）を設定・変更・解除する。
 * **オーナーのみ**が操作でき、編集・閲覧権限では無効化して理由を示す（ほかの管理画面と同じ方式）。
 * 権限判定・値の検証・変更履歴への記録はすべてバインディング側が行い、画面は判定を持たない
 * （判定を 1 か所に集めるため）。現在の消費・上限・状態も同じ取得口（UsageStatusNow）から得る。
 */

/** 利用量の状態ラベル（保存値は英語識別子）。網羅を型で強制し、生のコード値は表示しない。 */
const LEVEL_LABELS = {
    none: '上限内',
    warn: '上限に接近',
    blocked: '上限に到達',
} as const

type Level = keyof typeof LEVEL_LABELS

function levelLabel(level: string): string {
    return level in LEVEL_LABELS ? LEVEL_LABELS[level as Level] : '状態不明'
}

function levelTone(level: string): 'accent' | 'warn' | 'danger' {
    if (level === 'blocked') {
        return 'danger'
    }
    return level === 'warn' ? 'warn' : 'accent'
}

/** 既定の警告閾値（warn_ratio の既定 = 80%）。未設定のときの入力欄の初期値。 */
const DEFAULT_WARN_PERCENT = 80

export function UsageLimit({onBack, onChanged}: {onBack: () => void; onChanged?: () => void}) {
    const [status, setStatus] = useState<binding.UsageStatus | null>(null)
    const [permission, setPermission] = useState<binding.PermissionView | null>(null)
    const [tokensMax, setTokensMax] = useState('')
    const [warnPercent, setWarnPercent] = useState(String(DEFAULT_WARN_PERCENT))
    const [error, setError] = useState('')
    const [toast, setToast] = useState<{tone: 'accent' | 'danger'; message: string} | null>(null)
    const [busy, setBusy] = useState(false)

    /** 保存済みの値を入力欄へ写す（未設定なら空欄と既定の警告閾値）。 */
    const applyStatus = useCallback((next: binding.UsageStatus) => {
        setStatus(next)
        setTokensMax(next.limitTokens === undefined || next.limitTokens === null ? '' : String(next.limitTokens))
        setWarnPercent(
            next.warnRatio > 0 ? String(Math.round(next.warnRatio * 100)) : String(DEFAULT_WARN_PERCENT),
        )
    }, [])

    useEffect(() => {
        Promise.resolve()
            .then(() => UsageStatusNow())
            .then(applyStatus)
            .catch((err: unknown) => setError(errorText(err)))
    }, [applyStatus])

    useEffect(() => {
        Promise.resolve()
            .then(() => CurrentPermission())
            .then(setPermission)
            .catch(() => setPermission(null))
    }, [])

    const save = async () => {
        setBusy(true)
        setError('')
        try {
            // 入力は文字列で受け、数値化はここだけで行う（空欄と 0 を取り違えない）。
            const max = Number(tokensMax)
            const percent = Number(warnPercent)
            const next = await SetUsageLimit({
                tokensMax: Number.isFinite(max) ? max : 0,
                warnRatio: Number.isFinite(percent) ? percent / 100 : undefined,
            } as binding.UsageLimitRequest)
            applyStatus(next)
            setToast({tone: 'accent', message: 'トークン上限を保存しました。'})
            onChanged?.()
        } catch (err: unknown) {
            setToast({tone: 'danger', message: errorText(err)})
        } finally {
            setBusy(false)
        }
    }

    const clear = async () => {
        setBusy(true)
        setError('')
        try {
            applyStatus(await ClearUsageLimit())
            setToast({tone: 'accent', message: 'トークン上限を解除しました。'})
            onChanged?.()
        } catch (err: unknown) {
            setToast({tone: 'danger', message: errorText(err)})
        } finally {
            setBusy(false)
        }
    }

    const canManage = permission?.canManageUsageLimit ?? false
    const manageReason = canManage
        ? undefined
        : (permission?.usageLimitReason ??
          'AI 利用量上限の設定はオーナー権限が必要です。オーナーに依頼してください。')
    const busyReason = busy ? '処理中です。' : undefined
    const inputReason = tokensMax.trim() === '' ? '上限値を入力してください。' : undefined
    const hasLimit = status?.limitTokens !== undefined && status?.limitTokens !== null

    return (
        <section className="rw-limit" aria-label="トークン上限設定">
            <header className="rw-limit__head">
                <h2 className="rw-limit__title">トークン上限</h2>
                <div className="rw-limit__actions">
                    <Button onClick={onBack}>AI 利用量へ戻る</Button>
                </div>
            </header>

            {error ? (
                <Banner tone="danger" title={error} onDismiss={() => setError('')} onDefer={() => setError('')} />
            ) : null}

            <dl className="rw-limit__figures">
                <div>
                    <dt>これまでの消費</dt>
                    <dd className="rw-mono">{formatCount(status?.consumedTokens ?? 0)}</dd>
                </div>
                <div>
                    <dt>上限</dt>
                    <dd className="rw-mono">{hasLimit ? formatCount(status?.limitTokens ?? 0) : '未設定'}</dd>
                </div>
                <div>
                    <dt>消費率</dt>
                    <dd className="rw-mono">{formatRatio(status?.consumptionRatio)}</dd>
                </div>
                <div>
                    <dt>上限までの残り</dt>
                    <dd className="rw-mono">
                        {status?.remainingTokens === undefined || status?.remainingTokens === null
                            ? '—'
                            : formatCount(status.remainingTokens)}
                    </dd>
                </div>
                <div>
                    <dt>状態</dt>
                    <dd>
                        <Chip tone={levelTone(status?.level ?? 'none')}>{levelLabel(status?.level ?? 'none')}</Chip>
                    </dd>
                </div>
            </dl>

            <div className="rw-limit__controls">
                <label className="rw-limit__field">
                    <span>上限値（トークン）</span>
                    <input
                        type="number"
                        min={1}
                        value={tokensMax}
                        onChange={(e) => setTokensMax(e.target.value)}
                        disabled={!canManage}
                        aria-label="上限値（トークン）"
                    />
                </label>
                <label className="rw-limit__field">
                    <span>警告閾値（%）</span>
                    <input
                        type="number"
                        min={1}
                        max={100}
                        value={warnPercent}
                        onChange={(e) => setWarnPercent(e.target.value)}
                        disabled={!canManage}
                        aria-label="警告閾値（%）"
                    />
                </label>
                <Button
                    variant="primary"
                    onClick={() => void save()}
                    disabledReason={manageReason ?? busyReason ?? inputReason}
                >
                    {hasLimit ? '上限を変更する' : '上限を設定する'}
                </Button>
                <Button
                    onClick={() => void clear()}
                    disabledReason={manageReason ?? busyReason ?? (hasLimit ? undefined : '上限は設定されていません。')}
                >
                    上限を解除する
                </Button>
            </div>

            <p className="rw-limit__hint">
                上限に達すると AI を使う操作を開始できなくなります（参照・手動編集・進捗レポート・
                エクスポート・利用量の表示は続けられます）。警告閾値を超えると、AI を使う画面に
                残りトークン数の警告が出ます。
                {manageReason ? `　${manageReason}` : ''}
            </p>

            {toast ? <Toast tone={toast.tone} message={toast.message} onDismiss={() => setToast(null)} /> : null}
        </section>
    )
}
