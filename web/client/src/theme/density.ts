// 介面密度（design-language.md §5.5）：舒適（預設）或緊湊。和主題同一個機制：選擇存在 localStorage，
// 套用為 <html data-density>；舒適時不設屬性。緊湊只縮小列表的列與表格儲存格的上下留白（src/styles/density.css），
// 不改字級與 --space-* token，按鈕仍是自己的最小高度，所以可點擊的目標不會小於 24px。
export const DENSITIES = ["comfortable", "compact"] as const;
export type Density = (typeof DENSITIES)[number];

const STORAGE_KEY = "notesteme.density";

export function readDensity(): Density {
  try {
    return localStorage.getItem(STORAGE_KEY) === "compact" ? "compact" : "comfortable";
  } catch {
    return "comfortable";
  }
}

export function applyDensity(density: Density, root: HTMLElement = document.documentElement) {
  if (density === "comfortable") delete root.dataset.density;
  else root.dataset.density = density;
}

// 只在 saveDensity 時更新；localStorage 讀不到（隱私模式）時仍保留這次工作階段的選擇。
let current: Density | undefined;
const listeners = new Set<() => void>();

/** 給 useSyncExternalStore 用：密度改變時通知。 */
export function subscribeDensity(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function saveDensity(density: Density) {
  try {
    if (density === "comfortable") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, density);
  } catch {
    // 隱私模式：只在這次工作階段生效
  }
  applyDensity(density);
  current = density;
  for (const fn of listeners) fn();
}

/** 目前的密度（useSyncExternalStore 的 getSnapshot）。 */
export function currentDensity(): Density {
  return current ?? readDensity();
}
