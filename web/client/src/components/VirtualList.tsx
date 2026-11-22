// 虛擬列表（NFR-PERF-10：捲動 10,000 列不掉幀）：只渲染可視範圍加上 overscan 的列。
//
//   - 捲動容器是自己的 div（不是 window），高度由 CSS 決定（.vlist）。
//   - 列高可以不同（例如有錯誤說明的列）：用 measureElement 量實際高度；量不到（jsdom）時用估計值。
//   - 鍵盤：roving focus。焦點在列裡時 ↑／↓／Home／End／PageUp／PageDown 移到其他列，
//     焦點目標是 renderItem 給的 focusProps 所在的元素（通常是標題連結），Enter 由它自己處理。
//   - 無障礙：role="list"／"listitem"，aria-setsize 是總數（不只已載入的），aria-posinset 是位置。
import { measureElement as defaultMeasure, useVirtualizer } from "@tanstack/react-virtual";
import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from "react";

export interface RowFocusProps {
  tabIndex: number;
  "data-row-focus": true;
}

export interface VirtualListHandle {
  scrollToTop: () => void;
  /** 捲到第 i 列（不移動焦點）；用在切換顯示模式後回到同一個位置、播放時跟著目前句 */
  scrollToIndex: (i: number, align?: "start" | "center" | "auto") => void;
  /** 目前可視範圍的第一列（不含 overscan）；沒有列時是 -1 */
  firstVisibleIndex: () => number;
}

export interface VirtualListProps<T> {
  items: readonly T[];
  getKey: (item: T, index: number) => string;
  renderItem: (
    item: T,
    ctx: { index: number; active: boolean; focusProps: RowFocusProps },
  ) => ReactNode;
  /** 列表的可及名稱 */
  label: string;
  /** 符合條件的總數（aria-setsize）；省略時用已載入的數量 */
  total?: number;
  estimateSize?: number;
  overscan?: number;
  /** 捲到離最後一列 endThreshold 列以內時呼叫（呼叫端判斷是否還有下一頁、是否正在載入） */
  onEndReached?: () => void;
  endThreshold?: number;
  /** 列表下方的內容（例如分頁載入器），在捲動容器裡 */
  footer?: ReactNode;
  className?: string;
}

function VirtualListInner<T>(
  {
    items,
    getKey,
    renderItem,
    label,
    total,
    estimateSize = 88,
    overscan = 8,
    onEndReached,
    endThreshold = 10,
    footer,
    className,
  }: VirtualListProps<T>,
  ref: Ref<VirtualListHandle>,
) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(0);
  const pendingFocus = useRef<number | null>(null);

  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer 回傳的函式不能被 memo，這裡不需要
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => estimateSize,
    overscan,
    getItemKey: (i) => getKey(items[i] as T, i),
    measureElement: (el, entry, inst) => {
      const size = defaultMeasure(el, entry, inst);
      return size > 0 ? size : estimateSize;
    },
  });

  useImperativeHandle(ref, () => ({
    scrollToTop: () => {
      virtualizer.scrollToOffset(0);
      setActive(0);
    },
    scrollToIndex: (i, align = "auto") => {
      if (items.length === 0) return;
      virtualizer.scrollToIndex(Math.max(0, Math.min(items.length - 1, i)), { align });
    },
    firstVisibleIndex: () => {
      const offset = virtualizer.scrollOffset ?? 0;
      const first = virtualizer.getVirtualItems().find((v) => v.end > offset);
      return first?.index ?? -1;
    },
  }));

  const virtualItems = virtualizer.getVirtualItems();
  const last = virtualItems.at(-1)?.index ?? -1;
  useEffect(() => {
    if (onEndReached && items.length > 0 && last >= items.length - 1 - endThreshold) onEndReached();
  }, [last, items.length, endThreshold, onEndReached]);

  // 已載入的列變少時（換篩選），active 不能超出範圍
  const clamped = Math.min(active, Math.max(items.length - 1, 0));
  // active 的列被捲出渲染範圍（卸載）時，讓第一個渲染出來的列可以 Tab 進來，列表才不會被整段跳過
  const rendered = virtualItems.some((v) => v.index === clamped);
  const activeIndex = rendered ? clamped : (virtualItems[0]?.index ?? 0);

  // 移動焦點：目標列可能還沒渲染，先捲過去，渲染後再聚焦
  useEffect(() => {
    const i = pendingFocus.current;
    if (i === null) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(
      `[data-index="${i}"] [data-row-focus]`,
    );
    if (el) {
      el.focus();
      pendingFocus.current = null;
    }
  });

  const moveTo = useCallback(
    (i: number) => {
      const next = Math.max(0, Math.min(items.length - 1, i));
      setActive(next);
      pendingFocus.current = next;
      virtualizer.scrollToIndex(next, { align: "auto" });
    },
    [items.length, virtualizer],
  );

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-index]");
    if (!row || e.altKey || e.ctrlKey || e.metaKey) return;
    const i = Number(row.dataset.index);
    const page = Math.max(1, Math.floor((scrollRef.current?.clientHeight ?? 0) / estimateSize));
    const target = {
      ArrowDown: i + 1,
      ArrowUp: i - 1,
      Home: 0,
      End: items.length - 1,
      PageDown: i + page,
      PageUp: i - page,
    }[e.key];
    if (target === undefined) return;
    // 只在焦點是列本身的焦點目標時接管方向鍵（列裡的其他控制元件照常運作）
    if (!(e.target as HTMLElement).hasAttribute("data-row-focus")) return;
    e.preventDefault();
    moveTo(target);
  }

  const setsize = total ?? items.length;
  return (
    <div
      ref={scrollRef}
      className={["vlist", className].filter(Boolean).join(" ")}
      onKeyDown={onKeyDown}
      onFocus={(e) => {
        const row = (e.target as HTMLElement).closest<HTMLElement>("[data-index]");
        if (row) setActive(Number(row.dataset.index));
      }}
    >
      <div
        role="list"
        aria-label={label}
        className="vlist__inner"
        style={{ height: virtualizer.getTotalSize() }}
      >
        {virtualItems.map((v) => {
          const item = items[v.index] as T;
          const isActive = v.index === activeIndex;
          return (
            <div
              key={v.key}
              role="listitem"
              className="vlist__row"
              data-index={v.index}
              ref={virtualizer.measureElement}
              aria-setsize={setsize}
              aria-posinset={v.index + 1}
              style={{ transform: `translateY(${v.start}px)` }}
            >
              {renderItem(item, {
                index: v.index,
                active: isActive,
                focusProps: { tabIndex: isActive ? 0 : -1, "data-row-focus": true },
              })}
            </div>
          );
        })}
      </div>
      {footer}
    </div>
  );
}

export const VirtualList = forwardRef(VirtualListInner) as <T>(
  props: VirtualListProps<T> & { ref?: Ref<VirtualListHandle> },
) => ReturnType<typeof VirtualListInner>;
