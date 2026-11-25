// 可以水平捲動的寬表格：窄螢幕上只能捲動才看得到右邊的欄，所以要能用鍵盤聚焦後以方向鍵捲動
// （WCAG 2.1.1；瀏覽器 e2e 的 axe scrollable-region-focusable 找到，docs/ui/README.md 10.32）。
// 名稱用表格的標題（caption）或所在區塊的標題。
import type { ReactNode } from "react";

export interface TableScrollProps {
  label: string;
  /** 外觀：資料表（成本、訂閱）是 data-table-wrap，設定頁是 settings-table-wrap */
  className?: string;
  children: ReactNode;
}

export function TableScroll({ label, className = "data-table-wrap", children }: TableScrollProps) {
  return (
    <div className={className} tabIndex={0} role="region" aria-label={label}>
      {children}
    </div>
  );
}
