import './Utterance.css'

/**
 * 対話系の発話行。
 *
 * 吹き出しではなく「左ボーダー + 話者ラベル + タイムスタンプ」の行形式とする。
 * 左ボーダーは AI = `accent` / 人 = `faint`（faint は文字には薄すぎるため非テキストのみに使う）。
 * 話者ラベル・タイムスタンプは識別子・数値のためモノスペース。
 */
export type Speaker = 'ai' | 'human'

export function Utterance({
    speaker,
    name,
    timestamp,
    body,
    interrupted = false,
}: {
    speaker: Speaker
    /** 人の場合は作業者の表示名、AI の場合はモデル名など */
    name: string
    /** 表示用のタイムスタンプ文字列。ローカル時刻へ変換済みのものを渡す */
    timestamp: string
    body: string
    /** 中断された発話は抽出対象から除外されることを示す */
    interrupted?: boolean
}) {
    return (
        <article className={`rw-utterance rw-utterance--${speaker}`} data-speaker={speaker}>
            <header className="rw-utterance__head rw-mono">
                <span className="rw-utterance__name">{name}</span>
                <time className="rw-utterance__time">{timestamp}</time>
                {interrupted ? <span className="rw-utterance__flag">中断</span> : null}
            </header>
            <p className="rw-utterance__body">{body}</p>
        </article>
    )
}
