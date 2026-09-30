import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { clickEnabled, expectDisabledReason } from "../test/interact";
import { ProjectList } from "./ProjectList";

// フロントエンドはバックエンドの公開バインディング経由でのみ機能を呼ぶ（依存規則）。
const projects = vi.hoisted(() => vi.fn());
const domainPresets = vi.hoisted(() => vi.fn());
const chooseFolder = vi.hoisted(() => vi.fn());
const chooseProject = vi.hoisted(() => vi.fn());
const locationWarning = vi.hoisted(() => vi.fn());
const projectFolderNameFor = vi.hoisted(() => vi.fn());
const migratableProjects = vi.hoisted(() => vi.fn());
const migrateProject = vi.hoisted(() => vi.fn());
const renameProject = vi.hoisted(() => vi.fn());
const forgetProject = vi.hoisted(() => vi.fn());
const createProject = vi.hoisted(() => vi.fn());
const deletePreview = vi.hoisted(() => vi.fn());
const deleteProject = vi.hoisted(() => vi.fn());
const addExistingProject = vi.hoisted(() => vi.fn());
const syncKindOptions = vi.hoisted(() => vi.fn());
const cloneSyncProject = vi.hoisted(() => vi.fn());
vi.mock("../../wailsjs/go/binding/API", () => ({
  SyncKindOptions: syncKindOptions,
  CloneSyncProject: cloneSyncProject,
  Projects: projects,
  DomainPresets: domainPresets,
  ChooseFolder: chooseFolder,
  // macOS ではプロジェクトを 1 個のファイルに見せるため、選び方が OS で分かれる。
  // 画面は経路を 1 つだけ持ち、分岐はバインディング側にある。
  ChooseProject: chooseProject,
  LocationWarning: locationWarning,
  ProjectFolderNameFor: projectFolderNameFor,
  MigratableProjects: migratableProjects,
  MigrateProject: migrateProject,
  RenameProject: renameProject,
  ForgetProject: forgetProject,
  CreateProject: createProject,
  DeletePreview: deletePreview,
  DeleteProject: deleteProject,
  AddExistingProject: addExistingProject,
}));

const AVAILABLE = {
  path: "/work/inventory",
  projectId: "id-1",
  targetSystemName: "在庫管理システム",
  phase: "requirements",
  phaseLabel: "要件定義",
  updatedAt: "2026-08-27T05:00:00Z",
  role: "owner",
  roleLabel: "オーナー",
  available: true,
};

const UNAVAILABLE = {
  path: "/work/gone",
  projectId: "",
  targetSystemName: "",
  phase: "",
  phaseLabel: "",
  updatedAt: "",
  role: "",
  roleLabel: "",
  available: false,
  notice:
    "このプロジェクトを読み込めません。フォルダの場所と共有フォルダの接続を確認してください。",
};

const SYNC_KINDS = [
  {
    kind: "folder",
    label: "共有フォルダ上のリポジトリ",
    hint: "共有フォルダ上のフォルダのパスを指定します。",
    requiresConsent: false,
    requiresCredential: false,
  },
  {
    kind: "git_external",
    label: "外部 Git サーバ",
    hint: "https:// または ssh:// の URL で指定します。",
    requiresConsent: true,
    consentText:
      "外部 Git サーバを同期先にすると、要件定義データが利用組織の外部が運用するホストへ保管されます。",
    requiresCredential: true,
  },
];

const PRESETS = [
  { id: "sales", label: "販売", summary: "受注・価格" },
  { id: "inventory", label: "在庫", summary: "入出庫・棚卸" },
];

describe("プロジェクト一覧", () => {
  beforeEach(() => {
    projects.mockReset();
    domainPresets.mockReset();
    chooseFolder.mockReset();
    chooseProject.mockReset();
    locationWarning.mockReset();
    locationWarning.mockResolvedValue("");
    projectFolderNameFor.mockReset();
    projectFolderNameFor.mockImplementation((n: string) => Promise.resolve(`${n}.reqweave`));
    migratableProjects.mockReset();
    migratableProjects.mockResolvedValue([]);
    migrateProject.mockReset();
    renameProject.mockReset();
    forgetProject.mockReset();
    createProject.mockReset();
    deletePreview.mockReset();
    deleteProject.mockReset();
    addExistingProject.mockReset();
    addExistingProject.mockResolvedValue(AVAILABLE);
    syncKindOptions.mockReset();
    syncKindOptions.mockResolvedValue(SYNC_KINDS);
    cloneSyncProject.mockReset();
    projects.mockResolvedValue([AVAILABLE]);
    domainPresets.mockResolvedValue(PRESETS);
    createProject.mockResolvedValue(AVAILABLE);
    deleteProject.mockResolvedValue(undefined);
  });

  // 共有プロジェクトに自分の権限と作業状況（予約）を表示する
  it("自分の権限と作業状況を表示する", async () => {
    projects.mockResolvedValue([
      { ...AVAILABLE, working: ["鈴木（排他）", "田中（並行）"] },
    ]);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );

    const list = await screen.findByLabelText("プロジェクト");
    expect(
      await within(list).findByText("在庫管理システム"),
    ).toBeInTheDocument();
    expect(within(list).getByText("オーナー")).toBeInTheDocument();
    expect(
      within(list).getByText("鈴木（排他） / 田中（並行）"),
    ).toBeInTheDocument();
  });

  // 作業中のメンバーがいない場合は「—」を出す（空欄にしない）。
  it("作業状況が無ければ空欄にしない", async () => {
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    const list = await screen.findByLabelText("プロジェクト");
    await within(list).findByText("在庫管理システム");
    const row = within(list)
      .getByText("在庫管理システム")
      .closest('[role="row"]') as HTMLElement;
    expect(within(row).getAllByText("—").length).toBeGreaterThan(0);
  });

  // 対象システム名・現在フェーズ・最終更新日時を表示する
  it("一覧に対象システム名・フェーズ・最終更新・権限を表示する", async () => {
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );

    const list = await screen.findByLabelText("プロジェクト");
    // 一覧コンテナは空でも描画されるため、行が出るまで待ってから内容を検証する
    // （空のコンテナで解決する待ち方は間欠的な失敗になる）。
    expect(
      await within(list).findByText("在庫管理システム"),
    ).toBeInTheDocument();
    expect(within(list).getByText("要件定義")).toBeInTheDocument();
    expect(within(list).getByText("オーナー")).toBeInTheDocument();
    // 保存は UTC、表示はローカル
    const expected = new Date("2026-08-27T05:00:00Z");
    const pad = (n: number) => String(n).padStart(2, "0");
    expect(
      within(list).getByText(
        `${expected.getFullYear()}-${pad(expected.getMonth() + 1)}-${pad(expected.getDate())} ${pad(expected.getHours())}:${pad(expected.getMinutes())}`,
      ),
    ).toBeInTheDocument();
  });

  // プロジェクト横断の画面は統制表示を持たない
  it("プロジェクト横断の画面なので完成度・トークン消費を出さない", async () => {
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    expect(screen.queryByLabelText("プロジェクトの状態")).toBeNull();
    expect(screen.getByLabelText("現在地")).toHaveTextContent(
      "reqweave / プロジェクト",
    );
  });

  // 到達できないプロジェクトは黙って消さず、理由を示して開けないようにする
  it("読み込めないプロジェクトは理由を示し、開くを無効化する", async () => {
    projects.mockResolvedValue([UNAVAILABLE]);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );

    await screen.findByText(/このプロジェクトを読み込めません/);
    fireEvent.click(screen.getByText("（読み込めません）"));
    const open = screen.getByRole("button", { name: "開く" });
    expectDisabledReason(open, /読み込めません/);
  });

  // 対象システム名と保存先が揃うまで作成できない。業務領域は任意・複数可
  it("新規作成は名前と保存先が揃うまで実行できず、業務領域を複数選べる", async () => {
    chooseFolder.mockResolvedValue("/work/new-project");
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    fireEvent.click(screen.getByRole("button", { name: "新規作成" }));
    const create = screen.getByRole("button", { name: "作成する" });
    expect(create).toBeDisabled();

    fireEvent.change(screen.getByLabelText("対象システム名"), {
      target: { value: "受発注システム" },
    });
    fireEvent.change(screen.getByLabelText("概要（任意）"), {
      target: { value: "受注から出荷まで" },
    });
    expect(screen.getByRole("button", { name: "作成する" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "フォルダを選ぶ" }));
    await screen.findByText("/work/new-project");

    fireEvent.click(screen.getByRole("checkbox", { name: "販売" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "在庫" }));
    fireEvent.click(screen.getByRole("button", { name: "作成する" }));

    await waitFor(() => expect(createProject).toHaveBeenCalled());
    expect(createProject).toHaveBeenCalledWith({
      path: "/work/new-project",
      targetSystemName: "受発注システム",
      summary: "受注から出荷まで",
      domainPresets: ["sales", "inventory"],
    });
  });

  // 対象システム名と成果物の有無を提示し、確認操作なしに削除しない
  it("削除は確認を経てから実行する", async () => {
    deletePreview.mockResolvedValue({
      path: AVAILABLE.path,
      targetSystemName: "在庫管理システム",
      hasDocuments: true,
      backupCount: 3,
      deletable: true,
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    fireEvent.click(screen.getByText("在庫管理システム"));
    fireEvent.click(screen.getByRole("button", { name: "削除" }));

    await screen.findByText("プロジェクトを削除しますか");
    expect(
      screen.getByText(/成果物ドキュメントがあります/),
    ).toBeInTheDocument();
    expect(screen.getByText(/自動退避は 3 世代残ります/)).toBeInTheDocument();
    expect(deleteProject).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "削除する" }));
    await waitFor(() =>
      expect(deleteProject).toHaveBeenCalledWith(AVAILABLE.path),
    );
  });

  // 削除できない場合は理由を示し、実行させない
  it("削除できない場合は理由を示して実行させない", async () => {
    deletePreview.mockResolvedValue({
      path: AVAILABLE.path,
      targetSystemName: "在庫管理システム",
      hasDocuments: false,
      backupCount: 0,
      deletable: false,
      notice: "削除できるのはオーナーだけです。オーナーに依頼してください。",
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    fireEvent.click(screen.getByText("在庫管理システム"));
    fireEvent.click(screen.getByRole("button", { name: "削除" }));

    await screen.findByText(/削除できるのはオーナーだけです/);
    expect(screen.getByRole("button", { name: "削除する" })).toBeDisabled();
    expect(deleteProject).not.toHaveBeenCalled();
  });

  // 選択して再開できる
  it("選択したプロジェクトを開ける", async () => {
    const onOpen = vi.fn();
    render(
      <ProjectList
        onOpen={onOpen}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    expect(screen.getByRole("button", { name: "開く" })).toBeDisabled();
    fireEvent.click(screen.getByText("在庫管理システム"));
    fireEvent.click(screen.getByRole("button", { name: "開く" }));
    expect(onOpen).toHaveBeenCalledWith(AVAILABLE.path);
  });

  // プロジェクト一覧から AI 利用量ダッシュボードへ入れる（横断のため選択は不要）。
  it("プロジェクトを選ばずに AI 利用量ダッシュボードへ入れる", async () => {
    const onOpenUsage = vi.fn();
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={onOpenUsage}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    const button = screen.getByRole("button", { name: "AI 利用量" });
    expect(button).not.toBeDisabled();
    fireEvent.click(button);
    expect(onOpenUsage).toHaveBeenCalledTimes(1);
  });

  // プロジェクト一覧の共通メニューから消費実績へ（対象は選択中のプロジェクト）。
  it("選択したプロジェクトのトークン消費実績へ入れる（未選択では理由つきで無効）", async () => {
    const onOpenTokenUsage = vi.fn();
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={onOpenTokenUsage}
      />,
    );
    await screen.findByText("在庫管理システム");

    const button = screen.getByRole("button", { name: "トークン消費実績" });
    expect(button).toBeDisabled();
    // 上部ナビはアプリ内の要素で理由を出す（title 属性は使わない）
    expect(button).not.toHaveAttribute("title");
    fireEvent.mouseOver(button);
    expect(screen.getByRole("tooltip").textContent).toContain(
      "プロジェクトを選んでください",
    );
    fireEvent.mouseOut(button);

    fireEvent.click(screen.getByText("在庫管理システム"));
    fireEvent.click(screen.getByRole("button", { name: "トークン消費実績" }));
    expect(onOpenTokenUsage).toHaveBeenCalledWith(AVAILABLE.path);
  });

  // 機能の 1 文はまず上部ナビに添える
  it("上部ナビの各ボタンは機能を示す 1 文をアプリ内の要素で示す", async () => {
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
        onOpenSync={() => undefined}
      />,
    );
    await screen.findByText("在庫管理システム");

    const nav = [
      "設定",
      "AI 利用量",
      "トークン消費実績",
      "バックアップ・復元",
      "既存のプロジェクトを開く",
      "同期先から取得して参加",
      "同期",
      "新規作成",
    ];
    for (const name of nav) {
      const button = screen.getByRole("button", { name });
      expect(button).not.toHaveAttribute("title");
      fireEvent.mouseOver(button);
      const tip = screen.getByRole("tooltip");
      // 1 文（句点で終わる）であり、ラベルの言い換えだけで終わらない
      expect(tip.textContent?.endsWith("。")).toBe(true);
      expect(tip.textContent?.length ?? 0).toBeGreaterThan(name.length);
      expect(button.getAttribute("aria-describedby")).toBe(tip.id);
      fireEvent.mouseOut(button);
      expect(screen.queryByRole("tooltip")).toBeNull();
    }
  });

  it("プロジェクトが無いときは次の行動を案内する", async () => {
    projects.mockResolvedValue([]);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );

    // 新規作成に加えて、既存フォルダを開く経路も案内する。
    expect(
      await screen.findByText(
        /「新規作成」で作るか、「既存のプロジェクトを開く」でフォルダを選んでください/,
      ),
    ).toBeInTheDocument();
  });

  /*
   * プロジェクトを 1 件のファイルとして扱えるようにする。
   */
  it("作成前に、どんな名前のフォルダができるかを示す", async () => {
    chooseFolder.mockResolvedValue("/work");
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");
    fireEvent.click(screen.getByRole("button", { name: "新規作成" }));

    fireEvent.change(screen.getByLabelText("対象システム名"), {
      target: { value: "在庫管理システム" },
    });
    fireEvent.click(screen.getByRole("button", { name: "フォルダを選ぶ" }));

    // 名前の規則はバインディングが持つ（画面で組み立て直さない）。
    await waitFor(() =>
      expect(projectFolderNameFor).toHaveBeenCalledWith("在庫管理システム"),
    );
    expect(
      await screen.findByText(/この中に「在庫管理システム.reqweave」を作ります。/),
    ).toBeInTheDocument();
  });

  /*
   * 読み込めなくなったプロジェクトを一覧から外す。
   *
   * 「削除」は実体を消す操作で、実体が無いと実行できない。外す手段が無いと一覧に残り続ける。
   */
  it("読み込めないプロジェクトは一覧から外せる（データは消さない）", async () => {
    projects.mockResolvedValue([UNAVAILABLE]);
    forgetProject.mockResolvedValue(undefined);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    const list = await screen.findByLabelText("プロジェクト");
    fireEvent.click(within(list).getByText("（読み込めません）"));

    await clickEnabled("一覧から外す");
    await waitFor(() => expect(forgetProject).toHaveBeenCalledWith(UNAVAILABLE.path));
    // データを消していないことを伝える（消したと誤解させない）。
    expect(
      await screen.findByText(/プロジェクトのデータは消していません/),
    ).toBeInTheDocument();
  });

  it("読み込めるプロジェクトには「一覧から外す」を出さない", async () => {
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    const list = await screen.findByLabelText("プロジェクト");
    fireEvent.click(await within(list).findByText("在庫管理システム"));
    expect(screen.queryByRole("button", { name: "一覧から外す" })).toBeNull();
  });

  /*
   * 対象システム名の変更。
   *
   * 利用者の気づき「ファイル名を変更してもプロジェクト名には反映されないのですね」への対応。
   * **アプリ内の操作で名前を変え、フォルダ名を追従させる**（逆向きは行わない）。
   */
  it("対象システム名を変えると、フォルダ名も一緒に変わることを先に伝える", async () => {
    renameProject.mockResolvedValue({
      ...AVAILABLE,
      path: "/work/在庫管理システム.reqweave",
      targetSystemName: "在庫管理システム",
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    const list = await screen.findByLabelText("プロジェクト");
    fireEvent.click(await within(list).findByText("在庫管理システム"));

    fireEvent.click(screen.getByRole("button", { name: "名前を変える" }));
    const panel = await screen.findByLabelText("対象システム名を変える");
    // 何が一緒に変わり、何が変わらないかを先に言う。
    expect(within(panel).getByText(/フォルダの名前も一緒に変わります/)).toBeInTheDocument();
    expect(within(panel).getByText(/中身と場所は変わりません/)).toBeInTheDocument();
    expect(within(panel).getByText(/変更は記録に残ります/)).toBeInTheDocument();

    // 同じ名前のままでは押せない（意味のない変更を記録に残さない）。
    const apply = within(panel).getByRole("button", { name: "この名前にする" });
    expectDisabledReason(apply, "いまの名前と同じです。");

    fireEvent.change(within(panel).getByLabelText("変更後の対象システム名"), {
      target: { value: "在庫管理システム 2026" },
    });
    await clickEnabled("この名前にする");
    await waitFor(() =>
      expect(renameProject).toHaveBeenCalledWith(
        "/work/inventory",
        "在庫管理システム 2026",
      ),
    );
    expect(
      await screen.findByText(/対象システム名を「在庫管理システム」に変えました。/),
    ).toBeInTheDocument();
  });

  /*
   * 置き場所の注意。**作成は止めない**。
   * 判定は当て推量なので、誤検知でも作業が続けられることが要る。
   */
  it("クラウド同期のフォルダを選んだら注意を出すが、作成は止めない", async () => {
    chooseFolder.mockResolvedValue("/Users/x/Library/CloudStorage/Box-Box/work");
    locationWarning.mockResolvedValue(
      "Box の同期フォルダのようです。ここへ置いても作業はできますが、別の端末から同じプロジェクトを開くと内容が壊れることがあります。",
    );
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");
    fireEvent.click(screen.getByRole("button", { name: "新規作成" }));
    fireEvent.change(screen.getByLabelText("対象システム名"), {
      target: { value: "在庫管理システム" },
    });
    fireEvent.click(screen.getByRole("button", { name: "フォルダを選ぶ" }));

    expect(await screen.findByText(/Box の同期フォルダのようです/)).toBeInTheDocument();
    // 注意は出すが、作成は押せる（止めない）。
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "作成する" })).toBeEnabled(),
    );
  });

  it("拡張子の付いていないプロジェクトを示し、押したときだけ名前を変える", async () => {
    migratableProjects.mockResolvedValue([
      {
        path: "/work/在庫管理",
        targetSystemName: "在庫管理システム",
        newName: "在庫管理システム.reqweave",
      },
    ]);
    migrateProject.mockResolvedValue({
      ...AVAILABLE,
      path: "/work/在庫管理システム.reqweave",
      targetSystemName: "在庫管理システム",
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );

    const panel = await screen.findByLabelText("ファイルとして扱えるようにする");
    expect(within(panel).getByText("在庫管理システム")).toBeInTheDocument();
    expect(within(panel).getByText(/在庫管理システム.reqweave/)).toBeInTheDocument();
    // 何が変わらないかを先に言う（場所と中身は動かない）。
    expect(within(panel).getByText(/中身と場所は変わりません/)).toBeInTheDocument();

    // 起動しただけでは改名しない（利用者が押したときだけ）。
    expect(migrateProject).not.toHaveBeenCalled();

    fireEvent.click(within(panel).getByRole("button", { name: "名前を変える" }));
    await waitFor(() =>
      expect(migrateProject).toHaveBeenCalledWith("/work/在庫管理"),
    );
    expect(
      await screen.findByText(/名前を変えました。中身と場所は変わっていません。/),
    ).toBeInTheDocument();
  });

  it("移行するものが無いときは案内を出さない", async () => {
    migratableProjects.mockResolvedValue([]);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");
    expect(
      screen.queryByLabelText("ファイルとして扱えるようにする"),
    ).toBeNull();
  });

  // 既存のプロジェクトフォルダを選んで一覧へ加えられる
  it("既存のプロジェクトを開くとフォルダ選択から一覧へ追加される", async () => {
    chooseProject.mockResolvedValue("/share/共有プロジェクト");
    addExistingProject.mockResolvedValue({
      ...AVAILABLE,
      targetSystemName: "共有プロジェクト",
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");

    fireEvent.click(
      screen.getByRole("button", { name: "既存のプロジェクトを開く" }),
    );

    await waitFor(() =>
      expect(addExistingProject).toHaveBeenCalledWith(
        "/share/共有プロジェクト",
      ),
    );
    // 一覧を読み直す（追加が画面へ反映される）。
    await waitFor(() => expect(projects).toHaveBeenCalledTimes(2));
    expect(
      await screen.findByText(/「共有プロジェクト」を一覧に追加しました。/),
    ).toBeInTheDocument();
  });

  // フォルダ選択を取り消したときは何も呼ばない
  it("フォルダ選択を取り消すと追加を呼ばない", async () => {
    chooseProject.mockResolvedValue("");
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");

    fireEvent.click(
      screen.getByRole("button", { name: "既存のプロジェクトを開く" }),
    );

    await waitFor(() => expect(chooseProject).toHaveBeenCalled());
    expect(addExistingProject).not.toHaveBeenCalled();
  });

  // プロジェクトデータでないフォルダは、バインディングの文言をそのまま出す
  it("プロジェクトのフォルダでない場合はバインディングの文言を表示する", async () => {
    chooseProject.mockResolvedValue("/share/ただのフォルダ");
    addExistingProject.mockRejectedValue(
      new Error(
        "選んだフォルダはプロジェクトのフォルダではありません。project.json があるフォルダを選んでください。",
      ),
    );
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");

    fireEvent.click(
      screen.getByRole("button", { name: "既存のプロジェクトを開く" }),
    );

    expect(
      await screen.findByText(/プロジェクトのフォルダではありません/),
    ).toBeInTheDocument();
    // 画面側で判定をやり直さない（一覧の読み直しは行うが、独自の可否判定は持たない）。
    expect(await screen.findByText(/選んでください/)).toBeInTheDocument();
  });

  // メンバー未登録のときは、追加はされたうえで開けない旨が示される
  it("メンバー未登録のときは注意書きを表示する", async () => {
    chooseProject.mockResolvedValue("/share/共有プロジェクト");
    addExistingProject.mockResolvedValue({
      ...AVAILABLE,
      targetSystemName: "共有プロジェクト",
      available: false,
      notice:
        "このプロジェクトのメンバーに登録されていません。オーナーに登録を依頼してください。",
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");

    fireEvent.click(
      screen.getByRole("button", { name: "既存のプロジェクトを開く" }),
    );

    expect(
      await screen.findByText(/メンバーに登録されていません/),
    ).toBeInTheDocument();
  });

  // 共同プロジェクトの同期状態を行に出す。
  it("同期先の設定と未反映の有無を表示する", async () => {
    projects.mockResolvedValue([
      {
        ...AVAILABLE,
        syncConfigured: true,
        syncKindLabel: "共有フォルダ上のリポジトリ",
        lastSyncedAt: "2026-09-02 10:00",
        hasUnpublished: true,
      },
      { ...AVAILABLE, path: "/work/local", targetSystemName: "単独プロジェクト" },
    ]);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    const list = await screen.findByLabelText("プロジェクト");
    expect(await within(list).findByText("未反映あり")).toBeInTheDocument();
    expect(within(list).getByText("2026-09-02 10:00")).toBeInTheDocument();
    expect(within(list).getByText("この端末のみ")).toBeInTheDocument();
  });

  // 同期先から取得して参加する（外部 Git は同意を経る）。
  it("同期先から取得して参加できる", async () => {
    chooseFolder.mockResolvedValue("/work");
    cloneSyncProject.mockResolvedValue({
      done: true,
      project: AVAILABLE,
      notice: "「在庫管理システム」を取得しました（要件項目 3）。一覧から開いて作業を始められます。",
    });
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");

    fireEvent.click(
      screen.getByRole("button", { name: "同期先から取得して参加" }),
    );
    fireEvent.change(await screen.findByLabelText("同期先の所在"), {
      target: { value: "/share/proj.git" },
    });
    // 作成先が未指定のうちは取得できない
    expect(screen.getByRole("button", { name: "取得する" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "親フォルダを選ぶ" }));
    await waitFor(() => expect(chooseFolder).toHaveBeenCalled());
    fireEvent.change(screen.getByLabelText("新しく作るフォルダ名"), {
      target: { value: "在庫管理システム" },
    });

    fireEvent.click(screen.getByRole("button", { name: "取得する" }));
    await waitFor(() =>
      expect(cloneSyncProject).toHaveBeenCalledWith({
        kind: "folder",
        location: "/share/proj.git",
        dest: "/work/在庫管理システム",
        credentialKind: "ssh_key",
        username: "",
        secret: "",
        externalConsent: false,
      }),
    );
    expect(await screen.findByText(/取得しました/)).toBeInTheDocument();
  });

  // 外部 Git サーバから取得するときも明示表示と同意を経る。
  it("外部 Git サーバからの取得は同意するまで実行できない", async () => {
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
      />,
    );
    await screen.findByLabelText("プロジェクト");
    fireEvent.click(
      screen.getByRole("button", { name: "同期先から取得して参加" }),
    );
    fireEvent.click(
      await screen.findByRole("radio", { name: /外部 Git サーバ/ }),
    );
    expect(
      screen.getByText(/利用組織の外部が運用するホストへ保管されます/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "取得する" })).toBeDisabled();
    expect(cloneSyncProject).not.toHaveBeenCalled();
  });

  // プロジェクト一覧から同期パネルへ移れる。
  it("同期先が設定されたプロジェクトだけ同期パネルへ移れる", async () => {
    const onOpenSync = vi.fn();
    projects.mockResolvedValue([
      { ...AVAILABLE, syncConfigured: true, syncKindLabel: "共有フォルダ上のリポジトリ" },
      { ...AVAILABLE, path: "/work/local", targetSystemName: "単独プロジェクト" },
    ]);
    render(
      <ProjectList
        onOpen={() => undefined}
        onOpenSettings={() => undefined}
        onOpenBackup={() => undefined}
        onOpenUsage={() => undefined}
        onOpenTokenUsage={() => undefined}
        onOpenSync={onOpenSync}
      />,
    );
    const list = await screen.findByLabelText("プロジェクト");
    // 未選択のうちは選ぶよう促す
    expect(screen.getByRole("button", { name: "同期" })).toBeDisabled();

    fireEvent.click(within(list).getByText("単独プロジェクト"));
    const disabled = screen.getByRole("button", { name: "同期" });
    expect(disabled).toBeDisabled();
    // 理由はアプリ内の要素で示す
    fireEvent.mouseOver(disabled);
    expect(screen.getByRole("tooltip")).toHaveTextContent(
      "このプロジェクトには同期先が設定されていません。",
    );
    fireEvent.mouseOut(disabled);

    fireEvent.click(within(list).getByText("在庫管理システム"));
    fireEvent.click(screen.getByRole("button", { name: "同期" }));
    expect(onOpenSync).toHaveBeenCalledWith("/work/inventory");
  });
});
