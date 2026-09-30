import { useCallback, useEffect, useState } from "react";
import {
  AddExistingProject,
  ChooseFolder,
  ChooseProject,
  ForgetProject,
  LocationWarning,
  MigratableProjects,
  MigrateProject,
  ProjectFolderNameFor,
  RenameProject,
  CloneSyncProject,
  CreateProject,
  DeletePreview,
  DeleteProject,
  DomainPresets,
  Projects,
  SyncKindOptions,
} from "../../wailsjs/go/binding/API";
import { binding } from "../../wailsjs/go/models";
import {
  AppShell,
  Button,
  Chip,
  DataList,
  formatLocal,
  type Row,
  errorText,
} from "../ui";
import "./ProjectList.css";

/**
 * プロジェクト一覧。
 *
 * プロジェクト横断の画面のため統制表示（完成度・消費量）は持たず、
 * 指標は行内に分散表示する（一覧・管理系の画面型）。
 * 作成・再開・削除・バックアップへの起点。
 * **既存のプロジェクトを開く**（フォルダ選択 → 一覧へ追加）は、共有フォルダのプロジェクトへ
 * 別の端末のメンバーが参加する経路でもある。
 * **同期先から取得して参加**は、同期先を指定して作業コピーを
 * この端末へ取得する経路。失敗した場合は中途半端な作業コピーを一覧へ加えない。
 * 共同プロジェクトの行には、自分の権限・最後に同期した日時・未反映の変更の有無を表示する。
 */

const COLUMNS = [
  { key: "name", label: "対象システム" },
  { key: "phase", label: "フェーズ" },
  { key: "updated", label: "最終更新", mono: true },
  { key: "role", label: "権限" },
  { key: "sync", label: "同期" },
  { key: "working", label: "作業状況" },
];

export function ProjectList({
  onOpen,
  onOpenSettings,
  onOpenBackup,
  onOpenUsage,
  onOpenTokenUsage,
  onOpenSync,
}: {
  onOpen: (path: string) => void;
  onOpenSettings: () => void;
  /** 同期パネルへ。対象のプロジェクトを開いてから開く */
  onOpenSync?: (path: string) => void;
  /** バックアップ・復元へ（選択中のプロジェクトを対象にする。未選択なら空文字） */
  onOpenBackup: (path: string) => void;
  /** AI 利用量ダッシュボードへ（プロジェクト横断のため対象の選択は要らない） */
  onOpenUsage: () => void;
  /** トークン消費実績へ（対象は選択中のプロジェクト） */
  onOpenTokenUsage: (path: string) => void;
}) {
  const [projects, setProjects] = useState<binding.ProjectSummary[]>([]);
  const [presets, setPresets] = useState<binding.DomainPresetOption[]>([]);
  const [selected, setSelected] = useState("");
  const [message, setMessage] = useState("");

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [summary, setSummary] = useState("");
  const [path, setPath] = useState("");
  const [chosenPresets, setChosenPresets] = useState<string[]>([]);

  const [deleteTarget, setDeleteTarget] =
    useState<binding.DeletePreview | null>(null);

  // 同期先から取得して参加。
  const [joining, setJoining] = useState(false);
  const [syncKinds, setSyncKinds] = useState<binding.SyncKindOption[]>([]);
  const [joinKind, setJoinKind] = useState("");
  const [joinLocation, setJoinLocation] = useState("");
  const [joinParent, setJoinParent] = useState("");
  const [joinFolder, setJoinFolder] = useState("");
  const [joinCredentialKind, setJoinCredentialKind] = useState("ssh_key");
  const [joinUsername, setJoinUsername] = useState("");
  const [joinSecret, setJoinSecret] = useState("");
  const [joinConsent, setJoinConsent] = useState(false);
  const [joinBusy, setJoinBusy] = useState(false);

  // `.reqweave` への移行（旧形式のフォルダ名からの改名）。対象は読み出しのみで求め、実行は利用者の操作に限る。
  const [migratable, setMigratable] = useState<binding.MigratableProject[]>([]);
  const [busy, setBusy] = useState(false);
  // 作成されるフォルダ名。**名前の規則はバインディングが持つ**（画面で組み立て直さない）。
  const [folderName, setFolderName] = useState("");
  // 保存先についての注意（クラウド同期のフォルダなど）。判定もバインディングが持つ。
  const [locationWarning, setLocationWarning] = useState("");
  // 対象システム名の変更。空 = 変更中でない。
  const [renaming, setRenaming] = useState<binding.ProjectSummary | null>(null);
  const [renameTo, setRenameTo] = useState("");

  const reload = useCallback(async () => {
    try {
      setProjects(await Projects());
    } catch (err: unknown) {
      setMessage(
        "プロジェクト一覧を読み込めませんでした。アプリを再起動してください。",
      );
    }
    try {
      setMigratable(await MigratableProjects());
    } catch {
      // 移行の案内は補助であり、出せなくても一覧の利用は妨げない。
      setMigratable([]);
    }
  }, []);

  // 対象システム名を変え、フォルダ名も追従させる。
  // Finder での改名は逆に反映しない（業務データを変えるのはアプリ内の操作に限る）。
  const rename = useCallback(async () => {
    if (!renaming || !renameTo.trim()) {
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      const moved = await RenameProject(renaming.path, renameTo.trim());
      await reload();
      setSelected(moved.path);
      setRenaming(null);
      setRenameTo("");
      setMessage(`対象システム名を「${moved.targetSystemName}」に変えました。`);
    } catch (err: unknown) {
      setMessage(errorText(err));
    } finally {
      setBusy(false);
    }
  }, [renaming, renameTo, reload]);

  // 一覧から外す。**データは消さない**。実体が見つからなくなったものを片づけるため。
  const forget = useCallback(
    async (target: string) => {
      setBusy(true);
      setMessage("");
      try {
        await ForgetProject(target);
        await reload();
        setSelected("");
        setMessage(
          "一覧から外しました。プロジェクトのデータは消していません。場所が分かれば「既存のプロジェクトを開く」で戻せます。",
        );
      } catch (err: unknown) {
        setMessage(errorText(err));
      } finally {
        setBusy(false);
      }
    },
    [reload],
  );

  // 1 件ずつ改名する。中身と場所は変えず、名前だけを変える。
  const migrate = useCallback(
    async (target: string) => {
      setBusy(true);
      setMessage("");
      try {
        const moved = await MigrateProject(target);
        await reload();
        setMessage(
          `「${moved.targetSystemName}」の名前を変えました。中身と場所は変わっていません。`,
        );
      } catch (err: unknown) {
        setMessage(errorText(err));
      } finally {
        setBusy(false);
      }
    },
    [reload],
  );

  // 対象システム名からできるフォルダ名を、作成前に見せる。
  useEffect(() => {
    const target = name.trim();
    if (!target) {
      setFolderName("");
      return;
    }
    let alive = true;
    ProjectFolderNameFor(target)
      .then((n) => {
        if (alive) {
          setFolderName(n);
        }
      })
      // 予告が出せなくても作成はできる（補助の表示）。
      .catch(() => setFolderName(""));
    return () => {
      alive = false;
    };
  }, [name]);

  useEffect(() => {
    void reload();
    Promise.resolve()
      .then(() => DomainPresets())
      .then(setPresets)
      .catch(() => setPresets([]));
    Promise.resolve()
      .then(() => SyncKindOptions())
      .then((kinds) => {
        setSyncKinds(kinds);
        setJoinKind((current) => current || kinds[0]?.kind || "");
      })
      .catch(() => setSyncKinds([]));
  }, [reload]);

  const chooseFolder = useCallback(async () => {
    try {
      // 選ぶのは**保存先**。フォルダ名はアプリが決める（対象システム名から作る）。
      const chosen = await ChooseFolder("プロジェクトの保存先フォルダ");
      if (chosen) {
        setPath(chosen);
        // 置き場所の注意は選んだ直後に出す（作ってからでは遅い）。
        setLocationWarning(await LocationWarning(chosen).catch(() => ""));
      }
    } catch (err: unknown) {
      setMessage("フォルダを選べませんでした。もう一度お試しください。");
    }
  }, []);

  // 既存のプロジェクトフォルダを一覧へ加える（別の端末のメンバーが共有フォルダのプロジェクトへ参加する経路）。
  // 追加の可否（プロジェクトデータかどうか）はバインディングが判定する。
  // 画面側で判定をやり直さず、返ってきた文言をそのまま出す（原因＋次の行動の 1 文）。
  const addExisting = useCallback(async () => {
    setMessage("");
    try {
      // macOS はプロジェクトを 1 個のファイルに見せているため、選び方が OS で分かれる。
      // 分岐はバインディング側にあり、画面は経路を 1 つだけ持つ。
      const chosen = await ChooseProject("開くプロジェクト");
      if (!chosen) {
        return;
      }
      const added = await AddExistingProject(chosen);
      await reload();
      setMessage(
        added.available
          ? `「${added.targetSystemName}」を一覧に追加しました。`
          : (added.notice ?? "一覧に追加しました。"),
      );
    } catch (err: unknown) {
      setMessage(errorText(err));
    }
  }, [reload]);

  const create = useCallback(async () => {
    setMessage("");
    try {
      await CreateProject({
        path,
        targetSystemName: name,
        summary,
        domainPresets: chosenPresets,
      } as binding.CreateProjectRequest);
      setCreating(false);
      setName("");
      setSummary("");
      setPath("");
      setChosenPresets([]);
      await reload();
    } catch (err: unknown) {
      setMessage(errorText(err));
    }
  }, [path, name, summary, chosenPresets, reload]);

  // 取得（参加）。認証情報の値はこの操作のときだけ渡し、成功後はバインディングが
  // 取得したプロジェクト ID で OS のセキュアストレージへ登録する。
  // 作成先は「親フォルダ + 新しいフォルダ名」で組み立てる（既存フォルダの上には作らない）。
  const joinDest =
    joinParent && joinFolder.trim()
      ? `${joinParent.replace(/[/\\]+$/, "")}/${joinFolder.trim()}`
      : "";

  const join = useCallback(async () => {
    setMessage("");
    setJoinBusy(true);
    try {
      const result = await CloneSyncProject({
        kind: joinKind,
        location: joinLocation,
        dest: joinDest,
        credentialKind: joinCredentialKind,
        username: joinUsername,
        secret: joinSecret,
        externalConsent: joinConsent,
      } as binding.CloneSyncProjectRequest);
      setMessage(result.notice);
      if (result.done) {
        setJoining(false);
        setJoinLocation("");
        setJoinFolder("");
        setJoinSecret("");
        setJoinUsername("");
        setJoinConsent(false);
        await reload();
      }
    } catch (err: unknown) {
      setMessage(errorText(err));
    } finally {
      setJoinBusy(false);
    }
  }, [
    joinKind,
    joinLocation,
    joinDest,
    joinCredentialKind,
    joinUsername,
    joinSecret,
    joinConsent,
    reload,
  ]);

  const chooseJoinDest = useCallback(async () => {
    try {
      const chosen = await ChooseFolder("作業コピーを作る親フォルダ");
      if (chosen) {
        setJoinParent(chosen);
      }
    } catch (err: unknown) {
      setMessage(errorText(err));
    }
  }, []);

  const askDelete = useCallback(async (target: string) => {
    setMessage("");
    try {
      setDeleteTarget(await DeletePreview(target));
    } catch (err: unknown) {
      setMessage(errorText(err));
    }
  }, []);

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) {
      return;
    }
    try {
      await DeleteProject(deleteTarget.path);
      setDeleteTarget(null);
      setSelected("");
      await reload();
    } catch (err: unknown) {
      setMessage(errorText(err));
    }
  }, [deleteTarget, reload]);

  const rows: Row[] = projects.map((p) => ({
    id: p.path,
    cells: {
      name: p.available ? (
        p.targetSystemName
      ) : (
        <>
          <span>{p.targetSystemName || "（読み込めません）"}</span>
          <span className="rw-projects__notice"> {p.notice}</span>
        </>
      ),
      phase: p.available ? (
        <Chip tone="info">{p.phaseLabel}</Chip>
      ) : (
        <Chip tone="danger">読み込み不可</Chip>
      ),
      updated: formatLocal(p.updatedAt),
      role: p.roleLabel ? (
        <Chip tone="accent">{p.roleLabel}</Chip>
      ) : (
        <span className="rw-projects__meta">—</span>
      ),
      // 同期の状態（共同プロジェクトのみ。最後に同期した日時と未反映の有無）。
      sync: p.syncConfigured ? (
        <span className="rw-projects__meta">
          {p.hasUnpublished ? (
            <Chip tone="warn">未反映あり</Chip>
          ) : (
            <Chip tone="accent">反映済み</Chip>
          )}
          <span className="rw-projects__sync rw-mono">
            {p.lastSyncedAt || "同期の記録なし"}
          </span>
        </span>
      ) : (
        <span className="rw-projects__meta">この端末のみ</span>
      ),
      // 作業状況（未解除の予約。最後に取り込んだ時点の情報）。
      working: p.working?.length ? (
        <span className="rw-projects__meta">{p.working.join(" / ")}</span>
      ) : (
        <span className="rw-projects__meta">—</span>
      ),
    },
  }));

  const selectedProject = projects.find((p) => p.path === selected);
  const joinKindOption = syncKinds.find((k) => k.kind === joinKind);
  const joinReason = joinBusy
    ? "取得しています。"
    : !joinLocation.trim()
      ? "同期先の所在を入力してください。"
      : !joinDest
        ? "作業コピーの親フォルダとフォルダ名を指定してください。"
        : joinKindOption?.requiresCredential && !joinSecret.trim()
          ? "この同期先には認証情報が必要です。"
          : joinKindOption?.requiresConsent && !joinConsent
            ? "外部 Git サーバへ保管することへの同意が必要です。"
            : undefined;

  return (
    <AppShell
      scope="global"
      breadcrumb={["reqweave", "プロジェクト"]}
      nav={
        <>
          {/* 上部ナビの各ボタンには機能を示す 1 文を添える（まず上部ナビから） */}
          <Button
            onClick={onOpenSettings}
            tooltip="AIプロバイダ・キー・利用者情報など、アプリ全体の設定を変更します。"
          >
            設定
          </Button>
          <Button
            onClick={onOpenUsage}
            tooltip="AI の利用量と上限の設定を確認します。"
          >
            AI 利用量
          </Button>
          <Button
            onClick={() => onOpenTokenUsage(selected)}
            tooltip="選んだプロジェクトのトークン消費の実績を期間ごとに確認します。"
            disabledReason={
              selected ? undefined : "一覧からプロジェクトを選んでください。"
            }
          >
            トークン消費実績
          </Button>
          <Button
            onClick={() => onOpenBackup(selected)}
            tooltip="プロジェクトの控えを作り、必要なときに書き戻します。"
          >
            バックアップ・復元
          </Button>
          <Button
            onClick={() => void addExisting()}
            tooltip="すでにこの端末にあるプロジェクトのフォルダを一覧へ加えます。"
          >
            既存のプロジェクトを開く
          </Button>
          <Button
            onClick={() => setJoining(true)}
            tooltip="ほかのメンバーが共有しているプロジェクトを取得して、作業を始めます。"
          >
            同期先から取得して参加
          </Button>
          {onOpenSync ? (
            <Button
              onClick={() => selectedProject && onOpenSync(selectedProject.path)}
              tooltip="ほかのメンバーの変更を取り込み、自分の変更を反映します。"
              disabledReason={
                !selectedProject
                  ? "一覧からプロジェクトを選んでください。"
                  : !selectedProject.syncConfigured
                    ? "このプロジェクトには同期先が設定されていません。"
                    : undefined
              }
            >
              同期
            </Button>
          ) : null}
          <Button
            variant="primary"
            onClick={() => setCreating(true)}
            tooltip="対象システムを決めて、新しいプロジェクトを作ります。"
          >
            新規作成
          </Button>
        </>
      }
    >
      {creating ? (
        <section className="rw-projects__panel">
          <h2 className="rw-projects__panel-title">プロジェクトを作成する</h2>
          <div className="rw-projects__field">
            <label className="rw-projects__label" htmlFor="project-name">
              対象システム名
            </label>
            <input
              id="project-name"
              className="rw-projects__input"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="rw-projects__field">
            <label className="rw-projects__label" htmlFor="project-summary">
              概要（任意）
            </label>
            <textarea
              id="project-summary"
              className="rw-projects__input"
              rows={2}
              value={summary}
              onChange={(e) => setSummary(e.target.value)}
            />
          </div>
          <div className="rw-projects__field">
            <span className="rw-projects__label">保存先フォルダ</span>
            <div className="rw-projects__path">
              <Button onClick={() => void chooseFolder()}>
                フォルダを選ぶ
              </Button>
              <span className="rw-projects__path-value">
                {path || "未選択"}
              </span>
            </div>
            {/* フォルダ名はアプリが決めるため、何ができるかを先に見せる */}
            {path && folderName ? (
              <span className="rw-projects__hint">
                この中に「{folderName}」を作ります。
              </span>
            ) : null}
            {/* 置き場所の注意。**作成は止めない**。気をつける点だけを伝える。 */}
            {locationWarning ? (
              <p className="rw-projects__warning" role="status">
                {locationWarning}
              </p>
            ) : null}
          </div>
          <div className="rw-projects__field">
            <span className="rw-projects__label">
              業務領域（任意・複数選択可）
            </span>
            <fieldset className="rw-projects__presets">
              {presets.map((preset) => (
                <label
                  className="rw-projects__preset"
                  key={preset.id}
                  title={preset.summary}
                >
                  <input
                    type="checkbox"
                    checked={chosenPresets.includes(preset.id)}
                    onChange={(e) =>
                      setChosenPresets((current) =>
                        e.target.checked
                          ? [...current, preset.id]
                          : current.filter((id) => id !== preset.id),
                      )
                    }
                  />
                  {preset.label}
                </label>
              ))}
            </fieldset>
          </div>
          <div className="rw-projects__actions">
            <Button
              variant="primary"
              onClick={() => void create()}
              disabledReason={
                !name.trim()
                  ? "対象システム名を入力してください。"
                  : !path
                    ? "保存先フォルダを選んでください。"
                    : undefined
              }
            >
              作成する
            </Button>
            <Button variant="quiet" onClick={() => setCreating(false)}>
              やめる
            </Button>
          </div>
        </section>
      ) : null}

      {joining ? (
        <section className="rw-projects__panel">
          <h2 className="rw-projects__panel-title">
            同期先から取得して参加する
          </h2>
          <p className="rw-projects__meta">
            オーナーから知らされた同期先を指定すると、この端末に作業コピーを作ります。
            取得に失敗したときは何も作られません。
          </p>
          <fieldset className="rw-projects__presets">
            {syncKinds.map((kind) => (
              <label
                className="rw-projects__preset"
                key={kind.kind}
                title={kind.hint}
              >
                <input
                  type="radio"
                  name="join-sync-kind"
                  checked={joinKind === kind.kind}
                  onChange={() => {
                    setJoinKind(kind.kind);
                    setJoinConsent(false);
                  }}
                />
                {kind.label}
              </label>
            ))}
          </fieldset>
          {joinKindOption ? (
            <p className="rw-projects__meta">{joinKindOption.hint}</p>
          ) : null}
          <div className="rw-projects__field">
            <label className="rw-projects__label" htmlFor="join-location">
              同期先の所在
            </label>
            <input
              id="join-location"
              className="rw-projects__input"
              type="text"
              value={joinLocation}
              onChange={(e) => setJoinLocation(e.target.value)}
            />
          </div>
          <div className="rw-projects__field">
            <span className="rw-projects__label">作業コピーの作成先</span>
            <div className="rw-projects__path">
              <Button onClick={() => void chooseJoinDest()}>
                親フォルダを選ぶ
              </Button>
              <span className="rw-projects__path-value">
                {joinParent || "未選択"}
              </span>
            </div>
          </div>
          <div className="rw-projects__field">
            <label className="rw-projects__label" htmlFor="join-folder">
              新しく作るフォルダ名
            </label>
            <input
              id="join-folder"
              className="rw-projects__input"
              type="text"
              value={joinFolder}
              onChange={(e) => setJoinFolder(e.target.value)}
            />
            <span className="rw-projects__sync rw-mono">
              {joinDest || "親フォルダとフォルダ名を指定してください"}
            </span>
          </div>
          {joinKindOption?.requiresCredential ? (
            <>
              <fieldset className="rw-projects__presets">
                <label className="rw-projects__preset">
                  <input
                    type="radio"
                    name="join-credential-kind"
                    checked={joinCredentialKind === "ssh_key"}
                    onChange={() => setJoinCredentialKind("ssh_key")}
                  />
                  SSH 鍵
                </label>
                <label className="rw-projects__preset">
                  <input
                    type="radio"
                    name="join-credential-kind"
                    checked={joinCredentialKind === "token"}
                    onChange={() => setJoinCredentialKind("token")}
                  />
                  アクセストークン
                </label>
              </fieldset>
              {joinCredentialKind === "token" ? (
                <div className="rw-projects__field">
                  <label className="rw-projects__label" htmlFor="join-username">
                    利用者名（任意）
                  </label>
                  <input
                    id="join-username"
                    className="rw-projects__input"
                    type="text"
                    value={joinUsername}
                    onChange={(e) => setJoinUsername(e.target.value)}
                  />
                </div>
              ) : null}
              <div className="rw-projects__field">
                <label className="rw-projects__label" htmlFor="join-secret">
                  認証情報
                </label>
                {joinCredentialKind === "ssh_key" ? (
                  <textarea
                    id="join-secret"
                    className="rw-projects__input"
                    rows={4}
                    value={joinSecret}
                    onChange={(e) => setJoinSecret(e.target.value)}
                  />
                ) : (
                  <input
                    id="join-secret"
                    className="rw-projects__input"
                    type="password"
                    value={joinSecret}
                    onChange={(e) => setJoinSecret(e.target.value)}
                  />
                )}
              </div>
            </>
          ) : null}
          {joinKindOption?.requiresConsent ? (
            <div className="rw-projects__field">
              <p className="rw-projects__consent" role="alert">
                {joinKindOption.consentText}
              </p>
              <label className="rw-projects__preset">
                <input
                  type="checkbox"
                  checked={joinConsent}
                  onChange={(e) => setJoinConsent(e.target.checked)}
                />
                上記の内容に同意します
              </label>
            </div>
          ) : null}
          <div className="rw-projects__actions">
            <Button
              variant="primary"
              onClick={() => void join()}
              disabledReason={joinReason}
            >
              取得する
            </Button>
            <Button variant="quiet" onClick={() => setJoining(false)}>
              やめる
            </Button>
          </div>
        </section>
      ) : null}

      {deleteTarget ? (
        <section className="rw-projects__panel">
          <h2 className="rw-projects__panel-title">
            プロジェクトを削除しますか
          </h2>
          <p>
            対象システム: {deleteTarget.targetSystemName || "（不明）"} /{" "}
            {deleteTarget.hasDocuments
              ? "成果物ドキュメントがあります"
              : "成果物ドキュメントはありません"}
          </p>
          <p className="rw-projects__meta">
            自動退避は {deleteTarget.backupCount}{" "}
            世代残ります（バックアップ・復元から戻せます）。
          </p>
          {deleteTarget.notice ? (
            <p className="rw-projects__message">{deleteTarget.notice}</p>
          ) : null}
          <div className="rw-projects__actions">
            <Button
              variant="primary"
              onClick={() => void confirmDelete()}
              disabledReason={
                deleteTarget.deletable ? undefined : deleteTarget.notice
              }
            >
              削除する
            </Button>
            <Button variant="quiet" onClick={() => setDeleteTarget(null)}>
              やめる
            </Button>
          </div>
        </section>
      ) : null}

      {message ? (
        <p className="rw-projects__message" role="alert">
          {message}
        </p>
      ) : null}

      <DataList
        caption="プロジェクト"
        columns={COLUMNS}
        rows={rows}
        selectedId={selected}
        onSelect={setSelected}
        empty={{
          message: "プロジェクトがありません。",
          next: "「新規作成」で作るか、「既存のプロジェクトを開く」でフォルダを選んでください。",
        }}
      />

      {/*
        `.reqweave` への移行。**起動時に無断で改名しない**。
        対象を示し、利用者が押したときだけ 1 件ずつ実行する。移行しない選択も残す。
      */}
      {migratable.length > 0 ? (
        <section className="rw-projects__migrate" aria-label="ファイルとして扱えるようにする">
          <h3 className="rw-projects__migrate-title">ファイルとして扱えるようにする</h3>
          <p className="rw-projects__hint">
            下のプロジェクトはフォルダのままです。名前を変えると、Finder で 1 個のファイルとして扱えます。
            中身と場所は変わりません。そのままでも使えます。
          </p>
          <ul className="rw-projects__migrate-list">
            {migratable.map((m) => (
              <li key={m.path}>
                <span className="rw-projects__migrate-name">{m.targetSystemName}</span>
                <span className="rw-mono rw-projects__migrate-to">→ {m.newName}</span>
                <Button
                  variant="secondary"
                  onClick={() => void migrate(m.path)}
                  disabledReason={busy ? "処理中です。" : undefined}
                >
                  名前を変える
                </Button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <div className="rw-projects__actions">
        <Button
          variant="primary"
          onClick={() => selectedProject && onOpen(selectedProject.path)}
          disabledReason={
            !selectedProject
              ? "プロジェクトを選んでください。"
              : !selectedProject.available
                ? selectedProject.notice
                : undefined
          }
        >
          開く
        </Button>
        <Button
          onClick={() => {
            if (selectedProject) {
              setRenaming(selectedProject);
              setRenameTo(selectedProject.targetSystemName);
            }
          }}
          disabledReason={
            !selectedProject
              ? "プロジェクトを選んでください。"
              : !selectedProject.available
                ? selectedProject.notice
                : undefined
          }
        >
          名前を変える
        </Button>
        <Button
          onClick={() =>
            selectedProject && void askDelete(selectedProject.path)
          }
          disabledReason={
            selectedProject ? undefined : "プロジェクトを選んでください。"
          }
        >
          削除
        </Button>
        {/* 読み込めないものは「削除」できない（実体が無い）。一覧から外す道を用意する */}
        {selectedProject && !selectedProject.available ? (
          <Button
            onClick={() => void forget(selectedProject.path)}
            disabledReason={busy ? "処理中です。" : undefined}
          >
            一覧から外す
          </Button>
        ) : null}
      </div>

      {/* 対象システム名の変更。フォルダ名も一緒に変わることを先に伝える。 */}
      {renaming ? (
        <section className="rw-projects__rename" aria-label="対象システム名を変える">
          <h3 className="rw-projects__migrate-title">対象システム名を変える</h3>
          <p className="rw-projects__hint">
            成果物のタイトルに使う名前です。変更は記録に残ります。
            フォルダの名前も一緒に変わりますが、中身と場所は変わりません。
          </p>
          <label className="rw-projects__field">
            <span className="rw-projects__label">対象システム名</span>
            <input
              className="rw-projects__input"
              value={renameTo}
              onChange={(e) => setRenameTo(e.target.value)}
              aria-label="変更後の対象システム名"
            />
          </label>
          <div className="rw-projects__actions">
            <Button
              variant="primary"
              onClick={() => void rename()}
              disabledReason={
                busy
                  ? "処理中です。"
                  : !renameTo.trim()
                    ? "対象システム名を入力してください。"
                    : renameTo.trim() === renaming.targetSystemName
                      ? "いまの名前と同じです。"
                      : undefined
              }
            >
              この名前にする
            </Button>
            <Button
              variant="quiet"
              onClick={() => {
                setRenaming(null);
                setRenameTo("");
              }}
            >
              やめる
            </Button>
          </div>
        </section>
      ) : null}
    </AppShell>
  );
}
