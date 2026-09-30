import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {ChangeSummaryPanel} from './ChangeSummaryPanel'
import {binding} from '../../wailsjs/go/models'

const SUMMARY = {
    incorporation: 'a1b2c3',
    noIncorporation: false,
    counts: {decisions: 1, members: 1},
    items: [
        {
            kind: 'decisions',
            kindLabel: '決定事項',
            target: 'DEC-002',
            summary: '対象は国内倉庫のみとする。',
            author: '鈴木',
            at: '2026-08-31 09:10',
        },
        {
            kind: 'members',
            kindLabel: 'メンバー',
            target: 'y.tanaka@example.co.jp',
            summary: '田中（y.tanaka@example.co.jp / 編集）',
            author: '佐藤',
            at: '2026-08-31 09:20',
        },
    ],
} as unknown as binding.ChangeSummaryView

describe('変更要約パネル', () => {
    // 区分別の件数と一覧（作業者名・日時つき）を表示する。
    it('区分ごとの件数と一覧を作業者名・日時つきで表示する', () => {
        render(
            <ChangeSummaryPanel summary={SUMMARY} onOpenRecord={() => undefined} onClose={() => undefined} />,
        )
        const panel = screen.getByLabelText('取り込んだ変更')
        expect(within(panel).getByText(/決定事項 1 件/)).toBeTruthy()
        expect(within(panel).getByText(/メンバー 1 件/)).toBeTruthy()
        expect(within(panel).getByText('対象は国内倉庫のみとする。')).toBeTruthy()
        expect(within(panel).getByText(/鈴木 ／ 2026-08-31 09:10/)).toBeTruthy()
        expect(within(panel).getByText(/前回確認した取り込み以降/)).toBeTruthy()
    })

    // 取り込みの位置は端末内の識別子であり画面へ出さない（git を露出させない）。
    it('取り込みの位置を画面へ出さない', () => {
        const {container} = render(
            <ChangeSummaryPanel summary={SUMMARY} onOpenRecord={() => undefined} onClose={() => undefined} />,
        )
        expect(container.textContent).not.toContain('a1b2c3')
    })

    // 各項目から該当レコードへ遷移できる。
    it('項目から該当レコードへ遷移できる', () => {
        const onOpenRecord = vi.fn()
        render(
            <ChangeSummaryPanel summary={SUMMARY} onOpenRecord={onOpenRecord} onClose={() => undefined} />,
        )
        fireEvent.click(screen.getByRole('button', {name: 'DEC-002'}))
        expect(onOpenRecord).toHaveBeenCalledWith('DEC-002')
    })

    // 変更が無いときは「変更なし」と判別できる（空のパネルを出しっぱなしにしない）。
    it('変更が無いときは案内を表示する', () => {
        render(
            <ChangeSummaryPanel
                summary={
                    {
                        noIncorporation: false,
                        notice: '前回確認した取り込み以降、他の作業者による変更はありません。',
                    } as unknown as binding.ChangeSummaryView
                }
                onOpenRecord={() => undefined}
                onClose={() => undefined}
            />,
        )
        expect(screen.getByText(/変更はありません/)).toBeInTheDocument()
        // 「無い」だけで終わらせず、どうなると増えるかまで書く（空状態を行き止まりにしない）。
        expect(
            screen.getByText('同期先から取り込むたびに、他の作業者の変更がここへ要約されます。'),
        ).toBeInTheDocument()
    })

    it('閉じる操作を返す', () => {
        const onClose = vi.fn()
        render(<ChangeSummaryPanel summary={SUMMARY} onOpenRecord={() => undefined} onClose={onClose} />)
        fireEvent.click(screen.getByRole('button', {name: '閉じる'}))
        expect(onClose).toHaveBeenCalledTimes(1)
    })
})
