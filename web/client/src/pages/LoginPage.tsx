// 登入（docs/ui/pages.md §13）：單一密碼；密碼錯誤顯示「密碼不正確。」，
// 連續失敗被鎖定（429 login_locked）時顯示剩餘秒數並停用按鈕；登入後導回原本的路徑。
// 登入頁網址的 next（伺服器的頁面路由帶來，ENTRY-R5）與 redirect（UI 自己帶的）都交給後端檢查，
// 只前往登入回應中的 next（ENTRY-R6），不直接使用網址中的值。
// OIDC 啟用時多一個「用 <issuer 的主機名稱> 登入」（AUTH 的 UI 說明）：連到 /api/auth/oidc/start?next=，
// next 同樣由後端以 SafeNext 檢查（AUTH-R17）。登入方式取自免驗證的 GET /api/auth/methods，失敗時不顯示按鈕。
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
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
  const { redirect, next } = route.useSearch();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [lockedUntil, setLockedUntil] = useState<number>();
  const [now, setNow] = useState(() => Date.now());
  const methods = useQuery({
    queryKey: ["auth-methods"],
    queryFn: () => api.get("/api/auth/methods"),
    retry: false,
    staleTime: Infinity,
  });
  const oidc = methods.data?.oidc;
  const requested = next ?? redirect;
  const oidcHref =
    "/api/auth/oidc/start" + (requested ? `?next=${encodeURIComponent(requested)}` : "");

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
      const res = await api.post("/api/auth/login", {
        body: requested ? { password, next: requested } : { password },
      });
      qc.clear();
      // 後端已檢查過；這裡再過一次 safeRedirect（例如 "/" 與 /login 換成媒體庫）。用 href：next 可能帶 query。
      await navigate({ href: safeRedirect(res.next), replace: true });
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
        {oidc?.enabled && oidc.host && (
          <a className="button button--outline" href={oidcHref}>
            {t("login.oidc", { host: oidc.host })}
          </a>
        )}
      </form>
    </main>
  );
}
