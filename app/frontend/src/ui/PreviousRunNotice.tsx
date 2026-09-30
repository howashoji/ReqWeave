import {useState} from 'react'
import {Banner} from './Banner'
import {Button} from './Button'

/**
 * 前回の異常終了の案内（起動時のクラッシュの検知による。パネル・バナー系）。
 *
 * 起動の記録に対応する終了の記録が無い場合に出す。**操作は止めない**（前回の話であり、
 * 今の操作を妨げる理由がない）。調査に協力してもらえるよう、診断情報の書き出し（設定画面）へ導く。
 *
 * 判定はバインディング側（動作ログの走査）で行い、画面はその結果を表示するだけにする。
 */
export function PreviousRunNotice({incomplete, onOpenSettings}: {incomplete: boolean; onOpenSettings: () => void}) {
    const [dismissed, setDismissed] = useState(false)
    if (!incomplete || dismissed) {
        return null
    }
    return (
        <Banner
            tone="warn"
            title="前回は正常に終了していません"
            onDismiss={() => setDismissed(true)}
            onDefer={() => setDismissed(true)}
        >
            <p>
                保存済みの内容はそのまま開けます。原因の調査に協力いただける場合は、設定の「診断情報」から
                記録を書き出してお知らせください。
            </p>
            <p>
                <Button variant="secondary" onClick={onOpenSettings}>
                    設定を開く
                </Button>
            </p>
        </Banner>
    )
}
