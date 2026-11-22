// App 外框（design-language.md §5.1）：skip link、左側導覽（767px 以下改成底部分頁列）、主內容。
// 也在這裡連上 SSE（useEventStream）並處理 401（導到登入頁）。
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import { api, setUnauthorizedHandler } from "../api/client";
import { Button } from "../components";
import { useEventStream } from "../events/useEventStream";
import { Preferences } from "./Preferences";

// M1 的導覽項目（docs/ui/README.md §2）。M2 之後的頁面（Prompt 庫、處理方案…）由各切片加進來。
const NAV_GROUPS = [
  {
    label: "nav.work",
    items: [
      { to: "/library", label: "nav.library" },
      { to: "/queue", label: "nav.queue" },
    ],
  },
  { label: "nav.system", items: [{ to: "/settings", label: "nav.settings" }] },
] as const;

export function AppShell() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const pathname = useRouterState({ select: (s) => s.location.pathname });

  const toLogin = useCallback(() => {
    qc.clear();
    void navigate({
      to: "/login",
      search: { redirect: window.location.pathname + window.location.search },
    });
  }, [navigate, qc]);

  useEffect(() => {
    setUnauthorizedHandler(toLogin);
    return () => setUnauthorizedHandler(undefined);
  }, [toLogin]);
  useEventStream({ onUnauthorized: toLogin });

  // 換頁後把焦點移到主內容（第一次渲染除外），螢幕閱讀器會從新頁面的標題開始讀。
  const mainRef = useRef<HTMLElement>(null);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    mainRef.current?.focus();
  }, [pathname]);

  async function logout() {
    try {
      await api.post("/api/auth/logout");
    } finally {
      qc.clear();
      await navigate({ to: "/login" });
    }
  }

  return (
    <>
      <a className="skip-link" href="#main">
        {t("nav.skip")}
      </a>
      <div className="app">
        <nav className="nav" aria-label={t("nav.label")}>
          <Link to="/library" className="nav__mark">
            {t("app.name")}
          </Link>
          {NAV_GROUPS.map((g) => (
            <div className="nav__section" key={g.label}>
              <h2 className="nav__group">{t(g.label)}</h2>
              <ul className="nav__list">
                {g.items.map((it) => (
                  <li key={it.to}>
                    <Link
                      to={it.to}
                      className="nav__item"
                      aria-current={
                        pathname === it.to || pathname.startsWith(`${it.to}/`) ? "page" : undefined
                      }
                    >
                      {t(it.label)}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          <div className="nav__footer">
            <Preferences />
            <Button variant="text" onClick={() => void logout()}>
              {t("prefs.logout")}
            </Button>
          </div>
        </nav>
        {/* tabIndex=-1：skip link 與換頁時可以把焦點移到這裡 */}
        <main className="main" id="main" tabIndex={-1} ref={mainRef}>
          <Outlet />
        </main>
      </div>
    </>
  );
}
