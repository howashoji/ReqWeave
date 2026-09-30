import '@testing-library/jest-dom/vitest'
// 実描画で見るので、全画面共通の基底スタイル（トークンを含む）を読み込む。
import '../style.css'

/*
 * 実ブラウザ（レイアウト検査）用の下ごしらえ。
 *
 * jsdom 用の `setup.ts` は「jsdom に無い API を補う」ためのものであり、実ブラウザでは要らない
 * （むしろ本物を差し替えてしまう）。ここでは読みやすい表明（jest-dom）だけを入れる。
 */
