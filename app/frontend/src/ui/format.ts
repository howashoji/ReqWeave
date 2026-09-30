/*
 * 画面共通の表示整形（モノスペースで表示する ID・日時・件数・消費量の表示値づくり）。
 *
 * `screens/` と `ui/` の双方が使うため `ui/` に置き `ui/index.ts` から公開する。
 * `screens/` に置くと ui → screens の逆向き依存になる。
 * **日時・件数の表記をここ以外に作らない**（画面ごとに書式が分かれると表記ゆれになる）。
 *
 * 保存値は UTC の ISO 8601 であり、表示のときにローカルへ変換する。
 * 桁区切りは ICU のロケール実装に依存させない（環境差で表示が変わらないようにする）。
 */

/** UTC の ISO 8601 をローカル表示（YYYY-MM-DD HH:MM）へ変換する。空・不正値は「—」 */
export function formatLocal(iso: string): string {
    if (!iso) {
        return '—'
    }
    const at = new Date(iso)
    if (Number.isNaN(at.getTime())) {
        return '—'
    }
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ${pad(at.getHours())}:${pad(at.getMinutes())}`
}

/** 当月の初日・末日（ローカル暦日）。期間指定の既定値に使う */
export function defaultPeriod(now: Date): {from: string; to: string} {
    const pad = (n: number) => String(n).padStart(2, '0')
    const fmt = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
    return {
        from: fmt(new Date(now.getFullYear(), now.getMonth(), 1)),
        to: fmt(new Date(now.getFullYear(), now.getMonth() + 1, 0)),
    }
}

/** 3 桁区切りの整数表記（消費量・件数の表示用） */
export function formatCount(value: number): string {
    const sign = value < 0 ? '-' : ''
    const digits = String(Math.abs(Math.trunc(value)))
    return sign + digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

/** 比率（0〜1）を百分率の表記にする。未設定（null / undefined）は「—」 */
export function formatRatio(ratio: number | null | undefined): string {
    if (ratio === null || ratio === undefined) {
        return '—'
    }
    return `${(ratio * 100).toFixed(1)}%`
}
