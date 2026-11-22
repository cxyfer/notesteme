// 主題：跟隨系統（預設）、淺色、深色。選擇存在 localStorage，套用為 <html data-theme>。
// token 用 light-dark() 搭配 color-scheme（src/styles/tokens.css）；CSP 不允許 inline script，
// 所以不在 index.html 防閃爍：手動選擇與系統不同時，JS 載入前會短暫顯示系統的主題。
export const THEMES = ["system", "light", "dark"] as const;
export type Theme = (typeof THEMES)[number];

const STORAGE_KEY = "notesteme.theme";

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

export function saveTheme(theme: Theme) {
  try {
    if (theme === "system") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // 隱私模式：只在這次工作階段生效
  }
  applyTheme(theme);
}
