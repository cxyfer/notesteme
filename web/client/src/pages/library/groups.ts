// 媒體庫的分組（LIB-R4）：預設依頻道，可以改成依發布日或狀態，或不分組。
// API 依分組排序（同一組連續、跨頁時下一頁接著同一組），這裡只決定每一列屬於哪一組與組名。
import type { TFunction } from "i18next";

import { formatDate } from "../../i18n";
import type { ApiItem } from "./stageView";

export const GROUPINGS = ["channel", "published", "status", "none"] as const;
export type Grouping = (typeof GROUPINGS)[number];
/** 預設的分組（LIB-R4：預設依頻道分組） */
export const DEFAULT_GROUPING: Grouping = "channel";

/** 網址的 group（省略時是預設的 channel）→ GET /api/items 的 group_by；不分組時省略 */
export function groupByParam(group: Grouping | undefined): string | undefined {
  const g = group ?? DEFAULT_GROUPING;
  return g === "none" ? undefined : g;
}

/** 這一列所屬的組：頻道 ID、當地時區的發布日、API 給的狀態組；不分組時是空字串 */
export function groupKey(item: ApiItem, group: Grouping | undefined): string {
  switch (group ?? DEFAULT_GROUPING) {
    case "channel":
      return item.channel_id ?? "";
    case "published":
      return item.published_at ? formatDate(item.published_at) : "";
    case "status":
      return item.group ?? "";
  }
  return "";
}

/** 組名：頻道名稱（沒有名稱時用頻道 ID）、發布日、狀態 */
export function groupLabel(t: TFunction, item: ApiItem, group: Grouping | undefined): string {
  const key = groupKey(item, group);
  switch (group ?? DEFAULT_GROUPING) {
    case "channel":
      return key ? item.channel_name || key : t("library.group.noChannel");
    case "published":
      return key || t("library.group.noDate");
    default:
      return t(`library.group.status.${key}`, { defaultValue: key });
  }
}

/** 某一組在已載入的列中的數量；complete 為 false 代表這組可能延續到還沒載入的頁（API 沒有每組的總數） */
export interface GroupCount {
  count: number;
  complete: boolean;
}

/** 每組第一列的 index → 這組已載入的列數。API 依分組排序，同一組連續 */
export function groupCounts(
  rows: readonly ApiItem[],
  group: Grouping | undefined,
  hasMore: boolean,
): Map<number, GroupCount> {
  const out = new Map<number, GroupCount>();
  let start = 0;
  for (let i = 1; i <= rows.length; i++) {
    if (i === rows.length || groupKey(rows[i]!, group) !== groupKey(rows[start]!, group)) {
      // 最後一組在還有下一頁時可能沒數完
      out.set(start, { count: i - start, complete: i < rows.length || !hasMore });
      start = i;
    }
  }
  return out;
}
