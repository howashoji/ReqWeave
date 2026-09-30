import {useEffect, useState} from 'react'
import {WorkModeOptions} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {Button} from './Button'
import {Overlay} from './Overlay'
import './WorkStartDialog.css'

/**
 * 予約の選択。パネル・バナー系。
 *
 * 成果物の生成・差分再生成・確定・差し戻しの着手時に、進め方（排他 / 並行）を選ばせる。
 *
 * - **予約は同期先へ反映して初めて他のメンバーに効く**（保証と誤認させない）。選択肢の説明で示す。
 * - 他のメンバーが排他で予約している対象は警告を表示し、確認操作を経れば着手できる（予約は同期先に載って初めて効く目印で、保証ではないため禁止しない）。
 * - 既定の選択を置かず、選ぶまで着手ボタンは無効のままにする。
 * - ネイティブ confirm は使わない（アプリ内のダイアログで受ける）。
 * - **不可逆な操作の着手確認**にあたるため、背後を操作させない面で受ける
 *   （`Overlay` の `modal`）。選び終わるまで他所を触らせない。
 */

export function WorkStartDialog({
    operation,
    check,
    busy,
    onStart,
    onCancel,
}: {
    /** operation は着手する操作名（「成果物の生成」等）。 */
    operation: string
    /** check は着手前の確認結果（他メンバーの排他予約があれば警告文を持つ）。 */
    check: binding.ReservationCheckView | null
    busy: boolean
    onStart: (work: binding.WorkStart) => void
    onCancel: () => void
}) {
    const [modes, setModes] = useState<binding.WorkModeOption[]>([])
    const [mode, setMode] = useState('')
    const [confirmed, setConfirmed] = useState(false)

    useEffect(() => {
        Promise.resolve()
            .then(() => WorkModeOptions())
            .then(setModes)
            .catch(() => setModes([]))
    }, [])

    const reserved = Boolean(check?.reserved)
    const startReason = busy
        ? '処理中です。'
        : !mode
          ? '進め方を選んでください。'
          : reserved && !confirmed
            ? '他のメンバーの予約があります。内容を確認して同意してください。'
            : undefined

    return (
        <Overlay label={`${operation}の進め方`} variant="modal" onClose={onCancel}>
        <section className="rw-workstart">
            <h3 className="rw-workstart__title">{operation}を始めます</h3>

            {reserved ? (
                <p className="rw-workstart__warning" role="alert">
                    {check?.warning}
                </p>
            ) : null}

            <fieldset className="rw-workstart__choices">
                <legend className="rw-workstart__legend">進め方（選ぶまで始められません）</legend>
                {modes.map((option) => (
                    <label key={option.mode} className="rw-workstart__choice">
                        <input
                            type="radio"
                            name="work-start-mode"
                            checked={mode === option.mode}
                            onChange={() => setMode(option.mode)}
                        />
                        <span>
                            {option.label}
                            <span className="rw-workstart__hint">{option.hint}</span>
                        </span>
                    </label>
                ))}
            </fieldset>

            {reserved ? (
                <label className="rw-workstart__choice">
                    <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />
                    <span>予約者がいることを確認したうえで、並行して着手します</span>
                </label>
            ) : null}

            <div className="rw-workstart__actions">
                <Button
                    variant="primary"
                    onClick={() => onStart({mode, confirmed} as binding.WorkStart)}
                    disabledReason={startReason}
                >
                    この進め方で始める
                </Button>
                <Button variant="quiet" onClick={onCancel}>
                    やめる
                </Button>
            </div>
        </section>
        </Overlay>
    )
}
