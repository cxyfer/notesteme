// 767px 以下的底部分頁列（docs/ui/README.md §8，UI-8）：總覽、媒體庫、新增、佇列、更多。
// 「更多」打開對話框，放其餘頁面、偏好設定與登出。只在窄螢幕時渲染（桌面的側邊導覽另外隱藏），
// 避免兩組相同的連結同時出現在無障礙樹中。
import { Link } from "@tanstack/react-router";
import { useState, useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Dialog } from "../components";

const NARROW = "(max-width: 767px)";

function subscribe(cb: () => void) {
  if (typeof window.matchMedia !== "function") return () => {};
  const m = window.matchMedia(NARROW);
  m.addEventListener("change", cb);
  return () => m.removeEventListener("change", cb);
}

/** 視窗寬度在 767px 以下（不支援 matchMedia 時當成寬螢幕） */
export function useNarrow(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => typeof window.matchMedia === "function" && window.matchMedia(NARROW).matches,
    () => false,
  );
}

const TABS = [
  { to: "/", label: "nav.overview" },
  { to: "/library", label: "nav.library" },
  { to: "/queue", label: "nav.queue" },
] as const;

export function TabBar({
  pathname,
  onAdd,
  more,
}: {
  pathname: string;
  onAdd: () => void;
  /** 「更多」的內容；參數是關閉對話框的函式 */
  more: (close: () => void) => ReactNode;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const current = (to: string) =>
    pathname === to || (to !== "/" && pathname.startsWith(`${to}/`)) ? "page" : undefined;
  const tab = (to: (typeof TABS)[number]) => (
    <li key={to.to}>
      <Link to={to.to} className="tabbar__item" aria-current={current(to.to)}>
        {t(to.label)}
      </Link>
    </li>
  );
  return (
    <nav className="tabbar" aria-label={t("nav.tabs")}>
      <ul className="tabbar__list">
        {tab(TABS[0])}
        {tab(TABS[1])}
        <li>
          <button type="button" className="tabbar__item tabbar__add" onClick={onAdd}>
            {t("add.open")}
          </button>
        </li>
        {tab(TABS[2])}
        <li>
          <button
            type="button"
            className="tabbar__item"
            aria-haspopup="dialog"
            onClick={() => setOpen(true)}
          >
            {t("nav.more")}
          </button>
        </li>
      </ul>
      <Dialog open={open} onOpenChange={setOpen} title={t("nav.more")}>
        <div className="tabbar__more">{more(() => setOpen(false))}</div>
      </Dialog>
    </nav>
  );
}
