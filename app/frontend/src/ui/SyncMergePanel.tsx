import {useState} from 'react'
import {binding} from '../../wailsjs/go/models'
import {Button} from './Button'
import './SyncMergePanel.css'

/**
 * 取り込み時の三面マージ。パネル・バナー系。
 *
 * 取り込みで他のメンバーと同じ対象を変更していたときだけ表示する。対象ごとに
 * **基準版 / 相手の内容 / 自分の内容**の 3 面を並べ、「相手を採る」「自分を採る」「両立させる」
 * 「未決事項として起票する」から本人が選ぶ（相手やオーナーの確認を必須にしない）。
 *
 * - **既定の選択を置かない**（承認なしに進む経路を作らない）。
 *   全対象に選択が揃うまで実行ボタンは無効のままにする。
 * - 中止したときは取り込み前の状態のまま（同期の先頭で作った復帰点へ戻す）。その旨を明示する。
 * - git のコンフリクトマーカー・生のエラー文言は出さない（利用者に git を見せない）。バックエンドが
 *   提示単位（レコード / エントリ / 項目 / 章）の内容だけを渡す。
 */

/** 「未決事項として起票する」で暫定採用する側。 */
const ADOPT_OPTIONS = [
    {value: 'theirs', label: '決まるまで相手の内容を使う'},
    {value: 'ours', label: '決まるまで自分の内容を使う'},
] as const

export function SyncMergePanel({
    conflicts,
    choices,
    notice,
    busy,
    resolutions,
    onChange,
    onCreateOpenIssue,
    onApply,
    onCancel,
}: {
    conflicts: binding.SyncConflictView[]
    /** choices はバックエンドが持つ 4 択（画面にラベルを二重に持たせない）。 */
    choices: binding.SyncChoiceOption[]
    notice: string
    busy: boolean
    /** resolutions は対象 ID → 現在の承認内容（親が保持し、再実行時にそのまま渡す）。 */
    resolutions: Record<string, binding.SyncResolutionInput>
    onChange: (id: string, resolution: binding.SyncResolutionInput) => void
    /** onCreateOpenIssue は未決事項を起票して ID を承認へ書き戻す。 */
    onCreateOpenIssue: (conflict: binding.SyncConflictView) => void
    onApply: () => void
    onCancel: () => void
}) {
    const [open, setOpen] = useState<Record<string, boolean>>({})

    const decided = conflicts.every((c) => isDecided(resolutions[c.id]))
    const undecided = conflicts.filter((c) => !isDecided(resolutions[c.id])).length

    return (
        <section className="rw-syncmerge" aria-label="取り込み時の競合の解決">
            <header className="rw-syncmerge__head">
                <h3 className="rw-syncmerge__title">同じ対象が両方で変更されています</h3>
                <Button variant="quiet" onClick={onCancel}>
                    取り込みをやめる
                </Button>
            </header>
            <p className="rw-syncmerge__notice" role="alert">
                {notice}
            </p>

            {conflicts.map((c) => {
                const current = resolutions[c.id] ?? emptyResolution()
                return (
                    <article key={c.id} className="rw-syncmerge__item" aria-label={`競合 ${c.label}`}>
                        <p className="rw-syncmerge__label">
                            {c.label}
                            <span className="rw-syncmerge__meta rw-mono">
                                {c.unitLabel} ／ {c.categoryLabel} ／ 相手: {c.theirsAuthor}
                            </span>
                        </p>
                        <div className="rw-syncmerge__panes">
                            <Pane title="基準版（分かれる前）" body={c.base} />
                            <Pane title={`${c.theirsAuthor}の内容`} body={c.theirs} />
                            <Pane title="自分の内容" body={c.ours} />
                        </div>

                        <fieldset className="rw-syncmerge__choices">
                            <legend className="rw-syncmerge__pane-title">この対象の扱い（選ぶまで進みません）</legend>
                            {choices.map((choice) => (
                                <label key={choice.choice} className="rw-syncmerge__choice">
                                    <input
                                        type="radio"
                                        name={`syncmerge-${c.id}`}
                                        checked={current.choice === choice.choice}
                                        onChange={() => onChange(c.id, {...emptyResolution(), choice: choice.choice})}
                                    />
                                    <span>
                                        {choice.label}
                                        <span className="rw-syncmerge__hint">{choice.hint}</span>
                                    </span>
                                </label>
                            ))}
                        </fieldset>

                        {current.choice === 'both' ? (
                            <label className="rw-syncmerge__merged">
                                <span className="rw-syncmerge__pane-title">両立させた内容</span>
                                <textarea
                                    aria-label={`${c.label} の統合後の内容`}
                                    value={current.merged ?? ''}
                                    rows={8}
                                    onChange={(e) => onChange(c.id, {...current, merged: e.target.value})}
                                />
                            </label>
                        ) : null}

                        {current.choice === 'open-issue' ? (
                            <div className="rw-syncmerge__issue">
                                {current.openIssueId ? (
                                    <p className="rw-syncmerge__meta rw-mono">起票済み: {current.openIssueId}</p>
                                ) : (
                                    <Button
                                        onClick={() => onCreateOpenIssue(c)}
                                        disabledReason={busy ? '処理中です。' : undefined}
                                    >
                                        両方の内容を含む未決事項を起票する
                                    </Button>
                                )}
                                <fieldset className="rw-syncmerge__choices">
                                    <legend className="rw-syncmerge__pane-title">決まるまでの扱い</legend>
                                    {ADOPT_OPTIONS.map((option) => (
                                        <label key={option.value} className="rw-syncmerge__choice">
                                            <input
                                                type="radio"
                                                name={`syncmerge-adopt-${c.id}`}
                                                checked={current.adopt === option.value}
                                                onChange={() => onChange(c.id, {...current, adopt: option.value})}
                                            />
                                            <span>{option.label}</span>
                                        </label>
                                    ))}
                                </fieldset>
                            </div>
                        ) : null}

                        <Button
                            variant="secondary"
                            onClick={() => setOpen((prev) => ({...prev, [c.id]: !prev[c.id]}))}
                            aria-expanded={Boolean(open[c.id])}
                        >
                            {open[c.id] ? '内容の全文を隠す' : '内容の全文を見る'}
                        </Button>
                        {open[c.id] ? (
                            <pre className="rw-syncmerge__full">{fullText(c)}</pre>
                        ) : null}
                    </article>
                )
            })}

            <div className="rw-syncmerge__actions">
                <Button
                    variant="primary"
                    onClick={onApply}
                    disabledReason={
                        busy
                            ? '処理中です。'
                            : decided
                              ? undefined
                              : `扱いが決まっていない対象が ${undecided} 件あります。すべて選んでください。`
                    }
                >
                    この内容で取り込む
                </Button>
                <Button variant="quiet" onClick={onCancel}>
                    取り込みをやめる（作業コピーは変わりません）
                </Button>
            </div>
        </section>
    )
}

/** 三面の 1 面。内容が無い（削除された）ことは文言で示す（空欄にしない）。 */
function Pane({title, body}: {title: string; body: string}) {
    return (
        <div className="rw-syncmerge__pane">
            <p className="rw-syncmerge__pane-title">{title}</p>
            <pre className="rw-syncmerge__pre">{body.trim() ? body : '（内容なし。削除されています）'}</pre>
        </div>
    )
}

/** 承認が成立しているか（判定はバックエンドが正だが、実行ボタンの有効化のため画面でも同じ条件を見る）。 */
function isDecided(resolution?: binding.SyncResolutionInput): boolean {
    if (!resolution) {
        return false
    }
    switch (resolution.choice) {
        case 'theirs':
        case 'ours':
            return true
        case 'both':
            return Boolean(resolution.merged?.trim())
        case 'open-issue':
            return Boolean(resolution.openIssueId) && (resolution.adopt === 'theirs' || resolution.adopt === 'ours')
        default:
            return false
    }
}

function emptyResolution(): binding.SyncResolutionInput {
    return {choice: '', merged: '', adopt: '', openIssueId: ''} as binding.SyncResolutionInput
}

/** 全文表示（3 面をまとめて確認するための折りたたみ）。 */
function fullText(c: binding.SyncConflictView): string {
    return [
        `【基準版】\n${c.base || '（内容なし）'}`,
        `【${c.theirsAuthor}の内容】\n${c.theirs || '（内容なし）'}`,
        `【自分の内容】\n${c.ours || '（内容なし）'}`,
    ].join('\n\n')
}
