// App 外框（design-language.md §5.1）：skip link、左側導覽（767px 以下改成底部分頁列）、主內容。
// 也在這裡連上 SSE（useEventStream）、處理 401（導到登入頁），並掛上「新增」抽屜：
// 導覽的「新增」按鈕、N 快捷鍵、焦點不在輸入框時貼上網址（docs/ui/README.md §3.2、§3.6）。
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { AddProvider, useAddDrawer } from "../add/AddContext";
import { isHttpUrl } from "../add/url";
import { api, setUnauthorizedHandler } from "../api/client";
import { authMethodsQuery } from "../auth/insecure";
import { Button, Dialog, Icon, type IconComponent } from "../components";
import { useEventStream } from "../events/useEventStream";
import { isEditableTarget, isMac, modalOpen } from "./keyboard";
import {
  ChatText,
  CurrencyCircleDollar,
  DotsThree,
  FilmStrip,
  GearSix,
  House,
  Notebook,
  Plus,
  Queue,
  Rss,
  SlidersHorizontal,
  Subtitles,
} from "./navIcons";
import { Preferences } from "./Preferences";
import { RAIL, useMediaQuery } from "./media";
import { TabBar, useNarrow } from "./TabBar";

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
// 每一項都有圖示（design-language.md §5.4）：768–1279px 的窄側欄（64px）只顯示圖示，名稱在 hover／focus 時出現。
const NAV_GROUPS: readonly {
  label: string;
  items: readonly { to: string; label: string; icon: IconComponent }[];
}[] = [
  {
    label: "nav.work",
    items: [
      { to: "/", label: "nav.overview", icon: House },
      { to: "/library", label: "nav.library", icon: FilmStrip },
      { to: "/queue", label: "nav.queue", icon: Queue },
      { to: "/subscriptions", label: "nav.subscriptions", icon: Rss },
    ],
  },
  // M2（UI-3a）：調整（design-language.md §5.1 的三組：工作、調整、系統）
  {
    label: "nav.tune",
    items: [
      { to: "/prompts", label: "nav.prompts", icon: ChatText },
      { to: "/profiles", label: "nav.profiles", icon: SlidersHorizontal },
      { to: "/styles", label: "nav.styles", icon: Subtitles },
      { to: "/note-templates", label: "nav.noteTemplates", icon: Notebook },
    ],
  },
  {
    label: "nav.system",
    items: [
      { to: "/costs", label: "nav.costs", icon: CurrencyCircleDollar },
      { to: "/settings", label: "nav.settings", icon: GearSix },
    ],
  },
];

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
 * UI-8：Ctrl K（macOS 為 ⌘ K）打開指令面板（焦點在輸入框也可以；已有對話框時不作用）。
 */
function GlobalShortcuts() {
  const add = useAddDrawer();
  const navigate = useNavigate();
  const [help, setHelp] = useState(false);
  const [palette, setPalette] = useState(false);
  useEffect(() => {
    let goAt = 0;
    function onKey(e: KeyboardEvent) {
      // README §3.1：Ctrl K（macOS 為 ⌘ K；Mac 的 Ctrl K 是文字輸入框的「刪到行尾」，不攔）
      const mod = isMac() ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
      if (mod && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        if (e.defaultPrevented || modalOpen()) return;
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
  // UI-8：767px 以下改用底部分頁列（README §8），側邊導覽不渲染
  const narrow = useNarrow();
  const rail = useMediaQuery(RAIL);

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

  const footer = (
    <div className="nav__footer">
      <Preferences />
      <Button variant="text" onClick={() => void logout()}>
        {t("prefs.logout")}
      </Button>
    </div>
  );

  return (
    <>
      <a className="skip-link" href="#main">
        {t("nav.skip")}
      </a>
      <InsecureBanner />
      <div className="app">
        {!narrow && (
          <nav className="nav" aria-label={t("nav.label")}>
            <Link to="/" className="nav__mark">
              <span className="nav__mark-full">{t("app.name")}</span>
              {/* 窄側欄只顯示第一個字母；完整名稱留給螢幕閱讀器 */}
              <span className="nav__mark-short" aria-hidden="true">
                {t("app.name").slice(0, 1)}
              </span>
            </Link>
            <Button className="nav__add" variant="primary" onClick={() => add.open()}>
              <Icon icon={Plus} size={16} className="nav__icon" />
              <span className="nav__label">{t("add.open")}</span>
              <kbd className="nav__kbd" aria-hidden="true">
                {t("add.hotkey")}
              </kbd>
            </Button>
            <NavGroups pathname={pathname} />
            {rail ? <NavMore>{footer}</NavMore> : footer}
          </nav>
        )}
        {/* tabIndex=-1：skip link 與換頁時可以把焦點移到這裡 */}
        <main className="main" id="main" tabIndex={-1} ref={mainRef}>
          <Outlet />
        </main>
      </div>
      {narrow && (
        <TabBar
          pathname={pathname}
          onAdd={() => add.open()}
          more={(close) => (
            <>
              <NavGroups pathname={pathname} skip={TAB_PAGES} onNavigate={close} />
              {footer}
            </>
          )}
        />
      )}
    </>
  );
}

/** 底部分頁列已經有的頁面，「更多」裡不重複列出 */
const TAB_PAGES: readonly string[] = ["/", "/library", "/queue"];

function NavGroups({
  pathname,
  skip = [],
  onNavigate,
}: {
  pathname: string;
  skip?: readonly string[];
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  return NAV_GROUPS.map((g) => {
    const items = g.items.filter((it) => !skip.includes(it.to));
    if (!items.length) return null;
    return (
      <div className="nav__section" key={g.label}>
        <h2 className="nav__group">{t(g.label)}</h2>
        <ul className="nav__list">
          {items.map((it) => (
            <li key={it.to}>
              <Link
                to={it.to}
                className="nav__item"
                onClick={onNavigate}
                aria-current={
                  pathname === it.to || (it.to !== "/" && pathname.startsWith(`${it.to}/`))
                    ? "page"
                    : undefined
                }
              >
                <Icon icon={it.icon} size={16} className="nav__icon" />
                <span className="nav__label">{t(it.label)}</span>
              </Link>
            </li>
          ))}
        </ul>
      </div>
    );
  });
}

/**
 * 768–1279px 的窄側欄放不下偏好設定（下拉選單）與登出：收成一個圖示按鈕，打開對話框。
 * 名稱不用「更多」：頁面上的「更多」選單（MoreMenu）同時可見，螢幕閱讀器會分不出來。
 */
function NavMore({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <div className="nav__more">
      <button
        type="button"
        className="nav__item"
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
      >
        <Icon icon={DotsThree} size={16} className="nav__icon" />
        <span className="nav__label">{t("nav.account")}</span>
      </button>
      <Dialog open={open} onOpenChange={setOpen} title={t("nav.account")}>
        <div className="tabbar__more">{children}</div>
      </Dialog>
    </div>
  );
}
