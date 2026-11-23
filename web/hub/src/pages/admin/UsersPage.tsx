// 使用者管理（HACC-R12、R17，admin）：列出帳號、登入方式與群組；手動指派或移除群組、停用與恢復。
// admins 只能在設定檔或 notesteme-hub admin grant 指定（403 admin_by_config），members 是每個人都有的群組，
// 兩者不提供操作。API 沒有列出設定檔群組的路由：群組名稱用輸入框（附內建群組的建議）。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { fieldErrorsOf } from "../../api/fieldErrors";
import { queryKeys } from "../../api/queryKeys";
import type { AdminUserView, Me } from "../../api/types";
import {
  Button,
  ConfirmDialog,
  EmptyState,
  ErrorCallout,
  Spinner,
  TextField,
  useToast,
} from "../../components";
import { formatDate, formatDateTime } from "../../i18n";
import { AdminFrame, NoteField } from "./AdminFrame";

const FIXED = new Set(["members", "admins"]);

export function UsersPage() {
  return (
    <AdminFrame tab="users" need="admin">
      {(me) => <Users me={me} />}
    </AdminFrame>
  );
}

type Pending =
  | { kind: "add"; user: AdminUserView }
  | { kind: "remove"; user: AdminUserView; group: string }
  | { kind: "suspend"; user: AdminUserView }
  | { kind: "unsuspend"; user: AdminUserView };

function Users({ me }: { me: Me }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const [pending, setPending] = useState<Pending | null>(null);
  const [group, setGroup] = useState("contributors");
  const [reason, setReason] = useState("");
  const q = useQuery({
    queryKey: queryKeys.adminUsers,
    queryFn: () => api.get("/api/v1/admin/users", { query: { limit: 500 } }),
  });
  const act = useMutation({
    mutationFn: async (p: Pending) => {
      const id = p.user.id;
      switch (p.kind) {
        case "add":
          return api.put<"/api/v1/admin/users/{id}/groups/{group}">(
            apiPath("/api/v1/admin/users/{id}/groups/{group}", { id, group: group.trim() }),
          );
        case "remove":
          return api.delete<"/api/v1/admin/users/{id}/groups/{group}">(
            apiPath("/api/v1/admin/users/{id}/groups/{group}", { id, group: p.group }),
          );
        case "suspend":
          return api.post<"/api/v1/admin/users/{id}/suspend">(
            apiPath("/api/v1/admin/users/{id}/suspend", { id }),
            {
              body: { reason: reason.trim() },
            },
          );
        case "unsuspend":
          return api.post<"/api/v1/admin/users/{id}/unsuspend">(
            apiPath("/api/v1/admin/users/{id}/unsuspend", { id }),
          );
      }
    },
    onSuccess: (_, p) => {
      setPending(null);
      toast.show({ title: t(`users.done.${p.kind}`, { name: p.user.display_name }) });
      void qc.invalidateQueries({ queryKey: queryKeys.adminUsers });
      void qc.invalidateQueries({ queryKey: queryKeys.adminAudit });
    },
  });
  const start = (p: Pending) => {
    act.reset();
    setReason("");
    setPending(p);
  };
  const users = q.data?.users ?? [];
  const errs = fieldErrorsOf(act.error);

  if (q.isPending) return <Spinner showLabel />;
  if (q.isError) return <ErrorCallout error={q.error} />;
  if (users.length === 0) return <EmptyState message={t("users.empty")} />;
  return (
    <>
      <ul className="rows">
        {users.map((u) => (
          <li key={u.id} className="case">
            <div className="case__head">
              <strong>{u.display_name}</strong>
              {u.suspended_at && <span className="badge badge--warn">{t("users.suspended")}</span>}
              <span className="secondary">
                {t("users.since", { date: formatDate(u.created_at) })}
              </span>
            </div>
            <p className="secondary">
              {(u.identities ?? [])
                .map((i) => `${i.provider}: ${i.login}`)
                .join(t("common.listSep"))}
            </p>
            {u.suspended_at && (
              <p className="secondary user-text">
                {t("users.suspendedAt", {
                  at: formatDateTime(u.suspended_at),
                  reason: u.suspended_reason,
                })}
              </p>
            )}
            <ul className="chips" aria-label={t("users.groups")}>
              {(u.groups ?? []).map((g) => (
                <li key={g} className="chip">
                  {g}
                  {!FIXED.has(g) && (
                    <button
                      type="button"
                      className="chip__remove"
                      aria-label={t("users.removeGroup", { group: g, name: u.display_name })}
                      onClick={() => start({ kind: "remove", user: u, group: g })}
                    >
                      ×
                    </button>
                  )}
                </li>
              ))}
            </ul>
            <div className="stack-inline">
              <Button onClick={() => start({ kind: "add", user: u })}>{t("users.addGroup")}</Button>
              {u.id !== me.id &&
                (u.suspended_at ? (
                  <Button onClick={() => start({ kind: "unsuspend", user: u })}>
                    {t("users.unsuspend")}
                  </Button>
                ) : (
                  <Button variant="danger" onClick={() => start({ kind: "suspend", user: u })}>
                    {t("users.suspend")}
                  </Button>
                ))}
            </div>
          </li>
        ))}
      </ul>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(o) => !o && setPending(null)}
        title={
          pending
            ? t(`users.title.${pending.kind}`, {
                name: pending.user.display_name,
                group: pending.kind === "remove" ? pending.group : "",
              })
            : ""
        }
        description={pending ? t(`users.hint.${pending.kind}`) : undefined}
        confirmLabel={pending ? t(`users.confirm.${pending.kind}`) : ""}
        danger={pending?.kind === "suspend" || pending?.kind === "remove"}
        pending={act.isPending}
        error={errs.reason ? undefined : act.error}
        onConfirm={() => pending && act.mutate(pending)}
      >
        {pending?.kind === "add" && (
          <>
            <TextField
              label={t("users.groupName")}
              hint={t("users.groupHint")}
              value={group}
              list="hub-groups"
              maxLength={64}
              onChange={(e) => setGroup(e.target.value)}
            />
            <datalist id="hub-groups">
              <option value="contributors" />
              <option value="moderators" />
            </datalist>
          </>
        )}
        {pending?.kind === "suspend" && (
          <NoteField
            id="suspend-reason"
            value={reason}
            onChange={setReason}
            error={errs.reason}
            max={500}
          />
        )}
      </ConfirmDialog>
    </>
  );
}
