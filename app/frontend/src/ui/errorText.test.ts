import {describe, expect, it} from 'vitest'
import {errorText} from './errorText'

// 画面のエラー表示に「Error: 」を出さない（利用者に内部の書式を見せない）。
describe('errorText', () => {
    it('Wails が Error で包んだエラーは文言だけを返す', () => {
        expect(errorText(new Error('保存先を書き込めません。空き容量を確認してください。'))).toBe(
            '保存先を書き込めません。空き容量を確認してください。',
        )
    })

    it('文字列化済みの「Error: 」も外す', () => {
        expect(errorText('Error: 接続できません')).toBe('接続できません')
        expect(errorText(new Error('Error: 二重に包まれた'))).toBe('二重に包まれた')
    })

    it('文字列のエラーはそのまま返す', () => {
        expect(errorText('期限は年-月-日で入れてください。')).toBe('期限は年-月-日で入れてください。')
    })

    it('文言が空なら「原因＋次の行動」の既定文にする（空のバナーを出さない）', () => {
        expect(errorText(new Error(''))).toBe('処理を完了できませんでした。もう一度お試しください。')
        expect(errorText(undefined)).toBe('処理を完了できませんでした。もう一度お試しください。')
    })
})
