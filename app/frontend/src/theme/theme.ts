/*
 * テーマ切替。
 * 既定はダーク。手動でライトへ切り替える。OS 外観設定への自動追従はしない（アプリの見た目を利用者の選択だけで決める）。
 *
 * 選択値の永続化は本モジュールの責務ではない（アプリ設定の側が持つ）。
 * ここでは「テーマ値 → DOM 属性」の写像だけを持つ。
 */

export type Theme = 'dark' | 'light'

export const DEFAULT_THEME: Theme = 'dark'

/** data-theme 属性名。tokens.css のセレクタと対になる。 */
const THEME_ATTRIBUTE = 'data-theme'

/**
 * テーマを DOM へ適用する。
 * 既定のダークは属性なしで表現する（tokens.css の :root がダーク）。
 */
export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement): void {
    if (theme === DEFAULT_THEME) {
        root.removeAttribute(THEME_ATTRIBUTE)
        return
    }
    root.setAttribute(THEME_ATTRIBUTE, theme)
}

/** 現在 DOM に適用されているテーマを返す。 */
export function currentTheme(root: HTMLElement = document.documentElement): Theme {
    return root.getAttribute(THEME_ATTRIBUTE) === 'light' ? 'light' : DEFAULT_THEME
}
