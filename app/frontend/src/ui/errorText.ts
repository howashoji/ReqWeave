/*
 * バインディングのエラーを画面へ出す文言にする（利用者に内部の書式を見せない）。
 *
 * Wails v2 は Go が返したエラーの文言を `new Error(文言)` で包んで reject する
 * （`wailsjs` の実行時 calls.js）。`String(err)` で表示すると先頭に「Error: 」が付き、
 * 利用者向けの文言に内部の表記が混ざる。**エラーを画面へ出すときは必ずこれを通す**
 * （`String(err)` を画面のソースに書かない = conventions.test.ts で検査）。
 */

/** エラーの文言だけを取り出す。空になるときは「原因＋次の行動」の既定文にする */
export function errorText(err: unknown): string {
    const raw = err instanceof Error ? err.message : String(err ?? '')
    const text = raw.replace(/^(Error:\s*)+/, '').trim()
    return text || '処理を完了できませんでした。もう一度お試しください。'
}
