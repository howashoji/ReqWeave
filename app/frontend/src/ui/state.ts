/*
 * 状態色の意味論。
 * 緑 = 正常・進行・承認 / 橙 = 未決・警告 / 赤 = 期限超過・エラー・停止 / 青 = 候補・情報。
 * **この 4 つ以外に意味色を増やさない**（色の意味がぶれると、色で状態を読めなくなる）。
 */
export const STATE_TONES = ['accent', 'warn', 'danger', 'info'] as const

export type StateTone = (typeof STATE_TONES)[number]
