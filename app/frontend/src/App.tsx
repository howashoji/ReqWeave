import {useCallback, useEffect, useState} from 'react'
import {
    AppInfo,
    ApplyUpdate,
    CancelUpdate,
    CheckForUpdate,
    DeferUpdate,
    IntroShown,
    MarkIntroShown,
    PendingOpenFile,
    PreviousRunIncomplete,
    RestartApp,
    ScaleStatus,
    SetupState,
    StartupMode,
    Theme as StoredTheme,
} from '../wailsjs/go/binding/API'
import {binding} from '../wailsjs/go/models'
import {EventsOn} from '../wailsjs/runtime/runtime'
import {Button, PreviousRunNotice, ScaleNotice, UpdateNotice} from './ui'
import {BackupRestore} from './screens/BackupRestore'
import {Dialogue} from './screens/Dialogue'
import {Intro} from './screens/Intro'
import {ProjectList} from './screens/ProjectList'
import {Respond} from './screens/Respond'
import {Settings} from './screens/Settings'
import {SyncCredentials} from './screens/SyncCredentials'
import {SetupWizard} from './screens/SetupWizard'
import {TokenUsage} from './screens/TokenUsage'
import {UsageDashboard} from './screens/UsageDashboard'
import {applyTheme, currentTheme, type Theme} from './theme/theme'
import './App.css'

/*
 * ルート。初期設定が未完了なら初期設定ウィザードを表示し、
 * 完了していれば担当者モードの画面へ進む。
 *
 * 初期設定の完了後はプロジェクト一覧。プロジェクトを開くと対話画面へ入る。
 *
 * 初期設定が未完了かつ紹介スライドを一度も見ていない場合は、初期設定ウィザードより**前**に
 * 紹介スライドを出す（何のアプリか分からないままシークレットキーの入力を求めないため）。回答モードでは出さない。
 */

function App() {
    const [setupComplete, setSetupComplete] = useState<boolean | null>(null)
    // 紹介スライドの初回の自動表示を終えたか（アプリ設定の intro_shown）。
    const [introShown, setIntroShown] = useState<boolean | null>(null)
    // 起動経路で動作モードが決まる。受け渡しファイルを開いた場合は回答モード。
    const [mode, setMode] = useState<binding.StartupMode | null>(null)
    const [, setInfo] = useState<binding.AppInfo | null>(null)
    // 自動更新の状態（全画面共通）。
    const [update, setUpdate] = useState<binding.UpdateState | null>(null)
    // 規模上限の予告・到達。通知欄へ出す。**操作は止めない**。
    const [scale, setScale] = useState<binding.ScaleWarning[] | null>(null)
    // 前回の異常終了（起動時のクラッシュの検知）。通知欄へ出す。**操作は止めない**。
    const [previousRunIncomplete, setPreviousRunIncomplete] = useState(false)
    const [error, setError] = useState<string | null>(null)
    const [, setTheme] = useState<Theme>(() => currentTheme())
    const [openedProject, setOpenedProject] = useState('')
    // プロジェクト一覧から同期パネルを開く経路。開いた直後に表示するパネルを渡す。
    const [openedView, setOpenedView] = useState<'sync' | undefined>(undefined)
    const [screen, setScreen] = useState<
        'projects' | 'settings' | 'backup' | 'usage' | 'token-usage' | 'sync-credentials'
    >('projects')
    // 同期先の認証情報はプロジェクトごと・端末ごとのため対象を保持する
    const [credentialTarget, setCredentialTarget] = useState({projectId: '', name: ''})
    const [backupTarget, setBackupTarget] = useState('')
    // トークン消費実績はプロジェクト単位のため対象を保持する
    const [tokenUsageTarget, setTokenUsageTarget] = useState('')
    // OS のファイル関連付け・ドラッグで渡された返送ファイル。
    // 担当者モードの取込導線へ渡す。
    const [returnFile, setReturnFile] = useState('')

    useEffect(() => {
        // バインディングは同期的にも失敗しうる（Wails ランタイム未注入の場合など）ため、
        // 呼び出し自体を Promise 連鎖の内側に置いて catch へ落とす。
        Promise.resolve()
            .then(() => AppInfo())
            .then(setInfo)
            .catch(() => setError('アプリ情報を取得できませんでした。アプリを再起動してください。'))
    }, [])

    // 保存済みテーマの適用（テーマは端末ごとの設定）。
    useEffect(() => {
        Promise.resolve()
            .then(() => StoredTheme())
            .then((stored) => {
                if (stored === 'dark' || stored === 'light') {
                    applyTheme(stored)
                    setTheme(stored)
                }
            })
            .catch(() => undefined)
    }, [])

    // 前回の異常終了の確認。
    // 判定は動作ログの走査（バインディング側）。取得できない場合は案内を出さない
    //（案内の取得失敗でアプリの利用を妨げない）。
    useEffect(() => {
        Promise.resolve()
            .then(() => PreviousRunIncomplete())
            .then(setPreviousRunIncomplete)
            .catch(() => setPreviousRunIncomplete(false))
    }, [])

    // 起動時の更新確認。
    // 確認の失敗は画面へ出さない（バインディングが idle を返す。記録は動作ログのみ）。
    // 通知するのは新版があるときだけで、いずれにしてもアプリの利用は妨げない。
    // 回答モードではバインディング側が何もしない（回答への集中を妨げない）。
    useEffect(() => {
        Promise.resolve()
            .then(() => CheckForUpdate())
            .then(setUpdate)
            .catch(() => setUpdate(null))
    }, [])

    // 規模上限の判定。判定は保存のたびに再評価する規定のため、
    // 開いているプロジェクトが変わるたびに取り直す。取得できない場合は通知を出さない
    // （通知の取得失敗でアプリの利用を妨げない）。
    useEffect(() => {
        Promise.resolve()
            .then(() => ScaleStatus())
            .then(setScale)
            .catch(() => setScale(null))
    }, [openedProject])

    // OS から受け取ったファイルの振り分け。
    // .rwvq は回答モード、.rwva は担当者側の取込導線、.reqweave はそのプロジェクトを開く。
    // それ以外は開けない旨を通知する（開けないファイルでアプリを終了させない）。
    const handleOpenFile = useCallback((ev: binding.OpenFileEvent) => {
        switch (ev.kind) {
            case 'respond':
                setMode({mode: 'respondent', filePath: ev.filePath, fileName: ev.fileName} as binding.StartupMode)
                break
            case 'import':
                setReturnFile(ev.filePath ?? '')
                break
            case 'project':
                // macOS でパッケージをダブルクリックした経路。
                setOpenedView(undefined)
                setOpenedProject(ev.filePath ?? '')
                break
            case 'unsupported':
                setError(ev.message ?? '')
                break
        }
    }, [])

    // 起動中に届く経路（macOS の open-file イベント）。
    useEffect(() => {
        const off = EventsOn('openfile:event', (ev: binding.OpenFileEvent) => handleOpenFile(ev))
        return () => off()
    }, [handleOpenFile])

    // 起動時に受け取っていた分（Windows の関連付け起動・画面が出る前の open-file）。
    useEffect(() => {
        Promise.resolve()
            .then(() => PendingOpenFile())
            .then((ev) => {
                if (ev.kind) {
                    handleOpenFile(ev)
                }
            })
            .catch(() => undefined)
    }, [handleOpenFile])

    // 起動モードの判定。取得できない場合は担当者モードとして扱う。
    useEffect(() => {
        Promise.resolve()
            .then(() => StartupMode())
            .then(setMode)
            .catch(() => setMode({mode: 'owner'} as binding.StartupMode))
    }, [])

    // 初期設定の完了判定。未完了なら初期設定ウィザードへ入る。
    useEffect(() => {
        Promise.resolve()
            .then(() => SetupState())
            .then((state) => setSetupComplete(state.complete))
            .catch(() => setSetupComplete(false))
    }, [])

    // 紹介スライドの表示判定。取得できない場合は「表示済み」として扱い、
    // 案内の失敗で起動（初期設定）を妨げない。
    useEffect(() => {
        Promise.resolve()
            .then(() => IntroShown())
            .then(setIntroShown)
            .catch(() => setIntroShown(true))
    }, [])

    // 更新の操作。いずれもバインディングが返した状態でそのまま置き換える
    //（画面側で更新の可否を判定しない）。
    const runUpdate = (call: () => Promise<binding.UpdateState>) => {
        Promise.resolve()
            .then(call)
            .then(setUpdate)
            .catch(() => undefined)
    }

    // アプリ全体のエラー（起動時の情報取得など）は画面の先頭に出す（「原因＋次の行動」の様式）。
    const notice = (
        <>
            {error ? (
                <p className="rw-app__status" role="alert">
                    {error}
                </p>
            ) : null}
            <UpdateNotice
                state={update}
                onApply={() => runUpdate(ApplyUpdate)}
                onDefer={() => runUpdate(DeferUpdate)}
                onCancel={() => runUpdate(CancelUpdate)}
                onRestart={() => runUpdate(RestartApp)}
            />
            <ScaleNotice warnings={scale} />
            {/* 案内は起動直後（プロジェクトを開く前）の状態でだけ出す。設定画面へはプロジェクト一覧からしか
                入れないため、プロジェクトを開いている間に出しても導線が働かない。閉じるまでは戻れば再び出る。 */}
            <PreviousRunNotice
                incomplete={previousRunIncomplete && !openedProject}
                onOpenSettings={() => {
                    setPreviousRunIncomplete(false)
                    setScreen('settings')
                }}
            />
            {returnFile && !openedProject ? (
                <p className="rw-app__status" role="status">
                    返送ファイルを取り込むには、対象のプロジェクトを開いてください。
                </p>
            ) : null}
        </>
    )

    if (setupComplete === null || mode === null || introShown === null) {
        return <p className="rw-app__status">読み込んでいます…</p>
    }
    // 回答モードは担当者モードの画面へ遷移する導線を持たない（初期設定も要らない）。
    if (mode.mode === 'respondent' && mode.filePath) {
        return <Respond filePath={mode.filePath} fileName={mode.fileName ?? ''} />
    }
    // 紹介スライドは初期設定ウィザードより前。最後まで送っても飛ばしても記録する。
    if (!setupComplete && !introShown) {
        return (
            <Intro
                onFinish={() => {
                    setIntroShown(true)
                    Promise.resolve()
                        .then(() => MarkIntroShown())
                        .catch(() => undefined)
                }}
            />
        )
    }
    if (!setupComplete) {
        return (
            <>
                {notice}
                <SetupWizard onComplete={() => setSetupComplete(true)} />
            </>
        )
    }

    if (!openedProject) {
        if (screen === 'settings') {
            return (
                <>
                    {notice}
                    <Settings
                        onBack={() => setScreen('projects')}
                        onOpenSyncCredentials={(projectId, name) => {
                            setCredentialTarget({projectId, name})
                            setScreen('sync-credentials')
                        }}
                    />
                </>
            )
        }
        if (screen === 'sync-credentials') {
            return (
                <>
                    {notice}
                    <SyncCredentials
                        projectId={credentialTarget.projectId}
                        projectName={credentialTarget.name}
                        canCheck={false}
                        onBack={() => setScreen('settings')}
                        backLabel="設定へ戻る"
                    />
                </>
            )
        }
        if (screen === 'usage') {
            return (
                <>
                    {notice}
                    <UsageDashboard onBack={() => setScreen('projects')} />
                </>
            )
        }
        if (screen === 'token-usage') {
            return (
                <>
                    {notice}
                    <TokenUsage
                        projectPath={tokenUsageTarget}
                        onBack={() => setScreen('projects')}
                        onOpenDashboard={() => setScreen('usage')}
                    />
                </>
            )
        }
        if (screen === 'backup') {
            return (
                <>
                    {notice}
                    <BackupRestore projectPath={backupTarget} onBack={() => setScreen('projects')} />
                </>
            )
        }
        return (
            <>
                {notice}
                    <ProjectList
                    onOpen={(path) => {
                        setOpenedView(undefined)
                        setOpenedProject(path)
                    }}
                    onOpenSync={(path) => {
                        setOpenedView('sync')
                        setOpenedProject(path)
                    }}
                    onOpenSettings={() => setScreen('settings')}
                    onOpenUsage={() => setScreen('usage')}
                    onOpenTokenUsage={(path) => {
                        setTokenUsageTarget(path)
                        setScreen('token-usage')
                    }}
                    onOpenBackup={(path) => {
                        setBackupTarget(path)
                        setScreen('backup')
                    }}
                />
            </>
        )
    }

    return (
        <>
            {notice}
            <Dialogue
                projectPath={openedProject}
                initialView={openedView}
                importFile={returnFile}
                onImportFileHandled={() => setReturnFile('')}
                onBack={() => {
                    setOpenedProject('')
                    setOpenedView(undefined)
                    setScreen('projects')
                }}
            />
        </>
    )
}

export default App
