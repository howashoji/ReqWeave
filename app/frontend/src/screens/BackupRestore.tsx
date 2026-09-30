import {useCallback, useEffect, useState} from 'react'
import {
    BackupGenerations,
    BackupProject,
    ChooseBackupFile,
    ChooseFolder,
    PreviewBackup,
    RestoreBackup,
} from '../../wailsjs/go/binding/API'
import {binding} from '../../wailsjs/go/models'
import {AppShell, Button, DataList, formatLocal, type Row, errorText} from '../ui'
import './BackupRestore.css'

/**
 * バックアップ・復元。
 *
 * 手動バックアップの単一ファイル出力、ファイルからの復元、
 * 自動退避世代（直近 10 世代）からの復元。
 * 復元は既存フォルダを上書きせず、新しいフォルダへ展開する（復元に失敗しても元のデータを失わないように）。
 */

type Tone = 'info' | 'accent' | 'danger' | 'warn'

const GENERATION_COLUMNS = [
    {key: 'createdAt', label: '退避日時', mono: true},
    {key: 'size', label: 'サイズ', mono: true},
]

function formatSize(bytes: number): string {
    if (bytes < 1024) {
        return `${bytes} B`
    }
    if (bytes < 1024 * 1024) {
        return `${Math.round(bytes / 1024)} KB`
    }
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function BackupRestore({projectPath, onBack}: {projectPath: string; onBack: () => void}) {
    const [generations, setGenerations] = useState<binding.BackupGenerationView[]>([])
    const [selectedGeneration, setSelectedGeneration] = useState('')
    const [preview, setPreview] = useState<binding.BackupPreview | null>(null)
    const [destination, setDestination] = useState('')
    const [message, setMessage] = useState('')
    const [tone, setTone] = useState<Tone>('info')

    useEffect(() => {
        if (!projectPath) {
            return
        }
        Promise.resolve()
            .then(() => BackupGenerations(projectPath))
            .then(setGenerations)
            .catch(() => setGenerations([]))
    }, [projectPath])

    const backup = useCallback(async () => {
        try {
            const path = await BackupProject(projectPath)
            if (!path) {
                return
            }
            setTone('accent')
            setMessage(`バックアップを出力しました: ${path}`)
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [projectPath])

    const chooseSource = useCallback(async (source: string) => {
        try {
            setPreview(await PreviewBackup(source))
            setMessage('')
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const chooseFile = useCallback(async () => {
        try {
            const file = await ChooseBackupFile()
            if (file) {
                await chooseSource(file)
            }
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [chooseSource])

    const chooseDestination = useCallback(async () => {
        try {
            const dir = await ChooseFolder('復元先のフォルダ（空のフォルダ）')
            if (dir) {
                setDestination(dir)
            }
        } catch (err: unknown) {
            setTone('danger')
            setMessage(errorText(err))
        }
    }, [])

    const restore = useCallback(
        async (keepAsCopy: boolean) => {
            if (!preview) {
                return
            }
            try {
                const restored = await RestoreBackup(preview.sourcePath, destination, keepAsCopy)
                // 復元後の注意（同期先からの取り直しが要る場合など）は続けて示す。
                setTone(restored.notice ? 'warn' : 'accent')
                setMessage(
                    `「${restored.targetSystemName}」を復元しました。${restored.notice ? ` ${restored.notice}` : ''}`,
                )
                setPreview(null)
                setDestination('')
            } catch (err: unknown) {
                setTone('danger')
                setMessage(errorText(err))
            }
        },
        [preview, destination],
    )

    const rows: Row[] = generations.map((g) => ({
        id: g.path,
        cells: {createdAt: formatLocal(g.createdAt), size: formatSize(g.sizeBytes)},
    }))

    return (
        <AppShell
            scope="global"
            breadcrumb={['reqweave', 'バックアップ・復元']}
            nav={
                <Button onClick={onBack} tooltip="この画面を閉じて、プロジェクトの一覧へ戻ります。">
                    プロジェクト一覧へ戻る
                </Button>
            }
        >
            <section className="rw-backup__section">
                <h2 className="rw-backup__title">バックアップを出力する</h2>
                <p className="rw-backup__meta">{projectPath || 'プロジェクトが選ばれていません'}</p>
                <div className="rw-backup__actions">
                    <Button
                        variant="primary"
                        onClick={() => void backup()}
                        disabledReason={projectPath ? undefined : 'プロジェクト一覧で対象を選んでください。'}
                    >
                        単一ファイルへ出力
                    </Button>
                </div>
                <p className="rw-backup__hint">プロジェクトデータ全体を 1 つのファイルに書き出します。</p>
            </section>

            <section className="rw-backup__section">
                <h2 className="rw-backup__title">自動退避から復元する</h2>
                <DataList
                    caption="自動退避の世代"
                    columns={GENERATION_COLUMNS}
                    rows={rows}
                    selectedId={selectedGeneration}
                    onSelect={(id) => {
                        setSelectedGeneration(id)
                        void chooseSource(id)
                    }}
                    empty={{
                        message: '自動退避はまだありません。',
                        automatic: 'プロジェクトを閉じるたびに自動で作成され、直近 10 世代を保持します。',
                    }}
                />
                <p className="rw-backup__hint">直近 10 世代を保持します。</p>
            </section>

            <section className="rw-backup__section">
                <h2 className="rw-backup__title">ファイルから復元する</h2>
                <div className="rw-backup__actions">
                    <Button onClick={() => void chooseFile()}>バックアップファイルを選ぶ</Button>
                </div>
            </section>

            {preview ? (
                <section className="rw-backup__section">
                    <h2 className="rw-backup__title">復元の確認</h2>
                    <p>対象システム: {preview.targetSystemName}</p>
                    <p className="rw-backup__meta">{preview.sourcePath}</p>
                    <div className="rw-backup__actions">
                        <Button onClick={() => void chooseDestination()}>復元先のフォルダを選ぶ</Button>
                        <span className="rw-backup__meta">{destination || '未選択'}</span>
                    </div>
                    {preview.notice ? (
                        <p className="rw-backup__message" data-tone="warn">
                            {preview.notice}
                        </p>
                    ) : null}
                    <div className="rw-backup__actions">
                        {preview.duplicatePath ? (
                            <>
                                <Button
                                    variant="primary"
                                    onClick={() => void restore(true)}
                                    disabledReason={destination ? undefined : '復元先のフォルダを選んでください。'}
                                >
                                    複製として保持する
                                </Button>
                                <Button
                                    onClick={() => void restore(false)}
                                    disabledReason={destination ? undefined : '復元先のフォルダを選んでください。'}
                                >
                                    置き換える
                                </Button>
                            </>
                        ) : (
                            <Button
                                variant="primary"
                                onClick={() => void restore(false)}
                                disabledReason={destination ? undefined : '復元先のフォルダを選んでください。'}
                            >
                                復元する
                            </Button>
                        )}
                        <Button variant="quiet" onClick={() => setPreview(null)}>
                            やめる
                        </Button>
                    </div>
                    <p className="rw-backup__hint">
                        復元は既存のフォルダを上書きしません。空のフォルダを選んでください。
                    </p>
                </section>
            ) : null}

            {message ? (
                <p className="rw-backup__message" data-tone={tone} role="status">
                    {message}
                </p>
            ) : null}
        </AppShell>
    )
}
