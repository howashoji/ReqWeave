import {binding} from '../../wailsjs/go/models'
import {Button} from './Button'
import {EmptyState} from './EmptyState'
import {Overlay} from './Overlay'
import './ChangeSummaryPanel.css'

/**
 * 変更要約。パネル・バナー系。
 *
 * 取り込みの完了時に自動表示し、プロジェクトを開いたとき・共通メニューからも表示できる。
 * **前回確認した取り込み以降に自分の作業コピーへ入った**、他の作業者による変更を区分別に並べ、
 * 各項目から該当レコードへ遷移する（遷移は影響一覧と同じ入口）。
 * 単位は「取り込み」であり日時の区切りではない（他の作業者の変更は取り込んだときに初めて届くため）。AI 呼び出しを伴わない。
 *
 * **読むための一時の面**なので作業領域へ重ねる（`Overlay`）。
 * 流れの中へ挿し込むと、開いた瞬間に作業がそのぶん下へ押し出される。
 */

export function ChangeSummaryPanel({
    summary,
    onOpenRecord,
    onClose,
}: {
    summary: binding.ChangeSummaryView
    /** onOpenRecord は対象 ID の該当レコードを開く（影響一覧と同じ遷移）。 */
    onOpenRecord: (target: string) => void
    onClose: () => void
}) {
    const items = summary.items ?? []
    const counts = summary.counts ?? {}

    return (
        <Overlay label="取り込んだ変更" onClose={onClose}>
        <section className="rw-summary">
            <header className="rw-summary__head">
                <h3 className="rw-summary__title">取り込んだ他の作業者による変更</h3>
                <Button variant="quiet" onClick={onClose}>
                    閉じる
                </Button>
            </header>

            {items.length > 0 ? (
                <p className="rw-summary__meta">前回確認した取り込み以降に、自分の作業コピーへ入った変更です。</p>
            ) : null}

            {items.length === 0 ? (
                <EmptyState
                    message={summary.notice ?? '前回確認した取り込み以降、他の作業者による変更はありません。'}
                    automatic="同期先から取り込むたびに、他の作業者の変更がここへ要約されます。"
                />
            ) : (
                <>
                    <p className="rw-summary__counts">
                        {Object.entries(counts).map(([kind, count]) => (
                            <span key={kind} className="rw-summary__count">
                                {labelOf(items, kind)} {count} 件
                            </span>
                        ))}
                    </p>
                    <ul className="rw-summary__items">
                        {items.map((item, i) => (
                            <li key={`${item.target}-${i}`}>
                                <Button variant="secondary" onClick={() => onOpenRecord(item.target)}>
                                    {item.target}
                                </Button>
                                <span className="rw-summary__kind">{item.kindLabel}</span>
                                <span className="rw-summary__body">{item.summary}</span>
                                <span className="rw-summary__meta rw-mono">
                                    {item.author} ／ {item.at}
                                </span>
                            </li>
                        ))}
                    </ul>
                </>
            )}
        </section>
        </Overlay>
    )
}

/** 区分の表示名は項目が持つラベルを使う（画面側で内部値を訳さない）。 */
function labelOf(items: binding.ChangeSummaryItemView[], kind: string): string {
    return items.find((i) => i.kind === kind)?.kindLabel ?? kind
}
