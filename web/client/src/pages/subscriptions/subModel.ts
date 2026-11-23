// 訂閱表單的模型（SUB-R1、R3、R4、R9）：API 的 View ↔ 表單狀態 ↔ POST／PATCH／preview 的 body。
// 片長在表單用分鐘（API 是秒）；PATCH 只送改過的欄位（backfill 一送就會在下一次檢查重新評估，SUB-R3）。
import type { components } from "../../api/schema";

export type SubscriptionView = components["schemas"]["View"];
type CreateBody = components["schemas"]["CreateSubscriptionInputBody"];
type PatchBody = components["schemas"]["PatchSubscriptionInputBody"];
type Filters = components["schemas"]["SubscriptionFilters"];
type Backfill = components["schemas"]["SubscriptionBackfill"];

/** 檢查頻率的選項（SUB-R9：1h～168h；FormatSchedule 輸出最短寫法） */
export const SCHEDULES = ["1h", "3h", "6h", "12h", "24h", "72h", "168h"] as const;
export const BACKFILL_MODES = ["none", "recent", "all"] as const;
export const LIVE_MODES = ["wait", "skip"] as const;

export interface SubForm {
  url: string;
  /** 空字串＝預設處理方案 */
  profile: string;
  schedule: string;
  backfillMode: string;
  /** recent 的部數（文字，驗證交給後端與 countError） */
  backfillCount: string;
  /** 分鐘（文字；空字串＝不限） */
  minMinutes: string;
  maxMinutes: string;
  excludeShorts: boolean;
  live: string;
  titleInclude: string;
  titleExclude: string;
}

export const EMPTY_FORM: SubForm = {
  url: "",
  profile: "",
  schedule: "6h",
  backfillMode: "none",
  backfillCount: "10",
  minMinutes: "",
  maxMinutes: "",
  excludeShorts: true,
  live: "wait",
  titleInclude: "",
  titleExclude: "",
};

function minutes(s: number | undefined): string {
  return s == null ? "" : String(Math.round((s / 60) * 100) / 100);
}

export function formFromView(v: SubscriptionView): SubForm {
  return {
    url: v.url,
    profile: v.profile ?? "",
    schedule: v.schedule,
    backfillMode: v.backfill.mode,
    backfillCount: v.backfill.count != null ? String(v.backfill.count) : "10",
    minMinutes: minutes(v.filters.min_duration_s),
    maxMinutes: minutes(v.filters.max_duration_s),
    excludeShorts: v.filters.exclude_shorts,
    live: v.filters.live || "wait",
    titleInclude: v.filters.title_include ?? "",
    titleExclude: v.filters.title_exclude ?? "",
  };
}

/** 分鐘的文字 → 秒；空白是 undefined，不是數字時原樣回傳 NaN（交給前端提示） */
function seconds(text: string): number | undefined {
  const s = text.trim();
  if (s === "") return undefined;
  const n = Number(s);
  return Number.isFinite(n) ? Math.round(n * 60) : NaN;
}

export function filtersOf(f: SubForm): Filters {
  const out: Filters = { exclude_shorts: f.excludeShorts, live: f.live };
  const min = seconds(f.minMinutes);
  const max = seconds(f.maxMinutes);
  if (min !== undefined && !Number.isNaN(min)) out.min_duration_s = min;
  if (max !== undefined && !Number.isNaN(max)) out.max_duration_s = max;
  if (f.titleInclude.trim()) out.title_include = f.titleInclude.trim();
  if (f.titleExclude.trim()) out.title_exclude = f.titleExclude.trim();
  return out;
}

export function backfillOf(f: SubForm): Backfill {
  if (f.backfillMode !== "recent") return { mode: f.backfillMode };
  return { mode: "recent", count: Number(f.backfillCount) };
}

/** 前端先擋明顯的錯誤（數字欄位）；其他規則交給後端的 422 */
export function formProblems(f: SubForm): { minMinutes?: true; maxMinutes?: true; count?: true } {
  const out: { minMinutes?: true; maxMinutes?: true; count?: true } = {};
  const min = seconds(f.minMinutes);
  const max = seconds(f.maxMinutes);
  if (min !== undefined && (Number.isNaN(min) || min < 0)) out.minMinutes = true;
  if (max !== undefined && (Number.isNaN(max) || max <= 0)) out.maxMinutes = true;
  if (f.backfillMode === "recent") {
    const n = Number(f.backfillCount);
    if (!Number.isInteger(n) || n < 1 || n > 200) out.count = true;
  }
  return out;
}

export function createBody(f: SubForm): CreateBody {
  const body: CreateBody = {
    url: f.url.trim(),
    schedule: f.schedule,
    backfill: backfillOf(f),
    filters: filtersOf(f),
  };
  if (f.profile) body.profile = f.profile;
  return body;
}

/** 只送改過的欄位；沒有差異時是 undefined */
export function patchBody(before: SubForm, after: SubForm): PatchBody | undefined {
  const out: PatchBody = {};
  if (before.profile !== after.profile) out.profile = after.profile; // "" 改回預設處理方案
  if (before.schedule !== after.schedule) out.schedule = after.schedule;
  if (JSON.stringify(backfillOf(before)) !== JSON.stringify(backfillOf(after)))
    out.backfill = backfillOf(after);
  if (JSON.stringify(filtersOf(before)) !== JSON.stringify(filtersOf(after)))
    out.filters = filtersOf(after);
  return Object.keys(out).length ? out : undefined;
}

/** 看起來可以送預覽的網址（http／https）；其他格式交給後端判斷 */
export function previewableUrl(s: string): boolean {
  try {
    const u = new URL(s.trim());
    return (u.protocol === "https:" || u.protocol === "http:") && !!u.hostname;
  } catch {
    return false;
  }
}

/** 略過原因（SUB-R3、R4、R5 與內部的 details_unavailable、ingest_failed）；認不得的原樣顯示 */
export const SKIP_REASONS = [
  "tombstoned",
  "in_trash",
  "already_in_library",
  "live",
  "shorts",
  "too_short",
  "too_long",
  "title_excluded",
  "title_not_included",
  "before_subscription",
  "backfill_limit",
  "backfill_per_check",
  "details_unavailable",
  "ingest_failed",
] as const;
