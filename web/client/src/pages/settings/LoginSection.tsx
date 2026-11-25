// 登入（pages.md §12、§13、AUTH-R2、AUTH-R20、R22）：修改密碼、工作階段列表（可以撤銷目前以外的任一個）。
// 反向代理驗證與 OIDC 只能寫在設定檔（AUTH-R12、R16），這裡不提供設定。
//
// POST /api/auth/password：目前的密碼錯誤回 422 invalid_password，和登入共用鎖定（429 login_locked）。
// 密碼欄位不回填；送出後（不論成功或失敗）清空，不用 useMutation（variables 會留在 MutationCache）。
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api, apiPath } from "../../api/client";
import { isApiError } from "../../api/errors";
import { queryKeys } from "../../api/queryKeys";
import { Button, Dialog, ErrorCallout, TextField, useToast } from "../../components";
import { formatDateTime } from "../../i18n";
import { Loading, Panel, TableScroll } from "./shared";

export function LoginSection() {
  return (
    <>
      <ChangePassword />
      <Sessions />
    </>
  );
}

interface SessionInfo {
  id: string;
  method: string;
  created_at: string;
  last_seen_at: string;
  ip: string;
  user_agent: string;
  current: boolean;
}

/** AUTH-R20、R22：工作階段列表；目前的 session 標示出來，其他的可以撤銷（先確認）。 */
function Sessions() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toast = useToast();
  const query = useQuery({
    queryKey: queryKeys.sessions,
    queryFn: () => api.get("/api/auth/sessions"),
  });
  const [revoking, setRevoking] = useState<SessionInfo>();
  const [error, setError] = useState<unknown>();

  async function revoke(s: SessionInfo) {
    setRevoking(undefined);
    setError(undefined);
    try {
      await api.delete<"/api/auth/sessions/{id}">(apiPath("/api/auth/sessions/{id}", { id: s.id }));
      await qc.invalidateQueries({ queryKey: queryKeys.sessions });
      toast.show({ title: t("settings.login.sessionRevoked") });
    } catch (err) {
      setError(err);
    }
  }

  const sessions = (query.data?.sessions ?? []) as SessionInfo[];
  return (
    <Panel title={t("settings.login.sessionsTitle")} description={t("settings.login.sessionsHint")}>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorCallout
          error={query.error}
          actions={[{ label: t("common.retry"), onClick: () => void query.refetch() }]}
        />
      ) : (
        <TableScroll label={t("settings.login.sessionsTitle")}>
          <table className="settings-table">
            <thead>
              <tr>
                <th scope="col">{t("settings.login.col.method")}</th>
                <th scope="col">{t("settings.login.col.device")}</th>
                <th scope="col">{t("settings.login.col.created")}</th>
                <th scope="col">{t("settings.login.col.lastSeen")}</th>
                <th scope="col">
                  <span className="visually-hidden">{t("settings.login.col.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id}>
                  <td>
                    {t(`settings.login.method.${s.method}`, { defaultValue: s.method })}
                    {s.current && (
                      <span className="settings-row__meta">
                        {" "}
                        {t("settings.login.currentSession")}
                      </span>
                    )}
                  </td>
                  <td>
                    <span className="num">{s.ip}</span>
                    {s.user_agent && (
                      <span className="settings-row__meta" title={s.user_agent}>
                        {" "}
                        {s.user_agent.length > 60 ? `${s.user_agent.slice(0, 60)}…` : s.user_agent}
                      </span>
                    )}
                  </td>
                  <td className="num">{formatDateTime(s.created_at)}</td>
                  <td className="num">{formatDateTime(s.last_seen_at)}</td>
                  <td>
                    {!s.current && (
                      <Button variant="text" onClick={() => setRevoking(s)}>
                        {t("settings.login.revoke")}
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {error !== undefined && <ErrorCallout error={error} />}
      <Dialog
        open={revoking !== undefined}
        onOpenChange={(o) => !o && setRevoking(undefined)}
        title={t("settings.login.revokeTitle")}
        description={t("settings.login.revokeBody")}
        closable={false}
        footer={
          <>
            <Button variant="text" onClick={() => setRevoking(undefined)}>
              {t("settings.common.cancel")}
            </Button>
            <Button variant="danger" onClick={() => revoking && void revoke(revoking)}>
              {t("settings.login.revoke")}
            </Button>
          </>
        }
      />
    </Panel>
  );
}

function ChangePassword() {
  const { t } = useTranslation();
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [mismatch, setMismatch] = useState(false);
  const [lockedUntil, setLockedUntil] = useState<number>();
  const [now, setNow] = useState(() => Date.now());

  const remaining = lockedUntil ? Math.max(0, Math.ceil((lockedUntil - now) / 1000)) : 0;
  useEffect(() => {
    if (!lockedUntil) return;
    const id = setInterval(() => {
      const n = Date.now();
      setNow(n);
      if (n >= lockedUntil) {
        setLockedUntil(undefined);
        setError(undefined);
      }
    }, 1000);
    return () => clearInterval(id);
  }, [lockedUntil]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending || remaining > 0 || current === "" || next === "") return;
    if (next !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    const body = { current_password: current, new_password: next };
    setCurrent("");
    setNext("");
    setConfirm("");
    setPending(true);
    setError(undefined);
    try {
      await api.post("/api/auth/password", { body });
      toast.show({ title: t("settings.login.changed") });
    } catch (err) {
      setError(err);
      if (isApiError(err) && err.code === "login_locked") {
        const n = Date.now();
        setNow(n);
        setLockedUntil(n + (err.body.retry_after_s ?? 60) * 1000);
      }
    } finally {
      setPending(false);
    }
  }

  let message: string | undefined;
  if (isApiError(error) && error.code === "invalid_password")
    message = t("settings.login.wrongCurrent");
  if (isApiError(error) && error.code === "login_locked")
    message = t("login.locked", { seconds: remaining });

  return (
    <Panel title={t("settings.login.passwordTitle")} description={t("settings.login.passwordHint")}>
      <form className="settings-form settings-form--narrow" onSubmit={onSubmit} noValidate>
        <TextField
          label={t("settings.login.current")}
          type="password"
          name="current-password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
        <TextField
          label={t("settings.login.new")}
          type="password"
          name="new-password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
        <TextField
          label={t("settings.login.confirm")}
          type="password"
          name="confirm-password"
          autoComplete="new-password"
          value={confirm}
          error={mismatch ? t("settings.login.mismatch") : undefined}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {error !== undefined && (
          <ErrorCallout
            error={error}
            category={isApiError(error) && error.code === "login_locked" ? "wait" : undefined}
            message={message}
          />
        )}
        <div className="settings-actions">
          <Button
            type="submit"
            variant="primary"
            disabled={pending || remaining > 0 || current === "" || next === ""}
          >
            {t("settings.login.submit")}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
