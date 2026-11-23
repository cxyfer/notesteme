// 成本頁的 search params：分組與日期範圍（README §3.3：篩選條件存在網址裡）。不合法的值直接丟掉。
// 這個檔案由 router.tsx 直接匯入，不能 import 頁面元件（否則頁面會被拉進初始 bundle）。

export const GROUP_BY = ["day", "provider", "model", "stage", "channel", "item"] as const;
export type GroupBy = (typeof GROUP_BY)[number];

export interface CostsSearch {
  group_by?: GroupBy;
  /** 預算時區中的日期 YYYY-MM-DD（含）；省略時由伺服器決定（本月 1 日到今天） */
  from?: string;
  to?: string;
}

const DATE = /^\d{4}-\d{2}-\d{2}$/;

export function validateCostsSearch(raw: Record<string, unknown>): CostsSearch {
  const out: CostsSearch = {};
  const g = raw.group_by;
  if (typeof g === "string" && g !== "day" && (GROUP_BY as readonly string[]).includes(g))
    out.group_by = g as GroupBy;
  if (typeof raw.from === "string" && DATE.test(raw.from)) out.from = raw.from;
  if (typeof raw.to === "string" && DATE.test(raw.to)) out.to = raw.to;
  return out;
}

/** 兩個 YYYY-MM-DD 之間的天數（含兩端）；格式不對時是 NaN */
export function daysInclusive(from: string, to: string): number {
  if (!DATE.test(from) || !DATE.test(to)) return NaN;
  const a = Date.parse(`${from}T00:00:00Z`);
  const b = Date.parse(`${to}T00:00:00Z`);
  return Math.round((b - a) / 86_400_000) + 1;
}

/** COST-R14：查詢範圍最多 366 天 */
export const MAX_RANGE_DAYS = 366;
