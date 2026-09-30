/// <reference types="vitest/config" />
import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
    plugins: [react()],
    test: {
        environment: 'jsdom',
        globals: true,
        // 実行ホストのタイムゾーンでテスト結果が変わらないようにする。
        // ローカル日時の表示（ui/format.ts の formatLocal / defaultPeriod）は保存値（UTC）を
        // ローカルへ変換するため、固定しないと期待値が実行環境に依存する。
        // 値は本プロジェクトの利用者・開発者の実運用に合わせて JST。
        env: {TZ: 'Asia/Tokyo'},
        setupFiles: ['./src/test/setup.ts'],
        include: ['src/**/*.test.{ts,tsx}'],
        // レイアウト検査（`*.layout.test.tsx`）は**実ブラウザ**で走らせる（jsdom では寸法を測れない）。
        // jsdom はレイアウトを計算しないため、ここへ混ざると必ず落ちる（実測: 本体の高さが 0）。
        // 設定は vitest.layout.config.ts、実行は `make -C app test-layout`。
        exclude: ['node_modules/**', 'src/**/*.layout.test.tsx'],
        passWithNoTests: false,
        // CSS を ?raw で読めるようにする（トークンの検証テストが tokens.css を直接読む）
        css: true,
        // テスト単位のタイムアウト（既定は 5000ms）。
        //
        // 画面テストは jsdom の環境構築が重く（26 ファイルの並列実行で environment 83s / setup 23s の実測）、
        // `waitFor` / `findBy` を数回連鎖する遷移テストは、他の処理と並行して回すと既定値を超える
        // （2026-09-01 に `Dialogue.test.tsx` が 5207ms で時間切れ。単独実行では 822ms）。
        // 実装の欠陥でないのにゲートが赤くなる（偽赤）とゲートへの信頼が失われるため、
        // 負荷に耐える値へ広げる。
        //
        // **長すぎる値にはしない**。本当にハングしたテストの検出が遅れる。
        // 単独実行の実測（1 ファイル 1 秒未満）に対し 15 秒は十分な余裕がありつつ、
        // ハングを 15 秒で打ち切れる範囲に収まる。
        testTimeout: 15000,
        // `waitFor` 等のフック側も同様の理由で広げる（既定 10000ms のままだと
        // testTimeout より先に切れて原因が分かりにくくなる）。
        hookTimeout: 15000,
    },
})
