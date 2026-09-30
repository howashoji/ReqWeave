import type {binding} from '../../wailsjs/go/models'
import {Banner} from './Banner'
import {Button} from './Button'
import './UpdateNotice.css'

/**
 * 自動更新の通知・同意・進行表示（パネル・バナー系）。
 *
 * 全画面共通の位置（App の先頭）へ置く。表示するかどうかと内容は、
 * バインディングが返した `UpdateState.status` だけで決める（画面側で更新の可否を判定しない）。
 *
 *   idle             … 何も出さない
 *   checking         … 確認中（操作なし）
 *   available        … 新版あり。**「更新する」を押すまで取得は始まらない**
 *   downloading      … 取得中（中止できる）
 *   verifying        … 検証中（中止できる）
 *   restart_required … 適用済み。利用者の操作で再起動する（自動で再起動しない）
 *   failed           … 失敗。現行版のまま利用を続けられる
 *
 * 回答モードでは出さない（質問票に答えるだけの一時利用で、回答への集中を妨げないため。回答モードは AppShell 自体を使わない）。
 * 同意・中止はすべてアプリ内のボタンで行う（ブラウザ標準のモーダルは使わない）。
 */

/** status ごとの見出し。列挙を型で網羅強制する（生のコード値を画面へ出さない） */
const TITLES = {
    idle: '',
    checking: '新しい版を確認しています',
    available: '新しい版が公開されています',
    downloading: '更新ファイルを取得しています',
    verifying: '更新ファイルを確認しています',
    restart_required: '更新の準備ができました',
    failed: '更新できませんでした',
} satisfies Record<binding.UpdateState['status'] & string, string>

type Status = keyof typeof TITLES

function titleFor(status: string): string {
    return status in TITLES ? TITLES[status as Status] : '更新の状態を確認できません'
}

export function UpdateNotice({
    state,
    onApply,
    onDefer,
    onCancel,
    onRestart,
}: {
    state: binding.UpdateState | null
    /** 「更新する」= 利用者の同意。これが押されるまで取得は始まらない */
    onApply: () => void
    /** 「後で」= このアプリ実行中は同じ版を再通知しない */
    onDefer: () => void
    onCancel: () => void
    onRestart: () => void
}) {
    if (!state || state.status === 'idle') {
        return null
    }
    const status = state.status
    const tone = status === 'failed' ? 'danger' : status === 'restart_required' ? 'accent' : 'info'

    if (status === 'available') {
        return (
            <Banner tone="info" title={titleFor(status)} onDismiss={onDefer} onDefer={onDefer} deferLabel="後で">
                <p className="rw-update__detail">
                    現在の版 <span className="rw-mono">{state.currentVersion}</span> から{' '}
                    <span className="rw-mono">{state.newVersion}</span> へ更新できます。
                    更新は「更新する」を押したときだけ始まります。
                </p>
                <div className="rw-update__actions">
                    <Button onClick={onApply}>更新する</Button>
                </div>
            </Banner>
        )
    }

    if (status === 'restart_required') {
        return (
            <Banner tone="accent" title={titleFor(status)} onDismiss={onDefer} onDefer={onDefer} deferLabel="後で">
                <p className="rw-update__detail">
                    版 <span className="rw-mono">{state.newVersion}</span> を適用しました。
                    再起動すると新しい版で使えます。
                </p>
                {state.message ? <p className="rw-update__detail">{state.message}</p> : null}
                <div className="rw-update__actions">
                    <Button onClick={onRestart}>再起動する</Button>
                </div>
            </Banner>
        )
    }

    if (status === 'downloading' || status === 'verifying') {
        return (
            <Banner tone="info" title={titleFor(status)} onDismiss={onCancel} onDefer={onCancel} deferLabel="中止する">
                <p className="rw-update__detail">
                    版 <span className="rw-mono">{state.newVersion}</span> を準備しています。
                    完了するまでこのまま使えます。
                </p>
            </Banner>
        )
    }

    if (status === 'failed') {
        return (
            <Banner tone="danger" title={titleFor(status)} onDismiss={onDefer} onDefer={onDefer} deferLabel="後で">
                <p className="rw-update__detail">{state.message}</p>
                <p className="rw-update__detail">
                    現在の版 <span className="rw-mono">{state.currentVersion}</span> のまま使い続けられます。
                </p>
            </Banner>
        )
    }

    // checking（および未知の状態）。操作は出さない。
    return (
        <Banner tone={tone} title={titleFor(status)} onDismiss={onDefer} onDefer={onDefer} deferLabel="後で">
            <p className="rw-update__detail">しばらくお待ちください。この間もこのまま使えます。</p>
        </Banner>
    )
}
