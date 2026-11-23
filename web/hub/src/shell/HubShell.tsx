// Hub 的外框：頂端列（站名、導覽、帳號、語言與主題）、skip link、主要內容。
// 導覽只列出目前帳號有權限的頁面；未登入時顯示「登入」。登入回來後回到登入前的頁面（sessionStorage）。
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useEffect, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";

import { api } from "../api/client";
import { hasPermission } from "../api/types";
import { takeReturnTo } from "../auth/session";
import { Button } from "../components";
import { currentLanguage, LANGUAGE_NAMES, LANGUAGES, setLanguage, type Language } from "../i18n";
import { currentTheme, saveTheme, subscribeTheme, THEMES, type Theme } from "../theme/theme";
import { useMe } from "./useMe";

export function HubShell() {
  const { t } = useTranslation();
  const me = useMe();
  const user = me.data ?? null;
  const navigate = useNavigate();
  const qc = useQueryClient();
  const theme = useSyncExternalStore(subscribeTheme, currentTheme);

  useEffect(() => {
    if (!user) return;
    const back = takeReturnTo();
    if (back && back !== "/") void navigate({ to: back });
  }, [user, navigate]);

  const logout = useMutation({
    mutationFn: () => api.post("/api/v1/auth/logout"),
    onSettled: () => {
      qc.clear();
      void navigate({ to: "/login" });
    },
  });

  const moderate = hasPermission(user, "moderate");
  const admin = hasPermission(user, "admin");
  const active = { "aria-current": "page" } as const;

  return (
    <>
      <a className="skip-link" href="#main">
        {t("shell.skip")}
      </a>
      <header className="hub-header">
        <Link to="/" className="hub-header__mark cond">
          {t("app.short")} <span>{t("app.hub")}</span>
        </Link>
        <nav aria-label={t("shell.nav")}>
          <ul className="hub-nav">
            <li>
              <Link to="/" activeProps={active} activeOptions={{ exact: true }}>
                {t("nav.browse")}
              </Link>
            </li>
            {user && (
              <li>
                <Link to="/account" activeProps={active}>
                  {t("nav.account")}
                </Link>
              </li>
            )}
            {moderate && (
              <li>
                <Link to="/admin/reports" activeProps={active}>
                  {t("nav.moderation")}
                </Link>
              </li>
            )}
            {admin && (
              <li>
                <Link to="/admin/users" activeProps={active}>
                  {t("nav.users")}
                </Link>
              </li>
            )}
            <li>
              <Link to="/takedown" activeProps={active}>
                {t("nav.takedown")}
              </Link>
            </li>
          </ul>
        </nav>
        <div className="hub-header__user">
          {user ? (
            <>
              <span>{t("shell.signedInAs", { name: user.display_name })}</span>
              <Button variant="text" onClick={() => logout.mutate()} disabled={logout.isPending}>
                {t("shell.signOut")}
              </Button>
            </>
          ) : me.isSuccess ? (
            <Link to="/login" className="button button--outline">
              {t("shell.signIn")}
            </Link>
          ) : null}
          <label>
            <span className="visually-hidden">{t("shell.language")}</span>
            <select
              value={currentLanguage()}
              onChange={(e) => void setLanguage(e.target.value as Language)}
            >
              {LANGUAGES.map((l) => (
                <option key={l} value={l} lang={l}>
                  {LANGUAGE_NAMES[l]}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="visually-hidden">{t("shell.theme")}</span>
            <select value={theme} onChange={(e) => saveTheme(e.target.value as Theme)}>
              {THEMES.map((th) => (
                <option key={th} value={th}>
                  {t(`shell.themes.${th}`)}
                </option>
              ))}
            </select>
          </label>
        </div>
      </header>
      <main id="main" className="main" tabIndex={-1}>
        <Outlet />
      </main>
      <footer className="hub-footer">
        <p>{t("shell.footer")}</p>
      </footer>
    </>
  );
}
