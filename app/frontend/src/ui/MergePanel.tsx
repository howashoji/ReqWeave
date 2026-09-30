import {useState} from 'react'
import {binding, dialogue} from '../../wailsjs/go/models'
import {Button} from './Button'
import './MergePanel.css'

/**
 * 競合マージ。
 *
 * 共有レコードの反映時に競合が検知されたときだけ表示する。三面（基準版 / 他メンバーの変更 /
 * 自分の反映案）を並べ、**反映を行う本人**がマージのしかたを選んで承認する
 * （相手メンバー・オーナーの確認を必須にしない）。
 *
 * 承認しない場合は反映案を保留する（未決事項として起票する導線を親画面が出す）。
 * 対話・回答取込・取り込みの画面で同じパネルを使う。
 */

/** マージのしかた。 */
export type MergeChoice = 'mine' | 'theirs' | 'open-issue'

const CHOICE_LABELS: Record<MergeChoice, string> = {
    mine: '自分の反映案を通す',
    theirs: '他メンバーの変更を採用する（自分の案は反映しない）',
    'open-issue': '決めきれないので未決事項にする',
}

export function MergePanel({
    conflicts,
    notice,
    proposals,
    busy,
    onApprove,
    onDefer,
}: {
    conflicts: binding.ConflictView[]
    notice?: string
    /** proposals は対象 ID → 自分の反映案（三面の右）。 */
    proposals: Record<string, string>
    busy: boolean
    /** onApprove は「自分の案を通す」と選んだ対象のマージ承認を返す（反映する本人が承認する）。 */
    onApprove: (merges: dialogue.MergeResolution[], openIssueTargets: string[]) => void
    onDefer: () => void
}) {
    const [choices, setChoices] = useState<Record<string, MergeChoice>>({})

    const approve = () => {
        const merges = conflicts
            .filter((c) => choices[c.id] === 'mine')
            .map((c) => ({
                id: c.id,
                baselineHash: c.currentHash,
                reference: `他メンバーの変更（${c.label}）`,
            })) as dialogue.MergeResolution[]
        const openIssues = conflicts.filter((c) => choices[c.id] === 'open-issue').map((c) => c.id)
        onApprove(merges, openIssues)
    }

    const decided = conflicts.every((c) => choices[c.id])

    return (
        <section className="rw-merge" aria-label="競合の解決">
            <h3 className="rw-merge__title">他のメンバーの変更と競合しました</h3>
            <p className="rw-merge__notice" role="alert">
                {notice ?? '反映は行っていません。内容を確認して、反映のしかたを選んでください。'}
            </p>

            {conflicts.map((c) => (
                <article key={c.id} className="rw-merge__item" aria-label={`競合 ${c.label}`}>
                    <p className="rw-merge__label rw-mono">{c.label}</p>
                    <div className="rw-merge__panes">
                        <div>
                            <p className="rw-merge__pane-title">基準版（あなたが候補を作った時点）</p>
                            <pre className="rw-merge__pre">{c.baselineBody}</pre>
                        </div>
                        <div>
                            <p className="rw-merge__pane-title">他メンバーの変更（現在の内容）</p>
                            <pre className="rw-merge__pre">{c.currentBody}</pre>
                        </div>
                        <div>
                            <p className="rw-merge__pane-title">自分の反映案</p>
                            <pre className="rw-merge__pre">{proposals[c.id] ?? '（本文の変更なし）'}</pre>
                        </div>
                    </div>
                    <fieldset className="rw-merge__choices">
                        <legend className="rw-merge__pane-title">この対象の扱い</legend>
                        {(Object.keys(CHOICE_LABELS) as MergeChoice[]).map((choice) => (
                            <label key={choice} className="rw-merge__choice">
                                <input
                                    type="radio"
                                    name={`merge-${c.id}`}
                                    checked={choices[c.id] === choice}
                                    onChange={() => setChoices((prev) => ({...prev, [c.id]: choice}))}
                                />
                                {CHOICE_LABELS[choice]}
                            </label>
                        ))}
                    </fieldset>
                </article>
            ))}

            <div className="rw-merge__actions">
                <Button
                    variant="primary"
                    onClick={approve}
                    disabledReason={
                        busy ? '処理中です。' : decided ? undefined : 'すべての対象について扱いを選んでください。'
                    }
                >
                    この内容で反映する
                </Button>
                <Button variant="quiet" onClick={onDefer}>
                    反映せず保留する
                </Button>
            </div>
        </section>
    )
}
