import '@testing-library/jest-dom/vitest'
import {configure} from '@testing-library/react'

/*
 * testing-library の非同期ユーティリティ（findBy* / waitFor）の待ち時間。
 *
 * 既定は 1000ms。26 ファイルを並列実行すると jsdom の環境構築だけで 100 秒規模になり
 * （2026-09-03 の実測: environment 100.92s / setup 13.18s）、1 秒以内に描画が終わらないことがある。
 * そのとき findBy* は vitest の testTimeout（vite.config.ts = 15000ms）に達する前に諦め、
 * **「要素が見つからない」という原因を誤らせるエラー**になる（時間切れだと分からない）。
 * 以前に vitest 側の待ち時間だけを広げたため、この経路が既定のまま残っていた。
 *
 * **長すぎる値にしない**。本当に描画されない不具合の検出が遅れる（vitest 側の待ち時間と同じ判断基準）。
 * 単独実行の実測（1 ファイルあたり数百 ms）に対し 5 秒は十分な余裕がありつつ、
 * vitest の testTimeout（15 秒）より先に切れるため、失敗の原因が待ち時間だと判別できる。
 */
configure({asyncUtilTimeout: 5000})

/*
 * jsdom は `scrollIntoView` を実装しない（レイアウトを持たないため）。
 * 実装が呼ぶと TypeError になり、原因と無関係な「要素が見つからない」で落ちる（実測）。
 * 呼ばれたことを検証できるよう、何もしない実装を置く（テスト側は呼び出しの有無を見る）。
 */
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = function scrollIntoView() {
        // レイアウトが無いため何もしない
    }
}
