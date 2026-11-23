// 主題：跟隨系統（預設）、淺色、深色。選擇存在 localStorage，套用為 <html data-theme>。
// token 用 light-dark() 搭配 color-scheme（src/styles/tokens.css）；CSP 不允許 inline script，
// 所以不在 index.html 防閃爍：手動選擇與系統不同時，JS 載入前會短暫顯示系統的主題。
export const THEMES = ["system", "light", "dark"] as const;
export type Theme = (typeof THEMES)[number];

const STORAGE_KEY = "notesteme-hub.theme";

export function readTheme(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return (THEMES as readonly string[]).includes(v ?? "") ? (v as Theme) : "system";
  } catch {
    return "system";
  }
}

export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement) {
  if (theme === "system") delete root.dataset.theme;
  else root.dataset.theme = theme;
}

// 只在 saveTheme 時更新；localStorage 讀不到（隱私模式）時仍保留這次工作階段的選擇。
let current: Theme | undefined;

// 主題的訂閱者：導覽底部的快速切換與設定頁的「外觀」分區共用同一個值（useTheme）。
const listeners = new Set<() => void>();

/** 給 useSyncExternalStore 用：主題改變時通知。 */
export function subscribeTheme(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function saveTheme(theme: Theme) {
  try {
    if (theme === "system") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // 隱私模式：只在這次工作階段生效
  }
  applyTheme(theme);
  current = theme;
  for (const fn of listeners) fn();
}

/** 目前的主題（useSyncExternalStore 的 getSnapshot）。 */
export function currentTheme(): Theme {
  return current ?? readTheme();
}
