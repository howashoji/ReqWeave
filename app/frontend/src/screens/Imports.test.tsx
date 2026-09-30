import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {clickEnabled, expectDisabledReason} from '../test/interact'
import {GuideHintProvider} from '../ui/GuideHint'
import {Imports} from './Imports'

const importKinds = vi.hoisted(() => vi.fn())
const listImports = vi.hoisted(() => vi.fn())
const importFile = vi.hoisted(() => vi.fn())
const importClipboardText = vi.hoisted(() => vi.fn())
const chooseImportFile = vi.hoisted(() => vi.fn())
const chooseImportDirectory = vi.hoisted(() => vi.fn())
const scanImportDirectory = vi.hoisted(() => vi.fn())
const importDirectory = vi.hoisted(() => vi.fn())
const importContent = vi.hoisted(() => vi.fn())
const previewImportAnalysis = vi.hoisted(() => vi.fn())
const analyzeImport = vi.hoisted(() => vi.fn())
const approveImportCandidates = vi.hoisted(() => vi.fn())
const pendingImportCandidates = vi.hoisted(() => vi.fn())
const currentPermission = vi.hoisted(() => vi.fn())
const evidence = vi.hoisted(() => vi.fn())
const feedbackClassificationOptions = vi.hoisted(() => vi.fn())
const setFeedbackClassification = vi.hoisted(() => vi.fn())
const perspectives = vi.hoisted(() => vi.fn())
const addPerspective = vi.hoisted(() => vi.fn())
const updatePerspective = vi.hoisted(() => vi.fn())
const removePerspective = vi.hoisted(() => vi.fn())
const paneWidths = vi.hoisted(() => vi.fn())
const setPaneWidths = vi.hoisted(() => vi.fn())
// プレビューの位置まで送ったかを見る（jsdom はレイアウトを持たないため呼び出しで確かめる）。
const scrollIntoView = vi.fn()

vi.mock('../../wailsjs/go/binding/API', () => ({
    ImportKinds: importKinds,
    Imports: listImports,
    ImportFile: importFile,
    ImportClipboardText: importClipboardText,
    ChooseImportFile: chooseImportFile,
    ChooseImportDirectory: chooseImportDirectory,
    ScanImportDirectory: scanImportDirectory,
    ImportDirectory: importDirectory,
    ImportContent: importContent,
    PreviewImportAnalysis: previewImportAnalysis,
    AnalyzeImport: analyzeImport,
    ApproveImportCandidates: approveImportCandidates,
    PendingImportCandidates: pendingImportCandidates,
    CurrentPermission: currentPermission,
    Evidence: evidence,
    FeedbackClassificationOptions: feedbackClassificationOptions,
    SetFeedbackClassification: setFeedbackClassification,
    Perspectives: perspectives,
    AddPerspective: addPerspective,
    UpdatePerspective: updatePerspective,
    RemovePerspective: removePerspective,
    PaneWidths: paneWidths,
    SetPaneWidths: setPaneWidths,
}))

const KINDS = [
    {id: 'material', label: '資料', summary: '既存の業務資料'},
    {id: 'minutes', label: '議事録', summary: '打ち合わせの記録'},
    {id: 'dev-ai-feedback', label: '開発AIフィードバック', summary: '開発AIからの指摘'},
]

const MATERIAL = {
    id: 'IMP-001',
    kind: 'material',
    kindLabel: '資料',
    sourceName: '現行業務.md',
    importedAt: '2026-08-31 10:00',
    extracted: true,
    statusLabel: '抽出済み',
}

const FAILED = {
    id: 'IMP-002',
    kind: 'material',
    kindLabel: '資料',
    sourceName: 'scan.pdf',
    importedAt: '2026-08-31 10:05',
    extracted: false,
    statusLabel: '抽出できません（原本のみ保持）',
}

const FEEDBACK = {
    id: 'IMP-003',
    kind: 'dev-ai-feedback',
    kindLabel: '開発AIフィードバック',
    sourceName: 'feedback.md',
    importedAt: '2026-08-31 10:10',
    extracted: true,
    statusLabel: '抽出済み',
    classification: 'requirements-gap',
    classificationLabel: '要件定義時に確認すべきだった論点',
}

// ディレクトリ指定の一括取り込み。
const SCAN = {
    dir: '/tmp/資料一式',
    count: 3,
    totalBytes: 4096,
    totalSizeLabel: '0.0 MB',
    files: [{name: '現行業務.md', size: 2048}, {name: 'メモ.txt', size: 1024}, {name: 'sub/仕様.md', size: 1024}],
    skipped: [{name: '図.png', reason: '対象の形式ではありません（txt / md / docx / xlsx / pptx / pdf）。'}],
    skippedCount: 1,
}

const BATCH = {
    imported: [MATERIAL],
    importedCount: 3,
    failed: [{name: '壊れた.md', reason: 'ファイルを読み込めませんでした。ファイルの場所と権限を確認してください。'}],
    failedCount: 1,
    skippedCount: 1,
    notice: '3 件を取り込みました。1 件は取り込めませんでした（理由は一覧を確認してください）。分析は行っていません。',
}

/**
 * 一括取り込みの操作は、押した直後に複数の非同期呼び出し（選択 → 走査 → 一覧の読み直し）が
 * 続けて解決する。モックが即座に解決するため、`fireEvent` だけでは状態更新が act の外で起き、
 * 「not wrapped in act」の警告が出る（テストは通るがログが汚れて本当の問題が埋もれる）。
 * 押す操作を act で包み、連なる解決までを内側で終わらせる。
 */
async function clickBatch(name: string) {
    const button = await waitFor(() => {
        const found = screen.getByRole('button', {name})
        expect(found).toBeEnabled()
        return found
    })
    await act(async () => {
        fireEvent.click(button)
    })
}

const CONTENT = {
    id: 'IMP-001',
    extracted: '## 在庫管理\n\n受注確定時に在庫を引き当てる。\n棚卸は月次で行う。',
    extractionFailed: false,
    sourceText: '## 在庫管理',
    sourcePath: 'imports/IMP-001/source.md',
    sourceName: '現行業務.md',
}

const PREVIEW = {
    importId: 'IMP-001',
    kind: 'material',
    sourceName: '現行業務.md',
    system: 'あなたは要件定義を進める AIエージェントです。',
    context: '## 分析対象の資料\n\n- IMP-001',
    chunks: [{index: 1, startLine: 1, endLine: 4, text: '## 在庫管理'}],
    chunkCount: 1,
    estimatedTokens: 1200,
    tooLarge: false,
}

const ANALYSIS = {
    importId: 'IMP-001',
    kind: 'material',
    sourceName: '現行業務.md',
    chunkCount: 1,
    estimatedTokens: 1200,
    fallback: false,
    fromTemplate: false,
    extraction: {
        decisions: [
            {
                topic_key: 'business-flow/main-flow',
                body: '受注確定時に在庫を引き当てる。',
                rationale: '現行業務の記述',
                evidence_refs: ['IMP-001#L3-L3'],
                duplicate_of: 'DEC-001',
            },
            {
                topic_key: 'background/scope',
                body: '破棄する候補。',
                rationale: '',
                evidence_refs: ['IMP-001#L4-L4'],
            },
        ],
        open_issues: [
            {topic: '未選択のまま残す候補', owner: '', evidence_refs: ['IMP-001#L4-L4']},
        ],
        requirement_updates: [],
        term_candidates: [],
        contradictions: [{with_decision_id: 'DEC-002', description: '既存決定と矛盾します。'}],
        perspective_candidates: [
            {name: '棚卸の差異処理', summary: '差異の承認経路', evidence_refs: ['IMP-001#L4-L4']},
        ],
    },
}

const REGISTERED = {
    id: 'PRS-001',
    name: '棚卸の差異処理',
    summary: '差異の承認経路',
    origin: 'material-analysis',
    originLabel: '資料の分析から登録',
    evidence: 'IMP-001#L4-L4',
    topicKey: 'custom/PRS-001',
    createdAt: '2026-08-31T00:00:00Z',
    author: 'k.sato@example.co.jp',
}

/**
 * 資料を選び、同意して分析するところまで進める（根拠を特定できない候補のテストが使う）。
 *
 * describe の外に置く。it と it の間に置くと、hygiene.test.ts が直前の it の本文として読み、
 * その it の waitFor(モック) の後ろに素の click があるように見える。
 * 分析ボタンは同意の反映を待ってから押す（clickEnabled）。
 */
async function analyzeSelected() {
    fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))
    fireEvent.click(await screen.findByRole('button', {name: '送信内容を確認する'}))
    fireEvent.click(await screen.findByRole('checkbox', {name: /送信することに同意します/}))
    await clickEnabled('この内容で分析する')
}

describe('取り込み画面', () => {
    beforeEach(() => {
        importKinds.mockReset().mockResolvedValue(KINDS)
        listImports.mockReset().mockResolvedValue([MATERIAL])
        importFile.mockReset().mockResolvedValue(MATERIAL)
        importClipboardText.mockReset().mockResolvedValue(MATERIAL)
        chooseImportFile.mockReset().mockResolvedValue('/tmp/現行業務.md')
        chooseImportDirectory.mockReset().mockResolvedValue('/tmp/資料一式')
        scanImportDirectory.mockReset().mockResolvedValue(SCAN)
        importDirectory.mockReset().mockResolvedValue(BATCH)
        importContent.mockReset().mockResolvedValue(CONTENT)
        previewImportAnalysis.mockReset().mockResolvedValue(PREVIEW)
        analyzeImport.mockReset().mockResolvedValue(ANALYSIS)
        approveImportCandidates
            .mockReset()
            .mockResolvedValue({applied: {decisionIds: ['DEC-003'], perspectiveIds: ['PRS-001']}})
        pendingImportCandidates.mockReset().mockResolvedValue(null)
        currentPermission
            .mockReset()
            .mockResolvedValue({role: 'editor', roleLabel: '編集', canEdit: true, reason: ''})
        evidence.mockReset().mockResolvedValue({ref: 'IMP-001#L3-L3', found: true})
        feedbackClassificationOptions
            .mockReset()
            .mockResolvedValue([{value: 'requirements-gap', label: '要件定義時に確認すべきだった論点'}])
        setFeedbackClassification.mockReset().mockResolvedValue(FEEDBACK)
        perspectives.mockReset().mockResolvedValue([])
        addPerspective.mockReset().mockResolvedValue({id: 'PRS-001', name: '棚卸', summary: '要旨'})
        updatePerspective.mockReset().mockResolvedValue({id: 'PRS-001', name: '棚卸2', summary: '要旨2'})
        removePerspective.mockReset().mockResolvedValue(undefined)
        paneWidths.mockReset().mockResolvedValue({versions: 220, checks: 360, min: 160, max: 640})
        setPaneWidths.mockReset().mockResolvedValue(undefined)
        scrollIntoView.mockReset()
        Element.prototype.scrollIntoView = scrollIntoView
    })

    // 実データ規模の再現（利用者の環境: 抽出テキスト 122KB・送信内容 127KB）。
    it('大きな資料でも送信内容の確認が進み、プレビューと同意欄が出る', async () => {
        const big = Array.from({length: 3000}, (_, i) => `${i + 1} 行目の本文です。在庫の引き当てについて記述します。`).join('\n')
        importContent.mockResolvedValue({...CONTENT, extracted: big})
        previewImportAnalysis.mockResolvedValue({
            ...PREVIEW,
            chunks: [{index: 1, startLine: 1, endLine: 3000, text: big}],
            estimatedTokens: 58822,
        })
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))
        await waitFor(() => expect(importContent).toHaveBeenCalledWith('IMP-001'))

        // 押せる状態であること（無効なら理由が付く）
        const button = await screen.findByRole('button', {name: '送信内容を確認する'})
        expect(button).toBeEnabled()
        fireEvent.click(button)

        // 押した結果が画面で分かる（案内文・プレビュー位置への移動・次の操作の提示）
        expect(
            await screen.findByText(/送信内容を作成しました（分割 1 件・概算 58822 トークン）。/),
        ).toBeTruthy()
        await waitFor(() => expect(scrollIntoView).toHaveBeenCalled())

        // 送信内容が画面に出て、同意して分析へ進める状態になる
        expect(await screen.findByText(/分割 1 件 ／ 概算 58822 トークン/)).toBeTruthy()
        expect(screen.getByText('送信するシステムプロンプト')).toBeTruthy()
        expect(await screen.findByRole('button', {name: 'この内容で分析する'})).toBeTruthy()
    })

    // 送信内容が大きすぎるときも、理由が画面に出る（押しても何も起きないように見せない）。
    it('送信内容が大きすぎる場合は理由を案内する', async () => {
        previewImportAnalysis.mockResolvedValue({
            ...PREVIEW,
            tooLarge: true,
            notice: '分割数が上限を超えるため分析を開始できません（分割 12 件・上限 10 件）。',
            chunks: [],
        })
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))
        await waitFor(() => expect(importContent).toHaveBeenCalledWith('IMP-001'))

        fireEvent.click(await screen.findByRole('button', {name: '送信内容を確認する'}))
        // 画面上部の案内（バナー）と中央ペインの注意の両方に理由が出る
        const shown = await screen.findAllByText(/分割数が上限を超えるため分析を開始できません/)
        expect(shown.length).toBeGreaterThanOrEqual(2)
        // 大きすぎる場合は分析へ進めない（ボタンは「送信内容を確認する」のまま）
        expect(screen.queryByRole('button', {name: 'この内容で分析する'})).toBeNull()
    })

    // 候補は既定で読むだけ。直したいときだけ「編集」で入力欄へ。
    it('候補の本文は既定で読むだけで、編集を押すと入力欄になる', async () => {
        pendingImportCandidates.mockResolvedValue(ANALYSIS.extraction)
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))

        // 既定は入力欄ではない（枠・スクロールの中に本文を閉じ込めない）
        const label = '決定事項の候補 1'
        const shown = await screen.findByLabelText(label)
        expect(shown.tagName).toBe('P')
        expect(shown).toHaveTextContent('受注確定時に在庫を引き当てる。')

        // 「編集」で入力欄になり、直した内容が残る
        const article = shown.closest('article')
        expect(article).not.toBeNull()
        fireEvent.click(within(article!).getByRole('button', {name: '編集'}))
        const input = await screen.findByLabelText(label)
        expect(input.tagName).toBe('TEXTAREA')
        fireEvent.change(input, {target: {value: '受注確定時に引き当てる（修正）。'}})

        // 「編集をやめる」で読むだけへ戻り、直した内容が表示される
        fireEvent.click(within(article!).getByRole('button', {name: '編集をやめる'}))
        const back = await screen.findByLabelText(label)
        expect(back.tagName).toBe('P')
        expect(back).toHaveTextContent('受注確定時に引き当てる（修正）。')
    })

    // 一覧の 1 件はカード全体が押せる（資料名だけが押せる状態にしない）。
    it('取り込み済みのカードは資料名以外の場所を押しても選べ、選択状態を示す', async () => {
        listImports.mockResolvedValue([MATERIAL, FAILED])
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')

        // 日時・種別のメタ行（資料名ではない部分）を押しても、その資料が選ばれる。
        const meta = screen.getByText(/資料 ／ 2026-08-31 10:05/)
        fireEvent.click(meta)
        await waitFor(() => expect(importContent).toHaveBeenCalledWith('IMP-002'))

        // 選択中はボタンの押下状態で示し、緑塗り（primary = 1 画面 1 つ）を使わない。
        const card = screen.getByRole('button', {name: /scan\.pdf/})
        await waitFor(() => expect(card).toHaveAttribute('aria-pressed', 'true'))
        expect(card.dataset.variant).toBeUndefined()
        expect(screen.getByRole('button', {name: /現行業務\.md/})).toHaveAttribute('aria-pressed', 'false')

        // 状態チップもカードの中にあり、押せば同じ資料が選ばれる（押せない場所を作らない）。
        fireEvent.click(screen.getByText('抽出済み'))
        await waitFor(() => expect(importContent).toHaveBeenCalledWith('IMP-001'))
    })

    // フォルダを選ぶと一覧と件数が出て、確認するまで取り込まない。分析は行わない。
    it('フォルダ指定は対象の一覧と件数を提示し、確認してから一括で取り込む', async () => {
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')

        await clickBatch('フォルダを選ぶ（一括）')
        const panel = await screen.findByLabelText('一括取り込みの確認')
        // 件数・合計の大きさ・適用する種別を示す
        expect(within(panel).getByText(/3 件（0\.0 MB）を「資料」として取り込みます。/)).toBeTruthy()
        // 対象の一覧（サブフォルダを含む）
        expect(within(panel).getByText('sub/仕様.md')).toBeTruthy()
        // 対象外は件数と理由を示す
        expect(within(panel).getByText(/取り込まないもの 1 件/)).toBeTruthy()
        expect(within(panel).getByText(/図\.png — 対象の形式ではありません/)).toBeTruthy()
        // 分析を自動で行わないことを明示する
        expect(within(panel).getByText(/AI での分析は行いません/)).toBeTruthy()
        // 確認前は 1 件も取り込まない
        expect(importDirectory).not.toHaveBeenCalled()

        await clickBatch('この内容で取り込む')
        // 待つ対象と確かめる対象を一致させる（画面に結果が出るまで待ってから中身を見る）
        expect(await screen.findByText(/3 件を取り込みました。/)).toBeTruthy()
        expect(importDirectory).toHaveBeenCalledWith('/tmp/資料一式', 'material')
        // 取り込めなかったものは理由つきで示す
        const failedPanel = await screen.findByLabelText('取り込めなかったファイル')
        expect(within(failedPanel).getByText(/壊れた\.md — ファイルを読み込めませんでした。/)).toBeTruthy()
        // 取り込み後は一覧を読み直し、操作できる状態へ戻る（処理中のまま固まらない）
        await waitFor(() => expect(listImports).toHaveBeenCalledTimes(2))
        await waitFor(() => expect(screen.getByRole('button', {name: 'フォルダを選ぶ（一括）'})).toBeEnabled())
    })

    // 総量の上限を超える場合は理由を示して実行させない。
    it('上限を超える一括取り込みは理由を示して実行できない', async () => {
        scanImportDirectory.mockResolvedValue({
            ...SCAN,
            blocked: 'この内容を取り込むとプロジェクトの総量が上限の目安（500.0 MB）を超えます（取り込み後 520.0 MB の見込み）。フォルダを分けるか、不要な資料を整理してから実行してください。',
        })
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')

        await clickBatch('フォルダを選ぶ（一括）')
        const panel = await screen.findByLabelText('一括取り込みの確認')
        expect(within(panel).getByText(/上限の目安（500\.0 MB）を超えます/)).toBeTruthy()
        const run = within(panel).getByRole('button', {name: 'この内容で取り込む'})
        expect(run).toBeDisabled()
        expect(importDirectory).not.toHaveBeenCalled()
    })

    // 取りやめれば何も起きない（確認操作の意味を保つ）。
    it('確認を取りやめると取り込まない', async () => {
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')

        await clickBatch('フォルダを選ぶ（一括）')
        await screen.findByLabelText('一括取り込みの確認')
        await clickBatch('取りやめる')
        await waitFor(() => expect(screen.queryByLabelText('一括取り込みの確認')).toBeNull())
        expect(importDirectory).not.toHaveBeenCalled()
    })

    // ファイル選択・貼り付けの両経路と、一覧の種別・日時・抽出状態。
    it('ファイル選択と貼り付けの両方から取り込め、一覧に種別・日時・抽出状態が出る', async () => {
        render(<Imports onBack={() => undefined} />)
        expect(await screen.findByText('現行業務.md')).toBeTruthy()
        expect(screen.getByText(/資料 ／ 2026-08-31 10:00/)).toBeTruthy()
        expect(screen.getByText('抽出済み')).toBeTruthy()

        fireEvent.click(screen.getByRole('button', {name: 'ファイルを選ぶ'}))
        await waitFor(() => expect(importFile).toHaveBeenCalledWith('/tmp/現行業務.md', 'material'))

        fireEvent.change(screen.getByLabelText('種別'), {target: {value: 'minutes'}})
        fireEvent.change(screen.getByLabelText('貼り付ける本文'), {target: {value: '打ち合わせの記録'}})
        await clickEnabled('貼り付けを取り込む')
        await waitFor(() => expect(importClipboardText).toHaveBeenCalledWith('打ち合わせの記録', 'minutes'))
    })

    // 抽出できない資料はその旨を示し、原本の所在を参照できる。
    it('抽出できない資料は理由を示し、原本の所在を表示する', async () => {
        listImports.mockResolvedValue([FAILED])
        importContent.mockResolvedValue({
            id: 'IMP-002',
            extracted: '',
            extractionFailed: true,
            sourcePath: 'imports/IMP-002/source.pdf',
            sourceName: 'scan.pdf',
            notice: 'この資料からはテキストを取り出せませんでした。原本は保持しています。',
        })
        render(<Imports onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: /scan\.pdf/}))

        expect(await screen.findByText(/テキストを取り出せませんでした/)).toBeTruthy()
        expect(screen.getByText(/imports\/IMP-002\/source.pdf/)).toBeTruthy()
    })

    // 送信前プレビューと同意。同意しないと分析できない。
    it('送信前プレビューを出し、同意するまで分析できない', async () => {
        render(<Imports onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))

        fireEvent.click(await screen.findByRole('button', {name: '送信内容を確認する'}))
        expect(await screen.findByText(/分割 1 件 ／ 概算 1200 トークン/)).toBeTruthy()
        expect(screen.getByText('送信するシステムプロンプト')).toBeTruthy()

        // 同意前は分析ボタンが無効で理由が示される（非表示にしない）。
        const analyze = screen.getByRole('button', {name: 'この内容で分析する'})
        expectDisabledReason(analyze, /同意/)
        fireEvent.click(analyze)
        expect(analyzeImport).not.toHaveBeenCalled()

        fireEvent.click(screen.getByRole('checkbox', {name: /送信することに同意します/}))
        fireEvent.click(screen.getByRole('button', {name: 'この内容で分析する'}))
        await waitFor(() => expect(analyzeImport).toHaveBeenCalledWith('IMP-001', true))
    })

    // 候補の承認と、根拠箇所の同時視認。
    it('候補を承認して反映し、根拠箇所を抽出テキストの該当行として示す', async () => {
        render(<Imports onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))
        fireEvent.click(await screen.findByRole('button', {name: '送信内容を確認する'}))
        fireEvent.click(await screen.findByRole('checkbox', {name: /送信することに同意します/}))
        fireEvent.click(screen.getByRole('button', {name: 'この内容で分析する'}))

        // 重複・矛盾の指摘が表示される。
        expect(await screen.findByText('DEC-001 と重複の可能性')).toBeTruthy()
        expect(screen.getByText('矛盾の指摘')).toBeTruthy()
        expect(screen.getByText(/既存決定と矛盾します/)).toBeTruthy()

        // 根拠を押すと抽出テキストの該当行が強調される（同時視認）。
        fireEvent.click(screen.getByRole('button', {name: 'IMP-001#L3-L3'}))
        await waitFor(() => {
            const line = document.querySelector('.rw-imports__line--on')
            expect(line?.getAttribute('data-line')).toBe('3')
        })

        // 承認したものだけが渡る（破棄・未選択の候補は渡らない）。
        const decision = screen.getByLabelText('決定事項の候補 1').closest('article') as HTMLElement
        fireEvent.click(within(decision).getByRole('button', {name: '承認'}))
        const discarded = screen.getByLabelText('決定事項の候補 2').closest('article') as HTMLElement
        fireEvent.click(within(discarded).getByRole('button', {name: '破棄'}))
        const perspective = screen.getByLabelText('質問観点の候補 1').closest('article') as HTMLElement
        fireEvent.click(within(perspective).getByRole('button', {name: '承認'}))

        fireEvent.click(screen.getByRole('button', {name: '承認した候補を反映する'}))
        await waitFor(() => expect(approveImportCandidates).toHaveBeenCalled())
        const [importID, request] = approveImportCandidates.mock.calls[0]
        expect(importID).toBe('IMP-001')
        expect(request.decisions).toHaveLength(1)
        expect(request.decisions[0].candidate.body).toBe('受注確定時に在庫を引き当てる。')
        // 未選択のまま残した未決事項候補は渡らない。
        expect(request.openIssues).toHaveLength(0)
        expect(request.perspectives).toHaveLength(1)
        expect(await screen.findByText(/決定事項 1 件 \/ プロジェクト観点 1 件を反映しました。/)).toBeTruthy()
    })

    // 根拠を特定できない候補は理由を文で示し、記録できない種別は承認を選べなくする
    // （対話画面の抽出候補パネルと同じ規則。押してから失敗させない）。
    it('根拠を特定できない候補は理由を示し、決定事項・観点は承認を選べなくする', async () => {
        analyzeImport.mockResolvedValue({
            ...ANALYSIS,
            extraction: {
                ...ANALYSIS.extraction,
                decisions: [{...ANALYSIS.extraction.decisions[0], evidence_refs: [], missing_evidence: true}],
                perspective_candidates: [{...ANALYSIS.extraction.perspective_candidates[0], evidence_refs: []}],
            },
        })
        render(<Imports onBack={() => undefined} />)
        await analyzeSelected()

        const decision = (await screen.findByLabelText('決定事項の候補 1')).closest('article') as HTMLElement
        expect(within(decision).getByRole('note').textContent).toContain(
            '根拠にした資料の該当箇所を特定できませんでした。このままでは記録できないため、この候補は破棄してください。',
        )
        expect(within(decision).queryByText('根拠なし')).toBeNull()
        expectDisabledReason(within(decision).getByRole('button', {name: '承認'}), /資料の該当箇所を特定できない/)
        expect(within(decision).getByRole('button', {name: '破棄'})).toBeEnabled()

        const perspective = screen.getByLabelText('質問観点の候補 1').closest('article') as HTMLElement
        expectDisabledReason(within(perspective).getByRole('button', {name: '承認'}), /資料の該当箇所を特定できない/)
    })

    // 未決事項の候補は「決める人・期限」を入れて承認できる
    // （以前は入力欄が無く、決める人が空の候補は記録できなかった）。
    it('未決事項の候補は決める人・期限を入れて承認でき、入れた値が渡る', async () => {
        render(<Imports onBack={() => undefined} />)
        await analyzeSelected()

        const issue = (await screen.findByLabelText('未決事項の候補 1')).closest('article') as HTMLElement
        fireEvent.change(within(issue).getByLabelText('未決事項の候補 1 の決める人'), {target: {value: '営業部 佐藤'}})
        fireEvent.change(within(issue).getByLabelText('未決事項の候補 1 の期限'), {target: {value: '2026-09-30'}})
        fireEvent.click(within(issue).getByRole('button', {name: '承認'}))
        fireEvent.click(screen.getByRole('button', {name: '承認した候補を反映する'}))

        await waitFor(() => expect(approveImportCandidates).toHaveBeenCalled())
        const [, request] = approveImportCandidates.mock.calls[0]
        expect(request.openIssues).toEqual([
            expect.objectContaining({topic: '未選択のまま残す候補', owner: '営業部 佐藤', due: '2026-09-30'}),
        ])
    })

    // 反映の失敗は、押した右下の反映ボタンのすぐ上に「Error: 」を付けずに出す。
    it('反映に失敗した理由を右下の反映ボタンのすぐ上に出す', async () => {
        approveImportCandidates.mockRejectedValue(
            new Error('未決事項候補「未選択のまま残す候補」の「決める人」が空です。決める人を入れてから反映してください。'),
        )
        render(<Imports onBack={() => undefined} />)
        await analyzeSelected()

        const issue = (await screen.findByLabelText('未決事項の候補 1')).closest('article') as HTMLElement
        fireEvent.click(within(issue).getByRole('button', {name: '承認'}))
        fireEvent.click(screen.getByRole('button', {name: '承認した候補を反映する'}))

        const checks = screen.getByLabelText('チェック結果・操作')
        const alert = await within(checks).findByText(/反映できませんでした。何も記録していません。/)
        expect(alert.getAttribute('role')).toBe('alert')
        expect(alert.textContent).toContain('「決める人」が空です')
        expect(alert.textContent).not.toContain('Error')
        expect(alert.nextElementSibling?.className).toContain('rw-doc__actions')
        // 候補は残り、直して押し直せる。
        expect(within(issue).getByLabelText('未決事項の候補 1 の決める人')).toBeTruthy()
    })

    /*
     * 反映を終えた直後の画面が行き止まりに見えないこと。
     * 「押せる要素が無く、ここから何をどう進めたらいいのか分からない」という利用者報告への対応。
     */
    it('候補が無いときは、右ペインに次に押す操作を出す', async () => {
        perspectives.mockResolvedValue([REGISTERED])
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')

        // 資料を選んでいないときは、まず何をすれば分析へ進めるかを示す。
        expect(screen.getByText(/左の一覧で資料を選ぶと「送信内容を確認する」が押せるようになります。/)).toBeTruthy()
        // 対話へ戻る道も同時に示す（この画面で行き止まりにしない）。
        expect(screen.getByText(/「対話へ戻る」を押し、対話画面で「次の質問」を押します/)).toBeTruthy()
        // 観点が完成度に数えられないことを画面の文言で伝える（0% のままでも手掛かりを失わない）。
        expect(screen.getByText(/完成度の割合には数えません。/)).toBeTruthy()

        // 資料を選ぶと、そのまま押す順番へ変わる。
        fireEvent.click(screen.getByRole('button', {name: /現行業務\.md/}))
        expect(
            await screen.findByText(/「送信内容を確認する」→ 内容に同意 →「この内容で分析する」の順に押します。/),
        ).toBeTruthy()
    })

    it('戻る操作は戻り先が分かるラベルにする', async () => {
        const onBack = vi.fn()
        render(<Imports onBack={onBack} />)
        fireEvent.click(await screen.findByRole('button', {name: '対話へ戻る'}))
        expect(onBack).toHaveBeenCalledTimes(1)
    })

    /*
     * 主操作が押せない理由は、ポインタを重ねなくても読める。
     * `title` 属性に載せていた頃は、無効化された要素がポインタ事象を発しないため
     * 理由が画面のどこにも出ていなかった。
     */
    it('主操作が無効なときは、理由がホバーなしで画面に出る', async () => {
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')

        const button = screen.getByRole('button', {name: '送信内容を確認する'})
        expect(button).toBeDisabled()
        expect(screen.getByText('資料を選んでください。')).toBeTruthy()
        // 重ねたときも同じ文が出る。
        expectDisabledReason(button, '資料を選んでください。')
    })

    /*
     * 押せるものは枠を持ち、押せないメタ情報をチップ（塗り）で描かない
     * （塗りのチップは押せる操作と誤認される）。
     */
    it('観点の操作は枠線ボタンにし、由来はチップで描かない', async () => {
        perspectives.mockResolvedValue([REGISTERED])
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('棚卸の差異処理')

        expect(screen.getByRole('button', {name: '編集'}).getAttribute('data-variant')).toBe('secondary')
        expect(screen.getByRole('button', {name: '削除'}).getAttribute('data-variant')).toBe('secondary')

        // 由来は状態ではない。塗りつぶしのチップにすると押せる操作と誤認される。
        const origin = screen.getByText('資料の分析から登録')
        expect(origin.closest('.rw-chip')).toBeNull()
        expect(origin.querySelector('.rw-chip')).toBeNull()
    })

    it('候補の承認・破棄・編集と根拠へのリンクは、押せると分かる枠を持つ', async () => {
        render(<Imports onBack={() => undefined} />)
        await screen.findByText('現行業務.md')
        fireEvent.click(screen.getByRole('button', {name: /現行業務\.md/}))
        await clickEnabled('送信内容を確認する')
        fireEvent.click(await screen.findByRole('checkbox', {name: /送信することに同意します/}))
        await clickEnabled('この内容で分析する')

        const decision = (await screen.findByLabelText('決定事項の候補 1')).closest('article') as HTMLElement
        for (const name of ['承認', '破棄', '編集']) {
            expect(within(decision).getByRole('button', {name}).getAttribute('data-variant')).toBe('secondary')
        }
        // 根拠（IMP-nnn#Lm-Ln）も押せる操作であり、ただの文字にしない。
        expect(within(decision).getByRole('button', {name: 'IMP-001#L3-L3'}).getAttribute('data-variant')).toBe(
            'secondary',
        )

        // 承認したものだけが緑の塗りへ変わる（選択状態が形で分かる）。
        fireEvent.click(within(decision).getByRole('button', {name: '承認'}))
        expect(within(decision).getByRole('button', {name: '承認'}).getAttribute('data-variant')).toBe('primary')
    })

    /*
     * 画面の状態が変わるたびに「次に触る要素」が追従すること。
     *
     * 利用者報告「この画面からの業務と操作手順が全く見当がつかない」への対応。
     * 表示は上部の常設ストリップが担うため、ここでは**画面が publish する内容**を捕まえて確かめる。
     */
    it('次に触る要素の案内が、画面の状態に追従する', async () => {
        const hints: string[] = []
        render(
            <GuideHintProvider value={(h) => hints.push(h)}>
                <Imports onBack={() => undefined} />
            </GuideHintProvider>,
        )
        const latest = () => hints.filter((h) => h !== '').at(-1) ?? ''

        // 資料を選ぶ前は、選ぶことを促す。
        await screen.findByText('現行業務.md')
        await waitFor(() => expect(latest()).toContain('分析したい資料を選びます'))

        // 資料を選んだら、送信内容の確認へ。
        fireEvent.click(screen.getByRole('button', {name: /現行業務\.md/}))
        await waitFor(() => expect(latest()).toContain('「送信内容を確認する」'))

        // プレビューを出したら、同意のチェックを指す（押せない理由と同じ場所を指す）。
        await clickEnabled('送信内容を確認する')
        await waitFor(() => expect(latest()).toContain('同意します'))

        // 同意したら、分析の実行へ。
        fireEvent.click(screen.getByRole('checkbox', {name: /送信することに同意します/}))
        await waitFor(() => expect(latest()).toContain('「この内容で分析する」'))

        // 候補が出たら、承認と反映へ。
        await clickEnabled('この内容で分析する')
        await waitFor(() => expect(latest()).toContain('「承認した候補を反映する」'))
    })

    // 開発AIフィードバックの分類を付与できる（AI は分類しない）。
    it('開発AIフィードバックには分類を表示・付与できる', async () => {
        listImports.mockResolvedValue([FEEDBACK])
        importContent.mockResolvedValue({...CONTENT, id: 'IMP-003'})
        render(<Imports onBack={() => undefined} />)
        expect(await screen.findByText('要件定義時に確認すべきだった論点')).toBeTruthy()

        fireEvent.click(screen.getByRole('button', {name: /feedback\.md/}))
        const select = await screen.findByLabelText('フィードバックの分類')
        fireEvent.change(select, {target: {value: 'requirements-gap'}})
        await waitFor(() =>
            expect(setFeedbackClassification).toHaveBeenCalledWith('IMP-003', 'requirements-gap'),
        )
    })

    // プロジェクト観点の一覧・手動追加・編集・削除（AI 呼び出しを伴わない）。
    it('プロジェクト観点を手動で追加・編集・削除できる', async () => {
        perspectives.mockResolvedValue([
            {
                id: 'PRS-001',
                name: '棚卸の差異処理',
                summary: '差異の承認経路',
                origin: 'manual',
                originLabel: '手動で登録',
                topicKey: 'custom/PRS-001',
                createdAt: '2026-08-31T00:00:00Z',
                author: 'k.sato@example.co.jp',
            },
        ])
        render(<Imports onBack={() => undefined} />)
        expect(await screen.findByText('棚卸の差異処理')).toBeTruthy()

        fireEvent.change(screen.getByLabelText('観点名'), {target: {value: '入出庫の権限'}})
        fireEvent.change(screen.getByLabelText('観点の要旨'), {target: {value: '誰が確定できるか'}})
        fireEvent.click(screen.getByRole('button', {name: '観点を追加する'}))
        await waitFor(() =>
            expect(addPerspective).toHaveBeenCalledWith({id: '', name: '入出庫の権限', summary: '誰が確定できるか'}),
        )

        await clickEnabled('編集')
        fireEvent.change(screen.getByLabelText('PRS-001 の観点名'), {target: {value: '棚卸の差異処理と承認'}})
        await clickEnabled('保存')
        await waitFor(() =>
            expect(updatePerspective).toHaveBeenCalledWith({
                id: 'PRS-001',
                name: '棚卸の差異処理と承認',
                summary: '差異の承認経路',
            }),
        )

        await clickEnabled('削除')
        await waitFor(() => expect(removePerspective).toHaveBeenCalledWith('PRS-001'))
        // 分析・プレビューは呼ばれない（手動管理は AI 呼び出しを伴わない）。
        expect(analyzeImport).not.toHaveBeenCalled()
        expect(previewImportAnalysis).not.toHaveBeenCalled()
    })

    // 閲覧権限では操作を隠さず無効化し、理由を示す。
    it('閲覧権限では操作が無効化され、理由が表示される', async () => {
        currentPermission.mockResolvedValue({
            role: 'viewer',
            roleLabel: '閲覧',
            canEdit: false,
            reason: 'この操作は編集権限が必要です（現在は閲覧）。オーナーに権限の変更を依頼してください。',
        })
        render(<Imports onBack={() => undefined} />)

        const file = await screen.findByRole('button', {name: 'ファイルを選ぶ'})
        expectDisabledReason(file, /編集権限が必要です/)
        fireEvent.click(file)
        expect(chooseImportFile).not.toHaveBeenCalled()

        // 一覧・内容の参照はできる（操作を隠さない）。
        expect(screen.getByText('現行業務.md')).toBeTruthy()
        expect(screen.getByRole('button', {name: '観点を追加する'}).hasAttribute('disabled')).toBe(true)
    })

    // 反映時に競合したら三面を出し、承認するまで反映しない。
    it('反映時の競合で三面を表示し、マージ承認で再実行する', async () => {
        approveImportCandidates.mockResolvedValueOnce({
            notice: '他のメンバーの変更と競合しています（FR-INV-001）。',
            conflicts: [
                {
                    id: 'FR-INV-001',
                    label: 'FR-INV-001',
                    baselineBody: '受注確定時に在庫を引き当てること。',
                    currentBody: '他メンバーの変更。',
                    currentHash: 'hash-current',
                },
            ],
        })
        pendingImportCandidates.mockResolvedValue(ANALYSIS.extraction)
        render(<Imports onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))
        const decision = await screen.findByLabelText('決定事項の候補 1')
        fireEvent.click(
            within(decision.closest('article') as HTMLElement).getByRole('button', {name: '承認'}),
        )
        fireEvent.click(screen.getByRole('button', {name: '承認した候補を反映する'}))

        // 三面が出る（反映は行われていない）。
        expect(await screen.findByLabelText('競合の解決')).toBeTruthy()
        expect(screen.getByText('他メンバーの変更。')).toBeTruthy()

        // 本人がマージを承認すると、確認した基準版つきで再実行される。
        fireEvent.click(screen.getByRole('radio', {name: '自分の反映案を通す'}))
        fireEvent.click(screen.getByRole('button', {name: 'この内容で反映する'}))
        await waitFor(() => expect(approveImportCandidates).toHaveBeenCalledTimes(2))
        const [, second] = approveImportCandidates.mock.calls[1]
        expect(second.merges).toHaveLength(1)
        expect(second.merges[0]).toMatchObject({id: 'FR-INV-001', baselineHash: 'hash-current'})
    })

    // 中断・障害で保全された未承認候補を再開時に復元する。
    it('保全された未承認候補を選択時に復元する', async () => {
        pendingImportCandidates.mockResolvedValue(ANALYSIS.extraction)
        render(<Imports onBack={() => undefined} />)
        fireEvent.click(await screen.findByRole('button', {name: /現行業務\.md/}))

        expect(await screen.findByLabelText('決定事項の候補 1')).toBeTruthy()
        expect(analyzeImport).not.toHaveBeenCalled()
    })
})
