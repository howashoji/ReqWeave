/*
 * 同梱フォントの読込（フォントは配布物に同梱し、外部 CDN から読み込まない）。
 *
 * 本文 = Noto Sans JP / モノスペース = IBM Plex Mono（いずれも OFL）。
 * 必要な字種・ウェイトのみを取り込む（latin と japanese の 400/700。モノは latin 400）。
 * パッケージは node_modules からバンドルされ、実行時にネットワークへ出ない。
 */
import '@fontsource/noto-sans-jp/latin-400.css'
import '@fontsource/noto-sans-jp/latin-700.css'
import '@fontsource/noto-sans-jp/japanese-400.css'
import '@fontsource/noto-sans-jp/japanese-700.css'
import '@fontsource/ibm-plex-mono/latin-400.css'
