// 響應式的斷點（docs/ui/README.md §8）：CSS 的媒體查詢決定樣式，這裡讓元件決定要渲染哪一組控制項，
// 避免同一組按鈕在 DOM 裡出現兩份（只用 CSS 隱藏時，jsdom 與某些輔助科技仍看得到）。
import { useCallback, useSyncExternalStore } from "react";

/** 767px 以下：底部分頁列 */
export const NARROW = "(max-width: 767px)";
/** 768–1279px：側邊導覽收合成 64px 的窄側欄（base.css 的同一組數字） */
export const RAIL = "(min-width: 768px) and (max-width: 1279px)";

/** 不支援 matchMedia 時（jsdom）一律是 false：當成 1280px 以上的寬螢幕 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (cb: () => void) => {
      if (typeof window.matchMedia !== "function") return () => {};
      const m = window.matchMedia(query);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    [query],
  );
  return useSyncExternalStore(
    subscribe,
    () => typeof window.matchMedia === "function" && window.matchMedia(query).matches,
    () => false,
  );
}
