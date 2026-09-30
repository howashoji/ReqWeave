import {describe, expect, it} from 'vitest'
import {defaultPeriod, formatCount, formatLocal, formatRatio} from './format'

/*
 * 表示整形の単体テスト。7 画面が参照する単一実装。
 *
 * **タイムゾーンは vite.config.ts の `test.env.TZ = 'Asia/Tokyo'` で固定**している
 * （固定しないと期待値が実行ホストに依存する）。
 * 固定が効いていることを最初のテストで確かめる（設定が外れたら以降の期待値も無意味になるため）。
 *
 * 保存値は UTC の ISO 8601 で、表示時にローカルへ変換する。
 */

describe('テストのタイムゾーン固定（この前提が崩れると以降の期待値が無意味になる）', () => {
    it('JST に固定されている', () => {
        expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe('Asia/Tokyo')
        expect(new Date('2026-09-02T15:00:00Z').getHours()).toBe(0)
    })
})

describe('formatLocal — UTC の保存値をローカル表示へ', () => {
    it('空・不正値は「—」', () => {
        expect(formatLocal('')).toBe('—')
        expect(formatLocal('not-a-date')).toBe('—')
        expect(formatLocal('2026-13-45T99:99:99Z')).toBe('—')
    })

    it('JST の日付境界（0:00 の前後）で暦日が正しく変わる', () => {
        // UTC 15:00 = JST 翌日 0:00
        expect(formatLocal('2026-09-02T15:00:00Z')).toBe('2026-09-03 00:00')
        // その 1 分前は前日のまま
        expect(formatLocal('2026-09-02T14:59:00Z')).toBe('2026-09-02 23:59')
    })

    it('JST 8:59 / 9:00（UTC の日付が変わる時刻）でも暦日がずれない', () => {
        expect(formatLocal('2026-09-02T23:59:00Z')).toBe('2026-09-03 08:59')
        expect(formatLocal('2026-09-03T00:00:00Z')).toBe('2026-09-03 09:00')
    })

    it('月・日・時・分を 2 桁へゼロ埋めする', () => {
        expect(formatLocal('2026-01-05T00:04:00Z')).toBe('2026-01-05 09:04')
    })

    it('オフセット付きの表記でも同じ時点なら同じ表示になる', () => {
        expect(formatLocal('2026-09-03T00:00:00+09:00')).toBe('2026-09-03 00:00')
        expect(formatLocal('2026-09-02T15:00:00Z')).toBe('2026-09-03 00:00')
    })
})

describe('defaultPeriod — 当月の初日・末日（ローカル暦日）', () => {
    it('31 日の月', () => {
        expect(defaultPeriod(new Date(2026, 0, 15))).toEqual({from: '2026-01-01', to: '2026-01-31'})
    })

    it('30 日の月', () => {
        expect(defaultPeriod(new Date(2026, 3, 1))).toEqual({from: '2026-04-01', to: '2026-04-30'})
    })

    it('平年の 2 月は 28 日まで', () => {
        expect(defaultPeriod(new Date(2026, 1, 28))).toEqual({from: '2026-02-01', to: '2026-02-28'})
    })

    it('閏年の 2 月は 29 日まで', () => {
        expect(defaultPeriod(new Date(2028, 1, 10))).toEqual({from: '2028-02-01', to: '2028-02-29'})
    })

    it('12 月でも翌年へ回り込まない', () => {
        expect(defaultPeriod(new Date(2026, 11, 31))).toEqual({from: '2026-12-01', to: '2026-12-31'})
    })
})

describe('formatCount — 3 桁区切り（ICU に依存しない）', () => {
    it('区切りの要否', () => {
        expect(formatCount(0)).toBe('0')
        expect(formatCount(999)).toBe('999')
        expect(formatCount(1000)).toBe('1,000')
        expect(formatCount(1234567)).toBe('1,234,567')
    })

    it('負数は符号を保ったまま区切る', () => {
        expect(formatCount(-1234)).toBe('-1,234')
    })

    it('小数は切り捨てる（0 方向へ）', () => {
        expect(formatCount(1234.9)).toBe('1,234')
        expect(formatCount(-1234.9)).toBe('-1,234')
    })
})

describe('formatRatio — 百分率', () => {
    it('未設定は「—」', () => {
        expect(formatRatio(null)).toBe('—')
        expect(formatRatio(undefined)).toBe('—')
    })

    it('端の値', () => {
        expect(formatRatio(0)).toBe('0.0%')
        expect(formatRatio(1)).toBe('100.0%')
    })

    it('小数第 1 位までに丸める', () => {
        expect(formatRatio(0.8)).toBe('80.0%')
        expect(formatRatio(0.125)).toBe('12.5%')
        expect(formatRatio(0.4567)).toBe('45.7%')
    })
})
