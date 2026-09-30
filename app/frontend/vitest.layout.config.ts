/// <reference types="vitest/config" />
import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'

/*
 * レイアウト検査だけを**実ブラウザ**で走らせる設定。
 *
 * jsdom はレイアウトを計算しない（幅・高さ・はみ出しがすべて 0）。そのため
 * 「右ペインを縮めたら入力欄がはみ出す」「行を 1 つ足したら本体が縦を失う」
 * といった崩れは、これまで**実機で人が見つけるまで気づけなかった**。
 *
 * 通常の単体テスト（jsdom）はそのまま据え置き、**実描画が要るものだけ**をここへ隔離する:
 *
 * - 対象は `*.layout.test.tsx` の数本に限る（起動が重いため全部は移さない）。
 * - 実行は独立した段（`make -C app test-layout`）。既存の段の所要時間を延ばさない。
 * - ブラウザが無い環境では**黙って緑にせず失敗させる**（未実施を成功と区別する）。
 */
export default defineConfig({
    plugins: [react()],
    test: {
        // testing-library の自動後片づけ（afterEach の cleanup）は globals が要る。
        // 無いと**前のテストの DOM が残り**、次のテストの測定位置がずれる（実測: 1306px 下）。
        globals: true,
        include: ['src/**/*.layout.test.tsx'],
        passWithNoTests: false,
        css: true,
        env: {TZ: 'Asia/Tokyo'},
        setupFiles: ['./src/test/setup.browser.ts'],
        browser: {
            enabled: true,
            provider: 'playwright',
            headless: true,
            screenshotFailures: false,
            // 実機で崩れたのは「窓を広げないと読めない」状況なので、**小さめの窓**で見る。
            instances: [{browser: 'chromium', viewport: {width: 1000, height: 700}}],
        },
        testTimeout: 15000,
        hookTimeout: 15000,
    },
})
