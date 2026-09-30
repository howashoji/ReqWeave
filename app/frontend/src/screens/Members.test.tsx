import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Members } from "./Members";
import {clickEnabled, clickEnabledBy, expectDisabledReason} from "../test/interact";

const listMembers = vi.hoisted(() => vi.fn());
const addMember = vi.hoisted(() => vi.fn());
const removeMember = vi.hoisted(() => vi.fn());
const changeMemberRole = vi.hoisted(() => vi.fn());
const takeOverOwner = vi.hoisted(() => vi.fn());
const correctMemberAuthorID = vi.hoisted(() => vi.fn());
const reservations = vi.hoisted(() => vi.fn());
const releaseReservation = vi.hoisted(() => vi.fn());
const windowLocks = vi.hoisted(() => vi.fn());
const releaseWindowLock = vi.hoisted(() => vi.fn());
const currentPermission = vi.hoisted(() => vi.fn());

vi.mock("../../wailsjs/go/binding/API", () => ({
  Members: listMembers,
  AddMember: addMember,
  RemoveMember: removeMember,
  ChangeMemberRole: changeMemberRole,
  TakeOverOwner: takeOverOwner,
  CorrectMemberAuthorID: correctMemberAuthorID,
  Reservations: reservations,
  ReleaseReservation: releaseReservation,
  WindowLocks: windowLocks,
  ReleaseWindowLock: releaseWindowLock,
  CurrentPermission: currentPermission,
}));

const OWNER = {
  authorId: "k.sato@example.co.jp",
  displayName: "佐藤",
  role: "owner",
  roleLabel: "オーナー",
  addedAt: "2026-08-01 10:00",
  addedBy: "k.sato@example.co.jp",
  self: true,
};
const EDITOR = {
  authorId: "y.suzuki@example.co.jp",
  displayName: "鈴木",
  role: "editor",
  roleLabel: "編集",
  addedAt: "2026-08-02 11:00",
  addedBy: "k.sato@example.co.jp",
  self: false,
};
const NOTICE =
  "表示は最後に取り込んだ時点の情報です。予約は同期先へ反映して初めて他のメンバーに効き、他のメンバーの予約は取り込むまで表示されません。";
const OTHERS_RESERVATION = {
  target: "terms",
  targetLabel: "用語集",
  mode: "exclusive",
  modeLabel: "排他（1 名で進める）",
  author: "鈴木",
  authorId: "y.suzuki@example.co.jp",
  startedAt: "2026-08-31 09:00",
  self: false,
};
const OWN_RESERVATION = {
  target: "roster",
  targetLabel: "ステークホルダー名簿",
  mode: "concurrent",
  modeLabel: "並行（同時に進める）",
  author: "佐藤",
  authorId: "k.sato@example.co.jp",
  startedAt: "2026-09-01 10:00",
  self: true,
};
const STALE_WINDOW_LOCK = {
  target: "documents-requirements",
  targetLabel: "要件定義書",
  acquiredAt: "2026-08-31 09:30",
  stale: true,
  self: false,
};

/** メンバー一覧のスコープ（作業者名が予約一覧にも出るため、表で絞る）。 */
async function memberTable() {
  return await screen.findByRole("table", { name: "メンバー" });
}

/**
 * メンバー一覧の行を**その場で引き直す**。
 *
 * 掴んだ行を持ち回すと、読み出しの完了で表が作り直されたときに切り離された節点を指したままになり、
 * 「いつまでも有効にならないボタン」を待ち続ける。
 */
function reservationRow(label: string): HTMLElement {
  return within(screen.getByRole("table", { name: "予約と作業状況" }))
    .getByText(label)
    .closest('[role="row"]') as HTMLElement;
}

function memberRow(name: string): HTMLElement {
  return within(screen.getByRole("table", { name: "メンバー" }))
    .getByText(name)
    .closest('[role="row"]') as HTMLElement;
}

describe("メンバー管理", () => {
  beforeEach(() => {
    listMembers.mockReset().mockResolvedValue([OWNER, EDITOR]);
    addMember.mockReset().mockResolvedValue(EDITOR);
    removeMember.mockReset().mockResolvedValue(undefined);
    changeMemberRole
      .mockReset()
      .mockResolvedValue({ ...EDITOR, role: "viewer", roleLabel: "閲覧" });
    takeOverOwner
      .mockReset()
      .mockResolvedValue({ ...EDITOR, role: "owner", roleLabel: "オーナー" });
    correctMemberAuthorID
      .mockReset()
      .mockResolvedValue({ ...EDITOR, authorId: "y.tanaka@example.co.jp" });
    reservations
      .mockReset()
      .mockResolvedValue({
        items: [OTHERS_RESERVATION, OWN_RESERVATION],
        notice: NOTICE,
      });
    releaseReservation.mockReset().mockResolvedValue(undefined);
    windowLocks.mockReset().mockResolvedValue([STALE_WINDOW_LOCK]);
    releaseWindowLock.mockReset().mockResolvedValue(undefined);
    currentPermission.mockReset().mockResolvedValue({
      role: "owner",
      roleLabel: "オーナー",
      canEdit: true,
      canManageMembers: true,
    });
  });

  // 一覧にメールアドレス（利用者 ID）・表示名・権限・登録日時が出る。
  it("メンバー一覧にメールアドレス・表示名・権限・登録日時を表示する", async () => {
    render(<Members onBack={() => undefined} />);
    const table = await memberTable();
    expect(within(table).getByText("鈴木")).toBeTruthy();
    expect(within(table).getByText("y.suzuki@example.co.jp")).toBeTruthy();
    expect(within(table).getByText("2026-08-02 11:00")).toBeTruthy();
    expect(
      (screen.getByLabelText("鈴木 の権限") as HTMLSelectElement).value,
    ).toBe("editor");
    expect(within(table).getByText("佐藤")).toBeTruthy();
  });

  // 追加・権限変更・削除（削除は確認を経る）。
  it("オーナーは追加・権限変更・削除ができ、削除は確認を経る", async () => {
    render(<Members onBack={() => undefined} />);
    await memberTable();

    fireEvent.change(screen.getByLabelText("追加するメールアドレス（利用者 ID）"), {
      target: { value: "y.tanaka@example.co.jp" },
    });
    fireEvent.change(screen.getByLabelText("追加する表示名"), {
      target: { value: "田中" },
    });
    // 押せる状態になるまで待ってから押す（無効なボタンへの click は黙って無視される）。
    await clickEnabled("追加する");
    await waitFor(() =>
      expect(addMember).toHaveBeenCalledWith({
        authorId: "y.tanaka@example.co.jp",
        displayName: "田中",
        role: "editor",
      }),
    );

    fireEvent.change(screen.getByLabelText("鈴木 の権限"), {
      target: { value: "viewer" },
    });
    await waitFor(() =>
      expect(changeMemberRole).toHaveBeenCalledWith({
        authorId: "y.suzuki@example.co.jp",
        role: "viewer",
      }),
    );

    // 削除は確認ダイアログを経る（ネイティブ confirm を使わない）。
    // **処理中は操作が無効になる**ため、押せるようになるまで待つ。
    await clickEnabledBy(() =>
      within(memberRow("鈴木")).getByRole("button", { name: "削除" }),
    );
    expect(removeMember).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog.textContent).toContain("鈴木");
    await clickEnabledBy(() =>
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "実行する",
      }),
    );
    await waitFor(() =>
      expect(removeMember).toHaveBeenCalledWith("y.suzuki@example.co.jp"),
    );
  });

  // メールアドレス（利用者 ID）の訂正は旧値・新値の確認表示を経る。
  it("メールアドレスの訂正は旧値と新値を確認してから実行する", async () => {
    render(<Members onBack={() => undefined} />);
    await memberTable();
    await clickEnabledBy(() =>
      within(memberRow("鈴木")).getByRole("button", {
        name: "メールアドレスを訂正",
      }),
    );
    const form = await screen.findByRole("group", { name: "メールアドレス（利用者 ID）の訂正" });
    expect(form.textContent).toContain("y.suzuki@example.co.jp");
    expect(correctMemberAuthorID).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("訂正後のメールアドレス"), {
      target: { value: "y.tanaka@example.co.jp" },
    });
    // 押せる状態になるまで待ってから押す（無効なボタンへの click は黙って無視される）。
    await clickEnabled("この内容で訂正する");
    await waitFor(() =>
      expect(correctMemberAuthorID).toHaveBeenCalledWith(
        "y.suzuki@example.co.jp",
        "y.tanaka@example.co.jp",
      ),
    );
  });

  // オーナーの引き継ぎは警告と明示同意を経る。
  it("オーナーの引き継ぎは警告の確認を経てのみ実行される", async () => {
    render(<Members onBack={() => undefined} />);
    await memberTable();

    // 押せる状態になるまで待ってから押す（無効なボタンへの click は黙って無視される）。
    await clickEnabled("オーナーを引き継ぐ");
    expect(takeOverOwner).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog.textContent).toContain("変更履歴");
    await clickEnabledBy(() =>
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "実行する",
      }),
    );
    await waitFor(() => expect(takeOverOwner).toHaveBeenCalledWith(true));
  });

  // 予約一覧に対象・作業者・進め方・着手日時と「最後に取り込んだ時点」の注記が出る。
  it("予約と作業状況の一覧に対象・作業者・進め方・着手日時と注記を表示する", async () => {
    render(<Members onBack={() => undefined} />);
    // 表の枠は中身より先に描かれる（一覧は別の読み出しで届く）。**中身が届くまで待ってから**
    // 表を引き直す。枠を先に掴むと、中身の到着で作り直された表を掴んだままになる。
    await screen.findByText("用語集");
    const table = screen.getByRole("table", { name: "予約と作業状況" });
    expect(within(table).getByText("用語集")).toBeTruthy();
    expect(within(table).getByText("排他（1 名で進める）")).toBeTruthy();
    expect(within(table).getByText("2026-08-31 09:00")).toBeTruthy();
    expect(within(table).getByText("並行（同時に進める）")).toBeTruthy();
    expect(screen.getByText(NOTICE)).toBeTruthy();
  });

  // 本人の予約は確認なしで解除でき、他メンバーの予約は確認を経てのみ解除される。
  it("自分の予約はそのまま、他メンバーの予約は確認つきで解除する", async () => {
    render(<Members onBack={() => undefined} />);
    // 中身が届くまで待つ（表の枠だけを待つと並行実行時に落ちる）。
    await screen.findByText("ステークホルダー名簿");
    // 行は毎回引き直し、押せるようになるまで待つ。
    await clickEnabledBy(() =>
      within(reservationRow("ステークホルダー名簿")).getByRole("button", {
        name: "解除",
      }),
    );
    await waitFor(() =>
      expect(releaseReservation).toHaveBeenCalledWith(
        "roster",
        "k.sato@example.co.jp",
        false,
      ),
    );
    expect(screen.queryByRole("alertdialog")).toBeNull();

    releaseReservation.mockClear();
    await screen.findByText("用語集");
    await clickEnabledBy(() =>
      within(reservationRow("用語集")).getByRole("button", {name: "解除"}),
    );
    expect(releaseReservation).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog.textContent).toContain("鈴木");
    await clickEnabledBy(() =>
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "実行する",
      }),
    );
    await waitFor(() =>
      expect(releaseReservation).toHaveBeenCalledWith(
        "terms",
        "y.suzuki@example.co.jp",
        true,
      ),
    );
  });

  // 同一端末の別ウィンドウのロックは作業者名を出さず、確認つきで解除する。
  it("別ウィンドウの残留ロックを作業者名なしで示し、確認つきで解除する", async () => {
    render(<Members onBack={() => undefined} />);
    // ここも中身が届くまで待ってから表を引き直す。ロックの読み出しは一覧より後に返る。
    await screen.findByText("要件定義書");
    const table = screen.getByRole("table", {
      name: "処理中のウィンドウ",
    });
    expect(within(table).getByText("要件定義書")).toBeTruthy();
    expect(within(table).getByText("別のウィンドウ")).toBeTruthy();
    expect(within(table).getByText("残留の可能性")).toBeTruthy();
    expect(within(table).queryByText("鈴木")).toBeNull();

    await clickEnabledBy(() =>
      within(
        within(screen.getByRole("table", {name: "処理中のウィンドウ"}))
          .getByText("要件定義書")
          .closest('[role="row"]') as HTMLElement,
      ).getByRole("button", {name: "解除"}),
    );
    expect(releaseWindowLock).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog.textContent).toContain("別のウィンドウ");
    await clickEnabledBy(() =>
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "実行する",
      }),
    );
    await waitFor(() =>
      expect(releaseWindowLock).toHaveBeenCalledWith(
        "documents-requirements",
        true,
      ),
    );
  });

  // オーナー以外では管理操作が非表示ではなく無効化され、理由が出る。
  it("オーナー以外ではメンバー管理が無効化され、理由が表示される", async () => {
    currentPermission.mockResolvedValue({
      role: "editor",
      roleLabel: "編集",
      canEdit: true,
      canManageMembers: false,
      manageReason: "メンバー管理はオーナー権限が必要です（現在は編集）。",
    });
    render(<Members onBack={() => undefined} />);
    const table = await memberTable();

    const add = screen.getByRole("button", { name: "追加する" });
    expectDisabledReason(add, /オーナー権限が必要です/);
    expect(
      (screen.getByLabelText("鈴木 の権限") as HTMLSelectElement).disabled,
    ).toBe(true);
    // 参照はできる（操作を隠さない）。
    expect(within(table).getByText("y.suzuki@example.co.jp")).toBeTruthy();
    // 予約の解除は編集権限で可能。
    // 一覧の中身が届くまで待つ（枠だけを待つと解除ボタンがまだ無い）。
    await screen.findByText("用語集");
    expect(
      within(screen.getByRole("table", { name: "予約と作業状況" }))
        .getAllByRole("button", { name: "解除" })[0]
        .hasAttribute("disabled"),
    ).toBe(false);
  });
});
