// 登入（docs/ui/pages.md §13）：單一密碼；密碼錯誤顯示「密碼不正確。」，
// 連續失敗被鎖定（429 login_locked）時顯示剩餘秒數並停用按鈕；登入後導回原本的路徑。
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../api/client";
import { isApiError } from "../api/errors";
import { safeRedirect } from "../auth/session";
import { Button, ErrorCallout, TextField } from "../components";
import { usePageTitle } from "../shell/usePageTitle";

const route = getRouteApi("/login");

export function LoginPage() {
  const { t } = useTranslation();
  usePageTitle(t("login.title"));
  const { redirect } = route.useSearch();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
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
    if (pending || remaining > 0 || password === "") return;
    setPending(true);
    setError(undefined);
    try {
      await api.post("/api/auth/login", { body: { password } });
      qc.clear();
      await navigate({ to: safeRedirect(redirect), replace: true });
    } catch (err) {
      setError(err);
      if (isApiError(err) && err.code === "login_locked") {
        const secs = err.body.retry_after_s ?? 60;
        const n = Date.now();
        setNow(n);
        setLockedUntil(n + secs * 1000);
      }
    } finally {
      setPending(false);
    }
  }

  let message: string | undefined;
  if (isApiError(error) && error.code === "invalid_password") message = t("login.invalidPassword");
  if (isApiError(error) && error.code === "login_locked")
    message = t("login.locked", { seconds: remaining });

  return (
    <main className="login" id="main">
      <form className="login__card" onSubmit={onSubmit} noValidate>
        <h1 className="login__mark">{t("app.name")}</h1>
        <TextField
          label={t("login.password")}
          type="password"
          name="password"
          autoComplete="current-password"
          autoFocus
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {error !== undefined && (
          <ErrorCallout
            error={error}
            category={isApiError(error) && error.code === "login_locked" ? "wait" : undefined}
            message={message}
          />
        )}
        <Button type="submit" variant="primary" disabled={pending || remaining > 0}>
          {pending ? t("login.submitting") : t("login.submit")}
        </Button>
      </form>
    </main>
  );
}
