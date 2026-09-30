import {beforeEach, describe, expect, it} from 'vitest'
import {applyTheme, currentTheme, DEFAULT_THEME} from './theme'

describe('テーマ切替（既定はダーク・OS の外観に追従しない）', () => {
    let root: HTMLElement

    beforeEach(() => {
        root = document.createElement('html')
    })

    it('既定はダーク', () => {
        expect(DEFAULT_THEME).toBe('dark')
        expect(currentTheme(root)).toBe('dark')
    })

    it('ライトへ切り替えると data-theme="light" が付く', () => {
        applyTheme('light', root)
        expect(root.getAttribute('data-theme')).toBe('light')
        expect(currentTheme(root)).toBe('light')
    })

    it('ダークへ戻すと属性が外れる（既定は属性なしで表す）', () => {
        applyTheme('light', root)
        applyTheme('dark', root)
        expect(root.hasAttribute('data-theme')).toBe(false)
        expect(currentTheme(root)).toBe('dark')
    })
})
