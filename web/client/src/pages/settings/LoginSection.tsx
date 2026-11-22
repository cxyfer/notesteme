// 登入（pages.md §12、§13、AUTH-R2）：修改密碼。工作階段列表、反向代理驗證與 OIDC 是 M4。
//
// POST /api/auth/password：目前的密碼錯誤回 422 invalid_password，和登入共用鎖定（429 login_locked）。
// 密碼欄位不回填；送出後（不論成功或失敗）清空，不用 useMutation（variables 會留在 MutationCache）。
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../../api/client";
import { isApiError } from "../../api/errors";
import { Button, ErrorCallout, TextField, useToast } from "../../components";
import { NotAvailable, Panel } from "./shared";

export function LoginSection() {
  const { t } = useTranslation();
  return (
    <>
      <ChangePassword />
      <Panel title={t("settings.login.sessionsTitle")}>
        <NotAvailable>{t("settings.common.plannedFor", { milestone: "M4" })}</NotAvailable>
      </Panel>
    </>
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
