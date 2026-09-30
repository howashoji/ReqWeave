import { useCallback, useEffect, useState } from "react";
import {
  AddMember,
  ChangeMemberRole,
  CorrectMemberAuthorID,
  CurrentPermission,
  Members as ListMembers,
  ReleaseReservation,
  ReleaseWindowLock,
  RemoveMember,
  Reservations,
  TakeOverOwner,
  WindowLocks,
} from "../../wailsjs/go/binding/API";
import { binding } from "../../wailsjs/go/models";
import { Banner, Button, Chip, DataList, errorText } from "../ui";
import "./Members.css";

/**
 * メンバー管理。画面型は一覧・管理系。
 *
 * メンバー一覧・追加・削除・権限変更・オーナー移譲・オーナー不在時の引き継ぎ・メールアドレス（利用者 ID）の訂正と、
 * 予約と作業状況の一覧・解除。予約は同期先へ反映して初めて他メンバーに効き、
 * 表示は最後に取り込んだ時点の情報であることを一覧の注記で示す。
 * 同一端末の別ウィンドウが処理中のロックは作業者名を出さずに別表で扱う。
 * 同期先の設定への導線を持つ。
 *
 * 権限の判定はバインディング（CurrentPermission）が行い、画面は結果で
 * 無効化と理由表示だけを行う（操作を隠さない。何ができないかと理由を利用者が知れるように）。
 * 確認が必要な操作（引き継ぎ・強制解除・削除）はアプリ内の確認ダイアログで受ける
 * （ネイティブ confirm はテーマに追従せず、自動テストもできないため使わない）。
 */

/** 権限の選択肢（値集合はこの 3 つで閉じている）。 */
const ROLES = [
  { value: "owner", label: "オーナー" },
  { value: "editor", label: "編集" },
  { value: "viewer", label: "閲覧" },
] as const;

/** 確認を要する操作（アプリ内ダイアログで受ける）。 */
type Confirmation =
  | { kind: "remove"; authorId: string; displayName: string }
  | { kind: "takeover" }
  | {
      kind: "release-reservation";
      target: string;
      targetLabel: string;
      holder: string;
      holderId: string;
    }
  | { kind: "release-lock"; target: string; targetLabel: string };

export function Members({
  onBack,
  onOpenSyncRemote,
}: {
  onBack: () => void;
  /** onOpenSyncRemote は同期先の設定（オーナーのみ変更可）へ移る。 */
  onOpenSyncRemote?: () => void;
}) {
  const [members, setMembers] = useState<binding.MemberView[]>([]);
  const [reservations, setReservations] =
    useState<binding.ReservationsView | null>(null);
  const [windowLocks, setWindowLocks] = useState<binding.WindowLockView[]>([]);
  const [permission, setPermission] = useState<binding.PermissionView | null>(
    null,
  );
  const [authorId, setAuthorId] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState<string>("editor");
  const [correcting, setCorrecting] = useState("");
  const [correctedId, setCorrectedId] = useState("");
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const [message, setMessage] = useState("");
  const [tone, setTone] = useState<"info" | "accent" | "warn" | "danger">(
    "info",
  );
  const [busy, setBusy] = useState(false);

  const fail = useCallback((err: unknown) => {
    setTone("danger");
    setMessage(errorText(err));
  }, []);

  const reload = useCallback(async () => {
    try {
      setMembers(await ListMembers());
      setReservations(await Reservations());
      setWindowLocks(await WindowLocks());
      setPermission(await CurrentPermission());
    } catch (err: unknown) {
      fail(err);
    }
  }, [fail]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const canManage = permission?.canManageMembers ?? false;
  const manageReason =
    permission?.manageReason ?? "メンバー管理はオーナー権限が必要です。";
  const canEdit = permission?.canEdit ?? false;
  const editReason = permission?.reason ?? "この操作は編集権限が必要です。";
  const manage = (reason?: string) => (canManage ? reason : manageReason);

  const run = async (label: string, fn: () => Promise<unknown>) => {
    setBusy(true);
    setMessage("");
    try {
      await fn();
      await reload();
      setTone("accent");
      setMessage(`${label}しました。`);
    } catch (err: unknown) {
      fail(err);
    } finally {
      setBusy(false);
      setConfirmation(null);
    }
  };

  const add = () =>
    run("メンバーを追加", async () => {
      await AddMember({ authorId, displayName, role } as binding.MemberRequest);
      setAuthorId("");
      setDisplayName("");
    });

  const memberRows = members.map((m) => ({
    id: m.authorId,
    cells: {
      member: (
        <span>
          {m.displayName}
          {m.self ? <Chip tone="info">自分</Chip> : null}
          <span className="rw-members__id rw-mono">{m.authorId}</span>
        </span>
      ),
      role: (
        <select
          value={m.role}
          onChange={(e) =>
            void run("権限を変更", () =>
              ChangeMemberRole({
                authorId: m.authorId,
                role: e.target.value,
              } as binding.MemberRequest),
            )
          }
          aria-label={`${m.displayName} の権限`}
          disabled={!canManage || busy}
        >
          {ROLES.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
      ),
      addedAt: <span className="rw-mono">{m.addedAt}</span>,
      actions: (
        <span className="rw-members__actions">
          <Button
            variant="secondary"
            onClick={() => setCorrecting(m.authorId)}
            disabledReason={manage(busy ? "処理中です。" : undefined)}
          >
            メールアドレスを訂正
          </Button>
          <Button
            variant="secondary"
            onClick={() =>
              setConfirmation({
                kind: "remove",
                authorId: m.authorId,
                displayName: m.displayName,
              })
            }
            disabledReason={manage(busy ? "処理中です。" : undefined)}
          >
            削除
          </Button>
        </span>
      ),
    },
  }));

  const reservationRows = (reservations?.items ?? []).map((r) => ({
    id: `${r.target}\u0000${r.authorId}`,
    cells: {
      target: r.targetLabel,
      worker: (
        <span>
          {r.author}
          {r.self ? <Chip tone="info">自分</Chip> : null}
        </span>
      ),
      mode: (
        <Chip tone={r.mode === "exclusive" ? "warn" : "info"}>
          {r.modeLabel}
        </Chip>
      ),
      startedAt: <span className="rw-mono">{r.startedAt}</span>,
      actions: (
        <Button
          variant="secondary"
          onClick={() => {
            if (r.self) {
              void run("予約を解除", () =>
                ReleaseReservation(r.target, r.authorId, false),
              );
              return;
            }
            setConfirmation({
              kind: "release-reservation",
              target: r.target,
              targetLabel: r.targetLabel,
              holder: r.author,
              holderId: r.authorId,
            });
          }}
          disabledReason={
            canEdit ? (busy ? "処理中です。" : undefined) : editReason
          }
        >
          解除
        </Button>
      ),
    },
  }));

  const windowLockRows = windowLocks.map((l) => ({
    id: l.target,
    cells: {
      target: l.targetLabel,
      state: (
        <span>
          {l.self ? (
            <Chip tone="info">このウィンドウ</Chip>
          ) : (
            <Chip tone="warn">別のウィンドウ</Chip>
          )}
          {l.stale ? <Chip tone="warn">残留の可能性</Chip> : null}
        </span>
      ),
      acquiredAt: <span className="rw-mono">{l.acquiredAt ?? ""}</span>,
      actions: (
        <Button
          variant="secondary"
          onClick={() =>
            setConfirmation({
              kind: "release-lock",
              target: l.target,
              targetLabel: l.targetLabel,
            })
          }
          disabledReason={
            l.self
              ? "このウィンドウが処理中です。"
              : canEdit
                ? busy
                  ? "処理中です。"
                  : undefined
                : editReason
          }
        >
          解除
        </Button>
      ),
    },
  }));

  return (
    <section className="rw-members" aria-label="メンバー管理">
      <header className="rw-members__head">
        <h2 className="rw-members__title">メンバーと作業状況</h2>
        <div className="rw-members__actions">
          {onOpenSyncRemote ? (
            <Button onClick={onOpenSyncRemote}>同期先の設定</Button>
          ) : null}
          <Button onClick={onBack}>対話へ戻る</Button>
        </div>
      </header>

      {message ? (
        <Banner
          tone={tone}
          title={message}
          onDismiss={() => setMessage("")}
          onDefer={() => setMessage("")}
        />
      ) : null}

      {permission ? (
        <p className="rw-members__meta">
          自分の権限: {permission.roleLabel}
          {canManage ? "" : `（${manageReason}）`}
        </p>
      ) : null}

      <DataList
        caption="メンバー"
        columns={[
          { key: "member", label: "メンバー" },
          { key: "role", label: "権限" },
          { key: "addedAt", label: "登録日時", mono: true },
          { key: "actions", label: "操作" },
        ]}
        rows={memberRows}
        empty={{
          message: "メンバーがいません。",
          next: "下の「メンバーを追加」で、メールアドレス（利用者 ID）と権限を指定して追加してください。",
        }}
      />

      <h3 className="rw-members__section">メンバーを追加</h3>
      <div className="rw-members__form">
        <label className="rw-members__field">
          <span>メールアドレス（利用者 ID）</span>
          <input
            value={authorId}
            onChange={(e) => setAuthorId(e.target.value)}
            aria-label="追加するメールアドレス（利用者 ID）"
            placeholder="name@example.co.jp"
            disabled={!canManage}
          />
        </label>
        <label className="rw-members__field">
          <span>表示名</span>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            aria-label="追加する表示名"
            disabled={!canManage}
          />
        </label>
        <label className="rw-members__field">
          <span>権限</span>
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            aria-label="追加する権限"
            disabled={!canManage}
          >
            {ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
        <Button
          variant="primary"
          onClick={() => void add()}
          disabledReason={manage(
            busy
              ? "処理中です。"
              : authorId.trim() && displayName.trim()
                ? undefined
                : "メールアドレスと表示名を入力してください。",
          )}
        >
          追加する
        </Button>
      </div>

      {correcting ? (
        <div
          className="rw-members__form"
          role="group"
          aria-label="メールアドレス（利用者 ID）の訂正"
        >
          <p className="rw-members__meta">
            訂正前: <span className="rw-mono">{correcting}</span>
          </p>
          <label className="rw-members__field">
            <span>訂正後のメールアドレス</span>
            <input
              value={correctedId}
              onChange={(e) => setCorrectedId(e.target.value)}
              aria-label="訂正後のメールアドレス"
            />
          </label>
          <Button
            variant="secondary"
            onClick={() =>
              void run("メールアドレスを訂正", async () => {
                await CorrectMemberAuthorID(correcting, correctedId);
                setCorrecting("");
                setCorrectedId("");
              })
            }
            disabledReason={
              correctedId.trim()
                ? undefined
                : "訂正後のメールアドレスを入力してください。"
            }
          >
            この内容で訂正する
          </Button>
          <Button variant="quiet" onClick={() => setCorrecting("")}>
            取消
          </Button>
        </div>
      ) : null}

      <h3 className="rw-members__section">オーナーが不在のとき</h3>
      <p className="rw-members__meta">
        オーナーに連絡がつかない場合、編集権限のメンバーがオーナーを引き継げます。実行は変更履歴に残ります。
      </p>
      <Button
        onClick={() => setConfirmation({ kind: "takeover" })}
        disabledReason={
          canEdit ? (busy ? "処理中です。" : undefined) : editReason
        }
      >
        オーナーを引き継ぐ
      </Button>

      <h3 className="rw-members__section">予約と作業状況</h3>
      <p className="rw-members__meta">{reservations?.notice ?? ""}</p>
      <DataList
        caption="予約と作業状況"
        columns={[
          { key: "target", label: "対象" },
          { key: "worker", label: "作業者" },
          { key: "mode", label: "進め方" },
          { key: "startedAt", label: "着手日時", mono: true },
          { key: "actions", label: "操作" },
        ]}
        rows={reservationRows}
        empty={{
          message: "進行中の作業はありません。",
          automatic:
            "メンバーが予約を反映すると、取り込んだあとにここへ表示されます。",
        }}
      />

      <h3 className="rw-members__section">
        このプロジェクトを開いているウィンドウ
      </h3>
      <p className="rw-members__meta">
        同じ端末で同じプロジェクトを開いている処理です。他のメンバーの作業状況ではありません。
      </p>
      <DataList
        caption="処理中のウィンドウ"
        columns={[
          { key: "target", label: "対象" },
          { key: "state", label: "状態" },
          { key: "acquiredAt", label: "開始日時", mono: true },
          { key: "actions", label: "操作" },
        ]}
        rows={windowLockRows}
        empty={{
          message: "処理中のウィンドウはありません。",
          automatic:
            "同じ端末でこのプロジェクトを開くと、その処理がここへ表示されます。",
        }}
      />

      {confirmation ? (
        <ConfirmDialog
          confirmation={confirmation}
          busy={busy}
          onCancel={() => setConfirmation(null)}
          onConfirm={() => {
            if (confirmation.kind === "remove") {
              void run("メンバーを削除", () =>
                RemoveMember(confirmation.authorId),
              );
            } else if (confirmation.kind === "takeover") {
              void run("オーナーを引き継ぎ", () => TakeOverOwner(true));
            } else if (confirmation.kind === "release-reservation") {
              void run("予約を解除", () =>
                ReleaseReservation(
                  confirmation.target,
                  confirmation.holderId,
                  true,
                ),
              );
            } else {
              void run("ロックを解除", () =>
                ReleaseWindowLock(confirmation.target, true),
              );
            }
          }}
        />
      ) : null}
    </section>
  );
}

/** 確認ダイアログ（アプリ内。ネイティブ confirm を使わない）。 */
function ConfirmDialog({
  confirmation,
  busy,
  onCancel,
  onConfirm,
}: {
  confirmation: Confirmation;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const text =
    confirmation.kind === "remove"
      ? `${confirmation.displayName}（${confirmation.authorId}）をメンバーから削除します。よろしいですか。`
      : confirmation.kind === "takeover"
        ? "オーナーを引き継ぎます。オーナーに連絡がつかない場合の操作です。実行は作業者名・日時つきで変更履歴に残ります。"
        : confirmation.kind === "release-reservation"
          ? `${confirmation.targetLabel} の予約を解除します（予約者: ${confirmation.holder}）。予約者が作業中の場合があります。解除は作業者名・日時つきで変更履歴に残ります。`
          : `${confirmation.targetLabel} のロックを解除します。同じ端末の別のウィンドウが処理中でないことを確認してください。処理中に解除すると、その処理の保存が失われることがあります。`;

  return (
    <div className="rw-members__confirm" role="alertdialog" aria-label="確認">
      <p>{text}</p>
      <div className="rw-members__actions">
        <Button
          variant="primary"
          onClick={onConfirm}
          disabledReason={busy ? "処理中です。" : undefined}
        >
          実行する
        </Button>
        <Button variant="quiet" onClick={onCancel}>
          取消
        </Button>
      </div>
    </div>
  );
}
