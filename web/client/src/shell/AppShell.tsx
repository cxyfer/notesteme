// App 外框（design-language.md §5.1）：skip link、左側導覽（767px 以下改成底部分頁列）、主內容。
// 也在這裡連上 SSE（useEventStream）、處理 401（導到登入頁），並掛上「新增」抽屜：
// 導覽的「新增」按鈕、N 快捷鍵、焦點不在輸入框時貼上網址（docs/ui/README.md §3.2、§3.6）。
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { AddProvider, useAddDrawer } from "../add/AddContext";
import { isHttpUrl } from "../add/url";
import { api, setUnauthorizedHandler } from "../api/client";
import { authMethodsQuery } from "../auth/insecure";
import { Button } from "../components";
import { useEventStream } from "../events/useEventStream";
import { isEditableTarget, modalOpen } from "./keyboard";
import { Preferences } from "./Preferences";

const ShortcutsDialog = lazy(() =>
  import("./ShortcutsDialog").then((m) => ({ default: m.ShortcutsDialog })),
);
// UI-8：指令面板（README §3.1）只在打開時載入
const CommandPalette = lazy(() =>
  import("./CommandPalette").then((m) => ({ default: m.CommandPalette })),
);

/** G 之後接的鍵（README §3.6）；G 之後 1.5 秒內沒有按第二個鍵就取消 */
const GO_TO = { l: "/library", q: "/queue", s: "/subscriptions" } as const;
const GO_TIMEOUT = 1500;

// M1 的導覽項目（docs/ui/README.md §2）。M2 之後的頁面（Prompt 庫、處理方案…）由各切片加進來。
const NAV_GROUPS = [
  {
    label: "nav.work",
    items: [
      { to: "/", label: "nav.overview" },
      { to: "/library", label: "nav.library" },
      { to: "/queue", label: "nav.queue" },
      { to: "/subscriptions", label: "nav.subscriptions" },
    ],
  },
  // M2（UI-3a）：調整（design-language.md §5.1 的三組：工作、調整、系統）
  {
    label: "nav.tune",
    items: [
      { to: "/prompts", label: "nav.prompts" },
      { to: "/profiles", label: "nav.profiles" },
      { to: "/styles", label: "nav.styles" },
      { to: "/note-templates", label: "nav.noteTemplates" },
    ],
  },
  {
    label: "nav.system",
    items: [
      { to: "/costs", label: "nav.costs" },
      { to: "/settings", label: "nav.settings" },
    ],
  },
] as const;

export function AppShell() {
  return (
    <AddProvider>
      <GlobalShortcuts />
      <ShellLayout />
    </AddProvider>
  );
}

/**
 * N 打開新增抽屜；焦點不在輸入框時貼上 http(s) 網址，打開抽屜並帶入網址。
 * UI-7：G 然後 L／Q／S 前往媒體庫、佇列、訂閱；? 顯示所有快捷鍵。
 * UI-8：Ctrl K／⌘ K 打開指令面板（焦點在輸入框也可以；已有對話框時不作用）。
 */
function GlobalShortcuts() {
  const add = useAddDrawer();
  const navigate = useNavigate();
  const [help, setHelp] = useState(false);
  const [palette, setPalette] = useState(false);
  useEffect(() => {
    let goAt = 0;
    function onKey(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        if (modalOpen()) return;
        e.preventDefault();
        setPalette(true);
        return;
      }
      if (e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
      if (isEditableTarget(e.target) || modalOpen()) return;
      // ? 在多數鍵盤配置要按 Shift
      if (e.key === "?") {
        e.preventDefault();
        setHelp(true);
        return;
      }
      if (e.shiftKey) return;
      const key = e.key.toLowerCase();
      if (goAt && Date.now() - goAt < GO_TIMEOUT && key in GO_TO) {
        goAt = 0;
        e.preventDefault();
        void navigate({ to: GO_TO[key as keyof typeof GO_TO] });
        return;
      }
      goAt = key === "g" ? Date.now() : 0;
      if (key !== "n") return;
      e.preventDefault();
      add.open();
    }
    function onPaste(e: ClipboardEvent) {
      if (isEditableTarget(e.target) || modalOpen()) return;
      const text = e.clipboardData?.getData("text/plain").trim() ?? "";
      if (!isHttpUrl(text)) return;
      e.preventDefault();
      add.open(text);
    }
    document.addEventListener("keydown", onKey);
    document.addEventListener("paste", onPaste);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("paste", onPaste);
    };
  }, [add, navigate]);
  return (
    <Suspense fallback={null}>
      {help && <ShortcutsDialog onClose={() => setHelp(false)} />}
      {palette && <CommandPalette onClose={() => setPalette(false)} />}
    </Suspense>
  );
}

// AUTH-R3：以 --insecure-no-auth 啟動時，頂部持續顯示警告（/api/auth/methods 免驗證；查詢失敗時不顯示）。
function InsecureBanner() {
  const { t } = useTranslation();
  const methods = useQuery(authMethodsQuery);
  if (!methods.data?.insecure_no_auth) return null;
  return (
    <p className="insecure-banner" role="alert">
      {t("nav.insecureNoAuth")}
    </p>
  );
}

function ShellLayout() {
  const { t } = useTranslation();
  const add = useAddDrawer();
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
      <InsecureBanner />
      <div className="app">
        <nav className="nav" aria-label={t("nav.label")}>
          <Link to="/" className="nav__mark">
            {t("app.name")}
          </Link>
          <Button className="nav__add" variant="primary" onClick={() => add.open()}>
            {t("add.open")}
            <kbd className="nav__kbd" aria-hidden="true">
              {t("add.hotkey")}
            </kbd>
          </Button>
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
                        pathname === it.to || (it.to !== "/" && pathname.startsWith(`${it.to}/`))
                          ? "page"
                          : undefined
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
