import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {expectDisabledReason} from '../test/interact'
import {describe, expect, it, vi} from 'vitest'
import {
    AppShell,
    Banner,
    Button,
    Chip,
    DataList,
    DocumentWorkspace,
    STATE_TONES,
    Toast,
    ScaleNotice,
    UsageNotice,
    Utterance,
    Wizard,
} from './index'

// 3 ペインは幅の保存にバインディングを使う。ここでは既定値を返す。
const paneWidths = vi.hoisted(() => vi.fn())
const setPaneWidths = vi.hoisted(() => vi.fn())
vi.mock('../../wailsjs/go/binding/API', () => ({
    PaneWidths: paneWidths,
    SetPaneWidths: setPaneWidths,
}))
paneWidths.mockResolvedValue({versions: 220, checks: 360, min: 160, max: 640})
setPaneWidths.mockResolvedValue(undefined)

const completeness = {
    sections: [
        {label: '背景・目的', percent: 100},
        {label: '機能要件', percent: 60},
        {label: '非機能要件', percent: 20},
    ],
}
const counts = {decided: 12, open: 3, candidates: 5}

describe('AppShell — 3 層構造と常時可視', () => {
    function renderShell(usage = {consumed: 12345, limit: null as number | null}) {
        return render(
            <AppShell
                breadcrumb={['reqweave', '在庫管理システム', '要件定義']}
                nav={<Button>対話</Button>}
                completeness={completeness}
                counts={counts}
                effort="標準"
                model="claude-opus-5"
                usage={usage}
            >
                <p>作業領域</p>
            </AppShell>,
        )
    }

    it('パンくずを「reqweave / プロジェクト / フェーズ」の順で表示する', () => {
        renderShell()
        const crumb = screen.getByLabelText('現在地')
        expect(crumb.textContent).toBe('reqweave / 在庫管理システム / 要件定義')
    })

    it('完成度を章観点ごとの充足率と全体値で示す', () => {
        renderShell()
        const strip = screen.getByLabelText('プロジェクトの状態')
        // 全体値 = 各章観点の平均（100 + 60 + 20）/ 3 = 60
        expect(within(strip).getByText('60%')).toBeInTheDocument()
        for (const s of completeness.sections) {
            expect(within(strip).getByLabelText(`${s.label} ${s.percent}%`)).toBeInTheDocument()
        }
    })

    it('決定・未決・候補の件数を常時表示する', () => {
        renderShell()
        const strip = screen.getByLabelText('プロジェクトの状態')
        expect(within(strip).getByText('決定 12')).toBeInTheDocument()
        expect(within(strip).getByText('未決 3')).toBeInTheDocument()
        expect(within(strip).getByText('候補 5')).toBeInTheDocument()
    })

    it('ステータスラインにエフォート・モデル・トークン消費を出す', () => {
        renderShell()
        expect(screen.getByText('標準')).toBeInTheDocument()
        expect(screen.getByText('claude-opus-5')).toBeInTheDocument()
        expect(screen.getByText('12,345')).toBeInTheDocument()
    })

    it('上限設定時は「消費 / 上限」の分母付きで残量が読める', () => {
        renderShell({consumed: 12345, limit: 200000})
        expect(screen.getByText('12,345 / 200,000')).toBeInTheDocument()
    })
})

describe('DataList — 一覧・管理系', () => {
    const columns = [
        {key: 'id', label: 'ID', mono: true},
        {key: 'title', label: '対象'},
        {key: 'state', label: '状態'},
    ]
    const rows = [
        {id: 'FR-DLG-001', cells: {id: 'FR-DLG-001', title: '質問生成', state: <Chip tone="accent">確定</Chip>}},
        {id: 'FR-DLG-002', cells: {id: 'FR-DLG-002', title: '候補抽出', state: <Chip tone="warn">未決</Chip>}},
    ]

    it('見出し行の小ラベルはモノスペースで、faint ではなく muted 系のクラスを持つ', () => {
        render(<DataList caption="要件項目" columns={columns} rows={rows} empty={{message: '要件項目はまだありません。', next: '対話で答えると増えます。'}} />)
        const head = screen.getAllByRole('row')[0]
        expect(head.className).toContain('rw-mono')
        expect(head.className).toContain('rw-list__head')
        for (const c of columns) {
            expect(within(head).getByText(c.label)).toBeInTheDocument()
        }
    })

    it('mono 指定の列だけがモノスペースになる（識別子・数値に限定）', () => {
        render(<DataList caption="要件項目" columns={columns} rows={rows} empty={{message: '要件項目はまだありません。', next: '対話で答えると増えます。'}} />)
        const cells = screen.getAllByRole('cell')
        expect(cells[0].className).toContain('rw-mono') // ID 列
        expect(cells[1].className).not.toContain('rw-mono') // 対象（本文）
    })

    it('選択行に選択状態のクラスと aria-selected が付く', () => {
        render(<DataList caption="要件項目" columns={columns} rows={rows} selectedId="FR-DLG-002" empty={{message: '要件項目はまだありません。', next: '対話で答えると増えます。'}} />)
        const dataRows = screen.getAllByRole('row').slice(1)
        expect(dataRows[0]).toHaveAttribute('aria-selected', 'false')
        expect(dataRows[1]).toHaveAttribute('aria-selected', 'true')
        expect(dataRows[1].className).toContain('rw-list__row--selected')
    })

    /*
     * 0 件の表示は「何が無いか」だけで終わらせない（空状態を行き止まりにしない）。
     * 既定の文言を持たせると、呼び出し側が何も考えないまま行き止まりの画面ができる。
     * そのため `empty` は必須の prop であり、次の一手か「利用者の操作では増えない理由」を型で求める。
     */
    it('0 件のときは「何が無いか」と「次に何をすると埋まるか」を出す', () => {
        render(
            <DataList
                caption="要件項目"
                columns={columns}
                rows={[]}
                empty={{
                    message: '要件項目はまだありません。',
                    next: '対話画面で「次の質問」に答えると増えます。',
                }}
            />,
        )
        expect(screen.getByText('要件項目はまだありません。')).toBeInTheDocument()
        expect(screen.getByText('対話画面で「次の質問」に答えると増えます。')).toBeInTheDocument()
    })

    it('利用者の操作では増えないものは、増える条件を説明する（次の一手を書かない）', () => {
        render(
            <DataList
                caption="自動退避の世代"
                columns={columns}
                rows={[]}
                empty={{
                    message: '自動退避はまだありません。',
                    automatic: 'プロジェクトを閉じるたびに自動で作成されます。',
                }}
            />,
        )
        expect(screen.getByText('プロジェクトを閉じるたびに自動で作成されます。')).toBeInTheDocument()
    })
})

describe('Utterance — 対話系の行形式', () => {
    it('話者・タイムスタンプ・本文を出し、話者で左ボーダーのクラスを分ける', () => {
        const {container} = render(
            <>
                <Utterance speaker="ai" name="claude-opus-5" timestamp="2026-08-27 11:20" body="要件を確認します。" />
                <Utterance speaker="human" name="鈴木" timestamp="2026-08-27 11:21" body="お願いします。" />
            </>,
        )
        const articles = container.querySelectorAll('article')
        expect(articles[0].className).toContain('rw-utterance--ai')
        expect(articles[1].className).toContain('rw-utterance--human')
        expect(screen.getByText('claude-opus-5')).toBeInTheDocument()
        expect(screen.getByText('2026-08-27 11:21')).toBeInTheDocument()
        expect(screen.getByText('お願いします。')).toBeInTheDocument()
    })

    it('中断された発話には「中断」を表示する', () => {
        render(<Utterance speaker="ai" name="claude-opus-5" timestamp="11:22" body="途中で" interrupted />)
        expect(screen.getByText('中断')).toBeInTheDocument()
    })
})

describe('DocumentWorkspace — 文書・承認系', () => {
    it('3 ペインを持ち、緑塗りのプライマリ操作は 1 つだけになる', () => {
        render(
            <DocumentWorkspace
                versionsPane={<p>v1 / v2</p>}
                documentPane={<p>本文</p>}
                checksPane={<p>検証 OK</p>}
                primaryAction={{label: '確定する', onClick: vi.fn()}}
                secondaryActions={[{label: '差分を再生成', onClick: vi.fn()}]}
                cancelAction={{label: '取消', onClick: vi.fn()}}
            />,
        )
        expect(screen.getByLabelText('対象・版の一覧')).toBeInTheDocument()
        expect(screen.getByLabelText('本文・差分')).toBeInTheDocument()
        expect(screen.getByLabelText('チェック結果・操作')).toBeInTheDocument()

        const primary = screen.getAllByRole('button').filter((b) => b.dataset.variant === 'primary')
        expect(primary).toHaveLength(1)
        expect(primary[0]).toHaveTextContent('確定する')
        expect(screen.getByText('差分を再生成').dataset.variant).toBe('secondary')
        expect(screen.getByText('取消').dataset.variant).toBe('quiet')
    })

    it('無効化されたプライマリ操作は理由を伴う', () => {
        render(
            <DocumentWorkspace
                versionsPane={null}
                documentPane={null}
                checksPane={null}
                primaryAction={{
                    label: '確定する',
                    onClick: vi.fn(),
                    disabledReason: '未決事項が残っているため確定できません。未決事項一覧で解消してください。',
                }}
            />,
        )
        const reason = '未決事項が残っているため確定できません。未決事項一覧で解消してください。'
        const primary = screen.getByText('確定する')
        // 理由はホバーしなくても読める。
        expect(screen.getByRole('status')).toHaveTextContent(reason)
        // 重ねたときも同じ文が出る。
        expectDisabledReason(primary, reason)
    })
})

describe('DocumentWorkspace のペイン幅', () => {
    function renderWorkspace() {
        return render(
            <DocumentWorkspace
                versionsPane={<p>版</p>}
                documentPane={<p>本文</p>}
                checksPane={<p>チェック</p>}
                primaryAction={{label: '確定する', onClick: vi.fn()}}
            />,
        )
    }

    it('保存済みの幅で開き、境界のつまみを持つ', async () => {
        paneWidths.mockResolvedValue({versions: 300, checks: 420, min: 160, max: 640})
        renderWorkspace()

        const left = await screen.findByRole('separator', {name: '左のペインの幅'})
        const right = screen.getByRole('separator', {name: '右のペインの幅'})
        await waitFor(() => expect(left).toHaveAttribute('aria-valuenow', '300'))
        expect(right).toHaveAttribute('aria-valuenow', '420')
        // 許容範囲も画面へ渡る（範囲を UI 側に二重に持たない）
        expect(left).toHaveAttribute('aria-valuemin', '160')
        expect(left).toHaveAttribute('aria-valuemax', '640')
    })

    it('ドラッグで幅が変わり、離したときに端末ごとの設定へ保存する', async () => {
        paneWidths.mockResolvedValue({versions: 220, checks: 360, min: 160, max: 640})
        setPaneWidths.mockClear()
        renderWorkspace()
        const left = await screen.findByRole('separator', {name: '左のペインの幅'})
        await waitFor(() => expect(left).toHaveAttribute('aria-valuenow', '220'))

        fireEvent.mouseDown(left, {clientX: 220})
        fireEvent.mouseMove(window, {clientX: 300})
        await waitFor(() => expect(left).toHaveAttribute('aria-valuenow', '300'))
        // ドラッグ中は保存しない（離したときに 1 回だけ）
        expect(setPaneWidths).not.toHaveBeenCalled()

        fireEvent.mouseUp(window)
        await waitFor(() => expect(setPaneWidths).toHaveBeenCalledWith(300, 360))
    })

    it('キーボードでも幅を変えられ、許容範囲を超えない', async () => {
        paneWidths.mockResolvedValue({versions: 176, checks: 360, min: 160, max: 640})
        setPaneWidths.mockClear()
        renderWorkspace()
        const left = await screen.findByRole('separator', {name: '左のペインの幅'})
        await waitFor(() => expect(left).toHaveAttribute('aria-valuenow', '176'))

        fireEvent.keyDown(left, {key: 'ArrowRight'})
        await waitFor(() => expect(left).toHaveAttribute('aria-valuenow', '192'))

        // 下限を下回らない（176 → 160 → それ以上は下がらない）
        fireEvent.keyDown(left, {key: 'ArrowLeft'})
        fireEvent.keyDown(left, {key: 'ArrowLeft'})
        fireEvent.keyDown(left, {key: 'ArrowLeft'})
        await waitFor(() => expect(left).toHaveAttribute('aria-valuenow', '160'))
        expect(setPaneWidths).toHaveBeenLastCalledWith(160, 360)
    })
})

describe('Wizard — ウィザード・回答系', () => {
    it('進捗（n / 総数）と自動保存の明示を出し、統制表示を一切出さない', () => {
        render(
            <Wizard title="現在の在庫はどこで管理していますか" step={3} total={12} onNext={vi.fn()} onBack={vi.fn()}>
                <p>回答欄</p>
            </Wizard>,
        )
        expect(screen.getByLabelText('進捗')).toHaveTextContent('3 / 12')
        expect(
            screen.getByText('回答は自動保存されます。中断しても同じところから再開できます。'),
        ).toBeInTheDocument()
        // 担当者向けの統制表示（完成度・トークン消費・モデル・エフォート）は出さない
        expect(screen.queryByLabelText('プロジェクトの状態')).toBeNull()
        expect(screen.queryByText(/トークン/)).toBeNull()
        expect(screen.queryByText(/エフォート/)).toBeNull()
    })

    it('最初の項目では戻るが無効になり、理由が付く', () => {
        render(
            <Wizard title="最初の質問" step={1} total={5} onNext={vi.fn()}>
                <p>回答欄</p>
            </Wizard>,
        )
        const back = screen.getByText('戻る')
        expectDisabledReason(back, '最初の項目のため戻れません。')
    })
})

describe('Banner — パネル・バナー系', () => {
    it.each(STATE_TONES)('種別 %s を左ボーダー色のクラスで示す', (tone) => {
        render(
            <Banner tone={tone} title="通知" onDismiss={vi.fn()} onDefer={vi.fn()}>
                詳細
            </Banner>,
        )
        expect(screen.getByRole('status').className).toContain(`rw-banner--${tone}`)
    })

    it('閉じる・あとで見るの導線を必ず持つ', () => {
        const onDismiss = vi.fn()
        const onDefer = vi.fn()
        render(<Banner tone="warn" title="残りトークンが少なくなっています" onDismiss={onDismiss} onDefer={onDefer} />)
        fireEvent.click(screen.getByText('閉じる'))
        fireEvent.click(screen.getByText('あとで見る'))
        expect(onDismiss).toHaveBeenCalledTimes(1)
        expect(onDefer).toHaveBeenCalledTimes(1)
    })
})

describe('Toast — 一過性の操作フィードバック（パネル・バナー系）', () => {
    it.each(STATE_TONES)('種別 %s を左ボーダー色のクラスで示す', (tone) => {
        render(<Toast tone={tone} message="保存しました。" onDismiss={vi.fn()} />)
        expect(screen.getByRole('status').className).toContain(`rw-toast--${tone}`)
    })

    it('自動で消える前にも閉じられる', () => {
        const onDismiss = vi.fn()
        render(<Toast tone="accent" message="保存しました。" onDismiss={onDismiss} durationMs={0} />)
        fireEvent.click(screen.getByRole('button', {name: '通知を閉じる'}))
        expect(onDismiss).toHaveBeenCalledTimes(1)
    })

    it('指定時間が過ぎると自動で閉じる（操作を残したままにしない）', () => {
        vi.useFakeTimers()
        try {
            const onDismiss = vi.fn()
            render(<Toast tone="accent" message="コピーしました。" onDismiss={onDismiss} durationMs={6000} />)
            expect(onDismiss).not.toHaveBeenCalled()
            vi.advanceTimersByTime(6000)
            expect(onDismiss).toHaveBeenCalledTimes(1)
        } finally {
            vi.useRealTimers()
        }
    })

    it('durationMs が 0 以下なら自動で閉じない（閉じる操作だけで消す）', () => {
        vi.useFakeTimers()
        try {
            const onDismiss = vi.fn()
            render(<Toast tone="danger" message="コピーできませんでした。" onDismiss={onDismiss} durationMs={0} />)
            vi.advanceTimersByTime(60000)
            expect(onDismiss).not.toHaveBeenCalled()
        } finally {
            vi.useRealTimers()
        }
    })
})

describe('UsageNotice — 上限の警告・停止表示', () => {
    const WARN = {
        consumedTokens: 82000,
        missingRecords: 0,
        limitTokens: 100000,
        warnRatio: 0.8,
        remainingTokens: 18000,
        consumptionRatio: 0.82,
        level: 'warn',
    }
    const BLOCKED = {...WARN, consumedTokens: 100000, remainingTokens: 0, consumptionRatio: 1, level: 'blocked'}

    it('上限未設定・閾値未満では何も表示しない', () => {
        const {container} = render(
            <UsageNotice
                status={{consumedTokens: 10, missingRecords: 0, warnRatio: 0, level: 'none'}}
                onOpenDashboard={vi.fn()}
                onDismiss={vi.fn()}
            />,
        )
        expect(container.textContent).toBe('')
        const {container: empty} = render(
            <UsageNotice status={null} onOpenDashboard={vi.fn()} onDismiss={vi.fn()} />,
        )
        expect(empty.textContent).toBe('')
    })

    it('警告閾値以上では橙で残りトークン数を示す（操作は妨げない）', () => {
        render(<UsageNotice status={WARN} onOpenDashboard={vi.fn()} onDismiss={vi.fn()} />)
        const banner = screen.getByRole('status')
        expect(banner.getAttribute('data-tone')).toBe('warn')
        expect(banner.textContent).toContain('残り 18,000 トークン')
        // 色以外の手掛かり（色だけに頼らない）。
        expect(banner.textContent).toContain('警告')
        expect(banner.textContent).toContain('このまま続けられます')
    })

    it('上限到達では赤で理由・再開方法・ダッシュボードへの導線を示す', () => {
        const onOpenDashboard = vi.fn()
        render(<UsageNotice status={BLOCKED} onOpenDashboard={onOpenDashboard} onDismiss={vi.fn()} />)
        const banner = screen.getByRole('status')
        expect(banner.getAttribute('data-tone')).toBe('danger')
        expect(banner.textContent).toContain('停止')
        expect(banner.textContent).toContain('上限に達したため')
        expect(banner.textContent).toContain('オーナーが上限を変更すると再開できます')
        expect(banner.textContent).toContain('AI を使わない操作は続けられます')
        fireEvent.click(within(banner).getByRole('button', {name: 'AI 利用量を確認する'}))
        expect(onOpenDashboard).toHaveBeenCalledTimes(1)
    })

    it('警告と上限到達は文言でも区別できる（色だけに頼らない）', () => {
        const {unmount} = render(<UsageNotice status={WARN} onOpenDashboard={vi.fn()} onDismiss={vi.fn()} />)
        const warnText = screen.getByRole('status').textContent ?? ''
        unmount()
        render(<UsageNotice status={BLOCKED} onOpenDashboard={vi.fn()} onDismiss={vi.fn()} />)
        const blockedText = screen.getByRole('status').textContent ?? ''
        expect(warnText).not.toBe(blockedText)
        expect(warnText.startsWith('警告')).toBe(true)
        expect(blockedText.startsWith('停止')).toBe(true)
    })
})

describe('ScaleNotice — 規模上限の予告・到達表示', () => {
    const WARN = {
        kind: 'sessions',
        label: '対話セッション',
        level: 'warn',
        message:
            '対話セッションが 80 件です（上限の目安 100 件）。上限に近づいています。' +
            '上限を超えても操作は続けられますが、動作の速さは性能保証の対象外になります。',
    }
    const EXCEEDED = {
        kind: 'totalBytes',
        label: 'プロジェクトの総量',
        level: 'exceeded',
        message:
            'プロジェクトの総量が 500.0 MB に達しました（上限の目安 500.0 MB）。' +
            'このまま操作は続けられますが、動作の速さは性能保証の対象外になります。' +
            '不要な項目の整理や、プロジェクトの分割をご検討ください。',
    }

    it('通知が無ければ何も表示しない', () => {
        const {container} = render(<ScaleNotice warnings={[]} />)
        expect(container.textContent).toBe('')
        const {container: none} = render(<ScaleNotice warnings={null} />)
        expect(none.textContent).toBe('')
    })

    it('予告は橙で、操作を続けられることを本文で示す', () => {
        render(<ScaleNotice warnings={[WARN]} />)
        const banner = screen.getByRole('status')
        expect(banner.getAttribute('data-tone')).toBe('warn')
        expect(banner.textContent).toContain('予告')
        expect(banner.textContent).toContain('対話セッション')
        expect(banner.textContent).toContain('80 件')
        expect(banner.textContent).toContain('操作は続けられます')
    })

    it('上限到達は赤だが「操作は続けられる」ことを明示する（拒否ではない）', () => {
        render(<ScaleNotice warnings={[EXCEEDED]} />)
        const banner = screen.getByRole('status')
        expect(banner.getAttribute('data-tone')).toBe('danger')
        expect(banner.textContent).toContain('上限到達')
        expect(banner.textContent).toContain('性能保証の対象外')
        expect(banner.textContent).toContain('このまま操作は続けられます')
    })

    it('予告と上限到達は文言でも区別できる（色だけに頼らない）', () => {
        const {unmount} = render(<ScaleNotice warnings={[WARN]} />)
        const warnText = screen.getByRole('status').textContent ?? ''
        unmount()
        render(<ScaleNotice warnings={[EXCEEDED]} />)
        const exceededText = screen.getByRole('status').textContent ?? ''
        expect(warnText).not.toBe(exceededText)
        expect(warnText.startsWith('予告')).toBe(true)
        expect(exceededText.startsWith('上限到達')).toBe(true)
    })

    it('複数の対象が同時に上限へ近づいたら、それぞれを 1 件ずつ出す', () => {
        render(<ScaleNotice warnings={[WARN, EXCEEDED]} />)
        expect(screen.getAllByRole('status')).toHaveLength(2)
    })

    it('閉じた対象は出し直さない（保存のたびの再評価で再表示しない）', () => {
        render(<ScaleNotice warnings={[WARN, EXCEEDED]} />)
        const first = screen.getAllByRole('status')[0]
        fireEvent.click(within(first).getByRole('button', {name: '閉じる'}))
        const remaining = screen.getAllByRole('status')
        expect(remaining).toHaveLength(1)
        expect(remaining[0].textContent).toContain('プロジェクトの総量')
    })
})

describe('Chip — 状態色の意味論', () => {
    it('意味色は 4 色のみ', () => {
        expect([...STATE_TONES]).toEqual(['accent', 'warn', 'danger', 'info'])
    })

    it('状態ラベルはモノスペースで表示する', () => {
        render(<Chip tone="danger">期限超過</Chip>)
        expect(screen.getByText('期限超過').className).toContain('rw-mono')
    })
})
