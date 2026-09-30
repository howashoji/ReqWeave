import {fireEvent, screen, waitFor} from '@testing-library/react'
import {expect} from 'vitest'

/*
 * テストの操作ヘルパー。
 *
 * `await waitFor(() => expect(<モック>).toHaveBeenCalled())` は **呼ばれたこと**までしか待たない。
 * その呼び出しの解決を受けた再描画（ボタンの有効化・編集モードの解除など）は、まだ起きていないことがある。
 * 無効なボタンへの `fireEvent.click` は**黙って無視される**ため、押し損ねに気づけないまま
 * 後続の `waitFor` が時間切れになり、実装に欠陥がないのにゲートが赤くなる（偽赤）。
 * 並列実行の負荷が高いときだけ落ちるため、原因の特定も難しい。
 *
 * そこで「押せる状態になるまで待ってから押す」をこのヘルパーに寄せる。
 */

/**
 * 名前の一致するボタンが**存在し、かつ有効**になるまで待ってから押す。
 *
 * 毎回引き直すため、再描画でボタンの実体が入れ替わっても取り残されない。
 */
export async function clickEnabled(name: string | RegExp): Promise<HTMLElement> {
    const button = await waitFor(() => {
        const found = screen.getByRole('button', {name})
        expect(found).toBeEnabled()
        return found
    })
    fireEvent.click(button)
    return button
}

/**
 * 無効化されたボタンの**理由が画面に出ている**ことを確かめる（無効化したボタンには必ず理由を画面に出す）。
 *
 * `title` 属性を見るだけでは何も確かめられない。無効化された要素はポインタ事象を発しないため、
 * `title` に載せた理由はブラウザ上では表示されず、それでもテストは緑になるからだ（実際にそれで見逃した）。
 * ここでは**アプリ内の要素として描かれているか**を、ポインタを重ねて確かめる。
 */
export function expectDisabledReason(button: HTMLElement, reason: string | RegExp): void {
    expect(button).toBeDisabled()
    fireEvent.mouseOver(button)
    const tip = screen.getByRole('tooltip')
    if (typeof reason === 'string') {
        expect(tip).toHaveTextContent(reason)
    } else {
        expect(tip.textContent ?? '').toMatch(reason)
    }
    fireEvent.mouseOut(button)
}

/**
 * **引き直しながら**「現れて、かつ有効になる」まで待ってから押す。
 *
 * `clickEnabled` は名前で `screen` から引くため、行やダイアログの**中に限って**探したいとき
 * （同名のボタンが複数ある画面）には使えない。ここでは探し方そのものを受け取り、
 * **毎回その場で引き直す**。掴んだ節点を持ち回すと、再描画で作り直された要素を掴んだままになり、
 * いつまでも有効にならない要素を待ち続ける（`Members.test.tsx` で実測）。
 *
 * 押せない理由が「処理中」であることは珍しくない（画面は処理中に操作を無効化する）。
 * 読み出しが遅い環境ではこの間が伸びるため、**待たずに押すと click が黙って捨てられる**。
 */
export async function clickEnabledBy(find: () => HTMLElement): Promise<HTMLElement> {
    const button = await waitFor(() => {
        const found = find()
        expect(found).toBeEnabled()
        return found
    })
    fireEvent.click(button)
    return button
}
